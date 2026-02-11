package container

import (
	"context"
	"time"
)

// Lifecycle abstracts container/service start, stop, restart, and status.
// Implemented by both Manager (docker containers) and ServiceManager (swarm services).
type Lifecycle interface {
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string, gracePeriod time.Duration) error
	Restart(ctx context.Context, name string, gracePeriod time.Duration) error
	Status(ctx context.Context, name string) (string, error)
}
