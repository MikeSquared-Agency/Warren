#!/usr/bin/env bash
set -euo pipefail

# ── Warren Swarm Smoke Test ─────────────────────────────────────────
# Checks health of all Warren services and alerts to Slack on failure.
# Run via cron every 15 minutes.
#
# Host-reachable services are checked directly via localhost.
# Overlay-only services (no published ports) are probed from INSIDE
# the Docker network using `docker exec` against a running container.
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
OPENCLAW_WEBHOOK_URL="${OPENCLAW_WEBHOOK_URL:-http://localhost:18789}"
OPENCLAW_WEBHOOK_TOKEN="${OPENCLAW_WEBHOOK_TOKEN:-}"
ALEXANDRIA_URL="http://localhost:8500"
CURL_TIMEOUT=5

# ── Overlay probe setup ─────────────────────────────────────────────
# Find a container on the warren_agents network to use as a probe.
# Prefer alexandria (Python image, has urllib). Falls back to wget/curl
# in any warren container, or docker run as last resort.
PROBE_CONTAINER=""
PROBE_CMD=""  # "wget" or "curl" or "python"

find_probe() {
    local candidates=("warren_alexandria" "warren_hermes" "warren_dispatch" "warren_chronicle")
    for name in "${candidates[@]}"; do
        local cid
        cid=$(docker ps -q -f "name=${name}" 2>/dev/null | head -1) || continue
        [[ -z "$cid" ]] && continue

        # Check for wget (Alpine default)
        if docker exec "$cid" which wget >/dev/null 2>&1; then
            PROBE_CONTAINER="$cid"
            PROBE_CMD="wget"
            return 0
        fi
        # Check for curl
        if docker exec "$cid" which curl >/dev/null 2>&1; then
            PROBE_CONTAINER="$cid"
            PROBE_CMD="curl"
            return 0
        fi
        # Check for python3 (can do HTTP)
        if docker exec "$cid" which python3 >/dev/null 2>&1; then
            PROBE_CONTAINER="$cid"
            PROBE_CMD="python"
            return 0
        fi
    done
    return 1
}

# Execute an HTTP GET from inside the overlay network.
# Usage: overlay_get <url> [header:value ...]
# Returns: HTTP body on stdout, exits 0 on 2xx, 1 otherwise.
overlay_get() {
    local url="$1"; shift
    local headers=("$@")

    if [[ -z "$PROBE_CONTAINER" ]]; then
        echo ""
        return 1
    fi

    case "$PROBE_CMD" in
        wget)
            local wget_args=("-qO-" "--timeout=$CURL_TIMEOUT")
            for h in "${headers[@]}"; do
                wget_args+=("--header=$h")
            done
            wget_args+=("$url")
            docker exec "$PROBE_CONTAINER" wget "${wget_args[@]}" 2>/dev/null
            ;;
        curl)
            local curl_args=("-sf" "--max-time" "$CURL_TIMEOUT")
            for h in "${headers[@]}"; do
                curl_args+=("-H" "$h")
            done
            curl_args+=("$url")
            docker exec "$PROBE_CONTAINER" curl "${curl_args[@]}" 2>/dev/null
            ;;
        python)
            local py_headers=""
            for h in "${headers[@]}"; do
                local key="${h%%:*}"
                local val="${h#*: }"
                py_headers+="req.add_header('$key','$val');"
            done
            docker exec "$PROBE_CONTAINER" python3 -c "
import urllib.request,sys,json
try:
    req=urllib.request.Request('$url')
    $py_headers
    resp=urllib.request.urlopen(req,timeout=$CURL_TIMEOUT)
    sys.stdout.write(resp.read().decode())
except Exception as e:
    sys.exit(1)
" 2>/dev/null
            ;;
        *)
            return 1
            ;;
    esac
}

# overlay_check: like overlay_get but returns HTTP status code string
overlay_check() {
    local url="$1"; shift
    local headers=("$@")

    if [[ -z "$PROBE_CONTAINER" ]]; then
        echo "000"
        return 0
    fi

    case "$PROBE_CMD" in
        wget)
            # wget: if it succeeds, it's 2xx
            local wget_args=("-qO/dev/null" "--timeout=$CURL_TIMEOUT" "-S")
            for h in "${headers[@]}"; do
                wget_args+=("--header=$h")
            done
            wget_args+=("$url")
            if docker exec "$PROBE_CONTAINER" wget "${wget_args[@]}" >/dev/null 2>&1; then
                echo "200"
            else
                echo "000"
            fi
            ;;
        curl)
            local curl_args=("-sf" "--max-time" "$CURL_TIMEOUT" "-o" "/dev/null" "-w" "%{http_code}")
            for h in "${headers[@]}"; do
                curl_args+=("-H" "$h")
            done
            curl_args+=("$url")
            docker exec "$PROBE_CONTAINER" curl "${curl_args[@]}" 2>/dev/null || echo "000"
            ;;
        python)
            local py_headers=""
            for h in "${headers[@]}"; do
                local key="${h%%:*}"
                local val="${h#*: }"
                py_headers+="req.add_header('$key','$val');"
            done
            docker exec "$PROBE_CONTAINER" python3 -c "
