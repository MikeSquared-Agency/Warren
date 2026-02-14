package process

import (
	"testing"
	"time"
)

func TestNewTracker(t *testing.T) {
	tracker := NewTracker()
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
	if len(tracker.List()) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(tracker.List()))
	}
}

func TestRegisterAndList(t *testing.T) {
	tracker := NewTracker()

	agent := &ProcessAgent{
		Name:      "cc-abc123",
		Type:      "process",
		Runtime:   "claude-code",
		SessionID: "abc-123-def-456",
		Status:    "running",
		StartedAt: time.Now(),
	}
	tracker.Register(agent)

	agents := tracker.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "cc-abc123" {
		t.Errorf("expected name cc-abc123, got %s", agents[0].Name)
	}
	if agents[0].Type != "process" {
		t.Errorf("expected type process, got %s", agents[0].Type)
	}
	if agents[0].Runtime != "claude-code" {
		t.Errorf("expected runtime claude-code, got %s", agents[0].Runtime)
	}
	if agents[0].Status != "running" {
		t.Errorf("expected status running, got %s", agents[0].Status)
	}
}

func TestUpdate(t *testing.T) {
	tracker := NewTracker()
	tracker.Register(&ProcessAgent{
		Name:      "cc-test",
		SessionID: "sess-1",
		Status:    "running",
	})

	exitCode := 0
	tracker.Update("sess-1", "done", &exitCode)

	agent, ok := tracker.Get("sess-1")
	if !ok {
		t.Fatal("expected agent to be found")
	}
	if agent.Status != "done" {
		t.Errorf("expected status done, got %s", agent.Status)
	}
	if agent.ExitCode == nil || *agent.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", agent.ExitCode)
	}
}

func TestUpdateNonExistent(t *testing.T) {
	tracker := NewTracker()
	// Should not panic.
	exitCode := 1
	tracker.Update("nonexistent", "failed", &exitCode)

	if len(tracker.List()) != 0 {
		t.Fatal("expected 0 agents after updating nonexistent")
	}
}

func TestGetNotFound(t *testing.T) {
	tracker := NewTracker()
	_, ok := tracker.Get("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestListReturnsCopy(t *testing.T) {
	tracker := NewTracker()
	tracker.Register(&ProcessAgent{
		Name:      "cc-orig",
		SessionID: "sess-copy",
		Status:    "running",
	})

	agents := tracker.List()
	agents[0].Status = "mutated"

	// Original should be unchanged.
	orig, _ := tracker.Get("sess-copy")
	if orig.Status != "running" {
		t.Errorf("expected original status running, got %s", orig.Status)
	}
}

func TestMultipleAgents(t *testing.T) {
	tracker := NewTracker()

	for i := 0; i < 5; i++ {
		tracker.Register(&ProcessAgent{
			Name:      "cc-agent",
			SessionID: "sess-" + string(rune('a'+i)),
			Status:    "running",
		})
	}

	if len(tracker.List()) != 5 {
		t.Fatalf("expected 5 agents, got %d", len(tracker.List()))
	}
}

func TestRegisterWithTaskID(t *testing.T) {
	tracker := NewTracker()
	tracker.Register(&ProcessAgent{
		Name:      "cc-worker-task1",
		Type:      "process",
		Runtime:   "claude-code",
		TaskID:    "task-abc-123",
		OwnerUUID: "person-uuid-456",
		SessionID: "sess-with-task",
		Status:    "running",
	})

	agent, ok := tracker.Get("sess-with-task")
	if !ok {
		t.Fatal("expected agent to be found")
	}
	if agent.TaskID != "task-abc-123" {
		t.Errorf("expected task_id task-abc-123, got %s", agent.TaskID)
	}
	if agent.OwnerUUID != "person-uuid-456" {
		t.Errorf("expected owner_uuid person-uuid-456, got %s", agent.OwnerUUID)
	}
}
