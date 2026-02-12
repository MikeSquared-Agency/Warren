package hermes

import "testing"

func TestAgentSubject(t *testing.T) {
	tests := []struct {
		pattern string
		agent   string
		want    string
	}{
		{SubjectAgentStarted, "friend", "swarm.agent.friend.started"},
		{SubjectAgentStopped, "mc", "swarm.agent.mc.stopped"},
		{SubjectAgentReady, "root", "swarm.agent.root.ready"},
		{SubjectAgentDegraded, "test", "swarm.agent.test.degraded"},
		{SubjectAgentScaled, "worker", "swarm.agent.worker.scaled"},
	}

	for _, tt := range tests {
		got := AgentSubject(tt.pattern, tt.agent)
		if got != tt.want {
			t.Errorf("AgentSubject(%q, %q) = %q, want %q", tt.pattern, tt.agent, got, tt.want)
		}
	}
}

func TestTaskSubject(t *testing.T) {
	got := TaskSubject(SubjectTaskCompleted, "task-123")
	want := "swarm.task.task-123.completed"
	if got != want {
		t.Errorf("TaskSubject = %q, want %q", got, want)
	}
}
