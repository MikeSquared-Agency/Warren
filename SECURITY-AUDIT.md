# Warren Security Audit

**Date:** 2026-02-11
**Auditor:** Automated Security Review
**Scope:** All Go source files in the Warren project
**Deployment Model:** Behind Cloudflare Tunnel, Docker Swarm overlay network

---

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 1     |
| HIGH     | 3     |
| MEDIUM   | 5     |
| LOW      | 4     |
| INFO     | 4     |

---

## CRITICAL

### C1: Unauthenticated Service Registration API Exposed on Public Port (SSRF)

- **File:** `internal/proxy/proxy.go`, lines 63–65 (handleServiceAPI route) and lines 120–145 (handleServiceAPI handler)
- **Description:** The `/api/services` endpoint is served on the **public proxy port** (`:8080`) on *any hostname*. Any external client can POST to register arbitrary backend targets (e.g., `http://169.254.169.254/latest/meta-data/`, `http://localhost:2375/`, internal Docker socket endpoints). Subsequent requests to the registered hostname are proxied to the attacker-controlled target, creating a full **SSRF primitive**. An attacker can also **overwrite existing configured routes** by registering a hostname that matches a configured agent, hijacking all traffic to that agent.
- **Exploitable behind Cloudflare Tunnel:** **YES** — Cloudflare Tunnel forwards HTTP requests including POST. An attacker who knows the tunnel URL can register arbitrary routes.
- **Recommended Fix:**
  1. Move `/api/services` to the admin port only, OR
  2. Require authentication (shared secret / mTLS), OR
  3. At minimum, validate targets against an allowlist and prevent overwriting configured hostnames.

**Fix applied:** Service API moved to admin-only port; public port now rejects `/api/services` requests. Additionally, registry now rejects targets pointing to link-local, loopback, and metadata endpoints.

---

## HIGH

### H1: No Request Body Size Limit on Service Registration

- **File:** `internal/proxy/proxy.go`, line 130 (`json.NewDecoder(r.Body).Decode`)
- **Description:** The JSON decoder reads the entire request body with no size limit. An attacker can send a multi-gigabyte POST body to exhaust memory.
- **Exploitable behind Cloudflare Tunnel:** Partially — Cloudflare has its own body size limits (~100MB free tier), but this is still exploitable from internal network.
- **Recommended Fix:** Wrap `r.Body` with `http.MaxBytesReader` before decoding.

**Fix applied:** Added `http.MaxBytesReader(w, r.Body, 1<<20)` (1MB limit).

### H2: Dynamic Service Registration Can Overwrite Configured Agent Hostnames

- **File:** `internal/services/registry.go`, line 33 (Register method); `internal/proxy/proxy.go`, line 70 (lookup order)
- **Description:** Configured backends are checked first in `ServeHTTP`, so a dynamic registration can't *directly* hijack them. However, the registry still accepts registrations for those hostnames, and the admin API `/admin/services` would show misleading data. More critically, if the code order ever changes, or if hostnames are added to agents dynamically, this becomes a full hijack.
- **Recommended Fix:** Registry.Register should reject hostnames that match configured backends.

**Fix applied:** Proxy now passes configured hostnames to registry; registry rejects collisions.

### H3: No Timeout or Connection Limits on WebSocket Proxy

- **File:** `internal/proxy/websocket.go`, lines 100–160 (`handleWebSocket`)
- **Description:** WebSocket connections have no read/write deadlines and no per-agent or global connection limit. A slow-read/slow-write attack can hold connections open indefinitely, exhausting file descriptors and goroutines (4 goroutines per WS connection: 2 for io.Copy, 1 for context cancellation, plus the HTTP handler goroutine).
- **Exploitable behind Cloudflare Tunnel:** Partially — Cloudflare has its own WS timeout (~100s idle), but direct access to the port bypasses this.
- **Recommended Fix:** Add read/write deadlines on both client and backend connections; add a global max WS connection limit.

**Fix applied:** Added 5-minute read/write deadlines refreshed on activity; connections are now closed if truly idle.

---

## MEDIUM

### M1: Admin API Has No Authentication

- **File:** `internal/admin/admin.go`, all handlers
- **Description:** The admin API (wake/sleep agents, list services, health info) has no authentication. Anyone who can reach the admin port can wake/sleep agents, potentially causing DoS.
- **Exploitable behind Cloudflare Tunnel:** **NO** — admin port (`:9090`) is separate and not exposed through the tunnel. Only exploitable if the admin port is accidentally exposed or from within the Docker network.
- **Recommended Fix:** Add basic auth or bearer token authentication to the admin API. Document that the admin port must never be publicly exposed.

### M2: Webhook URLs Not Validated (SSRF via Config)

- **File:** `internal/alerts/webhook.go`, line 57 (`http.NewRequest` with `cfg.URL`)
- **Description:** Webhook URLs from config are not validated. A malicious config could set webhook URLs to internal services, causing the orchestrator to make requests to internal endpoints on every event.
- **Exploitable behind Cloudflare Tunnel:** **NO** — requires config file access.
- **Recommended Fix:** Validate webhook URLs at config load time; reject private/internal IP ranges.

### M3: Health Check URLs Not Validated (SSRF via Config)

- **File:** `internal/container/health.go`, line 15 (`http.NewRequestWithContext` with arbitrary URL)
- **Description:** Health check URLs are taken from config without validation. A malicious config could point health checks at internal services, causing periodic requests to arbitrary internal endpoints.
- **Exploitable behind Cloudflare Tunnel:** **NO** — requires config file access.
- **Recommended Fix:** Validate health URLs at config load time.

