#!/usr/bin/env bash
# One-shot installer for the sd-dev fixed-verb agent gate (agora-box-shell).
#
# What it does, in order:
#   1. Generates the dedicated deploy keypair ~/.ssh/agora_sd_deploy (if absent).
#      This key is what AGENTS use; it can only reach the forced command below.
#   2. Uploads agora-box-shell to ~/bin on the box and writes the site
#      allowlist ~/.agora-box-sites.
#   3. Backs up ~/.ssh/authorized_keys, then appends ONE restricted entry:
#        restrict,command="/home/jamshidfr/bin/agora-box-shell" <pubkey>
#      "restrict" disables pty/port-forward/agent-forward; the forced command
#      means this key can NEVER get a shell — only deploy/rollback/status/read.
#   4. Smoke-tests the gate with `status` and verifies a write verb is denied.
#
# Run from your Mac:  bash deploy/sdteam/install-agora-box.sh
# Re-running is idempotent (the key line is appended only once).
set -euo pipefail

HOST=193.149.18.99
PORT=33022
BOX_USER=jamshidfr
ADMIN_KEY="$HOME/.ssh/sdteam"          # your normal full-access key (install only)
DEPLOY_KEY="$HOME/.ssh/agora_sd_deploy" # the restricted key agents will use
HERE="$(cd "$(dirname "$0")" && pwd)"

[ -f "$DEPLOY_KEY" ] || ssh-keygen -t ed25519 -N "" -C "agora-sd-deploy" -f "$DEPLOY_KEY" -q
PUB="$(cat "$DEPLOY_KEY.pub")"

scp -P "$PORT" -i "$ADMIN_KEY" "$HERE/agora-box-shell" "$BOX_USER@$HOST:bin/agora-box-shell"

ssh -p "$PORT" -i "$ADMIN_KEY" "$BOX_USER@$HOST" "
  set -e
  chmod 755 ~/bin/agora-box-shell
  printf '%s\n' jamshid.sdteam.uz sandbox.sdteam.uz agora.sdteam.uz agora-cs.sdteam.uz > ~/.agora-box-sites
  cp ~/.ssh/authorized_keys ~/.ssh/authorized_keys.bak-\$(date +%Y%m%d%H%M%S)
  grep -qF 'agora-sd-deploy' ~/.ssh/authorized_keys || \
    echo 'restrict,command=\"/home/jamshidfr/bin/agora-box-shell\" $PUB' >> ~/.ssh/authorized_keys
  echo '--- installed; allowlisted sites:'
  cat ~/.agora-box-sites
"

echo "--- smoke: status verb via restricted key"
ssh -p "$PORT" -i "$DEPLOY_KEY" -o IdentitiesOnly=yes "$BOX_USER@$HOST" status agora-cs.sdteam.uz

echo "--- smoke: write verb must be DENIED"
if ssh -p "$PORT" -i "$DEPLOY_KEY" -o IdentitiesOnly=yes "$BOX_USER@$HOST" rm -rf /tmp/x 2>/dev/null; then
  echo "!!! GATE FAILED OPEN — do not hand this key to agents"; exit 1
else
  echo "denied as expected — gate OK"
fi
