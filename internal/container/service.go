package container

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"

	"warren/internal/config"
	"warren/internal/hermes"
)

// Manager manages Docker swarm services via scale 0/1.
type Manager struct {
	docker        *client.Client
	logger        *slog.Logger
	cfg           *config.Config
	sharedBinPath string
}

func NewManager(docker *client.Client, logger *slog.Logger) *Manager {
	return &Manager{
		docker: docker,
		logger: logger,
	}
}

// NewManagerWithConfig creates a new Manager with access to the configuration.
// This enables Hermes injection capabilities.
func NewManagerWithConfig(docker *client.Client, logger *slog.Logger, cfg *config.Config, sharedBinPath string) *Manager {
	m := &Manager{
		docker:        docker,
		logger:        logger,
		cfg:           cfg,
		sharedBinPath: sharedBinPath,
	}

	// Write the Hermes wrapper script on initialization
	if err := hermes.WriteWrapperScript(sharedBinPath); err != nil {
		logger.Warn("failed to write Hermes wrapper script", "error", err)
	} else {
		logger.Info("Hermes wrapper script written", "path", filepath.Join(sharedBinPath, hermes.WrapperScriptName))
	}

	return m
}

func (m *Manager) Start(ctx context.Context, name string) error {
	m.logger.Info("scaling service to 1", "service", name)
	return m.scale(ctx, name, 1)
}

func (m *Manager) Stop(ctx context.Context, name string, _ time.Duration) error {
	m.logger.Info("scaling service to 0", "service", name)
	return m.scale(ctx, name, 0)
}

func (m *Manager) Restart(ctx context.Context, name string, _ time.Duration) error {
	m.logger.Info("restarting service", "service", name)
	if err := m.scale(ctx, name, 0); err != nil {
		return err
	}
	// Brief pause to let the container fully stop.
	time.Sleep(2 * time.Second)
	return m.scale(ctx, name, 1)
}

func (m *Manager) Status(ctx context.Context, name string) (string, error) {
	svc, _, err := m.docker.ServiceInspectWithRaw(ctx, name, types.ServiceInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect service %q: %w", name, err)
	}

	replicas := uint64(0)
	if svc.Spec.Mode.Replicated != nil && svc.Spec.Mode.Replicated.Replicas != nil {
		replicas = *svc.Spec.Mode.Replicated.Replicas
	}

	if replicas == 0 {
		return "exited", nil
	}

	// Check if any tasks are actually running.
	tasks, err := m.docker.TaskList(ctx, types.TaskListOptions{
		Filters: filters.NewArgs(
			filters.Arg("service", name),
			filters.Arg("desired-state", "running"),
		),
	})
	if err != nil {
		return "", fmt.Errorf("list tasks for service %q: %w", name, err)
	}

	for _, task := range tasks {
		if task.Status.State == "running" {
			return "running", nil
		}
	}

	return "starting", nil
}

// findAgentForService finds the agent config that corresponds to a service name.
// It looks for an agent whose container name matches the service name.
func (m *Manager) findAgentForService(serviceName string) (*config.Agent, string) {
	if m.cfg == nil {
		return nil, ""
	}

	for agentName, agent := range m.cfg.Agents {
		if agent.Container.Name == serviceName {
			return agent, agentName
		}
	}
	return nil, ""
}

// injectHermes modifies a service spec to enable Hermes watcher injection.
func (m *Manager) injectHermes(spec *swarm.ServiceSpec, agentID string) error {
	if spec.TaskTemplate.ContainerSpec == nil {
		return fmt.Errorf("service spec missing container spec")
	}

	container := spec.TaskTemplate.ContainerSpec

	// 1. Mount the shared-bin volume
	hasSharedBinMount := false
	for _, mount := range container.Mounts {
		if mount.Target == hermes.SharedBinMountPath {
			hasSharedBinMount = true
			break
		}
	}
	if !hasSharedBinMount {
		container.Mounts = append(container.Mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.sharedBinPath,
			Target:   hermes.SharedBinMountPath,
			ReadOnly: true,
		})
	}

	// 2. Set required environment variables
	envVars := map[string]string{
		"AGENT_ID":     agentID,
		"NATS_URL":     m.cfg.Hermes.URL,
		"GATEWAY_PORT": "18790", // Default OpenClaw gateway port
	}

	// Update or add environment variables
	envMap := make(map[string]string)
	for _, env := range container.Env {
		if equalIdx := findChar(env, '='); equalIdx != -1 {
			key := env[:equalIdx]
			value := env[equalIdx+1:]
			envMap[key] = value
		}
	}

	// Add or update the required env vars
	for key, value := range envVars {
		envMap[key] = value
	}

	// Rebuild the env slice
	container.Env = nil
	for key, value := range envMap {
		container.Env = append(container.Env, key+"="+value)
	}

	// 3. Wrap the command to use the Hermes wrapper
	if len(container.Command) > 0 {
		container.Command = hermes.WrapperCommand(container.Command)
		m.logger.Info("injected Hermes wrapper", "agent", agentID, "original_command", len(container.Command))
	} else if len(container.Args) > 0 {
		// If no Command but has Args, wrap the Args
		container.Command = hermes.WrapperCommand(container.Args)
		container.Args = nil
		m.logger.Info("injected Hermes wrapper using args", "agent", agentID)
	}

	return nil
}

func (m *Manager) scale(ctx context.Context, name string, replicas uint64) error {
	svc, _, err := m.docker.ServiceInspectWithRaw(ctx, name, types.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect service %q: %w", name, err)
	}

	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %q is not replicated", name)
	}

	// Store the original replicas value for comparison
	originalReplicas := uint64(0)
	if svc.Spec.Mode.Replicated.Replicas != nil {
		originalReplicas = *svc.Spec.Mode.Replicated.Replicas
	}

	svc.Spec.Mode.Replicated.Replicas = &replicas

	// If we're scaling from 0 to >0 and have config access, check if we should inject Hermes
	if originalReplicas == 0 && replicas > 0 && m.cfg != nil {
		agent, agentID := m.findAgentForService(name)
		if agent != nil && agent.Hermes.Enabled {
			m.logger.Info("injecting Hermes watcher", "service", name, "agent", agentID)
			if err := m.injectHermes(&svc.Spec, agentID); err != nil {
				m.logger.Error("failed to inject Hermes", "service", name, "agent", agentID, "error", err)
			}
		}
	}

	_, err = m.docker.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, types.ServiceUpdateOptions{})
	if err != nil {
		return fmt.Errorf("scale service %q to %d: %w", name, replicas, err)
	}

	return nil
}

// findChar finds the first occurrence of a character in a string.
func findChar(s string, char rune) int {
	for i, r := range s {
		if r == char {
			return i
		}
	}
	return -1
}
