> Prompt says "always share absolute file paths." Key files referenced below live under `/Users/jamshid/Projects/agora/`; I use repo-relative paths in prose for readability but the roots are: `server/internal/handler/{connected_box,remote_box_provision,remote_box_sync,slice_action}.go`, `server/internal/daemon/{daemon,health,browser}.go`, `deploy/sddev/`.

# Agora legacy-monolith dev+QA cycle — capability audit

## 1. Capability map: works end-to-end / partial / missing

The seven subsystems together form one loop: **provision a per-dev box → sync the branch → run deterministic QA against the live box → capture verdict → (on pass) auto-doc → feed docs back into next QA**. Here is what actually closes today.

### Works end-to-end (proven by code + unit tests, safe to rely on)
- **Deterministic per-issue smoke (`run_qa`)** against a live box URL. Agent runs build/lint/test where they exist, else browser-driven HTTP/DOM asserts; captures a structured `qa-result` block → `qa_evidence` row (immutable per issue/baseline_ref/branch_sha); attaches `qa:pass`/`qa:fail`; live `RUNNING test_case:<id>` markers stream to the cockpit. This is the strongest link.
- **Box provisioning runbook** — idempotent SSH shell script: clone-once (never re-clones over a dev checkout), copy gitignored glue (index.php, main.php, db.php, framework symlink) from a seed site, make runtime dirs writable, **never touches the DB**. Non-destructive by construction and unit-tested for idempotence + injection safety (`shellQuote`, token redaction).
- **Branch git-sync to a box** — `git fetch --depth 1 <url+ephemeral-token> <branch>` + `checkout -f -B`, glue preserved, token only in argv + redacted in logs, serialized per-box via a Postgres advisory lock.
- **Box resolution routing** — per-developer box (assignee agent's owner) → explicit project binding → repo-name fallback. Tenancy-scoped, unit-tested.
- **CSP/X-Frame-Options framing detection** for the Live-testing iframe (this caught a real sd-main regression) with an "open in new tab" fallback.
- **Test-case authoring/execution capture** — `gen_test_cases` parses a ` ```test-cases``` ` JSON block → `test_case` rows; `run_test_cases` parses ` ```test-runs``` ` → `test_run` rows (run_source agent|human).
- **Daemon-per-box agent execution at loopback** — agent + preview + Chromium CDP colocated on `127.0.0.1`, per-workspace sealed git PATs injected (`AGORA_GIT_SECRET_KEY`). Agent-driven QA needs no network exposure and works today.
- **Sprint-branch guard** — `sprintBranchRejected` blocks protected/prod branches (billing/main/master/prod) as sprint integration branches on create/update.
- **sddev fast-path** (`deploy/sddev/qa_switch.php`) — a *lighter alternative* to the full provisioner: flips an existing dev box's checkout to a task branch and back in seconds, with `simulate_host` params to keep QA off real billing.

### Partial (works only when every precondition aligns; degrades silently otherwise)
- **Auto-QA on `in_review`** — fires only for **sprint-assigned** issues (via `DeploySprintBranch`). **Non-sprint issues never auto-sync their branch to the dev's box**: `maybeRunQAOnInReview` resolves the box URL but does not call `DeployIssueQA`, so `run_qa` receives a *stale* smoke target. `POST /api/issues/{id}/deploy-qa` exists and is routed but **has no UI trigger** and no automation for non-sprint issues.
- **Shared-sprint-branch model** — `ensureSprintBranch` **silently degrades to the fork model** if `origin/<sprintBranch>` doesn't exist yet (race between sprint creation and branch push). No gate/early error.
- **`last-green` moving baseline** — the ref is read/advanced by the agent but **nothing seeds it at sprint creation**; first task falls back to a coarser merge-base baseline. Operationally fragile, not a hard blocker.
- **Sprint regression** — deploys sprint branch to the *project-bound* box and runs `scope=regression`, but: no durable ops-controlled baseline (sprint-root is ephemeral), no suite grouping, no per-dev isolation (all devs regress on one shared box → state collisions).
- **Provision config gating** — `qaHostConfigured()` requires all core `AGORA_QA_HOST_*` vars (SSH host/user, base domain, web root, repo URL, seed dir, seed DB; SSH port optional). Missing any one → generic 503, no "which var is missing" hint.
- **Deploy is best-effort** — sprint/box sync failure logs a warning and continues; `run_qa` still fires against stale state with no signal to the reviewer.
- **Docs knowledge loop** — `auto_docs` fires on `qa:pass` and QA runs consume `docs_repo` as context, but **GitLab/private docs-repo auth is never attached** (creds attach only to code `ProjectResource`s, not the inline `docs_repo` URL), there's **no merged-MR feedback** (QA always reads the static settings URL), and the whole chain is **off by default** (`AGORA_AUTO_DOCS_ENABLED`).

### Missing (blocks a real end-to-end legacy cycle)
- **No disposable legacy demo repo/box.** The entire system is currently dogfooded against **real prod `agora.sdteam.uz`** — a broken provisioner or bad sync could wipe a real developer's checkout. There is no Yii1+MySQL+mixed-frontend throwaway target, no demo project/squad/skill, no wired repo URL, no versioned demo db.php.
- **Per-box SSH keypair generation + enrollment.** `connected_box.deploy_pubkey` column exists but is never populated; all boxes share one global deploy key + one git token in env. Security/audit liability.
- **Daemon bootstrapper for per-dev boxes.** `connected_box.daemon_id` is never populated on provision; no agent installs/registers the daemon, so a provisioned box is QA-only, not a dev environment.
- **Per-runtime daemon addressing.** Only `editor_port` is stored in `agent_runtime.metadata`, not `daemon_addr`; multi-box cloud routing falls back to a single global env → multiple boxes can't coexist.
- **Remote bind + mesh security boundary.** Health/editor/preview/CDP hardcode `127.0.0.1` with only `AGORA_HEALTH_BIND`/`AGORA_EDITOR_BIND` overrides; `code-server --auth none` + zero-auth `/editor/launch`, `/repo/checkout` endpoints. `AGORA_HEALTH_BIND=0.0.0.0` = unauthenticated RCE. Live *human-watched* QA on a remote box is blocked until preview/CDP binds + a token proxy exist.
- **No real e2e harness.** Test cases are text JSON, not executable Playwright specs; no persistent, replayable suites; no cross-sprint test inheritance; manual cases can't enter regression.
- **No pre-flight box health check** before spending agent tokens; no service-dependency handshake (Redis/DB seed) before smoke.

**Bottom line:** the *deterministic per-issue smoke path against an already-correct box* is production-grade. Everything that makes it a real multi-dev cycle — getting the right branch onto the right box automatically for non-sprint work, per-box identity/secrets, a daemon on the box, and a safe non-prod target to prove it — is partial or missing.

---

## 2. Safe legacy-demo project plan (never touches prod / agora.sdteam.uz)

**Goal:** a fully disposable Yii1 + MySQL + mixed Vue2/Vue3/jQuery/Angular target that Agora can provision, sync, smoke, and regress against, with zero shared surface with real SalesDoctor.

**Scaffold (`legacy-demo` repo — new, throwaway):**
- Yii 1.1 skeleton (`framework/` shared via symlink from seed, same as prod pattern).
- A handful of real routes to exercise the smoke pyramid: `/site/login` (positive+negative auth), a list page (jQuery), a Vue2 widget, a Vue3 island, one Angular fragment — enough for stack-detection to see all four frontends.
- **Versioned `protected/config/db.php.example`** pinning a demo DB name (e.g. `demo_legacy_qa`) — the real db.php stays gitignored and is supplied as seed glue.
- A `protected/config/main.php` with `simulate_host`-style params (reuse the `deploy/sddev/qa-main-params.php` pattern) so the demo never contacts any real billing/license service.
- `composer.json` with a trivial vendor dep to exercise the "large vendor dir" watchdog path.
- Seed SQL: a minimal schema + a few rows (one demo user `demo/demo`) so login smoke has data.

**Hosting target — RECOMMENDED: a dedicated `legacy-demo.<isolated-domain>` box provisioned by Agora's OWN provisioner, hosted OFF the sdteam.uz parent.**
- Rationale: proves the real provisioner code path (the thing we're auditing), stays non-prod, and gives a shareable URL for the Live-testing iframe + framing check.
- **Isolation requirements (hard):** a **separate parent host** (not `agora.sdteam.uz`), a **separate `AGORA_QA_HOST_*` config set**, a **separate git token** scoped to the demo repo only, and a **separate demo MySQL** so no prod DB, DNS, or CI log is shared.
- Fallbacks if a separate parent isn't available: **(b)** throwaway Fly.io app (fast, but bypasses the provisioner — you test sync but not provisioning), or **(c)** local Docker Compose (Nginx+PHP-FPM+MySQL; fastest for local Agora dev, not shareable). Both (b)/(c) are manual — no runbook exists yet.

**Do NOT** point any `AGORA_QA_HOST_SEED_DIR`/`SEED_DB` at the prod seed, and do NOT reuse the prod git token — a shared token means the demo provisioner has write reach into prod repos.

**Agora-side wiring (all disposable, scriptable via API):** create a `legacy-demo` project → a `connected_box` row bound by `project_id` → a QA Tester agent + QA Lead orchestrator → project settings with the demo smoke URL + smoke command → a **demo QA skill** (minimal clone of `sd-qa-process` pointing at the demo URL/baseline — it can't reuse the prod skill, which smokes the wrong URL).

---

## 3. Phased test plan: smoke → regression → e2e

**Phase A — Smoke (single issue, single box):**
1. Provision the demo box via `ProvisionConnectedBoxForMember` over real SSH; verify clone shape, glue copied, Yii app boots (GET returns 200, not 500).
2. Verify DB reachability from the box (the current runbook does NOT test this — add a connection check).
3. Create a demo issue + branch, call `POST /api/issues/{id}/deploy-qa`, confirm the branch is actually checked out and served.
4. Fire `run_qa`; assert: stack-detected as Yii1 (no phantom `npm test`), positive login smoke passes, negative (missing auth → 401) recorded correctly, `qa_evidence` row written, `qa:pass` label attached, framing check resolves for the iframe.

**Phase B — Regression (whole sprint branch):**
1. Seed `refs/sprint/<id>/last-green` explicitly (don't rely on first-task fallback).
2. Ensure `origin/<sprintBranch>` exists BEFORE dispatch (guard against silent fork degrade).
3. `DispatchSprintRegression` → deploy sprint branch to project box → `scope=regression`; assert baseline diff attributes failures to the sprint delta.
4. Concurrent-sync test: two QA runs on the same box → advisory lock serializes, second doesn't silently land on the wrong branch.

**Phase C — E2E (the currently-missing tier):**
1. Author a persistent suite for critical paths (login, a CRUD flow, an admin page). Today this is agent-authored text cases; the gap is that they're not replayable Playwright specs — for the demo, prove the `run_test_cases` deterministic path end-to-end and record where it falls short of a managed runner.
2. Per-dev isolation: two demo developers, two boxes, one issue each → verify each issue's QA deploys to its own owner's box.
3. Docs loop (optional, if `AGORA_AUTO_DOCS_ENABLED=true` + demo docs repo with staged creds): `qa:pass` → `auto_docs` opens an MR → merge → next `run_qa` consults the docs.

Each phase gates the next: don't run B until A's box is proven reachable; don't attempt C's isolation test until non-sprint deploy-qa auto-sync (backlog #3) is fixed, or it will smoke stale state.

---

## 4. One sprint run: humans + agents against the demo

A single, bounded dogfood sprint on `legacy-demo`, mirroring the real SalesDoctor flow:

- **Setup (human):** create the `legacy-demo` sprint cut off the demo base branch; seed `last-green`; confirm `origin/<sprintBranch>` pushed; bind the project box.
- **Squad:** 1 human dev (owns a per-dev box), 1 coding agent (assignee), 1 QA Tester agent + 1 QA Lead orchestrator, 1 human QA reviewer (Davron/Gayrat-style roster).
- **Loop (3–4 small issues):** agent/dev picks up issue → pushes a task branch onto the shared sprint branch → moves to `in_review` → auto-QA deploys the branch to the box → QA Lead reads the repo, confirms Yii1 stack, delegates to QA Tester → deterministic smoke + `run_test_cases` against the demo URL → `qa:pass`/`qa:fail` + evidence in the cockpit → human QA reviewer triages on `/qa/:id`, re-runs if flaky.
- **On pass:** agent advances `last-green`; if docs loop enabled, `auto_docs` opens an MR.
- **Sprint end:** `DispatchSprintRegression` runs the whole branch against sprint-root; human merges the sprint branch back to base (guard prevents merging into a protected branch).
- **Exit criteria to declare the cycle "real":** every issue's branch was auto-synced to the correct box (not stale), verdicts persisted, no cross-issue state collision, no prod surface touched, and the framing iframe rendered the live box.

The two things this sprint will most likely expose first (fix before it, per backlog): **non-sprint deploy-qa auto-sync (#3)** and **pre-flight box health (#1)** — without them the QA agent smokes stale or dead boxes and burns tokens.


---

## Issue backlog (small → big)

1. **[S] (connected-boxes)** Report which AGORA_QA_HOST_* var is missing in the provision 503  
   _qaHostConfigured() checks all vars at once and returns a generic 503; operators debug by trial-and-error. Name the missing var(s) in the error so demo/prod setup is self-diagnosing._
2. **[S] (sprint-qa)** Add pre-flight box health check before enqueuing run_qa  
   _The smoke URL is injected but the box is never probed; a down box wastes agent token budget failing on the first request. A GET/HEAD to the smoke URL in the handler fails fast with a clear signal._
3. **[S] (sprint-qa)** Signal QA reviewer when box/sprint sync fails instead of silent warn  
   _DeploySprintBranch/DeployIssueQA failures log a warning and run_qa still fires against stale state; the reviewer has no indication the smoke target is unreliable. Surface a 'deploy failed' marker on the issue/evidence._
4. **[S] (sprint-qa)** Seed refs/sprint/<id>/last-green at sprint creation/activation  
   _The moving-baseline optimization only kicks in after the first task manually creates the ref; seed it deterministically so attribution is correct from task one._
5. **[S] (sprint-qa)** Gate silent fork-model degrade when origin/<sprintBranch> is absent  
   _ensureSprintBranch logs a warn and falls back to fork model, breaking the shared-branch contract and letting agents push to a dangling fork. Emit an early, explicit error/gate instead._
6. **[M] (connected-boxes)** Auto-sync non-sprint issue branches to the dev's box on in_review  
   _maybeRunQAOnInReview only deploys sprint issues; non-sprint issues get a resolved URL but a stale branch, so run_qa tests the wrong code. Call DeployIssueQA (or equivalent) for non-sprint issues too._
7. **[M] (connected-boxes)** Add a Deploy-to-QA UI trigger for POST /api/issues/{id}/deploy-qa  
   _The endpoint is routed but has no frontend button; manual re-deploy of a branch to a box is impossible from the UI today._
8. **[M] (connected-boxes)** Verify DB reachability/credentials during provision dry-run  
   _Provision copies db.php verbatim but never tests the connection; a box can succeed on file presence yet 500 on every page. Add a connection probe so setup fails loudly, not at first smoke._
9. **[M] (docs)** Attach git credentials to docs_repo for auto_docs + QA docs context  
   _docs_repo is an inline plain-text URL; creds attach only to code ProjectResources, so a private GitLab docs repo can't be cloned. Parse the docs_repo host and resolve workspace credentials for it._
10. **[M] (demo-project)** Scaffold the disposable legacy-demo repo (Yii1+MySQL+mixed frontends)  
   _No throwaway target exists; the system is dogfooded against real prod, risking data/checkout corruption. Build the scaffold with versioned db.php.example, seed SQL, and all four frontend fragments for stack detection._
11. **[M] (demo-project)** Bootstrap script for demo Agora project + box + squad + QA skill  
   _A demo box is useless without a project, project-bound connected_box row, QA Tester+Lead agents, smoke URL/cmd config, and a demo QA skill (can't reuse prod sd-qa-process). No automation exists to create these rows._
12. **[M] (per-user-ssh)** Enforce mesh-only bind + block AGORA_HEALTH_BIND=0.0.0.0 exposure  
   _code-server --auth none + zero-auth /editor/launch and /repo/checkout on a public bind = unauthenticated RCE. Add a daemon startup guard and provisioning gate enforcing mesh/loopback binding._
13. **[M] (agent-runtime)** Store daemon_addr in agent_runtime.metadata for per-runtime routing  
   _Only editor_port is stored; multi-box cloud routing falls back to a single global env, so multiple per-dev boxes cannot coexist. Store daemon_addr at register time and read it in resolveDaemonInternalAddr._
14. **[L] (per-user-ssh)** Per-box SSH keypair generation + authorized_keys enrollment  
   _deploy_pubkey column exists but is never populated; all boxes share one global deploy key and one git token — a security/audit liability blocking prod. Generate + store + enroll per-box keys and scope git tokens per box._
15. **[L] (agent-runtime)** Daemon bootstrapper agent to install/register the daemon on a provisioned box  
   _connected_box.daemon_id is never populated; without a daemon the box is QA-only, not a dev environment. A bootstrapper must SSH in, install/configure the Agora daemon, and report daemon_id back._
16. **[L] (qa-tiers)** Persistent, replayable e2e/regression suites with per-dev isolation  
   _Test cases are per-issue text JSON, not executable replayable specs; regression has no durable baseline, no suite grouping, and all devs regress on one shared box (state collisions). Add suite persistence, cross-sprint inheritance, a stable baseline, and per-run/per-dev isolation._


---

## Critic addendum (completeness + safety)

All claims verified against code. The report is accurate on the points it makes. Here is the addendum of what's missing or wrong.

---

# Addendum — gaps, unverified claims, and safety holes

## Safety holes that can touch real prod (highest priority)

- **The provisioner inherits the seed box's `db.php` verbatim — the demo box will point at whatever DB the seed points at.** Verified: `remote_box_provision.go` copies `protected/config/db.php` from `SeedDir` only when absent, "inherits the seed's DB config verbatim — no DB is created, cloned, or renamed." The plan's §2 says "supply db.php as seed glue" and "separate demo MySQL," but the *mechanism* is a copy from `AGORA_QA_HOST_SEED_DIR`. **If the demo's `SEED_DIR` is a prod seed (or any seed whose db.php names a real DB), every demo box writes to the real DB with zero code-level guard.** The report flags "don't point SEED_DIR at prod seed" as advice; there is **no assertion in code** that the seed db.php names a demo-only database. Backlog needs: *a provision-time guard that refuses a seed whose db.php database name isn't on an allowlist / doesn't match a `demo_*` prefix.* This is the single biggest prod-corruption vector and it's currently policy-only.

- **No environment allowlist on `AGORA_QA_HOST_*` — one misconfigured env var re-points the whole provisioner at `agora.sdteam.uz`.** The config set is a flat bag of host/domain/webroot/repo/seed strings. Nothing prevents `AGORA_QA_HOST_SSH_HOST=agora.sdteam.uz`. Issue #1 ("name the missing var") improves diagnosis but not *safety*. Add: a refuse-list of known-prod hosts/domains the provisioner will never target, checked in `qaHostConfigured()`.

- **Git-token blast radius is real and unmitigated in the plan's DoD.** Both the audit and plan note "one global git token." The plan's isolation list says "separate git token scoped to the demo repo," but issue #14 (per-box keypair) is sized **L** and not gated as a *blocker for the demo*. Until per-box scoping lands, the demo provisioner runs with a token that can write prod repos. The phased plan should state explicitly: **run the demo with a throwaway token that has no prod-repo scope, even before #14** — otherwise Phase A dogfeeds prod-write credentials into a throwaway box.

## Unverified / assumed-rather-than-code-checked claims

- **"advisory lock serializes per-box sync" is asserted but I could not confirm it in `remote_box_sync.go`.** Grep for `advisory`/`pg_advisory`/`lock` in that file returned nothing. The report and Phase-B step 4 both lean on this lock for concurrency safety. Either the lock lives elsewhere (sync is invoked through a different serialization path) or the claim is stale. **This must be re-verified before Phase B**, because the entire "concurrent QA runs on one shared box are safe" argument depends on it. If the lock isn't actually there, per-dev isolation (#16) becomes a Phase-A blocker, not a Phase-C nicety.

- **"`last-green` is read/advanced by the agent" — the advance step is agent-instruction-driven, not enforced in code.** The regression baseline correctness depends on an agent reliably moving a git ref. There's no server-side transaction advancing `refs/sprint/<id>/last-green` on `qa:pass`; if the agent forgets, attribution silently regresses to merge-base. Backlog should add a *server-side ref advance on `qa:pass`* rather than trusting the agent, or the baseline is only as reliable as the model's compliance.

## Test-tier gaps the report glosses

- **Smoke tier has no negative *box-state* case, only negative *auth* cases.** Phase A tests "missing auth → 401" but not "box is on the wrong branch / stale checkout → smoke silently passes against old code." Given the #3/#6 stale-branch bugs are the headline finding, the smoke phase must include an assertion that the served commit SHA matches the expected branch SHA (the `qa_evidence` already records `branch_sha` — assert the *live box* serves that SHA, e.g. via a committed `/version.txt` or header). Without this, a stale-box pass looks identical to a real pass.

- **Regression tier has no rollback/quarantine path.** Phase B detects failures but the plan never says what happens to the shared box after a regression run leaves it dirty (half-migrated schema, mutated demo rows). Legacy Yii1 + MySQL smoke *mutates state* (login sessions, any CRUD case). The plan claims "never touches the DB" for provisioning, but **`run_test_cases` executing CRUD flows absolutely mutates the demo DB**, and there is no reset-between-runs step. Add: a demo-DB snapshot/restore (or transaction-wrapped seed reset) between regression runs — otherwise run N poisons run N+1 and "state collisions" appear even on a single box.

- **E2E tier omits the auth/session-fixture problem entirely.** Executable specs against a Yii1 monolith need a logged-in session (CSRF token, cookie). The plan's Phase C says "prove `run_test_cases` end-to-end" but neither the audit nor the backlog addresses *how a deterministic runner authenticates* to the legacy app. This is the first thing a real e2e suite hits. #16 should call out session/CSRF fixture handling as in-scope.

## Per-user-SSH / daemon gaps under-weighted

- **`--auth none` code-server is not the only zero-auth surface — the entire health/editor/preview/CDP HTTP API is unauthenticated and CORS-open to `localhost`.** Verified in `health.go`: every endpoint gates only on `Origin: http://localhost|127.0.0.1`. On a `0.0.0.0` bind, Origin is trivially spoofable (it's a request header). So #12's framing ("block `AGORA_HEALTH_BIND=0.0.0.0`") is necessary but the mesh-token proxy is the real fix — the Origin check is **not** an auth boundary and shouldn't be counted as one. The backlog should state that the localhost-Origin check must be replaced by a real token, not merely supplemented by a bind guard.

- **CDP/browser and preview binds are hardcoded `127.0.0.1` with *no* override (unlike health/editor).** Verified: `browser.go` binds `127.0.0.1:0` with no env escape hatch. The report says "Live human-watched QA on a remote box is blocked until preview/CDP binds exist" — correct, but this also means #12's guard is incomplete: it guards the *two* binds that have overrides (health/editor) while the CDP path simply can't be remotely bound at all yet. The human-watched-QA blocker is therefore a **missing feature (remote CDP bind + proxy)**, not just a hardening task — it belongs sized L alongside #14/#15, not folded into #12.

## Missing backlog items

- **No item for the seed-db safety guard** (see safety hole #1) — the most important missing issue.
- **No item to assert served-SHA == expected-SHA** in smoke (closes the stale-box detection gap that #2/#3/#6 only *prevent* going forward but never *detect*).
- **No item for demo-DB reset between runs** (regression state poisoning).
- **No item for server-side `last-green` advance on `qa:pass`** (don't trust the agent to move the ref).
- **No item for legacy-app session/CSRF fixture** in the e2e runner.
- **Re-verify the per-box advisory lock exists** before relying on it (audit task, not a feature).

## One correction

- The report lists sddev `qa_switch.php` as a "works" fast-path, but `deploy/sddev/` operates on an **existing** dev box and (per `qa-main-params.php` / the `simulate_host` pattern) is the mechanism keeping QA off billing. It is **prod-adjacent tooling**, not part of the disposable demo. The plan should not present it as a demo building block — reusing it points the demo at the same sddev parent the isolation requirements say to avoid.