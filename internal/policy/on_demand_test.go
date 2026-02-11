package policy

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"warren/internal/container"
	"warren/internal/events"
)

type mockLifecycle struct {
	startCalled   int32
	stopCalled    int32
	restartCalled int32
	status        string
	startErr      error
	restartErr    error
}

func (m *mockLifecycle) Start(_ context.Context, _ string) error {
	atomic.AddInt32(&m.startCalled, 1)
	return m.startErr
}
func (m *mockLifecycle) Stop(_ context.Context, _ string, _ time.Duration) error {
	atomic.AddInt32(&m.stopCalled, 1)
	return nil
}
func (m *mockLifecycle) Restart(_ context.Context, _ string, _ time.Duration) error {
	atomic.AddInt32(&m.restartCalled, 1)
	return m.restartErr
}
func (m *mockLifecycle) Status(_ context.Context, _ string) (string, error) {
	return m.status, nil
}

// Ensure mockLifecycle implements container.Lifecycle
var _ container.Lifecycle = (*mockLifecycle)(nil)

type mockWSSource struct{ count int64 }

func (m *mockWSSource) Count(_ string) int64 { return atomic.LoadInt64(&m.count) }

// mockActivity implements ActivitySource for testing without importing proxy.
type mockActivity struct {
	mu       sync.Mutex
	activity map[string]time.Time
}

func newMockActivity() *mockActivity {
	return &mockActivity{activity: make(map[string]time.Time)}
}
func (m *mockActivity) Touch(hostname string) {
	m.mu.Lock()
	m.activity[hostname] = time.Now()
	m.mu.Unlock()
}
func (m *mockActivity) LastActivity(hostname string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activity[hostname]
}

func newTestOnDemand(healthURL string, mgr *mockLifecycle) (*OnDemand, *events.Emitter) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	activity := newMockActivity()
	ws := &mockWSSource{}

	od := NewOnDemand(mgr, OnDemandConfig{
		Agent:              "test",
		ContainerName:      "test-svc",
		HealthURL:          healthURL,
		Hostname:           "test.com",
		CheckInterval:      50 * time.Millisecond,
		StartupTimeout:     5 * time.Second,
		IdleTimeout:        200 * time.Millisecond,
		MaxFailures:        2,
		MaxRestartAttempts: 2,
	}, activity, ws, emitter, logger)

	return od, emitter
}

func TestOnDemandWakeFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	mgr := &mockLifecycle{status: "exited"}
	od, _ := newTestOnDemand(srv.URL, mgr)
	od.SetInitialState(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go od.Start(ctx)

	// Should start sleeping
	time.Sleep(50 * time.Millisecond)
	if s := od.State(); s != "sleeping" {
		t.Fatalf("state = %q, want sleeping", s)
	}

	// Wake it
	od.OnRequest()

	// Wait for ready
	deadline := time.After(3 * time.Second)
	for od.State() != "ready" {
		select {
		case <-deadline:
			t.Fatalf("timed out, state = %q", od.State())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	if atomic.LoadInt32(&mgr.startCalled) < 1 {
		t.Error("expected Start to be called")
	}
}

func TestOnDemandIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	mgr := &mockLifecycle{status: "exited"}
	od, _ := newTestOnDemand(srv.URL, mgr)
	od.SetInitialState(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go od.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	od.OnRequest()

	// Wait for ready
	for od.State() != "ready" {
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for idle timeout → sleeping
	deadline := time.After(3 * time.Second)
	for od.State() != "sleeping" {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for sleep, state = %q", od.State())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	if atomic.LoadInt32(&mgr.stopCalled) < 1 {
		t.Error("expected Stop to be called")
	}
}

func TestOnDemandWSPreventsIdleSleep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	activity := newMockActivity()
	ws := &mockWSSource{count: 1} // active WS connection

	mgr := &mockLifecycle{status: "exited"}
	od := NewOnDemand(mgr, OnDemandConfig{
		Agent:              "test",
		ContainerName:      "test-svc",
		HealthURL:          srv.URL,
		Hostname:           "test.com",
		CheckInterval:      50 * time.Millisecond,
		StartupTimeout:     5 * time.Second,
		IdleTimeout:        150 * time.Millisecond,
		MaxFailures:        3,
		MaxRestartAttempts: 2,
	}, activity, ws, emitter, logger)
	od.SetInitialState(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go od.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	od.OnRequest()

	for od.State() != "ready" {
		time.Sleep(20 * time.Millisecond)
	}

	// Wait longer than idle timeout — should NOT sleep because WS is active
	time.Sleep(400 * time.Millisecond)
	if od.State() != "ready" {
		t.Errorf("state = %q, want ready (WS should prevent sleep)", od.State())
	}
}

func TestOnDemandStartupTimeout(t *testing.T) {
	// Health always fails
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	activity := newMockActivity()
	ws := &mockWSSource{}

	mgr := &mockLifecycle{status: "exited"}
	od := NewOnDemand(mgr, OnDemandConfig{
		Agent:              "test",
		ContainerName:      "test-svc",
		HealthURL:          srv.URL,
		Hostname:           "test.com",
		CheckInterval:      50 * time.Millisecond,
		StartupTimeout:     500 * time.Millisecond,
		IdleTimeout:        30 * time.Minute,
		MaxFailures:        3,
		MaxRestartAttempts: 2,
	}, activity, ws, emitter, logger)
	od.SetInitialState(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go od.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	od.OnRequest()

	// Wait for starting
	for od.State() != "starting" {
		time.Sleep(20 * time.Millisecond)
	}

	// Should timeout and go back to sleeping
	deadline := time.After(3 * time.Second)
	for od.State() != "sleeping" {
		select {
		case <-deadline:
			t.Fatalf("timed out, state = %q", od.State())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestOnDemandSetInitialStateRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	mgr := &mockLifecycle{status: "running"}
	od, _ := newTestOnDemand(srv.URL, mgr)
	od.SetInitialState(true) // container already running

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go od.Start(ctx)

	// Should go to ready without needing wake
	deadline := time.After(3 * time.Second)
	for od.State() != "ready" {
		select {
		case <-deadline:
			t.Fatalf("timed out, state = %q", od.State())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Start should NOT have been called (already running)
	if atomic.LoadInt32(&mgr.startCalled) != 0 {
		t.Error("Start should not be called when container already running")
	}
}
