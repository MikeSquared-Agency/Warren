package events

import (
	"log/slog"
	"os"
	"testing"
)

func testEmitter() *Emitter {
	return NewEmitter(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestEmitCallsAllHandlers(t *testing.T) {
	e := testEmitter()
	var calls [2]int
	e.OnEvent(func(Event) { calls[0]++ })
	e.OnEvent(func(Event) { calls[1]++ })
	e.Emit(Event{Type: "test", Agent: "a"})
	if calls[0] != 1 || calls[1] != 1 {
		t.Errorf("expected both handlers called once, got %v", calls)
	}
}

func TestEmitCorrectFields(t *testing.T) {
	e := testEmitter()
	var got Event
	e.OnEvent(func(ev Event) { got = ev })
	e.Emit(Event{Type: AgentReady, Agent: "myagent", Fields: map[string]string{"k": "v"}})
	if got.Type != AgentReady || got.Agent != "myagent" {
		t.Errorf("unexpected event: %+v", got)
	}
	if got.Fields["k"] != "v" {
		t.Errorf("fields mismatch: %v", got.Fields)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestEmitNoHandlersNoPanic(t *testing.T) {
	e := testEmitter()
	e.Emit(Event{Type: "test"}) // should not panic
}
