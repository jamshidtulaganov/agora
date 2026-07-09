# SDLC Stage Cockpit — plan

**Goal:** run the full SDLC (Design → Dev → QA → Review → Deploy) inside Agora with
agents driving each stage — as **one coherent surface**, not five mismatched
mini-apps. The issue stays the spine (Linear-like, agent-native); stages are
**lenses** mounted into a single shared frame.

**Why now:** the QA space was built as a fork of issue-detail and visibly
diverges (width, rail, density, header). The same fork would repeat for
Design/Review/Deploy. Root cause discovered during recon: the two-pane
"detail cockpit" shell is already **copy-pasted four times** —
`issue-detail.tsx:2343-2379`, `project-detail.tsx:894-983`,
`inbox-page.tsx:422-454`, `runtimes-page.tsx:240-278` — and never extracted.

**Constraints:** local commits only, no push. Zero DB migrations. Frontend-first:
every stepper signal already exists in the data model (verified below). No
invented state.

---

## 1. Architecture

```
┌─ BreadcrumbHeader (shared, h-12 border-b) ─────────────────────────┐
│  project › MUL-123 Fix onboarding   [agent chip] [pin] [⋯] [rail]  │
├─ SDLC stepper ─────────────────────────────────────────────────────┤
│  ● Design ─ ● Dev ─ ◉ QA(running) ─ ○ Review ─ ○ Deploy            │
├──────────────────────────────────────────┬─────────────────────────┤
│  LENS BODY (per stage)                   │  context rail           │
│  issue: title/desc/activity (default)    │  PropRow strip          │
│  qa: live browser + verdict + triage     │  (status/assignee/      │
│  design: figma refs + proposal + audit   │   labels/sprint)        │
│  dev: co-code editor + gates             │  + lens rail sections   │
│  review: merge gates + PR list           │                         │
│  deploy: box sync + deploy-qa            │                         │
└──────────────────────────────────────────┴─────────────────────────┘
```

Three new shared pieces, everything else is re-mounted existing code:

1. **`CockpitFrame`** (`packages/views/layout/cockpit-frame.tsx`) — the extracted
   two-pane shell: `ResizablePanelGroup` + collapsible right panel (260/320/420,
   `useDefaultLayout` persistence, `usePanelRef` collapse/expand) + mobile
   `Sheet` fork + `BreadcrumbHeader` slot. Props:
   `{ header, children, rail, defaultRailOpen, layoutId, topStrip? }`.
2. **`InspectorSection`** (`packages/views/layout/inspector-section.tsx`) — the
   hand-rolled collapsible `<button>+ChevronRight` block currently duplicated
   ~6× inside issue-detail's sidebar and again in `qa-evidence-section.tsx`.
3. **`SDLCStepper` + stage model** — pure derivation in `packages/core`
   (section 2), stepper strip UI in `packages/views`, lens switch via `?lens=`
   query param (same pattern as `settings-page.tsx` `TAB_QUERY_KEY`:
   whitelist + `navigation.replace`; on desktop a query-only change stays
   in-tab — verified in `platform/navigation.tsx` `tryRouteToPinnedNewTab`).

**Routing decision:** one route. The cockpit IS issue-detail evolved:
`/{slug}/issues/{id}?lens=design|dev|qa|review|deploy` (no param = today's
issue view). The `/{slug}/qa` **queue** page stays (that's the cross-issue
"what needs QA" list) but its rows push `issueDetail(id) + "?lens=qa"`.
`/{slug}/qa/{id}` (`qaDetail`) is **removed** — product not live, no compat
path (CLAUDE.md rule). Update `paths.ts`, both app routes, and
`packages/core/paths/consistency.test.ts` in lockstep.

---

## 2. Stage model — derived, never stored

There is **no `stage` column and we don't add one.** Position in the cycle is
derived client-side from signals that already exist (recon-verified):

| Stage | Signals (all existing) | Passed when | Running when |
|---|---|---|---|
| Design | Figma refs in description; design evidence (`result_json.design.verdict`, `design_action.go:402`); lint findings | design verdict pass (or `qa:pass` override, mirroring `design_action.go:327`) | design-family agent task running |
| Dev | `metadata.work_mode` (`packages/core/issues/work-mode.ts`); status `in_progress`; PR (`pr_number` metadata) | PR opened or status advanced past dev | non-QA agent task running while `in_progress` |
| QA | status `in_review` + `qa:pass/fail/blocked/stale` labels + `qa_evidence` rows | `qa:pass` | QA-squad task running (attribution via squad membership — the `qa-live-progress.tsx:88-120` pattern) |
| Review | `MergeReadiness` gates (`merge_readiness.go:29` — ci/qa × pass/fail/pending, tier trivial/light/full); sprint-PR merge gate; `merge:override` | PR merged or issue `done` | — (gates poll) |
| Deploy | connected-box last-sync (`connected_box.go`), `deploy-qa` results | box synced to issue branch | deploy-qa in flight |

New core module `packages/core/issues/stage.ts`:

```ts
export type SDLCStage = "design" | "dev" | "qa" | "review" | "deploy";
export type StageState =
  | "pending" | "active" | "running" | "passed" | "failed" | "blocked" | "skipped";
export interface StageSnapshot { stage: SDLCStage; state: StageState; detail?: string }
export interface StagePipeline { stages: StageSnapshot[]; current: SDLCStage }

export function deriveStagePipeline(input: {
  status: IssueStatus;
  labels: Pick<Label, "name">[];
  workMode: "full_pipeline" | "in_editor";
  prNumber?: number | null;
  hasDesignSignals: boolean;        // figma refs or design evidence present
  designVerdict?: "pass" | "fail" | null;
  qaVerdict?: "pass" | "fail" | null;   // from qa_evidence
  mergeGates?: { ci: GateState; qa: GateState; tier: string } | null;
  prMerged?: boolean;
  runningTaskStages?: SDLCStage[];  // caller attributes tasks → stages (squad/kind)
  hasDeployTarget: boolean;         // project has a connected box / local dir
  deploySynced?: boolean;
}): StagePipeline;
```

