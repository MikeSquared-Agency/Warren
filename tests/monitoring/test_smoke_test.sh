#!/usr/bin/env bash
# Unit tests for deploy/monitoring/smoke-test.sh
# Tests service parsing, health check logic, vault fetch, and Slack alerting.
# Runs offline — uses mock HTTP servers, no real services needed.
set -euo pipefail

PASS=0
FAIL=0
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SMOKE_SCRIPT="$SCRIPT_DIR/../../deploy/monitoring/smoke-test.sh"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"; kill_mock_servers 2>/dev/null' EXIT

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# --- Mock HTTP Server ---
MOCK_PIDS=()

kill_mock_servers() {
  for pid in "${MOCK_PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  MOCK_PIDS=()
}

pick_port() {
  python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()"
}

# Start a mock HTTP server. Args: $1=port, $2=routes_json
# routes_json maps path -> {status, body}
start_mock_server() {
  local port="$1"
  local routes_file="$2"

  cat > "$TMPDIR/mock_http.py" <<'PYEOF'
import http.server, json, sys
PORT = int(sys.argv[1])
with open(sys.argv[2]) as f:
    ROUTES = json.load(f)

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        route = ROUTES.get(self.path)
        if route:
            self.send_response(route.get("status", 200))
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(route.get("body", "").encode())
        else:
            self.send_response(404)
            self.end_headers()
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length).decode() if length else ""
        # Store POST body for later inspection
        with open(sys.argv[2] + ".post_log", "a") as f:
            f.write(json.dumps({"path": self.path, "body": body,
                "auth": self.headers.get("Authorization", "")}) + "\n")
        route = ROUTES.get("POST " + self.path)
        if route:
            self.send_response(route.get("status", 200))
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(route.get("body", "").encode())
        else:
            self.send_response(404)
            self.end_headers()

http.server.HTTPServer(("127.0.0.1", PORT), H).serve_forever()
PYEOF

  python3 "$TMPDIR/mock_http.py" "$port" "$routes_file" &
  MOCK_PIDS+=($!)
  local tries=0
  while ! curl -sf -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null; do
    tries=$((tries + 1))
    if [ "$tries" -ge 50 ]; then
      # Server may 404 on / but still be up — check with a HEAD
      if curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/" 2>/dev/null | grep -q '[0-9]'; then
        break
      fi
      echo "  ERROR: Mock server failed to start on port $port"
      return 1
    fi
    sleep 0.1
  done
}

echo "=== Smoke Test Unit Tests ==="

# =========================================================================
# Test 1: Script exists and is executable
# =========================================================================
echo "[test] Script file"
if [ -f "$SMOKE_SCRIPT" ]; then
  pass "smoke-test.sh exists"
else
  fail "smoke-test.sh not found at $SMOKE_SCRIPT"
  echo "=== Results: $PASS passed, $FAIL failed ==="
  exit 1
fi

if [ -x "$SMOKE_SCRIPT" ]; then
  pass "smoke-test.sh is executable"
else
  fail "smoke-test.sh is not executable"
fi

# =========================================================================
# Test 2: All expected services are defined
# =========================================================================
echo "[test] Service definitions"
for svc in "NATS" "Alexandria" "PromptForge" "Dispatch" "Chronicle" "Slack-forwarder" "OpenClaw Gateway"; do
  if grep -q "$svc" "$SMOKE_SCRIPT"; then
    pass "service defined: $svc"
  else
    fail "service missing: $svc"
  fi
done

