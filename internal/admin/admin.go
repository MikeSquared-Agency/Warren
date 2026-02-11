package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"warren/internal/container"
	"warren/internal/events"
	"warren/internal/policy"
	"warren/internal/services"
)

// AgentInfo describes a configured agent.
type AgentInfo struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Policy   string `json:"policy"`
	Backend  string `json:"backend"`
}

// Server is the admin API server.
type Server struct {
	mu       sync.RWMutex
	agents   map[string]AgentInfo
	policies map[string]policy.Policy
	registry *services.Registry
	events   *events.Emitter
	manager  *container.Manager
	logger   *slog.Logger
	startAt  time.Time
	wsTotal  func() int64
}

// NewServer creates a new admin server.
func NewServer(
	agents map[string]AgentInfo,
	policies map[string]policy.Policy,
	registry *services.Registry,
	emitter *events.Emitter,
	manager *container.Manager,
	wsTotal func() int64,
	logger *slog.Logger,
) *Server {
	return &Server{
		agents:   agents,
		policies: policies,
		registry: registry,
		events:   emitter,
		manager:  manager,
		wsTotal:  wsTotal,
		logger:   logger.With("component", "admin"),
		startAt:  time.Now(),
	}
}

// Handler returns an http.Handler for the admin API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/agents", s.handleAgents)
	mux.HandleFunc("/admin/agents/", s.handleAgent)
	mux.HandleFunc("/admin/services", s.handleServices)
	mux.HandleFunc("/admin/health", s.handleHealth)
	return mux
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	type agentResp struct {
		AgentInfo
		State string `json:"state"`
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]agentResp, 0, len(s.agents))
	for name, info := range s.agents {
		state := "unknown"
		if pol, ok := s.policies[name]; ok {
			state = pol.State()
		}
		result = append(result, agentResp{AgentInfo: info, State: state})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	// Parse: /admin/agents/{name}[/action]
	path := strings.TrimPrefix(r.URL.Path, "/admin/agents/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if name == "" {
		http.Error(w, `{"error":"agent name required"}`, http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	info, ok := s.agents[name]
	pol := s.policies[name]
	s.mu.RUnlock()

	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && action == "":
		state := "unknown"
		if pol != nil {
			state = pol.State()
		}
		json.NewEncoder(w).Encode(map[string]string{
			"name":     info.Name,
			"hostname": info.Hostname,
			"policy":   info.Policy,
			"backend":  info.Backend,
			"state":    state,
		})

	case r.Method == http.MethodPost && action == "wake":
		od, ok := pol.(*policy.OnDemand)
		if !ok {
			http.Error(w, `{"error":"agent is not on-demand"}`, http.StatusBadRequest)
			return
		}
		od.Wake()
		json.NewEncoder(w).Encode(map[string]string{"status": "waking"})

	case r.Method == http.MethodPost && action == "sleep":
		od, ok := pol.(*policy.OnDemand)
		if !ok {
			http.Error(w, `{"error":"agent is not on-demand"}`, http.StatusBadRequest)
			return
		}
		od.Sleep(r.Context())
		json.NewEncoder(w).Encode(map[string]string{"status": "sleeping"})

	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.registry.List())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	agentCount := len(s.agents)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"uptime_seconds": time.Since(s.startAt).Seconds(),
		"agent_count":    agentCount,
		"ws_connections": s.wsTotal(),
	})
}

// ListenAndServe starts the admin server on the given address.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	s.logger.Info("admin server starting", "addr", addr)
	return srv.ListenAndServe()
}
