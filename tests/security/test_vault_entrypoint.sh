#!/usr/bin/env bash
# Unit tests for vault-entrypoint.sh secret-fetching behaviour
# Tests env validation, timeout, secret parsing, and exec passthrough
set -euo pipefail

PASS=0
FAIL=0
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENTRYPOINT="$SCRIPT_DIR/../../deploy/vault-entrypoint.sh"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"; kill_mock_server 2>/dev/null' EXIT

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# --- Mock HTTP Server Helpers ---

MOCK_PID=""

kill_mock_server() {
  if [ -n "$MOCK_PID" ] && kill -0 "$MOCK_PID" 2>/dev/null; then
    kill "$MOCK_PID" 2>/dev/null || true
    wait "$MOCK_PID" 2>/dev/null || true
  fi
  MOCK_PID=""
}

# Start a Python mock server that responds to Alexandria API routes.
# Args: $1=port, $2=secrets_json_file (maps secret_name -> value)
start_mock_server() {
  local port="$1"
  local secrets_file="$2"

  cat > "$TMPDIR/mock_server.py" <<'PYEOF'
import http.server
import json
import sys
import os

PORT = int(sys.argv[1])
SECRETS_FILE = sys.argv[2]

with open(SECRETS_FILE) as f:
    SECRETS = json.load(f)

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass  # suppress logs

    def do_GET(self):
        # Health/readiness check: GET /api/v1/secrets
        if self.path == "/api/v1/secrets":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"status":"ok"}')
            return

        # Secret fetch: GET /api/v1/secrets/<name>
        if self.path.startswith("/api/v1/secrets/"):
            secret_name = self.path.split("/api/v1/secrets/")[1]
            agent_id = self.headers.get("X-Agent-ID", "")
            if secret_name in SECRETS:
                resp = json.dumps({"name": secret_name, "value": SECRETS[secret_name], "agent": agent_id})
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(resp.encode())
            else:
                self.send_response(404)
                self.end_headers()
                self.wfile.write(b'{"error":"not found"}')
            return

        self.send_response(404)
        self.end_headers()

httpd = http.server.HTTPServer(("127.0.0.1", PORT), Handler)
httpd.serve_forever()
PYEOF

  python3 "$TMPDIR/mock_server.py" "$port" "$secrets_file" &
  MOCK_PID=$!

  # Wait for mock server to be ready (up to 5 seconds)
  local tries=0
  while ! curl -sf -o /dev/null "http://127.0.0.1:${port}/api/v1/secrets" 2>/dev/null; do
    tries=$((tries + 1))
    if [ "$tries" -ge 50 ]; then
      echo "  ERROR: Mock server failed to start on port $port"
      return 1
    fi
    sleep 0.1
  done
}

# Pick a random free port
pick_port() {
  python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()"
}

echo "=== Vault Entrypoint Unit Tests ==="

# =========================================================================
# Test 0: Entrypoint exists and is executable
# =========================================================================
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

# =========================================================================
# Test 1: Missing VAULT_AGENT_ID exits with error
# =========================================================================
echo "[test] Missing VAULT_AGENT_ID"
OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" VAULT_SECRETS="foo:BAR" \
  sh "$ENTRYPOINT" echo hello 2>&1 || true)

if echo "$OUTPUT" | grep -q "VAULT_AGENT_ID and VAULT_SECRETS must be set"; then
  pass "missing VAULT_AGENT_ID produces correct error message"
else
  fail "missing VAULT_AGENT_ID did not produce expected error"
fi

EXIT_CODE=0
env -i PATH="$PATH" HOME="$HOME" VAULT_SECRETS="foo:BAR" \
  sh "$ENTRYPOINT" echo hello >/dev/null 2>&1 || EXIT_CODE=$?

if [ "$EXIT_CODE" -ne 0 ]; then
  pass "missing VAULT_AGENT_ID exits non-zero (exit=$EXIT_CODE)"
else
  fail "missing VAULT_AGENT_ID should exit non-zero"
fi

# =========================================================================
# Test 2: Missing VAULT_SECRETS exits with error
# =========================================================================
echo "[test] Missing VAULT_SECRETS"
OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" VAULT_AGENT_ID="test-agent" \
  sh "$ENTRYPOINT" echo hello 2>&1 || true)

