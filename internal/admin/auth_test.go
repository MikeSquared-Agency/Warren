package admin

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"warren/internal/config"
	"warren/internal/events"
	"warren/internal/policy"
	"warren/internal/proxy"
	"warren/internal/services"
)

func testServerWithToken(t *testing.T, token string) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	emitter := events.NewEmitter(logger)
	registry := services.NewRegistry(logger)
	p := proxy.New(registry, "", logger)

	tmpFile, err := os.CreateTemp("", "warren-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })
	os.WriteFile(tmpFile.Name(), []byte("listen: \":8080\"\nagents: {}\n"), 0644)

	cfg := &config.Config{
		Listen:     ":8080",
		AdminToken: token,
		Agents:     make(map[string]*config.Agent),
	}

	return NewServer(
		make(map[string]AgentInfo),
		make(map[string]policy.Policy),
		make(map[string]context.CancelFunc),
		registry, emitter, nil, p, cfg, tmpFile.Name(),
		func() int64 { return 0 },
		nil, // no hermes client in tests
		nil, // no process tracker in tests
		logger,
	)
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	srv := testServerWithToken(t, "secret-token")
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/health", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("valid token: got %d, want 200", w.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	srv := testServerWithToken(t, "secret-token")
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("invalid token: got %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
	srv := testServerWithToken(t, "secret-token")
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("missing token: got %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_NoTokenConfigured(t *testing.T) {
	srv := testServerWithToken(t, "")
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/admin/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("no token configured: got %d, want 200 (open access)", w.Code)
	}
}

func TestAuthMiddleware_MalformedAuthHeader(t *testing.T) {
	srv := testServerWithToken(t, "secret-token")
	handler := srv.Handler()

	cases := []struct {
		name string
		auth string
	}{
		{"just token", "secret-token"},
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"empty bearer", "Bearer "},
		{"bearer lowercase", "bearer secret-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/health", nil)
			req.Header.Set("Authorization", tc.auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Errorf("%s: got %d, want 401", tc.name, w.Code)
			}
		})
	}
}
