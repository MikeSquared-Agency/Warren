package tailer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"warren/internal/store"
)

// jsonlEntry is the raw structure of each line in anthropic-payload.jsonl.
type jsonlEntry struct {
	RunID        string          `json:"runId"`
	SessionID    string          `json:"sessionId"`
	SessionKey   string          `json:"sessionKey"`
	Provider     string          `json:"provider"`
	ModelID      string          `json:"modelId"`
	WorkspaceDir string          `json:"workspaceDir"`
	Timestamp    string          `json:"ts"`
	Stage        string          `json:"stage"`
	Usage        *usageData      `json:"usage,omitempty"`
}

type usageData struct {
	Input       int64    `json:"input"`
	Output      int64    `json:"output"`
	CacheRead   int64    `json:"cacheRead"`
	CacheWrite  int64    `json:"cacheWrite"`
	TotalTokens int64    `json:"totalTokens"`
	Cost        costData `json:"cost"`
}

type costData struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// accumulator tracks running totals for a session.
type accumulator struct {
	agentID         string
	sessionLabel    string
	provider        string
	modelID         string
	inputTokens     int64
	outputTokens    int64
	cacheReadTokens int64
	cacheWriteTokens int64
	totalTokens     int64
	inputCostUSD    float64
	outputCostUSD   float64
	cacheReadCostUSD  float64
	cacheWriteCostUSD float64
	totalCostUSD    float64
	requestCount    int
	firstSeenAt     time.Time
	lastSeenAt      time.Time
	dirty           bool
}

// Tailer reads the JSONL log file incrementally and flushes to the usage store.
type Tailer struct {
	store         store.UsageStore
	jsonlPath     string
	offsetPath    string
	flushInterval time.Duration
	pollInterval  time.Duration
	logger        *slog.Logger

	mu           sync.Mutex
	sessions     map[string]*accumulator
	offset       int64
}

// New creates a new Tailer.
func New(s store.UsageStore, jsonlPath string, flushInterval, pollInterval time.Duration, logger *slog.Logger) *Tailer {
	offsetPath := filepath.Join(filepath.Dir(jsonlPath), ".tailer-offset")
	return &Tailer{
		store:         s,
		jsonlPath:     jsonlPath,
		offsetPath:    offsetPath,
		flushInterval: flushInterval,
		pollInterval:  pollInterval,
		logger:        logger.With("component", "tailer"),
		sessions:      make(map[string]*accumulator),
	}
}

// Run starts the tailer loop. Blocks until ctx is cancelled.
func (t *Tailer) Run(ctx context.Context) {
	t.loadOffset()

	flushTicker := time.NewTicker(t.flushInterval)
	pollTicker := time.NewTicker(t.pollInterval)
	defer flushTicker.Stop()
	defer pollTicker.Stop()

	// Initial read.
	t.readNewEntries()

	for {
		select {
		case <-ctx.Done():
			t.flush(context.Background()) // final flush
			return
		case <-pollTicker.C:
			t.readNewEntries()
		case <-flushTicker.C:
			t.flush(ctx)
		}
	}
}

// loadOffset reads the persisted file offset.
func (t *Tailer) loadOffset() {
	data, err := os.ReadFile(t.offsetPath)
	if err != nil {
		t.logger.Info("no offset file, starting from beginning", "path", t.offsetPath)
		return
	}
	off, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.logger.Warn("invalid offset file, starting from beginning", "error", err)
		return
	}
	t.offset = off
	t.logger.Info("resuming from offset", "offset", off)
}

// saveOffset persists the current file offset.
func (t *Tailer) saveOffset() {
	data := []byte(strconv.FormatInt(t.offset, 10))
	if err := os.WriteFile(t.offsetPath, data, 0644); err != nil {
		t.logger.Error("failed to save offset", "error", err)
	}
}

