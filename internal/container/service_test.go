package container

import (
	"context"
	"testing"
	"time"
)

// mockLifecycle implements Lifecycle for testing.
type mockLifecycle struct {
	started   bool
	stopped   bool
	restarted bool
	status    string
	statusErr error
	startErr  error
	stopErr   error
}

func (m *mockLifecycle) Start(_ context.Context, _ string) error {
	m.started = true
	return m.startErr
}

func (m *mockLifecycle) Stop(_ context.Context, _ string, _ time.Duration) error {
	m.stopped = true
	return m.stopErr
}

func (m *mockLifecycle) Restart(_ context.Context, _ string, _ time.Duration) error {
	m.restarted = true
	return nil
}

func (m *mockLifecycle) Status(_ context.Context, _ string) (string, error) {
	return m.status, m.statusErr
}

func TestLifecycleStart(t *testing.T) {
	m := &mockLifecycle{}
	if err := m.Start(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if !m.started {
		t.Error("expected started")
	}
}

func TestLifecycleStop(t *testing.T) {
	m := &mockLifecycle{}
	if err := m.Stop(context.Background(), "test", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if !m.stopped {
		t.Error("expected stopped")
	}
}

func TestLifecycleStatus(t *testing.T) {
	m := &mockLifecycle{status: "running"}
	s, err := m.Status(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if s != "running" {
		t.Errorf("status = %q, want running", s)
	}
}
