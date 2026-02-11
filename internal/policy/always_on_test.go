package policy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"warren/internal/events"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestAlwaysOnStartingToReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	var readyCount int32
	emitter.OnEvent(func(ev events.Event) {
		if ev.Type == events.AgentReady {
			atomic.AddInt32(&readyCount, 1)
		}
	})

	ao := NewAlwaysOn(AlwaysOnConfig{
		Agent:         "test",
		HealthURL:     srv.URL,
		CheckInterval: 50 * time.Millisecond,
		MaxFailures:   3,
	}, emitter, quietLogger())

	if ao.State() != "starting" {
		t.Errorf("initial state = %q, want starting", ao.State())
	}

	ctx, cancel := context.WithCancel(context.Background())
	go ao.Start(ctx)
	defer cancel()

	// Wait for ready
	deadline := time.After(2 * time.Second)
	for ao.State() != "ready" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for ready")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if atomic.LoadInt32(&readyCount) < 1 {
		t.Error("expected AgentReady event")
	}
}

func TestAlwaysOnReadyToDegraded(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	var degradedCount int32
	emitter.OnEvent(func(ev events.Event) {
		if ev.Type == events.AgentDegraded {
			atomic.AddInt32(&degradedCount, 1)
		}
	})

	ao := NewAlwaysOn(AlwaysOnConfig{
		Agent:         "test",
		HealthURL:     srv.URL,
		CheckInterval: 50 * time.Millisecond,
		MaxFailures:   2,
	}, emitter, quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go ao.Start(ctx)
	defer cancel()

	// Wait for ready
	for ao.State() != "ready" {
		time.Sleep(10 * time.Millisecond)
	}

	// Make unhealthy
	healthy.Store(false)

	deadline := time.After(2 * time.Second)
	for ao.State() != "degraded" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for degraded")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if atomic.LoadInt32(&degradedCount) < 1 {
		t.Error("expected AgentDegraded event")
	}
}

func TestAlwaysOnDegradedToReady(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	ao := NewAlwaysOn(AlwaysOnConfig{
		Agent:         "test",
		HealthURL:     srv.URL,
		CheckInterval: 50 * time.Millisecond,
		MaxFailures:   2,
	}, emitter, quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go ao.Start(ctx)
	defer cancel()

	// Ready → degraded
	for ao.State() != "ready" {
		time.Sleep(10 * time.Millisecond)
	}
	healthy.Store(false)
	for ao.State() != "degraded" {
		time.Sleep(10 * time.Millisecond)
	}

	// Recover
	healthy.Store(true)
	deadline := time.After(2 * time.Second)
	for ao.State() != "ready" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for recovery")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestAlwaysOnReconfigure(t *testing.T) {
	emitter := events.NewEmitter(quietLogger())
	ao := NewAlwaysOn(AlwaysOnConfig{
		Agent:         "test",
		HealthURL:     "http://localhost:1",
		CheckInterval: time.Second,
		MaxFailures:   3,
	}, emitter, quietLogger())

	ao.Reconfigure(5*time.Second, 10)
	ao.mu.RLock()
	if ao.checkInterval != 5*time.Second {
		t.Errorf("checkInterval = %v", ao.checkInterval)
	}
	if ao.maxFailures != 10 {
		t.Errorf("maxFailures = %d", ao.maxFailures)
	}
	ao.mu.RUnlock()
}
