package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"warren/internal/policy"
	"warren/internal/services"
)

func TestDeregister(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	registry := services.NewRegistry(logger)
	p := New(registry, "", logger)

	target, _ := url.Parse("http://localhost:9999")
	pol := policy.NewUnmanaged()
	p.Register("test.example.com", "test", target, pol)

	// Should route.
	req := httptest.NewRequest("GET", "http://test.example.com/", nil)
	req.Host = "test.example.com"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	// Should get 502 (backend not actually running) not 404.
	if w.Code == http.StatusNotFound {
		t.Fatal("expected backend to be registered, got 404")
	}

	// Deregister.
	p.Deregister("test.example.com")

	// Should return 404 now.
	req = httptest.NewRequest("GET", "http://test.example.com/", nil)
	req.Host = "test.example.com"
	w = httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after deregister, got %d", w.Code)
	}
}
