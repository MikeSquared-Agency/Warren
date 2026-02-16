package tailer

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"warren/internal/store"
)

// mockStore captures UpsertUsage calls for testing.
type mockStore struct {
	mu      sync.Mutex
	upserts []*store.TokenUsage
	enrichs []enrichCall
}

type enrichCall struct {
	SessionID    string
	AgentID      string
	TaskID       string
	DurationMs   int64
	ExitCode     int
	FilesChanged []string
}

func (m *mockStore) UpsertUsage(_ context.Context, u *store.TokenUsage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *u
	m.upserts = append(m.upserts, &cp)
	return nil
}

func (m *mockStore) EnrichSession(_ context.Context, sessionID, agentID, taskID string, durationMs int64, exitCode int, filesChanged []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enrichs = append(m.enrichs, enrichCall{sessionID, agentID, taskID, durationMs, exitCode, filesChanged})
	return nil
}

func (m *mockStore) GetSummary(_ context.Context, _ time.Time) (*store.UsageSummary, error) {
	return nil, nil
}
func (m *mockStore) GetAgentUsage(_ context.Context, _ string, _ time.Time) (*store.AgentUsageSummary, error) {
	return nil, nil
}
func (m *mockStore) GetModelUsage(_ context.Context, _ string, _ time.Time) (*store.ModelUsageSummary, error) {
	return nil, nil
}
func (m *mockStore) GetCostEfficiency(_ context.Context, _ string) (*store.CostEfficiency, error) {
	return nil, nil
}
func (m *mockStore) Close() {}

func TestParseSessionKey(t *testing.T) {
	tests := []struct {
		key           string
		wantAgent     string
		wantLabel     string
	}{
		{"agent:main:main", "main", "main"},
		{"agent:dredd:judge", "dredd", "judge"},
		{"agent:dispatch:", "dispatch", ""},
		{"unknown", "unknown", ""},
		{"agent:solo", "solo", ""},
		{"", "unknown", ""},
	}
	for _, tt := range tests {
		agent, label := parseSessionKey(tt.key)
		if agent != tt.wantAgent {
			t.Errorf("parseSessionKey(%q) agent = %q, want %q", tt.key, agent, tt.wantAgent)
		}
		if label != tt.wantLabel {
			t.Errorf("parseSessionKey(%q) label = %q, want %q", tt.key, label, tt.wantLabel)
		}
	}
}

func TestProcessEntry(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, "/dev/null", 30*time.Second, 5*time.Second, logger)

	entry := &jsonlEntry{
		RunID:     "run-1",
		SessionID: "sess-abc",
		SessionKey: "agent:main:main",
		Provider:  "anthropic",
		ModelID:   "claude-opus-4-6",
		Timestamp: "2026-02-15T22:16:37.523Z",
		Stage:     "usage",
		Usage: &usageData{
			Input:       10,
			Output:      200,
			CacheRead:   5000,
			CacheWrite:  300,
			TotalTokens: 5510,
			Cost: costData{
				Input:      0.00005,
				Output:     0.005,
				CacheRead:  0.025,
				CacheWrite: 0.002,
				Total:      0.032,
			},
		},
	}

	tl.processEntry(entry)

	tl.mu.Lock()
	acc, ok := tl.sessions["sess-abc"]
	tl.mu.Unlock()

	if !ok {
		t.Fatal("expected session to be tracked")
	}
	if acc.agentID != "main" {
		t.Errorf("agent_id = %q, want main", acc.agentID)
	}
	if acc.totalTokens != 5510 {
		t.Errorf("total_tokens = %d, want 5510", acc.totalTokens)
	}
	if acc.requestCount != 1 {
		t.Errorf("request_count = %d, want 1", acc.requestCount)
	}
	if !acc.dirty {
		t.Error("expected accumulator to be dirty")
	}
}

func TestProcessMultipleEntries(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, "/dev/null", 30*time.Second, 5*time.Second, logger)

	for i := 0; i < 3; i++ {
		tl.processEntry(&jsonlEntry{
			SessionID:  "sess-multi",
			SessionKey: "agent:test:label",
			Provider:   "anthropic",
			ModelID:    "claude-opus-4-6",
			Timestamp:  "2026-02-15T22:16:37.523Z",
			Stage:      "usage",
			Usage: &usageData{
				Input:       10,
				Output:      100,
				TotalTokens: 110,
				Cost:        costData{Total: 0.01},
			},
		})
	}

	tl.mu.Lock()
	acc := tl.sessions["sess-multi"]
	tl.mu.Unlock()

	if acc.requestCount != 3 {
		t.Errorf("request_count = %d, want 3", acc.requestCount)
	}
	if acc.totalTokens != 330 {
		t.Errorf("total_tokens = %d, want 330", acc.totalTokens)
	}
	if acc.totalCostUSD != 0.03 {
		t.Errorf("total_cost = %f, want 0.03", acc.totalCostUSD)
	}
}