Rules: a stage with no signals is `skipped` (Design without Figma refs,
Deploy without a target) — the stepper renders it dimmed, never blocks.
`current` = first non-passed non-skipped stage; `done`/`cancelled` issues pin
`current` to the last active stage with everything passed. Pure function, unit
tested in `packages/core` (node env). All response-derived inputs pass through
the existing zod-`parseWithFallback` layer at the query boundary — the
derivation itself never touches the network.

**Known model gaps (accepted for v1, no backend change):**
- QA and Review share the `in_review` status — the stepper distinguishes via
  labels + gates, which is exactly what the backend gate logic does.
- Task snapshot has no stage attribution — attribute via QA-squad membership
  (existing pattern) and default everything else to Dev. Slice-action-kind
  attribution is a later backend nicety, not needed for v1.

---

## 3. Lens registry

```ts
interface StageLens {
  key: SDLCStage | "issue";
  Body: React.ComponentType<{ issueId: string }>;
  railSections?: React.ComponentType<{ issueId: string }>; // appended under PropRow strip
  available: (p: StagePipeline) => boolean;  // skipped stages hide their lens
}
```

Every lens body is **existing components re-mounted** — the skin comes free
from the frame:

| Lens | Body (existing components) | Rail additions |
|---|---|---|
| issue (default) | title/description/sub-issues/activity (today's content column) | PropRow strip + existing sections (unchanged) |
| qa | `QALiveProgress` + `QALiveBrowser` (left) + verdict/`StructuredResult`/`QADesignCompare`/`TestCasesPanel`/triage bar (from `qa-review-page.tsx:283-465`) | verdict summary, `PullRequestList` |
| design | `FigmaLinksSection` + `DesignProposalSection` + `DesignAuditSection` + `QADesignCompare` | design verdict/lint counts |
| dev | co-code editor body (the `Dialog` internals of `editor-section.tsx:610` — pane switcher Live/Code/Preview/Browser + `EditorAskBar` + review bar) | `EditorGates`, `PullRequestList` |
| review | `MergeReadiness` gate cards + `PullRequestList` + lead-review status + merge/override actions | gates strip |
| deploy | `EditorDeployQA` + box sync status/history | box binding info |

**Skin contract** (from recon — every lens must match): header `h-12 border-b
px-4`; content `mx-auto w-full max-w-4xl px-8 py-8` (wide lenses — qa live
bay, dev editor — may use `max-w-[1600px]` but keep the same padding + type
scale); separators `my-8 border-t`; rail `p-4 space-y-5` + `PropRow` grid;
micro-labels `text-[11px] uppercase tracking-wide text-muted-foreground`.

---

## 4. Phases

Each phase = one sonnet subagent, own local commit(s), typecheck + targeted
vitest green before commit. **Never push. Never `git add -A`** (other-session
WIP exists in `server/cmd/server/` + `server/internal/handler/` test files —
stage only files you created/edited).

| Phase | What | Files | Depends on |
|---|---|---|---|
| **A** | Stage model: `packages/core/issues/stage.ts` + exhaustive unit tests (every state × stage; skipped-stage rules; done/cancelled pinning) | `packages/core/issues/` only | — |
| **B** | Extract `CockpitFrame` + `InspectorSection` into `packages/views/layout/`; refactor **issue-detail only** onto them (behavior-preserving — same layoutId, same defaults, mobile Sheet, skeleton); collapse the ~6 hand-rolled sidebar toggles to `InspectorSection` | `packages/views/layout/`, `issue-detail.tsx` | — (parallel with A) |
| **C** | `SDLCStepper` strip + `?lens=` switching + lens registry; mount in issue-detail under the header; default lens = issue; stepper reads `deriveStagePipeline` fed by existing queries (`issueDetailOptions`, `qaEvidenceOptions`, `api.mergeReadiness`, `agentTaskSnapshotOptions`) | `packages/views/issues/`, `packages/core` glue | A + B |
| **D** | QA lens: re-home `qa-review-page.tsx` composition into the lens; `/qa` queue rows → `issueDetail(id)?lens=qa`; delete `qaDetail` route (paths.ts + web page + desktop route + consistency test + qa-review-page file); migrate its mutations (setVerdict/rerun/sendBack/regression) into the lens | `packages/views/qa/`, `packages/core/paths/`, both app route files | C |
| **E** | Design lens (re-mount design sections) + Review lens v1 (gate cards + PR list + override) + Deploy lens v1 (deploy-qa + sync status) — three thin lenses, one phase | `packages/views/issues|qa/` | C |
| **F** | Dev lens: mount editor body as a frame content region (decouple from Dialog; keep the Dialog entry point working until parity, then remove) | `editor-section.tsx` + new lens file | C (riskiest — last) |

Follow-ups (not in this run): retrofit `project-detail`/`inbox`/`runtimes` onto
`CockpitFrame`; slice-action-kind stage attribution on the task snapshot
(backend); design pass/fail label for board filtering.

## 5. Verification per phase

- `pnpm --filter @agora/core exec vitest run issues/stage.test.ts` (A)
- `pnpm --filter @agora/views exec vitest run` for touched view suites (B–F)
- `pnpm typecheck` (all)
- Frontend container rebuild + manual smoke on `:3000` after C and D
  (`docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d --build frontend`)
- `packages/core/paths/consistency.test.ts` must pass after D (route removal)
