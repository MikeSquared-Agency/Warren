package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"warren/internal/config"
)

var (
	adminURL string
	format   string
)

func main() {
	root := &cobra.Command{
		Use:   "warren",
		Short: "Warren CLI — manage OpenClaw agents in Docker Swarm",
	}

	root.PersistentFlags().StringVar(&adminURL, "admin", "", "admin API URL (default http://localhost:9090)")
	root.PersistentFlags().StringVar(&format, "format", "table", "output format: table or json")

	// Agent commands
	agentCmd := &cobra.Command{Use: "agent", Short: "Manage agents"}

	agentCmd.AddCommand(
		agentListCmd(),
		agentAddCmd(),
		agentRemoveCmd(),
		agentInspectCmd(),
		agentWakeCmd(),
		agentSleepCmd(),
		agentLogsCmd(),
	)

	// Service commands
	serviceCmd := &cobra.Command{Use: "service", Short: "Manage dynamic services"}
	serviceCmd.AddCommand(
		serviceListCmd(),
		serviceAddCmd(),
		serviceRemoveCmd(),
	)

	root.AddCommand(
		agentCmd,
		serviceCmd,
		statusCmd(),
		reloadCmd(),
		eventsCmd(),
		configValidateCmd(),
		initCmd(),
		scaffoldCmd(),
		deployCmd(),
		secretsSetCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func getAdminURL() string {
	if adminURL != "" {
		return adminURL
	}
	if v := os.Getenv("WARREN_ADMIN"); v != "" {
		return v
	}
	// Try config file.
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.warren/config.yaml")
	if err == nil {
		var cfg struct {
			Admin string `yaml:"admin"`
		}
		if yaml.Unmarshal(data, &cfg) == nil && cfg.Admin != "" {
			return cfg.Admin
		}
	}
	return "http://localhost:9090"
}

func apiGet(path string) ([]byte, error) {
	resp, err := http.Get(getAdminURL() + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func apiPost(path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = strings.NewReader(string(data))
	}
	resp, err := http.Post(getAdminURL()+path, "application/json", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}

func apiDelete(path string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodDelete, getAdminURL()+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}

func agentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/admin/agents")
			if err != nil {
				return err
			}
			if format == "json" {
				fmt.Println(string(data))
				return nil
			}
			var agents []struct {
				Name        string `json:"name"`
				Hostname    string `json:"hostname"`
				Policy      string `json:"policy"`
				State       string `json:"state"`
				Connections int64  `json:"connections"`
			}
			_ = json.Unmarshal(data, &agents) //nolint:errcheck // non-JSON → empty table is fine
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tHOSTNAME\tPOLICY\tSTATE\tCONNECTIONS")
			for _, a := range agents {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", a.Name, a.Hostname, a.Policy, a.State, a.Connections)
			}
			return w.Flush()
		},
	}
}

func agentAddCmd() *cobra.Command {
	var name, hostname, backend, pol, containerName, healthURL, idleTimeout string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(os.Stdin)

			if name == "" {
				fmt.Print("Name: ")
				name, _ = reader.ReadString('\n')
				name = strings.TrimSpace(name)
			}
			if hostname == "" {
				fmt.Print("Hostname: ")
				hostname, _ = reader.ReadString('\n')
				hostname = strings.TrimSpace(hostname)
			}
			if backend == "" {
				fmt.Printf("Backend [http://tasks.openclaw_%s:18790]: ", name)
				backend, _ = reader.ReadString('\n')
				backend = strings.TrimSpace(backend)
				if backend == "" {
					backend = fmt.Sprintf("http://tasks.openclaw_%s:18790", name)
				}
			}
			if pol == "" {
				fmt.Print("Policy [on-demand]: ")
				pol, _ = reader.ReadString('\n')
				pol = strings.TrimSpace(pol)
				if pol == "" {
					pol = "on-demand"
				}
			}
			if containerName == "" && (pol == "on-demand" || pol == "always-on") {
				fmt.Printf("Container name [openclaw_%s]: ", name)
				containerName, _ = reader.ReadString('\n')
				containerName = strings.TrimSpace(containerName)
				if containerName == "" {
					containerName = fmt.Sprintf("openclaw_%s", name)
				}
			}
			if healthURL == "" && (pol == "on-demand" || pol == "always-on") {
				fmt.Printf("Health URL [%s/health]: ", backend)
				healthURL, _ = reader.ReadString('\n')
				healthURL = strings.TrimSpace(healthURL)
				if healthURL == "" {
					healthURL = backend + "/health"
				}
			}
			if idleTimeout == "" && pol == "on-demand" {
				fmt.Print("Idle timeout [30m]: ")
				idleTimeout, _ = reader.ReadString('\n')
				idleTimeout = strings.TrimSpace(idleTimeout)
				if idleTimeout == "" {
					idleTimeout = "30m"
				}
			}

			payload := map[string]string{
				"name":           name,
				"hostname":       hostname,
				"backend":        backend,
				"policy":         pol,
				"container_name": containerName,
				"health_url":     healthURL,
				"idle_timeout":   idleTimeout,
			}

			resp, err := apiPost("/admin/agents", payload)
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVar(&hostname, "hostname", "", "agent hostname")
	cmd.Flags().StringVar(&backend, "backend", "", "backend URL")
	cmd.Flags().StringVar(&pol, "policy", "", "policy (on-demand, always-on, unmanaged)")
	cmd.Flags().StringVar(&containerName, "container-name", "", "Docker service name")
	cmd.Flags().StringVar(&healthURL, "health-url", "", "health check URL")
	cmd.Flags().StringVar(&idleTimeout, "idle-timeout", "", "idle timeout (e.g. 30m)")

	return cmd
}

func agentRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Remove agent %q? [y/N]: ", args[0])
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
			resp, err := apiDelete("/admin/agents/" + args[0])
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}
}

func agentInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <name>",
		Short: "Show detailed agent info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/admin/agents/" + args[0])
			if err != nil {
				return err
			}
			if format == "json" {
				fmt.Println(string(data))
				return nil
			}
			var info map[string]any
			if err := json.Unmarshal(data, &info); err != nil {
				return fmt.Errorf("parse health info: %w", err)
			}
			for k, v := range info {
				fmt.Printf("%-16s %v\n", k+":", v)
			}
			return nil
		},
	}
}

func agentWakeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wake <name>",
		Short: "Wake an on-demand agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiPost("/admin/agents/"+args[0]+"/wake", nil)
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}
}

func agentSleepCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sleep <name>",
		Short: "Put an on-demand agent to sleep",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiPost("/admin/agents/"+args[0]+"/sleep", nil)
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}
}

func agentLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <name>",
		Short: "Tail Docker service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// First get agent info to find container name.
			data, err := apiGet("/admin/agents/" + args[0])
			if err != nil {
				return err
			}
			var info struct {
				ContainerName string `json:"container_name"`
			}
			_ = json.Unmarshal(data, &info) //nolint:errcheck // falls back to args[0]
			svcName := info.ContainerName
			if svcName == "" {
				svcName = args[0]
			}

			c := exec.Command("docker", "service", "logs", "--follow", svcName)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}

func serviceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List dynamic services",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/admin/services")
			if err != nil {
				return err
			}
			if format == "json" {
				fmt.Println(string(data))
				return nil
			}
			var services []struct {
				Hostname string `json:"hostname"`
				Target   string `json:"target"`
				Agent    string `json:"agent"`
			}
			_ = json.Unmarshal(data, &services)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "HOSTNAME\tTARGET\tAGENT")
			for _, s := range services {
				fmt.Fprintf(w, "%s\t%s\t%s\n", s.Hostname, s.Target, s.Agent)
			}
			return w.Flush()
		},
	}
}

func serviceAddCmd() *cobra.Command {
	var hostname, target, agent string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a dynamic service route",
		RunE: func(cmd *cobra.Command, args []string) error {
			if hostname == "" || target == "" {
				return fmt.Errorf("--hostname and --target are required")
			}
			resp, err := apiPost("/api/services", map[string]string{
				"hostname": hostname,
				"target":   target,
				"agent":    agent,
			})
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}
	cmd.Flags().StringVar(&hostname, "hostname", "", "service hostname")
	cmd.Flags().StringVar(&target, "target", "", "target URL")
	cmd.Flags().StringVar(&agent, "agent", "", "owning agent name")
	return cmd
}

func serviceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <hostname>",
		Short: "Remove a dynamic service route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiDelete("/api/services/" + args[0])
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show orchestrator status",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/admin/health")
			if err != nil {
				return err
			}
			if format == "json" {
				fmt.Println(string(data))
				return nil
			}
			var health struct {
				UptimeSeconds float64 `json:"uptime_seconds"`
				AgentCount    int     `json:"agent_count"`
				ReadyCount    int     `json:"ready_count"`
				SleepingCount int     `json:"sleeping_count"`
				WSConnections int64   `json:"ws_connections"`
				ServiceCount  int     `json:"service_count"`
			}
			_ = json.Unmarshal(data, &health)

			uptime := time.Duration(health.UptimeSeconds) * time.Second
			days := int(uptime.Hours()) / 24
			hours := int(uptime.Hours()) % 24
			mins := int(uptime.Minutes()) % 60

			fmt.Println("Warren Orchestrator")
			fmt.Printf("  Uptime:      %dd %dh %dm\n", days, hours, mins)
			fmt.Printf("  Agents:      %d (%d ready, %d sleeping)\n", health.AgentCount, health.ReadyCount, health.SleepingCount)
			fmt.Printf("  Connections: %d active WebSocket\n", health.WSConnections)
			fmt.Printf("  Services:    %d dynamic routes\n", health.ServiceCount)
			return nil
		},
	}
}

func reloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Send SIGHUP to the orchestrator to reload config",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Find orchestrator PID by process name.
			out, err := exec.Command("pgrep", "-f", "warren-server").Output()
			if err != nil {
				return fmt.Errorf("could not find orchestrator process: %w", err)
			}
			pids := strings.Fields(strings.TrimSpace(string(out)))
			if len(pids) == 0 {
				return fmt.Errorf("orchestrator process not found")
			}
			// Send SIGHUP to first PID found.
			c := exec.Command("kill", "-HUP", pids[0])
			if err := c.Run(); err != nil {
				return fmt.Errorf("failed to send SIGHUP: %w", err)
			}
			fmt.Printf("SIGHUP sent to PID %s\n", pids[0])
			return nil
		},
	}
}

func eventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events",
		Short: "Stream events from the orchestrator (SSE)",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := http.Get(getAdminURL() + "/admin/events")
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					fmt.Println(line[6:])
				}
			}
			return scanner.Err()
		},
	}
}

func configValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config validate <file>",
		Short: "Validate a config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := config.Load(args[0])
			if err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			fmt.Println("OK")
			return nil
		},
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Generate orchestrator.yaml and stack.yaml templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			orchYaml := `listen: ":8080"
admin_listen: ":9090"

defaults:
  health_check_interval: 30s

agents:
  example:
    hostname: example.yourdomain.com
    backend: "http://tasks.openclaw_example:18790"
    policy: on-demand
    container:
      name: openclaw_example
    health:
      url: "http://tasks.openclaw_example:18790/health"
      startup_timeout: 60s
      max_failures: 3
    idle:
      timeout: 30m
      drain_timeout: 30s
`
			stackYaml := `version: "3.8"

services:
  warren:
    image: warren-server:latest
    ports:
      - "8080:8080"
      - "9090:9090"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./orchestrator.yaml:/app/orchestrator.yaml
    deploy:
      placement:
        constraints:
          - node.role == manager

  example:
    image: openclaw-agent:latest
    deploy:
      replicas: 0
    networks:
      - default
`
			if err := os.WriteFile("orchestrator.yaml", []byte(orchYaml), 0644); err != nil {
				return err
			}
			fmt.Println("Created orchestrator.yaml")

			if err := os.WriteFile("stack.yaml", []byte(stackYaml), 0644); err != nil {
				return err
			}
			fmt.Println("Created stack.yaml")
			fmt.Println("\nNext steps:")
			fmt.Println("  1. Edit orchestrator.yaml with your agents")
			fmt.Println("  2. Edit stack.yaml with your services")
			fmt.Println("  3. Run: warren deploy")
			return nil
		},
	}
}

func scaffoldCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scaffold <name>",
		Short: "Generate agent scaffold directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir := name

			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}

			dockerfile := `FROM ubuntu:22.04

RUN apt-get update && apt-get install -y curl supervisor && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install OpenClaw agent
# ADD your agent binary or setup here

COPY openclaw.json /app/openclaw.json
COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf

EXPOSE 18790

CMD ["/usr/bin/supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
`

			openclawJSON := fmt.Sprintf(`{
  "name": "%s",
  "port": 18790,
  "health_endpoint": "/health"
}
`, name)

			supervisordConf := `[supervisord]
nodaemon=true
logfile=/var/log/supervisord.log

[program:agent]
command=/app/agent
autostart=true
autorestart=true
stdout_logfile=/dev/stdout
stdout_logfile_maxbytes=0
stderr_logfile=/dev/stderr
stderr_logfile_maxbytes=0
`

			if err := os.WriteFile(dir+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
				return err
			}
			if err := os.WriteFile(dir+"/openclaw.json", []byte(openclawJSON), 0644); err != nil {
				return err
			}
			if err := os.WriteFile(dir+"/supervisord.conf", []byte(supervisordConf), 0644); err != nil {
				return err
			}

			fmt.Printf("Scaffolded agent in ./%s/\n", name)
			fmt.Println("\nNext steps:")
			fmt.Printf("  1. Add your agent binary/setup to %s/Dockerfile\n", name)
			fmt.Printf("  2. Build: docker build -t openclaw-%s ./%s\n", name, name)
			fmt.Printf("  3. Add to stack.yaml and orchestrator.yaml\n")
			fmt.Printf("  4. Run: warren deploy\n")
			return nil
		},
	}
}

func deployCmd() *cobra.Command {
	var stackFile, stackName string
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the stack via docker stack deploy",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := exec.Command("docker", "stack", "deploy", "-c", stackFile, stackName)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&stackFile, "file", "stack.yaml", "stack file path")
	cmd.Flags().StringVar(&stackName, "name", "openclaw", "stack name")
	return cmd
}

func secretsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "secrets set <name>",
		Short: "Create a Docker secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Enter value for secret %q: ", args[0])
			reader := bufio.NewReader(os.Stdin)
			value, _ := reader.ReadString('\n')
			value = strings.TrimSpace(value)

			c := exec.Command("docker", "secret", "create", args[0], "-")
			c.Stdin = strings.NewReader(value)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			if err := c.Run(); err != nil {
				return fmt.Errorf("failed to create secret: %w", err)
			}
			fmt.Printf("Secret %q created.\n", args[0])
			return nil
		},
	}
}

