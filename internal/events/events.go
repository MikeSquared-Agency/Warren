package events

import (
	"log/slog"
	"sync"
	"time"
)

// Event type constants.
const (
	AgentReady        = "agent.ready"
	AgentDegraded     = "agent.degraded"
	AgentWake         = "agent.wake"
	AgentSleep        = "agent.sleep"
	AgentStarting     = "agent.starting"
	AgentHealthFailed = "agent.health_failed"
	RestartExhausted  = "restart.exhausted"
	AgentAdded        = "agent.added"
	AgentRemoved      = "agent.removed"
)

// Event represents a lifecycle event for an agent.
type Event struct {
	Type      string            `json:"type"`
	Agent     string            `json:"agent"`
	Timestamp time.Time         `json:"timestamp"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Emitter logs events and dispatches them to registered handlers.
type Emitter struct {
	logger   *slog.Logger
	mu       sync.RWMutex
	handlers []func(Event)
}

// NewEmitter creates a new event emitter.
func NewEmitter(logger *slog.Logger) *Emitter {
	return &Emitter{
		logger: logger.With("component", "events"),
	}
}

// Emit logs the event and calls all registered handlers.
func (e *Emitter) Emit(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}

	attrs := []any{
		"event", ev.Type,
		"agent", ev.Agent,
	}
	for k, v := range ev.Fields {
		attrs = append(attrs, k, v)
	}
	e.logger.Info("event emitted", attrs...)

	e.mu.RLock()
	handlers := e.handlers
	e.mu.RUnlock()

	for _, fn := range handlers {
		if fn != nil {
			fn(ev)
		}
	}
}

// OnEvent registers a handler to be called for every emitted event.
// Returns an ID that can be used with RemoveHandler.
func (e *Emitter) OnEvent(fn func(Event)) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers = append(e.handlers, fn)
	return len(e.handlers) - 1
}

// RemoveHandler removes a handler by its ID.
func (e *Emitter) RemoveHandler(id int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id >= 0 && id < len(e.handlers) {
		e.handlers[id] = nil
	}
}
