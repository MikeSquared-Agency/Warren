package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen         string            `yaml:"listen"`
	AdminListen    string            `yaml:"admin_listen"` // e.g. ":9090", empty = disabled
	AdminToken     string            `yaml:"admin_token"`  // bearer token for admin API auth
	ProxyToken     string            `yaml:"proxy_token"`  // bearer token for proxy port auth
	Defaults       Defaults          `yaml:"defaults"`
	Agents         map[string]*Agent `yaml:"agents"`
	Webhooks       []WebhookConfig   `yaml:"webhooks"`
	MaxReadyAgents int               `yaml:"max_ready_agents"` // 0 = unlimited
	Hermes         HermesConfig      `yaml:"hermes"`
	Alexandria     AlexandriaConfig  `yaml:"alexandria"`
	SSH            SSHConfig         `yaml:"ssh"`
	PicoClaw       PicoClawConfig    `yaml:"picoclaw"`
}

type PicoClawConfig struct {
	Binary         string        `yaml:"binary"`           // default: "picoclaw"
	MissionBaseDir string        `yaml:"mission_base_dir"` // default: "/tmp/picoclaw-missions"
	DefaultTimeout time.Duration `yaml:"default_timeout"`  // default: 5m
	MaxConcurrent  int           `yaml:"max_concurrent"`   // default: 20
}

type AlexandriaConfig struct {
	Enabled bool          `yaml:"enabled"`
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout"`
}

type SSHConfig struct {
	Enabled            bool   `yaml:"enabled"`
	AlexandriaURL      string `yaml:"alexandria_url"`
	AuthorizedKeysPath string `yaml:"authorized_keys_path"`
}

type HermesConfig struct {
	Enabled        bool          `yaml:"enabled"`
	URL            string        `yaml:"url"`
	Token          string        `yaml:"token"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	ReconnectWait  time.Duration `yaml:"reconnect_wait"`
	MaxReconnects  int           `yaml:"max_reconnects"`
}

type WebhookConfig struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Events  []string          `yaml:"events"`
}

type Defaults struct {
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
}

type AgentHermes struct {
	Enabled bool `yaml:"enabled"` // default: true
}

type Agent struct {
	Hermes    AgentHermes `yaml:"hermes"`
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
	WakeCooldown time.Duration `yaml:"wake_cooldown"`
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

// Save writes the config back to the given file path.
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
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

	if cfg.Hermes.URL == "" {
		cfg.Hermes.URL = "nats://localhost:4222"
	}
	if cfg.Hermes.ConnectTimeout == 0 {
		cfg.Hermes.ConnectTimeout = 5 * time.Second
	}
	if cfg.Hermes.ReconnectWait == 0 {
		cfg.Hermes.ReconnectWait = 2 * time.Second
	}
	if cfg.Hermes.MaxReconnects == 0 {
		cfg.Hermes.MaxReconnects = -1
	}

	if cfg.Alexandria.URL == "" {
		cfg.Alexandria.URL = "http://warren_alexandria:8500"
	}
	if cfg.Alexandria.Timeout == 0 {
		cfg.Alexandria.Timeout = 5 * time.Second
	}
	// Default enabled=true. Plain bool can't distinguish unset from false,
	// so to disable Alexandria, set enabled: false explicitly in config.
	if !cfg.Alexandria.Enabled {
		cfg.Alexandria.Enabled = true
	}

	// SSH defaults
	if cfg.SSH.AlexandriaURL == "" {
		cfg.SSH.AlexandriaURL = "http://localhost:8500"
	}
	if cfg.SSH.AuthorizedKeysPath == "" {
		cfg.SSH.AuthorizedKeysPath = "/home/{username}/.ssh/authorized_keys"
	}

	// PicoClaw defaults.
	if cfg.PicoClaw.Binary == "" {
		cfg.PicoClaw.Binary = "picoclaw"
	}
	if cfg.PicoClaw.MissionBaseDir == "" {
		cfg.PicoClaw.MissionBaseDir = "/tmp/picoclaw-missions"
	}
	if cfg.PicoClaw.DefaultTimeout == 0 {
		cfg.PicoClaw.DefaultTimeout = 5 * time.Minute
	}
	if cfg.PicoClaw.MaxConcurrent == 0 {
		cfg.PicoClaw.MaxConcurrent = 20
	}

	for _, agent := range cfg.Agents {
		// Default Hermes enabled=true for all agents
		if !agent.Hermes.Enabled {
			agent.Hermes.Enabled = true
		}
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
		if agent.Policy == "on-demand" && agent.Idle.WakeCooldown == 0 {
			agent.Idle.WakeCooldown = 30 * time.Second
		}
	}
}
