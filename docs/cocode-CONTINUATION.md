# Co-code + QA — CONTINUATION (resume-in-one-context)

> **Next account / next session: READ THIS FIRST**, then memory `agora-live-editor` + `agora-cocode-qa-design` + the design doc `docs/cocode-qa-design.md`. This file is updated **after every run** with current changes + next plan so you continue seamlessly.
> Updated: **2026-06-28**.

## TL;DR — where we are
Built a full **co-code editor** (human + AI agent co-code on an issue) + started the **QA layer** on top. Everything is **LOCAL self-host only (NOT on prod)**, all **uncommitted** on branch `sd-platform` (~/Projects/agora).
- Co-code editor: **COMPLETE** — old-dev trust layer (6) + modern-dev flow layer (5) + Ask front-door + comfort (expand-default, preview auto-install, inline changes) + live-tested on octane issue C-29.
- QA layer: **Phase 1 core DONE** (Gates panel + Run-QA button + deterministic recipe). Phases 2–4 + refinements + 4 decisions remain.

## Current uncommitted changes (sd-platform, +~1750 lines)
**New files (co-code + QA):** `server/internal/daemon/browser.go` (embedded Chromium + CDP screencast bridge), `server/internal/handler/{issue_steer,issue_work_mode,issue_repo_nudge,project_knowledge}.go`, `packages/core/issues/work-mode.ts`, `packages/views/issues/components/editor-{section is modified; ask-bar,browser-pane,changes-list,chat-panel,context-panel,gates,how-it-works,preview-pane,review-bar,run-qa,variants-dialog}.tsx` + `issue-repo-section.tsx` + `work-mode-switch.tsx`, `docs/cocode-qa-design.md`, `docs/cocode-CONTINUATION.md` (this).
**Modified:** `server/internal/daemon/{daemon,health,types}.go`, `server/internal/handler/{agent,daemon,editor,project}.go`, `server/cmd/server/router.go`, `packages/core/{api/client.ts,types/index.ts,mcp/types.ts}`, `packages/views/issues/components/{editor-section,issue-detail,agent-working-indicator}.tsx`, + landing-header/shared (light-mode fix), docker-compose.selfhost.yml (AGORA_DAEMON_EDITOR_URL).
*(Not committed. If a DB wipe / fresh clone happens, this working tree is the source of truth.)*