// readNewEntries reads new lines from the JSONL file since last offset.
func (t *Tailer) readNewEntries() {
	f, err := os.Open(t.jsonlPath)
	if err != nil {
		if !os.IsNotExist(err) {
			t.logger.Error("failed to open jsonl file", "error", err)
		}
		return
	}
	defer f.Close()

	// Detect truncation/rotation.
	info, err := f.Stat()
	if err != nil {
		t.logger.Error("failed to stat jsonl file", "error", err)
		return
	}
	if info.Size() < t.offset {
		t.logger.Warn("file truncated/rotated, resetting offset", "old_offset", t.offset, "new_size", info.Size())
		t.offset = 0
	}

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		t.logger.Error("failed to seek", "error", err)
		return
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	linesRead := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		t.offset += int64(len(line)) + 1 // +1 for newline
		linesRead++

		var entry jsonlEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}

		if entry.Stage != "usage" || entry.Usage == nil {
			continue
		}

		t.processEntry(&entry)
	}

	if err := scanner.Err(); err != nil {
		t.logger.Error("scanner error", "error", err)
	}

	if linesRead > 0 {
		t.saveOffset()
		t.logger.Debug("read new entries", "lines", linesRead)
	}
}

// processEntry accumulates a single usage entry.
func (t *Tailer) processEntry(entry *jsonlEntry) {
	agentID, label := parseSessionKey(entry.SessionKey)

	ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		ts = time.Now()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	acc, ok := t.sessions[entry.SessionID]
	if !ok {
		acc = &accumulator{
			agentID:      agentID,
			sessionLabel: label,
			provider:     entry.Provider,
			modelID:      entry.ModelID,
			firstSeenAt:  ts,
		}
		t.sessions[entry.SessionID] = acc
	}

	u := entry.Usage
	acc.inputTokens += u.Input
	acc.outputTokens += u.Output
	acc.cacheReadTokens += u.CacheRead
	acc.cacheWriteTokens += u.CacheWrite
	acc.totalTokens += u.TotalTokens
	acc.inputCostUSD += u.Cost.Input
	acc.outputCostUSD += u.Cost.Output
	acc.cacheReadCostUSD += u.Cost.CacheRead
	acc.cacheWriteCostUSD += u.Cost.CacheWrite
	acc.totalCostUSD += u.Cost.Total
	acc.requestCount++
	acc.lastSeenAt = ts
	acc.modelID = entry.ModelID // use most recent model
	acc.dirty = true
}

// flush writes all dirty accumulators to the database.
func (t *Tailer) flush(ctx context.Context) {
	t.mu.Lock()
	// Snapshot dirty sessions.
	var toFlush []struct {
		sessionID string
		usage     store.TokenUsage
	}
	for sid, acc := range t.sessions {
		if !acc.dirty {
			continue
		}
		toFlush = append(toFlush, struct {
			sessionID string
			usage     store.TokenUsage
		}{
			sessionID: sid,
			usage: store.TokenUsage{
				SessionID:        sid,
				AgentID:          acc.agentID,
				SessionLabel:     acc.sessionLabel,
				Provider:         acc.provider,
				ModelID:          acc.modelID,
				InputTokens:      acc.inputTokens,
				OutputTokens:     acc.outputTokens,
				CacheReadTokens:  acc.cacheReadTokens,
				CacheWriteTokens: acc.cacheWriteTokens,
				TotalTokens:      acc.totalTokens,
				InputCostUSD:     acc.inputCostUSD,
				OutputCostUSD:    acc.outputCostUSD,
				CacheReadCostUSD: acc.cacheReadCostUSD,
				CacheWriteCostUSD: acc.cacheWriteCostUSD,
				TotalCostUSD:     acc.totalCostUSD,
				RequestCount:     acc.requestCount,
				FirstSeenAt:      acc.firstSeenAt,
				LastSeenAt:       acc.lastSeenAt,
			},
		})
		acc.dirty = false
	}
	t.mu.Unlock()

	if len(toFlush) == 0 {
		return
	}

	flushed := 0
	for _, item := range toFlush {
		if err := t.store.UpsertUsage(ctx, &item.usage); err != nil {
			t.logger.Error("failed to flush usage", "session_id", item.sessionID, "error", err)
			// Mark dirty again so we retry.
			t.mu.Lock()
			if acc, ok := t.sessions[item.sessionID]; ok {
				acc.dirty = true
			}
			t.mu.Unlock()
			continue
		}
		flushed++
	}

	t.logger.Info(fmt.Sprintf("flushed %d/%d sessions to database", flushed, len(toFlush)))
}

// parseSessionKey extracts agent_id and label from "agent:{agentId}:{label}" format.
func parseSessionKey(key string) (agentID, label string) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 2 {
		return "unknown", ""
	}
	// Format: "agent:{agentId}:{label}"
	if parts[0] != "agent" {
		return parts[0], ""
	}
	agentID = parts[1]
	if len(parts) == 3 {
		label = parts[2]
	}
	return agentID, label
}
