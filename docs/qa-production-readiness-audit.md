# QA Process — Production-Readiness Audit

Date: 2026-07-07 · Branch: `sd-platform` · Method: 4 independent code audits (gate
pipeline, regression/sprint, cockpit correctness, UX ergonomics) + live data
forensics on the local (prod-copy) and prod databases.

## Executive verdict

**The QA process is NOT production-ready as a trust gate.** The cockpit's
"33/45 need fix (73%)" is structurally inflated: forensics show **33/33**
failing issues carry a watchdog "no verdict" comment while only **3/33** ever
had a real agent `qa-result` verdict (and 1/33 has qa_evidence). Root causes
are systemic, not cosmetic. Separately, **prod has the entire pipeline
disabled** (no `AGORA_AUTO_QA_ENABLED` / QA / sprint flags on the prod
backend; prod qa:fail count = 0) — everything below currently runs only on
local.

Three compounding defects manufacture the red wall:

1. **`qa:fail` is sticky.** No automated path ever detaches the opposite
   verdict label (only the human Pass/Fail button does). Fixed-and-re-passed
   issues keep `qa:fail` forever; every surface (cockpit lane, merge gate,
   sprint rollup) checks `fail` first, so it wins over a fresh `qa:pass`.
2. **Infra failures are minted as `qa:fail`.** The watchdog escalates any
   stale unverified gate (agent died, box down, usage limit, malformed
   verdict block) to `qa:fail`; the undeployable-sprint path's `qa:blocked`
   isn't excluded from the sweep, so deliberate "blocked" states get
   overwritten too. The watchdog's own comment promises "re-run QA … then
   this clears" — false: nothing clears it.
3. **The cockpit trusts sticky labels, not fresh verdicts.** A ready-made
   latest-verdict query (`ListQAEvidenceSummariesForIssues`,
   qa_evidence.sql:34-47) exists and has **zero callers**.

## Scorecard

