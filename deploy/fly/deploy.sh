#!/usr/bin/env bash
# One-shot fly.io deploy for Agora: agora-db (pgvector) + agora-backend (Go) +
# agora-web (Next.js). Idempotent — safe to re-run; only the web app is public.
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
: "${GOOGLE_CLIENT_SECRET:?set in secrets.env}"

app_exists() { fly apps list 2>/dev/null | awk '{print $1}' | grep -qx "$1"; }
ensure_app() { app_exists "$1" || fly apps create "$1" --org "$ORG"; }
vol_exists() { fly volumes list --app "$1" 2>/dev/null | grep -q "$2"; }
ensure_vol() { vol_exists "$1" "$2" || fly volumes create "$2" --app "$1" --region "$REGION" --size "$3" --yes; }

deploy_db() {
  echo "==> agora-db"
  ensure_app agora-db
  ensure_vol agora-db agora_pgdata 3
  fly secrets set POSTGRES_PASSWORD="$POSTGRES_PASSWORD" --app agora-db --stage
  fly deploy --config "$FLYDIR/db/fly.toml" --app agora-db --yes
}

deploy_backend() {
  echo "==> agora-backend"
  ensure_app agora-backend
  ensure_vol agora-backend agora_uploads 1
  local dburl="postgres://multica:${POSTGRES_PASSWORD}@agora-db.internal:5432/multica?sslmode=disable"
  fly secrets set \
    DATABASE_URL="$dburl" \
    JWT_SECRET="$JWT_SECRET" \
    SMTP_PASSWORD="$SMTP_PASSWORD" \
    GOOGLE_CLIENT_SECRET="$GOOGLE_CLIENT_SECRET" \
    --app agora-backend --stage
  # Build context = repo root; Dockerfile copies server/.
  fly deploy --config "$FLYDIR/backend/fly.toml" --app agora-backend \
    --dockerfile Dockerfile --yes .
}

deploy_web() {
  echo "==> agora-web"
  ensure_app agora-web
  fly deploy --config "$FLYDIR/web/fly.toml" --app agora-web \
    --dockerfile Dockerfile.web \
    --build-arg REMOTE_API_URL=http://agora-backend.internal:8080 --yes .
  echo "web: https://agora-web.fly.dev"
}

case "${1:-all}" in
  db) deploy_db ;;
  backend) deploy_backend ;;
  web) deploy_web ;;
  all) deploy_db; deploy_backend; deploy_web ;;
  *) echo "usage: deploy.sh [db|backend|web|all]"; exit 1 ;;
esac

echo "Done. Remember: add https://agora-web.fly.dev/auth/callback to Google OAuth redirect URIs."
