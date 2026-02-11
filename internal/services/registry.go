package services

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
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
	mu               sync.RWMutex
	services         map[string]*Service // hostname → service
	reservedHosts    map[string]bool     // hostnames reserved by configured backends
	logger           *slog.Logger
}

// NewRegistry creates a new service registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		services:      make(map[string]*Service),
		reservedHosts: make(map[string]bool),
		logger:        logger.With("component", "service-registry"),
	}
}

// ReserveHostname marks a hostname as reserved (used by configured backends).
// Reserved hostnames cannot be registered dynamically.
func (r *Registry) ReserveHostname(hostname string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reservedHosts[hostname] = true
}

// Register adds an ephemeral route. Returns an error if the hostname is reserved
// or the target URL is not allowed.
func (r *Registry) Register(hostname, target, agent string) error {
	// Validate target URL to prevent SSRF.
	if err := validateTarget(target); err != nil {
		r.logger.Warn("service registration rejected: invalid target", "hostname", hostname, "target", target, "error", err)
		return fmt.Errorf("invalid target: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Prevent overwriting configured backend hostnames.
	if r.reservedHosts[hostname] {
		r.logger.Warn("service registration rejected: hostname reserved", "hostname", hostname)
		return fmt.Errorf("hostname %q is reserved", hostname)
	}

	r.services[hostname] = &Service{
		Hostname:  hostname,
		Target:    target,
		Agent:     agent,
		CreatedAt: time.Now(),
	}
	r.logger.Info("service registered", "hostname", hostname, "target", target, "agent", agent)
	return nil
}

// validateTarget checks that a service target URL is safe to proxy to.
func validateTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}

	// Only allow http/https schemes.
	switch u.Scheme {
	case "http", "https":
		// ok
	default:
		return fmt.Errorf("scheme %q not allowed", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}

	// Block well-known metadata endpoints and dangerous hosts.
	blockedHosts := []string{
		"169.254.169.254", // AWS/GCP metadata
		"metadata.google.internal",
		"metadata.google",
	}
	for _, blocked := range blockedHosts {
		if strings.EqualFold(host, blocked) {
			return fmt.Errorf("target host %q is blocked", host)
		}
	}

	// Block link-local and loopback IPs.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("target IP %s is not allowed", ip)
		}
	}

	// Block unix socket paths and Docker socket access via URL.
	if strings.Contains(target, "docker.sock") || u.Scheme == "unix" {
		return fmt.Errorf("unix socket targets are not allowed")
	}

	return nil
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

// RegisterUnsafe adds an ephemeral route without target validation.
// Intended for testing only.
func (r *Registry) RegisterUnsafe(hostname, target, agent string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[hostname] = &Service{
		Hostname:  hostname,
		Target:    target,
		Agent:     agent,
		CreatedAt: time.Now(),
	}
}