if echo "$OUTPUT" | grep -q "VAULT_AGENT_ID and VAULT_SECRETS must be set"; then
  pass "missing VAULT_SECRETS produces correct error message"
else
  fail "missing VAULT_SECRETS did not produce expected error"
fi

EXIT_CODE=0
env -i PATH="$PATH" HOME="$HOME" VAULT_AGENT_ID="test-agent" \
  sh "$ENTRYPOINT" echo hello >/dev/null 2>&1 || EXIT_CODE=$?

if [ "$EXIT_CODE" -ne 0 ]; then
  pass "missing VAULT_SECRETS exits non-zero (exit=$EXIT_CODE)"
else
  fail "missing VAULT_SECRETS should exit non-zero"
fi

# =========================================================================
# Test 3: No command args exits with error
# =========================================================================
echo "[test] No command args"
OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" VAULT_SECRETS="foo:BAR" \
  sh "$ENTRYPOINT" 2>&1 || true)

if echo "$OUTPUT" | grep -q "No command specified"; then
  pass "no command args produces correct error message"
else
  fail "no command args did not produce expected error"
fi

EXIT_CODE=0
env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" VAULT_SECRETS="foo:BAR" \
  sh "$ENTRYPOINT" >/dev/null 2>&1 || EXIT_CODE=$?

if [ "$EXIT_CODE" -ne 0 ]; then
  pass "no command args exits non-zero (exit=$EXIT_CODE)"
else
  fail "no command args should exit non-zero"
fi

# =========================================================================
# Test 4: Retry-forever when Alexandria is unreachable
# =========================================================================
echo "[test] Retry-forever when Alexandria unreachable"
# The entrypoint should NOT exit when Alexandria is down — it retries
# indefinitely. We run it in the background for a few seconds, verify it's
# still alive and logging "Still waiting" messages, then kill it.

RETRY_LOG="$TMPDIR/retry_output.log"

env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="foo:BAR" \
  VAULT_URL="http://127.0.0.1:19999" \
  VAULT_TIMEOUT="60" \
  sh "$ENTRYPOINT" echo hello >"$RETRY_LOG" 2>&1 &
RETRY_PID=$!

# Let it run for 8 seconds (enough for 4 loop iterations at sleep 2)
sleep 8

if kill -0 "$RETRY_PID" 2>/dev/null; then
  pass "entrypoint still running after 8s (no premature exit)"
  kill "$RETRY_PID" 2>/dev/null || true
  wait "$RETRY_PID" 2>/dev/null || true
else
  wait "$RETRY_PID" 2>/dev/null || true
  fail "entrypoint exited prematurely (should retry forever)"
fi

RETRY_OUTPUT=$(cat "$RETRY_LOG")

if echo "$RETRY_OUTPUT" | grep -q "Waiting for Alexandria"; then
  pass "initial waiting message logged"
else
  fail "initial waiting message not logged"
fi

# Verify no fatal error/exit message
if echo "$RETRY_OUTPUT" | grep -q "ERROR.*not reachable"; then
  fail "should not produce fatal timeout error"
else
  pass "no fatal timeout error produced"
fi

# =========================================================================
# Test 4b: "Still waiting" progress message appears at 30s intervals
# =========================================================================
echo "[test] Still-waiting progress logging"
# We can't wait 30s in a unit test, so test the logic by checking the
# script source for the modulo-30 condition.
if grep -q 'elapsed % 30' "$ENTRYPOINT"; then
  pass "entrypoint logs progress every 30s (modulo check present)"
else
  fail "entrypoint missing 30s progress logging"
fi

# Verify the log message format
if grep -q 'Still waiting for Alexandria' "$ENTRYPOINT"; then
  pass "progress message uses expected format"
else
  fail "progress message format unexpected"
fi

# =========================================================================
# Test 4c: Entrypoint connects once Alexandria appears (delayed start)
# =========================================================================
echo "[test] Delayed Alexandria start — entrypoint eventually connects"
MOCK_PORT=$(pick_port)

cat > "$TMPDIR/delayed_secret.json" <<'EOF'
{
  "dummy_key": "delayed_value"
}
EOF

DELAYED_LOG="$TMPDIR/delayed_output.log"

