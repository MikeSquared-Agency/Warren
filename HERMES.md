# Hermes — Inter-Agent Message Bus

Hermes is Warren's NATS-based messaging layer. It provides pub/sub, request/reply, and durable JetStream event streams for the agent swarm.

## Architecture

- **NATS + JetStream** container runs as `hermes` service in Docker Swarm
- **Go SDK** at `internal/hermes/` provides typed event publishing, subscribing, and stream provisioning
- **Warren orchestrator** bridges internal lifecycle events to Hermes subjects automatically

## Quick Start

### 1. Deploy NATS in Swarm

```bash
docker stack deploy -c deploy/stack.yaml warren
```

The `hermes` service exposes:
- `4222` — NATS client port (published for bare-metal orchestrator)
- `8222` — HTTP monitoring

### 2. Enable in Orchestrator Config

```yaml
hermes:
  enabled: true
  url: "nats://localhost:4222"
```

### 3. Verify

```bash
# Check NATS is healthy
curl http://localhost:8222/healthz

# Check JetStream streams
curl http://localhost:8222/jsz
```

## Subject Hierarchy

| Pattern | Description |
|---------|-------------|
| `swarm.agent.<name>.started` | Agent started/woken |
| `swarm.agent.<name>.stopped` | Agent stopped/sleeping |
| `swarm.agent.<name>.ready` | Agent healthy and ready |
| `swarm.agent.<name>.degraded` | Agent degraded |
| `swarm.agent.<name>.scaled` | Agent replicas changed |
| `swarm.task.<id>.assigned` | Task assigned |
| `swarm.task.<id>.completed` | Task completed |
| `swarm.task.<id>.failed` | Task failed |
| `swarm.system.health` | System health |
| `swarm.system.config` | Config changes |
| `swarm.system.shutdown` | System shutdown |

### Wildcards

- `swarm.agent.>` — All agent events
- `swarm.task.>` — All task events
- `swarm.agent.*.ready` — All agent ready events

## JetStream Streams

| Stream | Subjects | Retention |
|--------|----------|-----------|
| `AGENT_LIFECYCLE` | `swarm.agent.>` | 7 days |
| `TASK_EVENTS` | `swarm.task.>` | 30 days |
| `SYSTEM_EVENTS` | `swarm.system.>` | 7 days |

## Event Envelope

Every event follows this schema:

```json
{
  "id": "uuid",
  "type": "agent.started",
  "source": "warren-orchestrator",
  "timestamp": "2024-01-01T00:00:00Z",
  "correlation_id": "optional",
  "causation_id": "optional",
  "data": { ... }
}
```

## SDK Usage

```go
import "warren/internal/hermes"

// Connect
client, err := hermes.Connect(hermes.Config{
    URL: "nats://localhost:4222",
}, "my-service", logger)
defer client.Close()

// Provision streams
client.ProvisionStreams(ctx)

// Publish
client.PublishEvent(
    hermes.AgentSubject(hermes.SubjectAgentReady, "friend"),
    "agent.ready",
    hermes.AgentLifecycleData{Agent: "friend"},
)

// Subscribe
client.Subscribe("swarm.agent.>", func(ev hermes.Event) {
    log.Printf("event: %s from %s", ev.Type, ev.Source)
})
```

## Monitoring

NATS monitoring endpoints at `:8222`:

- `/healthz` — Health check
- `/varz` — Server info
- `/jsz` — JetStream status
- `/connz` — Connections
- `/subz` — Subscriptions
