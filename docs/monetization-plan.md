# Monetization plan — the billing and entitlement plane

Status: proposed, 2026-09-20. Nothing in this document is built.
Evidence base: `docs/market-research-synthesis.md` (positioning constraints),
`docs/orchestration-upgrade-plan.md` §F (fleet economics, already-written
doctrine), plus repo grounding and external pricing research below.

Every pricing rule the market has settled is taken as given here and not
re-litigated. What this document decides is the *plane*: what is free, what is
paid, how usage becomes a number, how a limit is enforced without ever breaking
a run, and what we refuse to build.

---

## 0. The one-line result

**Agora is a control plane, not a compute reseller.** On the local-daemon path —
which is the default and the product's entire wedge — the agent runs on the
user's machine under the user's own provider credentials
(`server/internal/daemon/daemon.go:3337` injects the agent's `CustomEnv`
including `ANTHROPIC_API_KEY`; `server/internal/daemon/authprobe.go:310-336`
detects the user's own Claude subscription). **Agora's marginal LLM cost on that
path is exactly zero.**

That single fact settles most of the pricing question. Metering something that
costs us nothing is the ClickUp / monday mistake — a tax on the habit dressed as
a meter. So:

- **Flat per-human seat** for the product, because the product is what we build.
- **No meter at all** on the local path, because there is nothing to meter.
- **Pass-through metering only where we actually pay** — cloud runtime, and the
  Assistant when it runs on *our* provider key.
- **Agents are never a seat.** Linear, Atlassian, Salesforce and Dart have all
  already settled this; charging per agent would put us alone against the market.
- **Self-host stays free and fully featured, with no license key and no phone
  home.** Generosity there is the moat against the DIY Claude-Code roll, and the
  license already reserves the only right that matters (resale).

---

## Part 1 — What exists today (repo grounding)

### 1.1 Usage data: what is recorded, and at what grain

**`task_usage`** — `server/migrations/032_task_usage.up.sql:1-14`. One row per
`(task_id, provider, model)`:

```
task_id, provider, model,
input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
created_at            -- updated_at added by 072
UNIQUE (task_id, provider, model)
```

Grain: **per agent task, per provider, per model, tokens split four ways.** No
wall-clock, no cost, no workspace column (workspace is derived).

**`task_usage_hourly`** — `server/migrations/101_task_usage_hourly_schema.up.sql:37-55`.
The rollup, materialised in UTC, keyed:

```
(bucket_hour, workspace_id, runtime_id, agent_id, project_id, provider, model)
UNIQUE NULLS NOT DISTINCT
+ input/output/cache_read/cache_write tokens, task_count, event_count
```

Workspace attribution comes from `agent.workspace_id`, not from the issue
(`server/migrations/102_task_usage_hourly_pipeline.up.sql:292`), so **every agent
token is workspace-attributable even when the task has no issue.** `project_id`
is nullable and merges cleanly via `UNIQUE NULLS NOT DISTINCT`.

One caveat that matters for invoices: the rollup's recompute is fenced by
`WHERE atq.runtime_id IS NOT NULL` (`102_..._pipeline.up.sql`), so a task with no
runtime silently leaves the rollup. **Billing must read raw `task_usage`, never
the rollup, for an invoice line.**

**Wall clock** — `agent_task_queue.started_at` / `completed_at`
(`server/migrations/001_init.up.sql:135-136`). Runtime-minutes are derivable per
task today; `ListDashboardAgentRunTime` and `ListDashboardRunTimeDaily`
(`server/pkg/db/queries/task_usage.sql:89`, `:122`) already sum them.

**Run outcomes** — `agent_run_trace`
(`server/migrations/125_agent_run_trace.up.sql:14-33`) anchors one durable row
per terminal run with `workspace_id`, `agent_id`, `issue_id` and an
`outcome_label`. Built for the fine-tune flywheel, but it is also the cleanest
"one row per billable run" anchor that already exists.

**Attended vs unattended is already classified.**
`TaskService.taskMetricsContext` (`server/internal/service/task.go:333-355`)
returns `source ∈ {chat, issue, autopilot_issue, autopilot, quick_create, other}`
plus `runtimeMode ∈ {local, cloud}`. That is precisely the billable/non-billable
and attended/unattended axis the meter needs, and it exists and is used today.

**The single ingest seam** — `Handler.ReportTaskUsage`
(`server/internal/handler/daemon.go:2990`), route `POST /api/daemon/tasks/{taskId}/usage`
(`server/cmd/server/router.go:562`), write at `daemon.go:3028` via
`UpsertTaskUsage` (`server/pkg/db/queries/task_usage.sql:1-14`). The upsert is
**`DO UPDATE SET` — last-write-wins, not additive** — so the daemon reports
cumulative run totals and a re-report corrects rather than doubles. That is
already the right idempotency discipline and the meter should inherit it.

The daemon reports once per run, before any early return, deliberately including
cancelled runs (`server/internal/daemon/daemon.go:2395-2407`, with the comment
"dropping this on the cancelled path silently under-reports billing").

### 1.2 Two integrity holes on the billing-critical path

Both must be closed before any invoice is generated.

1. **No retry.** `Client.ReportTaskUsage` uses `postJSON`
   (`server/internal/daemon/client.go:393`) while `CompleteTask` on the very next
   concern uses `postJSONWithRetry(..., defaultTerminalRetrySchedule)`
   (`client.go:365`). A transient 502 loses the run's usage permanently.
2. **Server swallows persistence failure.** `daemon.go:3037-3039` logs
   `slog.Warn` and `continue`s, then returns `200 OK`. A DB error drops that
   model's usage and tells the daemon everything is fine.

Also note the asymmetry: `CaptureTaskUsage` (`server/internal/service/task.go:253`)
→ `Metrics.RecordLLMUsage` (`server/internal/metrics/business.go:253`) is an
**additive Prometheus counter** fired on the same call, so a re-report
double-counts Prometheus while the DB self-corrects. **Prometheus is
observability, never the invoice.**

### 1.3 Cost: computed nowhere authoritative, in two tables that disagree

There is no cost column anywhere in the database — not on `task_usage`, not on
`task_usage_hourly`. The decision was deliberate: "Cost is computed client-side
from a per-model pricing table"
(`server/internal/handler/dashboard.go:26`, restated at `runtime.go:189`).

There are two pricing tables and **they have drifted**:

| | Path | Entries | Consumer |
|---|---|---|---|
| Client | `packages/views/runtimes/utils.ts:150-234` | 46 models | every dollar figure the UI shows |
| Server | `server/internal/metrics/pricing.go:17-43` | 22 models | Prometheus only |

They disagree numerically — `deepseek-v4-flash` is `0.14 / 0.28` in the client
(`utils.ts:209`) and `0.56 / 1.12` in Go (`pricing.go:32`), a 4× gap; the client
file's own comment block notes DeepSeek promo pricing (`utils.ts:201-208`),
which is very likely the cause and is precisely the case an effective-dated
price table exists to handle. The server carries minimax/google SKUs the client
lacks; the client carries the GLM range the server lacks. Unmapped models resolve to **$0** silently
(`utils.ts:343-345`) after falling through a per-browser localStorage override
store (`packages/core/runtimes/custom-pricing-store.ts:29-55`).

The free model is already priced at $0 on both sides
(`pricing.go:42`, `agent.Model.Free` at `server/pkg/agent/models.go:34-39`,
`annotateAgoraFree` at `:477`), which is the right hook for "the branded free
base never enters a bill".

The assistant tool `usage_summary`
(`server/internal/handler/assistant_tools.go:1071-1143`) refuses to invent a
cost and says so to the model: *"Token counts only. This instance prices tokens
in the client, so no cost figure is available server-side."* That honesty is
correct today and is exactly what Phase 0 removes the need for.

### 1.4 Spend caps: the enforcement seam already exists

`resolveTaskBudgets` (`server/internal/handler/task_budget.go:107-123`) resolves
three per-task ceilings from the issue's tier labels and the project's config
overrides, at **claim time**, and rides them on the claim response
(`server/internal/handler/daemon.go:1535-1545`). Registry keys at
`server/internal/config/registry.go:65-70`:
`AGORA_TASK_BUDGET_USD_{TRIVIAL,LIGHT,MEDIUM,HEAVY}`, `AGORA_TASK_MAX_TURNS`,
`AGORA_TASK_TIMEOUT_MINUTES` — all `ProjectScoped`.

Three properties make this the template for entitlement enforcement:

- **Fail-open by construction.** `parseBudgetUSD` (`task_budget.go:91-101`):
  empty, unparsable and non-positive all mean *no cap*, never a cap of zero.
  "A cap nobody configured must never wedge a fleet" (`task_budget.go:63-66`).
- **Additive on the wire**, so an old daemon ignores the fields and keeps prior
  behaviour (`task_budget.go:23-26`).
- **A blown ceiling escalates to a human, never retries and never silently
  stops.** `ReasonBudgetExhausted` is deliberately excluded from
  `retryableReasons` (`server/pkg/taskfailure/failure.go:95-111`);
  `FailTask` calls `RaiseBudgetEscalation`
  (`server/internal/service/task.go:1722`,
  `server/internal/service/escalation.go:282-307`) which opens a
  `kind='budget'` row in `task_escalation`
  (`server/migrations/209_task_escalation.up.sql:24-60`) and puts the question in
  someone's inbox.

`docs/orchestration-upgrade-plan.md` §F1 already specifies the next two levels —
`AGORA_PROJECT_BUDGET_USD_MONTH` and `AGORA_WORKSPACE_BUDGET_USD_MONTH`,
enforced pre-claim in `ClaimTaskForRuntime`
(`server/internal/service/task.go:1119`). **Neither key is in the registry;
those caps are designed and unshipped.** Same doc, same section, states the
doctrine this plan must not contradict:

> **This is a cap, not a meter.** No credits, no AI-gated tier, no currency we
> mint. (`docs/orchestration-upgrade-plan.md:704-709`)

### 1.5 The unattended surfaces — where money is spent with nobody watching

| Surface | Tick / seam | Gate today |
|---|---|---|
| Scheduled report refresh | `RunDueReportSchedules` `server/internal/handler/assistant_schedules.go:383`; ticker `server/cmd/server/report_refresh_scheduler.go:31` (60s) | `AGORA_ASSISTANT_SCHEDULES_ENABLED` |
| Autopilots (cron) | `tickScheduledAutopilots` `server/cmd/server/autopilot_scheduler.go:72` (30s) | none |
| Automations (WHEN/IF/THEN) | `runAutomationsForEvent` `server/internal/handler/automation_engine.go:76` | per-rule `enabled` only |
| Auto-QA on in_review | `AGORA_AUTO_QA_ENABLED` | project-scoped flag |
| Attended agent dispatch | `enqueueIssueTask` `server/internal/service/task.go:509` | none |

The registry's own description of the schedules flag is the paid boundary stated
in the product's own words (`server/internal/config/registry.go:111`):

> "Each due schedule starts one ordinary assistant run under the report owner's
> identity, so this is **the one assistant surface that spends money with nobody
> watching** — turning it off here stops all of it instance-wide with no
> redeploy."

### 1.6 The Assistant is entirely unmetered

`assistant_run` (`server/migrations/197_assistant_run.up.sql:1-21`) has
`user_id`, `context_workspace_id`, `status`, `finished_at`, and a
`(session_id, request_id)` unique index for idempotency — a perfect billing
anchor. It has **no token columns**, and it never will get them by accident:
none of the three LLM clients parses a `usage` block at all. `chatResponse` in
`server/internal/integrations/llm/zhipu.go:68-75` reads only `choices` and
`error`; `anthropic.go` and `openai.go` contain no `usage`/`InputTokens`
reference. Assistant spend is currently invisible end-to-end.

Provider selection: `AGORA_ASSISTANT_PROVIDER` (default `zhipu`),
`AGORA_ASSISTANT_MODEL` (default `glm-4.5-flash`) —
`server/internal/config/registry.go:108-110`. `FreeModel = "glm-4.5-flash"` at
`server/internal/integrations/llm/zhipu.go:24`. So the assistant's *free* path
already runs on the branded Agora base, and the *paid* path is one config key
away.

### 1.7 Entitlement storage candidates — and why each existing one fails

| Candidate | Where | Verdict |
|---|---|---|
| `instance_config` | `server/migrations/153_instance_config.up.sql:6-11` | **No.** Flat global `key → value`, **no `workspace_id`**. Write authZ is "any workspace owner" (`server/internal/handler/instance_config.go:31-34, 48-51`). Structurally cannot express per-tenant state, and a tenant could grant themselves a plan. |
| `workspace.settings` JSONB | `server/migrations/001_init.up.sql:20`; merge helpers `MergeWorkspaceSettingsEntry` / `SetWorkspaceSettingKey` (`server/pkg/db/queries/workspace.sql:75-91`) | **No.** Returned **whole to every member** (`server/internal/handler/workspace.go:43, 52-72`) and self-serve writable via `UpdateWorkspace` (`workspace.sql:30`). A member could write their own plan. |
| `project.settings.config` | `server/migrations/123_project_settings.up.sql:9`; `SetProjectConfigKey` (`server/pkg/db/queries/project.sql:54-66`); filter `parseProjectConfigOverrides` (`server/internal/handler/project_config.go:47-73`) | **No.** Owner/admin self-serve editable by design. |

The **resolution pattern** is right, though, and should be copied exactly.
`config.resolve` (`server/internal/config/store.go:98-109`):
DB override → env → registry default. Scoped layer `ResolveFrom`
(`store.go:165-172`) puts a project map above it. Cross-replica propagation is a
30-second refresh loop (`store.go:52-58`) plus immediate local `NotifySet`
(`store.go:73`). Registry shape: 80 keys, 13 categories, `Kind ∈ {bool, int,
string, secret}`, secrets never DB-overridable and never returned
(`registry.go:14-37`).

The closest existing analogue to a plan tier is the import subsystem: one enable
flag plus six numeric ceilings — `AGORA_IMPORT_ENABLED`, `_MAX_ISSUES`,
`_MAX_ATTACHMENT_MB`, `_MAX_TOTAL_ATTACHMENT_MB`, `_MAX_ATTACHMENTS_PER_ISSUE`,
`_MAX_FAILURES`, `_JOB_TIMEOUT_MINUTES` (`registry.go:144-151`).

### 1.8 There is already a billing plane — for something else

A full prepaid-credit wallet exists and ships today, but it is **not** an Agora
subscription. It is a **proxy to the external agora-cloud service** that backs
cloud runtime:

- Routes: `server/cmd/server/router.go:1087-1097` —
  `/api/cloud-billing/{balance,transactions,batches,topups,price-tiers,checkout-sessions,portal-sessions}`,
  every one wrapped in `handler.RequireHumanActor` (`router.go:1088`).
- Handlers: `server/internal/handler/cloud_billing.go:47-171`. Agora owns no
  schema here; it forwards bytes to `/api/v1/billing/*`.
- Stripe webhook passthrough: `POST /api/webhooks/stripe`
  (`router.go:521`, handler `cloud_billing.go:211-294`) — unauthenticated by
  design, raw body byte-for-byte, `Stripe-Signature` forwarded verbatim, 1 MiB
  cap, per-IP rate limit. **This route is taken.**
- Types: `packages/core/types/billing.ts`. Unit convention at `:8-11` —
  "1 credit = 1_000_000 micro; 1 USD = 1000 credit". Transactions carry an
  `idempotency_key` (`:47`).
- UI: a self-described test page,
  `packages/views/billing/billing-test-page.tsx:1-24` — *"when the real billing
  UI ships it will live elsewhere and this whole page can be deleted."*
  Mounted at `apps/web/app/[workspaceSlug]/(dashboard)/billing/page.tsx`.

**This is a minted currency, and it is in direct tension with the standing "no
credits, no currency we mint" doctrine.** Resolution in §3.9 below: it is
confined to raw cloud compute, relabelled in dollars, and never extended to a
product feature.

`RequireHumanActor` (`server/internal/handler/actor_guards.go`) already exists
precisely because `mat_` task tokens and `mcn_` cloud PATs resolve to the owning
human's `X-User-ID`; without the guard a running agent could read its owner's
wallet or open a Checkout session. **Every billing mutation this plan adds gets
the same guard.**

### 1.9 The deployment split, and the no-phone-home precedent

- **Cloud**: `render.yaml` (backend on Render, `master`, Frankfurt,
  `autoDeployTrigger: checksPass`) and `deploy/fly/`. Cloud-only env —
  `AGORA_CLOUD_FLEET_URL` (`server/cmd/server/router.go:1845-1849`),
  `STRIPE_WEBHOOK_SECRET` (lives upstream, not in this repo at all),
  `POSTHOG_API_KEY`.
- **Self-host**: `make selfhost` / `selfhost-build` / `selfhost-stop`
  (`Makefile:56-161`), `docker-compose.selfhost.yml` (3 services, bound to
  `127.0.0.1` only, header comment at `:3-8`). **`AGORA_CLOUD_FLEET_URL`,
  `STRIPE*` and `POSTHOG*` are all absent from the self-host compose.** That
  absence is the existing mechanism for "cloud-only feature" — a capability that
  is simply unconfigured, returning 503, not a permission that is denied.

**Nothing phones home.** The precedent is written down in
`docs/analytics.md:36-52`: `POSTHOG_API_KEY` empty ⇒ `NoopClient`
(`server/internal/analytics/client.go:73-93`), and the key is served to the
frontend via `/api/config` *specifically* so a self-hosted server's blank value
disables browser event shipping too
(`server/internal/handler/config.go:54-58`). Daemon auto-update defaults **off**
for self-host (`server/internal/daemon/config.go:504-521`,
`isOfficialCloudServer`). Cloud runtime is disabled by an empty base URL —
`Enabled()` is one line (`server/internal/cloudruntime/client.go:107-109`) and
the proxy answers 503 (`server/internal/handler/cloud_runtime.go:104-107`).

**`/api/config`** (`server/internal/handler/config.go:96-146`) is the existing
"what is this deployment allowed to show" endpoint — `RemoteBoxesEnabled`,
`BitrixEnabled`, `SlackEnabled`, etc., each mirroring the *same* env gate the
backend enforces, so "the UI offers it" and "the server does it" cannot
disagree. It is public and unauthenticated, and its own comment forbids
tenant-scoped data (`config.go:96-99`) — so entitlements need a *different*,
workspace-scoped endpoint.

### 1.10 The licence already reserves the right that matters

`LICENSE:1-44` — a modified Apache 2.0 (Dify-style):

- **1a**: no hosted/embedded resale of Agora to third parties without a
  commercial licence — but *"Internal use within a single organization
  (including multiple workspaces) does not require a commercial license."*
- **1b**: logo/copyright must stay in `apps/web/`.
- **2a**: *"The producer can adjust the open-source agreement to be more strict
  or relaxed as deemed necessary."*

The commercial trigger is **redistribution, not feature use**. Meanwhile the
docs promise parity in writing:
`apps/docs/content/docs/self-host-quickstart.mdx:10` — *"Agent execution still
relies on the daemon you run locally … **exactly like Cloud**. Self-host swaps
out the server layer, not the execution layer."* Any feature gating in self-host
would be a visible posture reversal, not a clarification.

### 1.11 What is absent — the actual gap

A grep for `entitlement | licenseKey | license_key | subscription | quota`
across `server/internal` returns only unrelated hits (the orchestration "plan"
stage, issue subscriptions, assistant plan documents). Concretely missing:

- No plan object, no seat count (there is not even a `CountWorkspaceMembers`
  query — `server/pkg/db/queries/member.sql` has seven queries, none of them a
  count).
- No cost persisted anywhere.
- No per-workspace limit of any kind.
- No Stripe subscription integration (only the cloud-wallet passthrough).
- No Settings → Configs UI at all; `/api/instance-config` is API-only
  (`server/cmd/server/router.go:696-698`) and no frontend references it.
- Assistant spend unmeasured (§1.6).

---

## Part 2 — External evidence that changes the design

Full URLs and dates in Appendix A. Only findings that changed a decision are
here.

**1. Linear is the reference implementation, and it monetises the seat, not the
tokens.** Agents *"do not count as billable users"* and *"It does not cost
anything to develop agents in Linear."* The only two metered features are the
two with real marginal cost: coding sessions bill *"model tokens at
provider-published rates, **with no markup**, and sandbox runtime at **$0.25 per
20-minute block**"*, and Loops at *"$0.07–$0.20"* per run. The credit wallet is
workspace-pooled, **opt-in** (no funds ⇒ no access, no charge), minimum $10
ad-hoc / $50 auto-reload, expires 12 months after purchase, and *"Top-ups are
processed by Stripe and produce a **separate invoice** from your regular Linear
subscription invoice."* Seats are $10 / $16 per user/mo yearly.
→ **Changed:** we adopt the two-invoice split (flat subscription ⟂ opt-in
compute wallet) and the "markup on the thing that costs us wall-clock, zero
markup on tokens" rule verbatim.

**2. Linear also ships workspace *and* per-user spend limits with
daily/weekly/monthly resets** (changelog 2026-08-20), and seeded adoption with
*"$20 per seat in promotional credits"* that expired 2026-08-20 and were then
extended to 2026-12-31.
→ **Changed:** promotional credit with a hard expiry, extendable, is the
adoption mechanic. And per-user (not just per-workspace) spend limits are table
stakes.

**3. GitHub abandoned per-request multipliers for a dollar-denominated
credit — and kept the habit surface unmetered.** Business is $19/user/mo with
1,900 AI credits; Enterprise $39 with 3,900; overage **$0.01 per AI credit**
(verified verbatim). *"Code completions and next edit suggestions are not billed
in AI credits and remain unlimited for all paid plans."* Premium requests are
now described only as a legacy path for pre-2026-06-01 annual subscribers.
→ **Changed:** the brief's premise (multipliers per model) is out of date. More
importantly, the largest vendor in the category explicitly exempts the
habit-forming surface from the meter. The arithmetic ($19 seat → 1,900 credits →
$0.01/credit) implies the included allowance is exactly 100% of the seat price
rendered as credits — an elegant way to say "your seat includes its own price in
AI". Note: the 1 credit = $0.01 equivalence is *inferred* from two published
numbers, not quoted.

**4. Cursor's 2025 backlash was a communication failure, not a metering
failure.** Their own post-mortem (2025-07-04): *"We were not clear that
'unlimited usage' was only for **Auto** and not all other models"*, plus *"we
will refund any unexpected charges"* for 2025-06-16 → 07-04.
→ **Changed:** the kill list gains an explicit ban on unqualified "unlimited" in
any copy. Also note Cursor's margin mechanism — a **$0.25/M Cursor Token Rate on
third-party models, applied even to BYOK**. That is a real lever and we are
kill-listing it (§3.9), because taxing BYO-runtime attacks our own wedge.

**5. Anthropic runs two windows, not one — and the second one exists because of
agents.** Claude Code seat allowances reset on *"a rolling five-hour window and a
weekly window"*; usage inside the allowance *"isn't metered in dollars"* until
usage credits are enabled. The weekly limit was introduced 2025-07-28 (effective
2025-08-28) explicitly against users *"running Claude Code continuously in the
background, 24/7"*, hitting *"less than 5% of subscribers"*.
→ **Changed:** a fair-use ceiling for an agent-first product must have a
**weekly** component. A 5-hour window structurally cannot catch the unattended
24/7 case, which is exactly our paid surface. Also useful as a cost model:
Anthropic publishes *"$13 per developer per active day"*, *"$150–250 per
developer per month"*, *"below $30 per active day for 90% of users"*.

**6. Stripe has repositioned its usage-based stack, and our exact use case is
called out by name.** Verified verbatim on 2026-09-20: *"Metronome is Stripe's
primary usage-based billing platform, recommended for all new integrations.
Basic usage-based billing, built on the Billing Meters API, is a lower-level
primitive that remains fully supported for existing integrations."* The
recommendation list includes: *"Adding usage-based pricing to existing flat-rate
subscriptions — **this is the clearest signal to use Metronome** rather than
build on Billing Meters"*, plus prepaid credits, credit burndown, and *"real-time
usage visibility (Billing Meters only reconciles usage at invoice time)"*.
→ **Changed, decisively:** do **not** build on Billing Meters. And note the
counter-constraint — Metronome has limited support for Connect, Checkout,
Adaptive Pricing and Workflows, so the *flat seat* line stays on ordinary Stripe
Subscriptions + Checkout.

**7. Stripe cannot be our enforcement ledger, for two independent reasons.**
(a) *"Credits apply to invoices only at the time of finalization"* and customers
*"can exceed their balance during the cycle"* — there is no real-time hard stop.
(b) Credit grants apply **only to metered prices reported through Meters**, never
to licensed (per-seat) prices — so "your seat includes $X of compute" is not
expressible natively. Operationally, Billing Meters also allows only *"one
concurrent call per customer per meter"*, which an agent fleet finishing work
simultaneously would serialise or 429; Stripe's own advice is to pre-aggregate.
→ **Changed:** we own the ledger and the gate; Stripe is settlement only. This
is exactly Linear's architecture, and it is why their top-ups are a separate
invoice.

**8. Nobody charges a human-equivalent seat for an agent.** Linear: not billable
users. Atlassian Rovo: included in paid plans, agents draw from a shared org
credit pool, overage $0.01/credit. Salesforce Agentforce is the counter-example
and proves the rule — there *is* an "Agentforce User License at $5/user/month",
but at $5 it is a credential, and the revenue is the Flex Credit meter
($500/100,000 = $0.005/credit; an action is 20 credits = $0.10). Dart advertises
*"Unlimited AI model usage"* on all plans including free.
→ **Changed:** nothing — it confirms the given. But Intercom's Fin shape is worth
borrowing conceptually: **$0.99 per resolution, charged once per conversation
regardless of how many turns it took.** That is the only meter shape in the
category that is immune to the #1 credit-meter complaint, because cost tracks
*value delivered*, not agent chattiness.

**9. Two live self-host gating models, and only one is affordable.** GitLab uses
a 24-character activation code that **must reach `customers.gitlab.com:443`**,
with an offline licence file for air-gapped installs; on a fresh install *"only
Free features are enabled"*. n8n uses the Sustainable Use License: source is
open, *"To use source code files that contain '.ee.' in their filename … you
must hold a valid n8n Enterprise License"* — a **filename convention plus legal
enforcement, with zero licensing infrastructure**. Sentry's FSL converts to
Apache 2.0/MIT after two years and bans only *"Competing Use"*.
→ **Changed:** for a 2-10-person-team product, GitLab's model costs more to
build and operate than self-host revenue can plausibly return. n8n's is the
affordable shape. We take a third, cheaper option — see §3.3.

**10. Direct evidence for "AI credit meters churn customers" could not be
obtained in this pass** (search budget exhausted; only vendor pages were
reachable). The *only* first-party documented pricing backlash verified here is
Cursor's. The churn claim continues to rest on `docs/market-research-synthesis.md`
(2,933 Reddit comments, 26 HN threads) — which is a strong base, but this
document does not add independent confirmation. What vendor pages *do* show is
three different denominators for the same problem, and it is instructive:
ClickUp meters **per credit, per seat, on top of the seat** (Brain $9/user/mo
including 1,500 credits, extra at $10/10,000 = $0.001/credit); monday meters
**per credit, per account** (1,000 / 2,000 / 3,000 per month by tier); Intercom
meters **per outcome with no agent seat at all**.

---

## Part 3 — The plan

### 3.1 The offer

| | **Free** | **Team** | **Enterprise** |
|---|---|---|---|
| **Price** | $0 forever | **$12 / human / mo annual**<br>$15 monthly | custom, annual |
| **Humans (seats)** | unlimited | billed, flat | billed, flat |
| **Agents** | unlimited, **never a seat** | unlimited, never a seat | unlimited, never a seat |
| Issues, boards, projects, cycles, inbox, search | unlimited | unlimited | unlimited |
| Workspaces | 1 | unlimited | unlimited |
| **Attended agent runs on your own runtime** | **unlimited** | unlimited | unlimited |
| Agora Assistant — free Agora base (GLM) | unlimited (abuse rate limit) | unlimited | unlimited |
| Agora Assistant — premium model | — | included (instance key) or BYO key | BYO key / dedicated |
| **Scheduled report refreshes** | **1**, daily cadence only | unlimited | unlimited |
| **Automation rules** (WHEN/IF/THEN) | **2** | unlimited | unlimited |
| **Autopilots** (cron + webhook agent dispatch) | **0** | unlimited | unlimited |
| **Auto-QA on in_review** | — | included | included |
| Living-truth derivation (PR/CI → issue status) | — | included | included |
| Concurrent unattended runs (fair use) | n/a | 10 / workspace | negotiated |
| Spend caps + alerts (workspace, project, member) | view only | set + enforce | set + enforce |
| Usage history retention | 90 days | 24 months | custom |
| SSO / SCIM, audit log, DPA, invoicing | — | — | included |
| Support | community | email, 1 business day | SLA |
| **Cloud runtime compute** | opt-in wallet | opt-in wallet | opt-in wallet / committed |
| **Self-host** | **free, full features** | free, full features | + support contract |

**The line is: free forever to *use* agents; paid to *automate* them.**

#### Why per-human seat, flat, and not usage

Because usage on the default path costs us nothing (§0). A meter on
zero-marginal-cost work is a tax on the habit, which is the single most
consistently punished pricing decision in the corpus
(`docs/market-research-synthesis.md:41-47`). Flat also satisfies the
"predictable pricing" demand directly, and it is what the reference
implementation does.

**The trade-off, stated honestly:** a 2-person team running 40 agents pays for 2
seats. That is deliberate and it is the wedge — but it is not free for us: task
rows, WS fan-out, rollup work and DB storage all scale with agent activity, not
with seats. The mitigation is *not* a currency; it is a **fair-use admission
limit on concurrent unattended runs**, expressed in runs-in-flight (a resource),
never in dollars or credits. If that proves insufficient, the next lever is a
weekly unattended-run ceiling — following Anthropic, whose weekly window exists
for exactly this population — and *not* a per-run charge.

#### Why $12

Linear Basic $10 / Business $16 (yearly, per user). Dart $8 / $12. Plane $6 /
$13 annual. ClickUp $7 / $12. monday $9 / $12 / $19. Copilot Business $19.
Claude Team Standard $20 annual. $12 sits above the trackers and below the
AI-native seats, which is the honest position: Agora does more per seat than
Linear Basic (runtime, QA loop, automations, escalations) and is younger than
all of them. Undercutting Linear Basic by $2 is not a strategy; the research
says the demand is *predictability*, not *cheapness*. Sensitivity: $10 if we
want a clean "cheaper than Linear" line in the first 12 months; $15 once the
living-truth and review-routing gaps (G1/G3) ship. Do not launch a third paid
tier — three price points is already at the ceiling of the UI-simplicity rule.

#### Why the habit surface is never gated, and what "N runs/month" should be

The brief asks whether free should carry an N-agent-runs/month cap. **No, not on
the local path.** Three reasons:

1. It costs us nothing, so the cap would exist purely to create pain.
2. It caps the exact behaviour we need for the data flywheel — the branded free
   model feeding `agent_run_trace` outcomes
   (`server/migrations/125_agent_run_trace.up.sql:1-12`). A run cap is a cap on
   our own training data.
3. GitHub, the largest vendor in the category, explicitly exempts completions
   from its meter and keeps them *"unlimited for all paid plans"*. Gating the
   habit is a decision the market has already reversed.

**Yes, a cap on runs Agora funds.** Two of them: the Assistant on a premium
provider key (Team only), and the *unattended* dispatchers, which are the paid
line anyway. Free gets 1 scheduled report and 2 automation rules as a taste —
enough to feel the reporting-tax relief that `market-research-synthesis.md:22-28`
identifies as the one AI use people already love, and not enough to run a team's
process on.

#### The metered plane (cloud runtime only)

Orthogonal to the plan, opt-in, off by default, and never required to use
Agora:

- **Tokens: provider-published rates, zero markup.** Stated plainly in the UI.
- **Runtime: a published per-block price** for cloud sandbox wall-clock — the
  thing that actually costs us money. Linear's $0.25 / 20-minute block is the
  anchor; set ours from measured cloud-node cost + a stated margin.
- **Displayed in dollars.** The existing wallet's micro-credit unit
  (`packages/core/types/billing.ts:8-11`) stays as the storage unit; the UI shows
  USD only, per `orchestration-upgrade-plan.md:619-626`.
- **Separate invoice** from the subscription, per Linear.
- **Never buys a product feature.** A workspace with an empty wallet loses cloud
  runtime and nothing else.
- **Fairness exclusions, copied from Devin:** do not meter time waiting on a
  human, repo clone/setup, or test-suite execution. Do not meter a run that
  produced no output because the platform failed
  (`ReasonRuntimeOffline`, `ReasonRuntimeRecovery`,
  `ReasonQueuedExpired` — `server/pkg/taskfailure/failure.go:55-80`).

### 3.2 Seat definition

A **seat** is a row in `member` (`server/migrations/001_init.up.sql:26-33`) whose
`user_id` resolves to a human. Agents are rows in `agent`
(`001_init.up.sql:35-49`) and are never counted. Machine credentials (`mat_`
task tokens, `mcn_` cloud PATs) resolve to the owning human's `X-User-ID`
(`server/internal/handler/actor_guards.go`) and must be excluded from any seat
count by checking `X-Actor-Source`, not by counting distinct `user_id`s.

Seat changes are **soft and trued up**, never blocking: adding a member always
succeeds; the overage appears on the next invoice via Stripe proration. Blocking
an invite is blocking the habit.

Needs one new query — `CountWorkspaceMembers` — which does not exist today
(`server/pkg/db/queries/member.sql` has `ListMembers`, `GetMember`,
`GetMemberByUserAndWorkspace`, `CreateMember`, `UpdateMemberRole`,
`DeleteMember`, `ListMembersWithUser`).

### 3.3 Self-host licensing posture

**Decision: self-host stays free and fully featured. No licence key. No phone
home. No feature gate. Ever — not as a phase-4 option.**

The argument, from the DIY-competitor finding
(`docs/market-research-synthesis.md:32-38`): our real competitor is a weekend
Claude-Code-plus-MCP roll, and the DIY wave (Beads, Paca, dozens more) is the
reason "issues + AI" alone is not a moat. A self-hoster who chooses to run Agora
instead of writing their own is *already converted* on the thing that matters —
they are running our runtime, our QA loop, our escalation model, our issue
graph. Charging them $0 costs us nothing (they run their own Postgres, their own
daemon, their own tokens) and buys the one thing we cannot buy: a population
that has stopped building the competitor.

Three supporting reasons:

1. **It would be the first outbound call to Agora-owned infrastructure from a
   self-hosted server** (§1.9). Today the instance is genuinely silent. Breaking
   that is a trust change out of all proportion to the revenue.
2. **It reverses a written promise** —
   `apps/docs/content/docs/self-host-quickstart.mdx:10` says "exactly like
   Cloud". Reversals of this kind are named a churn trigger in the research
   ("change for its own sake").
3. **It is trivially defeated.** The source is available; a gate is a patch. The
   only durable enforcement is legal, and we already have it.

**We already own the right that earns money.** `LICENSE:10-20` forbids offering
Agora as a hosted service or embedding it in a commercial product. Internal use
by one organisation is explicitly free. So the self-host monetisation surface is:

- **Commercial licence** for anyone reselling/embedding Agora (the existing
  clause; a sales motion, not a code change).
- **Support/SLA contract** for self-hosters who want one (Enterprise row).
- **Cloud** for everyone who would rather not run Postgres.

If a future board demands self-host gating, the cheapest credible shape is
n8n's: a `.ee` path convention in the open repo plus a licence clause, with zero
licensing infrastructure. `LICENSE:34-35` already reserves the right to tighten.
That option is preserved and not exercised.

**Mechanism.** Entitlements are cloud-only *by construction*, using the existing
capability-not-permission pattern: a new `AGORA_BILLING_ENABLED` registry key
(default `false`, plus a required `STRIPE_SECRET_KEY` secret). When billing is
disabled — which is what the self-host compose ships, since it sets no Stripe
env at all — `entitlement.Resolve` returns the unlimited entitlement without
touching the database or the network. Precedent:
`analytics.NewFromEnv` returning `NoopClient` on an empty key
(`server/internal/analytics/client.go:73-93`).

### 3.4 The metering pipeline

#### Principle: derive, never dual-write

There is exactly one writer of billing facts, and it is a projector job, not a
request handler. Facts originate at the existing seams; the projector reads them
and materialises `usage_event`. This buys three things the repo already values:
idempotency by construction, replayability when pricing is wrong, and no new
failure mode on a write path. It is the same shape as
`rollup_task_usage_hourly_window` (advisory lock + watermark + dirty queue,
`server/migrations/102_task_usage_hourly_pipeline.up.sql:264-360`), which is
proven here.

#### Flow

```
FACT SOURCES                        PROJECTOR                 AGGREGATION          SURFACES
──────────────────────────────      ────────────────          ─────────────        ─────────────
task_usage                (032) ┐
agent_task_queue.started/         │
  completed_at            (001) ├→ usage_event      ────────→ usage_meter_daily ─→ /{slug}/usage
agent_runtime.runtime_mode(004) │  (append/upsert,            (day × ws ×           (cost only)
assistant_run + tokens    (197) │   idempotent on             project × kind      ─→ invoice lines
autopilot_run             (042) │   (source, source_id,        × model)           ─→ entitlement
assistant_report_schedule (204) │    kind, provider, model))                          counters
automation_run                  ┘        ▲
                                         │
                              model_price (effective-dated)
```

#### New tables

**Migration 211 — `model_price`** (next free number; latest is
`210_pr_changed_paths`).

```sql
CREATE TABLE model_price (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider                  TEXT NOT NULL,
    model_key                 TEXT NOT NULL,   -- canonical key, e.g. 'claude-sonnet-4.6'
    input_per_m_micros        BIGINT NOT NULL DEFAULT 0,
    output_per_m_micros       BIGINT NOT NULL DEFAULT 0,
    cache_read_per_m_micros   BIGINT NOT NULL DEFAULT 0,
    cache_write_per_m_micros  BIGINT NOT NULL DEFAULT 0,
    effective_from            TIMESTAMPTZ NOT NULL,
    effective_to              TIMESTAMPTZ,
    UNIQUE (provider, model_key, effective_from)
);
```

Effective-dating is non-negotiable for an invoice: a bill must be reproducible
from the prices in force *at the time of use*, not at read time. Seeded by
merging `server/internal/metrics/pricing.go:17-43` (22) with
`packages/views/runtimes/utils.ts:150-234` (46) and reconciling the four-fold
DeepSeek disagreement by hand. The alias regexes
(`metrics/pricing.go:45-75`) move to a `model_alias` lookup or stay in Go as the
canonicaliser — either is fine; the *prices* must be in the DB.

**This reverses a deliberate decision** (`dashboard.go:26`, `runtime.go:189`:
cost is client-side "so pricing changes don't need a deploy"). The reversal is
justified because an effective-dated table keeps the no-deploy property *and*
adds server authority, single-sourcing and reproducibility — all three of which
an invoice requires and a browser constant cannot provide.

**Migration 213 — `usage_event`** (the normalised meter).

```sql
CREATE TABLE usage_event (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    occurred_at          TIMESTAMPTZ NOT NULL,
    kind                 TEXT NOT NULL,   -- agent_run | assistant_run | qa_run
                                          -- | autopilot_run | schedule_run | runtime_minutes
    source               TEXT NOT NULL,   -- task | assistant_run | autopilot_run | schedule
    source_id            UUID NOT NULL,
    attended             BOOLEAN NOT NULL,       -- from taskMetricsContext (task.go:333)
    runtime_mode         TEXT NOT NULL DEFAULT '', -- local | cloud → who paid
    provider             TEXT NOT NULL DEFAULT '',
    model                TEXT NOT NULL DEFAULT '',
    input_tokens         BIGINT NOT NULL DEFAULT 0,
    output_tokens        BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens    BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens   BIGINT NOT NULL DEFAULT 0,
    runtime_seconds      BIGINT NOT NULL DEFAULT 0,
    run_count            BIGINT NOT NULL DEFAULT 1,
    provider_cost_micros BIGINT NOT NULL DEFAULT 0, -- what the tokens cost, always computed
    billable_micros      BIGINT NOT NULL DEFAULT 0, -- what WE charge; 0 on the local path
    price_id             UUID REFERENCES model_price(id),
    project_id           UUID,
    agent_id             UUID,
    actor_user_id        UUID,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, source_id, kind, provider, model)
);
CREATE INDEX idx_usage_event_ws_time   ON usage_event (workspace_id, occurred_at DESC);
CREATE INDEX idx_usage_event_ws_kind   ON usage_event (workspace_id, kind, occurred_at DESC);
CREATE INDEX idx_usage_event_project   ON usage_event (workspace_id, project_id, occurred_at DESC)
    WHERE project_id IS NOT NULL;
```

**Idempotency discipline**, inherited verbatim from `UpsertTaskUsage`:
`ON CONFLICT (source, source_id, kind, provider, model) DO UPDATE SET` with
**assignment, never addition**, and `updated_at = now()` bumped on both branches
so the daily rollup sees the correction — exactly the reasoning at
`server/pkg/db/queries/task_usage.sql:2-5`. Re-running the projector over any
window is therefore a no-op when nothing changed and a correction when something
did.

Two separate cost columns matter. `provider_cost_micros` is always computed (it
is what the user's spend page shows, and on the local path it is *their* money —
which is the honest and more useful number). `billable_micros` is what appears on
an Agora invoice and is **0 whenever `runtime_mode = 'local'`**. Conflating them
is how a spend page turns into a bill the user does not owe.

**Migration 214 — `usage_meter_daily`** + `usage_meter_state` (watermark) +
`usage_meter_dirty` (invalidation queue), structurally identical to
`101_task_usage_hourly_schema.up.sql:37-97`, keyed
`(day, workspace_id, project_id, kind, provider, model)` with token sums,
`runtime_seconds`, `run_count`, `provider_cost_micros`, `billable_micros`.

Daily, not hourly: nothing in billing needs an hour, the surface is
cost-by-period, and a smaller table is a cheaper invoice query. Buckets are UTC;
the viewer's timezone is applied at query time with
`DATE(day AT TIME ZONE @tz)` — the same reasoning as
`101_..._schema.up.sql:8-20`.

#### The projector job

Registered on the existing DB-backed scheduler
(`server/cmd/server/main.go:472-480`), following
`scheduler.TaskUsageHourlyJob` (`server/internal/scheduler/jobs_task_usage.go:36-56`)
exactly: `Cadence: 5 * time.Minute`, `ScheduleDelay: 5m`,
`CatchUpMode: CatchUpLatestOnly`, `CatchUpWindow: 24h`, `RunTimeout: 25m`,
`StaleTimeout: 30m`, `HeartbeatInterval: 30s`, `MaxAttempts: 3`,
`RetryBackoff: 1m/5m/15m`, `Scopes: StaticScopes(ScopeGlobal)`. A `pg_try_advisory_lock`
inside SQL, a trailing 5-minute lag so in-flight writes are never read, a
one-day-per-tick catch-up cap, and a watermark that only advances on success —
all copied from `102_..._pipeline.up.sql:436-496`.

New file: `server/internal/scheduler/jobs_usage_meter.go`.

#### Event seams (where facts originate)

| Fact | Seam |
|---|---|
| Agent tokens | `Handler.ReportTaskUsage` `server/internal/handler/daemon.go:2990`, write `:3028` |
| Agent wall-clock | `agent_task_queue.started_at/completed_at` `server/migrations/001_init.up.sql:135-136` |
| Attended / unattended | `taskMetricsContext` `server/internal/service/task.go:333-355` |
| local / cloud | `agent_runtime.runtime_mode` `server/migrations/004_agent_runtime_loop.up.sql:6` |
| Assistant run | `assistant_run` `server/migrations/197_assistant_run.up.sql:1-21` (needs token columns) |
| Assistant tokens | `server/internal/integrations/llm/{zhipu,openai,anthropic}.go` — **no client parses `usage` today** |
| Autopilot run | `autopilot_run` (042); tick `server/cmd/server/autopilot_scheduler.go:72` |
| Schedule run | `assistant_report_schedule` (204); claim `server/internal/handler/assistant_schedules.go:489` |
| Automation run | `recordAutomationRun` `server/internal/handler/automation_engine.go:283` |

#### Integrity fixes that gate everything downstream

1. `server/internal/daemon/client.go:393` — `postJSON` → `postJSONWithRetry(...,
   defaultTerminalRetrySchedule)`, matching `CompleteTask` at `:365`.
2. `server/internal/handler/daemon.go:3037-3039` — on upsert failure return 500
   so the daemon's retry fires, instead of `slog.Warn` + `continue` + 200.
3. Read raw `task_usage` for invoice lines, or fix the
   `atq.runtime_id IS NOT NULL` fence in the hourly rollup. Do not silently drop
   runtime-less tasks from a bill.
4. Reconcile `metrics/pricing.go` and `views/runtimes/utils.ts` before either is
   allowed to produce a number a customer sees.
5. Keep Prometheus as observability. `agora_llm_cost_usd_total`
   (`server/internal/metrics/business.go:117-123`) is additive and will diverge
   from the DB on re-reports. Never reconcile an invoice against it.

### 3.5 Entitlements

#### Shape

```sql
-- Migration 215
CREATE TABLE workspace_entitlement (
    workspace_id           UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    plan                   TEXT NOT NULL DEFAULT 'free',     -- free | team | enterprise
    status                 TEXT NOT NULL DEFAULT 'active',   -- active | trialing | past_due | canceled
    seats_paid             INT  NOT NULL DEFAULT 0,
    limits                 JSONB NOT NULL DEFAULT '{}',      -- resolved numeric ceilings
    features               JSONB NOT NULL DEFAULT '{}',      -- boolean capabilities
    trial_ends_at          TIMESTAMPTZ,
    current_period_end     TIMESTAMPTZ,
    grace_until            TIMESTAMPTZ,
    stripe_customer_id     TEXT,
    stripe_subscription_id TEXT,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`limits` holds `{schedules, automations, autopilots, workspaces,
concurrent_unattended_runs, usage_history_days}`. `features` holds
`{auto_qa, living_truth, premium_assistant, sso, audit_log, spend_caps}`.
Plan **defaults** live in a Go catalog (`server/internal/entitlement/plans.go`),
so a plan change is a code change with a test, and the row only carries
deviations — a negotiated Enterprise limit, a grandfathered free workspace.

**Why a dedicated table and not the three existing stores:** each of the three
is either global with no tenant column, or tenant-writable. See §1.7. Concretely:
`instance_config` has no `workspace_id` and lets any workspace owner write it
(`server/internal/handler/instance_config.go:48-51`); `workspace.settings` is
returned whole to every member and is writable through `UpdateWorkspace`. Either
choice would let a tenant grant themselves a plan. **`workspace_entitlement` is
written by exactly two paths — the Stripe webhook and an internal admin path —
and by no tenant-facing endpoint ever.**

#### Resolution

`server/internal/entitlement/resolve.go`, mirroring `config.resolve`
(`server/internal/config/store.go:98-109`):

```
Resolve(ctx, wsID) →
  1. billing disabled (AGORA_BILLING_ENABLED false / no STRIPE_SECRET_KEY)
        → Unlimited{}          // self-host and local dev. No DB read. No network.
  2. row in workspace_entitlement
        → plan defaults, overlaid by the row's limits/features
  3. no row
        → plan defaults for 'free'
```

Cached per workspace with a short TTL and invalidated on webhook, following the
`config` store's 30-second refresh plus immediate `NotifySet`
(`store.go:52-58`, `:73`). Every read is best-effort: **a resolver error returns
the permissive value and logs.** An entitlement system that fails closed on a DB
blip takes down the product to protect revenue that was not at risk.

#### Enforcement seams

Checked at **admission**, never mid-flight.

| # | Seam | File:line | Behaviour on limit |
|---|---|---|---|
| 1 | Scheduled report refresh | `RunDueReportSchedules` `server/internal/handler/assistant_schedules.go:383` — after the `AGORA_ASSISTANT_SCHEDULES_ENABLED` gate, per due row | Skip, `recordReportScheduleOutcome(... status=blocked, "plan limit")` (`:505`), advance `next_run_at` so it does not spin |
| 2 | Autopilot cron | `tickScheduledAutopilots` `server/cmd/server/autopilot_scheduler.go:72` | Do not claim the trigger; advance next run; one inbox notice per workspace per day |
| 3 | Automation engine | `runAutomationsForEvent` `server/internal/handler/automation_engine.go:76` — `ev.Issue.WorkspaceID` is already in hand | Evaluate the first N enabled rules by creation order; record `blocked` runs for the rest |
| 4 | Auto-QA | the `AGORA_AUTO_QA_ENABLED` resolution path | Resolve to `false` when the feature is not entitled |
| 5 | Premium Assistant | `GetAssistantAvailability` `server/internal/handler/assistant.go:792` + provider selection | Fall back to the free Agora base. **Never disable the assistant.** |
| 6 | Seats | `CreateMember` (`server/pkg/db/queries/member.sql:14`) + invite accept | **Never block.** Record overage; true up via Stripe proration |
| 7 | Attended agent dispatch (local) | `enqueueIssueTask` `server/internal/service/task.go:509`; claim `ClaimTaskForRuntime` `server/internal/service/task.go:1119` | **No entitlement gate. Ever.** |
| 8 | Cloud-runtime compute | upstream wallet, already enforced by agora-cloud | Existing behaviour |

Seams 1–4 are the paid line and they are all *ticks* — cheap, idempotent, and
already built to no-op silently when a gate is off. Seam 7 is the habit and is
explicitly out of scope.

`ClaimTaskForRuntime` (`task.go:1119`) is also where
`docs/orchestration-upgrade-plan.md` §F1 places the pre-claim project/workspace
**spend caps**. Those are an *owner-set* ceiling, not an entitlement, and they
should ship with the same resolution shape but a separate config path
(`AGORA_PROJECT_BUDGET_USD_MONTH`, `AGORA_WORKSPACE_BUDGET_USD_MONTH`,
still unregistered). Do not conflate them: one is "how much I chose to spend",
the other is "what I bought". Both escalate the same way.

#### Grace behaviour

Modelled on the blown-budget design, because it is already the right answer.

- **80% of a countable limit** → an inbox notice to owners/admins. No behaviour
  change. No modal, no in-product upsell banner (the research names AI upsell
  prompts as nag-ware, `market-research-synthesis.md:86`).
- **100%** → new *unattended* work is **not dispatched but not lost**: the
  trigger's next run advances, the outcome row records `blocked` with a plain
  reason, and the surface says so where the user already looks (the schedule
  row, the autopilot run list). Silence is indistinguishable from a broken
  daemon — the same argument as `orchestration-upgrade-plan.md:701-703`.
- **A run already in flight is never killed.** That compute is already paid for;
  killing it wastes the money *and* produces an unreviewable half-change. The
  per-task budget design makes the same choice: the run ends, then a human is
  asked (`server/internal/service/escalation.go:276-281`).
- **A limit blown mid-task** (only possible on the cloud-wallet path) reuses
  `RaiseBudgetEscalation` (`server/internal/service/escalation.go:282-307`)
  verbatim — `kind='budget'`, not auto-retried, in an inbox.
  `task_escalation.kind` already permits `'budget'`
  (`server/migrations/209_task_escalation.up.sql:36-38`). Workspace-level limits
  have no issue to hang on, so they use the inbox and the usage page instead —
  do not widen the escalation CHECK constraint for them.
- **`past_due`** → 14-day `grace_until`. During grace everything keeps working.
  At expiry, **unattended automation pauses and nothing else does.** Reads,
  writes, issues, boards, attended agent runs and the free Assistant stay up
  permanently, on any plan, in any billing state. **Locking the tracker on a
  failed card is the single highest-churn action available to us and it is on
  the kill list.**

### 3.6 Billing integration

**Two planes, two Stripe integrations, deliberately.**

| | Flat seat line | Metered compute line |
|---|---|---|
| Stripe product | Subscriptions + Checkout + Billing Portal, **licensed** price | Metronome (Stripe's primary usage platform) |
| When | Phase 3 | Phase 5, only if cloud runtime becomes material |
| Why not the other | Metronome has limited Checkout/Connect/Workflows support | Stripe's own docs: adding usage to flat-rate subscriptions is *"the clearest signal to use Metronome"* |
| Invoice | the subscription invoice | a **separate** invoice (Linear's model) |

**Do not build on Billing Meters.** Stripe now calls it "a lower-level primitive
that remains fully supported for existing integrations" and we have no existing
integration. Three concrete disqualifiers beyond the recommendation itself:
credits reconcile only at invoice finalisation (no real-time gate); credit grants
cannot attach to licensed per-seat prices, so "your seat includes $X of compute"
is inexpressible; and the one-concurrent-call-per-customer-per-meter limit is
hostile to a fleet of agents finishing simultaneously.

**We own the ledger; Stripe settles.** `usage_event` + `workspace_entitlement`
are the source of truth and the enforcement point. Stripe never gates anything
in real time — it cannot. This is exactly why Linear runs its own credit balance
and uses Stripe only for the top-up invoice.

**Webhooks.**

- New route `POST /api/webhooks/billing` with its own `STRIPE_BILLING_WEBHOOK_SECRET`.
  **It must not reuse `/api/webhooks/stripe`** — that path is already the raw
  passthrough to agora-cloud (`server/cmd/server/router.go:521`,
  `server/internal/handler/cloud_billing.go:211`). Two Stripe accounts, two
  secrets, two routes.
- Copy the existing discipline verbatim from `cloud_billing.go:173-259`: outside
  the Auth group, per-IP rate limit **before** reading the body, mandatory
  `Stripe-Signature` (401 if absent), `MaxBytesReader` at 1 MiB, body verified
  byte-for-byte with no unmarshal/re-marshal.
- Events: `checkout.session.completed`,
  `customer.subscription.{created,updated,deleted}`, `invoice.paid`,
  `invoice.payment_failed`.
- **Idempotency**: migration 216 `billing_webhook_event(stripe_event_id TEXT
  PRIMARY KEY, type TEXT, received_at, processed_at, error TEXT)`. Insert first;
  a conflict means already seen; process, then stamp. Stripe redelivers, and a
  redelivered `subscription.updated` must not double-apply a seat change.
- Handle **out-of-order delivery**: apply only if the event's
  `created` is newer than the row's `updated_at`, or compare the subscription
  object's own version. Webhook ordering is not guaranteed.
- Every mutating billing endpoint gets `handler.RequireHumanActor`, matching
  `router.go:1088` and reasoned at `server/internal/handler/actor_guards.go`.
  A prompt-injected agent must never be able to open a Checkout session.

**Self-host carve-out: honour system.** No licence key, no activation call, no
validation endpoint (§3.3). `AGORA_BILLING_ENABLED` is absent from
`docker-compose.selfhost.yml`, so `entitlement.Resolve` short-circuits to
unlimited on step 1 and no code path touches Stripe. The argument against
GitLab's model is cost: it requires running a licensing service and a
customers-portal, and it makes the self-hosted server call us — the exact
property we are preserving.

### 3.7 The spend-visibility surface

The specification is already written, in the repo, in
`docs/orchestration-upgrade-plan.md:737-743`:

| Allowed | Forbidden |
|---|---|
| Spend by project, by runtime, by period | Issues closed per agent |
| Open escalations and their age | Tasks or PRs per day |
| Review-queue depth and age | **Tokens per agent as a leaderboard** |
| Rework-after-merge, split by risk tier | Any per-human comparison |
| Runtime health and degradation | Any "agent productivity" framing |

> *measure the queue and the money, never the people*

**We currently violate it in three places, and all three must be removed before
a pricing page exists:**

1. **The agent leaderboard.**
   `packages/views/dashboard/components/dashboard-page.tsx:610-737`, rendered at
   `:411-416`. Ranked table of agents with a user-chosen sort over
   `"tokens" | "cost" | "time" | "tasks"` (`:613`, map `:615-620`), **default
   sort `"tokens"`** (`:632`), columns Agent / bar / Tokens / Cost / Time / Tasks
   (`:684-690`). This is the forbidden row, verbatim, with a progress bar.
   → **Delete.** Replace with **Spend by project** and **Spend by runtime**,
   both of which the data already supports — `task_usage_hourly` carries
   `project_id` in its key with a partial index
   (`server/migrations/101_task_usage_hourly_schema.up.sql:41`, `:80-82`).
2. **The agents-list RUNS column and sort-by-runs.**
   `packages/views/agents/components/agent-columns.tsx:127-138` and
   `agents-page.tsx:388-392`, fed by `GET /api/agent-run-counts`
   (`server/cmd/server/router.go:1636`, handler `agent.go:1523`).
   → **Delete the column, the sort option and the endpoint.** Agent *health*
   (online/offline, last seen, error state) stays; agent *throughput* goes.
3. **The `usage_summary` assistant tool leaks the leaderboard verbally.**
   `server/internal/handler/assistant_tools.go:1071-1143` returns `by_agent[]`
   with resolved **agent names** (`:1107-1116`), so the assistant can narrate the
   ranking a deleted UI no longer shows.
   → Drop `by_agent`; keep `by_day` and add `by_project`. Update the tool
   description at `server/internal/assistant/tools.go:1824-1843`.

**The workspace usage page** — `/{slug}/usage`
(`apps/web/app/[workspaceSlug]/(dashboard)/usage/page.tsx` →
`@agora/views/dashboard`) — becomes the money page:

- **What you're on**: plan, human seats in use vs paid, current period end.
- **What it cost**: this period's spend by project, by period, by model. Two
  clearly separated numbers — **"Your provider spend"** (`provider_cost_micros`,
  the user's own money on the local path, informational) and **"Billed by
  Agora"** (`billable_micros`, what appears on the invoice). Never one blended
  figure.
- **Caps**: workspace / project / member spend caps with alert thresholds
  (Linear ships per-user limits with daily/weekly/monthly resets; match that).
- **Never**: any per-agent or per-human ranking, any tasks/day figure attributed
  to a person or agent, any "productivity" framing.

**Who sees what.** Spend stays visible to every member — the existing choice
(`server/internal/handler/dashboard.go:29-33`: "token spend / run time are
workspace-level operational metrics") is right, and cost transparency inside a
2-10 person team is a feature, not a leak. Invoices, payment methods, plan
changes and the Billing Portal are **owner/admin only** via
`requireWorkspaceRole` (`server/internal/handler/handler.go:630`) and
`RequireHumanActor`.

**Route.** `/{slug}/settings/billing` for the money-movement surface (workspace
scoped, per `apps/docs/content/docs/developers/conventions.mdx:24`), replacing
the self-described test page at
`apps/web/app/[workspaceSlug]/(dashboard)/billing/page.tsx` →
`packages/views/billing/billing-test-page.tsx`. `billing` and `pricing` are
already reserved slugs — `pricing` is in
`server/internal/handler/reserved_slugs.json`; **add `billing` and `usage` to
that JSON and re-run `pnpm generate:reserved-slugs`** if a root-level route is
ever wanted.

### 3.8 Kill list

Things we do not build, with the reason each one is tempting.

1. **No per-agent seat, and no per-agent SKU.** Tempting because agents are the
   product. Fatal because Linear, Atlassian, Salesforce and Dart have all
   already priced them at zero.
2. **No credit meter for product features.** No "Agora credits" for a report, an
   automation or an issue. The existing cloud wallet is confined to raw compute
   and never extended (§3.1).
3. **No AI-gated tier.** "AI costs extra" on the habit surface is the ClickUp
   Brain shape. GitHub, the biggest vendor here, keeps completions unlimited and
   unmetered on every paid plan.
4. **No throughput dashboard, leaderboard, or "agent productivity" anything** —
   including the three we ship today (§3.7).
5. **No usage-based pricing on the local-daemon path.** It costs us nothing;
   charging for it is a tax on the wedge.
6. **No hard kill of a running task on a limit.** Admission-time only.
7. **No self-host licence key, activation call, or feature gate.** It would be
   our first phone-home, it reverses a written parity promise, and it is a patch
   away from defeated.
8. **No unqualified "unlimited" in any copy.** Cursor's exact failure, in their
   own words: *"We were not clear that 'unlimited usage' was only for Auto."*
   Every "unlimited" carries its qualifier in the same sentence.
9. **No BYOK tax.** Cursor charges $0.25/M even on bring-your-own-key. It is
   real money and it directly attacks our BYO-runtime thesis.
10. **No Stripe Billing Meters.** Superseded by Metronome for exactly our shape,
    and structurally unable to gate in real time.
11. **No trial that expires the tracker.** A trial may expire *automation*.
    Issues and boards never go read-only.
12. **No pricing page before §3.10 is green.**
13. **No annual-only tier below Enterprise, and no "contact sales for price".**
    Predictability is the demand.
14. **No in-product upsell prompts or plan nag modals.** Named nag-ware in the
    research. The usage page states the facts; it does not sell.

### 3.9 Resolving the tension with the existing credit wallet

`docs/orchestration-upgrade-plan.md:704-709` says *"no credits, no currency we
mint"*, and `packages/core/types/billing.ts:8-11` mints micro-credits at
1 USD = 1000 credit. Both are shipped. The resolution:

- The wallet's **scope is frozen** at raw cloud-runtime compute. It never buys a
  feature, a seat, a report or an automation.
- Its **UI is dollars.** The credit unit stays as the upstream storage unit;
  `billing-test-page.tsx` is replaced by a surface that shows USD only, per
  `orchestration-upgrade-plan.md:619-626`.
- The doctrine is **amended, not contradicted**, and the amendment belongs in
  that document: *no minted currency for product features; a prepaid dollar
  balance for raw third-party compute you explicitly opted into is a wallet, not
  a meter.* Linear draws the same line and survives the same critique.

### 3.10 Sequencing

**Phase 0 — make the number trustworthy.** No user-visible change. Nothing below
may start until this is green.

| PR | Scope |
|---|---|
| 0.1 | `server/internal/daemon/client.go:393` → `postJSONWithRetry`; `server/internal/handler/daemon.go:3037` → 500 on persist failure; Go tests for both |
| 0.2 | Migration 211 `model_price` + seed reconciling `metrics/pricing.go:17-43` with `views/runtimes/utils.ts:150-234`; `server/internal/pricing` resolver (`PriceAt(provider, model, t)`); dashboard/runtime endpoints return `cost_usd_micros`; **delete** `MODEL_PRICING`, `resolvePricing`, `estimateCost` and `packages/core/runtimes/custom-pricing-store.ts`; keep the unpriced-model diagnostic server-side (`agora_llm_unpriced_tokens_total` already exists at `metrics/business.go:124`) |
| 0.3 | Migration 212: token columns on `assistant_run`; parse `usage` in `server/internal/integrations/llm/{zhipu,openai,anthropic}.go`; persist per run |

**Phase 1 — the meter.**

| PR | Scope |
|---|---|
| 1.1 | Migration 213 `usage_event` + sqlc queries |
| 1.2 | `server/internal/scheduler/jobs_usage_meter.go` projector, registered at `server/cmd/server/main.go:473`; idempotent upsert; replay CLI (`server/cmd/backfill_usage_meter/`, modelled on `server/cmd/backfill_task_usage_hourly/main.go`) |
| 1.3 | Migration 214 `usage_meter_daily` + state + dirty queue + window function |
| 1.4 | `/{slug}/usage` rework: cost by project / period / model; **delete** the leaderboard (`dashboard-page.tsx:610-737`), the RUNS column (`agent-columns.tsx:127-138`), the sort (`agents-page.tsx:388-392`) and `/api/agent-run-counts` (`router.go:1636`); drop `by_agent` from `usage_summary` (`assistant_tools.go:1071-1143`) |

**Phase 2 — entitlements, resolving unlimited.** Ship dark.

| PR | Scope |
|---|---|
| 2.1 | Migration 215 `workspace_entitlement`; `server/internal/entitlement/{plans.go,resolve.go}`; `AGORA_BILLING_ENABLED` in `server/internal/config/registry.go`; `CountWorkspaceMembers` in `server/pkg/db/queries/member.sql` |
| 2.2 | Wire seams 1–5 (§3.5) with every limit resolving unlimited; tests asserting each seam no-ops when billing is disabled |
| 2.3 | `GET /api/workspaces/{id}/entitlement` (workspace-scoped, **not** `/api/config`); `packages/core/entitlement/` query + `packages/views` reads it to label surfaces |

**Phase 3 — billing.**

| PR | Scope |
|---|---|
| 3.1 | Stripe Subscriptions + Checkout + Billing Portal for the flat seat line; `RequireHumanActor` on every mutation |
| 3.2 | Migration 216 `billing_webhook_event`; `POST /api/webhooks/billing` with its own secret; out-of-order and redelivery handling; a test feeding a malformed webhook body through the parser |
| 3.3 | `/{slug}/settings/billing` replacing `packages/views/billing/billing-test-page.tsx`; seat true-up via proration |

**Phase 4 — flip the limits on.** 60 days' notice. Grandfather every existing
workspace at its current usage. Publish the pricing page **only after §3.10's
gate list is green**.

**Phase 5 — metered compute (conditional).** Only if cloud runtime becomes a
material cost centre. Metronome for the usage line; separate invoice; published
per-block runtime price; tokens at provider rates with zero markup.

#### Gate: nothing ships a public pricing page until all of these are true

1. Phase 0 complete — usage reporting retries, persistence failure is loud, and
   there is exactly **one** pricing table in the system.
2. `usage_event` reconciles against raw `task_usage` to the cent over a 30-day
   window, twice, with a re-run of the projector producing zero diff.
3. `provider_cost_micros` and `billable_micros` are separately displayed and
   never blended in any surface.
4. The three throughput violations (§3.7) are deleted from code, not hidden.
5. `entitlement.Resolve` makes **zero** DB reads and **zero** network calls when
   `AGORA_BILLING_ENABLED` is false, proven by a test.
6. A self-host smoke run (`make selfhost-build`) shows no outbound request to any
   Agora-owned host, and no feature difference from cloud.
7. Every enforcement seam has a test proving a blown limit **queues or skips and
   never kills a running task**.
8. Webhook idempotency has a test proving a redelivered
   `customer.subscription.updated` does not double-apply.
9. Every "unlimited" in the copy carries its qualifier in the same sentence.
10. The pricing page states, in plain words: agents are free; tokens are at
    provider rates with no markup; self-host is free and full-featured.

---

## Appendix A — Sources

All URLs fetched **2026-09-20**. Publication dates given where the page states
one; otherwise the page carried no date.

**Linear**
- Pricing — https://linear.app/pricing (no date on page)
- AI credits — https://linear.app/docs/ai-credits (no date on page)
- Agents (developer docs) — https://linear.app/developers/agents (no date on page)
- Changelog — https://linear.app/changelog (entries dated in-page: 2026-07-20 Loops launch; 2026-08-20 coding-session pricing + spend limits; 2026-09-14 credit extension)

**Cursor**
- "Clarifying our pricing" — https://cursor.com/blog/june-2025-pricing (published **2025-07-04**)
- Pricing — https://cursor.com/pricing
- Pricing docs — https://cursor.com/docs/account/pricing

**Devin / Cognition**
- Pricing — https://devin.ai/pricing
- Self-serve billing — https://docs.devin.ai/admin/billing/self-serve
- Usage — https://docs.devin.ai/admin/billing/usage

**Replit**
- "Effort-based pricing" — https://replit.com/blog/effort-based-pricing (rollout stated as 2025-07-01 for existing Core/Teams)

**GitHub Copilot**
- Individual plans billing — https://docs.github.com/en/copilot/concepts/billing/individual-plans
- Organizations and enterprises billing — https://docs.github.com/en/copilot/concepts/billing/organizations-and-enterprises
- Copilot requests (legacy) — https://docs.github.com/en/copilot/concepts/billing/copilot-requests (describes the pre-**2026-06-01** request model)

**Anthropic / Claude Code**
- Pricing — https://claude.com/pricing
- Claude Code cost management — https://code.claude.com/docs/en/costs
- Extra usage for paid plans — https://support.claude.com/en/articles/12429409-extra-usage-for-paid-claude-plans (article dated **2026-08-10**)
- TechCrunch, weekly rate limits — https://techcrunch.com/2025/07/28/anthropic-unveils-new-rate-limits-to-curb-claude-code-power-users/ (published **2025-07-28**, limits effective **2025-08-28**)

**Stripe and alternatives**
- Basic usage-based billing (Metronome repositioning) — https://docs.stripe.com/billing/subscriptions/usage-based
- Compare Billing Meters and Metronome — https://docs.stripe.com/billing/subscriptions/usage-based/compare-metronome
- Recording usage via API — https://docs.stripe.com/billing/subscriptions/usage-based/recording-usage-api
- Billing credits / credit grants — https://docs.stripe.com/billing/subscriptions/usage-based/billing-credits
- Orb — https://www.withorb.com/pricing
- Lago — https://www.getlago.com/pricing
- OpenMeter (now Kong "Konnect Metering & Billing") — https://openmeter.io/pricing

**Category comparables**
- ClickUp — https://clickup.com/pricing
- monday.com — https://monday.com/pricing
- Intercom (Fin, per-resolution) — https://www.intercom.com/pricing
- Atlassian Rovo — https://www.atlassian.com/software/rovo/pricing
- Salesforce Agentforce — https://www.salesforce.com/agentforce/pricing/
- Dart AI — https://www.dartai.com/pricing
- Plane — https://plane.so/pricing

**Licensing precedents**
- GitLab self-managed licensing (activation code, phone-home) — https://docs.gitlab.com/administration/license/
- n8n Sustainable Use License — https://github.com/n8n-io/n8n/blob/master/LICENSE.md
- Functional Source License — https://fsl.software/
- Sentry, "Introducing the Functional Source License" — https://blog.sentry.io/introducing-the-functional-source-license-freedom-without-free-riding/ (published **2023-11-17**)

**Internal**
- `docs/market-research-synthesis.md` (synthesised 2026-09-19)
- `docs/orchestration-upgrade-plan.md` §F (fleet economics; §F1 spend caps, §F3 allowed/forbidden metrics)
- `docs/analytics.md` (self-host telemetry posture)
- `LICENSE` (modified Apache 2.0, © 2025 Agora, Inc.)

---

## Appendix B — Claims this plan does NOT rest on

Flagged explicitly so no downstream decision inherits an unverified number.

1. **Independent reporting that AI credit meters cause churn was not obtained.**
   The research pass exhausted its search budget and could reach only vendor
   pricing pages, which do not report their own backlash. The claim rests
   entirely on `docs/market-research-synthesis.md` — a strong base, but this
   document adds no external confirmation. **This does not change any decision
   here**, because the no-credit-meter rule is also independently supported by
   the zero-marginal-cost argument (§0) and by GitHub keeping completions
   unmetered.
2. **GitHub's "1 AI credit = $0.01" is inferred**, not quoted — derived from
   Business $19/seat → 1,900 credits and the published $0.01 overage rate. Per-model
   credit consumption rates were not locatable.
3. **Devin's dollar value per ACU / on-demand credit is not published** anywhere
   reachable. The docs state only that on-demand credits are "the same dollar
   value as the ACUs you're used to".
4. **Replit's user backlash to effort-based pricing is unverified.** The
   announcement post contains no acknowledgement of one. Only Cursor's backlash
   is first-party documented.
5. **Plane's self-host licence and edition split are undocumented** on their
   pricing and self-hosting pages.
6. **Lago's licence type and OpenMeter's ingest scale** are not stated on their
   pricing pages.
7. **Dart's agents-are-not-seats position is inferred** from agents being
   available on the free tier, not quoted.
8. **Our own cloud-runtime unit cost is unmeasured.** The proposed per-block
   runtime price has no internal anchor yet; Linear's $0.25 / 20 min is a market
   reference, not a cost basis. Phase 5 must not price before measuring.
