#!/bin/sh
set -e

FORGE_URL="${FORGE_URL:-http://warren_promptforge:8083}"
SOUL_SLUG="${SOUL_SLUG:-dutybound}"
AGENT_ID="${AGENT_ID:-dutybound}"

echo "[entrypoint:dutybound] Starting boot sequence..."

# --- Pull soul from PromptForge ---
echo "[entrypoint:dutybound] Pulling soul from PromptForge ($FORGE_URL)..."
SOUL_CONTENT=$(wget -qO- "$FORGE_URL/api/v1/prompts/$SOUL_SLUG/versions/latest" 2>/dev/null || echo "")
if [ -n "$SOUL_CONTENT" ]; then
  echo "$SOUL_CONTENT" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    content = data.get('content', {})
    if isinstance(content, str):
        content = json.loads(content)
    with open('/home/node/.openclaw/workspace/SOUL.md', 'w') as f:
        f.write('# Soul\n\n')
        for key, value in content.items():
            heading = key.replace('_', ' ').title()
            f.write(f'## {heading}\n')
            if isinstance(value, list):
                for item in value:
                    f.write(f'- {item}\n')
            elif isinstance(value, str):
                f.write(value + '\n')
            else:
                f.write(str(value) + '\n')
            f.write('\n')
    print('[entrypoint:dutybound] Soul written to SOUL.md')
except Exception as e:
    print(f'[entrypoint:dutybound] Soul parse failed: {e}')
" 2>&1
else
  echo "[entrypoint:dutybound] PromptForge unreachable, using local soul"
fi

# --- Start Hermes watcher in background ---
if [ -f /usr/local/shared-bin/nats ]; then
  echo "[hermes-watcher:$AGENT_ID] Starting NATS watcher..."
  (
    while true; do
      /usr/local/shared-bin/nats sub "swarm.>" --server="${NATS_URL:-nats://warren_hermes:4222}" 2>/dev/null | while IFS= read -r line; do
        case "$line" in
          *"$AGENT_ID"*|*"swarm.task."*|*"swarm.forge.agent.$AGENT_ID"*)
            echo "[hermes-watcher:$AGENT_ID] Event: $line"
            ;;
        esac
      done
      sleep 5
    done
  ) &
fi

echo "[entrypoint:dutybound] Boot complete, starting gateway..."
exec node /app/openclaw.mjs gateway
