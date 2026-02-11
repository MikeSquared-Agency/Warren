package proxy

import (
	"encoding/json"
	"io"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"warren/internal/services"
)

type mockPolicy struct {
	state string
	woken bool
}

func (m *mockPolicy) Start(_ context.Context) {}
func (m *mockPolicy) State() string       { return m.state }
func (m *mockPolicy) OnRequest()          { m.woken = true }

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func setupProxy(t *testing.T, backends map[string]*mockBackendInfo) *Proxy {
	t.Helper()
	registry := services.NewRegistry(testLogger())
	p := New(registry, testLogger())
	for hostname, info := range backends {
		u, _ := url.Parse(info.server.URL)
		p.Register(hostname, info.agentName, u, info.policy)
	}
	return p
}

type mockBackendInfo struct {
	server    *httptest.Server
	agentName string
	policy    *mockPolicy
}

func TestHostnameRouting(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend-1"))
	}))
	defer s1.Close()
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend-2"))
	}))
	defer s2.Close()

	p := setupProxy(t, map[string]*mockBackendInfo{
		"a.example.com": {server: s1, agentName: "agent-a", policy: &mockPolicy{state: "ready"}},
		"b.example.com": {server: s2, agentName: "agent-b", policy: &mockPolicy{state: "ready"}},
	})

	// Request to a.example.com
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "a.example.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Result().Body)
	if string(body) != "backend-1" {
		t.Errorf("got %q, want backend-1", body)
	}

	// Request to b.example.com
	req = httptest.NewRequest("GET", "/", nil)
	req.Host = "b.example.com"
	w = httptest.NewRecorder()
	p.ServeHTTP(w, req)
	body, _ = io.ReadAll(w.Result().Body)
	if string(body) != "backend-2" {
		t.Errorf("got %q, want backend-2", body)
	}
}

func TestUnknownHostname404(t *testing.T) {
	p := setupProxy(t, map[string]*mockBackendInfo{})
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestSleepingReturns503(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	pol := &mockPolicy{state: "sleeping"}
	p := setupProxy(t, map[string]*mockBackendInfo{
		"a.com": {server: s, agentName: "a", policy: pol},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "a.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if w.Header().Get("Retry-After") != "3" {
		t.Error("missing Retry-After header")
	}
}

func TestStartingReturns503(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	p := setupProxy(t, map[string]*mockBackendInfo{
		"a.com": {server: s, agentName: "a", policy: &mockPolicy{state: "starting"}},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "a.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	p := setupProxy(t, map[string]*mockBackendInfo{
		"a.com": {server: s, agentName: "agent-a", policy: &mockPolicy{state: "ready"}},
	})

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "a.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp healthResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "ready" {
		t.Errorf("status = %q, want ready", resp.Status)
	}
}

func TestHealthEndpointDegraded(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	p := setupProxy(t, map[string]*mockBackendInfo{
		"a.com": {server: s, agentName: "a", policy: &mockPolicy{state: "degraded"}},
	})

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "a.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestWakeEndpoint(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	pol := &mockPolicy{state: "sleeping"}
	p := setupProxy(t, map[string]*mockBackendInfo{
		"a.com": {server: s, agentName: "a", policy: pol},
	})

	req := httptest.NewRequest("POST", "/api/wake", nil)
	req.Host = "a.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if !pol.woken {
		t.Error("expected OnRequest to be called")
	}
}

func TestServiceRegistryFallback(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dynamic"))
	}))
	defer backend.Close()

	registry := services.NewRegistry(testLogger())
	p := New(registry, testLogger())
	// Register directly (bypassing validation) since test backend is on localhost.
	registry.RegisterUnsafe("dynamic.com", backend.URL, "agent-x")

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "dynamic.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Result().Body)
	if string(body) != "dynamic" {
		t.Errorf("got %q, want dynamic", body)
	}
}

func TestServiceAPIRegisterAndList(t *testing.T) {
	registry := services.NewRegistry(testLogger())
	p := New(registry, testLogger())

	// Register via admin API handler (no longer on public port)
	body := strings.NewReader(`{"hostname":"x.com","target":"http://localhost:1234","agent":"a"}`)
	req := httptest.NewRequest("POST", "/api/services", body)
	w := httptest.NewRecorder()
	p.HandleServiceAPI(w, req)
	if w.Code != 201 {
		t.Errorf("register status = %d, want 201", w.Code)
	}

	// List via admin API handler
	req = httptest.NewRequest("GET", "/api/services", nil)
	w = httptest.NewRecorder()
	p.HandleServiceAPI(w, req)
	if w.Code != 200 {
		t.Errorf("list status = %d, want 200", w.Code)
	}
}

func TestServiceAPINotOnPublicPort(t *testing.T) {
	registry := services.NewRegistry(testLogger())
	p := New(registry, testLogger())

	req := httptest.NewRequest("GET", "/api/services", nil)
	req.Host = "any.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("public /api/services status = %d, want 404", w.Code)
	}
}
