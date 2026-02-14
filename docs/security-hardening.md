# Security Hardening: Vault Integration & Port Lockdown

## Summary

This change hardens the Warren Docker Swarm stack by:

1. **Moving secrets to Alexandria vault** — Services fetch database URLs, API keys, and tokens from the vault at boot instead of having them as plaintext environment variables.
2. **Removing unnecessary port exposure** — Internal services (NATS, dispatch, promptforge, slack-forwarder, chronicle) no longer publish ports to the host.
3. **Adding NATS authentication** — Hermes requires a 64-char auth token; all consumers embed it in their NATS URL.
4. **Replacing weak tokens** — All service-to-service tokens replaced with `openssl rand -hex 32` values.
5. **Using Docker secrets for Alexandria bootstrap** — `DATABASE_URL` and `ENCRYPTION_KEY` are delivered via Docker secrets (not env vars) since Alexandria can't fetch from itself.
6. **Removing pg-proxy** — Unused direct Postgres proxy container removed.

## Architecture

```
                    ┌──────────────────────────────────────┐
                    │          Docker Swarm (overlay)       │
                    │                                      │
  Cloudflare ──► Orchestrator ──► lily       (host:18790)  │
  Tunnel         :8080           scout      (host systemd) │
                                 dutybound  (host:18793)   │
                                 celebrimbor (host:18791)  │
                                                           │
                    │  Internal only (no host ports):       │
                    │    hermes (NATS+auth)                 │
                    │    dispatch ──► vault-entrypoint.sh   │
                    │    chronicle ──► vault-entrypoint.sh  │
                    │    promptforge ──► vault-entrypoint.sh│
                    │    slack-forwarder ──► vault-entrypoint│
                    │                                      │
                    │  Alexandria :8500 (host mode)         │
                    │    ├── Docker secrets: DB_URL, ENC_KEY│
                    │    └── Vault API: /api/v1/secrets     │
                    └──────────────────────────────────────┘
```

## Vault-Entrypoint Pattern

Services that need secrets use `deploy/vault-entrypoint.sh` as their Docker entrypoint. The script:

1. Waits for Alexandria to be reachable (retry loop with configurable timeout)
2. Fetches each secret via `GET /api/v1/secrets/{name}` with `X-Agent-ID` header
3. Exports the decrypted value as an environment variable
4. Execs the original binary

Configuration is via environment variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `VAULT_AGENT_ID` | Agent identity for access control | `dispatch` |
| `VAULT_SECRETS` | Comma-separated `secret:ENV_VAR` mappings | `supabase_db_url:DATABASE_URL` |
| `VAULT_URL` | Alexandria base URL (default: overlay) | `http://warren_alexandria:8500` |
| `VAULT_TIMEOUT` | Max seconds to wait (default: 60) | `60` |

Works on both Alpine (wget) and Debian (curl) images.

## Secret Ownership

| Secret | Services with Access |
|--------|---------------------|
| `supabase_db_url` | dispatch, chronicle, alexandria |
| `supabase_service_role_key` | alexandria, promptforge |
| `supabase_url` | promptforge |
| `encryption_key` | alexandria |
| `gemini_api_key` | lily |
| `slack_app_token` | slack-forwarder |
| `slack_bot_token` | slack-forwarder |

## What Stays as Env Vars

- **Internal tokens** (DISPATCH_ADMIN_TOKEN, OPENCLAW_GATEWAY_TOKEN, etc.) — service-to-service auth tokens that only exist on the encrypted overlay network.
- **Non-secret config** (ports, URLs, model names, agent IDs).
- **NATS auth token** — embedded in each service's NATS_URL. Internal overlay only.
- **Alexandria SUPABASE_KEY** — bootstrap dependency (Alexandria is the vault, can't self-fetch).

## Port Exposure Summary

| Service | Port | Mode | Why |
|---------|------|------|-----|
| alexandria | 8500 | host | Host services + orchestrator need it |
| lily | 18790 | host | Orchestrator proxies to it |
| dutybound-mc | 18793 | host | Orchestrator proxies to it |
| celebrimbor | 18791 | host | Orchestrator proxies to it |
| scout | — | systemd | Runs as bare metal host service |
| hermes | — | — | Overlay only, NATS auth required |
| dispatch | — | — | Overlay only |
| promptforge | — | — | Overlay only |
| chronicle | — | — | Overlay only |
| slack-forwarder | — | — | Overlay only, outbound websocket |

## Testing

### Unit Tests (Go)

```bash
go test ./internal/security/... -run TestStack -v
```

Validates stack.yaml for: no plaintext secrets, correct port exposure, NATS auth, Docker secrets, vault-entrypoint usage, host-mode ports.

### Unit Tests (Shell)

```bash
bash tests/security/test_vault_entrypoint.sh
```

Tests vault-entrypoint.sh: input validation, timeout behavior, secret parsing, multi-secret support, exec passthrough.

### E2E Tests (Live Stack)

```bash
bash tests/security/test_stack_security_e2e.sh
```

Verifies against running Docker Swarm: ports closed, pg-proxy removed, vault seeded, service envs clean, services healthy, NATS auth enforced, Docker secrets mounted.

## Follow-up (Not in Scope)

- **UFW/firewall rules** — Separate session to avoid SSH lockout risk.
- **Deeper openclaw-friend vault integration** — Agents currently use existing Alexandria auth token fetch; vault for all config could come later.
- **Secret rotation** — Alexandria supports `/secrets/{name}/rotate` but automated rotation isn't wired up yet.