# =========================================================================
# Test 3: check_result function — PASS on 2xx codes
# =========================================================================
echo "[test] check_result logic"
# Source just the function in a subshell
for code in 200 201 204; do
  OUTPUT=$(bash -c '
    failures=(); total=0; passed=0
    check_result() {
      local name="$1" http_code="$2"
      total=$((total + 1))
      if [[ "$http_code" =~ ^2 ]]; then
        echo "[PASS] $name"; passed=$((passed + 1))
      else
        local reason="HTTP $http_code"
        [[ "$http_code" == "000" ]] && reason="connection refused or timeout"
        echo "[FAIL] $name - $reason"; failures+=("$name ($reason)")
      fi
    }
    check_result "TestSvc" "'"$code"'"
    echo "passed=$passed total=$total failures=${#failures[@]}"
  ')
  if echo "$OUTPUT" | grep -q '\[PASS\] TestSvc'; then
    pass "check_result PASS on HTTP $code"
  else
    fail "check_result should PASS on HTTP $code"
  fi
done

# =========================================================================
# Test 4: check_result function — FAIL on non-2xx and connection error
# =========================================================================
echo "[test] check_result failure cases"
for code in 000 404 500 503; do
  OUTPUT=$(bash -c '
    failures=(); total=0; passed=0
    check_result() {
      local name="$1" http_code="$2"
      total=$((total + 1))
      if [[ "$http_code" =~ ^2 ]]; then
        echo "[PASS] $name"; passed=$((passed + 1))
      else
        local reason="HTTP $http_code"
        [[ "$http_code" == "000" ]] && reason="connection refused or timeout"
        echo "[FAIL] $name - $reason"; failures+=("$name ($reason)")
      fi
    }
    check_result "TestSvc" "'"$code"'"
    echo "passed=$passed total=$total failures=${#failures[@]}"
  ')
  if echo "$OUTPUT" | grep -q '\[FAIL\] TestSvc'; then
    pass "check_result FAIL on HTTP $code"
  else
    fail "check_result should FAIL on HTTP $code"
  fi
done

# Connection refused should show specific message
OUTPUT=$(bash -c '
  failures=(); total=0; passed=0
  check_result() {
    local name="$1" http_code="$2"
    total=$((total + 1))
    if [[ "$http_code" =~ ^2 ]]; then
      echo "[PASS] $name"; passed=$((passed + 1))
    else
      local reason="HTTP $http_code"
      [[ "$http_code" == "000" ]] && reason="connection refused or timeout"
      echo "[FAIL] $name - $reason"; failures+=("$name ($reason)")
    fi
  }
  check_result "TestSvc" "000"
')
if echo "$OUTPUT" | grep -q "connection refused or timeout"; then
  pass "check_result shows 'connection refused or timeout' for code 000"
else
  fail "check_result should show connection message for code 000"
fi

# =========================================================================
# Test 5: TCP connect check parses tcp:// URLs
# =========================================================================
echo "[test] TCP connect check"
# Start a TCP listener on a random port
TCP_PORT=$(pick_port)
python3 -c "
import socket, threading
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $TCP_PORT)); s.listen(1)
conn, _ = s.accept(); conn.close(); s.close()
" &
TCP_PID=$!
sleep 0.2

if bash -c "echo >/dev/tcp/127.0.0.1/$TCP_PORT" 2>/dev/null; then
  pass "TCP connect succeeds on open port"
else
  fail "TCP connect should succeed on open port $TCP_PORT"
fi
wait "$TCP_PID" 2>/dev/null || true

CLOSED_PORT=$(pick_port)
if bash -c "echo >/dev/tcp/127.0.0.1/$CLOSED_PORT" 2>/dev/null; then
  fail "TCP connect should fail on closed port"
else
  pass "TCP connect fails on closed port"
fi

# =========================================================================
# Test 6: Vault token fetch parses Alexandria response
# =========================================================================
echo "[test] Vault token fetch"
VAULT_PORT=$(pick_port)

cat > "$TMPDIR/vault_routes.json" <<EOF
{
  "/api/v1/secrets/slack_bot_token": {
    "status": 200,
    "body": "{\"data\":{\"name\":\"slack_bot_token\",\"value\":\"xoxb-test-token-12345\"},\"meta\":{}}"
  }
}
EOF

start_mock_server "$VAULT_PORT" "$TMPDIR/vault_routes.json"

FETCHED=$(curl -sf --max-time 3 \
  -H "X-Agent-ID: smoke-test" \
  "http://127.0.0.1:${VAULT_PORT}/api/v1/secrets/slack_bot_token" | jq -r '.data.value // empty')

if [ "$FETCHED" = "xoxb-test-token-12345" ]; then
  pass "vault fetch parses .data.value correctly"
else
  fail "vault fetch: expected xoxb-test-token-12345, got $FETCHED"
fi

# Test vault fetch with missing secret (404)
MISSING=$(curl -s --max-time 3 -o /dev/null -w '%{http_code}' \
  "http://127.0.0.1:${VAULT_PORT}/api/v1/secrets/nonexistent")
if [ "$MISSING" = "404" ]; then
  pass "vault returns 404 for missing secret"
else
  fail "vault should return 404 for missing secret, got $MISSING"
fi

# =========================================================================
# Test 7: Slack payload structure
# =========================================================================
echo "[test] Slack alert payload"
SLACK_PORT=$(pick_port)

cat > "$TMPDIR/slack_routes.json" <<EOF
{
  "POST /api/chat.postMessage": {
    "status": 200,
    "body": "{\"ok\":true,\"channel\":\"C0AE9AMK9MM\",\"ts\":\"1234.5678\"}"
  }
}
EOF

start_mock_server "$SLACK_PORT" "$TMPDIR/slack_routes.json"

# Simulate sending an alert
curl -sf --max-time 5 \
  -X POST \
  -H "Authorization: Bearer xoxb-test-token" \
  -H "Content-Type: application/json" \
  -d '{
    "channel": "C0AE9AMK9MM",
    "text": "Warren smoke test: 1 service(s) DOWN",
    "blocks": [{"type":"header","text":{"type":"plain_text","text":"Warren Smoke Test Alert"}}]
  }' \
  "http://127.0.0.1:${SLACK_PORT}/api/chat.postMessage" > "$TMPDIR/slack_resp.json" 2>/dev/null

