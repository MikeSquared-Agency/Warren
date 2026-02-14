package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// executeSwarmCommand builds a fresh root command with swarm subcommands and runs it.
func executeSwarmCommand(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()

	adminURL = serverURL
	format = "table"

	root := &cobra.Command{
		Use:   "warren",
		Short: "Warren CLI",
	}
	root.PersistentFlags().StringVar(&adminURL, "admin", serverURL, "admin API URL")
	root.PersistentFlags().StringVar(&format, "format", "table", "output format")

	root.AddCommand(swarmCmd())

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	root.SetArgs(args)
	err := root.Execute()

	w.Close()
	os.Stdout = old
	captured, _ := io.ReadAll(r)
	buf.Write(captured)

	return buf.String(), err
}

func TestSwarmStatus_MixedFleet(t *testing.T) {
	srv := mockAdminServer(t, map[string]http.HandlerFunc{
		"GET /admin/agents": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"name": "friend", "type": "container", "state": "ready", "runtime": "", "task_id": ""},
				{"name": "cc-worker-abc", "type": "process", "state": "running", "runtime": "claude-code", "task_id": "task-abc"},
			})
		},
	})
	defer srv.Close()

	out, err := executeSwarmCommand(t, srv.URL, "swarm", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Swarm Fleet") {
		t.Errorf("missing header in output:\n%s", out)
	}
	if !strings.Contains(out, "1 containers") {
		t.Errorf("missing container count:\n%s", out)
	}
	if !strings.Contains(out, "1 processes") {
		t.Errorf("missing process count:\n%s", out)
	}
	for _, col := range []string{"NAME", "TYPE", "STATE", "RUNTIME", "TASK"} {
		if !strings.Contains(out, col) {
			t.Errorf("missing column %q in output:\n%s", col, out)
		}
	}
	if !strings.Contains(out, "friend") || !strings.Contains(out, "cc-worker-abc") {
		t.Errorf("missing agent names:\n%s", out)
	}
}

func TestSwarmStatus_JSON(t *testing.T) {
	srv := mockAdminServer(t, map[string]http.HandlerFunc{
		"GET /admin/agents": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`[{"name":"friend","type":"container"}]`))
		},
	})
	defer srv.Close()

	out, err := executeSwarmCommand(t, srv.URL, "swarm", "status", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"name":"friend"`) {
		t.Errorf("expected JSON output, got:\n%s", out)
	}
}

func TestSwarmSessions_WithSessions(t *testing.T) {
	srv := mockAdminServer(t, map[string]http.HandlerFunc{
		"GET /admin/agents": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"name": "friend", "type": "container", "state": "ready"},
				{"name": "cc-abc12345", "type": "process", "state": "done", "session_id": "abc12345-defg-hijk", "task_id": "task-xyz"},
				{"name": "cc-adhoc", "type": "process", "state": "done", "session_id": "adhoc-session-id", "task_id": ""},
			})
		},
	})
	defer srv.Close()

	out, err := executeSwarmCommand(t, srv.URL, "swarm", "sessions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "friend") {
		t.Errorf("container agent should not appear in sessions:\n%s", out)
	}
	if !strings.Contains(out, "cc-abc12345") {
		t.Errorf("missing process agent:\n%s", out)
	}
	if !strings.Contains(out, "cc-adhoc") {
		t.Errorf("missing adhoc session:\n%s", out)
	}
	if !strings.Contains(out, "(ad-hoc)") {
		t.Errorf("expected (ad-hoc) for empty task_id:\n%s", out)
	}
}

func TestSwarmSessions_Empty(t *testing.T) {
	srv := mockAdminServer(t, map[string]http.HandlerFunc{
		"GET /admin/agents": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"name": "friend", "type": "container", "state": "ready"},
			})
		},
	})
	defer srv.Close()

	out, err := executeSwarmCommand(t, srv.URL, "swarm", "sessions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No CC sessions tracked") {
		t.Errorf("expected no-sessions message:\n%s", out)
	}
}

func TestSwarmSessions_JSON(t *testing.T) {
	srv := mockAdminServer(t, map[string]http.HandlerFunc{
		"GET /admin/agents": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"name": "cc-test", "type": "process", "state": "done", "session_id": "sess-123", "task_id": "task-456"},
			})
		},
	})
	defer srv.Close()

	out, err := executeSwarmCommand(t, srv.URL, "swarm", "sessions", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "cc-test") {
		t.Errorf("expected JSON output with session:\n%s", out)
	}
}

func TestSwarmTask_RequiresArg(t *testing.T) {
	_, err := executeSwarmCommand(t, "", "swarm", "task")
	if err == nil {
		t.Fatal("expected error for missing task description")
	}
}
