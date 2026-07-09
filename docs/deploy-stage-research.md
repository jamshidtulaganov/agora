# Deploy Stage Research — what "Deploy" should be in the SDLC stage cockpit

**Status:** research, not a build plan. No code changes in this doc.
**Companion:** `docs/sdlc-stage-cockpit-plan.md` (the cockpit this stage lives inside).
**Question:** for a 2-10 person AI-native team whose stacks vary (PHP monoliths
git-synced to boxes, Vue SPAs, Go services, Fly.io apps), what should the
DEPLOY stage of the agent-driven SDLC cycle actually *be* — not "what exists
today," which is a QA-box git-sync wearing a deploy costume.

---

## TL;DR

- Today "Deploy" in Agora is **QA-box git-sync**, not deploy. `DeployIssueQA`
  / `DeploySprintBranch` (`server/internal/handler/connected_box.go:792,861`)
  push a branch onto a `connected_box` for pre-merge QA. Nothing in the
  codebase deploys a merged branch anywhere — the trail goes cold at "PR
  squash-merged into the sprint branch" (`slice_action.go:1654-1675`,
  `sprint.go:47-60`). There is no staging/production concept in the schema at
  all.
- The stepper already has the right shape for a real deploy signal
  (`hasDeployTarget` / `deploySynced` in `packages/core/issues/stage.ts:59-61`)
  but `deploySynced` is **wired to `undefined` everywhere** — see the TODO at
  `packages/views/issues/components/use-stage-pipeline.ts:100-104`. This is
  the single biggest quick win: give that boolean a real source and the
  stepper tells the truth for the first time.
- Agora already has every primitive a real Deploy stage needs, just not
  connected to "deploy": a human-actor gate (`RequireHumanActor`), a
  label-triggered agent-instruction pipeline (`auto_docs`), an
  evidence-persistence pattern (`qa_evidence`), a project-setting-scoped
  target resolution chain (`qa_smoke_url`/`qa_smoke_cmd`), and a daemon that
  can already SSH/git-sync/run arbitrary commands on a box. **Tier 2 (a
  project-level `deploy_cmd` an agent runs on a daemon) covers the highest
  fraction of 2-10 person team stacks for the least engineering cost** and
  should ship before any provider-specific integration (Tier 3).
- Recommended signal: a **`deploy_event` table**, structurally identical to
  `qa_evidence` (same immutable-append, same `source` provenance column added
  in a one-line follow-up migration, same "latest row wins" read pattern).
  Minimal, cheap, on-model for this codebase.
- Post-deploy verification reuses the `run_qa` machinery almost verbatim —
  same slice-action instruction pipeline, same DOM-based smoke discipline —
  pointed at the deploy target's URL instead of a QA box, with one new
  constraint: **no test-data mutation**. That's a prompt-template distinction
  today, not a code-enforced one, and this doc argues it needs to become code
  enforced before Tier 2 ships to production targets.
- Production deploys must be `RequireHumanActor`-gated (`server/internal/
  handler/actor_guards.go:96`) — the same middleware that already gates
  instance-config writes and PAT management. This is a `r.With(...)` one-liner
  on the new route, not new infrastructure.
- Release notes: clone the `auto_docs` trigger chain (label → flag → project
  setting → agent resolution → templated instruction → `@mention` comment →
  `triggerTasksForComment`) verbatim, firing on deploy success instead of
  `qa:pass`.
- Rollback for Tier 1/2: re-run the deploy command/git-sync against the
  previous tag/SHA — no new mechanism, just recording *which* SHA was last
  known-good (the `deploy_event` row again).
- Recommend 4 phases, roughly stepper-signal → command-runner → verification
  → release notes, deferring provider integrations (GitHub Deployments API,
  Fly Machines API) to a Tier 3 that few of Agora's actual customers need on
  day one.

---

## 1. Where the Deploy stage stands today (codebase gap map)

### 1.1 What "deploy" currently means: QA-box git-sync

`connected_box` (`server/migrations/128_connected_box.up.sql:13-28`) models
**a developer's own remote dev/QA server** — SSH host/user/port, a
`deploy_pubkey` for the control-plane keypair, a `daemon_id` linking to the
box's self-host daemon, `status`/`last_error`. It is explicitly *not* a
staging or production environment record; the migration's own comment frames
it as "a developer's own remote dev server."

