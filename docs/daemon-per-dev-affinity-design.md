# Daemon-per-Dev + Runtime Affinity — Design

> Status: DESIGN (2026-07-07). Builds on `docs/remote-dev-servers-research.md`
> (daemon-per-box + cloud-editor-proxy + mesh — still the chosen direction) and
> on what shipped since it was written: per-runtime `editor_addr` resolution
> (`resolveDaemonInternalAddr`), the live-browser reverse proxy
> (`/browser/proxy/{token}`, commit 8ebfd9df), the trace proxy, and Settings →
> Labs project-scoped QA routing (02a45a31…6b069e2e).

## 1. Problem

"Agar project localda run bo'lgan bo'lsachi?" — the app under test lives on the
**developer's own machine** (laptop or personal box), started by the developer,
reachable only as that machine's `127.0.0.1`.

Today:

- **Agent-level pinning exists**: `agent.runtime_id` (+ `fallback_runtime_id`)
  binds an agent to a runtime; a daemon claims tasks per-agent
  (`ClaimAgentTask`, `server/pkg/db/queries/agent.sql:299`). But agents are
  *shared* — QA Tester lives on one shared runtime. When Shahzod's issue needs
  QA against Shahzod's locally-running app, the QA task runs wherever QA
  Tester's runtime is — a machine where `localhost:8081` is **not** Shahzod's
  app.
- **QA target resolution** (Labs, `connectedBoxForIssue`) only knows deployed
  targets: per-dev *boxes*, project boxes, `qa_smoke_url`. A dev's local app
  has no address the resolver could return that would be valid from any other
  machine.

So the missing primitive is **task-level runtime affinity**: "run THIS task on
THAT dev's daemon", plus a way for a dev's daemon to say "I serve project X at
this local URL".

## 2. Model

### 2.1 Personal runtime (daemon-per-dev)

A developer runs `agora daemon start` on their machine, authenticated with
**their own PAT** (not `mdt_`). That is already enough for identity:

- `agent_runtime.owner_id` ← the PAT's member/user (`daemon.go:312`, verified
  in the research doc). **The personal runtime IS the row where
  `owner_id = dev's user_id`.** No new concept, no new table.
- `AGORA_DAEMON_DEVICE_NAME` labels it ("MacBook-Jamshid").
- Multiple runtimes per owner are legal (laptop + box). Pick by
  `last_seen_at DESC` among `status='online'`.

### 2.2 Dev app declaration (explicit, per project — no defaults)

The dev declares which project(s) their machine serves and where. Mirrors the
"never cross-project" rule from the Labs box routing:

```
agora daemon apps set <project-slug> http://127.0.0.1:8081
agora daemon apps list / unset <project-slug>
```

Stored in the daemon profile config and reported on every
register/heartbeat into `agent_runtime.metadata.dev_apps`:

```json
{ "dev_apps": { "sd-main": "http://127.0.0.1:8081" } }
```

Rules:
- URL must be loopback/private — it is only ever dereferenced **on that
  runtime** (by an agent running there) or through the backend proxy chain
  (phase 3). The backend never hands it to another machine's browser as-is.
- No entry → the runtime serves nothing locally. An entry for sd-main says
  nothing about sd-cs. Exactly like box `project_id` scoping.

### 2.3 Task affinity

New nullable column:

```sql
ALTER TABLE agent_task_queue ADD COLUMN preferred_runtime_id uuid
  REFERENCES agent_runtime(id) ON DELETE SET NULL;   -- migration 150
```

Semantics: *"this task should execute on this runtime; the agent is whoever it
already is."* It does **not** replace `agent.runtime_id` — it overlays it for
one task.

**Dispatch-time resolution** (in the enqueue path, next to where
`model_override` is stamped):

```
preferredRuntimeFor(task):
  if task is a QA slice (run_qa / run_test_cases / compile_tests / deploy-qa smoke)
     AND issue has a human developer (developerUserForIssue — member or agent-owner)
     AND labs.qa_dev_runtimes == true
     AND ∃ runtime r: r.owner_id = devUser AND r.status='online'
         AND r.metadata.dev_apps[issue.project.slug] != ""
  → r.id
  else → NULL   (today's behavior, byte for byte)
```

Only QA-shaped tasks get affinity in phase 1. Dev-work tasks keep their
existing agent→runtime binding (a dev's coding agent can already be pinned to
their runtime via `agent.runtime_id` — that mechanism stays untouched).

**Claim-side enforcement.** `ClaimAgentTask` gains one predicate:

```sql
AND (atq.preferred_runtime_id IS NULL OR atq.preferred_runtime_id = @runtime_id)
```

The daemon already knows which runtime it is claiming for; the shared QA
Tester agent may be *hosted* by several daemons only in the fallback sense —
in practice the claim call carries the claiming runtime id
(`ReclaimStaleDispatchedTaskForRuntime` already keys on `runtime_id`, so the
plumbing exists). A task preferring runtime R is invisible to every other
daemon's claim loop.

