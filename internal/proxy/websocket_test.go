package proxy

import (
	"net/http"
	"testing"
)

func TestIsWebSocket(t *testing.T) {
	tests := []struct {
		name       string
		connection string
		upgrade    string
		want       bool
	}{
		{"standard", "upgrade", "websocket", true},
		{"capitalized", "Upgrade", "WebSocket", true},
		{"multi-token", "keep-alive, upgrade", "websocket", true},
		{"multi-token-spaces", "keep-alive , Upgrade", "websocket", true},
		{"no-upgrade-header", "upgrade", "", false},
		{"no-connection-header", "", "websocket", false},
		{"plain-request", "", "", false},
		{"wrong-upgrade", "upgrade", "h2c", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}}
			if tt.connection != "" {
				r.Header.Set("Connection", tt.connection)
			}
			if tt.upgrade != "" {
				r.Header.Set("Upgrade", tt.upgrade)
			}
			if got := IsWebSocket(r); got != tt.want {
				t.Errorf("IsWebSocket() = %v, want %v", got, tt.want)
			}
		})
	}
}
