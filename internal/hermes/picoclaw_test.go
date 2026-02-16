package hermes

import (
	"encoding/json"
	"testing"
)

func TestSubjectAllTaskAssigned(t *testing.T) {
	if SubjectAllTaskAssigned != "swarm.task.*.assigned" {
		t.Errorf("SubjectAllTaskAssigned = %q, want swarm.task.*.assigned", SubjectAllTaskAssigned)
	}
}

func TestCCSessionCompletedDataExtendedFields(t *testing.T) {
	data := CCSessionCompletedData{
		SessionID:    "sess-picoclaw-123",
		TaskID:       "task-abc",
		AgentType:    "claude-code",
		ExitCode:     0,
		DurationMs:   120000,
		Model:        "claude-sonnet-4-5-20250929",
		Runtime:      "picoclaw",
		InputTokens:  5000,
		OutputTokens: 2000,
		CacheReadTokens:    80000,
		CacheWriteTokens:   1000,
	}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got CCSessionCompletedData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("model = %q", got.Model)
	}
	if got.Runtime != "picoclaw" {
		t.Errorf("runtime = %q", got.Runtime)
	}
	if got.InputTokens != 5000 {
		t.Errorf("input_tokens = %d, want 5000", got.InputTokens)
	}
	if got.OutputTokens != 2000 {
		t.Errorf("output_tokens = %d, want 2000", got.OutputTokens)
	}
	if got.CacheReadTokens != 80000 {
		t.Errorf("cache_read_tokens = %d, want 80000", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 1000 {
		t.Errorf("cache_write_tokens = %d, want 1000", got.CacheWriteTokens)
	}
}

func TestCCSessionCompletedDataBackwardsCompat(t *testing.T) {
	// Old-format JSON without extended fields should still unmarshal fine.
	oldJSON := `{
		"session_id": "sess-old",
		"agent_type": "claude-code",
		"exit_code": 0,
		"duration_ms": 60000,
		"working_dir": "/home/mike",
		"timestamp": "2026-02-15T10:00:00Z"
	}`

	var got CCSessionCompletedData
	if err := json.Unmarshal([]byte(oldJSON), &got); err != nil {
		t.Fatalf("unmarshal old format: %v", err)
	}

	if got.SessionID != "sess-old" {
		t.Errorf("session_id = %q", got.SessionID)
	}
	// Extended fields should be zero-valued.
	if got.Model != "" {
		t.Errorf("expected empty model, got %q", got.Model)
	}
	if got.Runtime != "" {
		t.Errorf("expected empty runtime, got %q", got.Runtime)
	}
	if got.InputTokens != 0 {
		t.Errorf("expected 0 input_tokens, got %d", got.InputTokens)
	}
}

func TestCCSessionCompletedDataOmitsEmptyExtended(t *testing.T) {
	// When extended fields are zero, they should be omitted from JSON.
	data := CCSessionCompletedData{
		SessionID: "sess-minimal",
		AgentType: "claude-code",
		ExitCode:  0,
	}

	raw, _ := json.Marshal(data)
	var obj map[string]interface{}
	json.Unmarshal(raw, &obj)

	for _, field := range []string{"model", "runtime", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens"} {
		if _, ok := obj[field]; ok {
			t.Errorf("expected %q to be omitted when zero", field)
		}
	}
}
