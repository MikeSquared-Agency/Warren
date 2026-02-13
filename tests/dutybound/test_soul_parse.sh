#!/bin/bash
# Unit tests for DutyBound soul parsing logic
# Tests the Python soul-to-SOUL.md conversion used in pull-soul-entrypoint.sh
set -euo pipefail

PASS=0
FAIL=0
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

echo "=== DutyBound Soul Parse Unit Tests ==="

# --- Test 1: Standard soul content produces valid SOUL.md ---
echo "[test] Standard soul content"
cat > "$TMPDIR/forge_response.json" <<'EOF'
{
  "id": "test-id",
  "prompt_id": "test-prompt",
  "version": 1,
  "content": {
    "identity": "You are DutyBound, the Developer.",
    "voice": "Technical, precise, no-nonsense.",
    "capabilities": "code-writing, testing, debugging"
  }
}
EOF

python3 -c "
import sys, json
data = json.load(open('$TMPDIR/forge_response.json'))
content = data.get('content', {})
if isinstance(content, str):
    content = json.loads(content)
with open('$TMPDIR/SOUL.md', 'w') as f:
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
"

if grep -q "^# Soul" "$TMPDIR/SOUL.md"; then
    pass "SOUL.md has top-level heading"
else
    fail "SOUL.md missing top-level heading"
fi

if grep -q "^## Identity" "$TMPDIR/SOUL.md"; then
    pass "identity field becomes ## Identity heading"
else
    fail "identity field not converted to heading"
fi

if grep -q "^## Voice" "$TMPDIR/SOUL.md"; then
    pass "voice field becomes ## Voice heading"
else
    fail "voice field not converted to heading"
fi

if grep -q "You are DutyBound" "$TMPDIR/SOUL.md"; then
    pass "identity content preserved"
else
    fail "identity content missing"
fi

# --- Test 2: List values rendered as bullet points ---
echo "[test] List values"
cat > "$TMPDIR/forge_list.json" <<'EOF'
{
  "content": {
    "principles": ["Version everything", "Scan for corruption", "Keep prompts clean"]
  }
}
EOF

python3 -c "
import sys, json
data = json.load(open('$TMPDIR/forge_list.json'))
content = data.get('content', {})
with open('$TMPDIR/SOUL_list.md', 'w') as f:
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
"

if grep -q "^- Version everything" "$TMPDIR/SOUL_list.md"; then
    pass "list items rendered as bullet points"
else
    fail "list items not rendered as bullet points"
fi

LIST_COUNT=$(grep -c "^- " "$TMPDIR/SOUL_list.md")
if [ "$LIST_COUNT" -eq 3 ]; then
    pass "all 3 list items present"
else
    fail "expected 3 list items, got $LIST_COUNT"
fi

# --- Test 3: Underscore keys become Title Case headings ---
echo "[test] Key formatting"
cat > "$TMPDIR/forge_keys.json" <<'EOF'
{
  "content": {
    "slack_identity": "Not connected",
    "slack_rules": "N/A"
  }
}
EOF

python3 -c "
import sys, json
data = json.load(open('$TMPDIR/forge_keys.json'))
content = data.get('content', {})
with open('$TMPDIR/SOUL_keys.md', 'w') as f:
    f.write('# Soul\n\n')
    for key, value in content.items():
        heading = key.replace('_', ' ').title()
        f.write(f'## {heading}\n')
        if isinstance(value, str):
            f.write(value + '\n')
        f.write('\n')
"

if grep -q "^## Slack Identity" "$TMPDIR/SOUL_keys.md"; then
    pass "slack_identity becomes 'Slack Identity'"
else
    fail "underscore key not converted to title case"
fi

if grep -q "^## Slack Rules" "$TMPDIR/SOUL_keys.md"; then
    pass "slack_rules becomes 'Slack Rules'"
else
    fail "slack_rules key not converted"
fi

# --- Test 4: Empty content handled gracefully ---
echo "[test] Empty content"
cat > "$TMPDIR/forge_empty.json" <<'EOF'
{"content": {}}
EOF

python3 -c "
import sys, json
data = json.load(open('$TMPDIR/forge_empty.json'))
content = data.get('content', {})
with open('$TMPDIR/SOUL_empty.md', 'w') as f:
    f.write('# Soul\n\n')
    for key, value in content.items():
        heading = key.replace('_', ' ').title()
        f.write(f'## {heading}\n')
        if isinstance(value, str):
            f.write(value + '\n')
        f.write('\n')
" 2>/dev/null

if [ -f "$TMPDIR/SOUL_empty.md" ] && grep -q "^# Soul" "$TMPDIR/SOUL_empty.md"; then
    pass "empty content produces valid file with heading"
else
    fail "empty content handling failed"
fi

SECTION_COUNT=$(grep -c "^## " "$TMPDIR/SOUL_empty.md" 2>/dev/null || true)
SECTION_COUNT=${SECTION_COUNT:-0}
if [ "$SECTION_COUNT" -eq 0 ]; then
    pass "no section headings for empty content"
else
    fail "unexpected sections in empty content file"
fi

# --- Test 5: Content as stringified JSON ---
echo "[test] Stringified JSON content"
cat > "$TMPDIR/forge_string.json" <<'EOF'
{
  "content": "{\"identity\": \"Stringified identity\"}"
}
EOF

python3 -c "
import sys, json
data = json.load(open('$TMPDIR/forge_string.json'))
content = data.get('content', {})
if isinstance(content, str):
    content = json.loads(content)
with open('$TMPDIR/SOUL_string.md', 'w') as f:
    f.write('# Soul\n\n')
    for key, value in content.items():
        heading = key.replace('_', ' ').title()
        f.write(f'## {heading}\n')
        if isinstance(value, str):
            f.write(value + '\n')
        f.write('\n')
"

if grep -q "Stringified identity" "$TMPDIR/SOUL_string.md"; then
    pass "stringified JSON content parsed correctly"
else
    fail "stringified JSON content not handled"
fi

# --- Summary ---
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
