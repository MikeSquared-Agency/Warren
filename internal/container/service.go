package container

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// ServiceManager manages Docker swarm services via scale 0/1.
type ServiceManager struct {
	docker *client.Client
	logger *slog.Logger
}

func NewServiceManager(docker *client.Client, logger *slog.Logger) *ServiceManager {
	return &ServiceManager{
		docker: docker,
		logger: logger,
	}
}

func (m *ServiceManager) Start(ctx context.Context, name string) error {
	m.logger.Info("scaling service to 1", "service", name)
	return m.scale(ctx, name, 1)
}

func (m *ServiceManager) Stop(ctx context.Context, name string, _ time.Duration) error {
	m.logger.Info("scaling service to 0", "service", name)
	return m.scale(ctx, name, 0)
}

func (m *ServiceManager) Restart(ctx context.Context, name string, _ time.Duration) error {
	m.logger.Info("restarting service", "service", name)
	if err := m.scale(ctx, name, 0); err != nil {
		return err
	}
	// Brief pause to let the container fully stop.
	time.Sleep(2 * time.Second)
	return m.scale(ctx, name, 1)
}

func (m *ServiceManager) Status(ctx context.Context, name string) (string, error) {
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

func (m *ServiceManager) scale(ctx context.Context, name string, replicas uint64) error {
	svc, _, err := m.docker.ServiceInspectWithRaw(ctx, name, types.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect service %q: %w", name, err)
	}

	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %q is not replicated", name)
	}

	svc.Spec.Mode.Replicated.Replicas = &replicas
	_, err = m.docker.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, types.ServiceUpdateOptions{})
	if err != nil {
		return fmt.Errorf("scale service %q to %d: %w", name, replicas, err)
	}

	return nil
}
