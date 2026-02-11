package container

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckHealthHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	if err := CheckHealth(context.Background(), srv.URL); err != nil {
		t.Errorf("expected healthy, got %v", err)
	}
}

func TestCheckHealthUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if err := CheckHealth(context.Background(), srv.URL); err == nil {
		t.Error("expected error for 500 status")
	}
}

func TestCheckHealthUnreachable(t *testing.T) {
	if err := CheckHealth(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Error("expected error for unreachable server")
	}
}
