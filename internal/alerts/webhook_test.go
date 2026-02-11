package alerts

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"warren/internal/config"
	"warren/internal/events"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestWebhookFiresOnMatchingEvent(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	alerter := NewWebhookAlerter([]config.WebhookConfig{
		{URL: srv.URL, Events: []string{events.AgentReady}},
	}, quietLogger())
	alerter.RegisterEventHandler(emitter)

	emitter.Emit(events.Event{Type: events.AgentReady, Agent: "test"})
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("webhook called %d times, want 1", atomic.LoadInt32(&called))
	}
}

func TestWebhookDoesNotFireOnNonMatchingEvent(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	alerter := NewWebhookAlerter([]config.WebhookConfig{
		{URL: srv.URL, Events: []string{events.AgentReady}},
	}, quietLogger())
	alerter.RegisterEventHandler(emitter)

	emitter.Emit(events.Event{Type: events.AgentSleep, Agent: "test"})
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("webhook called %d times, want 0", atomic.LoadInt32(&called))
	}
}

func TestWebhookSendsCorrectJSON(t *testing.T) {
	gotEvent := make(chan events.Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev events.Event
		json.NewDecoder(r.Body).Decode(&ev)
		gotEvent <- ev
		w.WriteHeader(200)
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	alerter := NewWebhookAlerter([]config.WebhookConfig{
		{URL: srv.URL},
	}, quietLogger())
	alerter.RegisterEventHandler(emitter)

	emitter.Emit(events.Event{Type: events.AgentWake, Agent: "my-agent"})

	select {
	case ev := <-gotEvent:
		if ev.Type != events.AgentWake {
			t.Errorf("type = %q, want %q", ev.Type, events.AgentWake)
		}
		if ev.Agent != "my-agent" {
			t.Errorf("agent = %q", ev.Agent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

func TestWebhookCustomHeaders(t *testing.T) {
	gotAuth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	emitter := events.NewEmitter(quietLogger())
	alerter := NewWebhookAlerter([]config.WebhookConfig{
		{URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}},
	}, quietLogger())
	alerter.RegisterEventHandler(emitter)

	emitter.Emit(events.Event{Type: events.AgentReady, Agent: "test"})

	select {
	case auth := <-gotAuth:
		if auth != "Bearer secret" {
			t.Errorf("auth header = %q, want 'Bearer secret'", auth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}
