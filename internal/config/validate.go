package config

import (
	"fmt"
	"net/url"
)

func validate(cfg *Config) error {
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("config: no agents defined")
	}

	hostnames := make(map[string]string) // hostname → agent name
	for name, agent := range cfg.Agents {
		if agent.Hostname == "" {
			return fmt.Errorf("config: agent %q missing hostname", name)
		}
		if agent.Backend == "" {
			return fmt.Errorf("config: agent %q missing backend", name)
		}
		if _, err := url.Parse(agent.Backend); err != nil {
			return fmt.Errorf("config: agent %q invalid backend URL: %w", name, err)
		}

		switch agent.Policy {
		case "always-on", "unmanaged", "on-demand":
			// valid
		case "":
			return fmt.Errorf("config: agent %q missing policy", name)
		default:
			return fmt.Errorf("config: agent %q unknown policy %q", name, agent.Policy)
		}

		if agent.Policy == "always-on" {
			if agent.Container.Name == "" {
				return fmt.Errorf("config: agent %q with always-on policy requires container.name", name)
			}
			if agent.Health.URL == "" {
				return fmt.Errorf("config: agent %q with always-on policy requires health.url", name)
			}
		}

		if agent.Policy == "on-demand" {
			if agent.Container.Name == "" {
				return fmt.Errorf("config: agent %q with on-demand policy requires container.name", name)
			}
			if agent.Health.URL == "" {
				return fmt.Errorf("config: agent %q with on-demand policy requires health.url", name)
			}
			if agent.Idle.Timeout <= 0 {
				return fmt.Errorf("config: agent %q with on-demand policy requires idle.timeout > 0", name)
			}
		}

		// Check all hostnames (primary + additional) for duplicates.
		allHostnames := append([]string{agent.Hostname}, agent.Hostnames...)
		for _, h := range allHostnames {
			if h == "" {
				continue
			}
			if prev, ok := hostnames[h]; ok {
				return fmt.Errorf("config: duplicate hostname %q (agents %q and %q)", h, prev, name)
			}
			hostnames[h] = name
		}
	}

	return nil
}
