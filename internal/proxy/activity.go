package proxy

import (
	"sync"
	"time"
)

type ActivityTracker struct {
	mu       sync.RWMutex
	activity map[string]time.Time // hostname → last activity
}

func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{
		activity: make(map[string]time.Time),
	}
}

func (a *ActivityTracker) Touch(hostname string) {
	a.mu.Lock()
	a.activity[hostname] = time.Now()
	a.mu.Unlock()
}

func (a *ActivityTracker) LastActivity(hostname string) time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.activity[hostname]
}
