package hermes

import "fmt"

// Subject hierarchy constants for the Hermes message bus.
const (
	// Agent lifecycle subjects.
	SubjectAgentStarted  = "swarm.agent.%s.started"
	SubjectAgentStopped  = "swarm.agent.%s.stopped"
	SubjectAgentReady    = "swarm.agent.%s.ready"
	SubjectAgentDegraded = "swarm.agent.%s.degraded"
	SubjectAgentScaled   = "swarm.agent.%s.scaled"
	SubjectAgentBriefed  = "swarm.agent.%s.briefed"

	// Discovery subjects.
	SubjectAgentDiscovery = "swarm.agent.%s.discovery"

	// Task subjects.
	SubjectTaskAssigned  = "swarm.task.%s.assigned"
	SubjectTaskCompleted = "swarm.task.%s.completed"
	SubjectTaskFailed    = "swarm.task.%s.failed"

	// System subjects.
	SubjectSystemHealth    = "swarm.system.health"
	SubjectSystemConfig    = "swarm.system.config"
	SubjectSystemShutdown  = "swarm.system.shutdown"

	// SSH subjects.
	SubjectSSHAuthorized = "swarm.system.ssh.authorized"
	SubjectSSHDenied     = "swarm.system.ssh.denied"

	// Claude Code session subjects.
	SubjectCCSessionCompleted = "swarm.cc.session.completed"
	SubjectCCSessionFailed    = "swarm.cc.session.failed"

	// Wildcard patterns for subscriptions.
	SubjectAllAgents = "swarm.agent.>"
	SubjectAllTasks  = "swarm.task.>"
	SubjectAllSystem = "swarm.system.>"
	SubjectAllCC     = "swarm.cc.>"
	SubjectAll       = "swarm.>"
)

// AgentSubject returns a subject for a specific agent event.
func AgentSubject(pattern, agent string) string {
	return fmt.Sprintf(pattern, agent)
}

// TaskSubject returns a subject for a specific task event.
func TaskSubject(pattern, taskID string) string {
	return fmt.Sprintf(pattern, taskID)
}
