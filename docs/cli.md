# CLI Reference

The `warren` CLI manages Warren orchestrator instances from the command line. It communicates with the admin API over HTTP and provides commands for agent lifecycle, service management, deployment, and scaffolding.

## Installation

```bash
cd ~/Warren
make build
# Binary at bin/warren
```

Or install to your PATH:

```bash
cp bin/warren /usr/local/bin/
```

## Configuration

The CLI resolves the admin API URL in this order:

1. **`--admin` flag** — `warren --admin http://host:9090 status`
2. **`WARREN_ADMIN` env** — `export WARREN_ADMIN=http://host:9090`
3. **`~/.warren/config.yaml`** — persistent config file
4. **Default** — `http://localhost:9090`

### Config file

```yaml
# ~/.warren/config.yaml
admin: "http://localhost:9090"
```

## Global Flags

| Flag | Default | Description |
|---|---|---|
| `--admin` | `http://localhost:9090` | Admin API URL |
| `--format` | `table` | Output format: `table` or `json` |

---

## Agent Management

### `warren agent list`

List all configured agents with their current state.

```bash
warren agent list
```

```
NAME          HOSTNAME                   POLICY     STATE    CONNECTIONS
friend        friend.yourdomain.com      always-on  ready    2
dutybound     kai.yourdomain.com         on-demand  sleeping 0
root          root.yourdomain.com        unmanaged  ready    1
```

```bash
warren agent list --format json
```

### `warren agent add`

Add a new agent dynamically (zero downtime, no restart required). Supports both flags and interactive prompts.

```bash
# Interactive
warren agent add

# Non-interactive
warren agent add \
  --name my-agent \
  --hostname agent.yourdomain.com \
  --backend http://tasks.openclaw_my-agent:18790 \
  --policy on-demand \
  --container-name openclaw_my-agent \
  --health-url http://tasks.openclaw_my-agent:18790/health \
  --idle-timeout 30m
```

**Flags:**

| Flag | Description |
|---|---|
| `--name` | Agent name |
| `--hostname` | Agent hostname |
| `--backend` | Backend URL |
| `--policy` | `on-demand`, `always-on`, or `unmanaged` |
| `--container-name` | Docker Swarm service name |
| `--health-url` | Health check URL |
| `--idle-timeout` | Idle timeout (e.g. `30m`) |

### `warren agent remove <name>`

Remove an agent. Prompts for confirmation.

```bash
warren agent remove dutybound
# Remove agent "dutybound"? [y/N]: y
```

### `warren agent inspect <name>`

Show detailed information about a specific agent.

```bash
warren agent inspect dutybound
```

```
name:            dutybound
hostname:        kai.yourdomain.com
backend:         http://tasks.openclaw_dutybound:8081
policy:          on-demand
state:           sleeping
connections:     0
container_name:  openclaw_dutybound
idle_timeout:    30m
```

```bash
warren agent inspect dutybound --format json
```

### `warren agent wake <name>`

Manually wake an on-demand agent (scale 0→1).

```bash
warren agent wake dutybound
```

### `warren agent sleep <name>`

Manually put an on-demand agent to sleep (scale 1→0).

```bash
warren agent sleep dutybound
```

### `warren agent logs <name>`

Tail Docker service logs for an agent. Streams continuously (Ctrl+C to stop).

```bash
warren agent logs dutybound
```

This runs `docker service logs --follow <container_name>` under the hood.

---

## Service Management

### `warren service list`

List dynamically registered service routes.

```bash
warren service list
```

```
HOSTNAME                      TARGET    AGENT
preview.yourdomain.com        :3000     dutybound
docs.yourdomain.com           :8080     friend
```

### `warren service add`

Add a dynamic service route.

```bash
warren service add \
  --hostname preview.yourdomain.com \
  --target http://tasks.openclaw_dutybound:3000 \
  --agent dutybound
```

**Flags:**

| Flag | Required | Description |
|---|---|---|
| `--hostname` | yes | Service hostname |
| `--target` | yes | Target URL |
| `--agent` | no | Owning agent name |

### `warren service remove <hostname>`

Remove a dynamic service route.

```bash
warren service remove preview.yourdomain.com
```

---

## Operations

### `warren status`

Show orchestrator health and summary.

```bash
warren status
```

```
Warren Orchestrator
  Uptime:      3d 14h 22m
  Agents:      5 (3 ready, 2 sleeping)
  Connections: 4 active WebSocket
  Services:    2 dynamic routes
```

```bash
warren status --format json
```

### `warren reload`

Send SIGHUP to the orchestrator process to trigger a config hot-reload.

```bash
warren reload
# SIGHUP sent to PID 12345
```

Runtime-safe changes (idle timeouts, health intervals, thresholds) apply immediately. Structural changes (new agents, hostname changes) require a restart.

### `warren events`

Stream real-time events from the orchestrator via SSE. Runs continuously (Ctrl+C to stop).

```bash
warren events
```

```json
{"type":"agent.ready","agent":"friend","timestamp":"2026-02-11T19:00:00Z"}
{"type":"agent.sleep","agent":"dutybound","timestamp":"2026-02-11T19:30:00Z"}
```

### `warren config validate <file>`

Validate an orchestrator config file without starting the server.

```bash
warren config validate orchestrator.yaml
# OK
```

---

## Scaffolding & Deployment

### `warren init`

Generate template `orchestrator.yaml` and `stack.yaml` files in the current directory.

```bash
warren init
# Created orchestrator.yaml
# Created stack.yaml
#
# Next steps:
#   1. Edit orchestrator.yaml with your agents
#   2. Edit stack.yaml with your services
#   3. Run: warren deploy
```

### `warren scaffold <name>`

Generate a scaffold directory for a new agent with Dockerfile, config, and supervisord setup.

```bash
warren scaffold my-agent
# Scaffolded agent in ./my-agent/
#
# Next steps:
#   1. Add your agent binary/setup to my-agent/Dockerfile
#   2. Build: docker build -t openclaw-my-agent ./my-agent
#   3. Add to stack.yaml and orchestrator.yaml
#   4. Run: warren deploy
```

Creates:
- `<name>/Dockerfile` — Ubuntu-based container template
- `<name>/openclaw.json` — Agent configuration
- `<name>/supervisord.conf` — Process manager config

### `warren deploy`

Deploy the stack via `docker stack deploy`.

```bash
warren deploy
warren deploy --file custom-stack.yaml --name mystack
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--file` | `stack.yaml` | Stack file path |
| `--name` | `openclaw` | Stack name |

### `warren secrets set <name>`

Create a Docker secret interactively.

```bash
warren secrets set agent-api-key
# Enter value for secret "agent-api-key": ****
# Secret "agent-api-key" created.
```

---

## Troubleshooting

### Connection refused

```
Error: Get "http://localhost:9090/admin/health": dial tcp 127.0.0.1:9090: connection refused
```

The orchestrator isn't running or the admin port is different. Check:
- Is `warren-server` running? (`pgrep -f warren-server`)
- Is `admin_listen` set in `orchestrator.yaml`?
- Do you need `--admin` to point elsewhere?

### HTTP 404 on agent commands

The admin API endpoints require `admin_listen` to be configured in `orchestrator.yaml`. If it's not set, the admin API is disabled.

### `warren reload` can't find process

`reload` uses `pgrep -f warren-server` to find the orchestrator PID. If you renamed the binary or run it differently, send SIGHUP manually:

```bash
kill -HUP $(pidof your-binary-name)
```

### Docker permission errors

`warren agent logs`, `warren deploy`, and `warren secrets set` shell out to Docker commands. Ensure the current user has Docker access (`docker` group or sudo).
