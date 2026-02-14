#!/usr/bin/env bash
set -euo pipefail

FAIL=0
pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1 — $2"; FAIL=1; }

echo "=== Warren Stack Security E2E Tests ==="

# ---------------------------------------------------------------------------
# 1. NATS client port not reachable from host
# ---------------------------------------------------------------------------
echo "--- Port Exposure ---"
if curl -s --connect-timeout 2 localhost:4222 >/dev/null 2>&1; then
  fail "NATS client port 4222" "port is reachable from host"
else
  pass "NATS client port 4222 not reachable from host"
fi

# ---------------------------------------------------------------------------
# 2. NATS HTTP monitoring port not reachable from host
# ---------------------------------------------------------------------------
if curl -s --connect-timeout 2 localhost:8222 >/dev/null 2>&1; then
  fail "NATS HTTP port 8222" "port is reachable from host"
else
  pass "NATS HTTP port 8222 not reachable from host"
fi

# ---------------------------------------------------------------------------
# 3. Dispatch port not exposed to host
# ---------------------------------------------------------------------------
if curl -s --connect-timeout 2 localhost:8600 >/dev/null 2>&1; then
  fail "Dispatch port 8600" "port is reachable from host"
else
  pass "Dispatch port 8600 not exposed to host"
fi

# ---------------------------------------------------------------------------
# 4. Promptforge port not exposed to host
# ---------------------------------------------------------------------------
if curl -s --connect-timeout 2 localhost:8083 >/dev/null 2>&1; then
  fail "Promptforge port 8083" "port is reachable from host"
else
  pass "Promptforge port 8083 not exposed to host"
fi

# ---------------------------------------------------------------------------
# 5. pg-proxy container removed
# ---------------------------------------------------------------------------
echo "--- Removed Services ---"
PG_PROXY_COUNT=$(docker ps --filter name=pg-proxy --format '{{.Names}}' 2>/dev/null | wc -l)
if [ "$PG_PROXY_COUNT" -eq 0 ]; then
  pass "pg-proxy container not running"
else
  fail "pg-proxy removed" "found $PG_PROXY_COUNT running pg-proxy container(s)"
fi

# ---------------------------------------------------------------------------
# 6. Alexandria vault has secrets seeded
# ---------------------------------------------------------------------------
echo "--- Alexandria Vault ---"
EXPECTED_SECRETS="supabase_url supabase_db_url supabase_service_role_key encryption_key gemini_api_key slack_app_token slack_bot_token"
VAULT_BODY=$(curl -sf --connect-timeout 5 localhost:8500/api/v1/secrets -H "X-Agent-ID: system" 2>/dev/null || true)

if [ -z "$VAULT_BODY" ]; then
  fail "Alexandria vault reachable" "could not reach localhost:8500/api/v1/secrets"
else
  ALL_PRESENT=true
  for SECRET in $EXPECTED_SECRETS; do
    if echo "$VAULT_BODY" | grep -q "$SECRET"; then
      pass "Alexandria vault contains $SECRET"
    else
      fail "Alexandria vault secret" "$SECRET not found in vault response"
      ALL_PRESENT=false
    fi
  done
  if [ "$ALL_PRESENT" = true ]; then
    pass "All expected secrets present in Alexandria vault"
  fi
fi

# ---------------------------------------------------------------------------
# 7. Service env inspection clean — no leaked credentials
# ---------------------------------------------------------------------------
echo "--- Service Env Inspection ---"
SERVICES_TO_CHECK="warren_dispatch warren_chronicle warren_slack-forwarder warren_promptforge"
SENSITIVE_PATTERNS="eyJ|postgres://.*:.*@|xoxb-|xapp-"

for SVC in $SERVICES_TO_CHECK; do
  SVC_SHORT="${SVC#warren_}"
  ENV_DUMP=$(docker service inspect "$SVC" --format '{{json .Spec.TaskTemplate.ContainerSpec.Env}}' 2>/dev/null || true)

  if [ -z "$ENV_DUMP" ] || [ "$ENV_DUMP" = "null" ]; then
    pass "$SVC_SHORT env vars: clean (none or null)"
    continue
  fi

  if echo "$ENV_DUMP" | grep -qE "$SENSITIVE_PATTERNS"; then
    fail "$SVC_SHORT env inspection" "found sensitive credential pattern in env vars"
  else
    pass "$SVC_SHORT env vars: no leaked credentials"
  fi
done

