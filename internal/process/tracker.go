package process

import (
	"sync"
	"time"
)

// ProcessAgent represents a non-container agent tracked by the swarm.
type ProcessAgent struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`                 // "process"
	Runtime   string    `json:"runtime"`              // "claude-code"
	TaskID    string    `json:"task_id,omitempty"`
	OwnerUUID string    `json:"owner_uuid,omitempty"`
	SessionID string    `json:"session_id"`
	WorkDir   string    `json:"working_dir,omitempty"`
	Status    string    `json:"status"`               // running, done, failed
	StartedAt time.Time `json:"started_at"`
	ExitCode  *int      `json:"exit_code,omitempty"`
}

// Tracker maintains a map of active process-based agents.
type Tracker struct {
	mu     sync.RWMutex
	agents map[string]*ProcessAgent // session_id → agent
}

// NewTracker creates a new process tracker.
func NewTracker() *Tracker {
	return &Tracker{
		agents: make(map[string]*ProcessAgent),
	}
}

// Register adds a new process agent to the tracker.
func (t *Tracker) Register(agent *ProcessAgent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[agent.SessionID] = agent
}

// Update changes the status of a tracked process agent.
func (t *Tracker) Update(sessionID, status string, exitCode *int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if agent, ok := t.agents[sessionID]; ok {
		agent.Status = status
		agent.ExitCode = exitCode
	}
}

// List returns a snapshot of all tracked process agents.
func (t *Tracker) List() []*ProcessAgent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*ProcessAgent, 0, len(t.agents))
	for _, agent := range t.agents {
		cp := *agent
		result = append(result, &cp)
	}
	return result
}

// Get returns a single process agent by session ID.
func (t *Tracker) Get(sessionID string) (*ProcessAgent, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	agent, ok := t.agents[sessionID]
	if !ok {
		return nil, false
	}
	cp := *agent
	return &cp, true
}
