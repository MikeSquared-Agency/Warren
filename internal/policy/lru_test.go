package policy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"warren/internal/events"
)

func makeLRUAgent(t *testing.T, name, hostname string, activity *mockActivity, healthURL string) *OnDemand {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	mgr := &mockLifecycle{status: "running"}
	ws := &mockWSSource{}

	od := NewOnDemand(mgr, OnDemandConfig{
		Agent:              name,
		ContainerName:      name + "-svc",
		HealthURL:          healthURL,
		Hostname:           hostname,
		CheckInterval:      time.Hour, // don't actually tick
		StartupTimeout:     time.Hour,
		IdleTimeout:        time.Hour,
		MaxFailures:        3,
		MaxRestartAttempts: 2,
	}, activity, ws, emitter, logger)

	// Force state to ready for testing
	od.mu.Lock()
	od.state = "ready"
	od.mu.Unlock()

	return od
}

func TestLRUEvictsLeastRecentlyActive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	activity := newMockActivity()

	// Touch agent-a first (older), then agent-b (newer)
	activity.Touch("a.com")
	time.Sleep(10 * time.Millisecond)
	activity.Touch("b.com")

	agentA := makeLRUAgent(t, "agent-a", "a.com", activity, srv.URL)
	agentB := makeLRUAgent(t, "agent-b", "b.com", activity, srv.URL)

	lru := NewLRUManager(activity, logger)
	lru.Register("agent-a", agentA, "a.com")
	lru.Register("agent-b", agentB, "b.com")

	evicted := lru.Evict(context.Background())
	if evicted != "agent-a" {
		t.Errorf("evicted = %q, want agent-a (least recently active)", evicted)
	}
}

func TestLRUEvictIfNeededRespectsThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	activity := newMockActivity()

	activity.Touch("a.com")
	time.Sleep(5 * time.Millisecond)
	activity.Touch("b.com")
	time.Sleep(5 * time.Millisecond)
	activity.Touch("c.com")

	agentA := makeLRUAgent(t, "a", "a.com", activity, srv.URL)
	agentB := makeLRUAgent(t, "b", "b.com", activity, srv.URL)
	agentC := makeLRUAgent(t, "c", "c.com", activity, srv.URL)

	lru := NewLRUManager(activity, logger)
	lru.Register("a", agentA, "a.com")
	lru.Register("b", agentB, "b.com")
	lru.Register("c", agentC, "c.com")

	// 3 ready, max 2 → should evict 1
	lru.EvictIfNeeded(context.Background(), 2)

	ready := 0
	for _, od := range []*OnDemand{agentA, agentB, agentC} {
		if od.State() == "ready" {
			ready++
		}
	}
	if ready != 2 {
		t.Errorf("ready count = %d, want 2", ready)
	}
}

func TestLRUNoEvictionUnderThreshold(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	activity := newMockActivity()
	lru := NewLRUManager(activity, logger)

	// No agents registered, maxReady=5 → nothing happens
	lru.EvictIfNeeded(context.Background(), 5)
}
