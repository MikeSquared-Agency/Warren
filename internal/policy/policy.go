package policy

import "context"

type Policy interface {
	// Start runs the policy's long-lived goroutine (health checks, restarts, etc).
	// It blocks until ctx is cancelled.
	Start(ctx context.Context)

	// State returns the current agent state: "sleeping", "starting", "ready", "degraded".
	State() string

	// OnRequest is called by the proxy before forwarding a request.
	OnRequest()
}
