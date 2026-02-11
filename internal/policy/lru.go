package policy

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// LRUManager tracks on-demand agents and evicts least-recently-used ones.
type LRUManager struct {
	mu       sync.RWMutex
	agents   map[string]*OnDemand
	activity ActivitySource
	logger   *slog.Logger
}

// NewLRUManager creates a new LRU eviction manager.
func NewLRUManager(activity ActivitySource, logger *slog.Logger) *LRUManager {
	return &LRUManager{
		agents:   make(map[string]*OnDemand),
		activity: activity,
		logger:   logger.With("component", "lru-manager"),
	}
}

// Register tracks an on-demand agent for LRU eviction.
func (l *LRUManager) Register(name string, pol *OnDemand, hostname string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agents[name] = pol
}

// Evict finds the least-recently-used ready on-demand agent and puts it to sleep.
// Returns the name of the evicted agent, or empty string if none eligible.
func (l *LRUManager) Evict(ctx context.Context) string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var (
		lruName string
		lruTime time.Time
		lruPol  *OnDemand
	)

	for name, pol := range l.agents {
		if pol.State() != "ready" {
			continue
		}
		last := l.activity.LastActivity(pol.hostname)
		if lruPol == nil || last.Before(lruTime) {
			lruName = name
			lruTime = last
			lruPol = pol
		}
	}

	if lruPol == nil {
		return ""
	}

	l.logger.Info("evicting least-recently-used agent", "agent", lruName, "last_activity", lruTime)
	lruPol.Sleep(ctx)
	return lruName
}

// EvictIfNeeded evicts LRU agents until at most maxReady on-demand agents are awake.
// If maxReady is 0, no eviction is performed.
func (l *LRUManager) EvictIfNeeded(ctx context.Context, maxReady int) {
	if maxReady <= 0 {
		return
	}

	for {
		ready := l.countReady()
		if ready <= maxReady {
			return
		}
		evicted := l.Evict(ctx)
		if evicted == "" {
			return // no more eligible agents
		}
	}
}

func (l *LRUManager) countReady() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	count := 0
	for _, pol := range l.agents {
		if pol.State() == "ready" {
			count++
		}
	}
	return count
}
