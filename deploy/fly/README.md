# Deploying Agora to fly.io

Three apps in the `personal` org, region `fra` (mirrors the `sd-*` bots):

| App | What | Public? | Build |
|---|---|---|---|
| `agora-db` | Postgres + pgvector | no (6PN only) | `pgvector/pgvector:pg17` image |
| `agora-backend` | Go API, runs `migrate up` then `server` | no (6PN only) | repo `Dockerfile` |
| `agora-web` | Next.js standalone | **yes** — `https://agora-web.fly.dev` | repo `Dockerfile.web` |

Only `agora-web` gets a public IP. It proxies `/api`, `/ws`, `/auth`, `/uploads`
to `agora-backend.internal:8080` over Fly's private network (read at runtime via
`REMOTE_API_URL`, so repointing the backend needs no rebuild). The backend
reaches Postgres at `agora-db.internal:5432`.

## First deploy

```bash
fly auth login
cp deploy/fly/secrets.example.env deploy/fly/secrets.env   # then fill it in
bash deploy/fly/deploy.sh                                   # db -> backend -> web
```

`deploy.sh` is idempotent — creates apps/volumes only if missing, stages secrets,
deploys in dependency order. Re-run any single tier with `deploy.sh db|backend|web`.

## Secrets (never committed)

Set from `secrets.env` by the script — see `secrets.example.env`:

- `POSTGRES_PASSWORD` → agora-db
- `DATABASE_URL` (derived), `JWT_SECRET`, `SMTP_PASSWORD`, `GOOGLE_CLIENT_SECRET` → agora-backend

Non-secret config (public URLs, `GOOGLE_CLIENT_ID`, SMTP host/user) lives in each
app's `fly.toml` `[env]`.

## Post-deploy: Google OAuth

In Google Cloud Console → Credentials → the OAuth client, add:

- Authorized JavaScript origin: `https://agora-web.fly.dev`
- Authorized redirect URI: `https://agora-web.fly.dev/auth/callback`

(Local `http://localhost:3000/*` entries can stay alongside.)

## Notes / gotchas

- **Build context is the repo root** for both Dockerfiles (they copy `server/` and
  the pnpm workspace). The script passes `--dockerfile ... .` from the root — don't
  `cd` into the sub-dirs to deploy.
- **Single DB machine.** The `agora_pgdata` volume is not shared across machines;
  don't scale `agora-db` past 1. Same for `agora-backend`'s `agora_uploads` volume
  (used only when `S3_*` is unset — set those to go multi-machine).
- **`/docs`** rewrites to `DOCS_URL` (default `localhost:4000`) and will 404 in this
  setup. Set `DOCS_URL` in `web/fly.toml` or ignore if docs aren't deployed.
- **Backups:** self-hosted DB → take your own (`fly pg` snapshots don't apply).
  Consider `fly volumes snapshots` on `agora_pgdata`.
