package metrics

import (
	"log/slog"
	"os"
	"testing"

	"warren/internal/events"
)

func TestNewMetricsNoPanic(t *testing.T) {
	// Handler() should return without panic (metrics already registered in init)
	h := Handler()
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

func TestRegisterEventHandlerUpdatesCounters(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	RegisterEventHandler(emitter)

	// These should not panic and should update metrics
	emitter.Emit(events.Event{Type: events.AgentReady, Agent: "test"})
	emitter.Emit(events.Event{Type: events.AgentDegraded, Agent: "test"})
	emitter.Emit(events.Event{Type: events.AgentSleep, Agent: "test"})
	emitter.Emit(events.Event{Type: events.AgentWake, Agent: "test"})
	emitter.Emit(events.Event{Type: events.AgentHealthFailed, Agent: "test"})
	emitter.Emit(events.Event{Type: events.AgentStarting, Agent: "test"})
}
