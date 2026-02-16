package usage

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"warren/internal/store"
)

// Handler serves token usage API endpoints.
type Handler struct {
	store store.UsageStore
}

// NewHandler creates a new usage API handler.
func NewHandler(s store.UsageStore) *Handler {
	return &Handler{store: s}
}

// Register mounts the usage API routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/usage/summary", h.handleSummary)
	mux.HandleFunc("/api/usage/agent/", h.handleAgent)
	mux.HandleFunc("/api/usage/model/", h.handleModel)
	mux.HandleFunc("/api/usage/cost-efficiency/", h.handleCostEfficiency)
}

// handleSummary returns aggregate usage. GET /api/usage/summary?range=7d
func (h *Handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	since := parseSince(r.URL.Query().Get("range"))
	summary, err := h.store.GetSummary(r.Context(), since)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, summary)
}

// handleAgent returns per-agent usage. GET /api/usage/agent/{agent_id}?range=30d
func (h *Handler) handleAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	agentID := strings.TrimPrefix(r.URL.Path, "/api/usage/agent/")
	if agentID == "" {
		http.Error(w, `{"error":"agent_id required"}`, http.StatusBadRequest)
		return
	}

	since := parseSince(r.URL.Query().Get("range"))
	usage, err := h.store.GetAgentUsage(r.Context(), agentID, since)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, usage)
}

// handleModel returns per-model usage. GET /api/usage/model/{model_id}?range=30d
func (h *Handler) handleModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	modelID := strings.TrimPrefix(r.URL.Path, "/api/usage/model/")
	if modelID == "" {
		http.Error(w, `{"error":"model_id required"}`, http.StatusBadRequest)
		return
	}

	since := parseSince(r.URL.Query().Get("range"))
	usage, err := h.store.GetModelUsage(r.Context(), modelID, since)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, usage)
}

// handleCostEfficiency returns cost efficiency for Dispatch. GET /api/usage/cost-efficiency/{agent_id}
func (h *Handler) handleCostEfficiency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	agentID := strings.TrimPrefix(r.URL.Path, "/api/usage/cost-efficiency/")
	if agentID == "" {
		http.Error(w, `{"error":"agent_id required"}`, http.StatusBadRequest)
		return
	}

	ce, err := h.store.GetCostEfficiency(r.Context(), agentID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, ce)
}

// parseSince converts a range string like "7d", "24h", "30d" into a time.Time.
// Defaults to 7 days ago if unparseable.
func parseSince(rangeStr string) time.Time {
	if rangeStr == "" {
		return time.Now().AddDate(0, 0, -7)
	}

	rangeStr = strings.TrimSpace(rangeStr)
	if len(rangeStr) < 2 {
		return time.Now().AddDate(0, 0, -7)
	}

	unit := rangeStr[len(rangeStr)-1]
	valStr := rangeStr[:len(rangeStr)-1]

	val := 0
	for _, c := range valStr {
		if c < '0' || c > '9' {
			return time.Now().AddDate(0, 0, -7)
		}
		val = val*10 + int(c-'0')
	}

	switch unit {
	case 'h':
		return time.Now().Add(-time.Duration(val) * time.Hour)
	case 'd':
		return time.Now().AddDate(0, 0, -val)
	case 'w':
		return time.Now().AddDate(0, 0, -val*7)
	default:
		return time.Now().AddDate(0, 0, -7)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
