#!/usr/bin/env sh
# Headless boot for the always-on Agora daemon (Fly cloud node).
set -eu

: "${AGORA_PAT:?set AGORA_PAT — a mul_ user PAT from Settings -> Tokens}"
: "${AGORA_SERVER_URL:?set AGORA_SERVER_URL}"

mkdir -p "${AGORA_WORKSPACES_ROOT:-/data/workspaces}"

# Provision git auth for the cloud daemon. DAEMON_SSH_KEY is a private key (Fly
# secret) whose PUBLIC half is registered on the git hosts. We configure this
# SYSTEM-WIDE (under /etc, not $HOME) so it applies no matter what HOME the
# daemon sets for the git/ssh child processes it spawns per task — relying on
# $HOME/.ssh failed in practice ("Host key verification failed"). Idempotent.
if [ -n "${DAEMON_SSH_KEY:-}" ]; then
  KEYDIR=/etc/agora-ssh
  mkdir -p "$KEYDIR" && chmod 700 "$KEYDIR"
  printf '%s\n' "$DAEMON_SSH_KEY" > "$KEYDIR/id_ed25519"
  chmod 600 "$KEYDIR/id_ed25519"
  : > "$KEYDIR/known_hosts" && chmod 644 "$KEYDIR/known_hosts"
  # Best-effort pre-seed; accept-new below covers any host this misses.
  ssh-keyscan -p 2222 ssh-gitlab.sdteam.uz 2>/dev/null >> "$KEYDIR/known_hosts" || true
  ssh-keyscan github.com 2>/dev/null >> "$KEYDIR/known_hosts" || true
  for h in ${DAEMON_SSH_KNOWN_HOSTS:-}; do
    ssh-keyscan "$h" 2>/dev/null >> "$KEYDIR/known_hosts" || true
  done

  # System SSH client config: use the daemon key for these hosts and auto-accept
  # first-seen host keys (TOFU) so a failed/empty keyscan can't break clones.
  mkdir -p /etc/ssh/ssh_config.d
  cat > /etc/ssh/ssh_config.d/agora-daemon.conf <<EOF
Host ssh-gitlab.sdteam.uz
  Port 2222
  IdentityFile $KEYDIR/id_ed25519
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
  UserKnownHostsFile $KEYDIR/known_hosts
Host github.com
  IdentityFile $KEYDIR/id_ed25519
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
  UserKnownHostsFile $KEYDIR/known_hosts
EOF
  # Append to the main config if it doesn't already Include ssh_config.d.
  if ! grep -q "ssh_config.d/\*.conf" /etc/ssh/ssh_config 2>/dev/null; then
    cat /etc/ssh/ssh_config.d/agora-daemon.conf >> /etc/ssh/ssh_config
  fi

  # System gitconfig (HOME-independent): rewrite HTTPS GitHub remotes to SSH so
  # the daemon key authenticates them — HTTPS clones prompt for a username and
  # fail headless ("could not read Username for https://github.com").
  git config --system url."git@github.com:".insteadOf "https://github.com/" || true
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