import urllib.request,sys
try:
    req=urllib.request.Request('$url')
    $py_headers
    resp=urllib.request.urlopen(req,timeout=$CURL_TIMEOUT)
    print(resp.status)
except urllib.error.HTTPError as e:
    print(e.code)
except:
    print('000')
" 2>/dev/null
            ;;
        *)
            echo "000"
            ;;
    esac
}

# ── Service definitions ──────────────────────────────────────────────

# Host-reachable services (published ports).
# Format: name|health_url|fallback_url (fallback is optional)
HOST_HEALTH_SERVICES=(
    "Alexandria|http://localhost:8500/health|http://localhost:8500/api/v1/health"

)

# Host-reachable simple services (no /health JSON endpoint).
HOST_SIMPLE_SERVICES=(
    "NATS (Hermes)|http://localhost:8222/healthz"
    "OpenClaw Gateway|tcp://localhost:18789"
)

# Overlay-only services — probed from inside the Docker network.
# Format: name|health_url|fallback_url|docker_service_name
OVERLAY_HEALTH_SERVICES=(
    "Dispatch|http://warren_dispatch:8600/health|http://warren_dispatch:8600/api/v1/backlog?limit=1|warren_dispatch"
    "Chronicle|http://warren_chronicle:8700/health||warren_chronicle"
    "Slack-gateway|http://warren_slack-gateway:8750/health||warren_slack-gateway"
)

# ── Try to fetch Slack bot token from Alexandria vault ───────────────
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
degraded=()
total=0
passed=0

check_pass() {
    local name="$1"
    total=$((total + 1))
    echo "[PASS] $name"
    passed=$((passed + 1))
}

check_degraded() {
    local name="$1" detail="$2"
    total=$((total + 1))
    echo "[DEGRADED] $name - $detail"
    passed=$((passed + 1))  # service is up, but degraded
    degraded+=("$name ($detail)")
}

check_fail() {
    local name="$1" reason="$2"
    total=$((total + 1))
    echo "[FAIL] $name - $reason"
    failures+=("$name ($reason)")
}

# Parse /health JSON body and classify as pass/degraded/fail.
evaluate_health_body() {
    local name="$1" body="$2"
    local status
    status=$(echo "$body" | jq -r '.status // empty' 2>/dev/null)
    case "$status" in
        ok|healthy|UP)
            local db_status
            db_status=$(echo "$body" | jq -r '.db // .database // .components.db.status // empty' 2>/dev/null)
            if [[ -n "$db_status" && "$db_status" != "ok" && "$db_status" != "healthy" && "$db_status" != "UP" ]]; then
                check_degraded "$name" "service up, db: $db_status"
            else
                check_pass "$name"
            fi
            ;;
        degraded)
            local detail
            detail=$(echo "$body" | jq -r '.message // .reason // "degraded"' 2>/dev/null)
            check_degraded "$name" "$detail"
            ;;
        *)
            check_fail "$name" "unhealthy status: ${status:-unknown}"
            ;;
    esac
}

