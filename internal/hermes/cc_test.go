package hermes

import (
	"encoding/json"
	"testing"
)

func TestCCSubjectConstants(t *testing.T) {
	if SubjectCCSessionCompleted != "swarm.cc.session.completed" {
		t.Errorf("SubjectCCSessionCompleted = %q", SubjectCCSessionCompleted)
	}
	if SubjectCCSessionFailed != "swarm.cc.session.failed" {
		t.Errorf("SubjectCCSessionFailed = %q", SubjectCCSessionFailed)
	}
	if SubjectAllCC != "swarm.cc.>" {
		t.Errorf("SubjectAllCC = %q", SubjectAllCC)
	}
}

func TestCCSessionCompletedDataMarshal(t *testing.T) {
	data := CCSessionCompletedData{
		SessionID:      "cfa3335c-ea38-4cf8-a1f2-9e3cf9789708",
		TaskID:         "task-123",
		OwnerUUID:      "person-456",
		AgentType:      "claude-code",
		TranscriptPath: "/home/mike/.claude/projects/-home-mike/cfa3335c.jsonl",
		FilesChanged:   []string{"internal/api/health.go", "main.go"},
		ExitCode:       0,
		DurationMs:     342000,
		WorkingDir:     "/home/mike/Warren",
		Timestamp:      "2026-02-14T10:06:00Z",
	}

	ev, err := NewEvent("cc.session.completed", "cc-sidecar", data)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	if ev.Type != "cc.session.completed" {
		t.Errorf("type = %q, want cc.session.completed", ev.Type)
	}
	if ev.Source != "cc-sidecar" {
		t.Errorf("source = %q, want cc-sidecar", ev.Source)
	}

	// Roundtrip.
	bytes, err := ev.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := UnmarshalEvent(bytes)
	if err != nil {
		t.Fatalf("UnmarshalEvent: %v", err)
	}

	var payload CCSessionCompletedData
	if err := json.Unmarshal(got.Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload.SessionID != "cfa3335c-ea38-4cf8-a1f2-9e3cf9789708" {
		t.Errorf("session_id = %q", payload.SessionID)
	}
	if payload.TaskID != "task-123" {
		t.Errorf("task_id = %q", payload.TaskID)
	}
	if payload.AgentType != "claude-code" {
		t.Errorf("agent_type = %q", payload.AgentType)
	}
	if len(payload.FilesChanged) != 2 {
		t.Errorf("files_changed count = %d, want 2", len(payload.FilesChanged))
	}
	if payload.DurationMs != 342000 {
		t.Errorf("duration_ms = %d, want 342000", payload.DurationMs)
	}
	if payload.WorkingDir != "/home/mike/Warren" {
		t.Errorf("working_dir = %q", payload.WorkingDir)
	}
}

func TestCCSessionCompletedDataOptionalFields(t *testing.T) {
	// Ad-hoc session: no task_id or owner_uuid.
	data := CCSessionCompletedData{
		SessionID: "abc-def",
		AgentType: "claude-code",
		ExitCode:  0,
	}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got CCSessionCompletedData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.TaskID != "" {
		t.Errorf("expected empty task_id for ad-hoc session, got %q", got.TaskID)
	}
	if got.OwnerUUID != "" {
		t.Errorf("expected empty owner_uuid for ad-hoc session, got %q", got.OwnerUUID)
	}
}

func TestStreamConfigsIncludeCCSessions(t *testing.T) {
	found := false
	for _, cfg := range StreamConfigs {
		if cfg.Name == "CC_SESSIONS" {
			found = true
			if len(cfg.Subjects) != 1 || cfg.Subjects[0] != "swarm.cc.>" {
				t.Errorf("CC_SESSIONS subjects = %v, want [swarm.cc.>]", cfg.Subjects)
			}
		}
	}
	if !found {
		t.Error("CC_SESSIONS stream not found in StreamConfigs")
	}
}

func TestKVBucketConfigsIncludeCCRegistry(t *testing.T) {
	found := false
	for _, cfg := range KVBucketConfigs {
		if cfg.Bucket == "CC_SESSION_REGISTRY" {
			found = true
		}
	}
	if !found {
		t.Error("CC_SESSION_REGISTRY not found in KVBucketConfigs")
	}
}
