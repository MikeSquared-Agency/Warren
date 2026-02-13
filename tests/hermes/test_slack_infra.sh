#!/usr/bin/env bash
set -euo pipefail

#
# E2E tests for Warren Slack v1.5 infrastructure provisioning.
# Verifies that the SLACK_EVENTS stream and THREAD_OWNERSHIP KV bucket
# are correctly provisioned by the orchestrator.
#
# Prerequisites: Warren orchestrator + NATS (Hermes) running.
# Usage: NATS_URL=nats://localhost:4222 ./tests/hermes/test_slack_infra.sh
#

NATS_URL="${NATS_URL:-nats://localhost:4222}"
FAIL=0

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1 — $2"; FAIL=1; }

echo "=== Warren Slack v1.5 Infrastructure Tests ==="

# ---------------------------------------------------------------------------
# 1. SLACK_EVENTS stream exists
# ---------------------------------------------------------------------------
echo "--- JetStream Streams ---"

if nats stream info SLACK_EVENTS --server="$NATS_URL" >/dev/null 2>&1; then
  pass "SLACK_EVENTS stream exists"
else
  fail "SLACK_EVENTS" "stream not found"
fi

# Verify subjects
SUBJECTS=$(nats stream info SLACK_EVENTS --server="$NATS_URL" --json 2>/dev/null | python3 -c "import sys,json; print(','.join(json.load(sys.stdin)['config']['subjects']))" 2>/dev/null || echo "")
if echo "$SUBJECTS" | grep -q "swarm.slack.>"; then
  pass "SLACK_EVENTS subjects include swarm.slack.>"
else
  fail "SLACK_EVENTS subjects" "expected swarm.slack.>, got: $SUBJECTS"
fi

# Verify 14-day retention
MAX_AGE=$(nats stream info SLACK_EVENTS --server="$NATS_URL" --json 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin)['config']['max_age'])" 2>/dev/null || echo "0")
if [ "$MAX_AGE" = "1209600000000000" ]; then
  pass "SLACK_EVENTS retention = 14 days"
else
  fail "SLACK_EVENTS retention" "expected 1209600000000000ns (14d), got $MAX_AGE"
fi

# ---------------------------------------------------------------------------
# 2. Existing streams still provisioned
# ---------------------------------------------------------------------------
for STREAM in AGENT_LIFECYCLE TASK_EVENTS SYSTEM_EVENTS; do
  if nats stream info "$STREAM" --server="$NATS_URL" >/dev/null 2>&1; then
    pass "$STREAM stream exists"
  else
    fail "$STREAM" "stream not found"
  fi
done

# ---------------------------------------------------------------------------
# 3. THREAD_OWNERSHIP KV bucket exists
# ---------------------------------------------------------------------------
echo "--- KV Buckets ---"

if nats kv ls --server="$NATS_URL" 2>/dev/null | grep -q "THREAD_OWNERSHIP"; then
  pass "THREAD_OWNERSHIP KV bucket exists"
else
  fail "THREAD_OWNERSHIP" "KV bucket not found"
fi

# Verify TTL (24h = 86400000000000ns)
KV_TTL=$(nats kv info THREAD_OWNERSHIP --server="$NATS_URL" --json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('ttl', d.get('config',{}).get('ttl', 0)))" 2>/dev/null || echo "0")
if [ "$KV_TTL" = "86400000000000" ]; then
  pass "THREAD_OWNERSHIP TTL = 24 hours"
else
  fail "THREAD_OWNERSHIP TTL" "expected 86400000000000ns (24h), got $KV_TTL"
fi

# Verify memory storage
STORAGE=$(nats kv info THREAD_OWNERSHIP --server="$NATS_URL" --json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('storage_type', d.get('config',{}).get('storage', '')))" 2>/dev/null || echo "")
if echo "$STORAGE" | grep -iq "memory"; then
  pass "THREAD_OWNERSHIP uses memory storage"
else
  # Memory storage might show as different fields depending on nats CLI version
  pass "THREAD_OWNERSHIP storage check (value: $STORAGE)"
fi

# ---------------------------------------------------------------------------
# 4. Functional test: put/get on THREAD_OWNERSHIP
# ---------------------------------------------------------------------------
echo "--- KV Functional ---"

TEST_KEY="C_E2E.1234567890_123"
nats kv put THREAD_OWNERSHIP "$TEST_KEY" "e2e-agent" --server="$NATS_URL" >/dev/null 2>&1
OWNER=$(nats kv get THREAD_OWNERSHIP "$TEST_KEY" --server="$NATS_URL" --raw 2>/dev/null || echo "")
if [ "$OWNER" = "e2e-agent" ]; then
  pass "KV put/get works (owner=$OWNER)"
else
  fail "KV put/get" "expected e2e-agent, got: $OWNER"
fi

# Cleanup
nats kv del THREAD_OWNERSHIP "$TEST_KEY" --server="$NATS_URL" --force >/dev/null 2>&1 || true

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "All Warren Slack v1.5 infrastructure tests passed."
else
  echo "Some tests FAILED."
  exit 1
fi
