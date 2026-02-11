package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"warren/internal/config"
	"warren/internal/container"
	"warren/internal/events"
	"warren/internal/policy"
	"warren/internal/proxy"
	"warren/internal/services"
)

// AgentInfo describes a configured agent.
type AgentInfo struct {
	Name          string `json:"name"`
	Hostname      string `json:"hostname"`
	Policy        string `json:"policy"`
	Backend       string `json:"backend"`
	ContainerName string `json:"container_name,omitempty"`
	HealthURL     string `json:"health_url,omitempty"`
	IdleTimeout   string `json:"idle_timeout,omitempty"`
}

// AddAgentRequest is the JSON body for POST /admin/agents.
type AddAgentRequest struct {
	Name          string `json:"name"`
	Hostname      string `json:"hostname"`
	Backend       string `json:"backend"`
	Policy        string `json:"policy"`
	ContainerName string `json:"container_name"`
	HealthURL     string `json:"health_url"`
	IdleTimeout   string `json:"idle_timeout"`
}

// AgentManager is the interface for dynamically adding/removing agents.
type AgentManager interface {
	AddAgent(req AddAgentRequest) error
	RemoveAgent(name string) error
}

// Server is the admin API server.
type Server struct {
	mu       sync.RWMutex
	agents   map[string]AgentInfo
	policies map[string]policy.Policy
	cancels  map[string]context.CancelFunc
	registry *services.Registry
	events   *events.Emitter
	manager  *container.Manager
	prxy     *proxy.Proxy
	cfg      *config.Config
	cfgPath  string
	logger   *slog.Logger
	startAt  time.Time
	wsTotal  func() int64
}

