package proxy

import (
	"testing"
	"time"
)

func TestTouchAndLastActivity(t *testing.T) {
	a := NewActivityTracker()
	a.Touch("test.com")
	last := a.LastActivity("test.com")
	if time.Since(last) > time.Second {
		t.Errorf("last activity too old: %v", last)
	}
}

func TestLastActivityZeroForUnknown(t *testing.T) {
	a := NewActivityTracker()
	last := a.LastActivity("unknown.com")
	if !last.IsZero() {
		t.Errorf("expected zero time, got %v", last)
	}
}
