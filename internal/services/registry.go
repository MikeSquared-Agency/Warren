package services

import (
	"log/slog"
	"sync"
	"time"
)

// Service represents a dynamically registered route.
type Service struct {
	Hostname  string    `json:"hostname"`
	Target    string    `json:"target"`
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"created_at"`
}

// Registry holds ephemeral service routes registered by agents.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*Service // hostname → service
	logger   *slog.Logger
}

// NewRegistry creates a new service registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		services: make(map[string]*Service),
		logger:   logger.With("component", "service-registry"),
	}
}

// Register adds an ephemeral route.
func (r *Registry) Register(hostname, target, agent string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.services[hostname] = &Service{
		Hostname:  hostname,
		Target:    target,
		Agent:     agent,
		CreatedAt: time.Now(),
	}
	r.logger.Info("service registered", "hostname", hostname, "target", target, "agent", agent)
}

// Deregister removes a route by hostname.
func (r *Registry) Deregister(hostname string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.services[hostname]; ok {
		delete(r.services, hostname)
		r.logger.Info("service deregistered", "hostname", hostname)
	}
}

// DeregisterByAgent purges all routes for an agent.
func (r *Registry) DeregisterByAgent(agent string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var removed []string
	for hostname, svc := range r.services {
		if svc.Agent == agent {
			delete(r.services, hostname)
			removed = append(removed, hostname)
		}
	}
	if len(removed) > 0 {
		r.logger.Info("services deregistered by agent", "agent", agent, "hostnames", removed)
	}
}

// Lookup checks if a service is registered for the given hostname.
func (r *Registry) Lookup(hostname string) (*Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[hostname]
	return svc, ok
}

// List returns all registered services.
func (r *Registry) List() []Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Service, 0, len(r.services))
	for _, svc := range r.services {
		result = append(result, *svc)
	}
	return result
}
