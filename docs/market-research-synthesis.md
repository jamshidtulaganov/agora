# Market research synthesis — what the evidence says Agora must be

Status: synthesized 2026-09-19 from three independent research passes.
Sources: 2,933 Reddit comments (2024–2026, 9 subreddits, Arctic Shift archive),
26 Hacker News threads (Algolia), ~55 review-site/competitive sources
(G2/Capterra/HN/press). Full reports live in the research session transcripts;
every claim below traces to verbatim user quotes with URLs in those reports.
Reddit blocks Anthropic crawlers (403) — archive API was used; X was
unfetchable (402) and is thin here by necessity.

## The one-line result

The market's unclaimed problem is **tracker truth decay** — "everyone's solved
getting context *into* the issue; nobody's solved the issue quietly becoming
wrong afterward. Agents make it worse: they generate state changes faster than
anyone updates the tracker." The credible position is **verified work, never
"autonomous"** — Height led with "autonomous" and was dead five months later,
while the gap between "AI slop" sentiment and Microsoft's own ~61% agent-PR
merge rate is exactly where a QA-gated, evidence-producing pipeline can speak
and nobody currently does.

## Convergent findings (all three passes agree)

1. **Verified > autonomous.** AI-authored tickets are the most hated artifact
   in the corpus ("reverse prompt engineering the slop"); yet agent work that
   arrives with evidence gets merged. Positioning, product and prompt rules
   must all say *verified*.
2. **The reporting tax is the one AI use people already love.** "2/5 of my
   week is reporting busywork"; "one transcript becomes a Slack recap, exec
   summary, status update in one click." Phase 1 recipes (sprint report,
   standup, release notes) sit exactly on this. Double down; never ship
   "summarize this thread" gimmicks, which are named as the definition of
   bolted-on AI.
3. **Bolted-on AI + credit meters = churn.** Rovo ("barely above trash",
   "badgers me and I can't disable it"), ClickUp AI Super Credits burning
   invisibly, monday's "Fuck off with your AI" top comment. Rules: every AI
   surface disableable (registry flags — already true), no credit meters, no
   AI-gated tiers, flat predictable pricing with compute passed through.
   Agents are NOT billable seats — Linear/Dart/Shortcut already settled that.
4. **Beat the user's own Claude Code setup or lose the login.** "Linear Agent
   performs worse than my own Claude Code + MCP — I don't even login most
   days." The tracker-as-context-store + BYO-agent pattern is the real
   competitor for our exact ICP. Agora's answer is being the runtime (hosted
   daemon, QA loop, cross-agent state, review routing) — things a weekend
   Codex-roll can't reproduce. The DIY wave (Beads, Paca, dozens more) is
   simultaneously validation and the reason "issues + AI" alone is not a moat.
5. **Config sprawl is the root cause of tracker hatred.** "The main advantage
   of Jira is flexibility. The main drawback is ...flexibility." At 2–10
   people, process-maintenance cost exceeds process value. Stay opinionated;
   every convergence toward 47 views is a churn event. (Matches the standing
   UI-simplicity rule.)
6. **PM-vs-IC positioning:** ICs want the tracker to disappear into the agent
   workflow; PMs want the clerical layer gone but judgment kept. Position as
   removing the clerical layer between them — never as replacing PM judgment,
   and never manager-dashboard-first ("JIRA isn't for you, it's for your
   boss's boss's boss").

## Gaps the evidence exposes (not yet built)

- **G1 — Living truth: derive status from work.** PR merged → status moves;
  agent run finished → issue hears about it. The #1 unclaimed problem.
  Agora already binds the SDLC stepper to running tasks; the missing half is
  the commit/PR/CI → issue-status derivation and a "this issue looks stale"
  signal. Highest-leverage single feature in the corpus.
- **G2 — Escalation hatch as contract.** Loudest agent-trust complaint
  (Devin-class): no "ask for help" hatch, grinding for days. Ship a hard
  budget + forced escalation + one-click terminate, surfaced ON the issue.
- **G3 — Review queue as the scarce resource.** "Agents are DDoSing our
  attention." Route/rank pending review by risk tier; measure rework-after-
  merge, not merge counts (merge rate is named a vanity metric).
- **G4 — Risk taxonomy as a first-class object.** The one credible agentic
  success story: "1000 low-risk bug fixes shipped agentically" behind a
  defined low/medium/high/critical taxonomy. Agora's per-project risk_map is
  the seed; promote it from convention to product surface.
- **G5 — Decision memory.** Small teams' named gap: "knowing a task was
  completed is less useful than knowing why." The KB flywheel is this —
  extend capture to decisions/rationale, not just solutions.
- **G6 — Agent ownership semantics.** Linear keeps the human responsible
  after delegation; only Shortcut's Devin flow makes an agent a true owner.
  Agora's polymorphic assignee already does this — narrow open window, say it
  out loud in positioning.

## Anti-goals (evidence-mandated)

- Never market "autonomous"; never let agents mass-create issues; never let
  AI author the artifacts humans must audit later (tickets/PRs/commits all
  slop = unauditable history).
- No nag-ware: no non-disableable AI, no AI upsell prompts in-product.
- No cosmetic renames/redesigns ("change for its own sake" is a cited churn
  trigger); no capability claims ahead of shipping ("they talk like it works
  but it doesn't" is how Linear is losing credibility).
- No per-agent seat pricing; no AI credit meters.
- **No agent-throughput dashboards** (issues closed per agent, tasks/day,
  tokens burned). The corpus's #1 named burnout driver in 2026 is AI as a
  management speedup instrument ("1 task a day was productive; now 5 and
  your boss expects more"). Measure verified outcomes, never volume.
- **Don't market autonomy to senior ICs.** The highest-scoring comment in
  the whole 3,204-comment corpus flatly disbelieves that agent-built code
  ships. "Agents write code" reads as a claim about code quality and dies
  there; lead with "the tracker stays true and agent work arrives
  reviewable" and let users dial autonomy up themselves.

## Segment counterweight

Pro-agent voices are almost entirely solo devs and small teams (r/Linear);
hostile voices are ICs inside larger orgs who inherit other people's agent
output. That maps exactly onto Agora's 2–10 target — the segment where agent
enthusiasm is real is the one being sold to. The hostility is a positioning
constraint, not a market disqualifier.

## Competitive frame

Plan against **Linear** (declared "issue tracking is dead", agents in ~75% of
enterprise workspaces per its own numbers, +67% customers YoY, 46% of base at
11–50 people) and against **DIY Claude-Code setups** — not against Jira.
Jira's function in strategy is as the migration source (see
docs/importers-plan.md). Height's death is the cautionary tale: an AI bet on
the same buyer Linear owns, marketed as "autonomous", without a verification
story.

## Mapping to the existing roadmap

Already aligned, keep course: report recipes + pinned/scheduled reports
(reporting tax), plan primitive (batch with human authorization), QA-gated
pipeline + evidence floor (verified work), human-voice ticket rule, registry
kill switches, UI-subtraction rule, free-model flywheel.

Insert as next majors after current phases: G1 (living truth) and G3+G4
(risk-routed review) — they are the two largest unclaimed problems and both
sit directly on plumbing Agora already has (task events, QA verdicts,
risk_map, stage stepper).
