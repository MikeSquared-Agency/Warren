package process

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"warren/internal/config"
	"warren/internal/hermes"
)

func TestTruncateID(t *testing.T) {
	tests := []struct {
		id   string
		n    int
		want string
	}{
		{"abcdef12-3456-7890", 8, "abcdef12"},
		{"short", 8, "short"},
		{"exactly8", 8, "exactly8"},
		{"", 8, ""},
		{"abc", 0, ""},
	}
	for _, tt := range tests {
		got := truncateID(tt.id, tt.n)
		if got != tt.want {
			t.Errorf("truncateID(%q, %d) = %q, want %q", tt.id, tt.n, got, tt.want)
		}
	}
}

func TestSpawnerFiltersPicoClawRuntime(t *testing.T) {
	// Verify that handleAssigned only processes picoclaw runtime tasks.
	// We test by checking that non-picoclaw tasks don't register in tracker.
	tracker := NewTracker()

	cfg := config.PicoClawConfig{
		Binary:         "picoclaw",
		MissionBaseDir: t.TempDir(),
		DefaultTimeout: 5 * time.Minute,
		MaxConcurrent:  20,
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create spawner without hermes (we'll call handleAssigned directly).
	s := &Spawner{
		tracker: tracker,
		cfg:     cfg,
		logger:  logger,
	}

	// Non-picoclaw task should be ignored.
	task := taskAssignment{
		ID:      "task-non-picoclaw",
		Title:   "Do something",
		Runtime: "docker",
	}
	data, _ := json.Marshal(task)
	ev := hermes.Event{
		Data: data,
	}

	s.handleAssigned(ev)

	if len(tracker.List()) != 0 {
		t.Errorf("expected 0 agents for non-picoclaw task, got %d", len(tracker.List()))
	}
}

func TestSpawnerConcurrencyLimit(t *testing.T) {
	tracker := NewTracker()

	cfg := config.PicoClawConfig{
		Binary:         "picoclaw",
		MissionBaseDir: t.TempDir(),
		DefaultTimeout: 5 * time.Minute,
		MaxConcurrent:  2,
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	s := &Spawner{
		tracker: tracker,
		cfg:     cfg,
		running: 2, // already at limit
		logger:  logger,
	}

	// Picoclaw task should be dropped due to concurrency limit.
	task := taskAssignment{
		ID:      "task-overflow",
		Title:   "Overflow task",
		Runtime: "picoclaw",
	}
	data, _ := json.Marshal(task)
	ev := hermes.Event{
		Data: data,
	}

	s.handleAssigned(ev)

	// Should not have started a new worker.
	if len(tracker.List()) != 0 {
		t.Errorf("expected 0 agents when at concurrency limit, got %d", len(tracker.List()))
	}
}

func TestTaskAssignmentMarshal(t *testing.T) {
	task := taskAssignment{
		ID:               "task-123",
		Title:            "Fix bug",
		Description:      "Fix the authentication bug",
		Runtime:          "picoclaw",
		RecommendedModel: "claude-sonnet-4-5-20250929",
		AssignedAgent:    "worker-1",
		FilePatterns:     []string{"internal/auth/*.go"},
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got taskAssignment
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != "task-123" {
		t.Errorf("task_id = %q, want task-123", got.ID)
	}
	if got.Runtime != "picoclaw" {
		t.Errorf("runtime = %q, want picoclaw", got.Runtime)
	}
	if got.RecommendedModel != "claude-sonnet-4-5-20250929" {
		t.Errorf("recommended_model = %q", got.RecommendedModel)
	}
	if len(got.FilePatterns) != 1 {
		t.Errorf("file_patterns count = %d, want 1", len(got.FilePatterns))
	}
}

func TestTaskAssignmentOmitsEmpty(t *testing.T) {
	task := taskAssignment{
		ID:      "task-minimal",
		Title:   "Simple task",
		Runtime: "picoclaw",
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Optional fields should be omitted.
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["description"]; ok {
		t.Error("expected description to be omitted")
	}
	if _, ok := raw["recommended_model"]; ok {
		t.Error("expected recommended_model to be omitted")
	}
	if _, ok := raw["file_patterns"]; ok {
		t.Error("expected file_patterns to be omitted")
	}
}
