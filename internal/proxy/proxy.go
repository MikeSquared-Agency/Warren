package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"warren/internal/policy"
	"warren/internal/services"
)

type Backend struct {
	AgentName string
	Target    *url.URL
	Proxy     *httputil.ReverseProxy
	Policy    policy.Policy
}

type Proxy struct {
	backends map[string]*Backend // hostname → backend
	registry *services.Registry
	activity *ActivityTracker
	ws       *WSCounter
	logger   *slog.Logger
}

func New(registry *services.Registry, logger *slog.Logger) *Proxy {
	return &Proxy{
		backends: make(map[string]*Backend),
		registry: registry,
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

	// Reserve this hostname in the registry to prevent hijacking.
	p.registry.ReserveHostname(hostname)

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

	// Service API is NOT served on the public port — admin only.
	if strings.HasPrefix(r.URL.Path, "/api/services") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Check configured backends first.
	if backend, ok := p.backends[hostname]; ok {
		p.serveBackend(w, r, hostname, backend)
		return
	}

	// Fallback: check the dynamic service registry.
	if svc, ok := p.registry.Lookup(hostname); ok {
		p.serveDynamicService(w, r, hostname, svc)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (p *Proxy) serveBackend(w http.ResponseWriter, r *http.Request, hostname string, backend *Backend) {
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

func (p *Proxy) serveDynamicService(w http.ResponseWriter, r *http.Request, hostname string, svc *services.Service) {
	target, err := url.Parse(svc.Target)
	if err != nil {
		p.logger.Error("invalid dynamic service target", "hostname", hostname, "target", svc.Target, "error", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	p.activity.Touch(hostname)

	if IsWebSocket(r) {
		HandleWebSocket(w, r, target, hostname, p.ws, p.activity, p.logger)
		return
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		p.logger.Error("dynamic service proxy error", "hostname", hostname, "error", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

// HandleServiceAPI routes /api/services requests. Intended for admin mux only.
func (p *Proxy) HandleServiceAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/services":
		json.NewEncoder(w).Encode(p.registry.List())

	case r.Method == http.MethodPost && r.URL.Path == "/api/services":
		// Limit request body to 1MB to prevent memory exhaustion.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req struct {
			Hostname string `json:"hostname"`
			Target   string `json:"target"`
			Agent    string `json:"agent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if req.Hostname == "" || req.Target == "" {
			http.Error(w, `{"error":"hostname and target required"}`, http.StatusBadRequest)
			return
		}
		if err := p.registry.Register(req.Hostname, req.Target, req.Agent); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/services/"):
		hostname := strings.TrimPrefix(r.URL.Path, "/api/services/")
		if hostname == "" {
			http.Error(w, `{"error":"hostname required"}`, http.StatusBadRequest)
			return
		}
		p.registry.Deregister(hostname)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
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
