package services

import (
	"log/slog"
	"os"
	"testing"
)

func testRegistry() *Registry {
	return NewRegistry(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestRegisterAndLookup(t *testing.T) {
	r := testRegistry()
	r.Register("a.com", "http://localhost:3000", "agent-a")
	svc, ok := r.Lookup("a.com")
	if !ok {
		t.Fatal("expected lookup to succeed")
	}
	if svc.Target != "http://localhost:3000" {
		t.Errorf("target = %q", svc.Target)
	}
	if svc.Agent != "agent-a" {
		t.Errorf("agent = %q", svc.Agent)
	}
}

func TestLookupMissing(t *testing.T) {
	r := testRegistry()
	_, ok := r.Lookup("nope.com")
	if ok {
		t.Error("expected lookup to fail")
	}
}

func TestDeregister(t *testing.T) {
	r := testRegistry()
	r.Register("a.com", "http://x", "a")
	r.Deregister("a.com")
	_, ok := r.Lookup("a.com")
	if ok {
		t.Error("expected lookup to fail after deregister")
	}
}

func TestDeregisterByAgent(t *testing.T) {
	r := testRegistry()
	r.Register("a.com", "http://x", "agent1")
	r.Register("b.com", "http://y", "agent1")
	r.Register("c.com", "http://z", "agent2")
	r.DeregisterByAgent("agent1")

	if _, ok := r.Lookup("a.com"); ok {
		t.Error("a.com should be gone")
	}
	if _, ok := r.Lookup("b.com"); ok {
		t.Error("b.com should be gone")
	}
	if _, ok := r.Lookup("c.com"); !ok {
		t.Error("c.com should still exist")
	}
}

func TestList(t *testing.T) {
	r := testRegistry()
	r.Register("a.com", "http://x", "a")
	r.Register("b.com", "http://y", "b")
	list := r.List()
	if len(list) != 2 {
		t.Errorf("list len = %d, want 2", len(list))
	}
}

func TestDuplicateHostnameOverwrites(t *testing.T) {
	r := testRegistry()
	r.Register("a.com", "http://old", "a")
	r.Register("a.com", "http://new", "b")
	svc, _ := r.Lookup("a.com")
	if svc.Target != "http://new" {
		t.Errorf("target = %q, want http://new", svc.Target)
	}
	if svc.Agent != "b" {
		t.Errorf("agent = %q, want b", svc.Agent)
	}
}
