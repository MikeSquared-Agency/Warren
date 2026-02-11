package container

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type Manager struct {
	docker *client.Client
	logger *slog.Logger
}

func NewManager(docker *client.Client, logger *slog.Logger) *Manager {
	return &Manager{
		docker: docker,
		logger: logger,
	}
}

func (m *Manager) Start(ctx context.Context, name string) error {
	m.logger.Info("starting container", "container", name)
	if err := m.docker.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container %q: %w", name, err)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context, name string, gracePeriod time.Duration) error {
	m.logger.Info("stopping container", "container", name, "grace_period", gracePeriod)
	secs := int(gracePeriod.Seconds())
	opts := container.StopOptions{Timeout: &secs}
	if err := m.docker.ContainerStop(ctx, name, opts); err != nil {
		return fmt.Errorf("stop container %q: %w", name, err)
	}
	return nil
}

func (m *Manager) Restart(ctx context.Context, name string, gracePeriod time.Duration) error {
	m.logger.Info("restarting container", "container", name)
	secs := int(gracePeriod.Seconds())
	opts := container.StopOptions{Timeout: &secs}
	if err := m.docker.ContainerRestart(ctx, name, opts); err != nil {
		return fmt.Errorf("restart container %q: %w", name, err)
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, name string) (string, error) {
	info, err := m.docker.ContainerInspect(ctx, name)
	if err != nil {
		return "", fmt.Errorf("inspect container %q: %w", name, err)
	}
	return info.State.Status, nil
}
