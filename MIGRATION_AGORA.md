# multica → Agora migration plan

Status: **PLAN ONLY — no code changed yet.** Reviewed decision: scope TBD per phase.

## 1. Where things stand

The product is **already "Agora"** — this is a *finish-the-rebrand*, not a rename from scratch.

| Surface | State |
|---|---|
| Product copy, website (`agora.dev`), README title, `@AgoraAI` | ✅ Agora |
| Frontend monorepo packages (`@agora/web`, `@agora/desktop`, `@agora/core`, `@agora/ui`, `@agora/views`, `@agora/mobile`) | ✅ Agora |
| CLI binary (`server/cmd/agora`) | ✅ Agora |
| Backend image build target (`agora-backend`), helm chart dir (`deploy/helm/agora`) | ✅ Agora |
| **Go module path** `github.com/multica-ai/multica/server` | ❌ multica — 731 refs / **251 .go files** |
| **GitHub repo** `multica-ai/multica` (all clone/install/doc URLs) | ❌ multica |
| **Infra**: compose project `name: multica`, `POSTGRES_DB/USER/PASSWORD: multica`, GHCR `ghcr.io/multica-ai/multica-backend` | ❌ multica |
| **Token prefixes** `mul_` (PAT), `mdt_` (daemon), `mat_` (agent-task) | ❌ multica — but invisible to users |
| **Issue prefix** `MUL-` (default for new workspaces only; existing use `OCT-`/`HAN-`/…) | ❌ multica — mostly historical ticket refs in comments |

**Conclusion:** "migrate to Agora" = rebrand the **backend Go module + infra + GitHub repo**. The frontend needs nothing.

## 2. Decisions you make first

1. **New GitHub org/repo name?** e.g. `agora-ai/agora`, or keep org and rename repo only (`multica-ai/agora`), or keep repo as-is. *Everything URL-related depends on this.* GitHub auto-redirects the old path after a rename, so links don't hard-break immediately — but they should still be updated.
2. **Repo directory** (`~/Projects/multica`)? Renaming it changes Docker container names (`multica-backend-1` → `agora-backend-1`) but also **breaks local tooling that hard-codes the path** (Claude Code permissions, MCP server paths, worktrees, this session's cwd). Recommended: **leave the dir name**, set compose `name:` explicitly instead.
3. **Database name** (`multica`)? The live Octane data lives in a DB named `multica`. Renaming = `pg_dump`/restore. **Recommended: keep the DB name** — it's invisible to users; renaming buys nothing and risks data.
4. **Token prefixes** (`mul_`/`mdt_`/`mat_`)? Changing **invalidates every existing PAT, daemon token, and agent token**. Recommended: **leave them**, or do a dual-accept transition (Phase 5) only if there's a real reason.

## 3. Phased migration (ordered: low risk → high)

### Phase 1 — GitHub repo rename *(you, optional)*
- Rename org/repo on GitHub → new path. GitHub keeps redirects for the old path.
- Update the daemon's `gh`/git remotes if the local clones pin the old URL.
- **No code change yet.** Rollback: rename back on GitHub.

### Phase 2 — Go module path *(me, mechanical, ~251 files)*
- `go.mod`: `module github.com/multica-ai/multica/server` → `github.com/<new>/server`.
- Find/replace the import prefix `github.com/multica-ai/multica` across all `.go` files (sed + `gofmt`).
- Verify: `go build ./...` + `go vet ./...` + `make test` in the golang container.
- **Self-contained, no runtime/data impact.** Rollback: revert the commit.
- *Can be done even if you keep the GitHub repo name* (the module path is an internal string).

### Phase 3 — Infra identifiers *(me + coordinated deploy)*
- Compose `name: multica` → `agora` (changes container names → re-creates containers; harmless).
- GHCR image ref `ghcr.io/multica-ai/multica-backend` → new org (only if Phase 1 done).
- helm `deploy/helm/agora/*` values, `scripts/*`, `.env.example`, `.goreleaser.yml`, CI workflow.
- **Keep `POSTGRES_DB/USER/PASSWORD` defaults = `multica`** (or set them in `.env` and leave the live DB untouched) to avoid a data migration.
- Verify: `make dev` / the self-host compose comes up clean; backend connects to the existing DB.
- Rollback: revert; container/name changes are stateless.

### Phase 4 — Repo URLs in docs & copy *(me)*
- `apps/docs/**/*.mdx` (+ zh/ja/ko), `README*.md`, `CONTRIBUTING.md`, `SELF_HOSTING*.md`, `apps/web/features/landing/**`, `apps/docs/app/layout.config.tsx`, install scripts.
- Only meaningful **after** Phase 1 (need the final repo URL). If repo stays `multica-ai/multica`, these URLs are already correct — skip.

### Phase 5 — Data identifiers *(deferred / not recommended)*
- **Token prefixes**: only via dual-accept — middleware accepts `mul_` AND `agora_`, new tokens mint `agora_`, old ones keep working. Breaking otherwise. Low value (invisible).
- **`MUL-` issue prefix**: the in-code `MUL-####` are **historical ticket references in comments** — leave them. The *default* new-workspace prefix could change to `AGO-`, but existing workspaces already set their own.

## 4. Do NOT touch

- Historical ticket refs in comments (`MUL-2600`, `MUL-2339`, …) — they reference real past tickets.
- Existing workspace prefixes (`OCT-`, `HAN-`) and existing issue identifiers.
- Existing tokens / the live database name (unless you explicitly opt into Phase 5 / a DB rename).

## 5. Recommended minimal path

If the goal is "the code matches the Agora brand" with least risk:
**Phase 2 (Go module → agora) + Phase 3 (compose `name:` + scripts, keep DB name) + Phase 4 (only if you rename the GitHub repo).**
Skip Phase 5. No data migration, nothing user-visible breaks, fully reversible per-commit.

## 6. Effort

- Phase 2: ~1 commit, mechanical, must compile (251 files touched, ~30 min incl. verification).
- Phase 3: ~1 commit + 1 redeploy.
- Phase 4: ~1 commit (find/replace URLs).
- Phase 1 & the GitHub/DB decisions: yours.
