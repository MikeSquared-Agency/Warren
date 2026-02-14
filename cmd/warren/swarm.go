package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func swarmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "swarm",
		Short: "Interact with the OpenClaw swarm",
	}

	cmd.AddCommand(
		swarmTaskCmd(),
		swarmStatusCmd(),
		swarmSessionsCmd(),
	)

	return cmd
}

func swarmTaskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task <description>",
		Short: "Submit a task to Dispatch via NATS",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			description := args[0]
			taskID := uuid.New().String()

			// Resolve owner from SSH key.
			ownerUUID, ownerName := resolveOwner()

			// Detect git context.
			cwd, _ := os.Getwd()
			gitBranch := getGitBranch()
			repoRoot := getGitRoot()

			// Build task request event.
			taskRequest := map[string]any{
				"id":          uuid.New().String(),
				"type":        "task.request",
				"source":      "warren-cli",
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
				"data": map[string]any{
					"task_id":     taskID,
					"description": description,
					"owner_uuid":  ownerUUID,
					"owner_name":  ownerName,
					"context": map[string]string{
						"cwd":        cwd,
						"git_branch": gitBranch,
						"repo_root":  repoRoot,
					},
				},
			}

			// Connect to NATS and publish.
			nc, err := connectNATS()
			if err != nil {
				return fmt.Errorf("connect to NATS: %w", err)
			}
			defer nc.Close()

			data, _ := json.Marshal(taskRequest)
			if err := nc.Publish("swarm.task.request", data); err != nil {
				return fmt.Errorf("publish task: %w", err)
			}
			if err := nc.Flush(); err != nil {
				return fmt.Errorf("flush: %w", err)
			}

			fmt.Printf("Task submitted: %s\n", taskID)
			fmt.Printf("  Description: %s\n", description)
			if ownerName != "" {
				fmt.Printf("  Owner:       %s\n", ownerName)
			}
			if gitBranch != "" {
				fmt.Printf("  Branch:      %s\n", gitBranch)
			}
			return nil
		},
	}
}

func swarmStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show swarm status (pending/running tasks)",
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
				Type        string `json:"type"`
				State       string `json:"state"`
				Runtime     string `json:"runtime"`
				TaskID      string `json:"task_id"`
				Connections int64  `json:"connections"`
			}
			_ = json.Unmarshal(data, &agents)

			var containers, processes int
			for _, a := range agents {
				switch a.Type {
				case "container":
					containers++
				case "process":
					processes++
				}
			}

			fmt.Printf("Swarm Fleet: %d agents (%d containers, %d processes)\n\n", len(agents), containers, processes)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tTYPE\tSTATE\tRUNTIME\tTASK")
			for _, a := range agents {
				runtime := a.Runtime
				if runtime == "" {
					runtime = "-"
				}
				task := a.TaskID
				if task == "" {
					task = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.Type, a.State, runtime, task)
			}
			return w.Flush()
		},
	}
}

func swarmSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List CC sessions from the fleet",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/admin/agents")
			if err != nil {
				return err
			}

			var agents []struct {
				Name      string `json:"name"`
				Type      string `json:"type"`
				State     string `json:"state"`
				Runtime   string `json:"runtime"`
				TaskID    string `json:"task_id"`
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(data, &agents)

			// Filter to process agents only.
			var sessions []struct {
				Name      string
				State     string
				TaskID    string
				SessionID string
			}
			for _, a := range agents {
				if a.Type == "process" {
					sessions = append(sessions, struct {
						Name      string
						State     string
						TaskID    string
						SessionID string
					}{a.Name, a.State, a.TaskID, a.SessionID})
				}
			}

			if format == "json" {
				out, _ := json.MarshalIndent(sessions, "", "  ")
				fmt.Println(string(out))
				return nil
			}

			if len(sessions) == 0 {
				fmt.Println("No CC sessions tracked.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tTASK\tSESSION")
			for _, s := range sessions {
				task := s.TaskID
				if task == "" {
					task = "(ad-hoc)"
				}
				sessionID := s.SessionID
				if len(sessionID) > 8 {
					sessionID = sessionID[:8] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Name, s.State, task, sessionID)
			}
			return w.Flush()
		},
	}
}