- `performBoxSync` (`connected_box.go:653-701`) is the single git-sync
  primitive: takes a `pg_advisory_xact_lock` per box (serialized so two
  concurrent syncs can't interleave a half-checked-out tree, `connected_box.go
  :654-682`), runs `syncBoxBranch` (fetch + checkout over SSH), and records
  `status`/`last_error`/`last_branch` on the row.
- `DeployIssueQA` (`connected_box.go:792-841`) resolves the QA box bound to
  an **issue's** project and syncs the given branch onto it, so `run_qa` (see
  §1.3) can smoke the branch under review.
- `DeploySprintBranch` (`connected_box.go:861-917`) resolves the **sprint's**
  project → its explicitly-bound, ownerless/shared box (never a developer's
  personal box, to avoid clobbering their in-flight test session,
  `connected_box.go:888-895`) and syncs `SprintBranchFor(sprint)` — the
  sprint-end regression target.
- `DeploySprintQA` (`connected_box.go:1163-1208`) is the sprint-level HTTP
  entry point calling `DeploySprintBranch`.

All three of these deploy **a branch onto a QA box for testing before merge**.
None of them deploy anything **after** merge, and none of them touch anything
that could be called staging or production.

### 1.2 The gap: code merged → running in prod is a dead end

`maybeMergeOnQAPass` (`slice_action.go:1577-1676`) is the sprint-PR-mode merge
gate: when `AGORA_SPRINT_PR_MODE` is on and a task's PR gets `qa:pass`, either
(a) a human is asked to review+merge (default), or (b) the squad **lead**
auto-merges via `gh pr merge <pr> --squash --delete-branch` **into the sprint
branch** — the code is explicit that this "Never target the repository's
main/default branch" (`slice_action.go:1654-1675`).

`protectedSprintBranches()` (`sprint.go:53-60`) hard-blocks `master`/`main`/
`production`/`prod` from ever being used *as* a sprint's own integration
branch — a second independent guard against agents ever writing onto prod
directly. `sprint.go:47-49` states the intended real-world flow outright:
sprint branches are cut from the prod branch and "merged back to prod by a
human at sprint end." For GitLab projects, `gitlabBaseBranch()`
(`slice_action.go:2245-2254`) documents the same idea from the other
direction: agent MRs target a staging branch specifically "so their work does
NOT auto-deploy to prod every iteration (main → prod via deploy:main)" — i.e.
the actual main→prod pipeline is assumed to be **external** to Agora, living
in the target repo's own CI, invisible to Agora entirely.

**Net: nothing in Go code deploys a merged branch anywhere, tags a release,
or notifies anyone past "PR merged into sprint branch."** The stage-cockpit
plan's own model gap note in `docs/sdlc-stage-cockpit-plan.md:111-117`
undersells this — it frames the deploy gap as "no per-issue deploy signal,"
but the real gap is one level up: there is no deploy *event* to signal, for
any issue, ever, in the current backend.

### 1.3 What the frontend does with what exists

`packages/core/issues/stage.ts:189-203` (`deriveDeployStage`) is already
correctly shaped: `skipped` when the project has no deploy target, `passed`
when `deploySynced === true`, `running` while a deploy-attributed task is in
flight, else `pending`. The type contract (`StagePipelineInput.deploySynced?:
boolean`, `stage.ts:61`) anticipated a real backend signal.

The wiring never arrived. `use-stage-pipeline.ts:97-106` computes
`hasDeployTarget` (real: reuses the exact bound-box lookup `EditorDeployQA`
already runs, gated by `remoteBoxesEnabled`) but leaves `deploySynced`
**undefined**, with an explicit TODO:

> `deploySynced` has no client-side signal yet: `ConnectedBox` only tracks
> the project's last-synced branch, not "synced to THIS issue's branch" —
> left undefined ... so the stage renders "pending" rather than a false
> "passed".

That is the exact bug named in the research brief: the stage shows "pending"
whenever a box is bound (because `hasDeployTarget` is true and `deploySynced`
is never true), and "skipped" whenever it isn't. It is never `passed`,
`failed`, or `running` for any issue that has ever existed. `deploy-lens.tsx`
(`packages/views/issues/components/deploy-lens.tsx:21-86`) is a thin re-mount
of box info + `EditorDeployQA` — no verdict, no history, no "last deployed
when/by whom."

### 1.4 The QA gate machinery — the reusable part

`run_qa` is the closest thing Agora has to a verification pipeline, and it is
genuinely reusable for deploy verification:

- **Trigger:** manual via `CreateSliceAction` (`slice_action.go:2796`, kind
  `run_qa`) or automatic via `maybeRunQAOnInReview` (`slice_action.go:1853`),
  gated by `AGORA_AUTO_QA_ENABLED`.
- **Instruction:** the verbatim procedure lives in
  `server/internal/handler/slice_action_templates/run_qa.md`, loaded via
  `sliceActionTemplate("run_qa")` (`slice_action.go:90-99`, `//go:embed`).
  It mandates baseline-vs-branch diffing, deterministic exit-code checks, a
  DOM/accessibility-tree smoke (explicitly never vision-judged screenshots),
  and a terminal ` ```qa-result``` ` JSON block. It ends: **"Do NOT merge
  anything — your verdict is advisory."**
- **Smoke target resolution** is already layered and already looks like an
  environment-selection chain, just not labeled that way: dev's own running
  app → `local_directory` on-daemon target → developer's per-dev QA box
  (`devBoxSmokeURL`, `connected_box.go:535`) → project's static
  `qa_smoke_url` in `project.settings` (`connected_box.go:1306-1311`,
  `slice_action.go:400-401`). Whichever resolves is appended to the
  instruction as `SMOKE TARGET: ... It OVERRIDES any project smoke url
  below.`
- **Evidence persistence:** `server/pkg/db/queries/qa_evidence.sql` —
  `UpsertQAEvidence` (immutable per `(issue_id, baseline_ref, branch_sha)`,
  a re-run on an advanced SHA writes a new row rather than mutating), plus
  `GetLatestQAEvidenceForIssue` and `ListQAEvidenceSummariesForIssues` (one
  indexed read instead of re-parsing comment history). The `source` column
  (`server/migrations/149_qa_evidence_source.up.sql`) was added as a
  **single, tiny follow-up migration** — `ALTER TABLE qa_evidence ADD COLUMN
  source text NOT NULL DEFAULT 'agent'` — to distinguish agent/human/watchdog
  provenance after the fact. This is the exact shape of migration cost this
  doc wants for a `deploy_event` table (see §3.3).
- **Live browser attach:** `/browser/proxy/{token}/*` → `ProxyBrowser`
  (`server/internal/handler/browser_proxy.go:151`), token-scoped
  (`registerBrowserTarget`, 8h TTL) and path-allowlisted
  (`browserProxyPathAllowed`, `browser_proxy.go:133-146`) to editor/preview/
  browser/test/changes/open-pr/discard paths only — explicitly excludes
  `/editor/launch`, repo checkout, and the updater surface.

**Pointing this at staging/prod is mechanically trivial** — `qa_smoke_url` is
just a URL; nothing in the resolution chain distinguishes a QA box from
production semantically. **The gap is entirely on the guard side**: grepping
`slice_action.go` and `connected_box.go` for "read-only"/"no-mutation" only
turns up matches for the *design/manifest/audit* actions ("Inspect the
repository READ-ONLY (do not push, do not open a PR)" — `slice_action.go:211,
237,243,265`), never for `run_qa`'s execution target. Nothing today stops
`run_qa` from filling forms and mutating real data if pointed at a live
production URL. `qaHostCheckTarget`/`qaHostCheckDBPrefix`
(`server/internal/handler/remote_box_sync.go:221-287`, used by
`ProvisionConnectedBoxForMember`) is the one related safety rail, but it
guards **box provisioning** (SSH host/DB-name safety), is opt-in and
default-off, and doesn't touch smoke-target resolution. **The no-prod-testing
rule is today purely a documented human/CLAUDE.md convention, not enforced in
code anywhere.** Any Deploy stage that reuses `run_qa`-style automation
against staging/prod must close this gap first — see §3.4.

### 1.5 The reusable automation precedent: `auto_docs`

`auto_docs` (`slice_action.go:684-975`) is the closest existing analog to
"an agent does a scoped write triggered by a pipeline event, gated by a
feature flag and a project setting." Full chain:

1. **Trigger:** a label-attach event calls `maybeAutoDocsOnLabel(ctx, issue,
   labelName, userID)` from three call sites — `comment.go:1055`,
   `lark_card_action.go:98`, `label.go:400` — each fired in a detached
   goroutine (best-effort, doesn't block the label write).
2. **Gate** (`slice_action.go:943-949`): no-op unless `AGORA_AUTO_DOCS_ENABLED`
   (default off, `slice_action.go:758-760`, cataloged in
   `server/internal/config/registry.go:54`) **and** the label is exactly
   `qa:pass`.
3. **Target check** (`951-953`): reads `project.settings.docs_repo`
   (`slice_action.go:693`); empty → silent no-op.
4. **Agent resolution** (`916-934`, `resolveAutoDocsAgent`): project's
   configured `docs_agent` (`project.settings.docs_agent`, `slice_action.go
   :808`) → else the issue's agent assignee → else the `qa:pass`-setting
   user's own agent.
5. **Instruction:** `buildSliceInstruction` loads `slice_action_templates/
   auto_docs.md` + `docsRepoInstruction` (names the repo; appends a GitLab
   MR push-option flow when the docs host is GitLab) + QA manifest context.
6. **Delivery:** posted as an `@mention`-prefixed comment
   (`agentProtocolMarker("auto_docs") + "[@agent](mention://agent/<id>) " +
   instruction`, `slice_action.go:959`), then `h.triggerTasksForComment(...)`
   (`973`) queues exactly one agent task through the same canonical
   comment-trigger path every slice action uses.

This is a direct, low-risk template for "auto release-notes on deploy
success" (§3.6) and arguably for "auto deploy-smoke on deploy success" (§3.4).

### 1.6 Config, provisioning, and CI/CD substrate

- **`instance_config`** (`server/migrations/153_instance_config.up.sql`) is a
  **global** key/value override table; the catalog lives in
  `server/internal/config/registry.go:36-85` (categories: QA, Sprint,
  Automation, Remote boxes, Bitrix, Platform, Secrets). Precedence is DB
  override > env > default (`server/internal/config/store.go`). Adding a new
  flag is **one `config.Def{}` entry** — no migration — and it appears
  automatically in Settings → Configs, gated by `RequireHumanActor` on write
  (`router.go:620-621`). This is cheap for a *global* "deploy stage enabled"
  toggle; a *per-project* toggle needs a `project.settings` key instead,
  following the existing `qa_smoke_url`/`docs_repo` pattern (both are
  free-form JSONB keys on `project.settings`, no dedicated columns, no
  migration).
- **Project resource model:** the `project` table itself
  (`server/migrations/034_projects.up.sql:2-14`) has no deploy-related
  columns (`title, description, icon, status, lead_type, lead_id`).
  `project_resource` (`server/migrations/065_project_resources.up.sql:5-16`)
  is a polymorphic pointer table — `resource_type` + `resource_ref jsonb` —
  currently only populated with `"github_repo"` and `"local_directory"`
  (`server/internal/handler/project_resource.go:75-80`); "adding a new type
  requires zero schema changes" per its own comment. `connected_box` is the
  only thing that resembles a deploy *target*, and it's semantically locked
  to "one developer's personal QA box" (owner_id = developer). **There is no
  environment-kind discriminator anywhere in the schema** (dev/staging/prod
  is not a concept the database has ever heard of).
- **Remote-box provisioning:** `ProvisionConnectedBoxForMember`
  (`connected_box.go:213-356`) is gated by `remoteBoxesEnabled()` +
  `qaHostConfigured()`, runs two default-off safety rails before any SSH
  touch, supports a `DryRun` request field (`connected_box.go:190`) that
  returns the computed runbook without touching the host or writing a row
  (`298-301`) — a real "preview before you provision" gate. On a real run the
  DB row is created *before* the SSH script runs, so a failed provision still
  leaves an inspectable error row. Structurally reusable for a non-QA box,
  but would need a new discriminator (environment kind), not just a new box
  type.
- **Agora's own Fly.io deploy** (`deploy/fly/deploy.sh`) is **100% manual**:
  `bash deploy/fly/deploy.sh [db|backend|web|telegram|all]` run by hand after
  `fly auth login`. No workflow in `.github/workflows/*.yml` calls `flyctl` or
  this script. `.github/workflows/release.yml` triggers only on `v*.*.*` tag
  push and runs Go tests → GoReleaser (CLI → GitHub Releases + Homebrew) →
  multi-arch Docker image builds pushed to GHCR → Helm chart packaging — it
  **publishes artifacts, it does not deploy anything**. `.github/workflows/
  ci.yml` is build/typecheck/lint/test only.
- **Webhooks are inbound-only.** `HandleGitHubWebhook`
  (`server/internal/handler/github.go:572`) switches on `X-GitHub-Event` for
  `ping/installation/pull_request/check_suite` — no `deployment`/
  `deployment_status` case, and no outbound call to the GitHub Deployments
  API anywhere in the codebase. No outbound webhook dispatcher exists at all
  (confirms the prior finding in memory that Agora→Zapier needs outbound
  webhooks that were never built).
- **Worktree isolation is explicitly out of scope for this problem already.**
  `docs/local-worktree-isolation-design.md:141` states plainly: **"Auto
  merge-to-master + prod deploy (this task ends at an sd-platform PR)."**
  `docs/remote-boxes-spec.md` scopes itself to QA only (§8), phased plan
  stopping at "P4 productization/hardening" with no promote-to-production
  concept. So this research doc is not filling in a corner someone already
  planned — it's the first document to propose one.

---

## 2. External patterns worth stealing

- **GitHub Deployments API + Environments.** A `deployment` targets a ref +
  a named `environment` (default `production`; teams commonly add `staging`/
  `qa`). A `deployment_status` posts `pending/in_progress/success/failure/
  error` with an optional `description` and `log_url`. Setting a deployment's
  status to `success` auto-marks prior *non-production* deployments to the
  same environment `inactive` (transient-environment cleanup), unless
  `auto_inactive: false`. — [GitHub Docs: Deployments](https://docs.github.com/en/rest/deployments/deployments),
  [Deployment environments](https://docs.github.com/en/rest/deployments/environments),
  [Deployment statuses](https://docs.github.com/en/rest/deployments/statuses).
  **Relevance:** this is the right conceptual model for the stepper's
  `deploy_event` (below) even if Agora doesn't call the GitHub API on day
  one — "ref + environment + status + log_url" is exactly the row shape to
  design toward, so a Tier 3 GitHub integration is additive later rather than
  a rewrite.

- **Vercel preview → production promotion.** Every non-production push gets
  a live preview deployment; merging to the production branch triggers a
  real production deploy automatically, but a human can also **promote an
  already-built preview instantly with no rebuild** via a three-dot menu
  action — used for rollback (promote an older stable build), cherry-picking,
  or recovering from a broken auto-deploy. — [Promoting a preview deployment to production](https://vercel.com/docs/deployments/promote-preview-to-production),
  [Promoting Deployments](https://vercel.com/docs/deployments/promoting-a-deployment).
  **Relevance:** the same "promote, don't rebuild" idea maps onto Agora's
  git-sync deploys — `performBoxSync` is already idempotent and cheap to
  re-run against an older SHA, so "rollback" (§3.7) can literally be
  "re-run the same sync with a different ref," no new machinery.

- **Fly.io Machines API / rollback.** Fly rollback has no dedicated command —
  it's just `fly deploy --image <previous-release-image>` or `fly releases
  --image`; rolling back *is* deploying an older image. Blue/green is done by
  booting a cordoned "green" machine, health-checking it, then uncordoning and
  tearing down "blue." — [Fly Machines API](https://fly.io/docs/machines/api/machines-resource/),
  [Custom Deploy Workflows](https://fly.io/docs/blueprints/custom-deploy-workflows/),
  [Rollback Guide](https://fly.io/docs/blueprints/rollback-guide/).
  **Relevance:** validates the "rollback = redeploy a known-good ref, no
  special-case rollback code" design for both Tier 1 (git-sync) and Tier 3
  (Fly). Agora's own `deploy/fly/deploy.sh` already deploys by image; wiring
  the Machines API later for programmatic rollback is a bounded, well-trodden
  path.

- **GitOps-lite (ArgoCD/Flux) — deliberately not recommended for this
  product.** Flux/ArgoCD assume a Kubernetes control plane and a
  reconciliation loop; both sources agree they're overkill for small teams
  without that substrate. — [ArgoCD alternatives 2026 (Bunnyshell)](https://www.bunnyshell.com/comparisons/argocd-alternatives/),
  [GitOps tools for platform engineers (Northflank)](https://northflank.com/blog/gitops-tools).
  **Relevance:** Agora's actual target customers (PHP monolith on a box, Vue
  SPA, small Go service) are not running Kubernetes. A command-runner tier
  (Tier 2, below) is the honest equivalent of "GitOps-lite" for this
  audience — a declared command + a daemon that executes it, no reconciler.

- **Release trains vs. continuous deployment for 2-10 engineers.** The
  consensus is unambiguous for small teams: "small teams with mature
  automation may prefer continuous deployment; trains add value if
  coordination or compliance is required." — [Release Train vs Continuous Deployment](https://hector-reyesaleman.medium.com/release-trains-vs-continuous-deployment-13015e7f89ff).
  **Relevance:** Agora's sprint model already produces a natural release
  boundary (sprint-end regression) without imposing a fixed train — this doc
  recommends the Deploy stage support **both** an ad-hoc per-issue/per-PR
  deploy (continuous, Tier 1/2 today) and an explicit sprint-end "ship the
  sprint branch" action, rather than forcing either cadence.

- **AI-generated release notes from merged PRs.** This is now a common,
  productized pattern — GitHub's own "Generated release notes" feature lists
  merged PRs since the last release; third-party tools (Changeish, PR-Agent)
  do the same with an LLM summarization pass for readability. —
  [GitHub: Generated release notes](https://github.blog/changelog/2026-06-18-generated-release-notes-credit-you-for-copilot-pull-requests/),
  [Generating Changelogs and Release Notes with AI](https://www.deployhq.com/git/generating-changelogs-with-ai).
  **Relevance:** directly validates §3.6 — an agent summarizing a sprint's
  merged issues into release notes on deploy is not a novel idea, it's
  catching up to what's now standard tooling elsewhere; Agora's advantage is
  that the *source of truth* (issue titles/descriptions/labels) is already
  structured, unlike raw commit messages.

---

## 3. Design: what DEPLOY should be

### 3.1 Stage definition

**Entry:** the issue's PR is merged (or, for sprint-PR mode, merged into the
sprint branch) **and** `qa:pass` holds — i.e. Review has passed
(`deriveReviewStage`, `stage.ts:149-187`). Deploy has no meaning before
Review passes; it should stay `pending`/greyed exactly as it does today until
then.

**Exit:** a durable record exists that the merged ref is verified live in the
target environment — not "the sync command returned 0," but "a post-deploy
check confirmed the app answers at that ref." This distinction matters: a
`git pull` succeeding on a box says nothing about whether the app actually
restarted, migrated, or is serving traffic. See §3.4.

**Environments model.** Recommend a small, explicit, ordered list per
project rather than a fixed three-tier ladder every project must populate:

```
project.settings.deploy_environments: [
  { key: "qa",         label: "QA box",     kind: "box",     target: <connected_box binding, existing> },
  { key: "staging",    label: "Staging",    kind: "command" | "box" | "provider", target: ... },
  { key: "production", label: "Production", kind: "command" | "provider", target: ..., requires_human: true }
]
```

This is a `project.settings` JSONB key — exactly the existing `qa_smoke_url`/
`docs_repo`/`docs_agent` pattern — so it costs **zero migrations**, is
per-project (stacks vary per project, per the research question), and a
project with only a QA box today needs to configure nothing new: `hasDeployTarget`
degrades to the existing `connected_box` lookup when `deploy_environments` is
unset. `kind` maps directly onto the tiers in §3.2. Every environment except
the QA box is optional — a project that only ever git-syncs to a QA box (most
of them, today) sees no behavior change.

The stepper's Deploy lens should show one row per configured environment
(mirrors GitHub's environment model, §2) rather than a single pass/fail —
this is what makes "deployed to staging but not yet promoted to production"
representable, which today it structurally is not (there is exactly one
deploy verb).

### 3.2 Deploy providers — tiered proposal

**Tier 1 (exists today): git-sync to a bound box.** `DeployIssueQA` /
`DeploySprintBranch` unchanged. Correct for the QA environment kind and for
any team whose "production" genuinely is a box Agora's daemon can SSH into
(this describes a real fraction of Agora's own SalesDoctor PHP-monolith
customers — `agora.sdteam.uz`-style boxes are not hypothetical). No new work
beyond wiring its result into `deploy_event` (§3.3).

**Tier 2 (build first): command-based deploy.** A project setting
`deploy_cmd` (string, per environment — `project.settings.deploy_environments
[].target.command`) that an **agent runs on a daemon** (the local daemon on a
dev's machine, or the always-on cloud daemon — both already execute
arbitrary shell as part of `run_qa`/`draft_code` today, so this needs no new
execution capability, only a new slice-action kind that runs the command and
reports exit code + stdout/stderr). This single primitive covers:
- `fly deploy --app <app>` for Fly.io apps (Agora's own stack, dogfooded)
- an SSH deploy script for a bare box (`ssh box.example.com 'cd /var/www/app
  && git pull && composer install && php artisan migrate --force'`)
- `vercel --prod`, `netlify deploy --prod`, `docker compose up -d --build`,
  or any other one-liner a team already has
This is the tier that actually answers the research question — it is stack-
agnostic by construction, requires zero provider-specific code, and matches
what small teams already do by hand today (a deploy script, run from a
laptop or a cron). **Recommend building this before Tier 3.**

**Tier 3 (defer): provider integrations.** GitHub Deployments API
(create a `deployment` + post `deployment_status`, so a merged PR shows a
real "Deployed to production" badge on GitHub itself, and Environments'
built-in protection rules — required reviewers, wait timers — become
available for free) and the Fly Machines API (structured deploy/rollback
without shelling out to `flyctl`, per-machine health checks for a real
canary-lite). Both are well-scoped, bounded follow-ups once Tier 2 proves the
event/verification/notes loop end-to-end. Building Tier 3 first would mean
shipping GitHub-specific and Fly-specific code paths before validating the
generic one — backwards for a tool whose stated customers include PHP
monoliths with no GitHub Deployments concept at all.

### 3.3 Signals for the stepper: `deploy_event`

Mirror `qa_evidence` structurally — same team already knows this pattern,
same migration cost, same read pattern:

```sql
CREATE TABLE IF NOT EXISTS deploy_event (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id      uuid REFERENCES issue(id) ON DELETE CASCADE,   -- nullable: sprint-level deploys have no single issue
    sprint_id     uuid REFERENCES sprint(id) ON DELETE SET NULL,
    project_id    uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    environment   text NOT NULL,        -- "qa" | "staging" | "production" | project-defined key
    ref           text NOT NULL,        -- branch or SHA deployed
    status        text NOT NULL,        -- "pending" | "success" | "failed"
    verified_at   timestamptz,          -- set once post-deploy smoke passes (§3.4); null = synced but unverified
    source        text NOT NULL DEFAULT 'agent',  -- agent | human | schedule — same provenance idea as qa_evidence.source
    result_json   jsonb NOT NULL DEFAULT '{}',    -- command output / API response, redacted
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_deploy_event_issue ON deploy_event (issue_id, created_at DESC);
CREATE INDEX idx_deploy_event_project_env ON deploy_event (project_id, environment, created_at DESC);
```

One migration, additive, no change to any existing table. `use-stage-pipeline.ts`
adds one query (`GetLatestDeployEventForIssue`, same shape as
`GetLatestQAEvidenceForIssue`) and finally has a real value for `deploySynced`:
`deploySynced = latestDeployEvent?.status === "success" && latestDeployEvent
.ref === currentBranchSha`. This directly fixes the bug named in the research
brief — the stepper stops being permanently pending/skipped and starts
reflecting reality. `verified_at` (null until §3.4 passes) lets the stepper
distinguish "synced" (running but unconfirmed) from "verified live," which
maps onto `StageState`'s existing `running` vs `passed` split with no schema
change to `stage.ts` itself — only `deploySynced` needs to become
`deployState: "unverified" | "verified" | undefined` or equivalent, a small,
additive change to the existing derivation function.

**Why not reuse `qa_evidence` directly instead of a new table?** Its unique
key is `(issue_id, baseline_ref, branch_sha)` — no `environment` dimension,
no `project_id` for sprint/workspace-level deploys with no single issue, and
its `verdict` enum is QA-specific (pass/fail against a baseline diff, not
"synced and live"). A parallel table with the same *shape* (immutable rows,
provenance column, latest-wins read) is more honest than overloading QA
evidence with a second unrelated meaning.

### 3.4 Post-deploy verification: deploy-smoke vs. QA gate

Reuse the `run_qa` slice-action pipeline's *machinery* (instruction
templating, browser-proxy live attach, DOM-based checks) but not its
*license*. A new slice-action kind, `deploy_smoke`, differs from `run_qa` in
exactly the ways that matter for hitting a real environment:

| | `run_qa` | `deploy_smoke` (proposed) |
|---|---|---|
| Target | QA box (disposable, seeded/reset data) | staging or production (real or real-adjacent data) |
| Allowed actions | full interaction: fill forms, submit, mutate | **read-only**: navigate, assert DOM/status codes/health endpoints, no form submission, no state-changing requests |
| Test data | may create/use throwaway records | must not create or alter records |
| Failure mode | `qa:fail` blocks merge | `deploy_event.status = "failed"`, stage shows failed, does NOT roll back automatically (see §3.7) |
| Enforcement today | prompt discipline only | **must be code-enforced, not just prompted** — see below |

This closes the concrete gap found in §1.4: today nothing in code stops an
agent from mutating data if a smoke target happens to be production. Two
independent enforcement layers are warranted before Tier 2 targets anything
beyond a QA box:
1. **Template-level:** `slice_action_templates/deploy_smoke.md` states the
   read-only contract as unambiguously as `run_qa.md` states "do NOT merge
   anything."
2. **Code-level (the actual fix):** the browser-proxy path allowlist
   (`browserProxyPathAllowed`, `browser_proxy.go:133-146`) already
   demonstrates the pattern — a fixed allowlist of permitted operations. For
   `deploy_smoke`, gate on the *environment kind*: when `deploy_event
   .environment == "production"` (or any environment flagged
   `requires_human`/non-QA in `deploy_environments`), the smoke run should be
   restricted to HTTP GET/HEAD requests and DOM assertions only — no
   `browser_click` on submit-shaped elements, no form fill. This is new code,
   not a policy restated; treat it as a hard prerequisite for pointing any
   automation at a real production URL, matching the no-prod-testing rule in
   CLAUDE.md and closing the exact gap the backend-recon agent flagged.

### 3.5 Human gate

`RequireHumanActor` (`server/internal/handler/actor_guards.go:96`) is
directly reusable with no new plumbing: it checks the server-set
`X-Actor-Source` header (stamped only for `task_token`/`cloud_pat` auth,
un-spoofable by a client — `actor_guards.go:56-61`) and 403s any
agent/task-token caller. It already gates instance-config writes, PAT
management, and cloud-billing checkout (`router.go:613-1092`) — exactly the
"irreversible or privilege-escalating" bar a production deploy clears.

Recommend: the route that triggers a `production`-kind deploy (whichever
tier) is `r.With(handler.RequireHumanActor)`-gated at the router, same
one-line pattern as every existing usage. In the Deploy lens, this surfaces
as a **"Deploy to Production" button that a human must click** — an agent can
prepare everything (verify staging is green, draft the release notes,
present the diff since last production deploy) but cannot press it. This
mirrors the existing sprint-PR-mode pattern where an agent can auto-merge
into a sprint branch but merging into `main` always routes to a human
(`slice_action.go:1654-1675`, `protectedSprintBranches`, `sprint.go:53-60`) —
the same boundary, one hop further down the pipeline.

### 3.6 Release notes / changelog automation

Clone the `auto_docs` chain (§1.5) verbatim, substituting the trigger event
and target:

1. **Trigger:** a `deploy_event` reaching `status="success"` **and**
   `environment="production"` (or the terminal environment in the project's
   list) fires `maybeReleaseNotesOnDeploy`, called from wherever
   `deploy_event` rows get written (the deploy handler itself, not a label
   listener this time — there's no natural label to hang it on for a
   sprint-level deploy).
2. **Gate:** a new flag, `AGORA_RELEASE_NOTES_ENABLED` (one `config.Def{}`
   entry in `registry.go`, same category shape as `AGORA_AUTO_DOCS_ENABLED`).
3. **Scope:** for a sprint-level production deploy, gather every issue with
   `issue_to_sprint` membership in that sprint that reached `done` since the
   *previous* production `deploy_event` for the project — this is exactly
   the diff GitHub's native "generated release notes" computes (§2), just
   sourced from Agora issues instead of raw commits, which is Agora's actual
   advantage (structured titles/labels vs. commit-message archaeology).
4. **Agent resolution:** reuse `resolveAutoDocsAgent`'s fallback chain
   (project's configured release-notes agent → issue/sprint lead's agent →
   the deploying user's own agent).
5. **Delivery:** two reasonable targets, not mutually exclusive — (a) a
   comment on a synthetic "Release vX" issue or on the sprint itself (mirrors
   `auto_docs`'s comment delivery, cheapest to build), and (b) written into
   the project's `docs_repo` as a `CHANGELOG.md` entry if one is configured
   (reuses `docsRepoInstruction`'s existing repo-push machinery verbatim).

This is the one piece of the Deploy stage that is almost pure agent-prompt +
glue work — no new execution capability needed, since `triggerTasksForComment`
and the docs-repo push path already exist end-to-end.

### 3.7 Rollback story

Minimal viable, deliberately: **rollback = redeploy the previous known-good
ref**, not a distinct code path. `deploy_event` already stores `ref` per
successful deploy, so "previous known-good" is `SELECT ref FROM deploy_event
WHERE project_id=? AND environment=? AND status='success' ORDER BY
created_at DESC OFFSET 1 LIMIT 1` — one query, no new state.

- **Tier 1 (git-sync):** `performBoxSync` re-run with the previous ref as
  `branch` — already idempotent, already the exact function used for forward
  deploys. Rollback IS a deploy, same as Fly's own philosophy (§2).
  Zero new code beyond exposing "redeploy this past `deploy_event` row" as a
  button in the Deploy lens.
- **Tier 2 (command):** re-run `deploy_cmd` with the previous ref
  checked out first (`git checkout <previous ref> && <deploy_cmd>`) — same
  agent-runs-a-command primitive, no new capability.
- **Tier 3 (provider):** Fly's own rollback is "deploy an older image" (§2);
  GitHub Deployments supports marking a deployment `inactive` explicitly.
  Both map onto "create a new `deploy_event` row with the old ref" cleanly.

Explicitly **not** in scope for v1: automated rollback-on-health-check-
failure (canary-style auto-revert). That requires the deploy_smoke
verification (§3.4) to run *during* the deploy window with a rollback
trigger wired to its result — a real feature, but a Tier 3+ one, and the
research brief's own framing ("canary-lite") suggests treating it as a
stretch goal, not a phase-1 requirement, given the team-size target (2-10
people manually watching a deploy is a viable interim state; automated
canary is not what unblocks them first).

---

## 4. Phased recommendation

| Phase | What | Files (indicative) | Layer | Effort |
|---|---|---|---|---|
| **P0 — Signal fix** | `deploy_event` table + queries (mirrors `qa_evidence`); wire into `use-stage-pipeline.ts` so `deploySynced`/`deployState` is real for Tier-1 (existing box git-sync) deploys; write the row from `DeployIssueQA`/`DeploySprintBranch` on sync success. Deploy lens shows last-deploy ref/time/status instead of just box info. | 1 migration (`server/migrations/`), `server/pkg/db/queries/deploy_event.sql`, `connected_box.go` (write on sync), `use-stage-pipeline.ts`, `stage.ts` (extend `deploySynced` → verified/unverified), `deploy-lens.tsx` | Backend (small) + Frontend (small) | ~2-3 days. Zero new UX paradigm — fixes an existing bug with the existing pattern. Ship this regardless of anything else in this doc. |
| **P1 — Command-based deploy (Tier 2)** | `project.settings.deploy_environments` config surface (project settings page); new slice-action kind `deploy` that runs `deploy_cmd` on a daemon and writes a `deploy_event` row with exit code/output; `RequireHumanActor`-gated route for any `production`-flagged environment. Deploy lens: per-environment rows, a "Deploy to <env>" button per configured target. | `slice_action.go` (new kind + template `slice_action_templates/deploy.md`), `router.go` (human-actor gate), project settings UI (`packages/views/projects/`), `deploy-lens.tsx` (multi-environment rows) | Backend (medium) + Frontend (medium) + Agent prompt (new template) | ~1-1.5 weeks. This is the phase that actually answers "what should Deploy be for varied stacks" — ships the stack-agnostic primitive. |
| **P2 — Verification + human gate hardening** | `deploy_smoke` slice-action kind (read-only variant of `run_qa`, §3.4) with the code-level action-allowlist restriction on production-flagged environments; `verified_at` on `deploy_event` set only after a passing smoke; Deploy stage `passed` requires smoke, not just sync. | `slice_action.go` (new kind + template), `browser_proxy.go`-style allowlist extension for smoke-run restriction, `stage.ts`/`use-stage-pipeline.ts` (require `verified_at`) | Backend (medium, includes the new code-level guard — the most safety-critical piece in this doc) + Agent prompt | ~1 week. Should not ship before P1's production-flagged environments exist, since there's nothing to guard yet without them. |
| **P3 — Release notes + rollback UX** | `maybeReleaseNotesOnDeploy` cloned from `auto_docs` (§3.6); "redeploy previous ref" button reading `deploy_event` history (§3.7); `AGORA_RELEASE_NOTES_ENABLED` config flag. | `slice_action.go` (new function + `AGORA_RELEASE_NOTES_ENABLED` in `registry.go`), `deploy-lens.tsx` (history + rollback button) | Backend (small-medium) + Frontend (small) + Agent prompt | ~3-5 days. Independent of P2 — could ship in parallel with it if two people are working the Deploy stage. |
| **Deferred (Tier 3)** | GitHub Deployments API integration (create deployment + post status on P0-P3's `deploy_event` writes, so GitHub's own UI reflects Agora deploys); Fly Machines API for structured deploy/rollback/canary instead of shelling `flyctl`; automated rollback-on-failed-smoke. | `github.go` (outbound calls), new `fly_api.go` or similar | Backend (medium-large per integration) | Not before P0-P3 prove the generic path. Build only once a real customer's stack specifically wants GitHub Environments badges or Fly-native canary — don't speculate ahead of demand for a 2-10 person team. |

**Sequencing rationale:** P0 is a bug fix disguised as a phase and should
ship independent of everything else — it's the one-line-of-truth fix the
research brief called out directly. P1 is the actual product decision this
doc argues for (command-based deploy over provider-specific integration
first). P2 cannot start meaningfully before P1 gives it a production-flagged
target to guard. P3 is decoupled enough to parallelize with P2.

---

## 5. Open questions / risks not resolved by this doc

- **Multi-environment UI cost.** §3.1's per-project environment list is
  cheap on the backend (a JSONB key) but is a real new settings surface on
  the frontend — sizing that UI (add/edit/reorder environments, per-env
  `deploy_cmd` editor) is a P1 sub-task not estimated in detail above.
- **Who owns `deploy_cmd` correctness?** Tier 2 runs whatever string a human
  configured, on a daemon, as an agent action — this is strictly less
  sandboxed than `run_qa`'s browser-proxy allowlist, closer in shape to
  `draft_code`'s arbitrary-shell trust level. Worth an explicit confirm-before-
  first-run UX (or a `DryRun`-style preview, mirroring `ProvisionConnectedBoxForMember`'s
  existing `DryRun` field) rather than trusting the box-provisioning safety
  rails to generalize automatically.
- **Sprint-level vs. issue-level deploy granularity** is left ambiguous in
  this doc's `deploy_event` schema (`issue_id` nullable, `sprint_id` present)
  — whether a single-issue "hotfix to production" flow is common enough to
  warrant its own lens affordance vs. always deploying at sprint granularity
  is a product question, not something the codebase recon answers.
