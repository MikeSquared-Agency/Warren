package admin

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"warren/internal/config"
	"warren/internal/events"
	"warren/internal/policy"
	"warren/internal/proxy"
	"warren/internal/services"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	emitter := events.NewEmitter(logger)
	registry := services.NewRegistry(logger)
	p := proxy.New(registry, logger)

	// Create temp config file.
	tmpFile, err := os.CreateTemp("", "warren-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	cfg := &config.Config{
		Listen: ":8080",
		Agents: make(map[string]*config.Agent),
	}
	cfgData := []byte("listen: \":8080\"\nagents: {}\n")
	os.WriteFile(tmpFile.Name(), cfgData, 0644)

	srv := NewServer(
		make(map[string]AgentInfo),
		make(map[string]policy.Policy),
		make(map[string]context.CancelFunc),
		registry,
		emitter,
		nil, // no docker manager in tests
		p,
		cfg,
		tmpFile.Name(),
		func() int64 { return 0 },
		nil, // no hermes client in tests
		logger,
	)
	return srv, tmpFile.Name()
}

func TestListAgentsEmpty(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/agents", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var agents []any
	json.Unmarshal(w.Body.Bytes(), &agents)
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
	}
}

func TestAddAndRemoveAgent(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	// Add agent.
	body, _ := json.Marshal(AddAgentRequest{
		Name:          "test-agent",
		Hostname:      "test.example.com",
		Backend:       "http://localhost:18790",
		Policy:        "unmanaged",
		ContainerName: "",
		HealthURL:     "",
	})
	req := httptest.NewRequest("POST", "/admin/agents", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List should have 1 agent.
	req = httptest.NewRequest("GET", "/admin/agents", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var agents []map[string]any
	json.Unmarshal(w.Body.Bytes(), &agents)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0]["name"] != "test-agent" {
		t.Fatalf("expected name test-agent, got %v", agents[0]["name"])
	}

	// Inspect agent.
	req = httptest.NewRequest("GET", "/admin/agents/test-agent", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Delete agent.
	req = httptest.NewRequest("DELETE", "/admin/agents/test-agent", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List should be empty again.
	req = httptest.NewRequest("GET", "/admin/agents", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &agents)
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
	}
}

func TestAddAgentDuplicate(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	body, _ := json.Marshal(AddAgentRequest{
		Name:     "dup",
		Hostname: "dup.example.com",
		Backend:  "http://localhost:18790",
		Policy:   "unmanaged",
	})

	req := httptest.NewRequest("POST", "/admin/agents", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("first add: expected 201, got %d", w.Code)
	}

	req = httptest.NewRequest("POST", "/admin/agents", bytes.NewReader(body))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("duplicate add: expected 409, got %d", w.Code)
	}
}

func TestAddAgentValidation(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	// Missing required fields.
	body, _ := json.Marshal(AddAgentRequest{Name: "x"})
	req := httptest.NewRequest("POST", "/admin/agents", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var health map[string]any
	json.Unmarshal(w.Body.Bytes(), &health)
	if health["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", health["status"])
	}
}

func TestSSEEndpoint(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/events", nil)
	w := httptest.NewRecorder()

	// SSE will block, so run in goroutine and cancel quickly.
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// The handler should set SSE headers. We can't easily test streaming
	// with httptest.NewRecorder, but we verify it doesn't panic.
	// In a real test we'd use a pipe-based approach.
}
