package hermes

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Event is the standardised envelope for all Hermes messages.
type Event struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Source        string          `json:"source"`
	Timestamp     time.Time       `json:"timestamp"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	CausationID   string          `json:"causation_id,omitempty"`
	Data          json.RawMessage `json:"data"`
}

// NewEvent creates a new Event with a generated ID and current timestamp.
func NewEvent(eventType, source string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now().UTC(),
		Data:      raw,
	}, nil
}

// WithCorrelation returns a copy of the event with the given correlation and causation IDs.
func (e Event) WithCorrelation(correlationID, causationID string) Event {
	e.CorrelationID = correlationID
	e.CausationID = causationID
	return e
}

// Marshal serialises the event to JSON bytes.
func (e Event) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEvent deserialises an event from JSON bytes.
func UnmarshalEvent(data []byte) (Event, error) {
	var ev Event
	err := json.Unmarshal(data, &ev)
	return ev, err
}

// Lifecycle event data types.

// AgentLifecycleData is the payload for agent lifecycle events.
type AgentLifecycleData struct {
	Agent  string `json:"agent"`
	Reason string `json:"reason,omitempty"`
}

// AgentScaleData is the payload for agent scale events.
type AgentScaleData struct {
	Agent    string `json:"agent"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Reason   string `json:"reason,omitempty"`
}

// AgentBriefedData is the payload for agent briefed events.
type AgentBriefedData struct {
	Agent     string `json:"agent"`
	ItemCount int    `json:"item_count"`
	Summary   string `json:"summary,omitempty"`
}

// DiscoveryData is the payload for agent discovery events.
type DiscoveryData struct {
	Agent   string   `json:"agent"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

// SSH event data types.

// SSHAuthorizedData is the payload for successful SSH authorization events.
type SSHAuthorizedData struct {
	Device      string `json:"device"`
	Person      string `json:"person"`
	PersonID    string `json:"person_id"`
	Fingerprint string `json:"fingerprint"`
	Username    string `json:"username"`
	RemoteIP    string `json:"remote_ip,omitempty"`
}

// SSHDeniedData is the payload for denied SSH authorization events.
type SSHDeniedData struct {
	Fingerprint string `json:"fingerprint"`
	Username    string `json:"username,omitempty"`
	Reason      string `json:"reason"`
	RemoteIP    string `json:"remote_ip,omitempty"`
}

// CCSessionCompletedData is the payload for Claude Code session completion events.
type CCSessionCompletedData struct {
	SessionID      string   `json:"session_id"`
	TaskID         string   `json:"task_id,omitempty"`
	OwnerUUID      string   `json:"owner_uuid,omitempty"`
	AgentType      string   `json:"agent_type"`
	TranscriptPath string   `json:"transcript_path"`
	FilesChanged   []string `json:"files_changed"`
	ExitCode       int      `json:"exit_code"`
	DurationMs     int64    `json:"duration_ms"`
	WorkingDir     string   `json:"working_dir"`
	Timestamp      string   `json:"timestamp"`
}
