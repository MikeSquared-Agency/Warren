package admin

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"os"
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
	mu        sync.RWMutex
	agents    map[string]AgentInfo
	policies  map[string]policy.Policy
	cancels   map[string]context.CancelFunc
	registry  *services.Registry
	events    *events.Emitter
	manager   *container.Manager
	prxy      *proxy.Proxy
	cfg       *config.Config
	cfgPath   string
	authToken string
	logger    *slog.Logger
	startAt   time.Time
	wsTotal   func() int64
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
	l := logger.With("component", "admin")
	if cfg.AdminToken == "" {
		l.Warn("admin API has no auth token configured — all requests will be allowed")
	}
	return &Server{
		agents:    agents,
		policies:  policies,
		cancels:   cancels,
		registry:  registry,
		events:    emitter,
		manager:   manager,
		prxy:      prxy,
		cfg:       cfg,
		cfgPath:   cfgPath,
		authToken: cfg.AdminToken,
		wsTotal:   wsTotal,
		logger:    l,
		startAt:   time.Now(),
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
	// SSH endpoints (only available if SSH is enabled)
	if s.cfg.SSH.Enabled {
		mux.HandleFunc("/admin/ssh/authorize", s.handleSSHAuthorize)
	}
	return s.authMiddleware(mux)
}

// authMiddleware checks for a valid Bearer token if one is configured.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+s.authToken {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
			WakeCooldown:       30 * time.Second,
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

// SSHHandler returns an http.Handler for SSH-related endpoints that don't require admin authentication.
func (s *Server) SSHHandler() http.Handler {
	mux := http.NewServeMux()
	if s.cfg.SSH.Enabled {
		mux.HandleFunc("/ssh/authorized-keys/", s.handleSSHAuthorizedKeys)
	}
	return mux
}

// SSH-related types and methods

// SSHAuthorizeRequest represents the request for SSH authorization.
type SSHAuthorizeRequest struct {
	Fingerprint string `json:"fingerprint"`
	Username    string `json:"username"`
}

