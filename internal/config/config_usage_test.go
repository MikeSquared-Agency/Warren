package config

import (
	"os"
	"testing"
	"time"
)

func TestUsageConfigDefaults(t *testing.T) {
	yaml := `
agents:
  a:
    hostname: a.example.com
    backend: http://localhost:3000
    policy: unmanaged
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Usage.FlushInterval != 30*time.Second {
		t.Errorf("usage flush_interval = %v, want 30s", cfg.Usage.FlushInterval)
	}
	if cfg.Usage.PollInterval != 5*time.Second {
		t.Errorf("usage poll_interval = %v, want 5s", cfg.Usage.PollInterval)
	}
	if cfg.Usage.JSONLPath == "" {
		t.Error("expected non-empty default jsonl_path")
	}
}

func TestUsageConfigFromYAML(t *testing.T) {
	yaml := `
database_url: "postgres://localhost:5432/warren"
usage:
  enabled: true
  jsonl_path: "/custom/path.jsonl"
  flush_interval: 60s
  poll_interval: 10s
agents:
  a:
    hostname: a.example.com
    backend: http://localhost:3000
    policy: unmanaged
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://localhost:5432/warren" {
		t.Errorf("database_url = %q", cfg.DatabaseURL)
	}
	if !cfg.Usage.Enabled {
		t.Error("expected usage.enabled = true")
	}
	if cfg.Usage.JSONLPath != "/custom/path.jsonl" {
		t.Errorf("usage.jsonl_path = %q", cfg.Usage.JSONLPath)
	}
	if cfg.Usage.FlushInterval != 60*time.Second {
		t.Errorf("usage.flush_interval = %v, want 60s", cfg.Usage.FlushInterval)
	}
	if cfg.Usage.PollInterval != 10*time.Second {
		t.Errorf("usage.poll_interval = %v, want 10s", cfg.Usage.PollInterval)
	}
}

func TestDatabaseURLEnvOverride(t *testing.T) {
	yaml := `
database_url: "postgres://from-yaml:5432/warren"
agents:
  a:
    hostname: a.example.com
    backend: http://localhost:3000
    policy: unmanaged
`
	path := writeTemp(t, yaml)

	os.Setenv("WARREN_DATABASE_URL", "postgres://from-env:5432/warren")
	defer os.Unsetenv("WARREN_DATABASE_URL")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://from-env:5432/warren" {
		t.Errorf("expected env override, got database_url = %q", cfg.DatabaseURL)
	}
}