// NewServer creates a new admin server.
func NewServer(
	agents map[string]AgentInfo,
	policies map[string]policy.Policy,
	cancels map[string]context.CancelFunc,
	registry *services.Registry,
	emitter *events.Emitter,
	manager *container.Manager,
	prxy *proxy.Proxy,
	cfg *config.Config,
	cfgPath string,
	wsTotal func() int64,
	logger *slog.Logger,
) *Server {
	return &Server{
		agents:   agents,
		policies: policies,
		cancels:  cancels,
		registry: registry,
		events:   emitter,
		manager:  manager,
		prxy:     prxy,
		cfg:      cfg,
		cfgPath:  cfgPath,
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
	mux.HandleFunc("/admin/events", s.handleSSE)
	return mux
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listAgents(w, r)
	case http.MethodPost:
		s.addAgent(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) listAgents(w http.ResponseWriter, _ *http.Request) {
	type agentResp struct {
		AgentInfo
		State       string `json:"state"`
		Connections int64  `json:"connections"`
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]agentResp, 0, len(s.agents))
	for name, info := range s.agents {
		state := "unknown"
		if pol, ok := s.policies[name]; ok {
			state = pol.State()
		}
		var conns int64
		if s.prxy != nil {
			conns = s.prxy.WSCounter().Count(info.Hostname)
		}
		result = append(result, agentResp{AgentInfo: info, State: state, Connections: conns})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) addAgent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req AddAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Hostname == "" || req.Backend == "" || req.Policy == "" {
		http.Error(w, `{"error":"name, hostname, backend, and policy are required"}`, http.StatusBadRequest)
		return
	}

	switch req.Policy {
	case "on-demand", "always-on", "unmanaged":
	default:
		http.Error(w, `{"error":"policy must be on-demand, always-on, or unmanaged"}`, http.StatusBadRequest)
		return
	}

	if (req.Policy == "on-demand" || req.Policy == "always-on") && req.ContainerName == "" {
		http.Error(w, `{"error":"container_name required for on-demand/always-on policy"}`, http.StatusBadRequest)
		return
	}

	if (req.Policy == "on-demand" || req.Policy == "always-on") && req.HealthURL == "" {
		http.Error(w, `{"error":"health_url required for on-demand/always-on policy"}`, http.StatusBadRequest)
		return
	}

	target, err := url.Parse(req.Backend)
	if err != nil {
		http.Error(w, `{"error":"invalid backend URL"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.agents[req.Name]; exists {
		http.Error(w, `{"error":"agent already exists"}`, http.StatusConflict)
		return
	}

	// Parse idle timeout.
	idleTimeout := 30 * time.Minute
	if req.IdleTimeout != "" {
		idleTimeout, err = time.ParseDuration(req.IdleTimeout)
		if err != nil {
			http.Error(w, `{"error":"invalid idle_timeout"}`, http.StatusBadRequest)
			return
		}
	}

	// Create policy.
	var pol policy.Policy
	ctx, cancel := context.WithCancel(context.Background())

	switch req.Policy {
	case "always-on":
		pol = policy.NewAlwaysOn(policy.AlwaysOnConfig{
			Agent:         req.Name,
			HealthURL:     req.HealthURL,
			CheckInterval: 30 * time.Second,
			MaxFailures:   3,
		}, s.events, s.logger)
	case "on-demand":
		pol = policy.NewOnDemand(s.manager, policy.OnDemandConfig{
			Agent:              req.Name,
			ContainerName:      req.ContainerName,
			HealthURL:          req.HealthURL,
			Hostname:           req.Hostname,
			CheckInterval:      30 * time.Second,
			StartupTimeout:     60 * time.Second,
			IdleTimeout:        idleTimeout,
			MaxFailures:        3,
			MaxRestartAttempts: 10,
		}, s.prxy.Activity(), s.prxy.WSCounter(), s.events, s.logger)
	case "unmanaged":
		pol = policy.NewUnmanaged()
	}

	// Register in proxy.
	s.prxy.Register(req.Hostname, req.Name, target, pol)

	// Start policy goroutine.
	go pol.Start(ctx)

	// Store in admin state.
	s.agents[req.Name] = AgentInfo{
		Name:          req.Name,
		Hostname:      req.Hostname,
		Policy:        req.Policy,
		Backend:       req.Backend,
		ContainerName: req.ContainerName,
		HealthURL:     req.HealthURL,
		IdleTimeout:   req.IdleTimeout,
	}
	s.policies[req.Name] = pol
	s.cancels[req.Name] = cancel

	// Persist to config.
	agent := &config.Agent{
		Hostname: req.Hostname,
		Backend:  req.Backend,
		Policy:   req.Policy,
		Container: config.Container{Name: req.ContainerName},
		Health: config.Health{
			URL:                req.HealthURL,
			CheckInterval:      30 * time.Second,
			StartupTimeout:     60 * time.Second,
			MaxFailures:        3,
			MaxRestartAttempts: 10,
		},
		Idle: config.IdleConfig{
			Timeout:      idleTimeout,
			DrainTimeout: 30 * time.Second,
		},
	}
	if s.cfg.Agents == nil {
		s.cfg.Agents = make(map[string]*config.Agent)
	}
	s.cfg.Agents[req.Name] = agent
	if err := config.Save(s.cfg, s.cfgPath); err != nil {
		s.logger.Error("failed to persist config after adding agent", "error", err)
	}

	s.events.Emit(events.Event{Type: events.AgentAdded, Agent: req.Name})
	s.logger.Info("agent added via API", "name", req.Name, "hostname", req.Hostname)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": req.Name})
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

	// DELETE /admin/agents/{name}
	if r.Method == http.MethodDelete && action == "" {
		s.removeAgent(w, name)
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
		var conns int64
		if s.prxy != nil {
			conns = s.prxy.WSCounter().Count(info.Hostname)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"name":           info.Name,
			"hostname":       info.Hostname,
			"policy":         info.Policy,
			"backend":        info.Backend,
			"container_name": info.ContainerName,
			"health_url":     info.HealthURL,
			"idle_timeout":   info.IdleTimeout,
			"state":          state,
			"connections":    conns,
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

func (s *Server) removeAgent(w http.ResponseWriter, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.agents[name]
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	// Cancel policy goroutine.
	if cancel, ok := s.cancels[name]; ok {
		cancel()
		delete(s.cancels, name)
	}

	// Deregister from proxy.
	s.prxy.Deregister(info.Hostname)

	// Remove from admin state.
	delete(s.agents, name)
	delete(s.policies, name)

	// Remove from config and persist.
	delete(s.cfg.Agents, name)
	if err := config.Save(s.cfg, s.cfgPath); err != nil {
		s.logger.Error("failed to persist config after removing agent", "error", err)
	}

	s.events.Emit(events.Event{Type: events.AgentRemoved, Agent: name})
	s.logger.Info("agent removed via API", "name", name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
	readyCount := 0
	sleepingCount := 0
	for name := range s.agents {
		if pol, ok := s.policies[name]; ok {
			switch pol.State() {
			case "ready":
				readyCount++
			case "sleeping":
				sleepingCount++
			}
		}
	}
	s.mu.RUnlock()

	serviceCount := len(s.registry.List())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":          "ok",
		"uptime_seconds":  time.Since(s.startAt).Seconds(),
		"agent_count":     agentCount,
		"ready_count":     readyCount,
		"sleeping_count":  sleepingCount,
		"ws_connections":  s.wsTotal(),
		"service_count":   serviceCount,
	})
}

// handleSSE streams events as Server-Sent Events.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ch := make(chan events.Event, 64)
	id := s.events.OnEvent(func(ev events.Event) {
		select {
		case ch <- ev:
		default: // drop if client is slow
		}
	})

	defer s.events.RemoveHandler(id)

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// AddAgent adds an agent dynamically (used by SIGHUP reload).
func (s *Server) AddAgent(name string, info AgentInfo, pol policy.Policy, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[name] = info
	s.policies[name] = pol
	s.cancels[name] = cancel
}

// RemoveAgentInternal removes an agent from admin state (used by SIGHUP reload).
func (s *Server) RemoveAgentInternal(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.cancels[name]; ok {
		cancel()
		delete(s.cancels, name)
	}
	delete(s.agents, name)
	delete(s.policies, name)
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
