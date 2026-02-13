#!/bin/bash
# Unit tests for pull-soul-entrypoint.sh behaviour
# Tests variable defaults and script structure without running the full entrypoint
set -euo pipefail

PASS=0
FAIL=0
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENTRYPOINT="$SCRIPT_DIR/../../deploy/dutybound/pull-soul-entrypoint.sh"

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

echo "=== DutyBound Entrypoint Unit Tests ==="

# --- Test 1: Entrypoint exists and is executable ---
echo "[test] Entrypoint file"
if [ -f "$ENTRYPOINT" ]; then
    pass "entrypoint exists"
else
    fail "entrypoint not found at $ENTRYPOINT"
    echo "=== Results: $PASS passed, $FAIL failed ==="
    exit 1
fi

if [ -x "$ENTRYPOINT" ]; then
    pass "entrypoint is executable"
else
    fail "entrypoint is not executable"
fi

# --- Test 2: Default env vars ---
echo "[test] Default environment variables"
if grep -q 'FORGE_URL.*http://warren_promptforge:8083' "$ENTRYPOINT"; then
    pass "default FORGE_URL is warren_promptforge:8083"
else
    fail "default FORGE_URL not set correctly"
fi

if grep -q 'SOUL_SLUG.*dutybound' "$ENTRYPOINT"; then
    pass "default SOUL_SLUG is dutybound"
else
    fail "default SOUL_SLUG not set to dutybound"
fi

if grep -q 'AGENT_ID.*dutybound' "$ENTRYPOINT"; then
    pass "default AGENT_ID is dutybound"
else
    fail "default AGENT_ID not set to dutybound"
fi

# --- Test 3: Uses wget (alpine-compatible, not curl) ---
echo "[test] Alpine compatibility"
if grep -q 'wget' "$ENTRYPOINT"; then
    pass "uses wget for HTTP requests (alpine-compatible)"
else
    fail "does not use wget — may fail on alpine-based images"
fi

# --- Test 4: Writes SOUL.md to correct path ---
echo "[test] SOUL.md output path"
if grep -q '/home/node/.openclaw/workspace/SOUL.md' "$ENTRYPOINT"; then
    pass "writes SOUL.md to /home/node/.openclaw/workspace/"
else
    fail "SOUL.md path incorrect"
fi

# --- Test 5: Graceful fallback when Forge unreachable ---
echo "[test] Forge unreachable handling"
if grep -q 'PromptForge unreachable' "$ENTRYPOINT"; then
    pass "handles Forge unreachable with fallback message"
else
    fail "no fallback handling for Forge unavailability"
fi

# --- Test 6: Starts gateway via exec ---
echo "[test] Gateway exec"
if grep -q 'exec node /app/openclaw.mjs gateway' "$ENTRYPOINT"; then
    pass "starts gateway with exec (replaces shell process)"
else
    fail "gateway not started with exec"
fi

# --- Test 7: NATS watcher setup ---
echo "[test] Hermes NATS watcher"
if grep -q 'hermes-watcher' "$ENTRYPOINT"; then
    pass "includes NATS watcher for Hermes events"
else
    fail "no NATS watcher configured"
fi

if grep -q 'swarm\.>' "$ENTRYPOINT"; then
    pass "subscribes to swarm.> NATS subject"
else
    fail "NATS subscription pattern missing"
fi

# --- Test 8: Uses set -e for error handling ---
echo "[test] Error handling"
if head -3 "$ENTRYPOINT" | grep -q 'set -e'; then
    pass "script uses set -e for error handling"
else
    fail "script missing set -e"
fi

# --- Summary ---
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
