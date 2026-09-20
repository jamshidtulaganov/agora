# Activation plan — signup to the first verified change

Status: drafted 2026-09-20 · Owner: Jamshid · grounded in the code at
`bed9d320` · external sources in Appendix B

## Thesis

Agora's onboarding is not broken. It is finished, polished, four-locale,
WS-live, and it ends in the wrong place: a chat agent explaining Agora to you.
The three cards a new user is offered at the end of the flow are *"Introduce
Agora to me"*, *"Walk me through the core features"*, and *"Show me what Agora
can do for me — as slides"* (`packages/views/onboarding/templates/helper-starter-prompts.ts:24-167`).
A user who picks one gets a well-written essay. They do not get a change to
their code, and so they do not get the only thing that separates Agora from
the thing they already have.

Because the thing they already have is the prerequisite. The cloud quickstart
states it in its own voice: *"One prerequisite: you already have at least one
AI coding tool installed locally… The daemon auto-detects them on startup and
**refuses to start if none are present**"*
(`apps/docs/content/docs/cloud-quickstart.mdx:10`), enforced at
`server/internal/daemon/daemon.go:793-795`. So the ICP that activates fastest
is exactly the ICP the market research names as the competitor — *"Linear
Agent performs worse than my own Claude Code + MCP — I don't even login most
days"* (`docs/market-research-synthesis.md`, finding 4). We require the user to
own the competitor before we will run.

That is survivable, and it is even correct for the *second* run. It is fatal
for the first one, because the first run is where we have to prove we are
worth a login. And the product knows: the honest string
`"Heads-up: agents can't execute tasks without a runtime — if you hit Skip
now, your workspace is read-only until you come back and install one."`
(`packages/views/locales/en/onboarding.json` → `cloud_waitlist.intro_warning`)
sits in the funnel next to a **waitlist we built for our own wall**
(`POST /api/me/onboarding/cloud-waitlist`, `server/internal/handler/onboarding.go:285-345`,
with a first-class analytics exit path `OnboardingPathCloudWaitlist`,
`server/internal/analytics/events.go:96`).

This plan does four things: it defines the metric, it names every wall with
the file that builds it, it decides where a stranger's first agent runs, and
it sequences the work so each phase ships alone.

---

# 1. The metric

**Time-to-first-verified-change (TTFVC)** — wall-clock seconds from a user's
`signup` to the first moment a workspace they own holds an agent-made change
that passed a check.

One number, one product. Not "time to first agent run" — that is the vanity
version, and the external evidence is unusually direct about why: Bessemer's
2026 agentic report calls proving output correctness *"the single largest
bottleneck on autonomy"*; DORA 2025 finds 90% AI adoption alongside **30% with
little or no trust in AI-generated code**, and AI adoption still correlating
*negatively* with delivery stability; Devin's own justification for mandatory
environment setup is that without it *"Devin can't build your project, can't
run your tests, and can't verify its own work"* (Appendix B, 2/3/6). A
generated diff is not an aha for this audience. A green check on a generated
diff is.

## 1.1 What counts as verified

The eventual definition is the computed verification object designed in
`docs/orchestration-upgrade-plan.md` §D1 — `GET /api/issues/{id}/verification`
returning `level ∈ verified | partial | unverified`. **That endpoint does not
exist**; grep `server/cmd/server/router.go` and it is absent. Its `verified`
level also requires a `review:pass` from an actor distinct from the author
(`server/internal/service/review_evidence.go:117-127`) — a second agent a
ten-minute-old solo workspace does not have — and `AGORA_AUTO_REVIEW_ENABLED`
defaults **off** (`server/internal/config/registry.go:81`, no `Default` field).

So the activation predicate is deliberately weaker than the Phase-2 level, and
we say so out loud rather than quietly redefining "verified" to whatever is
convenient:

> **verified (activation)** — the issue carries a `qa:pass` gate label backed
> by a `qa_evidence` row whose verdict cleared the evidence floor. Source may
> be `agent` or `human`.

Every part of that is already built and already defended:

| Piece | Where |
| --- | --- |
| The gate label attach | `server/internal/service/qa_evidence.go:516` (`qa:pass`) |
| The evidence row | `server/migrations/134_qa_evidence.up.sql:14-26`, unique per `(issue_id, baseline_ref, branch_sha)`; timing columns added in `157_qa_run_identity.up.sql:32-36` |
| The anti-fabrication floor | `qa_evidence.go:507` → `qaEvidenceFloorGap` at `:650-712`; a pass with no commands becomes `qa:stale`, not `qa:pass` (`server/internal/handler/qa_evidence_floor_test.go:31-55`) |
| The human path, with provenance | `POST /api/issues/{id}/qa-override`, `server/internal/handler/qa_override.go:61`, `RequireHumanActor` at `router.go:1159`; writes `source="human"` + actor + reason into `result_json.override` |
| The fold into one state | `ReconcileQAState`, `server/internal/service/qa_state.go:95` |

The floor is what makes this predicate worth anything. Without it "verified"
would mean "an agent wrote the word pass", which is the failure mode the whole
positioning exists to avoid.

**Why not the alternatives**, each rejected for a concrete reason:

- **PR merged** — requires GitHub. The Changes view *"renders NOTHING when
  there is nothing to say. No PR linked means no section"*
  (`packages/views/issues/components/changes-section.tsx:37-38`), and a
  `local_directory` project never produces a PR at all
  (`apps/docs/content/docs/project-resources.mdx`). Half our activation paths
  would be unmeasurable.
- **`review:pass`** — needs a second agent and a flag that is off by default.
- **`issue.first_executed_at`** — this is what we measure today
  (`server/migrations/050_issue_first_executed_at.up.sql`, emitted as
  `issue_executed` from `server/internal/handler/daemon.go:2923-2952`, called
  the *"per-issue exactly-once activation keystone"* in
  `server/internal/analytics/events.go:256`). It means "a task finished". It
  says nothing about whether the change was right. Keep it — it is the
  denominator for "ran but never verified" — but stop calling it activation.

When orchestration Phase 2 lands, TTFVC's predicate upgrades to
`verification.level != "unverified"` as a **second series**. Do not retro-write
the first one; a metric whose definition silently moved is not a metric.

## 1.2 Where it is measured

Copy the `first_executed_at` pattern exactly — it already solved the
concurrency problem (`server/pkg/db/queries/issue.sql:474-481`: `UPDATE … SET
… WHERE id = $1 AND first_executed_at IS NULL RETURNING *`).

Migration 211 (next free number; 210 is `pr_changed_paths`):

```sql
ALTER TABLE issue      ADD COLUMN first_verified_at TIMESTAMPTZ NULL;
ALTER TABLE workspace  ADD COLUMN first_verified_change_at TIMESTAMPTZ NULL;
CREATE INDEX idx_issue_first_verified_at
  ON issue (workspace_id, first_verified_at) WHERE first_verified_at IS NOT NULL;
```

Two stamps because activation is a *team* property, not an issue property. The
issue stamp is the event; the workspace stamp is the funnel.

Emit sites — both already exist and both already run inside the right
transaction-ish scope:

1. `qa_evidence.go:516`, immediately after the `qa:pass` label attaches and
   the floor has been cleared.
2. `qa_override.go`, on a human override to `pass`.

