package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"warren/internal/store"
)

// mockStore implements store.UsageStore for testing.
type mockStore struct {
	summary        *store.UsageSummary
	agentUsage     *store.AgentUsageSummary
	modelUsage     *store.ModelUsageSummary
	costEfficiency *store.CostEfficiency
}

func (m *mockStore) UpsertUsage(_ context.Context, _ *store.TokenUsage) error { return nil }
func (m *mockStore) EnrichSession(_ context.Context, _, _, _ string, _ int64, _ int, _ []string) error {
	return nil
}
func (m *mockStore) GetSummary(_ context.Context, _ time.Time) (*store.UsageSummary, error) {
	return m.summary, nil
}
func (m *mockStore) GetAgentUsage(_ context.Context, _ string, _ time.Time) (*store.AgentUsageSummary, error) {
	return m.agentUsage, nil
}
func (m *mockStore) GetModelUsage(_ context.Context, _ string, _ time.Time) (*store.ModelUsageSummary, error) {
	return m.modelUsage, nil
}
func (m *mockStore) GetCostEfficiency(_ context.Context, _ string) (*store.CostEfficiency, error) {
	return m.costEfficiency, nil
}
func (m *mockStore) Close() {}

func TestHandleSummary(t *testing.T) {
	ms := &mockStore{
		summary: &store.UsageSummary{
			TotalTokens:   100000,
			TotalCostUSD:  5.50,
			TotalSessions: 10,
			TotalRequests: 150,
			ByAgent: []store.AgentUsageSummary{
				{AgentID: "main", TotalTokens: 80000, TotalCostUSD: 4.0, SessionCount: 5, RequestCount: 100},
			},
			ByModel: []store.ModelUsageSummary{
				{ModelID: "claude-opus-4-6", TotalTokens: 100000, TotalCostUSD: 5.50, SessionCount: 10, RequestCount: 150},
			},
		},
	}

	h := NewHandler(ms)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/usage/summary?range=7d", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result store.UsageSummary
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.TotalTokens != 100000 {
		t.Errorf("total_tokens = %d, want 100000", result.TotalTokens)
	}
	if result.TotalCostUSD != 5.50 {
		t.Errorf("total_cost_usd = %f, want 5.50", result.TotalCostUSD)
	}
	if len(result.ByAgent) != 1 {
		t.Errorf("by_agent count = %d, want 1", len(result.ByAgent))
	}
}

func TestHandleAgent(t *testing.T) {
	ms := &mockStore{
		agentUsage: &store.AgentUsageSummary{
			AgentID:      "main",
			TotalTokens:  50000,
			TotalCostUSD: 2.50,
			SessionCount: 5,
			RequestCount: 75,
		},
	}

	h := NewHandler(ms)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/usage/agent/main?range=30d", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result store.AgentUsageSummary
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.AgentID != "main" {
		t.Errorf("agent_id = %q, want main", result.AgentID)
	}
}

func TestHandleModel(t *testing.T) {
	ms := &mockStore{
		modelUsage: &store.ModelUsageSummary{
			ModelID:      "claude-opus-4-6",
			TotalTokens:  80000,
			TotalCostUSD: 4.0,
		},
	}

	h := NewHandler(ms)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/usage/model/claude-opus-4-6?range=7d", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result store.ModelUsageSummary
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.ModelID != "claude-opus-4-6" {
		t.Errorf("model_id = %q, want claude-opus-4-6", result.ModelID)
	}
}

func TestHandleCostEfficiency(t *testing.T) {
	ms := &mockStore{
		costEfficiency: &store.CostEfficiency{
			AgentID:       "main",
			AvgCostUSD:    0.05,
			AvgTokens:     5000,
			AvgDurationMs: 30000,
			SuccessRate:   0.95,
			SessionCount:  42,
		},
	}

	h := NewHandler(ms)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/usage/cost-efficiency/main", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result store.CostEfficiency
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.AgentID != "main" {
		t.Errorf("agent_id = %q, want main", result.AgentID)
	}
	if result.SuccessRate != 0.95 {
		t.Errorf("success_rate = %f, want 0.95", result.SuccessRate)
	}
}

func TestHandleSummaryMethodNotAllowed(t *testing.T) {
	h := NewHandler(&mockStore{})
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("POST", "/api/usage/summary", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleAgentEmpty(t *testing.T) {
	h := NewHandler(&mockStore{})
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/usage/agent/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestParseSince(t *testing.T) {
	tests := []struct {
		input    string
		wantAgo  time.Duration
		tolerance time.Duration
	}{
		{"7d", 7 * 24 * time.Hour, time.Minute},
		{"24h", 24 * time.Hour, time.Minute},
		{"30d", 30 * 24 * time.Hour, time.Minute},
		{"1w", 7 * 24 * time.Hour, time.Minute},
		{"", 7 * 24 * time.Hour, time.Minute},     // default
		{"bad", 7 * 24 * time.Hour, time.Minute},   // fallback
	}

	for _, tt := range tests {
		got := parseSince(tt.input)
		expected := time.Now().Add(-tt.wantAgo)
		diff := got.Sub(expected)
		if diff < -tt.tolerance || diff > tt.tolerance {
			t.Errorf("parseSince(%q): got %v ago, want ~%v ago", tt.input, time.Since(got), tt.wantAgo)
		}
	}
}

func TestSummaryDefaultRange(t *testing.T) {
	ms := &mockStore{
		summary: &store.UsageSummary{TotalTokens: 42},
	}

	h := NewHandler(ms)
	mux := http.NewServeMux()
	h.Register(mux)

	// No range param — should default to 7d.
	req := httptest.NewRequest("GET", "/api/usage/summary", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestContentTypeJSON(t *testing.T) {
	ms := &mockStore{
		summary: &store.UsageSummary{},
	}

	h := NewHandler(ms)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/usage/summary", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
