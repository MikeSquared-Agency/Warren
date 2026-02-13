#!/bin/bash
# E2E test for DutyBound developer agent
# Requires: Docker Swarm active, warren stack deployed, PromptForge running
# Usage: ./test_e2e.sh
set -euo pipefail

PASS=0
FAIL=0
SERVICE="warren_dutybound-mc"
PORT=18793
GATEWAY_TOKEN="dutybound-gateway-868f36e0"
FORGE_URL="http://192.168.1.218:8083"
MAX_WAIT=60

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
cleanup() {
    echo "[cleanup] Scaling down $SERVICE..."
    sudo docker service scale "$SERVICE=0" --detach 2>/dev/null || true
}

echo "=== DutyBound E2E Tests ==="
echo "[info] These tests scale up the dutybound-mc service and verify boot behaviour."
echo ""

# --- Prerequisite: PromptForge reachable ---
echo "[prereq] Checking PromptForge..."
if curl -sf --max-time 5 "$FORGE_URL/api/v1/prompts/dutybound/versions/latest" > /dev/null 2>&1; then
    pass "PromptForge reachable, dutybound soul exists"
else
    fail "PromptForge unreachable or dutybound soul missing"
    echo "=== Skipping E2E tests — prerequisites not met ==="
    exit 1
fi

# --- Prerequisite: Service exists in stack ---
echo "[prereq] Checking service exists..."
if sudo docker service inspect "$SERVICE" > /dev/null 2>&1; then
    pass "Service $SERVICE exists in stack"
else
    fail "Service $SERVICE not found — deploy stack first"
    echo "=== Skipping E2E tests — prerequisites not met ==="
    exit 1
fi

# --- Test 1: Scale up ---
echo "[test] Scale up service"
trap cleanup EXIT
sudo docker service scale "$SERVICE=1" --detach 2>/dev/null

# Wait for container to be running
ELAPSED=0
while [ "$ELAPSED" -lt "$MAX_WAIT" ]; do
    REPLICAS=$(sudo docker service ls --filter "name=$SERVICE" --format "{{.Replicas}}" 2>/dev/null)
    if [ "$REPLICAS" = "1/1" ]; then
        break
    fi
    sleep 2
    ELAPSED=$((ELAPSED + 2))
done

if [ "$REPLICAS" = "1/1" ]; then
    pass "service scaled to 1/1 within ${ELAPSED}s"
else
    fail "service did not reach 1/1 (got: $REPLICAS after ${MAX_WAIT}s)"
    echo "=== Aborting — container failed to start ==="
    sudo docker service ps "$SERVICE" --no-trunc 2>&1 | tail -5
    exit 1
fi

# --- Test 2: Soul pulled ---
echo "[test] Soul pulled from PromptForge"
sleep 5  # Give gateway time to start
LOGS=$(sudo docker service logs "$SERVICE" --tail 50 2>&1)

if echo "$LOGS" | grep -q "Soul written to SOUL.md"; then
    pass "soul pulled and written to SOUL.md"
else
    fail "soul pull not confirmed in logs"
fi

# --- Test 3: SOUL.md content ---
echo "[test] SOUL.md content"
CONTAINER_ID=$(sudo docker ps --filter "name=${SERVICE}" --format "{{.ID}}" | head -1)
if [ -n "$CONTAINER_ID" ]; then
    SOUL_CONTENT=$(sudo docker exec "$CONTAINER_ID" cat /home/node/.openclaw/workspace/SOUL.md 2>/dev/null || echo "")
    if echo "$SOUL_CONTENT" | grep -q "DutyBound"; then
        pass "SOUL.md contains DutyBound identity"
    else
        fail "SOUL.md missing DutyBound identity"
    fi

    if echo "$SOUL_CONTENT" | grep -q "## Identity"; then
        pass "SOUL.md has Identity section"
    else
        fail "SOUL.md missing Identity section"
    fi

    if echo "$SOUL_CONTENT" | grep -q "## Workflow"; then
        pass "SOUL.md has Workflow section"
    else
        fail "SOUL.md missing Workflow section"
    fi
else
    fail "could not find running container"
fi

# --- Test 4: Gateway responding ---
echo "[test] Gateway health"
# Wait for gateway to be ready
GATEWAY_READY=false
for i in $(seq 1 10); do
    if curl -4 -sf --max-time 3 "http://127.0.0.1:$PORT/" -H "Authorization: Bearer $GATEWAY_TOKEN" > /dev/null 2>&1; then
        GATEWAY_READY=true
        break
    fi
    sleep 2
done

if [ "$GATEWAY_READY" = "true" ]; then
    pass "gateway responding on port $PORT"
else
    fail "gateway not responding on port $PORT"
fi

# --- Test 5: Gateway listening on correct port ---
echo "[test] Gateway port"
# Refresh logs after gateway startup
LOGS=$(sudo docker service logs "$SERVICE" --tail 100 2>&1)
if echo "$LOGS" | grep -q "listening.*$PORT"; then
    pass "gateway listen log confirms port $PORT"
elif [ "$GATEWAY_READY" = "true" ]; then
    pass "gateway confirmed responding on port $PORT (log line not captured)"
else
    fail "gateway not listening on expected port"
fi

# --- Test 6: NATS watcher started ---
echo "[test] NATS watcher"
if echo "$LOGS" | grep -q "hermes-watcher.*Starting NATS watcher"; then
    pass "NATS watcher started"
else
    fail "NATS watcher not started"
fi

# --- Test 7: Agent identity ---
echo "[test] Agent identity (not Kai)"
if [ -n "$CONTAINER_ID" ]; then
    ENV_AGENT_ID=$(sudo docker exec "$CONTAINER_ID" printenv AGENT_ID 2>/dev/null || echo "")
    if [ "$ENV_AGENT_ID" = "dutybound" ]; then
        pass "AGENT_ID is 'dutybound' (not kai)"
    else
        fail "AGENT_ID is '$ENV_AGENT_ID', expected 'dutybound'"
    fi

    ENV_MODEL=$(sudo docker exec "$CONTAINER_ID" printenv OPENCLAW_MODEL 2>/dev/null || echo "")
    if echo "$ENV_MODEL" | grep -q "sonnet"; then
        pass "model is sonnet (code-optimised)"
    else
        fail "model is '$ENV_MODEL', expected sonnet"
    fi
fi

# --- Summary ---
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
# cleanup runs via trap
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
