# UI simplification manifest — make the SDLC surfaces sellable-simple

Goal: sold to 2-100 person **dev teams** (not SDLC experts). The QA, Review, and
Release surfaces have too many elements + too much jargon. North star: a dev who
has never seen Agora understands **"did it pass, what do I do next"** in ~2
seconds. Power is preserved — advanced affordances move behind expand/overflow,
they are not deleted. Source: 3-agent UI/UX audit.

## Five universal principles (every surface violates these today)

1. **One status, not three.** The "ready?" answer is computed once but shown as
   raw inputs 3-4× (verdict + gate grid + stepper chip; ring + pill + ✓✗◌ +
   summary + lane counts). Show the conclusion; collapse the inputs.
2. **Plain English, zero jargon.** Kill from the user surface: `tier/full/light/
   trivial`, `reconciled_state`, `pass_with_failing_cases`, `qa:fail`/`qa:pass`,
   `no-QA`, `modality`, `golden-path`, `stale`, `blocked`, `RUNS`, `override`.
   → **Passed / Failed / Not tested yet / Testing…**, **Ready to ship / N
   blocking**, **Overridden by you**.
3. **Minimal default, power behind disclosure.** Default view = the answer + the
   one action. Everything else (gate breakdown, metadata, deploy panel, coverage
   analytics, per-case taxonomy) behind an expand or `⋯`.
4. **Cut / demote useless surfaces.** Whole tabs and pages a small team never
   needs at the top level.
5. **Always show the "why".** A disabled button / blocked state must state its
   reason inline (the backend already returns `readiness.blocked[]` — currently
   thrown away).

---

## REMOVE / DEMOTE list (needs owner go — these delete top-level surface)

| Surface | Verdict | Where it goes |
|---|---|---|
| **Release → Bugs tab** | REMOVE from tabs | it's just issues filtered to `bug` — expose as a saved filter on the Issues page |
| **Release → Suite tab** | REMOVE from tabs | regression-case authoring = QA-lead config → move to project settings / a "Manage suite" link inside Ship |
| **Release → Metrics tab** | DEMOTE | 30-day analytics → behind an "Insights" (`⋯`) overflow |
| **Settings → Shared design system** | REMOVE from Integrations | raw-JSON tokens editor, not an integration, not needed now (already being done in the Integrations gallery redesign) |
| Result | **Release: 5 tabs → 2** (Ship default + Queue) | biggest at-a-glance win |

Broader nav (audit-adjacent, owner call): Autopilot · Usage · Squads · AI
accounts — review whether a 2-10 team needs each at top level.

---

## PHASE A — Release page (biggest visible win; do first)

- **5 tabs → 2:** Ship (default) + Queue, with a `⋯` overflow for Insights
  (Metrics). Bugs → Issues filter; Suite → project settings. (`qa-page.tsx:89-96`,
  default `:39`)
- **Kill overlapping numbers:** drop the Queue summary line 2 (re-sums the lanes
  20px below, `release-queue.tsx:325-345`); drop the ✓✗◌ trio from the health
  strip (ring + "N/M ready" already say it, `release-health-strip.tsx:127-145`).
- **One headline per sprint:** Ship card resting line = ring + name + headline
  state + one CTA. Move pass/fail/pending badges + regression chip + counts into
  the collapsible "What's shipping" (`qa-sprint-readiness-view.tsx:255-301`).
- **Deploy panel collapsed by default;** "Ship it" reveals it (not always-open in
  every card, `:439`). Run-regression / Attach → a card `⋯` menu.
- **Health strip → one roll-up line** on non-Ship tabs ("2 sprints · 1 ready · 3
  need a decision" → link to Ship). Keep the needs-decision chip.
- **Relabel jargon** (`locales/.../issues.json` `qa_cockpit`): `qa:fail/qa:pass`,
  "regression: never run", "no-QA", "Mergeable" → plain English (4 locales).

## PHASE B — QA lens

- **Verdict 7 states → 3+1** (`verdict.tsx` + `qa-lens.tsx:99-107,288-329,416-468`):
  Passed (green) / Failed (red) / Not tested yet (grey) / Testing… (blue). Fold
  `pass_with_failing_cases` → "Passed" + muted "2 checks still failing";
  `blocked` → "Failed" (reason on hover); `stale` → "Passed" + small "out of
  date — re-run" hint. Never surface enum names.
- **Test-case row = `[status] Title …… [Run]`.** Cut the 8 meta tokens
  (priority/flaky/modality/criterion/kind/category/source/time) to behind row
  expand; keep at most a P1 marker on a failing P1. Consolidate 5 per-row actions
  → one **Run** + a `⋯` (View trace / File bug). (`test-cases-panel.tsx:689-897`)
- **"What's running" 4× → 2:** keep the per-row highlight + one top strip; delete
  the duplicate sticky summary; drop the "RUNS" badge.
  (`qa-live-progress.tsx:218-263` vs `test-cases-panel.tsx:394-420`)
- **Header → 2 + 1 contextual button:** Add · Generate · one state-aware primary
  (Run all → Stop → Re-run failed). Merge coverage + attention lines into the
  header count ("Test cases · 12 · 2 failing"); cut the negative-missing warning
  from the issue view.
- **Source pill only when a human overrode.** AddCase form: Title + Steps + Save
  default, kind/category/priority/modality behind "Add details ▸".

## PHASE C — Review lens

- **One readiness banner** (`review-lens.tsx:486-518,151-221`): **Ready to merge /
  N blocking / Review not run yet / Merging…**, powered by the unused
  `readiness.blocked[]`. Cut the 3× `GateCard` grid + tier line from default.
- **Delete "Tier / full / light / trivial"** everywhere user-facing — rail
  (`:511-516`) AND the stepper resting chip (`stage.ts:180-182`, ~3-line fix so
  the beat never reads "Review · FULL").
- **Show the disabled-Approve reason** (`:384,264`) — currently the hint only
  renders when the button is *enabled*.
- Relabel gate names `ci/qa/review` → "Tests / CI", "QA", "Code review"
  (`:46-60`). Cut the duplicated gate recap in the Approve confirm (`:395-402`).
  Move commit/files/reviewer + gate breakdown behind a "Details" expander;
  collapse findings unless a blocker exists. Deploy pointer → footer link.

## PHASE D — Shared stepper + jargon sweep

- **Stepper** (`sdlc-stepper.tsx`): 8 dot states → 4 (passed / failed / current /
  pending; fold blocked→failed, active+running→current); keep 1-2 animations
  (drop the connector shimmer); **drop the detail chip** ("STALE"/"FULL" jargon).
- **i18n jargon sweep** across `qa_cockpit / qa_evidence / qa_review / test_cases /
  sdlc / sprint_deploy` — plain English, 4 locales, parity test.

## Sequencing
A (Release) → B (QA) → C (Review) → D (stepper + jargon). Each is frontend-only
and mostly independent; A is the highest-impact demo win. Keep every power
affordance reachable — this changes DEFAULT visibility, not capability.
