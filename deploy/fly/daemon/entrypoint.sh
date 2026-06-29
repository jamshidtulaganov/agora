#!/usr/bin/env sh
# Headless boot for the always-on Agora daemon (Fly cloud node).
set -eu

: "${AGORA_PAT:?set AGORA_PAT — a mul_ user PAT from Settings -> Tokens}"
: "${AGORA_SERVER_URL:?set AGORA_SERVER_URL}"

mkdir -p "${AGORA_WORKSPACES_ROOT:-/data/workspaces}"

# Provision SSH for git clone/push when a key is supplied. DAEMON_SSH_KEY is a
# private key (OpenSSH/PEM, multi-line) set as a Fly secret; its PUBLIC half must
# be registered on the git hosts the daemon pulls from (self-hosted GitLab account
# SSH key + private GitHub). Without this the cloud daemon cannot clone private
# SSH repos. known_hosts is pre-seeded so StrictHostKeyChecking stays on with no
# interactive prompt. Idempotent: re-run on every boot.
if [ -n "${DAEMON_SSH_KEY:-}" ]; then
  mkdir -p "$HOME/.ssh"
  chmod 700 "$HOME/.ssh"
  printf '%s\n' "$DAEMON_SSH_KEY" > "$HOME/.ssh/id_ed25519"
  chmod 600 "$HOME/.ssh/id_ed25519"
  : > "$HOME/.ssh/known_hosts"
  # Self-hosted GitLab SSH (port 2222) + GitHub. Extra hosts via DAEMON_SSH_KNOWN_HOSTS.
  ssh-keyscan -p 2222 ssh-gitlab.sdteam.uz 2>/dev/null >> "$HOME/.ssh/known_hosts" || true
  ssh-keyscan github.com 2>/dev/null >> "$HOME/.ssh/known_hosts" || true
  for h in ${DAEMON_SSH_KNOWN_HOSTS:-}; do
    ssh-keyscan "$h" 2>/dev/null >> "$HOME/.ssh/known_hosts" || true
  done
  chmod 644 "$HOME/.ssh/known_hosts"
fi

# Authenticate to the backend with the user PAT. resolveServerURL honors
# AGORA_SERVER_URL, and `login` persists server_url + token to
# ~/.agora/config.json (HOME=/data, on the volume). Re-running on every boot
# keeps the stored token fresh after restarts / redeploys.
agora login --token "$AGORA_PAT"

# Run the daemon in the foreground as PID 1 (Fly restarts the machine on exit).
# `daemon start` backgrounds by default; --foreground keeps it attached.
# --no-auto-update because the container image is immutable.
exec agora daemon start --foreground --no-auto-update
