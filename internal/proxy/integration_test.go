//go:build integration

package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"warren/internal/services"
)

type integPolicy struct {
	state string
}

func (p *integPolicy) Start(_ context.Context) {}
func (p *integPolicy) State() string       { return p.state }
func (p *integPolicy) OnRequest()          {}

func TestIntegrationFullProxySetup(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("agent-1"))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("agent-2"))
	}))
	defer backend2.Close()

	registry := services.NewRegistry(logger)
	p := New(registry, "", logger)

	u1, _ := url.Parse(backend1.URL)
	u2, _ := url.Parse(backend2.URL)

	p.Register("agent1.example.com", "agent-1", u1, &integPolicy{state: "ready"})
	p.Register("agent2.example.com", "agent-2", u2, &integPolicy{state: "ready"})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := proxyServer.Client()

	// Route to agent-1
	req, _ := http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "agent1.example.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "agent-1" {
		t.Errorf("got %q, want agent-1", body)
	}

	// Route to agent-2
	req, _ = http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "agent2.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "agent-2" {
		t.Errorf("got %q, want agent-2", body)
	}

	// Unknown host → 404
	req, _ = http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "unknown.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestIntegrationDynamicServiceRouting(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	dynamicBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dynamic-service"))
	}))
	defer dynamicBackend.Close()

	registry := services.NewRegistry(logger)
	p := New(registry, "", logger)
	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := proxyServer.Client()

	// Register dynamic service via API
	body := strings.NewReader(`{"hostname":"dyn.example.com","target":"` + dynamicBackend.URL + `","agent":"a"}`)
	req, _ := http.NewRequest("POST", proxyServer.URL+"/api/services", body)
	req.Host = "any.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("register status = %d", resp.StatusCode)
	}

	// Route to dynamic service
	req, _ = http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "dyn.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(respBody) != "dynamic-service" {
		t.Errorf("got %q, want dynamic-service", respBody)
	}
}

func TestIntegration503DuringStarting(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	registry := services.NewRegistry(logger)
	p := New(registry, "", logger)

	u, _ := url.Parse(backend.URL)
	pol := &integPolicy{state: "starting"}
	p.Register("starting.example.com", "agent-s", u, pol)

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := proxyServer.Client()

	// Starting → 503
	req, _ := http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "starting.example.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	// Switch to ready → 200
	pol.state = "ready"
	req, _ = http.NewRequest("GET", proxyServer.URL+"/", nil)
	req.Host = "starting.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Health endpoint
	req, _ = http.NewRequest("GET", proxyServer.URL+"/api/health", nil)
	req.Host = "starting.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var hr healthResponse
	json.NewDecoder(resp.Body).Decode(&hr)
	resp.Body.Close()
	if hr.Status != "ready" {
		t.Errorf("health status = %q, want ready", hr.Status)
	}
}
