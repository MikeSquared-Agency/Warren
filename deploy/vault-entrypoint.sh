#!/bin/sh
set -e

# vault-entrypoint.sh — Fetches secrets from Alexandria vault at boot,
# exports them as env vars, then execs the original command.
#
# Usage in stack.yaml:
#   entrypoint: ["/vault-entrypoint.sh"]
#   command: ["original-binary"]
#
# Env vars consumed:
#   VAULT_URL       — Alexandria base URL (default: http://warren_alexandria:8500)
#   VAULT_AGENT_ID  — Agent identity for access control
#   VAULT_SECRETS   — Comma-separated list of "SECRET_NAME:ENV_VAR" mappings
#   VAULT_TIMEOUT   — Max seconds to wait for Alexandria (default: 60)
#
# Example VAULT_SECRETS: "supabase_db_url:DATABASE_URL,slack_app_token:SLACK_APP_TOKEN"

VAULT_URL="${VAULT_URL:-http://warren_alexandria:8500}"
VAULT_TIMEOUT="${VAULT_TIMEOUT:-60}"

if [ -z "$VAULT_AGENT_ID" ] || [ -z "$VAULT_SECRETS" ]; then
  echo "[vault-entrypoint] ERROR: VAULT_AGENT_ID and VAULT_SECRETS must be set"
  exit 1
fi

if [ $# -eq 0 ]; then
  echo "[vault-entrypoint] ERROR: No command specified (pass as args)"
  exit 1
fi

# Detect HTTP client (Alpine has wget, Debian has curl)
if command -v wget >/dev/null 2>&1; then
  http_get() { wget -qO- "$1" --header="$2" 2>/dev/null; }
  http_check() { wget -qO /dev/null "$1" 2>/dev/null; }
elif command -v curl >/dev/null 2>&1; then
  http_get() { curl -sf "$1" -H "$2" 2>/dev/null; }
  http_check() { curl -sf -o /dev/null "$1" 2>/dev/null; }
else
  echo "[vault-entrypoint] ERROR: Neither wget nor curl found"
  exit 1
fi

echo "[vault-entrypoint] Agent: $VAULT_AGENT_ID"
echo "[vault-entrypoint] Waiting for Alexandria at $VAULT_URL ..."

# Wait for Alexandria to be reachable
elapsed=0
while ! http_check "$VAULT_URL/api/v1/secrets"; do
  elapsed=$((elapsed + 2))
  if [ "$elapsed" -ge "$VAULT_TIMEOUT" ]; then
    echo "[vault-entrypoint] ERROR: Alexandria not reachable after ${VAULT_TIMEOUT}s"
    exit 1
  fi
  sleep 2
done
echo "[vault-entrypoint] Alexandria is up"

# Fetch each secret and export as env var
IFS=','
for mapping in $VAULT_SECRETS; do
  secret_name=$(echo "$mapping" | cut -d: -f1)
  env_var=$(echo "$mapping" | cut -d: -f2)

  value=$(http_get "$VAULT_URL/api/v1/secrets/$secret_name" "X-Agent-ID: $VAULT_AGENT_ID" \
    | sed -n 's/.*"value"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')

  if [ -z "$value" ]; then
    echo "[vault-entrypoint] ERROR: Failed to fetch secret '$secret_name'"
    exit 1
  fi

  export "$env_var=$value"
  echo "[vault-entrypoint] Loaded $secret_name -> $env_var"
done
unset IFS

echo "[vault-entrypoint] All secrets loaded, starting: $*"
exec "$@"
