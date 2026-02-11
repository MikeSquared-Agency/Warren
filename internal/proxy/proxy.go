package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"warren/internal/policy"
)

type Backend struct {
	AgentName string
	Target    *url.URL
	Proxy     *httputil.ReverseProxy
	Policy    policy.Policy
}

type Proxy struct {
	backends map[string]*Backend // hostname → backend
	activity *ActivityTracker
	ws       *WSCounter
	logger   *slog.Logger
}

func New(logger *slog.Logger) *Proxy {
	return &Proxy{
		backends: make(map[string]*Backend),
		activity: NewActivityTracker(),
		ws:       NewWSCounter(),
		logger:   logger,
	}
}

func (p *Proxy) Register(hostname, agentName string, target *url.URL, pol policy.Policy) {
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1 // streaming/SSE support

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		p.logger.Error("proxy error", "agent", agentName, "error", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}

	p.backends[hostname] = &Backend{
		AgentName: agentName,
		Target:    target,
		Proxy:     rp,
		Policy:    pol,
	}

	p.logger.Info("registered backend", "hostname", hostname, "agent", agentName, "target", target)
}

func (p *Proxy) Activity() *ActivityTracker {
	return p.activity
}

func (p *Proxy) WSCounter() *WSCounter {
	return p.ws
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hostname := stripPort(r.Host)

	backend, ok := p.backends[hostname]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Health endpoint — return agent status.
	if r.URL.Path == "/api/health" && r.Method == http.MethodGet {
		p.handleHealth(w, backend)
		return
	}

	// Wake endpoint — trigger on-demand start.
	if r.URL.Path == "/api/wake" && r.Method == http.MethodPost {
		backend.Policy.OnRequest()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	backend.Policy.OnRequest()
	p.activity.Touch(hostname)

	// If the backend is sleeping or starting, return 503 instead of forwarding.
	state := backend.Policy.State()
	if state == "sleeping" || state == "starting" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(healthResponse{Status: state, Agent: backend.AgentName})
		return
	}

	// WebSocket passthrough.
	if IsWebSocket(r) {
		HandleWebSocket(w, r, backend.Target, hostname, p.ws, p.activity, p.logger)
		return
	}

	backend.Proxy.ServeHTTP(w, r)
}

type healthResponse struct {
	Status string `json:"status"`
	Agent  string `json:"agent"`
}

func (p *Proxy) handleHealth(w http.ResponseWriter, b *Backend) {
	state := b.Policy.State()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	status := http.StatusOK
	if state != "ready" {
		status = http.StatusServiceUnavailable
	}

	w.WriteHeader(status)
	json.NewEncoder(w).Encode(healthResponse{
		Status: state,
		Agent:  b.AgentName,
	})
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i != -1 {
		return host[:i]
	}
	return host
}
