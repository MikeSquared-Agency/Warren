#!/usr/bin/env bash
set -euo pipefail

# ── Warren Swarm Smoke Test ─────────────────────────────────────────
# Checks health of all Warren services and alerts to Slack on failure.
# Run via cron every 15 minutes.
# ─────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load .env if present
if [[ -f "$SCRIPT_DIR/.env" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$SCRIPT_DIR/.env"
    set +a
fi

SLACK_BOT_TOKEN="${SLACK_BOT_TOKEN:-}"
SLACK_CHANNEL="${SLACK_CHANNEL:-C0AE9AMK9MM}"
ALEXANDRIA_URL="http://localhost:8500"
CURL_TIMEOUT=5

# ── Service definitions ──────────────────────────────────────────────
# Services reachable from the host (published ports):
HOST_SERVICES=(
    "NATS (Hermes)|http://localhost:8222/healthz"
    "Alexandria|http://localhost:8500/api/v1/health"
    "PromptForge|http://localhost:8083/health"
    "OpenClaw Gateway|tcp://localhost:8080"
)

# Services only reachable via Docker overlay network (no published ports).
# Checked from inside the Alexandria container.
DOCKER_SERVICES=(
    "Dispatch|http://warren_dispatch:8601/health"
    "Chronicle|http://warren_chronicle:8700/api/v1/health"
    "Slack-gateway|http://warren_slack-gateway:8750/api/v1/health"
)

# Container to exec health checks from (must be on the warren_agents network).
DOCKER_PROBE_CONTAINER="warren_alexandria"

# ── Try to fetch Slack bot token from Alexandria vault ───────────────
# GET requests skip API key auth; only X-Agent-ID is needed.
# smoke-test agent has a read grant on slack_bot_token.
fetch_token_from_vault() {
    local resp
    resp=$(curl -sf --max-time "$CURL_TIMEOUT" \
        -H "X-Agent-ID: smoke-test" \
        "$ALEXANDRIA_URL/api/v1/secrets/slack_bot_token" 2>/dev/null) || return 1
    local token
    token=$(echo "$resp" | jq -r '.data.value // empty' 2>/dev/null) || return 1
    if [[ -n "$token" ]]; then
        SLACK_BOT_TOKEN="$token"
        return 0
    fi
    return 1
}

if [[ -z "$SLACK_BOT_TOKEN" ]]; then
    fetch_token_from_vault || true
fi

# ── Health check helpers ─────────────────────────────────────────────
failures=()
total=0
passed=0

check_result() {
    local name="$1" http_code="$2"
    total=$((total + 1))
    if [[ "$http_code" =~ ^2 ]]; then
        echo "[PASS] $name"
        passed=$((passed + 1))
    else
        local reason="HTTP $http_code"
        [[ "$http_code" == "000" ]] && reason="connection refused or timeout"
        echo "[FAIL] $name - $reason"
        failures+=("$name ($reason)")
    fi
}

# ── Check host-reachable services ────────────────────────────────────
for entry in "${HOST_SERVICES[@]}"; do
    name="${entry%%|*}"
    url="${entry##*|}"
    if [[ "$url" == tcp://* ]]; then
        # TCP connect check (for services with no health route)
        local_addr="${url#tcp://}"
        local_host="${local_addr%%:*}"
        local_port="${local_addr##*:}"
        if bash -c "echo >/dev/tcp/$local_host/$local_port" 2>/dev/null; then
            http_code="200"
        else
            http_code="000"
        fi
    else
        http_code=$(curl -sf --max-time "$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' "$url" 2>/dev/null) || http_code="000"
    fi
    check_result "$name" "$http_code"
done

# ── Check Docker-internal services ───────────────────────────────────
# Resolve the actual task container name (swarm appends a task suffix).
probe_id=$(docker ps -q -f "name=${DOCKER_PROBE_CONTAINER}" 2>/dev/null | head -1)
if [[ -z "$probe_id" ]]; then
    echo "[WARN] Probe container ${DOCKER_PROBE_CONTAINER} not running — skipping Docker service checks"
    for entry in "${DOCKER_SERVICES[@]}"; do
        name="${entry%%|*}"
        total=$((total + 1))
        echo "[FAIL] $name - probe container unavailable"
        failures+=("$name (probe container unavailable)")
    done
else
    for entry in "${DOCKER_SERVICES[@]}"; do
        name="${entry%%|*}"
        url="${entry##*|}"
        http_code=$(docker exec "$probe_id" \
            wget -qO /dev/null --spider -T "$CURL_TIMEOUT" "$url" 2>&1 \
            && echo "200" || echo "000") 2>/dev/null
        check_result "$name" "$http_code"
    done
fi

echo "---"
echo "$passed/$total services healthy"

# ── Slack alert on failure ───────────────────────────────────────────
send_slack_alert() {
    if [[ -z "$SLACK_BOT_TOKEN" ]]; then
        echo "[WARN] No SLACK_BOT_TOKEN configured — skipping Slack alert"
        return 0
    fi

    local hostname
    hostname=$(hostname -f 2>/dev/null || hostname)
    local timestamp
    timestamp=$(date -u '+%Y-%m-%d %H:%M:%S UTC')

    local fail_lines=""
    for f in "${failures[@]}"; do
        fail_lines+="\\n- $f"
    done

    local payload
    payload=$(cat <<EOJSON
{
    "channel": "$SLACK_CHANNEL",
    "text": "Warren smoke test: ${#failures[@]} service(s) DOWN",
    "blocks": [
        {
            "type": "header",
            "text": {
                "type": "plain_text",
                "text": "Warren Smoke Test Alert",
                "emoji": true
            }
        },
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": "*${#failures[@]} of $total services failing*\\nHost: \`$hostname\`\\nTime: $timestamp"
            }
        },
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": "*Failed services:*$fail_lines"
            }
        }
    ]
}
EOJSON
    )

    local resp
    resp=$(curl -sf --max-time 10 \
        -X POST \
        -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
        -H "Content-Type: application/json; charset=utf-8" \
        -d "$payload" \
        "https://slack.com/api/chat.postMessage" 2>/dev/null) || {
        echo "[WARN] Failed to send Slack alert"
        return 0
    }

    if echo "$resp" | jq -e '.ok == true' >/dev/null 2>&1; then
        echo "[INFO] Slack alert sent"
    else
        local err
        err=$(echo "$resp" | jq -r '.error // "unknown"' 2>/dev/null)
        echo "[WARN] Slack API error: $err"
    fi
}

if [[ ${#failures[@]} -gt 0 ]]; then
    send_slack_alert
    exit 1
fi

exit 0
