package hermes

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewEvent(t *testing.T) {
	data := AgentLifecycleData{Agent: "test-agent", Reason: "manual"}
	ev, err := NewEvent("agent.started", "warren", data)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	if ev.ID == "" {
		t.Error("expected non-empty ID")
	}
	if ev.Type != "agent.started" {
		t.Errorf("expected type agent.started, got %s", ev.Type)
	}
	if ev.Source != "warren" {
		t.Errorf("expected source warren, got %s", ev.Source)
	}
	if ev.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if ev.CorrelationID != "" {
		t.Error("expected empty correlation_id")
	}

	var payload AgentLifecycleData
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if payload.Agent != "test-agent" {
		t.Errorf("expected agent test-agent, got %s", payload.Agent)
	}
}

func TestEventMarshalRoundtrip(t *testing.T) {
	ev, _ := NewEvent("agent.ready", "warren", map[string]string{"key": "value"})
	ev = ev.WithCorrelation("corr-123", "cause-456")

	data, err := ev.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := UnmarshalEvent(data)
	if err != nil {
		t.Fatalf("UnmarshalEvent: %v", err)
	}

	if got.ID != ev.ID {
		t.Errorf("ID mismatch: %s != %s", got.ID, ev.ID)
	}
	if got.Type != ev.Type {
		t.Errorf("Type mismatch")
	}
	if got.CorrelationID != "corr-123" {
		t.Errorf("CorrelationID mismatch: %s", got.CorrelationID)
	}
	if got.CausationID != "cause-456" {
		t.Errorf("CausationID mismatch: %s", got.CausationID)
	}
	if got.Timestamp.Sub(ev.Timestamp).Abs() > time.Millisecond {
		t.Errorf("Timestamp mismatch")
	}
}

func TestWithCorrelation(t *testing.T) {
	ev, _ := NewEvent("test", "src", nil)
	ev2 := ev.WithCorrelation("a", "b")

	// Original unchanged.
	if ev.CorrelationID != "" {
		t.Error("original should be unchanged")
	}
	if ev2.CorrelationID != "a" || ev2.CausationID != "b" {
		t.Error("copy should have correlation IDs")
	}
}