# ── Check host-reachable /health services ────────────────────────────
for entry in "${HOST_HEALTH_SERVICES[@]}"; do
    IFS='|' read -r name health_url fallback_url <<< "$entry"

    body=$(curl -sf --max-time "$CURL_TIMEOUT" "$health_url" 2>/dev/null) || body=""

    if [[ -n "$body" ]]; then
        evaluate_health_body "$name" "$body"
        continue
    fi

    # Fallback to legacy endpoint if configured
    if [[ -n "$fallback_url" ]]; then
        http_code=$(curl -sf --max-time "$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' \
            -H "X-Agent-ID: smoke-test" "$fallback_url" 2>/dev/null) || http_code="000"
        if [[ "$http_code" =~ ^2 ]]; then
            check_pass "$name"
        else
            [[ "$http_code" == "000" ]] && http_code="connection refused or timeout"
            check_fail "$name" "HTTP $http_code (fallback)"
        fi
        continue
    fi

    check_fail "$name" "connection refused or timeout"
done

# ── Check host-reachable simple services ─────────────────────────────
for entry in "${HOST_SIMPLE_SERVICES[@]}"; do
    name="${entry%%|*}"
    url="${entry##*|}"
    if [[ "$url" == tcp://* ]]; then
        local_addr="${url#tcp://}"
        local_host="${local_addr%%:*}"
        local_port="${local_addr##*:}"
        if bash -c "echo >/dev/tcp/$local_host/$local_port" 2>/dev/null; then
            check_pass "$name"
        else
            check_fail "$name" "connection refused or timeout"
        fi
    else
        http_code=$(curl -sf --max-time "$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' "$url" 2>/dev/null) || http_code="000"
        if [[ "$http_code" =~ ^2 ]]; then
            check_pass "$name"
        else
            [[ "$http_code" == "000" ]] && http_code="connection refused or timeout"
            check_fail "$name" "HTTP $http_code"
        fi
    fi
done

# ── Check overlay-only services via docker exec probe ────────────────
find_probe || echo "[WARN] No probe container found — overlay checks will use replica count only"

for entry in "${OVERLAY_HEALTH_SERVICES[@]}"; do
    IFS='|' read -r name health_url fallback_url svc_name <<< "$entry"

    if [[ -n "$PROBE_CONTAINER" ]]; then
        # Try /health endpoint from inside the network
        body=$(overlay_get "$health_url") || body=""

        if [[ -n "$body" ]]; then
            evaluate_health_body "$name" "$body"
            continue
        fi

        # Try fallback URL if configured
        if [[ -n "$fallback_url" ]]; then
            fb_body=$(overlay_get "$fallback_url" "X-Agent-ID: smoke-test") || fb_body=""
            if [[ -n "$fb_body" ]]; then
                check_pass "$name"
                continue
            fi
        fi

        # If probe exists but HTTP failed, check if container responds at all
        # before falling through to replica count
        check_code=$(overlay_check "$health_url") || check_code="000"
        if [[ "$check_code" =~ ^2 ]]; then
            check_pass "$name"
            continue
        fi
    fi

    # Fallback: check docker service replica count
    replicas=$(docker service ls --filter "name=${svc_name}" --format '{{.Replicas}}' 2>/dev/null | head -1)
    if [[ -z "$replicas" ]]; then
        check_fail "$name" "service not found"
        continue
    fi
    running="${replicas%%/*}"
    desired="${replicas##*/}"
    if [[ "$running" == "$desired" && "$running" -gt 0 ]] 2>/dev/null; then
        if [[ -n "$PROBE_CONTAINER" ]]; then
            # We had a probe but HTTP failed — service is running but not responding
            check_degraded "$name" "replicas $replicas but health endpoint unreachable"
        else
            check_pass "$name" # no probe available, replicas OK is best we can do
        fi
    elif [[ "$running" -gt 0 ]] 2>/dev/null; then
        check_degraded "$name" "replicas $replicas"
    else
        check_fail "$name" "replicas $replicas"
    fi
done

echo "---"
echo "$passed/$total services healthy"
if [[ ${#degraded[@]} -gt 0 ]]; then
    echo "${#degraded[@]} service(s) degraded"
fi

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
    local alert_id
    alert_id="smoke_$(date +%s)"

    local fail_lines=""
    for f in "${failures[@]}"; do
        fail_lines+="\\n- $f"
    done

    local degraded_lines=""
    for d in "${degraded[@]}"; do
        degraded_lines+="\\n- $d"
    done

    local summary="${#failures[@]} service(s) DOWN"
    if [[ ${#degraded[@]} -gt 0 ]]; then
        summary+=", ${#degraded[@]} degraded"
    fi

    # Build Block Kit blocks with action buttons per failed service.
    local blocks
    blocks=$(cat <<EOJSON
[
    {
        "type": "header",
        "text": {
            "type": "plain_text",
            "text": "🚨 Warren Smoke Test Alert",
            "emoji": true
        }
    },
    {
        "type": "section",
        "text": {
            "type": "mrkdwn",
            "text": "*$summary*\\nHost: \`$hostname\`\\nTime: $timestamp"
        }
    },
    {
        "type": "section",
        "text": {
            "type": "mrkdwn",
            "text": "*Failed services:*$fail_lines"
        }
    }
EOJSON
    )

    # Add degraded block if any
    if [[ ${#degraded[@]} -gt 0 ]]; then
        blocks+=",
    {
        \"type\": \"section\",
        \"text\": {
            \"type\": \"mrkdwn\",
            \"text\": \"*Degraded services:*$degraded_lines\"
        }
    }"
    fi

    # Add action buttons for each failed service.
    for f in "${failures[@]}"; do
        # Extract service name: "ServiceName (reason)" → lowercase, no spaces
        local svc_display="${f%% (*}"
        local svc_name
        svc_name=$(echo "$svc_display" | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | sed 's/[()]//g')

        blocks+=",
    {
        \"type\": \"divider\"
    },
    {
        \"type\": \"section\",
        \"text\": {
            \"type\": \"mrkdwn\",
            \"text\": \"*${svc_display}*\"
        }
    },
    {
        \"type\": \"actions\",
        \"elements\": [
            {
                \"type\": \"button\",
                \"text\": {\"type\": \"plain_text\", \"text\": \"🔄 Restart\", \"emoji\": true},
                \"action_id\": \"smoke_restart:${svc_name}\",
                \"value\": \"${svc_name}\",
                \"style\": \"danger\",
                \"confirm\": {
                    \"title\": {\"type\": \"plain_text\", \"text\": \"Restart ${svc_display}?\"},
                    \"text\": {\"type\": \"mrkdwn\", \"text\": \"This will force-restart the Docker service.\"},
                    \"confirm\": {\"type\": \"plain_text\", \"text\": \"Restart\"},
                    \"deny\": {\"type\": \"plain_text\", \"text\": \"Cancel\"}
                }
            },
            {
                \"type\": \"button\",
                \"text\": {\"type\": \"plain_text\", \"text\": \"📋 Logs\", \"emoji\": true},
                \"action_id\": \"smoke_logs:${svc_name}\",
                \"value\": \"${svc_name}\"
            },
            {
                \"type\": \"button\",
                \"text\": {\"type\": \"plain_text\", \"text\": \"✅ Acknowledge\", \"emoji\": true},
                \"action_id\": \"smoke_ack:${alert_id}\",
                \"value\": \"${alert_id}\"
            }
        ]
    }"
    done

    blocks+="]"

    local payload
    payload=$(cat <<EOJSON
{
    "channel": "$SLACK_CHANNEL",
    "text": "Warren smoke test: $summary",
    "blocks": $blocks
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
        echo "[WARN] Failed to send Block Kit alert, trying plain text fallback"
        send_slack_alert_fallback "$summary" "$fail_lines" "$degraded_lines"
        return 0
    }

    if echo "$resp" | jq -e '.ok == true' >/dev/null 2>&1; then
        echo "[INFO] Slack alert sent (Block Kit with action buttons)"
        # Wake Kai to diagnose and fix
        if [[ -n "$OPENCLAW_WEBHOOK_TOKEN" ]]; then
            local msg_ts
            msg_ts=$(echo "$resp" | jq -r '.ts // empty' 2>/dev/null)
            local wake_text="Smoke test failure: $summary [slack_ref:${SLACK_CHANNEL}:${msg_ts}]"
            curl -sf --max-time 5                 -X POST                 -H "Authorization: Bearer $OPENCLAW_WEBHOOK_TOKEN"                 -H "Content-Type: application/json"                 -d "{\"text\":\"$wake_text\",\"mode\":\"now\"}"                 "${OPENCLAW_WEBHOOK_URL}/hooks/wake" >/dev/null 2>&1 &&                 echo "[INFO] OpenClaw webhook sent — Kai will investigate" ||                 echo "[WARN] Failed to send OpenClaw webhook"
        fi
    else
        local err
        err=$(echo "$resp" | jq -r '.error // "unknown"' 2>/dev/null)
        echo "[WARN] Slack API error: $err — trying plain text fallback"
        send_slack_alert_fallback "$summary" "$fail_lines" "$degraded_lines"
    fi
}

# Plain text fallback if Block Kit fails.
send_slack_alert_fallback() {
    local summary="$1" fail_lines="$2" degraded_lines="$3"
    local text="🚨 Warren Smoke Test Alert\n${summary}\n\nFailed:${fail_lines}"
    if [[ -n "$degraded_lines" ]]; then
        text+="\n\nDegraded:${degraded_lines}"
    fi

    local payload
    payload=$(printf '{"channel":"%s","text":"%s"}' "$SLACK_CHANNEL" "$text")

    local resp
    resp=$(curl -sf --max-time 10 \
        -X POST \
        -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
        -H "Content-Type: application/json; charset=utf-8" \
        -d "$payload" \
        "https://slack.com/api/chat.postMessage" 2>/dev/null) || {
        echo "[WARN] Failed to send fallback Slack alert"
        return 0
    }

    if echo "$resp" | jq -e '.ok == true' >/dev/null 2>&1; then
        echo "[INFO] Slack fallback alert sent"
    else
        local err
        err=$(echo "$resp" | jq -r '.error // "unknown"' 2>/dev/null)
        echo "[WARN] Slack fallback API error: $err"
    fi
}

if [[ ${#failures[@]} -gt 0 ]]; then
    send_slack_alert
    exit 1
fi

exit 0
