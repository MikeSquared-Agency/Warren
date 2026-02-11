package policy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"warren/internal/container"
)

// ActivitySource provides last-activity timestamps per hostname.
type ActivitySource interface {
	Touch(hostname string)
	LastActivity(hostname string) time.Time
}

// WSSource provides active WebSocket connection counts per hostname.
type WSSource interface {
	Count(hostname string) int64
}

type OnDemandConfig struct {
	Agent              string
	ContainerName      string
	HealthURL          string
	Hostname           string
	CheckInterval      time.Duration
	StartupTimeout     time.Duration
	IdleTimeout        time.Duration
	MaxFailures        int
	MaxRestartAttempts int
}

type OnDemand struct {
	agent, containerName, healthURL, hostname string
	startupTimeout, idleTimeout, checkInterval time.Duration
	maxFailures, maxRestartAttempts             int

	manager  container.Lifecycle
	activity ActivitySource
	ws       WSSource

	mu     sync.RWMutex
	state  string        // "sleeping", "starting", "ready"
	wakeCh chan struct{} // buffered(1), signals wake request

	logger *slog.Logger
}

func NewOnDemand(mgr container.Lifecycle, cfg OnDemandConfig, activity ActivitySource, ws WSSource, logger *slog.Logger) *OnDemand {
	return &OnDemand{
		agent:              cfg.Agent,
		containerName:      cfg.ContainerName,
		healthURL:          cfg.HealthURL,
		hostname:           cfg.Hostname,
		startupTimeout:     cfg.StartupTimeout,
		idleTimeout:        cfg.IdleTimeout,
		checkInterval:      cfg.CheckInterval,
		maxFailures:        cfg.MaxFailures,
		maxRestartAttempts: cfg.MaxRestartAttempts,
		manager:            mgr,
		activity:           activity,
		ws:                 ws,
		state:              "sleeping", // will be resolved in Start
		wakeCh:             make(chan struct{}, 1),
		logger:             logger.With("agent", cfg.Agent, "policy", "on-demand"),
	}
}

func (o *OnDemand) Start(ctx context.Context) {
	// Determine initial state from container status.
	status, err := o.manager.Status(ctx, o.containerName)
	if err != nil {
		o.logger.Warn("failed to inspect container on startup, assuming sleeping", "error", err)
		o.setState("sleeping")
	} else if status == "running" {
		o.logger.Info("container already running on startup, verifying health")
		o.setState("starting")
	} else {
		o.logger.Info("container not running on startup", "status", status)
		o.setState("sleeping")
	}

	for {
		if ctx.Err() != nil {
			return
		}
		switch o.State() {
		case "sleeping":
			o.waitForWake(ctx)
		case "starting":
			o.waitForReady(ctx)
		case "ready":
			o.waitForIdle(ctx)
		case "degraded":
			// Stay degraded until context cancelled; Swarm handles recovery.
			<-ctx.Done()
			return
		}
	}
}

func (o *OnDemand) State() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.state
}

func (o *OnDemand) OnRequest() {
	if o.State() == "sleeping" {
		select {
		case o.wakeCh <- struct{}{}:
		default: // already waking
		}
	}
}

func (o *OnDemand) setState(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state != s {
		o.logger.Info("state transition", "from", o.state, "to", s)
	}
	o.state = s
}

// waitForWake blocks until a wake signal arrives, then starts the container.
func (o *OnDemand) waitForWake(ctx context.Context) {
	o.logger.Info("waiting for wake signal")
	select {
	case <-ctx.Done():
		return
	case <-o.wakeCh:
		o.logger.Info("wake signal received, starting container")
	}

	if err := o.manager.Start(ctx, o.containerName); err != nil {
		o.logger.Error("failed to start container", "error", err)
		// Stay sleeping — next wake request will retry.
		return
	}

	o.setState("starting")
}

// waitForReady polls health until the container is ready or startup times out.
func (o *OnDemand) waitForReady(ctx context.Context) {
	o.logger.Info("polling health, waiting for ready", "timeout", o.startupTimeout)
	deadline := time.After(o.startupTimeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			o.logger.Error("startup timeout exceeded, stopping container")
			o.stopContainer(ctx)
			o.setState("sleeping")
			return
		case <-ticker.C:
			if err := container.CheckHealth(ctx, o.healthURL); err == nil {
				o.logger.Info("health check passed, agent ready")
				o.setState("ready")
				// Touch activity so idle timer starts from now.
				o.activity.Touch(o.hostname)
				return
			}
		}
	}
}

// waitForIdle monitors health and idle timeout while the agent is ready.
func (o *OnDemand) waitForIdle(ctx context.Context) {
	o.logger.Info("agent ready, monitoring for idle", "idle_timeout", o.idleTimeout)

	idleTimer := time.NewTimer(o.idleTimeout)
	defer idleTimer.Stop()

	healthTicker := time.NewTicker(o.checkInterval)
	defer healthTicker.Stop()

	failures := 0

	for {
		select {
		case <-ctx.Done():
			return

		case <-healthTicker.C:
			if err := container.CheckHealth(ctx, o.healthURL); err != nil {
				failures++
				o.logger.Warn("health check failed while ready", "error", err, "consecutive_failures", failures)

				if failures >= o.maxFailures {
					o.logger.Warn("max failures reached, attempting restart")
					if o.attemptRestart(ctx) {
						o.setState("starting")
						return
					}
					// All restart attempts exhausted.
					o.setState("degraded")
					return
				}
			} else {
				if failures > 0 {
					o.logger.Info("health recovered", "previous_failures", failures)
				}
				failures = 0
			}

		case <-idleTimer.C:
			// Check if there are active WebSocket connections.
			if o.ws.Count(o.hostname) > 0 {
				o.logger.Info("idle timer fired but WebSocket connections active, resetting")
				idleTimer.Reset(o.idleTimeout)
				continue
			}

			// Check if there was recent activity.
			lastActivity := o.activity.LastActivity(o.hostname)
			if !lastActivity.IsZero() {
				elapsed := time.Since(lastActivity)
				if elapsed < o.idleTimeout {
					remaining := o.idleTimeout - elapsed
					o.logger.Info("idle timer fired but recent activity detected, resetting", "remaining", remaining)
					idleTimer.Reset(remaining)
					continue
				}
			}

			o.logger.Info("idle timeout reached, stopping container")
			o.stopContainer(ctx)
			o.setState("sleeping")
			return
		}
	}
}

// attemptRestart tries to restart the container, returning true on success.
func (o *OnDemand) attemptRestart(ctx context.Context) bool {
	for attempt := 1; attempt <= o.maxRestartAttempts; attempt++ {
		o.logger.Info("restarting container", "attempt", attempt, "max", o.maxRestartAttempts)
		if err := o.manager.Restart(ctx, o.containerName, 10*time.Second); err != nil {
			o.logger.Error("restart failed", "attempt", attempt, "error", err)
			continue
		}
		return true
	}
	o.logger.Error("all restart attempts exhausted")
	return false
}

func (o *OnDemand) stopContainer(ctx context.Context) {
	if err := o.manager.Stop(ctx, o.containerName, 10*time.Second); err != nil {
		o.logger.Error("failed to stop container", "error", err)
	}
}
