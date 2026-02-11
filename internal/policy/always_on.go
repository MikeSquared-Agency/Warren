package policy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"warren/internal/container"
	"warren/internal/events"
)

type AlwaysOn struct {
	agent     string
	healthURL string

	checkInterval time.Duration
	maxFailures   int

	mu       sync.RWMutex
	state    string
	failures int

	emitter *events.Emitter
	logger  *slog.Logger
}

type AlwaysOnConfig struct {
	Agent         string
	HealthURL     string
	CheckInterval time.Duration
	MaxFailures   int
}

func NewAlwaysOn(cfg AlwaysOnConfig, emitter *events.Emitter, logger *slog.Logger) *AlwaysOn {
	return &AlwaysOn{
		agent:         cfg.Agent,
		healthURL:     cfg.HealthURL,
		checkInterval: cfg.CheckInterval,
		maxFailures:   cfg.MaxFailures,
		state:         "starting",
		emitter:       emitter,
		logger:        logger.With("agent", cfg.Agent, "policy", "always-on"),
	}
}

func (a *AlwaysOn) Start(ctx context.Context) {
	a.emitter.Emit(events.Event{Type: events.AgentStarting, Agent: a.agent})

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

func (a *AlwaysOn) tick(ctx context.Context) {
	err := container.CheckHealth(ctx, a.healthURL)
	if err == nil {
		a.onHealthy()
		return
	}
	a.onUnhealthy(err)
}

func (a *AlwaysOn) onHealthy() {
	a.mu.Lock()
	defer a.mu.Unlock()

	prev := a.state
	a.state = "ready"
	a.failures = 0

	if prev != "ready" {
		a.logger.Info("agent became healthy", "state", "ready")
		a.emitter.Emit(events.Event{Type: events.AgentReady, Agent: a.agent})
	}
}

func (a *AlwaysOn) onUnhealthy(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.failures++
	a.logger.Warn("health check failed", "error", err, "consecutive_failures", a.failures)
	a.emitter.Emit(events.Event{Type: events.AgentHealthFailed, Agent: a.agent, Fields: map[string]string{"error": err.Error()}})

	if a.failures >= a.maxFailures {
		if a.state != "degraded" {
			a.logger.Error("agent degraded, max failures reached",
				"consecutive_failures", a.failures,
				"max_failures", a.maxFailures,
			)
			a.emitter.Emit(events.Event{Type: events.AgentDegraded, Agent: a.agent})
		}
		a.state = "degraded"
	}
}
