# Shared local development env derivation. Source this after loading .env.

POSTGRES_DB="${POSTGRES_DB:-agora}"
POSTGRES_USER="${POSTGRES_USER:-agora}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"

PORT="${BACKEND_PORT:-${API_PORT:-${SERVER_PORT:-${PORT:-8080}}}}"
FRONTEND_PORT="${FRONTEND_PORT:-3000}"
FRONTEND_ORIGIN="${FRONTEND_ORIGIN:-http://localhost:${FRONTEND_PORT}}"

AGORA_APP_URL="${AGORA_APP_URL:-${FRONTEND_ORIGIN}}"
GOOGLE_REDIRECT_URI="${GOOGLE_REDIRECT_URI:-${FRONTEND_ORIGIN}/auth/callback}"
AGORA_SERVER_URL="${AGORA_SERVER_URL:-ws://localhost:${PORT}/ws}"
LOCAL_UPLOAD_BASE_URL="${LOCAL_UPLOAD_BASE_URL:-http://localhost:${PORT}}"
PLAYWRIGHT_BASE_URL="${PLAYWRIGHT_BASE_URL:-${FRONTEND_ORIGIN}}"

# A source-run backend shares the host network with the local daemon. Do not
# let a Docker-only `host.docker.internal` fallback from .env leak into
# `make dev` / `go run`; it is only resolvable from the backend container and
# turns artifact requests into 502s on the host. Developers who intentionally
# need a global remote-daemon fallback in local mode can set the explicit
# local override.
AGORA_DAEMON_INTERNAL="${AGORA_LOCAL_DAEMON_INTERNAL:-}"

export POSTGRES_DB POSTGRES_USER POSTGRES_PORT
export PORT FRONTEND_PORT FRONTEND_ORIGIN
export AGORA_APP_URL GOOGLE_REDIRECT_URI AGORA_SERVER_URL LOCAL_UPLOAD_BASE_URL
export PLAYWRIGHT_BASE_URL AGORA_DAEMON_INTERNAL