| Feature | Verdict | Core defect |
|---|---|---|
| run_qa auto-gate (trigger/selection/prompt) | 🟡 fragile | qa:blocked→watchdog qa:fail; baseline discipline prompt-only; dev-box smoke vs stale box |
| qa_evidence capture | 🟡 fragile | plumbing solid (both chokepoints); **no provenance field** (agent/human/watchdog indistinguishable); per-issue single row (per-sha = P2 TODO) |
| Verdict labels lifecycle | 🔴 broken | sticky qa:fail; both labels coexist; fail-wins everywhere |
| QA watchdog | 🔴 misleading | mints qa:fail for non-failures; unclearable; bypasses AttachLabel (no autoroute/auto-bug); false "clears" promise |
| Discrimination gate (baseline_status) | 🟡 inert | default OFF, fail-open on error, e2e-only issues can never satisfy it when ON (permanent hold) |
| done-gate | 🔴 hole | `qa:fail` does NOT block in_review→done; three verdict sources (done-gate/merge-readiness/cockpit) disagree |
| Base suite (promote-on-done) | 🟡 footgun | promotion ungated (no pass required, no flag) while the protecting QA gate is opt-in; one broken case reds the whole project; no auto-flake; quarantine-all silently removes the gate |
| Sprint regression dispatch | 🟡 fragile | whole-branch fallback ships bare JSON (baseline guidance not injected into autopilot); title-heuristic autopilot pick; deploy failures swallowed (stale-box verdicts); "daily backstop" exists only in comments |
| Sprint readiness / Mergeable | 🔴 broken | `runs_fail` counts ALL-TIME rows (no latest-per-case) → one historical fail = permanently unmergeable; human qa:pass silently overridden; regression result NOT part of Mergeable |
| Sprint PR mode | 🟢 solid | conservative defaults, tiered auto-merge, risk-tier refusals; minor: env reader duplicated in 2 packages |
| Connected boxes | 🔴 broken under concurrency | non-blocking advisory lock returns ok on skip → wrong-branch verdicts when two branches race one box; worktree isolation NOT implemented |
| Cockpit List/Board lanes | 🔴 misleading | label-driven (see stickiness); ListIssues clamps 200→**100** silently (tail dropped, no "showing X of N"); Board = List rotated (no drag, no new info) |
| Bugs tab | 🟡 fragile | client-filters `bug` within first 100 issues → bugs beyond invisible; "Verified fixed" ignores qa:fail (contradicts cockpit) |
| Metrics tab | 🟡 fragile | NULL-issue runs counted globally but dropped when project-scoped (totals don't add up); "~5s/case" is a hardcoded string styled as a metric; "runs" = per-case rows |
| QA review page (human triage) | 🟢 solid | the ONLY correct verdict writer (detaches opposite); rail default-collapsed hurts; File Bug hidden for human/watchdog fails |
| Test-cases panel | 🟢 best-in-class | failing-first sort, auto-open reasons, live markers, traces — the model for the rest |
| Realtime/live-ness | 🟡 stale | label changes don't invalidate qa-cockpit/qa-bugs (staleTime Infinity, no focus refetch); sprint/metrics keys missing wsId (cross-workspace stale); EventIssueSprintChanged has no frontend handler |
| UX ergonomics | 🔴 not convenient | no reason/provenance/age in lane rows; zero bulk ops (33 fails = 33 page opens); no "Send back to dev"; regression run → toast dead-end (no run link); Passed lane grows forever |

## Data forensics

Local (what the screenshot shows): 45 in_review · 33 qa:fail · 4 qa:pass ·
**33/33 with watchdog comment · 3/33 with real qa-result · 1/33 with
qa_evidence · 65 watchdog comments total.**
Prod: 59 in_review · **0 qa:fail** · no QA env flags on the backend → the
auto-QA pipeline has never run in prod.

## Unified fix plan

### P0 — make verdicts trustworthy (before anything else)
1. **Auto-clear the opposite verdict label** in `CaptureQAEvidence`
   (qa_evidence.go:102-134) and the `AttachLabel` handler for `qa:*` pairs
   (label.go:368-387); also clear stale gate labels on re-entry to in_review.
2. **Lanes read the fresh verdict**: wire the unused
   `ListQAEvidenceSummariesForIssues` into the cockpit; label becomes a
   display artifact, not the source of truth.
3. **Sprint readiness = latest-run-per-case** (mirror
   `ListLatestRunsForIssueCases`) + **fold the regression gate into
   Mergeable** (sprint_readiness.sql:40-46, sprint_readiness.go:117-152).
4. **Stop minting qa:fail for non-failures**: watchdog + undeployable path
   write a distinct clearable state (`qa:stale` / keep `qa:blocked`), exclude
   `qa:blocked` from the sweep (issue_label.sql:28), render as "gate didn't
   run", and make (or delete) the "re-run clears" promise.
5. **Fix the 100-clamp truncation**: raise/paginate ListIssues for QA views +
   "showing X of N"; Bugs tab filters by label server-side.

### P1 — process integrity
6. `qa:fail` (or absence of fresh pass) **blocks done**; reconcile the three
   verdict sources into one helper.
7. **Box concurrency**: blocking lock + post-lock branch re-verify (never
   ok=true for a skipped different-branch sync); then per-branch worktrees.
8. **Base suite**: gate promotion on a passing/discriminating run; auto-flake
   quarantine (N fails across distinct issues → park + alert); warn when the
   effective suite is empty.
9. **Regression contract**: inject `qaBaselineGuidanceFor("regression")` into
   the autopilot dispatch; add `run_id` to the readiness payload and link the
   chip to the run; explicit autopilot purpose tag instead of title heuristic.
10. **Provenance**: add `source: agent|human|watchdog` to qa_evidence; stamp
    it everywhere; render chips.
11. **Attach-tasks**: warn before silently moving an issue out of another
    sprint (issue_to_sprint PK = one sprint per issue).
12. **Realtime**: invalidate qa-cockpit/qa-bugs on label/evidence WS events;
    add wsId to sprint/metrics query keys.

### P1-UX — daily-loop ergonomics (the "not convenient" fixes)
13. **Lane rows carry the answer**: failure reason (top new_failure /
    summary), provenance chip, age — no more open-every-issue.
14. **Bulk operations**: multi-select + sticky bar (re-run QA, send back to
    dev, move to next sprint) over the selection.
15. **"Send back to dev" one-click** (qa:fail + status→in_progress) on the
    review page and in bulk.
16. Review-page **rail open by default** when a verdict/evidence exists;
    File Bug available on human/watchdog fails too.
17. **Board**: make it a real kanban (drag between lanes writes the verdict)
    or remove the tab.
18. Passed lane drains (collapse/paginate or "ready to merge" affordance).

### P2 — polish
Metrics NULL-symmetry + honest labels (drop hardcoded "~5s/case"); manual
file-bug dedup against `qa_bug_filed` + qa-bugs invalidation; consolidate the
duplicated `sprintPRModeEnabled` reader; discrimination gate rework (e2e
path) or keep opt-in; multi-replica QA-dispatch lock; editor Tests tab
"QA Runs" reads persisted evidence instead of re-parsing the timeline; build
or delete the "daily backstop".

## What to trust today
Sprint PR-mode gate layering; the test-cases panel; human triage on the
review page; evidence-capture plumbing (both chokepoints). Everything else
above 🟡/🔴 until P0 lands.

## Prod enablement (after P0)
Prod currently runs NONE of this. Staged rollout: enable `AGORA_AUTO_QA_ENABLED`
on one project → watch a full sprint → then watchdog (with the new
non-fail state) → then discrimination/PR-mode per team decision.