## NEXT PLAN (ordered)
1. **Live-test Run-QA** — open an in_editor issue → editor → **Run QA** → confirm the agent runs the deterministic recipe (build/lint/test + Playwright connectOverCDP smoke) + sets `qa:pass/fail` → Gates panel flips. *(1 agent run.)*
2. **Phase 2 — diff→regression tests**: from the cocode diff generate fail-before/pass-after tests, accept ONLY via executed build+pass+coverage filter (Assured-LLMSE/Qodo-Cover), commit into the PR.
3. **Phase 3 — Playwright Planner→Generator→Healer** (official 3-agent loop, a11y-mode) for code-grounded tests + flake self-heal.
4. **Phase 4 — exploratory (triaged, never auto-fail) + visual-regression (toHaveScreenshot) + a11y**.
5. **Refinements**: bind the QA recipe as a proper workspace SKILL (vs the inline prompt in `editor-run-qa.tsx`); generalize the backend `run_qa` slice instruction (`slice_action.go:88`, currently SD-box) for the slice-action path.
6. **4 decisions** (user's): tier→gate matrix · exploratory-signal triage recipe · flake/quarantine policy (Meta 5×-rerun was REFUTED — design our own) · test/visual-baseline ownership.
7. **Prod deploy** (the big remaining): web + backend + daemon to fly + cloud editor-proxy for the browser/preview panes (127.0.0.1 iframe → backend proxy). Local-first was the user's call.

## RESUME GUIDE (how to operate)
- **Stack:** ~/Projects/agora, docker `COMPOSE_PROJECT_NAME=agora`, web :3000 / backend :8080, DB creds agora/agora.
- **Build+deploy a service:** `COMPOSE_PROJECT_NAME=agora docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml build <frontend|backend> && … up -d <svc>`. (frontend=agora-web:dev, backend=agora-backend:dev. `--progress` is a build flag, NOT valid on `up`.)
- **Go compile-check (no deploy):** `docker run --rm -v "$PWD/server":/src -v agora_gomod:/go/pkg/mod -v agora_gobuild:/root/.cache/go-build -w /src golang:1.26.1 sh -c "gofmt -w <files> && go build ./..."`.
- **Frontend typecheck:** `pnpm --filter @agora/core --filter @agora/views typecheck`.
- **Editor/QA daemon:** `agora --profile local` on **127.0.0.1:20038** (endpoints in `health.go` + `browser.go`: /editor/launch,/changes,/open-pr,/discard,/preview{,/stop,/status},/browser/{start,stop,stream}; backend merge-readiness GET /api/issues/{id}/merge-readiness).
- **DAEMON SWAP (when daemon .go changes):** cross-compile `docker run … -e GOOS=darwin -e GOARCH=arm64 -e CGO_ENABLED=0 golang:1.26.1 go build -o bin/agora-new ./cmd/agora` → `codesign --force -s - server/bin/agora-new` (else killed:9) → replace BOTH `/opt/homebrew/bin/agora` + `/usr/local/bin/agora` → force-kill the :20038 holder → rm `~/.agora/profiles/local/daemon.pid` → restart `nohup env PATH="<captured from old daemon>:$PATH" /opt/homebrew/bin/agora --profile local daemon start &`. **The auto-mode classifier BLOCKS each daemon force-kill** → must ask the user (AskUserQuestion) every swap. A co-located 2nd Claude session auto-restarts the daemon (race handled by replace-binary-first).
- **PROVEN:** `playwright-core connectOverCDP(<embedded cdp_url>)` drives the embedded Chromium (cdp_url from /editor/browser/start) = watch-along QA.
- **AUTH:** the editor needs the user logged into localhost:3000 (Telegram-OTP — Claude CANNOT log in). Drive the UI via Claude-in-Chrome MCP (the user's logged-in Chrome). Local DB has octane/sd-main/etc. workspaces.

## CONSTRAINTS (hard)
- **Get approval before deploy** (even local) — bare "continue/yes" answers only the open question.
- **Don't touch CI/CD** (.github/workflows, .gitlab-ci.yml) — user owns it.
- **Don't modify repo access controls** (GitHub branch protection) — direct the user to run `gh api PUT …/branches/main/protection` themselves.
- **Blocked from prod DB**; never echo secrets (read tokens into a shell var for curl, never print).
- **Branch protection = the only hard merge wall** for co-code (an autonomous agent can otherwise merge to main). Enabled on jamshidtulaganov/browser-automation; Octane repos (johny-mercer4/*) need the owner.

## RUN LOG (append newest at top)
- **2026-06-28 r3:** Built **4 agent-pipeline fixes** (uncommitted, all compile + pure tests green): (1) **project→squad binding** — `project.squad_id` (migration 126) + API + enforcement in issue create/update/batch + autopilot dispatch + create-project squad picker; (2) **squad routing resilience** — member-task failure re-engages the leader (`maybeReTriggerSquadLeaderOnMemberFailure` in service/task.go) instead of stalling; (3) **generic QA gate** — slice_action.go `run_qa`/`run_ci` de-SD-boxed (no sd-qa-process/btx-/dev-box), project-configurable smoke (`project.settings.qa_smoke_cmd/url`), frontend editor-run-qa now delegates to the backend slice-action (single source); (4) **runtime fallback** — `agent.fallback_runtime_id` (migration 127) + UpdateAgent API + `maybeFailoverToFallbackRuntime` re-dispatches a quota/rate-limit failure onto the fallback runtime. **Live finding:** the whole sd-bridge squad runs on ONE Claude account that hit its org weekly limit (resets ~19:00 Tashkent) → entire pipeline dead-stops; only GLM(Agora)/Gemini agents survived. Daemon UNCHANGED (no daemon swap). NEXT: deploy (migrate 126/127 + rebuild backend/frontend) → live-test full squad with the user's 2nd Claude account. See memory `agora-pipeline-fixes`.
- **2026-06-28 r2:** This continuation doc created (user wants per-run change+plan capture for cross-account handoff). No code change this run.
- **2026-06-27 r1:** QA Phase 1 — Gates panel (`editor-gates.tsx` + `MergeReadiness` type/client) + Run-QA button (`editor-run-qa.tsx`). Deployed frontend (web 200). Next: live-test Run-QA → Phase 2.
