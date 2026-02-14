package security

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// stackFile is the relative path from the repo root to the stack definition.
const stackFile = "../../deploy/stack.yaml"

// composeFile represents the subset of a Docker Compose / Swarm stack file
// needed for security validation.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Secrets  map[string]interface{}    `yaml:"secrets"`
}

type composeService struct {
	Image       string        `yaml:"image"`
	Command     interface{}   `yaml:"command"` // string or []string
	Entrypoint  interface{}   `yaml:"entrypoint"`
	Environment []string      `yaml:"environment"`
	Ports       []interface{} `yaml:"ports"` // mixed short-form strings and long-form maps
	Secrets     []interface{} `yaml:"secrets"`
}

type composePort struct {
	Target    int    `yaml:"target"`
	Published int    `yaml:"published"`
	Protocol  string `yaml:"protocol"`
	Mode      string `yaml:"mode"`
}

// parsePorts handles both short-form ("18790:18790") and long-form port definitions.
func parsePorts(raw []interface{}) []composePort {
	var ports []composePort
	for _, p := range raw {
		switch v := p.(type) {
		case string:
			// Short form like "18790:18790" — no mode specified (ingress by default)
			ports = append(ports, composePort{Mode: ""})
		case map[string]interface{}:
			cp := composePort{}
			if t, ok := v["target"].(int); ok {
				cp.Target = t
			}
			if pub, ok := v["published"].(int); ok {
				cp.Published = pub
			}
			if m, ok := v["mode"].(string); ok {
				cp.Mode = m
			}
			if proto, ok := v["protocol"].(string); ok {
				cp.Protocol = proto
			}
			ports = append(ports, cp)
		default:
			_ = v
			ports = append(ports, composePort{Mode: ""})
		}
	}
	return ports
}

func loadStack(t *testing.T) composeFile {
	t.Helper()
	data, err := os.ReadFile(stackFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", stackFile, err)
	}
	var stack composeFile
	if err := yaml.Unmarshal(data, &stack); err != nil {
		t.Fatalf("failed to parse %s: %v", stackFile, err)
	}
	if len(stack.Services) == 0 {
		t.Fatalf("stack has no services")
	}
	return stack
}

// commandString returns the full command for a service as a single string,
// handling both string and []interface{} YAML representations.
func commandString(cmd interface{}) string {
	switch v := cmd.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = fmt.Sprintf("%v", p)
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

// entrypointSlice returns the entrypoint as a string slice.
func entrypointSlice(ep interface{}) []string {
	switch v := ep.(type) {
	case string:
		return []string{v}
	case []interface{}:
		out := make([]string, len(v))
		for i, p := range v {
			out[i] = fmt.Sprintf("%v", p)
		}
		return out
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// 1. No plaintext secrets in environment blocks
// ---------------------------------------------------------------------------

func TestStackNoPlaintextSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stack security tests in short mode")
	}
	stack := loadStack(t)

	// Patterns that indicate a leaked secret value.
	secretPatterns := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{"JWT token (eyJ...)", regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.`)},
		{"PostgreSQL connection string with password", regexp.MustCompile(`postgresql://[^:]+:[^@]+@`)},
		{"Slack app token (xapp-)", regexp.MustCompile(`^xapp-`)},
		{"Slack bot token (xoxb-)", regexp.MustCompile(`^xoxb-`)},
		{"Gemini API key (AIza)", regexp.MustCompile(`^AIza`)},
		{"trivial password", regexp.MustCompile(`^password$`)},
	}

	// Alexandria is the vault bootstrap — it can't fetch secrets from itself,
	// so its SUPABASE_KEY remains as an env var. This is accepted by the
	// project's threat model (Alexandria runs in host mode, is the most
	// privileged service, and its critical secrets DATABASE_URL/ENCRYPTION_KEY
	// are in Docker secrets).
	allowlist := map[string]map[string]bool{
		"alexandria": {"SUPABASE_KEY": true},
	}

	for svcName, svc := range stack.Services {
		for _, envLine := range svc.Environment {
			parts := strings.SplitN(envLine, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key, val := parts[0], parts[1]
			if allowlist[svcName][key] {
				continue
			}
			for _, sp := range secretPatterns {
				if sp.pattern.MatchString(val) {
					t.Errorf("service %q env %s contains plaintext secret matching %q pattern",
						svcName, key, sp.name)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 2. No unnecessary port exposure
// ---------------------------------------------------------------------------

func TestStackPortExposure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stack security tests in short mode")
	}
	stack := loadStack(t)

	// Only these services are allowed to publish ports to the host.
	allowed := map[string]bool{
		"lily":         true,
		"scout":        true,
		"dutybound-mc": true,
		"celebrimbor":  true,
		"alexandria":   true,
	}

	for svcName, svc := range stack.Services {
		ports := parsePorts(svc.Ports)
		if len(ports) > 0 && !allowed[svcName] {
			t.Errorf("service %q exposes ports but is not in the allowed list", svcName)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. NATS (hermes) auth is configured
// ---------------------------------------------------------------------------

func TestStackNATSAuthConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stack security tests in short mode")
	}
	stack := loadStack(t)

	hermes, ok := stack.Services["hermes"]
	if !ok {
		t.Fatal("hermes service not found in stack")
	}

	cmd := commandString(hermes.Command)
	if !strings.Contains(cmd, "--auth") {
		t.Errorf("hermes command does not include --auth flag; got command: %s", cmd)
	}
}

// ---------------------------------------------------------------------------
// 4. Docker secrets are used for Alexandria
// ---------------------------------------------------------------------------

func TestStackAlexandriaUsesDockerSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stack security tests in short mode")
	}
	stack := loadStack(t)

	alex, ok := stack.Services["alexandria"]
	if !ok {
		t.Fatal("alexandria service not found in stack")
	}
	if len(alex.Secrets) == 0 {
		t.Error("alexandria service has no secrets: block; expected Docker secrets for sensitive config")
	}
}

// ---------------------------------------------------------------------------
// 5. vault-entrypoint is used by internal services
// ---------------------------------------------------------------------------

func TestStackVaultEntrypoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stack security tests in short mode")
	}
	stack := loadStack(t)

	// Services that must use vault-entrypoint.sh.
	vaultServices := []string{"dispatch", "chronicle", "slack-forwarder", "promptforge"}

	for _, svcName := range vaultServices {
		svc, ok := stack.Services[svcName]
		if !ok {
			t.Errorf("expected service %q not found in stack", svcName)
			continue
		}
		ep := entrypointSlice(svc.Entrypoint)
		found := false
		for _, arg := range ep {
			if strings.Contains(arg, "vault-entrypoint.sh") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("service %q entrypoint does not use vault-entrypoint.sh; got %v", svcName, ep)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. All exposed ports use host mode
// ---------------------------------------------------------------------------

func TestStackPortsUseHostMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stack security tests in short mode")
	}
	stack := loadStack(t)

	for svcName, svc := range stack.Services {
		ports := parsePorts(svc.Ports)
		for i, p := range ports {
			if p.Mode != "host" {
				t.Errorf("service %q port[%d] (target=%d) mode=%q, want \"host\"",
					svcName, i, p.Target, p.Mode)
			}
		}
	}
}