# Start entrypoint BEFORE mock server — it should wait
env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="dummy_key:DELAYED_VAR" \
  VAULT_URL="http://127.0.0.1:${MOCK_PORT}" \
  VAULT_TIMEOUT="60" \
  sh "$ENTRYPOINT" echo "DELAYED_SUCCESS" >"$DELAYED_LOG" 2>&1 &
DELAYED_PID=$!

# Wait 4s then start mock server
sleep 4
start_mock_server "$MOCK_PORT" "$TMPDIR/delayed_secret.json"

# Give entrypoint time to detect the server and fetch secrets
WAIT_TRIES=0
while kill -0 "$DELAYED_PID" 2>/dev/null && [ "$WAIT_TRIES" -lt 15 ]; do
  WAIT_TRIES=$((WAIT_TRIES + 1))
  sleep 1
done

DELAYED_OUTPUT=$(cat "$DELAYED_LOG")
kill_mock_server

if echo "$DELAYED_OUTPUT" | grep -q "Alexandria is up"; then
  pass "entrypoint detected delayed Alexandria start"
else
  fail "entrypoint did not detect delayed Alexandria"
fi

if echo "$DELAYED_OUTPUT" | grep -q "Loaded dummy_key -> DELAYED_VAR"; then
  pass "secret fetched after delayed start"
else
  fail "secret not fetched after delayed start"
fi

if echo "$DELAYED_OUTPUT" | grep -q "DELAYED_SUCCESS"; then
  pass "command executed after delayed start"
else
  fail "command not executed after delayed start"
fi

# Clean up if still running
if kill -0 "$DELAYED_PID" 2>/dev/null; then
  kill "$DELAYED_PID" 2>/dev/null || true
  wait "$DELAYED_PID" 2>/dev/null || true
fi

# =========================================================================
# Test 5: Successful secret parsing from mock JSON response
# =========================================================================
echo "[test] Single secret parsing via mock server"
MOCK_PORT=$(pick_port)

cat > "$TMPDIR/secrets.json" <<'EOF'
{
  "supabase_db_url": "postgresql://user:pass@db:5432/mydb"
}
EOF

start_mock_server "$MOCK_PORT" "$TMPDIR/secrets.json"

# Use a wrapper script that exports the fetched secret then prints it,
# instead of exec-ing the final command (so we can capture the env var).
cat > "$TMPDIR/check_env.sh" <<'ENVEOF'
#!/bin/sh
echo "DATABASE_URL=$DATABASE_URL"
ENVEOF
chmod +x "$TMPDIR/check_env.sh"

OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="supabase_db_url:DATABASE_URL" \
  VAULT_URL="http://127.0.0.1:${MOCK_PORT}" \
  VAULT_TIMEOUT="5" \
  sh "$ENTRYPOINT" sh "$TMPDIR/check_env.sh" 2>&1)

kill_mock_server

if echo "$OUTPUT" | grep -q "Loaded supabase_db_url -> DATABASE_URL"; then
  pass "secret load message logged"
else
  fail "secret load message not found in output"
fi

if echo "$OUTPUT" | grep -q "DATABASE_URL=postgresql://user:pass@db:5432/mydb"; then
  pass "secret value correctly parsed and exported"
else
  fail "secret value not correctly exported (output: $OUTPUT)"
fi

# =========================================================================
# Test 5b: Sed regex parses various JSON formats
# =========================================================================
echo "[test] Sed regex handles whitespace variants"

# The sed pattern used: sed -n 's/.*"value"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
# Test it directly against different JSON formats

REGEX='s/.*"value"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'

RESULT=$(echo '{"name":"test","value":"myval123"}' | sed -n "$REGEX")
if [ "$RESULT" = "myval123" ]; then
  pass "sed regex: compact JSON (no spaces)"
else
  fail "sed regex: compact JSON failed (got: $RESULT)"
fi

RESULT=$(echo '{"name": "test", "value": "myval456"}' | sed -n "$REGEX")
if [ "$RESULT" = "myval456" ]; then
  pass "sed regex: standard JSON (single spaces)"
else
  fail "sed regex: standard JSON failed (got: $RESULT)"
fi

