package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name:    "no agents",
			cfg:     &Config{Agents: map[string]*Agent{}},
			wantErr: "no agents defined",
		},
		{
			name: "missing hostname",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Backend: "http://x", Policy: "unmanaged"},
			}},
			wantErr: "missing hostname",
		},
		{
			name: "missing backend",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Policy: "unmanaged"},
			}},
			wantErr: "missing backend",
		},
		{
			name: "missing policy",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Backend: "http://x"},
			}},
			wantErr: "missing policy",
		},
		{
			name: "unknown policy",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Backend: "http://x", Policy: "magic"},
			}},
			wantErr: "unknown policy",
		},
		{
			name: "on-demand missing container name",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Backend: "http://x", Policy: "on-demand",
					Health: Health{URL: "http://x/h"}, Idle: IdleConfig{Timeout: time.Minute}},
			}},
			wantErr: "requires container.name",
		},
		{
			name: "on-demand missing health url",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Backend: "http://x", Policy: "on-demand",
					Container: Container{Name: "svc"}, Idle: IdleConfig{Timeout: time.Minute}},
			}},
			wantErr: "requires health.url",
		},
		{
			name: "always-on missing container name",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Backend: "http://x", Policy: "always-on",
					Health: Health{URL: "http://x/h"}},
			}},
			wantErr: "requires container.name",
		},
		{
			name: "always-on missing health url",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Backend: "http://x", Policy: "always-on",
					Container: Container{Name: "svc"}},
			}},
			wantErr: "requires health.url",
		},
		{
			name: "duplicate hostname",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "same.com", Backend: "http://x", Policy: "unmanaged"},
				"b": {Hostname: "same.com", Backend: "http://y", Policy: "unmanaged"},
			}},
			wantErr: "duplicate hostname",
		},
		{
			name: "duplicate via additional hostnames",
			cfg: &Config{Agents: map[string]*Agent{
				"a": {Hostname: "a.com", Hostnames: []string{"shared.com"}, Backend: "http://x", Policy: "unmanaged"},
				"b": {Hostname: "shared.com", Backend: "http://y", Policy: "unmanaged"},
			}},
			wantErr: "duplicate hostname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateSuccess(t *testing.T) {
	cfg := &Config{Agents: map[string]*Agent{
		"a": {Hostname: "a.com", Backend: "http://x", Policy: "unmanaged"},
	}}
	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