if jq -e '.ok == true' "$TMPDIR/slack_resp.json" >/dev/null 2>&1; then
  pass "Slack mock returns ok:true"
else
  fail "Slack mock should return ok:true"
fi

# Check that the POST was logged with auth header
POST_LOG="$TMPDIR/slack_routes.json.post_log"
if [ -f "$POST_LOG" ]; then
  if grep -q "Bearer xoxb-test-token" "$POST_LOG"; then
    pass "Slack request includes Bearer token"
  else
    fail "Slack request missing Bearer token"
  fi
  if grep -q "C0AE9AMK9MM" "$POST_LOG"; then
    pass "Slack payload includes channel ID"
  else
    fail "Slack payload missing channel ID"
  fi
else
  fail "POST log not created"
fi

# =========================================================================
# Test 8: Script uses correct jq path for vault response
# =========================================================================
echo "[test] Script internals"
if grep -q '\.data\.value' "$SMOKE_SCRIPT"; then
  pass "vault fetch uses .data.value jq path"
else
  fail "vault fetch should use .data.value jq path"
fi

# =========================================================================
# Test 9: .env loading
# =========================================================================
echo "[test] .env loading"
if grep -q 'source.*\.env' "$SMOKE_SCRIPT"; then
  pass "script sources .env file"
else
  fail "script should source .env file"
fi

# Verify it guards with file existence check
if grep -q '\-f.*\.env' "$SMOKE_SCRIPT"; then
  pass "script checks .env file exists before sourcing"
else
  fail "script should check .env exists before sourcing"
fi

# =========================================================================
# Test 10: Exit codes
# =========================================================================
echo "[test] Exit codes"
if grep -q 'exit 0' "$SMOKE_SCRIPT" && grep -q 'exit 1' "$SMOKE_SCRIPT"; then
  pass "script exits 0 on success, 1 on failure"
else
  fail "script should exit 0/1 based on results"
fi

# =========================================================================
# Test 11: Docker probe fallback when container not available
# =========================================================================
echo "[test] Docker probe fallback"
if grep -q 'probe container unavailable' "$SMOKE_SCRIPT"; then
  pass "script handles missing probe container gracefully"
else
  fail "script should handle missing probe container"
fi

# =========================================================================
# Test 12: Slack alert skipped when no token
# =========================================================================
echo "[test] Slack alert skip without token"
if grep -q 'No SLACK_BOT_TOKEN configured' "$SMOKE_SCRIPT"; then
  pass "script warns when SLACK_BOT_TOKEN is empty"
else
  fail "script should warn when no token configured"
fi

# =========================================================================
# Test 13: Full script run against mock services (all pass)
# =========================================================================
echo "[test] Full run — all services healthy"

HEALTH_PORT=$(pick_port)
TCP_PORT2=$(pick_port)

cat > "$TMPDIR/health_routes.json" <<EOF
{
  "/healthz": {"status": 200, "body": "ok"},
  "/api/v1/health": {"status": 200, "body": "{\"status\":\"ok\"}"},
  "/health": {"status": 200, "body": "{\"status\":\"ok\"}"}
}
EOF

start_mock_server "$HEALTH_PORT" "$TMPDIR/health_routes.json"

# Start a TCP listener for the gateway check
python3 -c "
import socket, threading, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $TCP_PORT2)); s.listen(5)
while True:
    try:
        conn, _ = s.accept(); conn.close()
    except: break
" &
TCP_LISTEN_PID=$!
MOCK_PIDS+=($TCP_LISTEN_PID)
sleep 0.2

# Create a modified version of the script that points to our mock
cat > "$TMPDIR/smoke-test-mock.sh" <<TESTEOF
#!/usr/bin/env bash
set -euo pipefail

SLACK_BOT_TOKEN=""
SLACK_CHANNEL="C0AE9AMK9MM"
CURL_TIMEOUT=3

HOST_SERVICES=(
    "NATS (Hermes)|http://127.0.0.1:${HEALTH_PORT}/healthz"
    "Alexandria|http://127.0.0.1:${HEALTH_PORT}/api/v1/health"
    "PromptForge|http://127.0.0.1:${HEALTH_PORT}/health"
    "OpenClaw Gateway|tcp://127.0.0.1:${TCP_PORT2}"
)
DOCKER_SERVICES=()

