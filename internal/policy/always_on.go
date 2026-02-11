package policy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"openclaw-orchestrator/internal/container"
)

type AlwaysOn struct {
	agent     string
	manager   *container.Manager
	name      string // container name
	healthURL string

	checkInterval      time.Duration
	maxFailures        int
	maxRestartAttempts int

	mu               sync.RWMutex
	state            string
	failures         int
	restartAttempts  int
	backoffUntil     time.Time

	logger *slog.Logger
}

type AlwaysOnConfig struct {
	Agent              string
	ContainerName      string
	HealthURL          string
	CheckInterval      time.Duration
	MaxFailures        int
	MaxRestartAttempts int
}

func NewAlwaysOn(manager *container.Manager, cfg AlwaysOnConfig, logger *slog.Logger) *AlwaysOn {
	return &AlwaysOn{
		agent:              cfg.Agent,
		manager:            manager,
		name:               cfg.ContainerName,
		healthURL:          cfg.HealthURL,
		checkInterval:      cfg.CheckInterval,
		maxFailures:        cfg.MaxFailures,
		maxRestartAttempts: cfg.MaxRestartAttempts,
		state:              "starting",
		logger:             logger.With("agent", cfg.Agent, "policy", "always-on"),
	}
}

func (a *AlwaysOn) Start(ctx context.Context) {
	// Ensure container is running on startup.
	a.ensureRunning(ctx)

	ticker := time.NewTicker(a.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *AlwaysOn) State() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *AlwaysOn) OnRequest() {}

func (a *AlwaysOn) ensureRunning(ctx context.Context) {
	status, err := a.manager.Status(ctx, a.name)
	if err != nil {
		a.logger.Error("failed to inspect container on startup", "error", err)
		a.setState("unhealthy")
		return
	}

	if status != "running" {
		a.logger.Info("container not running on startup, starting", "status", status)
		if err := a.manager.Start(ctx, a.name); err != nil {
			a.logger.Error("failed to start container on startup", "error", err)
			a.setState("unhealthy")
			return
		}
	}

	a.setState("starting")
}

func (a *AlwaysOn) tick(ctx context.Context) {
	a.mu.RLock()
	backoff := a.backoffUntil
	a.mu.RUnlock()

	if time.Now().Before(backoff) {
		return
	}

	err := container.CheckHealth(ctx, a.healthURL)
	if err == nil {
		a.onHealthy()
		return
	}

	a.onUnhealthy(ctx, err)
}

func (a *AlwaysOn) onHealthy() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.state != "running" {
		a.logger.Info("agent became healthy")
	}

	a.state = "running"
	a.failures = 0
	a.restartAttempts = 0
}

func (a *AlwaysOn) onUnhealthy(ctx context.Context, err error) {
	a.mu.Lock()
	a.failures++
	failures := a.failures
	a.mu.Unlock()

	a.logger.Warn("health check failed", "error", err, "consecutive_failures", failures)

	if failures < a.maxFailures {
		a.setState("unhealthy")
		return
	}

	// Threshold reached — attempt restart.
	a.mu.Lock()
	a.restartAttempts++
	attempts := a.restartAttempts
	maxAttempts := a.maxRestartAttempts
	a.failures = 0 // reset failure count for next cycle
	a.mu.Unlock()

	if attempts > maxAttempts {
		a.logger.Error("max restart attempts reached, agent degraded",
			"attempts", attempts,
			"max", maxAttempts,
		)
		a.setState("degraded")
		return
	}

	a.logger.Info("restarting container", "attempt", attempts, "max", maxAttempts)

	if err := a.manager.Restart(ctx, a.name, 10*time.Second); err != nil {
		a.logger.Error("restart failed", "error", err)
		backoff := a.calculateBackoff(attempts)
		a.mu.Lock()
		a.backoffUntil = time.Now().Add(backoff)
		a.mu.Unlock()
		a.logger.Info("backing off", "duration", backoff)
		a.setState("unhealthy")
		return
	}

	a.setState("starting")
}

func (a *AlwaysOn) setState(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
}

func (a *AlwaysOn) calculateBackoff(attempt int) time.Duration {
	base := time.Duration(1) << uint(attempt) * time.Second
	cap := 5 * time.Minute
	if base > cap {
		return cap
	}
	return base
}
