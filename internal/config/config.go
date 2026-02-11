package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen   string            `yaml:"listen"`
	Defaults Defaults          `yaml:"defaults"`
	Agents   map[string]*Agent `yaml:"agents"`
}

type Defaults struct {
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
}

type Agent struct {
	Hostname  string   `yaml:"hostname"`
	Hostnames []string `yaml:"hostnames"` // additional hostnames
	Backend   string   `yaml:"backend"`
	Policy    string    `yaml:"policy"`
	Container Container `yaml:"container"`
	Health    Health    `yaml:"health"`
	Idle      IdleConfig `yaml:"idle"`
}

type IdleConfig struct {
	Timeout      time.Duration `yaml:"timeout"`
	DrainTimeout time.Duration `yaml:"drain_timeout"`
}

type Container struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

type Health struct {
	URL                string        `yaml:"url"`
	CheckInterval      time.Duration `yaml:"check_interval"`
	StartupTimeout     time.Duration `yaml:"startup_timeout"`
	MaxFailures        int           `yaml:"max_failures"`
	MaxRestartAttempts int           `yaml:"max_restart_attempts"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.Defaults.HealthCheckInterval == 0 {
		cfg.Defaults.HealthCheckInterval = 30 * time.Second
	}

	for _, agent := range cfg.Agents {
		if agent.Health.CheckInterval == 0 {
			agent.Health.CheckInterval = cfg.Defaults.HealthCheckInterval
		}
		if agent.Health.StartupTimeout == 0 {
			agent.Health.StartupTimeout = 60 * time.Second
		}
		if agent.Health.MaxFailures == 0 {
			agent.Health.MaxFailures = 3
		}
		if agent.Health.MaxRestartAttempts == 0 {
			agent.Health.MaxRestartAttempts = 10
		}
		if agent.Policy == "on-demand" && agent.Idle.Timeout == 0 {
			agent.Idle.Timeout = 30 * time.Minute
		}
		if agent.Idle.DrainTimeout == 0 {
			agent.Idle.DrainTimeout = 30 * time.Second
		}
	}
}
