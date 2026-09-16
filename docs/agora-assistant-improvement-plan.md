# Agora Assistant — improvement plan (single source of truth)

Reconciled: 2026-09-17 by the Fable session, merging three documents into one:
the GPT-agents assessment (previous content of this file), the feature spec
(`docs/agora-assistant-plan.md`), and the artifacts spec
(`docs/agora-assistant-artifacts-plan.md`). Those two stay as detailed specs;
**this file is the roadmap.** Every "done" claim below was verified against
the working tree, the test suites, or a live API run on 2026-09-16/17.

## 0. Main rule (owner, final)

**The assistant can do everything a user can do manually in Agora.** Full UI
parity, same role checks (the real HTTP handlers enforce them), destructive
actions gated by an explicit conversational confirmation — the chat
equivalent of the UI's confirm dialog, not a stricter policy. The single
carve-out: raw secret values never pass through chat (transcripts persist);
the assistant points at the exact Settings field instead. "I can't do X here,
use the UI" is the failure mode this product removes.

## 1. Where we are — DONE and verified

| Area | State |
|---|---|
| Core service | Go tool loop (8 rounds, 3 min run, 15 s/tool), user-scoped sessions/messages (migration 195), endpoints beside `/api/me`, no workspace header — workspace is a tool argument (cross-workspace by construction) |
| Durable runs | `assistant_run`/`assistant_operation` (migration 197): DB-leased runs, request dedup, idempotent-replay receipts for mutating tools, cross-process cancel. Built by the owner's GPT agents; suite green |
| Tool catalog | 35 tools live (reads, writes via in-process real-handler invocation, grounding, analytics, artifacts); **parity batch in flight** adds ~15 more: plain `list_issues`, deletes+confirm, invites/roles, workspace CRUD, automations, autopilots, inbox writes, pins |
| Artifacts | Backend (migration 196, `create_artifact`/`update_artifact`, ownership-isolated endpoints) + frontend (ArtifactCard, split-pane viewer: native Recharts chart, table, markdown, sandboxed HTML). Live-verified: chart from prompt; table iterated to v2 |
| Providers | zhipu (free "Agora" glm-4.5-flash), Anthropic, OpenAI — reasoning-model wire handled (`max_completion_tokens`, `reasoning_effort:"none"` with tools; verified live on gpt-5.6-luna) |
| UI | Launcher-hero page + floating Assistant panel (replaced the agent-chat popup, owner decision), slash commands (`/add-task`…), copy actions, session rail, i18n ×4, unique Agora-mark identity (not the agent robot) |
| Adjacent | Per-user sidebar customization (`user.hidden_nav`, migration 194); onboarding seed issues no longer spam the Telegram room |
| Verification | Stress round 1: 16/16 (task flow, RU, ambiguity, permission probe, injection resist, 409/parallel/cancel/isolation). Round 2: 26/28 assistant-correct (2 non-assistant: see fix list). Backend suites green; TS 3,000+ tests green. Avg run 4.2 s |

Not deployed anywhere. All local, all uncommitted (migrations 194–197).

## 2. In flight (2026-09-17)

- **Parity batch** (Opus agent): the main-rule tool expansion + confirm
  protocol + secrets-only exclusion pins + `mark_inbox_read` context fix +
  prompt-guidance hardening ("add a comment" → `comment_issue` binding).
- After it lands: re-verify, restart local backend, update §1 table.

## 3. Fix list — verified findings, priority order

**P0**
1. **HTML-artifact network exfiltration** (from the GPT assessment — confirmed
   real): `sandbox="allow-scripts"` isolates the DOM but does not block
   `fetch()` to external hosts; a steered model could emit HTML that ships
   rendered workspace data out. Fix: inject a `<meta http-equiv=
   "Content-Security-Policy">` into the srcdoc wrapper (default-src 'none';
   inline script/style allowances only), test that outbound fetch fails.
   Until then artifacts render model-derived data the user already saw — the
   risk is bounded but must not survive to a shared-artifacts phase.
2. **Tool-result envelope honesty**: capped lists narrate as complete (round-2
   chart under-counted from `list_my_issues`). Fix: every list tool returns
   `{data, total, truncated, scope, failed_workspaces[]}`; prompt guidance
   requires stating coverage; aggregates for totals.