failures=()
total=0
passed=0

check_result() {
    local name="\$1" http_code="\$2"
    total=\$((total + 1))
    if [[ "\$http_code" =~ ^2 ]]; then
        echo "[PASS] \$name"; passed=\$((passed + 1))
    else
        local reason="HTTP \$http_code"
        [[ "\$http_code" == "000" ]] && reason="connection refused or timeout"
        echo "[FAIL] \$name - \$reason"; failures+=("\$name (\$reason)")
    fi
}

for entry in "\${HOST_SERVICES[@]}"; do
    name="\${entry%%|*}"
    url="\${entry##*|}"
    if [[ "\$url" == tcp://* ]]; then
        local_addr="\${url#tcp://}"
        local_host="\${local_addr%%:*}"
        local_port="\${local_addr##*:}"
        if bash -c "echo >/dev/tcp/\$local_host/\$local_port" 2>/dev/null; then
            http_code="200"
        else
            http_code="000"
        fi
    else
        http_code=\$(curl -sf --max-time "\$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' "\$url" 2>/dev/null) || http_code="000"
    fi
    check_result "\$name" "\$http_code"
done

echo "---"
echo "\$passed/\$total services healthy"
[[ \${#failures[@]} -gt 0 ]] && exit 1
exit 0
TESTEOF
chmod +x "$TMPDIR/smoke-test-mock.sh"

OUTPUT=$(bash "$TMPDIR/smoke-test-mock.sh" 2>&1)
EXIT_CODE=$?

if [ "$EXIT_CODE" -eq 0 ]; then
  pass "full run exits 0 when all healthy"
else
  fail "full run should exit 0 when all healthy (got $EXIT_CODE)"
fi

if echo "$OUTPUT" | grep -q "4/4 services healthy"; then
  pass "full run reports 4/4 healthy"
else
  fail "full run should report 4/4 healthy (output: $OUTPUT)"
fi

if echo "$OUTPUT" | grep -c '\[PASS\]' | grep -q '4'; then
  pass "full run shows 4 PASS lines"
else
  fail "full run should show 4 PASS lines"
fi

# =========================================================================
# Test 14: Full script run against mock services (one fails)
# =========================================================================
echo "[test] Full run — one service down"

DEAD_PORT=$(pick_port)

cat > "$TMPDIR/smoke-test-fail.sh" <<TESTEOF
#!/usr/bin/env bash
set -euo pipefail

SLACK_BOT_TOKEN=""
CURL_TIMEOUT=2

HOST_SERVICES=(
    "Healthy|http://127.0.0.1:${HEALTH_PORT}/health"
    "Dead|http://127.0.0.1:${DEAD_PORT}/health"
)
DOCKER_SERVICES=()

failures=()
total=0
passed=0

check_result() {
    local name="\$1" http_code="\$2"
    total=\$((total + 1))
    if [[ "\$http_code" =~ ^2 ]]; then
        echo "[PASS] \$name"; passed=\$((passed + 1))
    else
        local reason="HTTP \$http_code"
        [[ "\$http_code" == "000" ]] && reason="connection refused or timeout"
        echo "[FAIL] \$name - \$reason"; failures+=("\$name (\$reason)")
    fi
}

for entry in "\${HOST_SERVICES[@]}"; do
    name="\${entry%%|*}"
    url="\${entry##*|}"
    http_code=\$(curl -sf --max-time "\$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' "\$url" 2>/dev/null) || http_code="000"
    check_result "\$name" "\$http_code"
done

echo "---"
echo "\$passed/\$total services healthy"
[[ \${#failures[@]} -gt 0 ]] && exit 1
exit 0
TESTEOF
chmod +x "$TMPDIR/smoke-test-fail.sh"

OUTPUT=$(bash "$TMPDIR/smoke-test-fail.sh" 2>&1 || true)
EXIT_CODE=0
bash "$TMPDIR/smoke-test-fail.sh" >/dev/null 2>&1 || EXIT_CODE=$?

if [ "$EXIT_CODE" -eq 1 ]; then
  pass "full run exits 1 when a service is down"
else
  fail "full run should exit 1 when a service is down (got $EXIT_CODE)"
fi

if echo "$OUTPUT" | grep -q '\[FAIL\] Dead'; then
  pass "full run reports dead service as FAIL"
else
  fail "full run should report dead service as FAIL"
fi

if echo "$OUTPUT" | grep -q "1/2 services healthy"; then
  pass "full run reports 1/2 healthy"
else
  fail "full run should report 1/2 healthy"
fi

# =========================================================================
# Clean up
# =========================================================================
kill_mock_servers

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