// SSHAuthorizeResponse represents the response for SSH authorization.
type SSHAuthorizeResponse struct {
	Allowed   bool   `json:"allowed"`
	Device    string `json:"device,omitempty"`
	Person    string `json:"person,omitempty"`
	PersonID  string `json:"person_id,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// AlexandriaDevice represents a device from Alexandria API.
type AlexandriaDevice struct {
	Identifier string                 `json:"identifier"`
	OwnerID    string                 `json:"owner_id"`
	Metadata   map[string]interface{} `json:"metadata"`
}

// AlexandriaPerson represents a person from Alexandria API.
type AlexandriaPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AlexandriaDevicesResponse represents the response from Alexandria devices API.
type AlexandriaDevicesResponse struct {
	Data []AlexandriaDevice `json:"data"`
}

// AlexandriaPeopleResponse represents the response from Alexandria people API.
type AlexandriaPeopleResponse struct {
	Data []AlexandriaPerson `json:"data"`
}

// handleSSHAuthorize handles the POST /admin/ssh/authorize endpoint.
func (s *Server) handleSSHAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check if SSH is enabled
	if !s.cfg.SSH.Enabled {
		http.Error(w, `{"error":"SSH authorization is disabled"}`, http.StatusServiceUnavailable)
		return
	}

	var req SSHAuthorizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if req.Fingerprint == "" || req.Username == "" {
		http.Error(w, `{"error":"fingerprint and username are required"}`, http.StatusBadRequest)
		return
	}

	// Query Alexandria for devices
	devices, err := s.getAlexandriaDevices()
	if err != nil {
		s.logger.Error("failed to get devices from Alexandria", "error", err)
		http.Error(w, `{"error":"failed to query device registry"}`, http.StatusInternalServerError)
		return
	}

	// Find device with matching SSH fingerprint
	var matchedDevice *AlexandriaDevice
	for _, device := range devices {
		if fingerprint, ok := device.Metadata["ssh_fingerprint"].(string); ok {
			if fingerprint == req.Fingerprint {
				matchedDevice = &device
				break
			}
		}
	}

	if matchedDevice == nil {
		response := SSHAuthorizeResponse{
			Allowed: false,
			Reason:  "unregistered device",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get person information
	people, err := s.getAlexandriaPeople()
	if err != nil {
		s.logger.Error("failed to get people from Alexandria", "error", err)
		http.Error(w, `{"error":"failed to query people registry"}`, http.StatusInternalServerError)
		return
	}

	var personName, personID string
	for _, person := range people {
		if person.ID == matchedDevice.OwnerID {
			personName = person.Name
			personID = person.ID
			break
		}
	}

	// Get the public key from authorized_keys file
	publicKey, err := s.getPublicKeyByFingerprint(req.Username, req.Fingerprint)
	if err != nil {
		s.logger.Error("failed to get public key", "error", err, "fingerprint", req.Fingerprint)
		response := SSHAuthorizeResponse{
			Allowed: false,
			Reason:  "public key not found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Success response
	response := SSHAuthorizeResponse{
		Allowed:   true,
		Device:    matchedDevice.Identifier,
		Person:    personName,
		PersonID:  personID,
		PublicKey: publicKey,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleSSHAuthorizedKeys handles the GET /ssh/authorized-keys/{username} endpoint.
func (s *Server) handleSSHAuthorizedKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check if SSH is enabled
	if !s.cfg.SSH.Enabled {
		http.Error(w, `{"error":"SSH authorization is disabled"}`, http.StatusServiceUnavailable)
		return
	}

	// Extract username from path
	path := strings.TrimPrefix(r.URL.Path, "/ssh/authorized-keys/")
	username := strings.Split(path, "/")[0]
	if username == "" {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}

	// Localhost-only protection
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "invalid remote address", http.StatusBadRequest)
		return
	}
	if remoteIP != "127.0.0.1" && remoteIP != "::1" {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	// Get all devices from Alexandria
	devices, err := s.getAlexandriaDevices()
	if err != nil {
		s.logger.Error("failed to get devices from Alexandria", "error", err)
		http.Error(w, "failed to query device registry", http.StatusInternalServerError)
		return
	}

	// Read authorized_keys file
	authorizedKeysPath := strings.Replace(s.cfg.SSH.AuthorizedKeysPath, "{username}", username, 1)
	file, err := os.Open(authorizedKeysPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty response for non-existent file
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			return
		}
		s.logger.Error("failed to open authorized_keys file", "error", err, "path", authorizedKeysPath)
		http.Error(w, "failed to read authorized keys", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Build allowed fingerprints map
	allowedFingerprints := make(map[string]bool)
	for _, device := range devices {
		if fingerprint, ok := device.Metadata["ssh_fingerprint"].(string); ok {
			allowedFingerprints[fingerprint] = true
		}
	}

	var allowedKeys []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fingerprint, err := calculateSSHFingerprint(line)
		if err != nil {
			continue // Skip malformed keys
		}

		if allowedFingerprints[fingerprint] {
			allowedKeys = append(allowedKeys, line)
		}
	}

	if err := scanner.Err(); err != nil {
		s.logger.Error("failed to read authorized_keys file", "error", err)
		http.Error(w, "failed to read authorized keys", http.StatusInternalServerError)
		return
	}

	// Return the allowed keys
	w.Header().Set("Content-Type", "text/plain")
	for _, key := range allowedKeys {
		fmt.Fprintln(w, key)
	}
}

// getAlexandriaDevices retrieves devices from Alexandria API.
func (s *Server) getAlexandriaDevices() ([]AlexandriaDevice, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := s.cfg.SSH.AlexandriaURL + "/api/v1/devices"

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to request devices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("devices API returned status %d", resp.StatusCode)
	}

	var response AlexandriaDevicesResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode devices response: %w", err)
	}

	return response.Data, nil
}

// getAlexandriaPeople retrieves people from Alexandria API.
func (s *Server) getAlexandriaPeople() ([]AlexandriaPerson, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := s.cfg.SSH.AlexandriaURL + "/api/v1/people"

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to request people: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("people API returned status %d", resp.StatusCode)
	}

	var response AlexandriaPeopleResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode people response: %w", err)
	}

	return response.Data, nil
}

// getPublicKeyByFingerprint finds a public key by its fingerprint in authorized_keys.
func (s *Server) getPublicKeyByFingerprint(username, fingerprint string) (string, error) {
	authorizedKeysPath := strings.Replace(s.cfg.SSH.AuthorizedKeysPath, "{username}", username, 1)
	file, err := os.Open(authorizedKeysPath)
	if err != nil {
		return "", fmt.Errorf("failed to open authorized_keys file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		keyFingerprint, err := calculateSSHFingerprint(line)
		if err != nil {
			continue
		}

		if keyFingerprint == fingerprint {
			return line, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read authorized_keys file: %w", err)
	}

	return "", fmt.Errorf("public key not found for fingerprint %s", fingerprint)
}

// calculateSSHFingerprint calculates the SSH fingerprint for a public key line.
func calculateSSHFingerprint(keyLine string) (string, error) {
	parts := strings.Split(keyLine, " ")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid public key format")
	}

	keyData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode public key: %w", err)
	}

	hash := sha256.Sum256(keyData)
	return "SHA256:" + base64.StdEncoding.EncodeToString(hash[:]), nil
}