3. **Archived issues appear in list endpoints** — pre-existing backend bug
   (archive worked; the list didn't hide it). Known open follow-up; fix in
   ListIssues param plumbing, not in the assistant.

**P1**
4. Timezone-correct date windows for `usage_summary`/`activity_digest`
   ("today"/"this week" in the user's tz — `user.timezone` already exists),
   explicit range echoed in results.
5. Per-message context envelope (workspace/entity/tz captured at Send) —
   partially built in the durable-runs slice; finish: composer shows the
   target workspace chip, explicit mention overrides focus.
6. Permission-denial clarity: non-member tool errors should say "not a
   member of that workspace", not a generic validation message (stress A10).
7. `activity_log` is sparsely written upstream — `UpdateIssue` records no
   activity row, so digests are thin. Upstream fix in issue handlers.
8. Model self-knowledge: inject the model label into the system prompt so
   "which model are you" answers match the UI footer.

**P2**
9. Cursor-paged transcripts + capability-grouped tool schemas (prompt-size
   growth), bounded concurrent independent reads.
10. Streaming responses (SSE/WS deltas) once run recovery is stable.

## 4. Product roadmap (kept from the GPT assessment — it's right)

The spine: **What needs my attention? What did you change? What happens
next?** Ranked workflows (validate with a small pilot):

1. "What should I focus on today?" — deterministic ranking inputs (due dates,
   priority, review requests, inactivity), model explains, coverage explicit.
2. "Turn this idea into an actionable plan" — versioned editable proposals;
   accepting a version creates the tasks exactly once (`assistant_proposal`).
3. "Give this bug to the right agent and keep me updated" — handoff into the
   existing orchestration + status/evidence tools (`assistant_handoff`).
4. "What is blocking this release?" — scoped release readiness, per-blocker
   evidence.
5. "Show what we delivered this week" — **live artifacts**: report/chart with
   sources, date range, versioning; `schedule_artifact_refresh` (headless
   assistant re-run as the owner) + `bind_artifact_to_autopilot` (refresh on
   autopilot completion). Spec: artifacts plan Phase 3.

Phasing + gates (effort = one engineer familiar with Agora):

| Phase | Effort | Gate |
|---|---|---|
| A. Land parity batch + P0 fixes 1–3 | 2–4 d | suites green; stress round 3 clean incl. coverage-honesty cases |
| B. "My day" + context envelope finish + P1 4–6 | 5–8 d | correct ranked recommendations on fixtures; tz answers exact |
| C. Plan & delegate (proposals, handoff, task-status tools) | 7–12 d | idea → intended tasks exactly once; no offline runtime reported running |
| D. Live artifacts (refresh, autopilot binding, report reproducibility) | 4–7 d | numbers reproduce from queries; refresh authorized as owner |
| E. Proactive digests (opt-in, quiet hours, via existing automations) | 3–5 d | explicit opt-in; zero autonomous writes |

## 5. Quality bar

- Keep the deterministic suites (permissions meta-test: every workspace tool
  has a denial test) + the live stress harness
  (`scratchpad/stress_assistant.py` pattern — port into `e2e/` as a scripted
  scenario suite, 40–60 cases incl. multilingual, injection, revoked
  membership, restart/reconnect recovery; `e2e/assistant-recovery.spec.ts`
  exists from the durable-runs slice).
- Release gates: zero permission violations/duplicate effects (blocking);
  ≥95% simple task success; report numbers exactly reproduce fixtures; p95
  ack < 1 s; every interruption recovers to an accurate state.
- Provider choice by benchmark on the same scenario set, not by label.
- Rollout: internal cohort → small cohort → expand; writes kill-switch
  separate from full disable.

## 6. Reconciliation notes

- The previous version of this file (GPT-agents assessment) understated the
  artifact state ("backend not established" — it is, live-verified) and
  listed two test failures that were my parity agent's mid-flight contract
  rewrite, not regressions. Its P0 durable-runs finding was correct and is
  now shipped. Its read/plan/act capability-mode proposal is folded into the
  confirm protocol (Phase A) and proposal versioning (Phase C) — aligned with
  the main rule rather than overriding it.
- Detailed specs live in `agora-assistant-plan.md` (architecture, permission
  model, main rule §0) and `agora-assistant-artifacts-plan.md` (artifact
  kinds, schema, live-artifacts design). This file is the only roadmap.
- References kept from the assessment: Anthropic guidance on
  [agent architecture](https://www.anthropic.com/engineering/building-effective-agents),
  [context](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents),
  [tool design](https://www.anthropic.com/engineering/writing-tools-for-agents),
  [evals](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents);
  MDN on [iframe sandbox](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/iframe)
  and [CSP connect-src](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/connect-src).
