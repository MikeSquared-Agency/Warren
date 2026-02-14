#!/usr/bin/env bash
# E2E test: vault-entrypoint resilience during Alexandria outages
#
# Validates that Chronicle and Dispatch survive Alexandria going down
# and reconnect automatically once it comes back.
#
# Prerequisites: Warren stack must be running (docker stack deploy warren ...)
# WARNING: This test temporarily scales Alexandria to 0 — run in staging only.
set -euo pipefail

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1 — $2"; }

echo "=== Vault Entrypoint Resilience E2E Tests ==="

# ---------------------------------------------------------------------------
# Pre-flight: Verify stack is running
# ---------------------------------------------------------------------------
echo "--- Pre-flight checks ---"

for SVC in warren_alexandria warren_chronicle warren_dispatch; do
  REPLICAS=$(docker service ls --filter "name=${SVC}" --format '{{.Replicas}}' 2>/dev/null || true)
  if [ -z "$REPLICAS" ]; then
    echo "  SKIP: Service $SVC not found — is the Warren stack deployed?"
    echo "=== Results: $PASS passed, $FAIL failed (skipped — stack not running) ==="
    exit 0
  fi
  CURRENT=$(echo "$REPLICAS" | cut -d'/' -f1)
  if [ "$CURRENT" -ge 1 ]; then
    pass "$SVC is running ($REPLICAS)"
  else
    fail "$SVC running" "got $REPLICAS"
  fi
done

# ---------------------------------------------------------------------------
# 1. Scale Alexandria to 0 — simulate outage
# ---------------------------------------------------------------------------
echo "--- Simulating Alexandria outage ---"
docker service scale warren_alexandria=0 --detach=false 2>/dev/null
sleep 3

ALEX_REPLICAS=$(docker service ls --filter "name=warren_alexandria" --format '{{.Replicas}}' 2>/dev/null)
if [ "$ALEX_REPLICAS" = "0/0" ]; then
  pass "Alexandria scaled to 0"
else
  fail "Alexandria scale-down" "expected 0/0, got $ALEX_REPLICAS"
fi

# ---------------------------------------------------------------------------
# 2. Force-restart Chronicle and Dispatch during the outage
# ---------------------------------------------------------------------------
echo "--- Restarting Chronicle + Dispatch during outage ---"
docker service update --force warren_chronicle --detach 2>/dev/null
docker service update --force warren_dispatch --detach 2>/dev/null
sleep 10

# ---------------------------------------------------------------------------
# 3. Verify Chronicle and Dispatch are alive (not crash-looping)
# ---------------------------------------------------------------------------
echo "--- Checking services survive outage ---"

for SVC in warren_chronicle warren_dispatch; do
  SVC_SHORT="${SVC#warren_}"

  # Check that the container exists and is running (not restarting)
  CONTAINER_ID=$(docker ps --filter "name=${SVC}" --format '{{.ID}}' 2>/dev/null | head -1 || true)
  if [ -n "$CONTAINER_ID" ]; then
    pass "$SVC_SHORT has a running container during outage"
  else
    fail "$SVC_SHORT container" "no running container found during Alexandria outage"
    continue
  fi

  # Check logs for "Still waiting" (retry-forever behavior)
  RECENT_LOGS=$(docker service logs --since 30s "$SVC" 2>&1 || true)
  if echo "$RECENT_LOGS" | grep -q "Still waiting for Alexandria\|Waiting for Alexandria"; then
    pass "$SVC_SHORT is waiting for Alexandria (not crashed)"
  else
    # May not have hit 30s yet — check it hasn't exited with error
    if echo "$RECENT_LOGS" | grep -q "ERROR.*not reachable"; then
      fail "$SVC_SHORT retry" "found fatal timeout error — old exit-on-timeout behavior"
    else
      pass "$SVC_SHORT appears to be retrying (no fatal error in logs)"
    fi
  fi
done

# ---------------------------------------------------------------------------
# 4. Bring Alexandria back
# ---------------------------------------------------------------------------
echo "--- Restoring Alexandria ---"
docker service scale warren_alexandria=1 --detach=false 2>/dev/null

# Wait for Alexandria to be healthy (up to 60s)
TRIES=0
while ! curl -sf -o /dev/null "http://127.0.0.1:8500/api/v1/secrets" 2>/dev/null; do
  TRIES=$((TRIES + 1))
  if [ "$TRIES" -ge 30 ]; then
    fail "Alexandria recovery" "not reachable after 60s"
    break
  fi
  sleep 2
done

if curl -sf -o /dev/null "http://127.0.0.1:8500/api/v1/secrets" 2>/dev/null; then
  pass "Alexandria is back and reachable"
fi

# ---------------------------------------------------------------------------
# 5. Verify Chronicle and Dispatch recover and start normally
# ---------------------------------------------------------------------------
echo "--- Checking service recovery ---"
# Give services time to detect Alexandria and fetch secrets
sleep 15

for SVC in warren_chronicle warren_dispatch; do
  SVC_SHORT="${SVC#warren_}"

  REPLICAS=$(docker service ls --filter "name=${SVC}" --format '{{.Replicas}}' 2>/dev/null)
  CURRENT=$(echo "$REPLICAS" | cut -d'/' -f1)
  DESIRED=$(echo "$REPLICAS" | cut -d'/' -f2)

  if [ "$CURRENT" = "$DESIRED" ] && [ "$CURRENT" -ge 1 ]; then
    pass "$SVC_SHORT recovered ($REPLICAS)"
  else
    fail "$SVC_SHORT recovery" "replicas: $REPLICAS"
  fi

  # Check logs for successful startup after recovery
  RECOVERY_LOGS=$(docker service logs --since 60s "$SVC" 2>&1 || true)
  if echo "$RECOVERY_LOGS" | grep -q "Alexandria is up"; then
    pass "$SVC_SHORT connected to Alexandria after recovery"
  else
    fail "$SVC_SHORT connection" "no 'Alexandria is up' message in recent logs"
  fi

  if echo "$RECOVERY_LOGS" | grep -q "All secrets loaded"; then
    pass "$SVC_SHORT fetched secrets after recovery"
  else
    fail "$SVC_SHORT secrets" "no 'All secrets loaded' message in recent logs"
  fi
done

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
