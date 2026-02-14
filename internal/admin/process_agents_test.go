package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"warren/internal/config"
	"warren/internal/events"
	"warren/internal/policy"
	"warren/internal/process"
	"warren/internal/proxy"
	"warren/internal/services"
)

func testServerWithTracker(t *testing.T, tracker *process.Tracker) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	registry := services.NewRegistry(logger)
	p := proxy.New(registry, logger)

	tmpFile, err := os.CreateTemp("", "warren-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	cfg := &config.Config{
		Listen: ":8080",
		Agents: make(map[string]*config.Agent),
	}
	os.WriteFile(tmpFile.Name(), []byte("listen: \":8080\"\nagents: {}\n"), 0644)

	return NewServer(
		make(map[string]AgentInfo),
		make(map[string]policy.Policy),
		make(map[string]context.CancelFunc),
		registry,
		emitter,
		nil,
		p,
		cfg,
		tmpFile.Name(),
		func() int64 { return 0 },
		nil,
		tracker,
		logger,
	)
}

func TestListAgentsWithProcessAgents(t *testing.T) {
	tracker := process.NewTracker()
	tracker.Register(&process.ProcessAgent{
		Name:      "cc-worker-abc123",
		Type:      "process",
		Runtime:   "claude-code",
		TaskID:    "task-abc123",
		SessionID: "sess-abc123",
		Status:    "running",
		StartedAt: time.Now(),
	})

	srv := testServerWithTracker(t, tracker)
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/agents", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var agents []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &agents); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent (process), got %d", len(agents))
	}

	agent := agents[0]
	if agent["type"] != "process" {
		t.Errorf("type = %v, want process", agent["type"])
	}
	if agent["name"] != "cc-worker-abc123" {
		t.Errorf("name = %v, want cc-worker-abc123", agent["name"])
	}
	if agent["runtime"] != "claude-code" {
		t.Errorf("runtime = %v, want claude-code", agent["runtime"])
	}
	if agent["task_id"] != "task-abc123" {
		t.Errorf("task_id = %v, want task-abc123", agent["task_id"])
	}
	if agent["state"] != "running" {
		t.Errorf("state = %v, want running", agent["state"])
	}
	if agent["session_id"] != "sess-abc123" {
		t.Errorf("session_id = %v, want sess-abc123", agent["session_id"])
	}
}

func TestListAgentsMixedTypes(t *testing.T) {
	tracker := process.NewTracker()
	tracker.Register(&process.ProcessAgent{
		Name:      "cc-adhoc",
		Type:      "process",
		Runtime:   "claude-code",
		SessionID: "sess-adhoc",
		Status:    "done",
		StartedAt: time.Now(),
	})

	srv := testServerWithTracker(t, tracker)

	// Also add a container agent.
	srv.agents["friend"] = AgentInfo{
		Name:     "friend",
		Hostname: "friend.example.com",
		Policy:   "always-on",
		Backend:  "http://backend:18790",
	}
	srv.policies["friend"] = policy.NewUnmanaged()

	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/agents", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var agents []map[string]any
	json.Unmarshal(w.Body.Bytes(), &agents)

	if len(agents) != 2 {
		t.Fatalf("expected 2 agents (1 container + 1 process), got %d", len(agents))
	}

	types := map[string]bool{}
	for _, a := range agents {
		if typ, ok := a["type"].(string); ok {
			types[typ] = true
		}
	}
	if !types["container"] {
		t.Error("expected container type in response")
	}
	if !types["process"] {
		t.Error("expected process type in response")
	}
}

func TestListAgentsContainerTypeField(t *testing.T) {
	// Verify container agents get type="container".
	srv, _ := testServer(t)

	srv.mu.Lock()
	srv.agents["test-container"] = AgentInfo{
		Name:     "test-container",
		Hostname: "tc.example.com",
		Policy:   "unmanaged",
		Backend:  "http://backend:18790",
	}
	srv.policies["test-container"] = policy.NewUnmanaged()
	srv.mu.Unlock()

	handler := srv.Handler()
	req := httptest.NewRequest("GET", "/admin/agents", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var agents []map[string]any
	json.Unmarshal(w.Body.Bytes(), &agents)

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0]["type"] != "container" {
		t.Errorf("container agent type = %v, want container", agents[0]["type"])
	}
}

func TestListAgentsNilTracker(t *testing.T) {
	// Ensure nil tracker doesn't cause panic.
	srv := testServerWithTracker(t, nil)
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/agents", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
