#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CONTEXT=$(mktemp -d)
trap "rm -rf $CONTEXT" EXIT

echo "Assembling build context in $CONTEXT ..."

cp -r ~/MissionControl "$CONTEXT/MissionControl"
cp -r ~/openclaw-friend-src "$CONTEXT/openclaw-src"
cp "$SCRIPT_DIR/Dockerfile" "$CONTEXT/"
cp "$SCRIPT_DIR/supervisord.conf" "$CONTEXT/"
cp "$SCRIPT_DIR/openclaw.json" "$CONTEXT/"
cp "$SCRIPT_DIR/entrypoint.sh" "$CONTEXT/"

echo "Building dutybound-mc image ..."
docker build -t dutybound-mc:latest "$CONTEXT"

echo "Done. Image: dutybound-mc:latest"
