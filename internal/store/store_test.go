package store

import (
	"testing"
	"time"
)

func TestTokenUsageZeroValues(t *testing.T) {
	u := &TokenUsage{}
	if u.SessionID != "" {
		t.Error("expected empty session ID")
	}
	if u.TotalTokens != 0 {
		t.Error("expected zero total tokens")
	}
	if u.TotalCostUSD != 0 {
		t.Error("expected zero total cost")
	}
}

func TestUsageSummaryDefaults(t *testing.T) {
	s := &UsageSummary{}
	if s.TotalSessions != 0 {
		t.Error("expected zero sessions")
	}
	if s.ByAgent != nil {
		t.Error("expected nil agent slice")
	}
	if s.ByModel != nil {
		t.Error("expected nil model slice")
	}
}

func TestCostEfficiencyFields(t *testing.T) {
	ce := &CostEfficiency{
		AgentID:       "main",
		AvgCostUSD:    0.05,
		AvgTokens:     5000,
		AvgDurationMs: 30000,
		SuccessRate:   0.95,
		SessionCount:  42,
	}
	if ce.AgentID != "main" {
		t.Errorf("agent_id = %q, want main", ce.AgentID)
	}
	if ce.SuccessRate != 0.95 {
		t.Errorf("success_rate = %f, want 0.95", ce.SuccessRate)
	}
}

func TestTokenUsagePopulated(t *testing.T) {
	now := time.Now()
	u := &TokenUsage{
		SessionID:        "sess-123",
		AgentID:          "main",
		SessionLabel:     "main",
		Provider:         "anthropic",
		ModelID:          "claude-opus-4-6",
		InputTokens:      100,
		OutputTokens:     200,
		CacheReadTokens:  5000,
		CacheWriteTokens: 300,
		TotalTokens:      5600,
		InputCostUSD:     0.001,
		OutputCostUSD:    0.005,
		CacheReadCostUSD: 0.025,
		CacheWriteCostUSD: 0.002,
		TotalCostUSD:     0.033,
		RequestCount:     3,
		FirstSeenAt:      now.Add(-time.Hour),
		LastSeenAt:       now,
	}
	if u.TotalTokens != 5600 {
		t.Errorf("total_tokens = %d, want 5600", u.TotalTokens)
	}
	if u.RequestCount != 3 {
		t.Errorf("request_count = %d, want 3", u.RequestCount)
	}
}
