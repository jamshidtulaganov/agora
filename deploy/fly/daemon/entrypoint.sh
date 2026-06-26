#!/usr/bin/env sh
# Headless boot for the always-on Agora daemon (Fly cloud node).
set -eu

: "${AGORA_PAT:?set AGORA_PAT — a mul_ user PAT from Settings -> Tokens}"
: "${AGORA_SERVER_URL:?set AGORA_SERVER_URL}"

mkdir -p "${AGORA_WORKSPACES_ROOT:-/data/workspaces}"

# Authenticate to the backend with the user PAT. resolveServerURL honors
# AGORA_SERVER_URL, and `login` persists server_url + token to
# ~/.agora/config.json (HOME=/data, on the volume). Re-running on every boot
# keeps the stored token fresh after restarts / redeploys.
agora login --token "$AGORA_PAT"

# Run the daemon in the foreground as PID 1 (Fly restarts the machine on exit).
# `daemon start` backgrounds by default; --foreground keeps it attached.
# --no-auto-update because the container image is immutable.
exec agora daemon start --foreground --no-auto-update
