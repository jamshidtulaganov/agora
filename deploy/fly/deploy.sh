#!/usr/bin/env bash
# One-shot fly.io deploy for Agora: sd-agora-db (pgvector) + sd-agora-backend (Go) +
# sd-agora-web (Next.js) + sd-agora-tg (Telegram Mini App). Idempotent — safe to
# re-run; the web app and the Telegram Mini App are the only public apps.
#
# Prereqs:
#   - flyctl logged in:  fly auth login
#   - cp deploy/fly/secrets.example.env deploy/fly/secrets.env  && fill it in
#
# Usage (from repo root):
#   bash deploy/fly/deploy.sh            # full deploy
#   bash deploy/fly/deploy.sh db         # just the database
#   bash deploy/fly/deploy.sh backend    # just the backend
#   bash deploy/fly/deploy.sh web        # just the web app
#   bash deploy/fly/deploy.sh telegram   # just the Telegram Mini App
set -euo pipefail

ORG="personal"
REGION="fra"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FLYDIR="$ROOT/deploy/fly"
cd "$ROOT"

# shellcheck disable=SC1091
[ -f "$FLYDIR/secrets.env" ] || { echo "missing $FLYDIR/secrets.env (copy secrets.example.env)"; exit 1; }
set -a; . "$FLYDIR/secrets.env"; set +a
: "${POSTGRES_PASSWORD:?set in secrets.env}"
: "${JWT_SECRET:?set in secrets.env}"
: "${SMTP_PASSWORD:?set in secrets.env}"

app_exists() { fly apps list 2>/dev/null | awk '{print $1}' | grep -qx "$1"; }
ensure_app() { app_exists "$1" || fly apps create "$1" --org "$ORG"; }
vol_exists() { fly volumes list --app "$1" 2>/dev/null | grep -q "$2"; }
ensure_vol() { vol_exists "$1" "$2" || fly volumes create "$2" --app "$1" --region "$REGION" --size "$3" --yes; }

deploy_db() {
  echo "==> sd-agora-db"
  ensure_app sd-agora-db
  ensure_vol sd-agora-db agora_pgdata 3
  fly secrets set POSTGRES_PASSWORD="$POSTGRES_PASSWORD" --app sd-agora-db --stage
  fly deploy --config "$FLYDIR/db/fly.toml" --app sd-agora-db --yes
}

deploy_backend() {
  echo "==> sd-agora-backend"
  ensure_app sd-agora-backend
  ensure_vol sd-agora-backend agora_uploads 1
  # The prod db volume was initialized under the OLD repo name — role + database
  # are BOTH "multica", not "agora" (postgres runs initdb once; POSTGRES_USER/DB
  # changes in db/fly.toml after that are ignored). DATABASE_URL must match the
  # real names, or the backend crash-loops on FATAL: role "agora" does not exist.
  # Override via POSTGRES_USER / POSTGRES_DB in secrets.env if the volume is ever
  # re-initialized under a different name.
  local dbuser="${POSTGRES_USER:-multica}"
  local dbname="${POSTGRES_DB:-multica}"
  local dburl="postgres://${dbuser}:${POSTGRES_PASSWORD}@sd-agora-db.internal:5432/${dbname}?sslmode=disable"
  fly secrets set \
    DATABASE_URL="$dburl" \
    JWT_SECRET="$JWT_SECRET" \
    SMTP_PASSWORD="$SMTP_PASSWORD" \
    TELEGRAM_BOT_TOKEN="$TELEGRAM_BOT_TOKEN" \
    TELEGRAM_WEBHOOK_SECRET="$TELEGRAM_WEBHOOK_SECRET" \
    TELEGRAM_MINIAPP_SHORTNAME="${TELEGRAM_MINIAPP_SHORTNAME:-}" \
    BITRIX_WEBHOOK_URL="$BITRIX_WEBHOOK_URL" \
    BITRIX_INBOUND_SECRET="$BITRIX_INBOUND_SECRET" \
    AGORA_GIT_SECRET_KEY="${AGORA_GIT_SECRET_KEY:-}" \
    --app sd-agora-backend --stage
  # Build context = repo root; Dockerfile copies server/.
  fly deploy --config "$FLYDIR/backend/fly.toml" --app sd-agora-backend \
    --dockerfile Dockerfile --yes .
}

deploy_web() {
  echo "==> sd-agora-web"
  ensure_app sd-agora-web
  fly deploy --config "$FLYDIR/web/fly.toml" --app sd-agora-web \
    --dockerfile Dockerfile.web \
    --build-arg REMOTE_API_URL=http://sd-agora-backend.internal:8080 --yes .
  echo "web: https://sd-agora-web.fly.dev"
}

deploy_telegram() {
  echo "==> sd-agora-tg (Telegram Mini App)"
  ensure_app sd-agora-tg
  # Static SPA; nginx proxies /api,/ws,/auth,/uploads to the private backend.
  # No app secrets — auth rides the same backend the web app uses.
  fly deploy --config "$FLYDIR/telegram/fly.toml" --app sd-agora-tg \
    --dockerfile Dockerfile.telegram --yes .
  echo "telegram mini app: https://sd-agora-tg.fly.dev"
  echo "  → set this URL as the Mini App in @BotFather (/newapp), then set"
  echo "    TELEGRAM_MINIAPP_SHORTNAME in secrets.env and re-run: deploy.sh backend"
}

deploy_daemon() {
  echo "==> sd-agora-daemon (always-on agent runtime)"
  : "${AGORA_PAT:?set AGORA_PAT in secrets.env (mul_ user PAT from Settings -> Tokens)}"
  claude_secret=""
  [ -n "${ANTHROPIC_API_KEY:-}" ] && claude_secret="ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY"
  [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && claude_secret="CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN"
  : "${claude_secret:?set ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN in secrets.env}"
  ensure_app sd-agora-daemon
  ensure_vol sd-agora-daemon agora_daemon_data 3
  # shellcheck disable=SC2086
  fly secrets set AGORA_PAT="$AGORA_PAT" $claude_secret --app sd-agora-daemon --stage
  fly deploy --config "$FLYDIR/daemon/fly.toml" --app sd-agora-daemon \
    --dockerfile Dockerfile.daemon --yes .
  echo "daemon: running, dialing https://sd-agora-web.fly.dev (no public URL)"
}

case "${1:-all}" in
  db) deploy_db ;;
  backend) deploy_backend ;;
  web) deploy_web ;;
  telegram) deploy_telegram ;;
  daemon) deploy_daemon ;;
  all) deploy_db; deploy_backend; deploy_web; deploy_telegram ;;
  *) echo "usage: deploy.sh [db|backend|web|telegram|daemon|all]"; exit 1 ;;
esac

echo "Done. Remember: add https://sd-agora-web.fly.dev/auth/callback to Google OAuth redirect URIs."
