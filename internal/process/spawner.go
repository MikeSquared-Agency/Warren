package process

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"warren/internal/config"
	"warren/internal/events"
	"warren/internal/hermes"
)

// taskAssignment is the subset of the Dispatch Task struct we need for spawning.
// The full struct is published on swarm.task.*.assigned.
type taskAssignment struct {
	ID               string `json:"task_id"`
	Title            string `json:"title"`
	Description      string `json:"description,omitempty"`
	Runtime          string `json:"runtime,omitempty"`
	RecommendedModel string `json:"recommended_model,omitempty"`
	AssignedAgent    string `json:"assigned_agent,omitempty"`
	FilePatterns     []string `json:"file_patterns,omitempty"`
}

// Spawner subscribes to task assignment events and spawns PicoClaw workers
// for tasks with runtime: "picoclaw".
type Spawner struct {
	hermes     *hermes.Client
	tracker    *Tracker
	emitter    *events.Emitter
	cfg        config.PicoClawConfig
	logger     *slog.Logger
	running    int64 // atomic count of active workers
	mu         sync.Mutex
}

// NewSpawner creates a new PicoClaw worker spawner.
func NewSpawner(h *hermes.Client, tracker *Tracker, emitter *events.Emitter, cfg config.PicoClawConfig, logger *slog.Logger) *Spawner {
	return &Spawner{
		hermes:  h,
		tracker: tracker,
		emitter: emitter,
		cfg:     cfg,
		logger:  logger.With("component", "picoclaw-spawner"),
	}
}

// Start subscribes to task assignment events.
func (s *Spawner) Start() error {
	_, err := s.hermes.Subscribe(hermes.SubjectAllTaskAssigned, s.handleAssigned)
	if err != nil {
		return fmt.Errorf("subscribe task assignments: %w", err)
	}
	s.logger.Info("picoclaw spawner started",
		"binary", s.cfg.Binary,
		"mission_base_dir", s.cfg.MissionBaseDir,
		"max_concurrent", s.cfg.MaxConcurrent,
	)
	return nil
}

func (s *Spawner) handleAssigned(ev hermes.Event) {
	var task taskAssignment
	if err := json.Unmarshal(ev.Data, &task); err != nil {
		s.logger.Error("failed to unmarshal task assignment", "error", err)
		return
	}

	// Only handle picoclaw runtime tasks.
	if task.Runtime != "picoclaw" {
		return
	}

	// Check concurrency limit.
	current := atomic.LoadInt64(&s.running)
	if int(current) >= s.cfg.MaxConcurrent {
		s.logger.Warn("picoclaw worker limit reached, dropping task",
			"task_id", task.ID,
			"running", current,
			"max", s.cfg.MaxConcurrent,
		)
		return
	}

	s.logger.Info("spawning picoclaw worker",
		"task_id", task.ID,
		"title", task.Title,
		"model", task.RecommendedModel,
	)

	go s.spawnWorker(task)
}

func (s *Spawner) spawnWorker(task taskAssignment) {
	atomic.AddInt64(&s.running, 1)
	defer atomic.AddInt64(&s.running, -1)

	taskID := task.ID
	missionDir := filepath.Join(s.cfg.MissionBaseDir, taskID)

	// Create mission directory structure.
	handoffsDir := filepath.Join(missionDir, ".mission", "handoffs")
	findingsDir := filepath.Join(missionDir, ".mission", "findings")
	if err := os.MkdirAll(handoffsDir, 0755); err != nil {
		s.logger.Error("failed to create handoffs dir", "task_id", taskID, "error", err)
		return
	}
	if err := os.MkdirAll(findingsDir, 0755); err != nil {
		s.logger.Error("failed to create findings dir", "task_id", taskID, "error", err)
		return
	}

	// Write briefing JSON.
	briefing := map[string]interface{}{
		"task_id":   taskID,
		"objective": task.Title,
		"context":   task.Description,
	}
	if len(task.FilePatterns) > 0 {
		briefing["file_scope"] = task.FilePatterns
	}

	briefingData, err := json.MarshalIndent(briefing, "", "  ")
	if err != nil {
		s.logger.Error("failed to marshal briefing", "task_id", taskID, "error", err)
		return
	}

	briefingPath := filepath.Join(handoffsDir, taskID+"-briefing.json")
	if err := os.WriteFile(briefingPath, briefingData, 0644); err != nil {
		s.logger.Error("failed to write briefing", "task_id", taskID, "error", err)
		return
	}

	// Build picoclaw command.
	args := []string{
		"worker",
		"--task", taskID,
		"--mission-dir", missionDir,
	}
	if task.RecommendedModel != "" {
		args = append(args, "--model", task.RecommendedModel)
	}
	// Pass NATS URL so picoclaw can publish completion events.
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	args = append(args, "--nats-url", natsURL)

	cmd := exec.Command(s.cfg.Binary, args...)
	cmd.Dir = missionDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Register in process tracker.
	sessionID := fmt.Sprintf("picoclaw-%s", taskID)
	s.tracker.Register(&ProcessAgent{
		Name:      "pc-" + truncateID(taskID, 8),
		Type:      "process",
		Runtime:   "picoclaw",
		TaskID:    taskID,
		SessionID: sessionID,
		WorkDir:   missionDir,
		Status:    "running",
		StartedAt: time.Now(),
	})

	s.emitter.Emit(events.Event{
		Type:  "picoclaw.worker.started",
		Agent: sessionID,
		Fields: map[string]string{
			"task_id": taskID,
			"title":   task.Title,
		},
	})

	// Start process with timeout.
	if err := cmd.Start(); err != nil {
		s.logger.Error("failed to start picoclaw worker", "task_id", taskID, "error", err)
		exitCode := 1
		s.tracker.Update(sessionID, "failed", &exitCode)
		return
	}

	// Wait with timeout.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		exitCode := 0
		status := "done"
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
			status = "failed"
			s.logger.Warn("picoclaw worker exited with error",
				"task_id", taskID,
				"exit_code", exitCode,
				"error", err,
			)
		} else {
			s.logger.Info("picoclaw worker completed", "task_id", taskID)
		}
		s.tracker.Update(sessionID, status, &exitCode)

	case <-time.After(s.cfg.DefaultTimeout):
		s.logger.Warn("picoclaw worker timed out, killing",
			"task_id", taskID,
			"timeout", s.cfg.DefaultTimeout,
		)
		_ = cmd.Process.Kill()
		exitCode := 137
		s.tracker.Update(sessionID, "failed", &exitCode)
	}
}

// truncateID returns the first n characters of an ID string.
func truncateID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}
