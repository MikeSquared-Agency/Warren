# Spec Delta & TODO

Gap analysis between the technical specification and the current implementation, with decisions resolved.

**Last updated:** 2026-02-11

---

## 1. Resolved Decisions

### 1.1 AlwaysOn Policy → DELETE Lifecycle Logic

**Decision:** Delete the restart/start/stop logic in `always_on.go`. Swarm handles always-on agents natively via `replicas: 1` + restart policy.

**Replace with:** A lightweight policy that polls health and reports state (for routing decisions and event emission) but **never** calls Start/Restart/Stop. Swarm owns the lifecycle.

**Work:**
- [ ] Rewrite `always_on.go` — remove all `manager.Start()`, `manager.Restart()` calls
- [ ] Keep health polling so the proxy knows if the agent is healthy or degraded
- [ ] Emit event when health degrades (for alerting pipeline)
- [ ] Set Swarm restart policy to `condition: any` with appropriate delay/max_attempts

### 1.2 Cold Start → 503 + Poll

**Decision:** Return 503 immediately with state in the response body. Frontend polls `/api/health` until ready, then retries.

**Already implemented.** The proxy returns:
```json
{"status": "starting", "agent": "dutybound"}
```
with `Retry-After: 3` header. No code changes needed to the core flow.

**Work:**
- [ ] Document the cold-start protocol for frontend implementors
- [ ] Ensure `/api/health` returns consistent states (`sleeping`, `starting`, `ready`)
- [ ] Consider adding `estimated_wait` to the 503 body if startup time is predictable

### 1.3 Raw Containers → Remove, Swarm Only

**Decision:** Everything runs as Swarm services. Remove `Manager` (raw container lifecycle) after migration.

**Work:**
- [ ] Migrate all agents to Swarm services
- [ ] Remove `internal/container/manager.go`
- [ ] Remove `container.mode` from config (always "service")
- [ ] Rename `ServiceManager` → `Manager` (it's the only one)

### 1.4 Networking → Overlay

**Decision:** Use Swarm overlay network. Services don't publish ports to the host. Orchestrator routes to services by Swarm DNS name.

**Work:**
- [ ] Create encrypted overlay network in stack file
- [ ] Remove host port mappings from services
- [ ] Orchestrator resolves backends via `tasks.<service>:<port>` or service DNS
- [ ] Update config — backends become service names instead of `localhost:<port>`

### 1.5 Alerting → Log + Events

**Decision:** Log state transitions as structured events. Wire up to Slack/Prometheus later.

**Work:**
- [ ] Define event types: `agent.degraded`, `agent.wake`, `agent.sleep`, `agent.health_failed`, `restart.exhausted`
- [ ] Emit events via structured log fields (machine-parseable)
- [ ] Add event channel/interface for future consumers (webhook, Prometheus push, etc.)
- [ ] Phase 4: Prometheus metrics endpoint, Slack webhook integration

### 1.6 Frontend Cold Start → 503 Body + Poll /api/health

**Decision:** 503 response body includes agent state. Frontend polls `/api/health` until `"ready"`, then retries the original request.

**Already implemented.** Document the protocol.

---

## 2. Remaining Work

### HIGH — Before Swarm Migration

| Task | Notes |
|---|---|
| Create `deploy/stack.yaml` | Services, secrets, overlay network, health checks, resource limits |
| Rewrite `always_on.go` | Health-only, no lifecycle management, emit events on state changes |
| Add drain timeout | `IdleConfig.DrainTimeout` — force-close WS after timeout |
| Fix WebSocket `Connection` header | Parse as comma-separated, check for `"upgrade"` token |
| Standardise state names | `sleeping`, `starting`, `ready`, `degraded` across all policies |
| Remove raw container manager | Delete `manager.go`, remove `container.mode` config |

### MEDIUM — Phase 2

| Task | Notes |
|---|---|
| Agent service registration API | `POST /api/services` — agents register dynamic hostnames |
| Dynamic route table in proxy | Ephemeral routes tied to parent agent, purged on sleep |
| Wildcard hostname config | Agent owns subdomains under a domain |
| Startup reconciliation | Use `Discover()` results to set initial policy state |
| Graceful shutdown with WS drain | Track hijacked connections, drain on SIGTERM |
| Overlay network routing | Backends as Swarm DNS names, no host ports |
| Event emission | Structured log events for all state transitions |

### LOW — Phase 3-4

| Task | Notes |
|---|---|
| Admin API (separate port) | List agents, manual wake/sleep, health |
| Swarm event watching | Subscribe to Docker events for real-time state |
| WS frame-level activity tracking | Update `last_activity` on data transfer, not just open |
| Config hot-reload (SIGHUP) | Reload YAML without restart |
| Prometheus metrics | Export via `/metrics` endpoint |
| Slack/webhook alerting | Consume events, push to external services |
| LRU eviction | Sleep least-recently-used on-demand agent under pressure |
| Systemd unit file | For the orchestrator process itself |

---

## 3. Code Bugs to Fix

| Issue | File | Fix |
|---|---|---|
| WS `Connection` header too strict | `proxy/websocket.go` | Parse comma-separated values |
| WS activity not updated on frames | `proxy/websocket.go` | Wrap `io.Copy` with activity touch (or document as intentional) |
| State names inconsistent | `policy/*.go`, `proxy/proxy.go` | Standardise: `sleeping`, `starting`, `ready`, `degraded` |

---

## 4. Spec Document Updates

The original spec needs these updates to match decisions:

1. Delete `always_on.go` lifecycle management — Swarm owns restarts
2. Remove `container.mode` — Swarm only, no raw containers
3. 503 + poll is the cold-start protocol
4. Overlay network — backends are Swarm DNS names
5. Event emission — new component for alerting pipeline
6. Agent service registration — new feature
7. Config field names — match implementation (`startup_timeout`, `idle.timeout`)
8. Project structure — `internal/container/` with only `ServiceManager`
9. DutyBound ports — MC 8081 exposed, OpenClaw gateway 18800 loopback