# ---------------------------------------------------------------------------
# 8. All active services healthy with expected replicas
# ---------------------------------------------------------------------------
echo "--- Service Health ---"
declare -A EXPECTED_REPLICAS=(
  [alexandria]=1
  [hermes]=1
  [dispatch]=1
  [chronicle]=1
  [promptforge]=1
  [slack-forwarder]=1
  [lily]=1
)

SERVICE_LS=$(docker service ls --format '{{.Name}} {{.Replicas}}' 2>/dev/null || true)

for SVC_NAME in "${!EXPECTED_REPLICAS[@]}"; do
  EXPECTED="${EXPECTED_REPLICAS[$SVC_NAME]}"
  REPLICA_INFO=$(echo "$SERVICE_LS" | grep "warren_${SVC_NAME}" | awk '{print $2}' || true)

  if [ -z "$REPLICA_INFO" ]; then
    fail "$SVC_NAME replicas" "service warren_${SVC_NAME} not found in docker service ls"
    continue
  fi

  CURRENT=$(echo "$REPLICA_INFO" | cut -d'/' -f1)
  DESIRED=$(echo "$REPLICA_INFO" | cut -d'/' -f2)

  if [ "$CURRENT" = "$EXPECTED" ] && [ "$DESIRED" = "$EXPECTED" ]; then
    pass "$SVC_NAME replicas: ${CURRENT}/${DESIRED}"
  else
    fail "$SVC_NAME replicas" "expected ${EXPECTED}/${EXPECTED}, got ${CURRENT}/${DESIRED}"
  fi
done

# ---------------------------------------------------------------------------
# 9. NATS requires auth
# ---------------------------------------------------------------------------
echo "--- NATS Auth ---"
HERMES_CMD=$(docker service inspect warren_hermes --format '{{json .Spec.TaskTemplate.ContainerSpec.Args}}' 2>/dev/null || true)
HERMES_CMD_FULL=$(docker service inspect warren_hermes --format '{{json .Spec.TaskTemplate.ContainerSpec}}' 2>/dev/null || true)

NATS_AUTH_FOUND=false
if echo "$HERMES_CMD" | grep -q -- '--auth\|--user\|--password\|authorization'; then
  NATS_AUTH_FOUND=true
fi
if echo "$HERMES_CMD_FULL" | grep -q -- '--auth\|--user\|--password\|authorization\|NATS_AUTH\|AUTH_TOKEN'; then
  NATS_AUTH_FOUND=true
fi

# Also try checking via exec into a NATS-connected container
HERMES_CONTAINER=$(docker ps --filter name=warren_hermes --format '{{.ID}}' 2>/dev/null | head -1 || true)
if [ -n "$HERMES_CONTAINER" ]; then
  # Attempt unauthenticated connection — should be rejected
  UNAUTH_RESULT=$(docker exec "$HERMES_CONTAINER" sh -c \
    'echo "PING" | timeout 2 nc localhost 4222 2>/dev/null || echo "CONNECTION_REFUSED"' 2>/dev/null || echo "EXEC_FAILED")
  if echo "$UNAUTH_RESULT" | grep -qiE 'authorization|err|refused|failed'; then
    NATS_AUTH_FOUND=true
    pass "NATS rejects unauthenticated connections from inside overlay"
  elif echo "$UNAUTH_RESULT" | grep -q 'PONG'; then
    fail "NATS auth" "NATS accepted unauthenticated PING from inside overlay"
  fi
fi

if [ "$NATS_AUTH_FOUND" = true ]; then
  pass "NATS auth configured (auth flag or env detected)"
else
  fail "NATS auth" "no auth flag or auth env found in hermes service spec"
fi

# ---------------------------------------------------------------------------
# 10. Alexandria uses Docker secrets
# ---------------------------------------------------------------------------
echo "--- Docker Secrets ---"
ALEX_SECRETS=$(docker service inspect warren_alexandria --format '{{json .Spec.TaskTemplate.ContainerSpec.Secrets}}' 2>/dev/null || true)

if [ -z "$ALEX_SECRETS" ] || [ "$ALEX_SECRETS" = "null" ]; then
  fail "Alexandria Docker secrets" "no secrets found in service spec"
else
  SECRET_COUNT=$(echo "$ALEX_SECRETS" | python3 -c "import sys,json; print(len(json.loads(sys.stdin.read())))" 2>/dev/null || echo "0")
  if [ "$SECRET_COUNT" -gt 0 ]; then
    pass "Alexandria uses Docker secrets ($SECRET_COUNT secret(s) mounted)"
  else
    fail "Alexandria Docker secrets" "secrets array is empty"
  fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "All Warren stack security tests passed."
else
  echo "Some Warren stack security tests FAILED."
  exit 1
fi
