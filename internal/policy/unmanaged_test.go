package policy

import (
	"context"
	"testing"
	"time"
)

func TestUnmanagedStateReady(t *testing.T) {
	u := NewUnmanaged()
	if s := u.State(); s != "ready" {
		t.Errorf("state = %q, want ready", s)
	}
}

func TestUnmanagedStartBlocksUntilCancel(t *testing.T) {
	u := NewUnmanaged()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		u.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

func TestUnmanagedOnRequestNoop(t *testing.T) {
	u := NewUnmanaged()
	u.OnRequest() // should not panic
}