RESULT=$(echo '{"name":"test",  "value" :  "myval789"}' | sed -n "$REGEX")
if [ "$RESULT" = "myval789" ]; then
  pass "sed regex: extra whitespace around colon"
else
  fail "sed regex: extra whitespace failed (got: $RESULT)"
fi

RESULT=$(echo '{"value":"s3cr3t_k3y!@#$%^&*()"}' | sed -n "$REGEX")
if [ "$RESULT" = 's3cr3t_k3y!@#$%^&*()' ]; then
  pass "sed regex: special characters in value"
else
  fail "sed regex: special characters failed (got: $RESULT)"
fi

# =========================================================================
# Test 6: Multiple secret mappings are all parsed correctly
# =========================================================================
echo "[test] Multiple secret mappings via mock server"
MOCK_PORT=$(pick_port)

cat > "$TMPDIR/multi_secrets.json" <<'EOF'
{
  "supabase_db_url": "postgresql://user:pass@db:5432/mydb",
  "slack_app_token": "xapp-1-AAAAAA-1234567890-abcdef",
  "openai_key": "sk-proj-abc123xyz"
}
EOF

start_mock_server "$MOCK_PORT" "$TMPDIR/multi_secrets.json"

cat > "$TMPDIR/check_multi_env.sh" <<'ENVEOF'
#!/bin/sh
echo "DATABASE_URL=$DATABASE_URL"
echo "SLACK_APP_TOKEN=$SLACK_APP_TOKEN"
echo "OPENAI_API_KEY=$OPENAI_API_KEY"
ENVEOF
chmod +x "$TMPDIR/check_multi_env.sh"

OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="supabase_db_url:DATABASE_URL,slack_app_token:SLACK_APP_TOKEN,openai_key:OPENAI_API_KEY" \
  VAULT_URL="http://127.0.0.1:${MOCK_PORT}" \
  VAULT_TIMEOUT="5" \
  sh "$ENTRYPOINT" sh "$TMPDIR/check_multi_env.sh" 2>&1)

kill_mock_server

if echo "$OUTPUT" | grep -q "Loaded supabase_db_url -> DATABASE_URL"; then
  pass "first secret (supabase_db_url) loaded"
else
  fail "first secret load message missing"
fi

if echo "$OUTPUT" | grep -q "Loaded slack_app_token -> SLACK_APP_TOKEN"; then
  pass "second secret (slack_app_token) loaded"
else
  fail "second secret load message missing"
fi

if echo "$OUTPUT" | grep -q "Loaded openai_key -> OPENAI_API_KEY"; then
  pass "third secret (openai_key) loaded"
else
  fail "third secret load message missing"
fi

if echo "$OUTPUT" | grep -q "DATABASE_URL=postgresql://user:pass@db:5432/mydb"; then
  pass "DATABASE_URL value correct"
else
  fail "DATABASE_URL value incorrect"
fi

if echo "$OUTPUT" | grep -q "SLACK_APP_TOKEN=xapp-1-AAAAAA-1234567890-abcdef"; then
  pass "SLACK_APP_TOKEN value correct"
else
  fail "SLACK_APP_TOKEN value incorrect"
fi

if echo "$OUTPUT" | grep -q "OPENAI_API_KEY=sk-proj-abc123xyz"; then
  pass "OPENAI_API_KEY value correct"
else
  fail "OPENAI_API_KEY value incorrect"
fi

if echo "$OUTPUT" | grep -q "All secrets loaded"; then
  pass "all-secrets-loaded message present"
else
  fail "all-secrets-loaded message missing"
fi

# =========================================================================
# Test 7: exec "$@" passes the right command
# =========================================================================
echo "[test] exec passes correct command and arguments"
MOCK_PORT=$(pick_port)

cat > "$TMPDIR/single_secret.json" <<'EOF'
{
  "dummy_key": "dummy_value"
}
EOF

start_mock_server "$MOCK_PORT" "$TMPDIR/single_secret.json"

# Test that exec "$@" passes all arguments correctly, including ones with spaces
cat > "$TMPDIR/arg_checker.sh" <<'ENVEOF'
#!/bin/sh
echo "ARG_COUNT=$#"
i=1
for arg in "$@"; do
  echo "ARG_${i}=${arg}"
  i=$((i + 1))
done
ENVEOF
chmod +x "$TMPDIR/arg_checker.sh"

OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="dummy_key:DUMMY_VAR" \
  VAULT_URL="http://127.0.0.1:${MOCK_PORT}" \
  VAULT_TIMEOUT="5" \
  sh "$ENTRYPOINT" sh "$TMPDIR/arg_checker.sh" --flag1 value1 --flag2 2>&1)

kill_mock_server

if echo "$OUTPUT" | grep -q "starting:.*arg_checker.sh --flag1 value1 --flag2"; then
  pass "exec log message shows correct command and args"
else
  fail "exec log message does not show expected command"
fi

if echo "$OUTPUT" | grep -q "ARG_COUNT=3"; then
  pass "exec passed correct number of arguments (3)"
else
  fail "exec did not pass correct argument count"
fi

if echo "$OUTPUT" | grep -q "ARG_1=--flag1"; then
  pass "first argument passed correctly (--flag1)"
else
  fail "first argument incorrect"
fi

if echo "$OUTPUT" | grep -q "ARG_2=value1"; then
  pass "second argument passed correctly (value1)"
else
  fail "second argument incorrect"
fi

if echo "$OUTPUT" | grep -q "ARG_3=--flag2"; then
  pass "third argument passed correctly (--flag2)"
else
  fail "third argument incorrect"
fi

# =========================================================================
# Test 7b: exec replaces shell process (PID check)
# =========================================================================
echo "[test] exec replaces shell process"
# The entrypoint uses 'exec' which replaces the shell process. We verify
# this by checking that the script source contains 'exec "$@"'.
if grep -q 'exec "\$@"' "$ENTRYPOINT"; then
  pass "entrypoint uses exec to replace shell process"
else
  fail "entrypoint does not use exec for command handoff"
fi

# =========================================================================
# Test 8: Failed secret fetch exits with error
# =========================================================================
echo "[test] Failed secret fetch (missing secret)"
MOCK_PORT=$(pick_port)

cat > "$TMPDIR/partial_secrets.json" <<'EOF'
{
  "existing_key": "some_value"
}
EOF

start_mock_server "$MOCK_PORT" "$TMPDIR/partial_secrets.json"

EXIT_CODE=0
OUTPUT=$(env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="nonexistent_key:MY_VAR" \
  VAULT_URL="http://127.0.0.1:${MOCK_PORT}" \
  VAULT_TIMEOUT="5" \
  sh "$ENTRYPOINT" echo hello 2>&1 || true)

# Capture exit code separately
env -i PATH="$PATH" HOME="$HOME" \
  VAULT_AGENT_ID="test-agent" \
  VAULT_SECRETS="nonexistent_key:MY_VAR" \
  VAULT_URL="http://127.0.0.1:${MOCK_PORT}" \
  VAULT_TIMEOUT="5" \
  sh "$ENTRYPOINT" echo hello >/dev/null 2>&1 || EXIT_CODE=$?

kill_mock_server

if echo "$OUTPUT" | grep -q "Failed to fetch secret 'nonexistent_key'"; then
  pass "missing secret produces correct error message"
else
  fail "missing secret did not produce expected error"
fi

if [ "$EXIT_CODE" -ne 0 ]; then
  pass "missing secret exits non-zero (exit=$EXIT_CODE)"
else
  fail "missing secret should exit non-zero"
fi

# =========================================================================
# Test 9: Default VAULT_URL and VAULT_TIMEOUT values in script
# =========================================================================
echo "[test] Default variable values"
if grep -q 'VAULT_URL.*http://warren_alexandria:8500' "$ENTRYPOINT"; then
  pass "default VAULT_URL is http://warren_alexandria:8500"
else
  fail "default VAULT_URL not set correctly"
fi

if grep -q 'VAULT_TIMEOUT.*60' "$ENTRYPOINT"; then
  pass "default VAULT_TIMEOUT is 60"
else
  fail "default VAULT_TIMEOUT not set to 60"
fi

# =========================================================================
# Test 10: Script uses set -e for error handling
# =========================================================================
echo "[test] Error handling"
if head -3 "$ENTRYPOINT" | grep -q 'set -e'; then
  pass "script uses set -e for error handling"
else
  fail "script missing set -e"
fi

# =========================================================================
# Summary
# =========================================================================
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
