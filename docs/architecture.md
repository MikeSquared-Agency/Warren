# Architecture

## Overview

OpenClaw Orchestrator sits between a Cloudflare Tunnel (or any reverse proxy) and a Docker Swarm cluster. It routes HTTP and WebSocket traffic by hostname, manages agent lifecycle through configurable policies, and tracks activity to automatically sleep idle agents.

```mermaid
flowchart TB
    subgraph Internet
        U1["User A"] 
        U2["User B"]
        U3["User C"]
    end

    subgraph Cloudflare
        CF["Tunnel<br/>*.yourdomain.com → :8080"]
    end

    subgraph Host["Server"]
        ORC["Go Orchestrator :8080"]
        
        subgraph Swarm["Docker Swarm (single-node)"]
            direction TB
            S1["Agent: friend<br/>replicas: 1<br/>always-on<br/>:18790"]
            S2["Agent: dutybound<br/>replicas: 0|1<br/>on-demand<br/>:8081"]
            SECRETS["Secrets<br/>(API keys, configs)"]
            OVERLAY["Encrypted Overlay Network"]
        end
        
        NATIVE["Root OpenClaw<br/>(native process)<br/>:18789<br/>unmanaged"]
    end

    U1 --> CF
    U2 --> CF
    U3 --> CF
    CF --> ORC
    ORC -->|"root.yourdomain.com"| NATIVE
    ORC -->|"friend.yourdomain.com"| S1
    ORC -->|"kai.yourdomain.com"| S2
    ORC -.->|"Swarm Service API"| Swarm
```

## Responsibility Split

The orchestrator and Swarm have a clean division of labour. The orchestrator never reimplements what Swarm provides natively.

```mermaid
flowchart LR
    subgraph Orchestrator["Go Orchestrator"]
        R["Hostname Routing"]
        WS["WebSocket Proxy"]
        WK["Wake-on-Request"]
        ID["Idle Detection"]
        AT["Activity Tracking"]
        SR["Service Registration"]
    end

    subgraph Swarm["Docker Swarm"]
        LC["Service Lifecycle"]
        HC["Health Checks"]
        RS["Restart Policies"]
        SC["Secrets"]
        RL["Resource Limits"]
        NET["Overlay Network"]
        RU["Rolling Updates"]
    end
```

| Concern | Owner | How |
|---|---|---|
| Hostname → backend routing | Orchestrator | Host header map lookup |
| WebSocket proxying | Orchestrator | HTTP Upgrade + bidirectional pipe |
| Wake-on-request | Orchestrator | Scale service 0→1 on first request |
| Idle timeout / sleep | Orchestrator | Track activity, scale 1→0 after timeout |
| Agent-created service routing | Orchestrator | Dynamic route registration API |
| Health checks + auto-restart | Swarm | Service `healthcheck` + restart policy |
| Secrets | Swarm | `docker secret` → `/run/secrets/` |
| Resource limits | Swarm | Service `resources.limits` |
| Networking | Swarm | Encrypted overlay network |
| Declarative deployment | Swarm | `docker stack deploy` |

## Request Flow

### Always-On Agent

```mermaid
sequenceDiagram
    participant User
    participant CF as Cloudflare Tunnel
    participant ORC as Orchestrator
    participant Agent

    User->>CF: GET friend.yourdomain.com
    CF->>ORC: forward (Host: friend.yourdomain.com)
    ORC->>ORC: lookup hostname → agent "friend"
    ORC->>ORC: check state → "running"
    ORC->>Agent: proxy request
    Agent-->>ORC: response
    ORC-->>CF: response
    CF-->>User: response
```

### On-Demand Agent (Cold Start)

```mermaid
sequenceDiagram
    participant User
    participant CF as Cloudflare Tunnel
    participant ORC as Orchestrator
    participant Swarm
    participant Agent

    User->>CF: GET kai.yourdomain.com
    CF->>ORC: forward (Host: kai.yourdomain.com)
    ORC->>ORC: lookup hostname → agent "dutybound"
    ORC->>ORC: check state → "sleeping"
    
    ORC->>ORC: trigger wake signal
    ORC-->>CF: 503 + Retry-After: 3
    CF-->>User: 503

    Note over ORC,Swarm: Wake loop (async)
    ORC->>Swarm: scale dutybound replicas: 0 → 1
    Swarm->>Agent: start container
    
    loop Health polling
        ORC->>Agent: GET /api/health
        Agent-->>ORC: 503 (still starting)
    end
    
    ORC->>Agent: GET /api/health
    Agent-->>ORC: 200 OK
    ORC->>ORC: state → "ready"

    User->>CF: GET kai.yourdomain.com (retry)
    CF->>ORC: forward
    ORC->>Agent: proxy request
    Agent-->>ORC: response
    ORC-->>CF: response
    CF-->>User: response
```

### On-Demand Agent (Idle → Sleep)