### M4: No Rate Limiting on Wake/Sleep Cycling

- **File:** `internal/policy/on_demand.go` (Wake/Sleep methods); `internal/proxy/proxy.go` (OnRequest triggers wake)
- **Description:** Rapid requests to a sleeping agent trigger repeated Docker API calls (scale 0→1→0→1). While there's a startup timeout, there's no cooldown between wake attempts, potentially exhausting Docker API rate limits or causing container churn.
- **Exploitable behind Cloudflare Tunnel:** **YES** — rapid requests through the tunnel can trigger wake cycling.
- **Recommended Fix:** Add a cooldown period between wake attempts (e.g., don't re-wake within 30s of last sleep).

### M5: Goroutine Leak in Webhook Alerter

- **File:** `internal/alerts/webhook.go`, line 42 (`go w.send(cfg, ev)`)
- **Description:** Each webhook send spawns an unbounded goroutine. A flood of events combined with slow/unresponsive webhook targets can lead to goroutine exhaustion.
- **Recommended Fix:** Use a bounded worker pool or buffered channel for webhook sends.

---

## LOW

### L1: Error Messages May Leak Internal Topology

- **File:** `internal/proxy/proxy.go`, line 48 (ErrorHandler logs backend address)
- **Description:** The proxy error handler logs the backend address and error details. While these go to structured logs (not HTTP responses), log aggregation systems could expose internal service names and ports.
- **Recommended Fix:** Ensure log outputs are not accessible to external users. The HTTP error response correctly returns only "bad gateway" — this is fine.

### L2: Dynamic Service Target URL Parsed Per-Request

- **File:** `internal/proxy/proxy.go`, line 79 (`url.Parse(svc.Target)`)
- **Description:** Each request to a dynamic service re-parses the target URL and creates a new `ReverseProxy`. This is inefficient and means malformed URLs stored in the registry cause per-request error logging.
- **Recommended Fix:** Validate and parse the URL at registration time; cache the `ReverseProxy` instance.

### L3: No Hostname Format Validation

- **File:** `internal/config/validate.go` — no hostname format checks; `internal/services/registry.go` — no hostname validation
- **Description:** Hostnames are not validated for format (e.g., could contain spaces, newlines, or other special characters). While `stripPort` handles the port, unusual characters could cause unexpected routing behavior.
- **Recommended Fix:** Validate hostnames against RFC 952/1123 at config load time and at dynamic registration time.

### L4: WriteTimeout Set to 0 on Public Server

- **File:** `cmd/orchestrator/main.go`, line 155 (`WriteTimeout: 0`)
- **Description:** `WriteTimeout: 0` disables write timeout, necessary for SSE/WS but means a slow client can hold a regular HTTP connection's goroutine indefinitely.
- **Recommended Fix:** Consider using `http.ResponseController` per-request to extend timeouts only for streaming connections, or accept the risk with documentation.

---

## INFO

### I1: Docker Socket Mounted Read-Only

- **File:** `deploy/stack.yaml`, line 44 (`/var/run/docker.sock:/var/run/docker.sock:ro`)
- **Description:** The stack config mounts the Docker socket as read-only (`:ro`), but `ServiceUpdate` (used for scaling) requires write access. This will fail at runtime. The `:ro` mount provides a false sense of security — if write access is needed, it must be removed, and other mitigations should be used.
- **Recommended Fix:** Remove `:ro` (since write is needed for scaling) and document the security implications. Consider using a Docker socket proxy like `tecnativa/docker-socket-proxy` to limit API access to only the endpoints needed.

### I2: Prometheus Metrics Expose Agent Names

- **File:** `internal/metrics/metrics.go`
- **Description:** Metrics include agent names as labels. This is standard for operational monitoring but could leak internal agent names if the metrics endpoint is exposed.
- **Recommended Fix:** Metrics are served on the admin port — ensure it stays that way.

### I3: Encrypted Overlay Network — Good Practice

- **File:** `deploy/stack.yaml`, line 12 (`encrypted: "true"`)
- **Description:** The overlay network is encrypted, which protects inter-node traffic. This is good practice and mitigates the lack of TLS between orchestrator and backends.

### I4: No TLS Between Orchestrator and Backends

- **Description:** All backend communication uses plain HTTP over the Docker overlay network. This is acceptable because the overlay is encrypted (I3), but if agents ever run on external networks, TLS should be added.

---

## Fixes Applied

The following source code changes were made to address CRITICAL and HIGH findings:

### Fix for C1: Move Service API to Admin Port + SSRF Target Validation

1. **`internal/proxy/proxy.go`**: Removed `/api/services` routing from public `ServeHTTP`; returns 404 for service API on public port.
2. **`internal/services/registry.go`**: Added target URL validation (rejects private IPs, link-local, metadata endpoints) and hostname collision protection.
3. **`cmd/orchestrator/main.go`**: Mounted service API endpoints on the admin mux.

### Fix for H1: Request Body Size Limit

1. **`internal/proxy/proxy.go`** (now in admin handler): Added `http.MaxBytesReader` with 1MB limit.

### Fix for H2: Hostname Collision Protection

1. **`internal/services/registry.go`**: `Register` now accepts a set of reserved hostnames and rejects collisions.

### Fix for H3: WebSocket Connection Deadlines

1. **`internal/proxy/websocket.go`**: Added 5-minute read/write deadlines on both sides of the WebSocket tunnel.
