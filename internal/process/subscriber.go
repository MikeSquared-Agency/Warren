package process

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"warren/internal/events"
	"warren/internal/hermes"
)

// Subscriber listens for CC sidecar events and updates the process tracker.
type Subscriber struct {
	hermes  *hermes.Client
	tracker *Tracker
	emitter *events.Emitter
	logger  *slog.Logger
}

// NewSubscriber creates a new CC session event subscriber.
func NewSubscriber(h *hermes.Client, tracker *Tracker, emitter *events.Emitter, logger *slog.Logger) *Subscriber {
	return &Subscriber{
		hermes:  h,
		tracker: tracker,
		emitter: emitter,
		logger:  logger.With("component", "process-subscriber"),
	}
}

// Start subscribes to CC session events.
func (s *Subscriber) Start() error {
	_, err := s.hermes.Subscribe(hermes.SubjectCCSessionCompleted, s.handleCompleted)
	if err != nil {
		return fmt.Errorf("subscribe cc.session.completed: %w", err)
	}

	_, err = s.hermes.Subscribe(hermes.SubjectCCSessionFailed, s.handleFailed)
	if err != nil {
		return fmt.Errorf("subscribe cc.session.failed: %w", err)
	}

	s.logger.Info("subscribed to CC session events")
	return nil
}

func (s *Subscriber) handleCompleted(ev hermes.Event) {
	var data hermes.CCSessionCompletedData
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		s.logger.Error("failed to unmarshal cc session completed", "error", err)
		return
	}

	s.logger.Info("CC session completed",
		"session_id", data.SessionID,
		"task_id", data.TaskID,
		"files_changed", len(data.FilesChanged),
		"duration_ms", data.DurationMs,
	)

	exitCode := data.ExitCode
	s.tracker.Update(data.SessionID, "done", &exitCode)

	// If we haven't seen this session before (ad-hoc), register it as done.
	if _, ok := s.tracker.Get(data.SessionID); !ok {
		name := "cc-" + data.SessionID[:8]
		if data.TaskID != "" {
			name = "cc-worker-" + data.TaskID[:8]
		}
		s.tracker.Register(&ProcessAgent{
			Name:      name,
			Type:      "process",
			Runtime:   data.AgentType,
			TaskID:    data.TaskID,
			OwnerUUID: data.OwnerUUID,
			SessionID: data.SessionID,
			WorkDir:   data.WorkingDir,
			Status:    "done",
			StartedAt: time.Now().Add(-time.Duration(data.DurationMs) * time.Millisecond),
			ExitCode:  &exitCode,
		})
	}

	s.emitter.Emit(events.Event{
		Type:  "cc.session.completed",
		Agent: data.SessionID,
		Fields: map[string]string{
			"task_id":    data.TaskID,
			"session_id": data.SessionID,
		},
	})
}

func (s *Subscriber) handleFailed(ev hermes.Event) {
	var data hermes.CCSessionCompletedData
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		s.logger.Error("failed to unmarshal cc session failed", "error", err)
		return
	}

	s.logger.Warn("CC session failed",
		"session_id", data.SessionID,
		"task_id", data.TaskID,
		"exit_code", data.ExitCode,
	)

	exitCode := data.ExitCode
	s.tracker.Update(data.SessionID, "failed", &exitCode)

	if _, ok := s.tracker.Get(data.SessionID); !ok {
		name := "cc-" + data.SessionID[:8]
		if data.TaskID != "" {
			name = "cc-worker-" + data.TaskID[:8]
		}
		s.tracker.Register(&ProcessAgent{
			Name:      name,
			Type:      "process",
			Runtime:   data.AgentType,
			TaskID:    data.TaskID,
			OwnerUUID: data.OwnerUUID,
			SessionID: data.SessionID,
			WorkDir:   data.WorkingDir,
			Status:    "failed",
			StartedAt: time.Now().Add(-time.Duration(data.DurationMs) * time.Millisecond),
			ExitCode:  &exitCode,
		})
	}

	s.emitter.Emit(events.Event{
		Type:  "cc.session.failed",
		Agent: data.SessionID,
		Fields: map[string]string{
			"task_id":    data.TaskID,
			"session_id": data.SessionID,
		},
	})
}
