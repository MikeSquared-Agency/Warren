package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidConfig(t *testing.T) {
	yaml := `
listen: ":9090"
agents:
  test-agent:
    hostname: test.example.com
    backend: http://localhost:3000
    policy: unmanaged
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":9090" {
		t.Errorf("listen = %q, want %q", cfg.Listen, ":9090")
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("agents count = %d, want 1", len(cfg.Agents))
	}
	a := cfg.Agents["test-agent"]
	if a.Hostname != "test.example.com" {
		t.Errorf("hostname = %q, want %q", a.Hostname, "test.example.com")
	}
}

func TestDefaultsApplied(t *testing.T) {
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
	if cfg.Listen != ":8080" {
		t.Errorf("default listen = %q, want %q", cfg.Listen, ":8080")
	}
	if cfg.Defaults.HealthCheckInterval != 30*time.Second {
		t.Errorf("default health_check_interval = %v, want 30s", cfg.Defaults.HealthCheckInterval)
	}
	a := cfg.Agents["a"]
	if a.Health.CheckInterval != 30*time.Second {
		t.Errorf("agent health check_interval = %v, want 30s", a.Health.CheckInterval)
	}
	if a.Health.StartupTimeout != 60*time.Second {
		t.Errorf("agent startup_timeout = %v, want 60s", a.Health.StartupTimeout)
	}
	if a.Health.MaxFailures != 3 {
		t.Errorf("agent max_failures = %d, want 3", a.Health.MaxFailures)
	}
	if a.Idle.DrainTimeout != 30*time.Second {
		t.Errorf("agent drain_timeout = %v, want 30s", a.Idle.DrainTimeout)
	}
}

func TestOnDemandIdleTimeoutDefault(t *testing.T) {
	yaml := `
agents:
  a:
    hostname: a.example.com
    backend: http://localhost:3000
    policy: on-demand
    container:
      name: my-svc
    health:
      url: http://localhost:3000/health
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := cfg.Agents["a"]
	if a.Idle.Timeout != 30*time.Minute {
		t.Errorf("on-demand default idle timeout = %v, want 30m", a.Idle.Timeout)
	}
}

func TestMultipleHostnames(t *testing.T) {
	yaml := `
agents:
  a:
    hostname: a.example.com
    hostnames:
      - a2.example.com
      - a3.example.com
    backend: http://localhost:3000
    policy: unmanaged
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := cfg.Agents["a"]
	if len(a.Hostnames) != 2 {
		t.Errorf("hostnames count = %d, want 2", len(a.Hostnames))
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
