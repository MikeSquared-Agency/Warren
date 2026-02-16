package store

import (
	"context"
	"time"
)

// TokenUsage represents a session's accumulated token usage.
type TokenUsage struct {
	SessionID       string
	AgentID         string
	SessionLabel    string
	Provider        string
	ModelID         string
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	CacheWriteTokens int64
	TotalTokens     int64
	InputCostUSD    float64
	OutputCostUSD   float64
	CacheReadCostUSD  float64
	CacheWriteCostUSD float64
	TotalCostUSD    float64
	RequestCount    int
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
}

// UsageSummary is the response for the summary endpoint.
type UsageSummary struct {
	TotalTokens    int64              `json:"total_tokens"`
	TotalCostUSD   float64            `json:"total_cost_usd"`
	TotalSessions  int                `json:"total_sessions"`
	TotalRequests  int64              `json:"total_requests"`
	ByAgent        []AgentUsageSummary `json:"by_agent"`
	ByModel        []ModelUsageSummary `json:"by_model"`
}

// AgentUsageSummary is per-agent breakdown within a summary.
type AgentUsageSummary struct {
	AgentID       string  `json:"agent_id"`
	TotalTokens   int64   `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	SessionCount  int     `json:"session_count"`
	RequestCount  int64   `json:"request_count"`
}

// ModelUsageSummary is per-model breakdown within a summary.
type ModelUsageSummary struct {
	ModelID       string  `json:"model_id"`
	TotalTokens   int64   `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	SessionCount  int     `json:"session_count"`
	RequestCount  int64   `json:"request_count"`
}

// CostEfficiency is what Dispatch queries for routing decisions.
type CostEfficiency struct {
	AgentID         string  `json:"agent_id"`
	AvgCostUSD      float64 `json:"avg_cost_usd"`
	AvgTokens       float64 `json:"avg_tokens"`
	AvgDurationMs   float64 `json:"avg_duration_ms"`
	SuccessRate     float64 `json:"success_rate"`
	SessionCount    int     `json:"session_count"`
}

// UsageStore defines the interface for token usage persistence.
type UsageStore interface {
	// UpsertUsage inserts or updates token/cost fields for a session.
	// Does NOT overwrite enrichment fields (task_id, duration_ms, exit_code, etc.).
	UpsertUsage(ctx context.Context, usage *TokenUsage) error

	// EnrichSession updates enrichment fields from CC sidecar events.
	// Creates a stub row if the session doesn't exist yet.
	EnrichSession(ctx context.Context, sessionID, agentID, taskID string, durationMs int64, exitCode int, filesChanged []string) error

	// GetSummary returns aggregate usage since the given time.
	GetSummary(ctx context.Context, since time.Time) (*UsageSummary, error)

	// GetAgentUsage returns usage for a specific agent since the given time.
	GetAgentUsage(ctx context.Context, agentID string, since time.Time) (*AgentUsageSummary, error)

	// GetModelUsage returns usage for a specific model since the given time.
	GetModelUsage(ctx context.Context, modelID string, since time.Time) (*ModelUsageSummary, error)

	// GetCostEfficiency returns cost efficiency metrics for routing decisions.
	GetCostEfficiency(ctx context.Context, agentID string) (*CostEfficiency, error)

	// Close closes the underlying connection pool.
	Close()
}
