#!/bin/bash
set -e

OPENCLAW_HOME="/home/dutybound/.openclaw"

# Read docker secrets into env vars if they exist.
if [ -f /run/secrets/anthropic_api_key ]; then
    export ANTHROPIC_API_KEY=$(cat /run/secrets/anthropic_api_key)
fi
if [ -f /run/secrets/openclaw_gateway_token ]; then
    export OPENCLAW_GATEWAY_TOKEN=$(cat /run/secrets/openclaw_gateway_token)
fi

# Write auth-profiles.json for the main agent using the API key.
if [ -n "$ANTHROPIC_API_KEY" ]; then
    for agent_dir in "$OPENCLAW_HOME"/agents/*/agent; do
        mkdir -p "$agent_dir"
        cat > "$agent_dir/auth-profiles.json" <<EOF
{
  "version": 1,
  "profiles": {
    "anthropic:default": {
      "type": "token",
      "provider": "anthropic",
      "token": "$ANTHROPIC_API_KEY"
    }
  },
  "lastGood": {
    "anthropic": "anthropic:default"
  }
}
EOF
    done
fi

exec supervisord -c /etc/supervisor/conf.d/supervisord.conf