Events (PostHog, following `docs/analytics.md`'s contract):

- **`issue_verified`** — at most once per issue. Properties: `issue_id`,
  `workspace_id`, `verdict_source` (`agent|human`), `runtime_mode`,
  `provider`, `evidence_commands`, `floor_cleared`,
  `seconds_since_issue_created`.
- **`workspace_activated`** — at most once per workspace. Properties:
  `workspace_id`, `seconds_since_workspace_created`,
  `seconds_since_owner_signup` ← **this is TTFVC**, `runtime_kind`
  (`managed|local`), `had_import` (bool), `had_repo` (bool).

Prometheus, for the operator view:
`agora_time_to_first_verified_change_seconds` (histogram, labels
`runtime_kind`, `provider`) — the same idiom as
`agora_agent_task_total_seconds` in `server/internal/metrics/business.go`.

## 1.3 The funnel

Steps, in order, all but three of which already emit:

| # | Step | Event | Exists? |
| --- | --- | --- | --- |
| 1 | Signup | `signup` | ✅ `events.go:115` |
| 2 | Workspace created | `workspace_created` | ✅ `events.go:133` |
| 3 | **Code connected** (repo or local dir) | `project_resource_connected` | ❌ new |
| 4 | Runtime online | `runtime_ready` | ✅ `events.go` + `daemon.go:443` |
| 5 | Agent created | `agent_created` | ✅ |
| 6 | Issue assigned to agent | `issue_created` | ✅ |
| 7 | Agent run finished | `issue_executed` | ✅ |
| 8 | **Change verified** | `issue_verified` | ❌ new |
| 9 | **Workspace activated** | `workspace_activated` | ❌ new |

Side-channel, for the import path: `import_started` / `import_completed`
(neither exists — `grep import_ server/internal/analytics/events.go` returns
nothing, despite the importer shipping end-to-end).

## 1.4 What is banned

Unchanged from `docs/market-research-synthesis.md` and
`docs/orchestration-upgrade-plan.md` §F3, restated because activation work is
where these get violated: no issues-closed-per-agent, no tasks/day, no tokens
as a leaderboard, no per-human comparison. TTFVC measures **the funnel and the
money, never the people**. It is a property of the product, not of the user.

Also banned: reporting TTFVC as a median without the denominator. Most signups
never verify anything. The pair is **TTFVC (p50 of those who activate)** and
**activation rate (share who ever reach step 9 within 7 days)**. Reporting the
first alone is how a funnel gets optimised by driving away the slow users.

There is no external benchmark to compare against, and we should stop looking
for one. The only public number is a 2022 self-reported survey (34% average /
25% median activation rate, each respondent using their own definition —
Appendix B, 10). Our baseline is our own cohort curve, measured **before**
Phase 0 changes anything. That ordering is the whole reason Phase 1 ships
first.

---

# 2. The walls, in order

What a fresh two-person team hits today, each with the file that builds it.

| # | Wall | Built at | What they see |
| --- | --- | --- | --- |
| 1 | Signup demands first name, last name **and company name** before it will send a code | `packages/views/auth/login-page.tsx:165-168`, `:435`, `:487` | Disabled button; *"First name, last name, and company name are required"* |
| 2 | Company name silently creates a workspace they never asked for | `login-page.tsx:201-217` (`finishSignup` → `createCompanyWorkspace`) | Step 2 later flips to *"Continue with {name}, or start another."* |
| 3 | Mandatory 6-digit email code, 10-minute TTL, 60s resend lock, real SMTP required | `server/internal/handler/auth.go:405-477`, `:435-439`, `:450` | *"Check your email"* |
| 4 | **The first paragraph of the first screen describes a distribution CRM** | `packages/views/locales/en/onboarding.json` → `welcome.lede`, rendered at `packages/views/onboarding/steps/step-welcome.tsx:96`; translated in all four locales | *"SalesDoctor is a multi-tenant Distribution CRM for field sales, van-selling, merchandising, and route accounting…"* |
| 5 | Workspace is created **completely bare** — one `workspace` row, one owner `member` row, nothing else | `server/internal/handler/workspace.go:190-215` | No project, no labels, no sprint, no issues, no agent, no repo |
| 6 | **The runtime wall.** Step 3 requires an AI coding CLI already installed and logged in on a real machine | `server/internal/daemon/daemon.go:793-795` (`"no agent runtimes could be registered"`); documented at `cloud-quickstart.mdx:10` | *"No agent runtime found on this computer yet."* |
| 6b | The cloud escape hatch is a waitlist | `onboarding.json` → `step_platform.cloud_subtitle` / `step_runtime.empty_waitlist_*`; handler `onboarding.go:285-345` | *"We'll run a computer for you in the cloud. Not live yet."* / **"Coming soon"** |
| 6c | Skipping is described, accurately, as breaking the product | `onboarding.json` → `step_runtime.empty_skip_subtitle` | *"Enter your workspace in read-only mode. Your agent can't run tasks…"* |
| 7 | **Nothing anywhere asks for the repo.** There is no code-connect step | absent from `packages/views/onboarding/steps/`; resources live at project level (`apps/docs/content/docs/project-resources.mdx`) | — |
| 7b | …so the agent discovers it at run time and apologises | `server/internal/handler/issue_repo_nudge.go:41-43` | *"⚠️ This project has no repository connected, so I have no code to work on."* |
| 8 | The happy path's payoff is three chat prompts | `packages/views/onboarding/templates/helper-starter-prompts.ts:24-167` | *"Introduce Agora to me"* · *"Walk me through the core features"* · *"Show me… as slides"* |
| 8b | The skip path's payoff is two pieces of homework, assigned to the user | `templates/install-runtime-issue.ts:23`, `templates/create-agent-guide-issue.ts:22`, seeded at `packages/views/workspace/welcome-after-onboarding.tsx:216-262` | *"Step 1 — Connect a runtime…"* · *"Step 2 — Create your first Agora Agent"* |
| 9 | Manual agent creation dead-ends with **no explanation** | `packages/views/agents/components/runtime-picker.tsx:113`, `:129-132`; submit gate `create-agent-dialog.tsx:386-395`; server `server/internal/handler/agent.go:805-808` (`"runtime_id is required"`) | Picker reads *"No runtime available"*; Create is permanently grey; no link, no CTA |
| 10 | The assistant's agent interview hits the same wall and can only point at a page | `server/internal/assistant/prompt.go:353-357` | *"nobody has connected one yet and that this one step happens in Settings → Runtimes"* |
| 11 | An issue assigned to an agent on a dead daemon queues forever, **silently** | `server/internal/handler/issue.go:3229-3240` (`isAgentAssigneeReady` checks a runtime id, never `status='online'`) | Nothing. No badge, no toast, no comment. |
| 12 | Verification is off by default, so the "verified" positioning is false out of the box | `registry.go:43` (`AGORA_AUTO_QA_ENABLED`), `:81` (`AGORA_AUTO_REVIEW_ENABLED`) — neither carries a `Default` | — |
| 13 | Empty workspace offers nothing | `issues-page.tsx:230-235` | *"No issues yet"* / *"Create an issue to get started."* |

Three of these are the plan. **#6 is the cliff** — everything above it is
cheap. **#7 is the silent one** — we let the agent find out. **#12 is the
credibility one** — we ship the claim and default the mechanism off.

Everything else is copy and one afternoon.

---

# 3. What the fast-activation products actually do

Only the findings that changed a decision below. Full table in Appendix B.

**Auth goes after intent capture, before compute — and the intent survives the
boundary.** Lovable documents it verbatim: *"type your prompt on the homepage
first, and Lovable asks you to sign up when you submit, keeping your prompt
through sign-up"*. Bolt's documented step order is identical: prompt → Build
now → sign up → compute. **Nobody runs inference for an anonymous user.** The
anonymity is in the input box, not the GPU. (Appendix B, 1/2.)

**Zero-config is the default; agent-driven setup is the offered upgrade, never
the prerequisite.** Copilot's coding agent requires *literally nothing* and
discovers dependencies by trial and error on a stock runner. Devin requires an
indexed repo and a machine snapshot and calls it *"the single highest-leverage
thing you can do"* — and then makes it conversational (*"Set up your
environment for this repo"*). Cursor does the same. Agora currently sits at the
Devin end of the spectrum — daemon, repo, agent, runtime — with none of
Devin's sales motion to carry it. (Appendix B, 3/4/5/7.)

**Configuration should accrue from use, not gate first use.** Devin's Knowledge
is explicitly earned: *"Devin will automatically suggest Knowledge to remember
based on your feedback in chat."* This is the KB flywheel argument, arriving
from outside. (Appendix B, 6.)

**Surface failures inside the artifact the user is already reading.** GitHub's
agent firewall writes the blocked address *and the exact command that tried
it* into the PR body. This is the cheapest activation-recovery mechanism in the
whole sweep, and it is the direct answer to wall #11. (Appendix B, 8.)

**Nobody seeds demo data into your real workspace.** Linear's demo is a
separate workspace you visit. Sentry has you plant a real error in your own app
(`throw new Error("My first Sentry error!")`). NN/g's only mention of demo data
is as a *button the user presses*. (Appendix B, 9/12/13.)

**Free tiers cap in dollars or tokens and degrade to a non-inference mode
rather than a paywall.** Bolt: 300K tokens/day, and at zero *"you can still
make direct edits… in Code view, which doesn't use tokens"*. Lovable: 5
credits/day, *"Building stops"*, published site stays live. Replit's Free Mode
*"uses intelligent model routing"* — **the free tier is a cheaper model, not a
smaller quota**, which is the most transferable lever here. Cursor asks for a
spend limit *at first use*, not in a buried setting. (Appendix B, 14-19.)

**The honest caveat:** every time-to-value number above — "about ten minutes",
"approximately five minutes" — is a vendor estimate in a docs page. No product
in this category publishes an activation rate. Do not benchmark against them;
benchmark against our own cohort.

---

# 4. The decision: where a stranger's first agent runs

Three options. The trade is real, so here it is with the numbers.

### A. A bundled cloud runtime per workspace (Fleet)

The machinery half-exists. `/api/cloud-runtime/*` is registered
(`router.go:1609-1621`), proxying to an external `agora-cloud` Fleet service
that provisions EC2 nodes and mints node-scoped `mcn_` PATs via SSM bootstrap
(`server/internal/handler/cloud_runtime.go:47-54`).

**Reject for activation.** Three independent reasons: (1) `AGORA_CLOUD_FLEET_URL`
is set in **no** deployment file — not `.env.example`, not `render.yaml`, not
any compose file — so the proxy 503s (`cloud_runtime.go:104-107`); (2) the UI
is behind `NEXT_PUBLIC_ENABLE_CLOUD_RUNTIME`, referenced at exactly one site
(`apps/web/app/[workspaceSlug]/(dashboard)/runtimes/page.tsx:3-4`) and set
nowhere; (3) the create dialog asks for **instance type, disk GiB, AMI ID,
subnet ID and key pair** (`packages/views/locales/en/runtimes.json` →
`cloud_runtime.fields.*`). That is an ops console. And a dedicated EC2 box per
signup is the wrong unit cost for a funnel where most signups never return.

Keep Fleet. It is the right shape for the paid BYOC / dedicated-box tier. It is
not the shape of a first run.

### B. A shared, instance-operated runtime pool — **chosen**

The decisive fact is that **the daemon is already multi-workspace**:

- `d.workspaces map[string]*workspaceState` — `server/internal/daemon/daemon.go:102`
- `workspaceSyncLoop`, 30-second tick — `daemon.go:1184-1198`,
  `DefaultWorkspaceSyncInterval = 30 * time.Second` at
  `server/internal/daemon/config.go:68`
- `syncWorkspacesFromAPI` — `daemon.go:1200-1214`, whose own comment is the
  design: *"fetches all workspaces the user belongs to and registers runtimes
  for any that aren't already tracked. Workspaces the user has left are cleaned
  up."*

So: **make a service account a member of a workspace, and within 30 seconds
that workspace has an online runtime. Remove the membership, and it goes
away.** The grant and the revoke are both one row.

And the multi-tenancy falls out of the existing schema rather than being
bolted on. `agent_runtime` is keyed `UNIQUE (workspace_id, daemon_id,
provider)` (`server/migrations/004_agent_runtime_loop.up.sql:14`), so one pool
daemon produces a **separate runtime row per workspace** — which means
`runtime_usage` (`013_runtime_usage.up.sql:3,13`, keyed on `runtime_id`) is
already per-workspace, which means per-workspace spend accounting and
per-workspace revocation are reads and deletes we can already express.

The image and the deployment pattern are proven in production: `Dockerfile.daemon`
bakes Claude Code (`:37`), Cursor Agent, code-server and Chromium; `render.yaml:210-244`
runs it as `agora-daemon-claude`, a `type: worker` on a 10 GB disk with
`AGORA_PAT` + `CLAUDE_CODE_OAUTH_TOKEN` injected at runtime, described as an
*"Outbound-only worker: claims Agora tasks and executes them with Claude Code
or Cursor Agent even when every developer laptop is offline."*

**Cost control is the honest objection, and it has an honest answer.** The pool
burns our tokens. The levers that exist:

- `--max-budget-usd` — a hard per-run dollar cap, emitted at
  `server/pkg/agent/claude.go:548-549`. It binds on **Claude Code only**
  (`server/internal/daemon/types.go:145-146`) — and Claude Code is exactly what
  the pool image runs. The one provider where the dollar cap works is the one
  we control.
- `--max-turns` (`claude.go:534-535`), default 200 (`registry.go:69`) — the
  runaway-loop guard.
- Wall-clock (`AGORA_TASK_TIMEOUT_MINUTES`, `registry.go:70`), default 0.

The gap: **budgets have no workspace scope.** They resolve project → instance
(`server/internal/handler/project_config.go:29-41`, then
`server/internal/config/store.go:180-205`), and `instance_config` is global to
the deployment (`153_instance_config.up.sql` — `key TEXT PRIMARY KEY`, no
workspace column). `parseBudgetUSD` also **fails open**
(`server/internal/handler/task_budget.go:91-101`): empty default = no cap.

**Decision: do not add a budget scope for this.** Ship the trial as a
**project** and set `AGORA_TASK_BUDGET_USD_*` in that project's
`settings.config` — `ProjectScoped: true` is already on all six keys
(`registry.go:65-70`) and `projectConfigOverrides` already reads them. Zero new
machinery. Add a workspace scope later if the pool outgrows it; the plumbing to
do so is one `ResolveFrom` tier.

Two implementation details that will bite if unwritten:
- `agent_runtime.visibility` defaults `'private'`
  (`083_runtime_visibility.up.sql:2-3`), and a private runtime is owner-or-admin
  only (`agent.go:838-841`). The pool's runtimes must register `public`, or an
  invited teammate cannot create an agent on it.
- `daemon_token.expires_at` is `NOT NULL` (`029_daemon_token.up.sql:6`) — a
  ready-made trial clock if we prefer token expiry to membership revocation.

### C. Local-first, one-command installer

This is what exists, and it is not slow because of us. `agora setup` does
config → browser login → daemon start in one command
(`server/cmd/agora/cmd_setup.go:115-151`), the desktop app ships the CLI in
`apps/desktop/resources/bin` and starts the daemon on launch, and the connect
dialog live-detects registration over WS in *"usually 10–30 seconds"*.
Realistically 3-5 minutes **from a prerequisite-satisfied state**.

The cost is entirely the prerequisite: a third-party install, a third-party
login, and usually a paid subscription — on the user's terminal, outside our
product, invisible to our telemetry. For a cold user that is 20-40 minutes, and
for a non-technical one it does not complete (the primary CTA contains the word
*"terminal"*).

### Verdict

**B for the first run. C for the second. Never presented as competing
options.**

The managed runtime's job is to get a stranger to a verified change before they
have invested anything. The local runtime's job is to be the thing they keep —
and the pitch for it is not "the free one ran out", it is the true one already
written in our docs: *"your API keys, toolchain, and code directories stay on
your machine — the Agora server never sees any of them"*
(`apps/docs/content/docs/daemon-runtimes.mdx:10`). Trial → local is an
**upgrade in privacy and control**, which is a story we can tell without
flinching. Trial → paid credits is a story the research says gets us
uninstalled (`market-research-synthesis.md`, finding 3: credit meters are a
named churn driver).

Pricing shape, stated once so nobody re-opens it: **a cap, not a meter.** The
trial shows a dollar ceiling the user can see, in dollars, with no invented
currency — the same rule as `docs/orchestration-upgrade-plan.md` §F1, and the
same placement as Cursor's *"You'll be asked to set a spend limit when you
first start using them"* (Appendix B, 17). If the cheaper-model route is
wanted, Replit's lever is the one to copy: the free tier is a **model routing
decision**, not a smaller quota.

---

# 5. The 10-minute path

For a team **with a repo**. Times are budgets, not promises.

| # | Step | Mechanism | Seam | Budget |
| --- | --- | --- | --- | --- |
| 0 | Intent capture on the landing page — a repo URL or one sentence | Cookie/localStorage, carried through signup exactly as the `agora_signup_source` cookie already is (`analytics.md` → `signup.signup_source`) | `apps/web/features/landing/`, `login-page.tsx` | 0s |
| 1 | Signup | Email code or Google. **Company name becomes optional** and stops silently creating a workspace | `login-page.tsx:165-168`, `:201-217` | 60s |
| 2 | Name the workspace | Unchanged (`step-workspace.tsx`). Plus **two optional rows**: *Bring your issues — Linear* and *Connect GitHub* | `packages/views/onboarding/steps/step-workspace.tsx` | 30s |
| 3 | **Connect your code** — new step | GitHub App install is one click and already wired: `GET /api/workspaces/{id}/github/connect` → install URL with a signed state → `GET /api/github/setup` callback. Pick a repo → create the first project with a `github_repo` resource | `server/internal/handler/github.go:268-288`, `:290+`; `packages/views/projects/components/project-resources-section.tsx` | 60s |
| 4 | **Runtime** — replace the "Coming soon" card | *"Use Agora's computer — free, capped at $N"*. Grant = add the pool account as a member; the sync loop registers within 30s (nudge it with a direct register call to make it instant) | `steps/step-platform-fork.tsx`, `steps/step-runtime-connect.tsx`; grant hook at `workspace.go:224-238` (post-commit) | 30s |
| 5 | First agent | UI: the four templates that already exist (`onboarding.json` → `step_agent.templates.{coding,planning,writing,assistant}`) with `recommend-template.ts` revived. Assistant: `/new-agent` interview, which now finds a runtime | `steps/step-agent.tsx` (orphaned, revive), `prompt.go:352-357` | 20s |
| 6 | First issue — **outcome-shaped, human voice** | Three cards derived from the connected repo, each producing a change. Marked `ticket_kind: "outcome"` per `orchestration-upgrade-plan.md` §C1 | replaces `templates/helper-starter-prompts.ts` | 30s |
| 7 | The agent works | Unchanged. Changes view renders the diff in-app | `packages/views/issues/components/changes-section.tsx` | ~154s |
| 8 | **Verify** | `AGORA_AUTO_QA_ENABLED` set **on for the trial project** (project-scoped, `registry.go:43`), or one click: `api.sliceAction(issueId, {kind:"run_qa"})` | `packages/views/qa/components/qa-lens.tsx:224` | ~120s |
| 9 | **The aha** | `qa:pass` clears the floor → `first_verified_at` stamps → the verdict chip goes green on the Changes view, with the evidence behind a hover | `qa_evidence.go:516` | 0s |

**≈ 8.5 minutes.** The two irreducible costs are the agent run (154s is the
measured dev-stage median from `docs/orchestration-upgrade-plan.md`'s thesis)
and the QA run. Everything else is form-filling and one OAuth round-trip.

Three design commitments inside that table:

**The first issue must produce a change, not an essay.** The current three
cards ask the agent to describe the product. The replacements ask it to touch
the repo — a missing `--version` flag, a broken README link, a test for the
one exported function that has none. Written the way a person files a quick
ticket, per the standing human-voice rule, because a verbose spec skews the
execution tier and makes the agent over-build.

**The repo step is the one that removes an apology.** Today the agent
discovers the missing repo at claim time and posts *"⚠️ This project has no
repository connected, so I have no code to work on"*
(`issue_repo_nudge.go:41-43`). That comment is a well-built piece of error
handling for a question we should never have to ask.

**Failures land in the issue, not the log.** Wall #11 — a queued task against
an offline daemon, with zero user-facing signal — gets GitHub's firewall
treatment: a system comment on the issue naming what failed and what to do,
using the same `postNoRepoBoundNudge` idiom that already exists two files over.

---

# 6. The import entry

The Linear importer shipped end-to-end — `import_connection` /
`import_job` (migrations 205/206), routes at `router.go:1013-1027`, UI at
`packages/views/settings/components/import-section.tsx`,
`IMPORT_SOURCES = ["linear"]` (`packages/core/imports/types.ts:21`). The
growth hook designed in `docs/importers-plan.md` §7 was never built. Build it,
unchanged from that design, because the design is right:

- **One optional row on the workspace step**, not a new step. *"Bring your
  issues — Linear"*, deep-linking to `/settings?tab=integrations` and
  returning. A mandatory migration screen in front of a solo evaluator is a
  wall; a row they can ignore costs nothing.
- **The issues empty state says it too.** Today it says *"No issues yet /
  Create an issue to get started."* (`issues-page.tsx:230-235`). It is the
  highest-intent surface in the product and it currently offers one verb.

**The instant-value move — the part that is new here.** When an import job
completes, auto-run the SPRINT REPORT recipe over the imported history and pin
it to the project. Every piece exists: the recipes
(`docs/assistant-domain-plan.md` Phase 1, `prompt.go`), the pin
(`POST /api/assistant/artifacts/{id}/pins`, `router.go:642`), the project
Reports section (`GET /api/projects/{id}/reports`, `router.go:1255`), and the
import receipt artifact itself (`importers-plan.md` §4.1 step 6). Eight months
of somebody's Linear history becomes a report in thirty seconds, pinned where
the whole team reads it. No competitor's importer produces an artifact,
because none of them has an artifact system.

**Scope discipline, stated plainly:** the import is a *retention* and *proof*
hook, not the activation hook. It does not produce a verified change. Track it
as a funnel side-channel (`had_import` on `workspace_activated`), and never let
it become a detour that inflates TTFVC. If a user imports first, the guided
first issue still comes from the repo.

---

# 7. The empty workspace

Today an empty workspace is three hand-rolled empty states that each name their
own emptiness — *"No issues yet"*, *"No notifications"*, *"No projects yet"* —
with no shared primitive between them (there is no `EmptyState` component in
`packages/views`; `agents-page.tsx`, `runtimes-page.tsx` and `skills-page.tsx`
each define a local one).

Replace the issues-list empty state with **one card**. Not a tour, not a
checklist, not a progress ring. The subtraction rule: one obvious behaviour
over a configurable surface.

The card computes the single next action from what is missing, first match
wins:

| Missing | Card | Button |
| --- | --- | --- |
| No runtime | *"Your agents need a computer to run on."* | **Use Agora's computer** (secondary: *Connect your own*) |
| No code | *"Connect a repo so agents have something to change."* | **Connect GitHub** (secondary: *Use a folder on this machine*) |
| No agent | *"Add your first teammate."* | **Create an agent** |
| Nothing missing | *"Give them something to do."* | **New issue** · *or import from Linear* |

One line of why, one primary verb, one quiet alternative. When everything is
present the card stops rendering — the same rule the Changes section already
keeps: *"It renders NOTHING when there is nothing to say"*
(`changes-section.tsx:37-38`).

**Kill the two seeded homework issues** (`templates/install-runtime-issue.ts`,
`templates/create-agent-guide-issue.ts`, seeded at
`welcome-after-onboarding.tsx:216-262`). Filing chores in a user's own tracker,
assigned to them, as their welcome gift, is the product admitting it could not
finish its own setup. The card above does the same job without leaving
residue they have to close.

---

# 8. Sandbox / demo — argue it, don't assume it

**No prefilled demo workspace. Confidence ~70%, and the hedge is free.**

The evidence is convergent practice rather than measurement, and it is worth
saying so instead of dressing it up:

- Linear's demo content lives in a **separate demo workspace** you visit, not
  seeded into the one you just created (Appendix B, 9).
- Sentry — the closest analogue, a dev tool whose value only appears once real
  data flows — does not fabricate sample errors. It walks you through planting
  a real one in your own app: `throw new Error("My first Sentry error!")`
  (Appendix B, 12).
- NN/g's empty-state guidance mentions demo data exactly once, and as a
  **button the user presses**, never as pre-seeded state (Appendix B, 13).
- DORA 2025's 30%-little-or-no-trust figure is about AI-generated code, not
  demos, so it is supporting texture rather than proof (Appendix B, 11).

I found **no controlled experiment in either direction**, and anyone who claims
measured evidence that sample data hurts activation is overclaiming.

What tips it is that `docs/importers-plan.md` §7 already reached the same
conclusion from inside: *"an imported workspace is a better demo of what Agora
is for than an empty one, which is the actual reason this is the highest-leverage
feature on the list."* **The imported history is the demo.** For a team with
no tracker to import, the demo is their own repo's actual README typo, fixed
and verified — which is strictly better than our fiction, because the verified
badge on it is true.

If we ever want a showroom, build it as a separate, clearly-labelled sandbox
workspace reachable from the marketing site. Never seed a real one.

---

# 9. Concierge vs UI — the degraded path

The assistant cannot be load-bearing. `AGORA_ASSISTANT_ENABLED` defaults true,
but the registry says the rest out loud: *"Availability still requires the
selected provider's API key — with no key the endpoint reports disabled and
the UI hides the nav item"* (`registry.go:108`). The default provider is
`zhipu` and needs an instance-level `ZHIPU_API_KEY` (`registry.go:109`, `:174`).
Self-hosted instances routinely have neither.

**Rule: every step of §5 must be completable by clicking.** The assistant makes
each step one sentence instead of four clicks; it is never the only door.

| Step | UI path (always) | Assistant path (accelerator) |
| --- | --- | --- |
| Connect code | GitHub tab + repo picker | *"connect the acme/api repo"* |
| Runtime | The trial card on step 3 | — (grant is a click; no reason to route it through chat) |
| First agent | Four template cards | `/new-agent` interview (`prompt.go:352-357`) |
| First issue | Three outcome cards | *"fix the broken link in the README"* |
| Import | Settings → Integrations panel (connect / dry-run / apply) | The conversation in `importers-plan.md` §4.1 |
| Verify | **Run QA** button (`qa-lens.tsx:224`) | *"run QA on MUL-3"* |

The one thing the assistant genuinely carries alone is the import **argument
loop** — *"Dana is dana@acme.com"*, *"skip anything closed before 2025"* —
because revising a mapping in prose is qualitatively better than a form. The
UI fallback there is the existing dry-run plan panel, which is worse but
complete.

Corollary for Phase 3: the trial runtime grant must be a plain HTTP endpoint
and a button, not an assistant tool. Standing spend behind a chat message is
the wrong door for the same reason pinning is UI-only in
`docs/assistant-domain-plan.md` Phase 2a.

---

# 10. Kill list

Things the evidence argues against, with the reason.

1. **The cloud waitlist.** `cloud_waitlist_email` / `cloud_waitlist_reason`
   (migration 052), `JoinCloudWaitlist` (`onboarding.go:285-345`), the *"Coming
   soon"* card, the `cloud_waitlist_joined` event, and the
   `OnboardingPathCloudWaitlist` completion path (`events.go:96`). We
   instrumented our own wall as a funnel stage. Once the pool ships, a waitlist
   for a thing that exists is a lie; before it ships, it is a confession.
2. **The three chat-demo starter cards.** An essay about Agora is not an aha
   for someone evaluating Agora against their own Claude Code.
3. **The two seeded homework issues.** §7.
4. **The onboarding questionnaire remnants.** The survey was removed from the
   UI (`onboarding-flow.tsx:43-45`) but `users.onboarding_questionnaire`,
   `PatchOnboarding`'s survey half, `recommend-template.ts` and
   `needs-backfill.ts` (exported, rendered nowhere) all survive. Revive
   `recommend-template.ts` for the agent-template default; delete the rest. Do
   not re-introduce the survey — asking a stranger their role before they have
   seen anything work is a tax we cannot spend.
5. **"Read-only mode" as an onboarding exit.** It is an accurate description of
   a broken state. The fix is to make the state not happen, not to describe it
   better.
6. **An onboarding checklist / progress object / product tour.** Nobody in the
   sweep ships one for developers; Linear's start guide is a doc, not in-product
   chrome. One computed next-action card instead (§7). This is the subtraction
   rule and it is also what the evidence shows.
7. **A prefilled demo workspace.** §8.
8. **A credit card on the trial.** No product in the sweep requires one for the
   free tier, and a spend cap is the control.
9. **Per-signup free-credit meters.** `market-research-synthesis.md` finding 3
   names credit meters a churn driver by name (ClickUp AI Super Credits burning
   invisibly). A dollar ceiling the owner can see is the opposite of a meter and
   should be said out loud in the copy.
10. **"Time-to-first-agent-run" as the headline metric.** §1.
11. **Reporting TTFVC without the activation rate.** §1.4.
12. **Anonymous free inference.** Nobody does it, it is unbounded abuse surface,
    and the research shows the value is in carrying the *prompt* across signup,
    not the compute.

Not a kill but a bug, listed because it is the first sentence a stranger reads:
**`welcome.lede` describes a distribution CRM** in all four locales
(`packages/views/locales/{en,ru,uz,zh-Hans}/onboarding.json`).

---

# 11. Sequencing

Each phase is independently shippable and independently measurable.
Dependencies are named. Nothing here is a big bang.

### Phase 0 — Truth in the first screen (½ PR, ~2 days)

Frontend and locales only, no backend, no migration.

- Fix `welcome.lede` in `packages/views/locales/{en,ru,uz,zh-Hans}/onboarding.json`.
- Replace `templates/helper-starter-prompts.ts` with three outcome-shaped,
  human-voice cards.
- Delete `templates/install-runtime-issue.ts`, `templates/create-agent-guide-issue.ts`
  and the skip-path seeding in `welcome-after-onboarding.tsx:216-262`.
- One computed next-action card in `issues-page.tsx`, replacing the bare empty
  state; locales ×4, `parity.test.ts` green.
- Add the runtime explanation + link that `runtime-picker.tsx:129-132` is
  missing (wall #9) — one sentence and one link, not a redesign.

Ships alone. Fixes four walls. Zero risk.

### Phase 1 — The metric (1 PR) · **must land before anything claims a win**

- Migration 211: `issue.first_verified_at`, `workspace.first_verified_change_at`,
  partial index.
- `MarkIssueFirstVerified` / `MarkWorkspaceFirstVerified` in
  `server/pkg/db/queries/`, copying `issue.sql:474-481`'s atomic flip.
- Emit from `qa_evidence.go:516` and from `qa_override.go`.
- `analytics.IssueVerified` / `analytics.WorkspaceActivated` in
  `server/internal/analytics/events.go`; Prometheus histogram in
  `server/internal/metrics/business.go` + labels.
- `project_resource_connected`, `import_started`, `import_completed`.
- `docs/analytics.md` — the funnel definition, the ban list, the
  TTFVC-plus-activation-rate pairing.
- Tests: the at-most-once flip under concurrent verdicts; a floor-downgraded
  pass must **not** stamp.

This is the phase that lets every later phase be judged. Per the activation
literature, the baseline cohort has to be measured before the funnel moves
(Appendix B, 20/21).

### Phase 2 — The code step (1 PR) · independent

- New onboarding step between workspace and runtime: GitHub connect → repo
  pick → first project + `github_repo` resource. Reuses `GitHubConnect`
  (`github.go:268-288`) and the existing resource writer.
- A "use a folder on this machine" alternative for the desktop path
  (`local_directory`, already supported).
- The two optional rows on the workspace step (import + GitHub), per
  `importers-plan.md` §7.
- The import link in the issues empty state.
- Delete-on-sight: once the step exists, `issue_repo_nudge.go` should almost
  never fire — keep it as the backstop, add a metric so we can see if it does.

### Phase 3 — The managed trial runtime (2 PRs) · the big one

**PR 1 — backend.**
- A pool service account + `AGORA_TRIAL_RUNTIME_ENABLED` / `AGORA_TRIAL_POOL_USER_ID`
  registry keys.
- Post-commit grant hook in `CreateWorkspace` (`workspace.go:224-238`) —
  best-effort, never blocks creation.
- `POST /api/workspaces/{id}/trial-runtime` for the explicit "Use Agora's
  computer" click (grant + immediate register nudge so it is instant rather
  than 30s).
- `agent_runtime.managed BOOL` + register the pool's runtimes with
  `visibility='public'` (else `agent.go:838-841` bites invited members).
- The trial project with `AGORA_TASK_BUDGET_USD_*` + `AGORA_AUTO_QA_ENABLED`
  in `settings.config`.
- Expiry: membership revocation on a clock, or `daemon_token.expires_at`.
- A `kind='budget'` escalation when the cap trips — the machinery is shipped
  (migration 209) and an invisible ceiling is indistinguishable from a broken
  daemon.

**PR 2 — frontend.**
- Replace the "Coming soon" cloud card in `step-platform-fork.tsx` and
  `step-runtime-connect.tsx`.
- Show the cap, in dollars, on the card itself (Cursor's placement).
- The upgrade-to-local nudge, phrased as privacy and control, not as a quota.
- Kill list items 1 (waitlist) in the same PR.

### Phase 4 — Verified is a badge · depends on orchestration Phase 2

`GET /api/issues/{id}/verification` is designed
(`orchestration-upgrade-plan.md` §D1) and absent from `router.go`. When it
lands, render the chip on the Changes view and on the first-run success moment,
with the signal list and **who produced each signal** on hover — provenance is
the product; a checkmark with no attribution is an emoji.

Until then the first-run moment shows the QA verdict chip that already exists.

### Phase 5 — Instant value from old data (1 PR) · depends on Phase 2's import row

Post-import: auto-run the sprint-report recipe over imported history, pin it to
the project. All machinery exists (§6).

### Phase 6 — Pre-auth intent capture (1 PR) · independent

Repo-URL-or-one-sentence box on the landing page, carried through signup the
way `agora_signup_source` already is, pre-filling step 3 or step 6. The
Lovable/Bolt pattern. Last because it is the only phase whose value depends on
all the others working.

---

# 12. Non-goals

- No new pipeline, no second onboarding, no fork of the flow for trial users.
  The trial runtime is a runtime; everything downstream is unchanged.
- No anonymous inference.
- No credit system, no AI-gated tier, no per-agent seat pricing.
- No onboarding tour, checklist, coachmark or progress object.
- No demo data seeded into a real workspace.
- No throughput metric of any kind, anywhere in this plan.
- No revival of the acquisition questionnaire.
- No change to the `ALLOW_SIGNUP` / `DISABLE_WORKSPACE_CREATION` self-host
  gates — a self-hosted instance that wants invite-only stays invite-only, and
  the trial runtime is off by default there (it costs the *operator's* money,
  not ours).
- No work on Fleet in this plan. It is the paid-tier shape, not the first-run
  shape.

---

# Appendix A — repo grounding index

Every claim above traces here.

**Signup / auth** — `server/internal/config/registry.go:157` (`ALLOW_SIGNUP`,
default true) · `server/internal/handler/auth.go:340-372` (signup gate),
`:405-477` (code send), `:539-545` (no default workspace join) ·
`packages/views/auth/login-page.tsx:165-168` (three required fields),
`:201-217` (silent workspace creation) ·
`apps/web/app/(auth)/signup/page.tsx:62` (redirect to `/onboarding`)

**Workspace** — `server/internal/handler/workspace.go:134-240`
(`CreateWorkspace`; creates exactly one workspace row + one owner member),
`:151-155` (`DISABLE_WORKSPACE_CREATION`), `:224-238` (post-commit, the grant
hook point) · `packages/views/onboarding/steps/step-workspace.tsx`

**Onboarding (v3, shipped)** — `docs/onboarding-refactor-plan.md` ·
`packages/views/onboarding/onboarding-flow.tsx:43-45` (survey removed), `:97-172` ·
`packages/core/onboarding/step-order.ts` (`workspace` → `runtime`) ·
`packages/core/onboarding/welcome-store.ts` ·
`packages/views/workspace/welcome-after-onboarding.tsx:80-127` (dispatch),
`:216-262` (skip-path homework) ·
`packages/views/onboarding/templates/helper-starter-prompts.ts:24-167` ·
`packages/views/onboarding/steps/step-welcome.tsx:96` (renders `welcome.lede`) ·
`server/internal/handler/onboarding.go:60-129` (thin `CompleteOnboarding`),
`:285-345` (`JoinCloudWaitlist`) · `server/internal/handler/onboarding_shim.go`
(deprecation shim, still routed at `router.go:708-709`) ·
`server/migrations/182_backfill_accepted_invitee_onboarding.up.sql`

**Runtime** — `server/migrations/004_agent_runtime_loop.up.sql:6` (`local|cloud`),
`:14` (`UNIQUE(workspace_id, daemon_id, provider)`) ·
`083_runtime_visibility.up.sql:2-3` · `029_daemon_token.up.sql:4-6` ·
`server/internal/handler/daemon.go:276-398` (`DaemonRegister`,
`RuntimeMode: "local"` hardcoded at `:392`), `:431-443` (`runtime_ready`) ·
`server/internal/daemon/daemon.go:102` (multi-workspace map), `:663`
(sync loop launch), `:751` (`registerRuntimesForWorkspace`), `:793-795` (refuses
with no tool), `:1184-1214` (`workspaceSyncLoop` / `syncWorkspacesFromAPI`) ·
`server/internal/daemon/config.go:68` (30s) ·
`server/cmd/agora/cmd_setup.go:115-151` · `Dockerfile.daemon:37` ·
`render.yaml:206-244` (the proven 24/7 worker) ·
`packages/views/runtimes/components/runtimes-page.tsx:188-191`, `:697-724`
(empty state), `:312-321` (dead cloud button) ·
`packages/views/runtimes/components/connect-remote-dialog.tsx:29-60`, `:209-268`

**Cloud runtime (Fleet)** — `server/cmd/server/router.go:1609-1621` ·
`server/internal/handler/cloud_runtime.go:26-107` ·
`server/internal/cloudruntime/client.go:21-22` ·
`apps/web/app/[workspaceSlug]/(dashboard)/runtimes/page.tsx:3-4`
(`NEXT_PUBLIC_ENABLE_CLOUD_RUNTIME`, set nowhere) ·
`packages/views/locales/en/runtimes.json` → `cloud_runtime.*`

**Agents** — `server/internal/handler/agent.go:782-939` (`CreateAgent`),
`:805-808` (`runtime_id is required`), `:838-841` (private runtime),
`:908-910` (duplicate name) ·
`packages/views/agents/components/create-agent-dialog.tsx:386-395` ·
`packages/views/agents/components/runtime-picker.tsx:113`, `:129-132` ·
`docs/agent-quick-create-plan.md` (draft, not started) ·
`packages/core/onboarding/recommend-template.ts` (orphaned)

**Code / repo** — `apps/docs/content/docs/project-resources.mdx` ·
`server/internal/handler/github.go:268-288` (`GitHubConnect`), `:290+`
(setup callback) · `server/internal/handler/issue_repo_nudge.go:19-80` ·
`packages/views/projects/components/project-resources-section.tsx`

**Verification** — `server/internal/service/qa_evidence.go:430-520`
(capture + `qa:pass`), `:650-712` (floor), `:762-805` (downgrade to stale) ·
`server/internal/service/qa_state.go:62-95` (`ReconcileQAState`) ·
`server/internal/service/review_evidence.go:96`, `:117-127` (author ≠ reviewer),
`:193` · `server/internal/handler/merge_readiness.go:13-40` ·
`server/internal/handler/qa_override.go:16-61` ·
`server/migrations/134_qa_evidence.up.sql`, `157_qa_run_identity.up.sql:32-36` ·
`packages/views/qa/components/qa-lens.tsx:224` (the Run QA button) ·
`packages/views/issues/components/changes-section.tsx:27-41` ·
`server/internal/config/registry.go:43`, `:81` (both verification flags
default off) · `docs/orchestration-upgrade-plan.md` §D1 (the object, not built)

**Budgets / escalation (Phase 0, shipped)** —
`server/internal/handler/task_budget.go:48-130` ·
`server/internal/config/registry.go:65-70` (all `ProjectScoped`, no defaults) ·
`server/internal/handler/project_config.go:29-41` ·
`server/internal/config/store.go:180-205` (resolve chain) ·
`server/migrations/153_instance_config.up.sql:6-11` (global, no workspace
column) · `server/pkg/agent/claude.go:534-535`, `:548-549` ·
`server/internal/daemon/types.go:145-150` (claude-only dollar cap) ·
`server/migrations/209_task_escalation.up.sql`

**Import** — `docs/importers-plan.md` §4.1, §7 ·
`server/migrations/205_import_connection.up.sql`, `206_import_job.up.sql` ·
`server/cmd/server/router.go:1013-1027` ·
`packages/views/settings/components/import-section.tsx` ·
`packages/core/imports/types.ts:21` (`["linear"]`)

**Assistant** — `server/internal/config/registry.go:108-110`, `:174` ·
`server/internal/assistant/prompt.go:136-140`, `:352-357` (the runtime
dead-end) · `server/internal/assistant/tools.go:212` (`propose_plan`) ·
`server/cmd/server/router.go:642-658`, `:1255` (pins + reports) ·
`docs/assistant-domain-plan.md`

**Analytics** — `docs/analytics.md` · `server/internal/analytics/events.go:8-30`
(event names), `:96` (`cloud_waitlist` path), `:115` (`signup`), `:256-259`
(`issue_executed`) · `server/internal/handler/daemon.go:2923-2952` ·
`server/migrations/050_issue_first_executed_at.up.sql` ·
`server/pkg/db/queries/issue.sql:474-481`

**Empty states** — `packages/views/issues/components/issues-page.tsx:230-235` ·
`packages/views/inbox/components/inbox-page.tsx:267` ·
`packages/views/projects/components/projects-page.tsx:299-306` ·
`packages/views/agents/components/agents-page.tsx:1006-1023`

**Copy** — `packages/views/locales/{en,ru,uz,zh-Hans}/onboarding.json`
(`welcome.lede`, `cloud_waitlist.intro_warning`, `step_runtime.empty_*`,
`step_platform.cloud_*`, `step_agent.templates.*`,
`welcome_after_onboarding.*`)

**Docs site** — `apps/docs/content/docs/cloud-quickstart.mdx:10` (the
prerequisite), `:12-104` (the ten steps) ·
`apps/docs/content/docs/install-agent-runtime.mdx:19-24` ·
`apps/docs/content/docs/daemon-runtimes.mdx:10` (keys never leave your machine)

---

# Appendix B — external sources

All fetched 2026-09-20 unless noted. ★ where the finding changed a decision in
this document. Sources 22-26 were fetched 2026-09-19 for
`docs/orchestration-upgrade-plan.md` and are reused here without re-fetching.

| # | Source | Date | What it established |
| --- | --- | --- | --- |
| 1 ★ | [Lovable — getting started](https://docs.lovable.dev/introduction/getting-started.md) | accessed 2026-09-20 | Verbatim: *"type your prompt on the homepage first, and Lovable asks you to sign up when you submit, keeping your prompt through sign-up."* Claim: first app *"in about ten minutes, from your first prompt to a live URL."* **Basis for §5 step 0 and Phase 6.** |
| 2 ★ | [Bolt — quickstart](https://support.bolt.new/get-started/quickstart.md) | accessed 2026-09-20 | Documented order: prompt → "Build now" → sign in → survey → ~5 min wait → Preview. Independent confirmation of the auth-after-intent placement. |
| 3 ★ | [Devin — environment setup](https://docs.devin.ai/onboard-devin/environment.md) | accessed 2026-09-20 | *"Devin can't build your project, can't run your tests, and can't verify its own work."* **The strongest external argument that verification, not generation, is the activation event (§1).** |
| 4 ★ | [Devin — repo setup](https://docs.devin.ai/onboard-devin/repo-setup) | accessed 2026-09-20 | Machine snapshot = *"a frozen, bootable image"*; exactly one active snapshot per org; env config is *"the single highest-leverage thing you can do."* Agora sits at this end of the config spectrum today (§3). |
| 5 | [Devin — first run](https://docs.devin.ai/get-started/first-run.md) | accessed 2026-09-20 | Prerequisite: *"make sure you've indexed and set up your repositories."* Scoping heuristic: *"if a task would take you three hours or less."* Ask mode (plan) before Agent mode (execute) — a human-confirmed plan before burning compute. |
| 6 ★ | [Devin — knowledge](https://docs.devin.ai/product-guides/knowledge) | accessed 2026-09-20 | Knowledge is earned, not authored: *"Devin will automatically suggest Knowledge to remember based on your feedback in chat."* **Config accrues from use rather than gating first use — the rule behind §5's "nothing optional is mandatory".** |
| 7 ★ | [Cursor — cloud agent setup](https://cursor.com/docs/cloud-agents/setup) | accessed 2026-09-20 | Resolution order repo → personal → team env; install script must be idempotent; *"Cursor can set up your dev environment in the cloud in less than 10 minutes."* Agent-driven setup as an offered upgrade. |
| 8 ★ | [GitHub — Copilot agent firewall](https://docs.github.com/en/copilot/how-tos/agents/copilot-coding-agent/customizing-or-disabling-the-firewall-for-copilot-coding-agent) | accessed 2026-09-20 | On by default with a curated allowlist; when it blocks, *"a warning is added to the pull request body… shows the blocked address and the command that tried to make the request."* **Basis for §5's rule that failures land in the issue, and the fix for wall #11.** |
| 9 ★ | [Linear — start guide](https://linear.app/docs/start-guide) | accessed 2026-09-20 | First-run order: intro video **or demo workspace** → create workspace → configure → invite. Import is **not** in the sequence; the demo is a **separate workspace**, not seeded data. **Basis for §8.** Caveat: the docs do not state where the importer surfaces in the live product. |
| 10 | [Linear — import issues](https://linear.app/docs/import-issues) | accessed 2026-09-20 | Admin-only; five steps (Setup, Review, Choose, Map users, Confirm). Actively discourages reflexive import: *"consider whether it needs to be in Linear at all."* |
| 11 | [Linear — agents](https://linear.app/agents) + [dev docs](https://linear.app/developers/agents) | accessed 2026-09-20 | *"the human user remains the primary assignee, while the agent is added as a contributor"*; delegation sets `delegate`, not `assignee`. Confirms `market-research-synthesis.md` G6 — Agora's polymorphic assignee is still the differentiated position. |
| 12 ★ | [Sentry — generate your first error](https://docs.sentry.io/product/sentry-basics/integrate-frontend/generate-first-error/) | accessed 2026-09-20 | No fabricated sample data: the user plants `throw new Error("My first Sentry error!")` in their own app. **The closest analogue to Agora and the strongest single data point in §8.** |
| 13 ★ | [NN/g — empty state interface design](https://www.nngroup.com/articles/empty-state-interface-design/) | 2021-09-19 | Empty states should communicate status, teach, and give a direct pathway. Demo data appears once, as a **user-pressed button** (Loggly), never pre-seeded. **Basis for §7's single card and §8.** |
| 14 ★ | [Replit — Free Mode](https://docs.replit.com/build/build-with-free-mode.md) + [Starter plan](https://docs.replit.com/billing/plans/starter-plan.md) | accessed 2026-09-20 | Free Mode *"uses intelligent model routing to select the model"* — **the free tier is a cheaper model, not a smaller quota.** The most transferable cost lever for a managed trial runtime (§4). |
| 15 | [Bolt — tokens](https://support.bolt.new/account-and-subscription/tokens) | accessed 2026-09-20 | 300K/day, 1M/month; at zero *"you can still make direct edits to your project's existing code for free in Code view, which doesn't use tokens."* Degrade to non-inference, don't paywall. |
| 16 | [Lovable — credits and usage](https://docs.lovable.dev/introduction/credits-and-usage) | accessed 2026-09-20 | 5 credits/day, max 30/month; at zero *"Building stops"* but the published site stays live. Also: the credit meter the research names as a churn driver. |
| 17 ★ | [Cursor — cloud agents](https://cursor.com/docs/background-agent) | 2026-09-19 | *"You'll be asked to set a spend limit when you first start using them."* **Spend limit as a first-use step, not a buried setting — adopted for the trial card copy (§4, Phase 3 PR 2).** |
| 18 | [v0 — pricing](https://v0.app/docs/pricing) | accessed 2026-09-20 | $5/month in credits; at zero *"generation pauses"*. The only public number that bounds cost-per-activation in this category. |
| 19 | [GitHub — Copilot plans](https://docs.github.com/en/copilot/get-started/plans) | accessed 2026-09-20 | Copilot Free: 2,000 completions/month, auto model selection only; **no coding agent on Free at all.** The conservative end of the trial-compute spectrum. |
| 20 ★ | [Lenny's Newsletter — how to determine your activation metric](https://www.lennysnewsletter.com/p/how-to-determine-your-activation) | 2022-11-08 | Brainstorm candidate moments → regress against retention → **experiment to prove causality**. *"A good activation metric is causal for your retention, not just correlative."* **Basis for Phase 1 shipping before Phase 0's effects are claimed.** |
| 21 | [Lenny's Newsletter — what is a good activation rate](https://www.lennysnewsletter.com/p/what-is-a-good-activation-rate) | 2022-10-25 | Average 34%, median 25%. **500+ self-reported responses, each using its own definition — a vibe benchmark, four years old.** Cited only to justify §1.4's refusal to benchmark externally. |
| 22 | [Amplitude — the aha moment](https://amplitude.com/blog/aha-moment) | 2025-06-16 | Correlate early actions against long-term retention; instrument via funnel + journey analysis; warns about selection bias from narrow cohorts. |
| 23 ★ | [DORA 2025 report announcement](https://cloud.google.com/blog/products/ai-machine-learning/announcing-the-2025-dora-report) | 2025-09-24 | ~5,000 respondents. 90% use AI at work; **30% report little or no trust in AI-generated code**; AI adoption now positive for throughput but **still negative for delivery stability**. **Basis for §1's rejection of "first agent run".** |
| 24 ★ | [Bessemer — The Agentic Awakening: convert the people](https://theagenticawakening.com/convert-the-people) | 2026 | *"The single largest bottleneck on autonomy"* is proving output correctness, not agent speed. Qualitative case studies, no sample size — directional, not measured. **Basis for §1.** |
| 25 | [GitHub — customizing the dev environment for Copilot coding agent](https://docs.github.com/en/copilot/how-tos/agents/copilot-coding-agent/customizing-the-development-environment-for-copilot-coding-agent) | accessed 2026-09-20 | Nothing mandatory; stock Ubuntu runner; agent discovers deps by trial and error. `copilot-setup-steps.yml` must be on the default branch to trigger. Hard 59-minute session cap. |
| 26 | [GitHub — repository custom instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions) | accessed 2026-09-20 | `AGENTS.md` anywhere in the tree (nearest wins), plus root `CLAUDE.md`/`GEMINI.md`. *"Instructions must be no longer than 2 pages. Instructions must not be task specific."* GitHub MCP + Playwright MCP enabled by default — zero-config tool access is the baseline. |

**Research limitation, recorded.** The web-search budget for this pass was
exhausted before the sweep began, so every source above is a **direct fetch of
a primary document** (vendor docs, the research reports themselves) rather than
a search result. That is better for verifiability and worse for coverage: no
secondary teardowns, no practitioner commentary, and in particular nothing
that would strengthen or overturn §8, where the evidence is convergent practice
rather than measurement. Treat §8 at ~70% confidence; the hedge (offer demo
data, never pre-seed it) costs nothing either way.

Internal references used throughout: `docs/market-research-synthesis.md`,
`docs/orchestration-upgrade-plan.md`, `docs/importers-plan.md`,
`docs/assistant-domain-plan.md`, `docs/living-truth-plan.md`,
`docs/onboarding-refactor-plan.md`, `docs/analytics.md` (all 2026-09-19 or
earlier).
