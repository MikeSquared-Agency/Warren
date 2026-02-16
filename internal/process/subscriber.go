package process

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"warren/internal/events"
	"warren/internal/hermes"
	"warren/internal/store"
)

// Subscriber listens for CC sidecar events and updates the process tracker.
type Subscriber struct {
	hermes     *hermes.Client
	tracker    *Tracker
	emitter    *events.Emitter
	usageStore store.UsageStore // nil when usage tracking disabled
	logger     *slog.Logger
}

// NewSubscriber creates a new CC session event subscriber.
// usageStore may be nil if usage tracking is disabled.
func NewSubscriber(h *hermes.Client, tracker *Tracker, emitter *events.Emitter, usageStore store.UsageStore, logger *slog.Logger) *Subscriber {
	return &Subscriber{
		hermes:     h,
		tracker:    tracker,
		emitter:    emitter,
		usageStore: usageStore,
		logger:     logger.With("component", "process-subscriber"),
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

	s.enrichSession(data)
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

	s.enrichSession(data)
}

// enrichSession writes CC session metadata to the usage store.
func (s *Subscriber) enrichSession(data hermes.CCSessionCompletedData) {
	if s.usageStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agentID := data.AgentType
	if agentID == "" {
		agentID = "unknown"
	}

	if err := s.usageStore.EnrichSession(ctx, data.SessionID, agentID, data.TaskID, data.DurationMs, data.ExitCode, data.FilesChanged); err != nil {
		s.logger.Error("failed to enrich session usage", "session_id", data.SessionID, "error", err)
	}
}
