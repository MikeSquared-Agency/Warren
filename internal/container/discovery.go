package container

import (
	"context"
	"log/slog"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type DiscoveredContainer struct {
	Name   string
	ID     string
	State  string
	Labels map[string]string
}

func Discover(ctx context.Context, docker *client.Client, logger *slog.Logger) ([]DiscoveredContainer, error) {
	f := filters.NewArgs()
	f.Add("label", "orchestrator.agent")

	containers, err := docker.ContainerList(ctx, container.ListOptions{
		All:     true, // include stopped
		Filters: f,
	})
	if err != nil {
		return nil, err
	}

	var result []DiscoveredContainer
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			// Docker prefixes names with /
			name = c.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}

		logger.Info("discovered container",
			"name", name,
			"id", c.ID[:12],
			"state", c.State,
			"agent", c.Labels["orchestrator.agent"],
		)

		result = append(result, DiscoveredContainer{
			Name:   name,
			ID:     c.ID,
			State:  c.State,
			Labels: c.Labels,
		})
	}

	return result, nil
}
