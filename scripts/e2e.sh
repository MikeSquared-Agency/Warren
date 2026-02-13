#!/usr/bin/env bash
set -euo pipefail

ADMIN="http://localhost:9090"
PROXY="http://localhost:8080"
TOKEN="e2e-test-token"
FAIL=0

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1 — $2"; FAIL=1; }

echo "=== Warren E2E Smoke Tests ==="

# 1. Admin health
echo "--- Admin Health ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$ADMIN/admin/health")
if [ "$HTTP" = "200" ]; then
  pass "GET /admin/health → 200"
else
  fail "GET /admin/health" "expected 200, got $HTTP"
fi

# 2. List agents
echo "--- List Agents ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$ADMIN/admin/agents")
if [ "$HTTP" = "200" ]; then
  pass "GET /admin/agents → 200"
  if grep -q "test-agent" /tmp/e2e_body; then
    pass "test-agent present in agent list"
  else
    fail "test-agent in list" "agent not found in response"
  fi
else
  fail "GET /admin/agents" "expected 200, got $HTTP"
fi

# 3. Get single agent
echo "--- Get Agent ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$ADMIN/admin/agents/test-agent")
if [ "$HTTP" = "200" ]; then
  pass "GET /admin/agents/test-agent → 200"
else
  fail "GET /admin/agents/test-agent" "expected 200, got $HTTP"
fi

# 4. Auth required (no token)
echo "--- Auth Required ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' "$ADMIN/admin/agents")
if [ "$HTTP" = "401" ]; then
  pass "GET /admin/agents (no token) → 401"
else
  fail "GET /admin/agents (no token)" "expected 401, got $HTTP"
fi

# 5. Add agent dynamically
echo "--- Dynamic Agent CRUD ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"dynamic-agent","hostname":"dynamic.local","backend":"http://localhost:9999","policy":"unmanaged"}' \
  "$ADMIN/admin/agents")
if [ "$HTTP" = "201" ] || [ "$HTTP" = "200" ]; then
  pass "POST /admin/agents (add dynamic-agent) → $HTTP"
else
  fail "POST /admin/agents" "expected 201 or 200, got $HTTP"
fi

# 6. Verify dynamic agent appears in list
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$ADMIN/admin/agents")
if grep -q "dynamic-agent" /tmp/e2e_body; then
  pass "dynamic-agent present after creation"
else
  fail "dynamic-agent in list" "agent not found after POST"
fi

# 7. Delete dynamic agent
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  "$ADMIN/admin/agents/dynamic-agent")
if [ "$HTTP" = "200" ] || [ "$HTTP" = "204" ]; then
  pass "DELETE /admin/agents/dynamic-agent → $HTTP"
else
  fail "DELETE /admin/agents/dynamic-agent" "expected 200 or 204, got $HTTP"
fi

# 8. Verify dynamic agent is gone
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$ADMIN/admin/agents")
if ! grep -q "dynamic-agent" /tmp/e2e_body; then
  pass "dynamic-agent removed after DELETE"
else
  fail "dynamic-agent removal" "agent still present after DELETE"
fi

# 9. Proxy routes to backend via Host header
echo "--- Proxy Routing ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' -H "Host: test.local" "$PROXY/")
if [ "$HTTP" = "200" ]; then
  pass "GET / via Host: test.local → 200 (proxied)"
else
  fail "Proxy routing" "expected 200, got $HTTP"
fi

# 10. Prometheus metrics
echo "--- Metrics ---"
HTTP=$(curl -s -o /tmp/e2e_body -w '%{http_code}' "$ADMIN/metrics")
if [ "$HTTP" = "200" ]; then
  pass "GET /metrics → 200"
else
  fail "GET /metrics" "expected 200, got $HTTP"
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "All Warren E2E tests passed."
else
  echo "Some Warren E2E tests FAILED."
  exit 1
fi
