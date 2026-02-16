package config

import (
	"testing"
	"time"
)

func TestPicoClawConfigDefaults(t *testing.T) {
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

	if cfg.PicoClaw.Binary != "picoclaw" {
		t.Errorf("picoclaw.binary = %q, want picoclaw", cfg.PicoClaw.Binary)
	}
	if cfg.PicoClaw.MissionBaseDir != "/tmp/picoclaw-missions" {
		t.Errorf("picoclaw.mission_base_dir = %q, want /tmp/picoclaw-missions", cfg.PicoClaw.MissionBaseDir)
	}
	if cfg.PicoClaw.DefaultTimeout != 5*time.Minute {
		t.Errorf("picoclaw.default_timeout = %v, want 5m", cfg.PicoClaw.DefaultTimeout)
	}
	if cfg.PicoClaw.MaxConcurrent != 20 {
		t.Errorf("picoclaw.max_concurrent = %d, want 20", cfg.PicoClaw.MaxConcurrent)
	}
}

func TestPicoClawConfigFromYAML(t *testing.T) {
	yaml := `
picoclaw:
  binary: /usr/local/bin/picoclaw
  mission_base_dir: /data/missions
  default_timeout: 10m
  max_concurrent: 5
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

	if cfg.PicoClaw.Binary != "/usr/local/bin/picoclaw" {
		t.Errorf("picoclaw.binary = %q", cfg.PicoClaw.Binary)
	}
	if cfg.PicoClaw.MissionBaseDir != "/data/missions" {
		t.Errorf("picoclaw.mission_base_dir = %q", cfg.PicoClaw.MissionBaseDir)
	}
	if cfg.PicoClaw.DefaultTimeout != 10*time.Minute {
		t.Errorf("picoclaw.default_timeout = %v, want 10m", cfg.PicoClaw.DefaultTimeout)
	}
	if cfg.PicoClaw.MaxConcurrent != 5 {
		t.Errorf("picoclaw.max_concurrent = %d, want 5", cfg.PicoClaw.MaxConcurrent)
	}
}