```mermaid
sequenceDiagram
    participant ORC as Orchestrator
    participant Swarm
    participant Agent

    Note over ORC: Idle timer running (e.g. 30m)
    
    loop Idle check
        ORC->>ORC: last activity? 25m ago
        ORC->>ORC: WebSocket connections? 0
        ORC->>ORC: not idle yet
    end

    ORC->>ORC: last activity? 31m ago
    ORC->>ORC: WebSocket connections? 0
    ORC->>ORC: idle timeout reached!
    
    ORC->>Swarm: scale dutybound replicas: 1 → 0
    Swarm->>Agent: stop container (graceful)
    ORC->>ORC: state → "sleeping"
    ORC->>ORC: purge registered service routes
```

### Agent-Created Service

```mermaid
sequenceDiagram
    participant Agent as OpenClaw Agent
    participant ORC as Orchestrator
    participant User

    Note over Agent: Agent starts a preview server on :3000
    
    Agent->>ORC: POST /api/services<br/>{"hostname": "preview.yourdomain.com", "target": ":3000"}
    ORC->>ORC: validate hostname against agent's allowed domain
    ORC->>ORC: register ephemeral route
    ORC-->>Agent: 200 OK

    User->>ORC: GET preview.yourdomain.com
    ORC->>ORC: lookup → dynamic route → dutybound :3000
    ORC->>Agent: proxy to :3000
    Agent-->>ORC: response
    ORC-->>User: response

    Note over ORC: Container sleeps
    ORC->>ORC: purge all routes for dutybound
```

## Policy State Machines

### Always-On

Swarm owns the lifecycle (restarts, health recovery). The orchestrator only monitors health for routing decisions and event emission.

```mermaid
stateDiagram-v2
    [*] --> running : Swarm starts service
    running --> degraded : health check fails (repeated)
    degraded --> running : health check recovers
    
    note right of running : Swarm handles restarts
    note right of degraded : Emit event for alerting
```

### On-Demand

```mermaid
stateDiagram-v2
    [*] --> sleeping : initial state (replicas 0)
    
    sleeping --> starting : wake signal (first request)
    starting --> ready : health check passes
    starting --> sleeping : startup timeout exceeded
    
    ready --> sleeping : idle timeout reached
    ready --> starting : health failure → restart
    
    note right of sleeping : Container at 0 replicas\nZero resource usage
    note right of ready : Monitoring activity\nTracking WebSocket connections
```

### Unmanaged

```mermaid
stateDiagram-v2
    [*] --> running
    running --> running : always running, no management
```

## Scaling Path

| Threshold | What to Add |
|---|---|
| **Now** | Single-node Swarm, overlay network, Swarm DNS routing |
| **10+ agents** | Dynamic port allocation if needed |
| **20+ on-demand** | LRU eviction — sleep least-recently-used agent to free resources for a wake |
| **50+ agents** | Config hot-reload (SIGHUP), centralised logging |
| **100+ agents** | Multi-node Swarm (stack file works unchanged) |

On-demand agents at `replicas: 0` cost nothing. The binding constraint is always-on agents consuming continuous resources. The on-demand pattern is what makes density possible — 50 agents configured, 3-5 awake at any time.

## Design Decisions

### Why Swarm and Not Kubernetes?

Single server. Swarm is built into Docker, requires zero additional infrastructure, and provides everything needed: service lifecycle, secrets, resource limits, overlay networking, rolling updates. Kubernetes is massive overkill for a single-node deployment managing 5-50 services.

### Why a Custom Proxy and Not Traefik/Caddy?

Traefik and Caddy are excellent reverse proxies but they don't do wake-on-demand. They can route by hostname and terminate TLS, but they can't scale a Swarm service from 0→1 on the first request, track WebSocket activity for idle detection, or manage agent-created dynamic service routes. The orchestrator fills the gap between "reverse proxy" and "service mesh."

### Why Swarm Manages Always-On, Not the Orchestrator?

Swarm's restart policy (`condition: any`, `max_attempts`, `delay`, `window`) handles always-on agent recovery natively. The orchestrator monitors health for routing decisions and emits events when agents degrade, but never calls start/restart/stop on always-on services. One restart loop, not two.

### Why 503 + Poll Instead of Connection Holding?

When an on-demand agent is sleeping, the orchestrator returns 503 with the agent's state in the body and a `Retry-After` header. The frontend polls `/api/health` until the agent is ready, then retries. This is simpler than holding the connection open, avoids request buffering complexity, and gives the frontend full control over the loading UX.

### Why Overlay Network?

Services communicate over Swarm's encrypted overlay network and are addressed by DNS name (`tasks.<service>:<port>`). No host port mapping, no port conflicts, no allocation needed. The orchestrator is the only process that publishes a host port (`:8080` for the tunnel).

### Why Supervisord Inside Containers?

An OpenClaw agent often needs multiple processes: the OpenClaw gateway and one or more companion services (like MissionControl). These are tightly coupled — they scale together, share a filesystem, and communicate over localhost. Supervisord is the simplest way to manage multiple processes in a single container. The alternative (separate containers per process) would require inter-container networking, shared volumes, and coordinated scaling — all complexity that provides no benefit when the processes are 1:1 coupled.

### Why Not Docker Compose?

Docker Compose doesn't provide: secrets management, service scaling (replicas 0↔1), health-check-driven restarts, resource limits, overlay networking, or declarative stack deployment. Swarm mode provides all of these with the same Docker CLI. The migration from Compose to Swarm is minimal — the stack file is nearly identical to a compose file.