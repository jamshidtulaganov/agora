#!/bin/sh
set -e

# Remote Boxes control-plane SSH key: Fly secrets are env-only, but the
# backend reads the key from a FILE (AGORA_REMOTE_BOXES_SSH_KEY is a path).
# When the base64 form is provided, materialize it before the server starts.
if [ -n "${AGORA_REMOTE_BOXES_SSH_KEY_B64:-}" ]; then
  mkdir -p /keys
  echo "$AGORA_REMOTE_BOXES_SSH_KEY_B64" | base64 -d > /keys/agora_remote_boxes
  chmod 600 /keys/agora_remote_boxes
  export AGORA_REMOTE_BOXES_SSH_KEY="${AGORA_REMOTE_BOXES_SSH_KEY:-/keys/agora_remote_boxes}"
  echo "Remote Boxes SSH key materialized at /keys/agora_remote_boxes"
fi

echo "Running database migrations..."
./migrate up

echo "Starting server..."
exec ./server