**Wait-state surfacing.** A task whose preferred runtime is not claiming shows
as `queued` forever without explanation — unacceptable. Reuse the existing
wait mechanics (`wait_reason`, `MarkAgentTaskWaiting…` precedent):

- Enqueue stamps `wait_reason = 'waiting_dev_runtime:<device_name>'` when
  `preferred_runtime_id` is set.
- The QA watchdog (qa_watchdog.go) gets one new check: preferred runtime
  offline (last_seen_at stale) OR task queued > `AGORA_DEV_RUNTIME_WAIT_MAX`
  (default 10 min) → **soft-affinity fallback**: clear
  `preferred_runtime_id`, post a comment ("<dev>'s daemon offline — QA fell
  back to the shared runtime; target resolution reverts to box/smoke"), and
  let normal claiming take it. A `labs.qa_dev_runtimes_strict = true` flag
  keeps it pinned instead (task stays queued, watchdog escalates qa:stale) —
  for teams where testing on the wrong env is worse than waiting.

The fallback MUST also clear the local QA target (see 2.4): a task that fell
back to a shared runtime must not try to reach a laptop's `127.0.0.1`.

### 2.4 QA target resolution — new step 0

`resolveQAPreviewURL` / the run_qa directive builder get a step **before** the
box chain, gated by `labs.qa_dev_runtimes` (new flag, sibling of
`qa_dev_boxes`, default **off** — opt-in, per the no-new-defaults rule):

```
0. dev-local app: devUser := developerUserForIssue(issue)
   r := newest online runtime owned by devUser
   url := r.metadata.dev_apps[project.slug]
   if url != "" → target = url, AND the QA task must be pinned to r
     (affinity and target are one decision — a local URL is only meaningful
      on the runtime that declared it)
1..5. existing chain (per-dev box → shared project box → repo match →
      labs fallback box → qa_smoke_url), unchanged.
```

Implementation note: target resolution happens where the QA slice is enqueued
(so the directive text embeds the right URL) — the same place stamps
`preferred_runtime_id`. `GetIssueQAPreviewURL` (the Live-testing bay) applies
the same step so the human reviewer's pane shows the dev-local app… which is
only reachable for the *human* through the proxy chain — see 3.

## 3. Reachability tiers (what works when)

| Consumer | Self-host (daemon on same machine as backend user) | Cloud backend + dev laptop |
|---|---|---|
| **QA agent driving the app** | ✅ phase 1 — agent runs ON the dev runtime, `127.0.0.1` is the app | ✅ phase 1 — same reason; affinity puts the agent there |
| **Compiled scripts + traces** | ✅ phase 1 (traces land in `$HOME/.agora/qa-traces` on that runtime; LaunchTrace already resolves per-runtime `editor_addr`) | ⚠️ needs the runtime reachable from the backend → phase 3 |
| **Human live-watch (browser proxy), editor, trace viewer** | ✅ works today (`/browser/proxy`, `/editor/proxy`, `/trace/proxy` + `resolveTraceDaemon` per-runtime address) | ⚠️ phase 3 — laptop must be dialable |
| **Other humans hitting the app directly** | ❌ by design — use a box for shareable envs | ❌ same |

Phase 3 = the research doc's mesh recommendation, now smaller than when it was
written: the backend already resolves a **per-runtime** `editor_addr` and all
three proxies ride it. Putting laptops on a WireGuard/Tailscale mesh and
reporting the mesh IP as `editor_addr` in runtime metadata lights up editor +
live-watch + trace for laptops with **zero new proxy code**. The remaining
net-new daemon work is unchanged from the research doc: preview/CDP loopback
binds + auth hardening (`health.go:509,513,546`, `browser.go`,
`code-server --auth none`) — the mesh is the security boundary, but the binds
still have to move off `127.0.0.1` for the backend to dial them.

## 4. Schema / API deltas (complete list)

- Migration 150: `agent_task_queue.preferred_runtime_id uuid NULL` (+ index
  `(status, preferred_runtime_id)` partial on `status='queued'`).
- `ClaimAgentTask` + the claim RPC: pass the claiming `runtime_id`, add the
  one-line predicate. `ReclaimStaleDispatchedTaskForRuntime` unchanged.
- Daemon: `agora daemon apps set|unset|list` subcommands; `dev_apps` merged
  into the register/heartbeat metadata payload (same channel as
  `editor_port`).
- Handler: `preferredRuntimeFor` at QA-slice enqueue; step-0 in
  `resolveQAPreviewURL` + the directive builder; watchdog fallback check;
  `WorkspaceLabs` gains `qa_dev_runtimes` + `qa_dev_runtimes_strict`.
- Labs UI: third toggle ("QA on developer machines"), strict/soft radio, and a
  read-only list of online personal runtimes with their declared apps
  (runtime name · owner · project → URL · last seen).
- Watchdog note + `wait_reason` rendering in the task drawer ("waiting for
  MacBook-Jamshid").

Nothing else moves: boxes, sprint regression (always shared/box targets —
personal runtimes are deliberately excluded from sprint-end regression),
editor account tokens, KB — untouched.

## 5. Failure modes & answers

- **Laptop asleep mid-run** → task already `running` on that runtime; existing
  stale-task watchdog handles it exactly like any dead daemon (no new state).
- **Laptop offline at dispatch** → resolver step 0 requires `online` + fresh
  `last_seen_at`; no pin is created, chain falls through to boxes. Race
  (went offline right after pin) → watchdog soft-fallback (2.3).
- **Two personal runtimes (laptop + box) both online** → newest
  `last_seen_at` wins; `agora daemon apps` lives per-profile so usually only
  one declares the project anyway. Tie is harmless — both are the dev's.
- **Dev app URL declared but app not actually running** → the QA agent's first
  probe fails fast; verdict comes back `blocked` with the connect error as
  evidence — same failure shape as a dead box today. No special handling.
- **Security**: `dev_apps` URLs are never proxied in phases 1–2; they are
  strings interpreted only inside the owner's own machine. Affinity cannot be
  forged from the client: `preferred_runtime_id` is stamped server-side from
  `agent_runtime.owner_id`, which comes from PAT auth. Strict mode never
  silently reroutes a task to a machine the dev doesn't own.
- **Multi-replica backend**: pin + claim are both DB-side; no in-memory state.

## 6. Phasing

| Phase | Scope | Size |
|---|---|---|
| **1 — Affinity core** | migration 150, claim predicate, `preferredRuntimeFor` for QA slices, wait_reason + watchdog soft-fallback, `labs.qa_dev_runtimes(+_strict)` flags | ~1 day, all server-side, default off |
| **2 — Dev apps** | `agora daemon apps` subcommands + metadata reporting, resolver step 0 + directive URL, Labs UI (toggle + runtimes list) | ~1 day |
| **3 — Human reachability (mesh)** | mesh onboarding doc, `editor_addr` from mesh IP, daemon bind envs for preview/CDP (`AGORA_PREVIEW_BIND`/`AGORA_BROWSER_BIND`), CORS/auth hardening from the research doc §2 | ~2–3 days, only needed for cloud backend + laptops |

Phase 1+2 alone fully answer the original question for the self-host/SD setup
(backend + devs in one network, or dev daemon co-located): Shahzod runs his
app locally, declares it, and his issues' QA executes on his machine against
his app — live-watchable, traces included — while everyone else's work keeps
flowing exactly as today.

## 7. Open questions (need product answers before phase 2)

1. Should a dev's **coding** tasks (not just QA) prefer their personal runtime
   when they have one? (Today: only via explicit `agent.runtime_id` pinning.)
   Leaning no-for-now — code execution placement is a bigger behavioral
   change than QA placement.
2. `dev_apps` keyed by project **slug** vs **id**: slug reads better in CLI,
   id survives renames. Proposal: CLI accepts slug, stores id + slug pair,
   matcher uses id.
3. Does sprint regression EVER run on a personal runtime? Current answer:
   never (excluded by design, § 4) — confirm.

---

## Implementation note (phase 1, shipped)

Reality simplified the design: tasks were ALREADY routed per-runtime at
enqueue (`agent_task_queue.runtime_id` is what `ListQueuedClaimCandidatesByRuntime`
keys on), so **no migration and no claim-SQL change** were needed. The pin is
an enqueue-time `runtime_id` override in `maybePinTaskToDevRuntime`
(service/dev_runtime_pin.go), hooked into `enqueueMentionTask` — which covers
auto run_qa, lead delegation, and manual QA mentions in one place, gated on
QA-squad membership (`AgentInQASquad`). Markers ride `context`
(`dev_runtime_pin`/`dev_runtime_home`); `wait_reason` surfaces the wait. The
QA watchdog boot+5-min tick runs `SweepStaleDevPinnedTasks` (soft fallback +
issue comment, or strict hold). Resolver step-0 landed as `devLocalAppURL`
inside `devBoxSmokeURL`, so the run_qa directive, qa-preview-url, and the
Live-testing bay all see the dev-local URL consistently. Verified end-to-end
with a synthetic runtime: pin → claim isolation → offline → fallback+comment.
Phase 2 remaining: `agora daemon apps` CLI + metadata reporting + Labs
runtimes list.

## Implementation note (phase 2, shipped)

`agora daemon apps set|unset|list [--profile]` — set resolves the project by
id or exact title across every workspace the profile's token sees (ambiguity
errors with candidates), enforces loopback/private URLs (net.IP.IsPrivate),
and stores `dev_apps` {projectID: {url,title}} in the profile CLI config.
LoadConfig lifts it into daemon Config; the register payload carries
`dev_apps` and the server persists it into runtime metadata (wholesale-rebuilt
per register, so unset+restart clears). Labs gained a read-only "Developer
machines" card (personal runtimes with declared apps: status dot, owner,
project → URL). Verified live end-to-end on the local daemon: CLI set →
restart → metadata in DB → resolver returned the dev-local URL over the
project's bound box → mention task landed on the dev's own runtime.