func TestFlush(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, "/dev/null", 30*time.Second, 5*time.Second, logger)

	tl.processEntry(&jsonlEntry{
		SessionID:  "sess-flush",
		SessionKey: "agent:main:main",
		Provider:   "anthropic",
		ModelID:    "claude-opus-4-6",
		Timestamp:  "2026-02-15T22:16:37.523Z",
		Stage:      "usage",
		Usage: &usageData{
			TotalTokens: 1000,
			Cost:        costData{Total: 0.05},
		},
	})

	tl.flush(context.Background())

	ms.mu.Lock()
	if len(ms.upserts) != 1 {
		ms.mu.Unlock()
		t.Fatalf("expected 1 upsert, got %d", len(ms.upserts))
	}
	u := ms.upserts[0]
	if u.SessionID != "sess-flush" {
		t.Errorf("session_id = %q, want sess-flush", u.SessionID)
	}
	if u.TotalTokens != 1000 {
		t.Errorf("total_tokens = %d, want 1000", u.TotalTokens)
	}
	ms.mu.Unlock()

	// Second flush should be a no-op (not dirty).
	tl.flush(context.Background())

	ms.mu.Lock()
	count := len(ms.upserts)
	ms.mu.Unlock()
	if count != 1 {
		t.Errorf("expected still 1 upsert after clean flush, got %d", count)
	}
}

func TestReadNewEntries(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "test.jsonl")

	// Write some entries.
	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}

	entries := []jsonlEntry{
		{
			SessionID:  "sess-read",
			SessionKey: "agent:main:main",
			Provider:   "anthropic",
			ModelID:    "claude-opus-4-6",
			Timestamp:  "2026-02-15T22:16:37.523Z",
			Stage:      "request", // should be skipped
		},
		{
			SessionID:  "sess-read",
			SessionKey: "agent:main:main",
			Provider:   "anthropic",
			ModelID:    "claude-opus-4-6",
			Timestamp:  "2026-02-15T22:16:37.523Z",
			Stage:      "usage",
			Usage: &usageData{
				TotalTokens: 500,
				Cost:        costData{Total: 0.025},
			},
		},
	}
	enc := json.NewEncoder(f)
	for _, e := range entries {
		enc.Encode(e)
	}
	f.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, jsonlPath, 30*time.Second, 5*time.Second, logger)

	tl.readNewEntries()

	tl.mu.Lock()
	acc, ok := tl.sessions["sess-read"]
	tl.mu.Unlock()

	if !ok {
		t.Fatal("expected session to be tracked after readNewEntries")
	}
	if acc.totalTokens != 500 {
		t.Errorf("total_tokens = %d, want 500", acc.totalTokens)
	}
	if acc.requestCount != 1 {
		t.Errorf("request_count = %d, want 1 (request stage should be skipped)", acc.requestCount)
	}
}

func TestOffsetPersistence(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "test.jsonl")
	offsetPath := filepath.Join(dir, ".tailer-offset")

	// Write an entry.
	f, _ := os.Create(jsonlPath)
	entry := jsonlEntry{
		SessionID:  "sess-offset",
		SessionKey: "agent:main:main",
		Provider:   "anthropic",
		ModelID:    "claude-opus-4-6",
		Timestamp:  "2026-02-15T22:16:37.523Z",
		Stage:      "usage",
		Usage:      &usageData{TotalTokens: 100, Cost: costData{Total: 0.01}},
	}
	json.NewEncoder(f).Encode(entry)
	f.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, jsonlPath, 30*time.Second, 5*time.Second, logger)
	tl.offsetPath = offsetPath

	tl.readNewEntries()

	// Verify offset was saved.
	if _, err := os.Stat(offsetPath); os.IsNotExist(err) {
		t.Fatal("expected offset file to be created")
	}

	// Create new tailer — should resume from saved offset.
	tl2 := New(ms, jsonlPath, 30*time.Second, 5*time.Second, logger)
	tl2.offsetPath = offsetPath
	tl2.loadOffset()

	if tl2.offset != tl.offset {
		t.Errorf("loaded offset %d != saved offset %d", tl2.offset, tl.offset)
	}

	// Reading again should produce nothing new.
	tl2.readNewEntries()
	tl2.mu.Lock()
	count := len(tl2.sessions)
	tl2.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 new sessions on re-read, got %d", count)
	}
}

func TestFileTruncationDetection(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "test.jsonl")

	// Write a long entry to set a high offset.
	f, _ := os.Create(jsonlPath)
	entry := jsonlEntry{
		SessionID:  "sess-trunc",
		SessionKey: "agent:main:main",
		Provider:   "anthropic",
		ModelID:    "claude-opus-4-6",
		Timestamp:  "2026-02-15T22:16:37.523Z",
		Stage:      "usage",
		Usage:      &usageData{TotalTokens: 100, Cost: costData{Total: 0.01}},
	}
	json.NewEncoder(f).Encode(entry)
	f.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, jsonlPath, 30*time.Second, 5*time.Second, logger)
	tl.readNewEntries()

	savedOffset := tl.offset
	if savedOffset == 0 {
		t.Fatal("expected non-zero offset after first read")
	}

	// Truncate the file (simulate rotation).
	os.WriteFile(jsonlPath, []byte{}, 0644)

	// Write a new, shorter entry.
	f, _ = os.OpenFile(jsonlPath, os.O_WRONLY, 0644)
	entry.SessionID = "sess-new"
	json.NewEncoder(f).Encode(entry)
	f.Close()

	tl.readNewEntries()

	tl.mu.Lock()
	_, hasNew := tl.sessions["sess-new"]
	tl.mu.Unlock()

	if !hasNew {
		t.Error("expected new session after file truncation")
	}
}

func TestMissingFileNoError(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ms := &mockStore{}
	tl := New(ms, "/nonexistent/path.jsonl", 30*time.Second, 5*time.Second, logger)

	// Should not panic.
	tl.readNewEntries()
}
