package container

import (
	"context"
	"log/slog"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// EventHandler is called when a Docker event is observed.
type EventHandler func(serviceID, serviceName, action string)

// Watcher subscribes to Docker events and calls the handler on state changes.
type Watcher struct {
	docker  *client.Client
	handler EventHandler
	logger  *slog.Logger
}

// NewWatcher creates a new Docker event watcher.
func NewWatcher(docker *client.Client, handler EventHandler, logger *slog.Logger) *Watcher {
	return &Watcher{
		docker:  docker,
		handler: handler,
		logger:  logger.With("component", "docker-watcher"),
	}
}

// Watch subscribes to Docker events and blocks until ctx is cancelled.
func (w *Watcher) Watch(ctx context.Context) {
	f := filters.NewArgs()
	f.Add("type", "service")
	f.Add("type", "container")

	msgCh, errCh := w.docker.Events(ctx, events.ListOptions{Filters: f})

	w.logger.Info("watching Docker events")
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("docker watcher stopped")
			return
		case err := <-errCh:
			if ctx.Err() != nil {
				return
			}
			w.logger.Error("docker events error", "error", err)
			return
		case msg := <-msgCh:
			w.handleEvent(msg)
		}
	}
}

func (w *Watcher) handleEvent(msg events.Message) {
	switch msg.Type {
	case events.ContainerEventType:
		switch msg.Action {
		case "start", "die", "health_status":
			name := msg.Actor.Attributes["name"]
			service := msg.Actor.Attributes["com.docker.swarm.service.name"]
			id := msg.Actor.ID
			if len(id) > 12 {
				id = id[:12]
			}
			w.logger.Info("container event", "action", msg.Action, "container", name, "id", id)
			w.handler(msg.Actor.ID, service, "container."+string(msg.Action))
		}
	case events.ServiceEventType:
		switch msg.Action {
		case "update":
			name := msg.Actor.Attributes["name"]
			w.logger.Info("service event", "action", msg.Action, "service", name)
			w.handler(msg.Actor.ID, name, "service."+string(msg.Action))
		}
	}
}
