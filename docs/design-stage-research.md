# Design Stage — Research: What It Should Actually Be

**Status:** research only, no code changes. Written 2026-07-09 against `sd-platform`.

## 0. Correcting the starting assumption

The brief for this research assumed "phases 1-3 shipped" of the 6-phase plan in
`docs/design-stage-implementation-plan.md`, with the DESIGN stage of the new
stage-cockpit (`docs/sdlc-stage-cockpit-plan.md`) being "the thinnest" lens.
That's half true and worth correcting before anything else, because it changes
what this document should recommend.

**What's actually shipped**, verified via `git log --oneline --all` and
`git merge-base --is-ancestor <sha> origin/sd-platform`:

| Plan phase | What it ships | Code | Reachable from `origin/sd-platform`? |
|---|---|---|---|
| P1 — Figma credential + MCP injection | `figma_credential.go`, `figma_mcp.go`, `figma_links.go` | ✅ shipped | ✅ pushed (`7f62b931`) |
| P2 — `design_proposal` + capture + review dialog | `design_proposal.go` (service), `design_review.go`, `design-review-dialog.tsx` | ✅ shipped, fully wired | ✅ pushed (`ab59dd98`) |
| P3 — per-project design manifest | `design_manifest.go`, `gen_design_manifest` | ✅ shipped — **plus an unplanned workspace-level manifest** | ✅ pushed (`16c5e41b`, `d29c4931`) |
| P4 — approval → decomposition + promotion | `design_decompose.go` | ✅ **fully implemented**, not the "seam" the plan described | ✅ pushed (`7a84af36`) |
| P5 — design-aware QA / gate | `sliceActionDesignCompareContext`, `AGORA_DESIGN_GATE_ENFORCED` | ✅ shipped | ✅ pushed (`64a1ae23`) |
| P6 — epic autopilot + credential lifecycle notifications | `maybeProposeDesignOnCreate`, `figma_probe.go`, `AGORA_AUTO_DESIGN_ENABLED` | ❌ **zero code** — no grep hits anywhere in `server/` | n/a |
| *(not in the plan)* `design_audit` + apply-as-codemod | repo-wide design-system governance audit | ✅ shipped (`885d1781`, `bf6eb663`) | ✅ pushed |
| *(not in the plan)* `AGORA_DESIGN_LINT_ENFORCED` | diff-scoped design-erosion gate | ✅ shipped, dark by default (`0e561e3e`) | ✅ pushed |

So: **five of six phases shipped and are already on `master`/`origin/sd-platform`**,
plus real functionality the plan never specified. Only Phase 6 (automatic
proposal-firing on Bitrix epics + nightly credential-health notifications) is
unbuilt. The separate stage-cockpit work (`docs/sdlc-stage-cockpit-plan.md`,
23 commits, all local/unpushed) then wrapped a **thin lens** —
`packages/views/issues/components/design-lens.tsx` (81 lines) — around this
already-rich backend by re-mounting four existing components. The lens is
thin; the stage is not. That reframes this research: the gap isn't "build the
design stage," it's **"stitch together what already exists, close the real
holes, and decide what Phase 6-style automation is actually worth building."**

The one thing genuinely stubbed in wired-up code (not a whole unshipped
phase) is `deploySynced` in
`packages/views/issues/components/use-stage-pipeline.ts:100-104` — unrelated
to design, left `undefined` with an explicit TODO. Design's own honest gaps
are narrower and are catalogued in §3-§5 below.

---

## 1. Stage definition

### Entry criteria — when does an issue *have* a design stage?

Two independent signals, OR'd together
(`packages/views/issues/components/use-stage-pipeline.ts:93-95`):

```ts
const hasDesignSignals =
  figmaRefsFrom(issue?.description ?? "").length > 0 || designResult != null;
```

1. **A Figma URL appears in the issue description.** Parsed client-side by
   `figmaRefsFrom` (`packages/core/figma`) and server-side by the twin
   `figmaRefsFrom`/`issueFigmaRefs` in `server/internal/handler/figma_links.go`
   — regex-matched `figma.com/{file|design|proto}/...` with `node-id` query
   extraction. This is a pure text scan; there's no label or project setting
   that gates entry today.
2. **A design QA result already exists** — `qa_evidence.result_json.design`
   is non-null (from a prior `run_qa`'s design-compare pass).

There is **no per-project toggle** that turns the design stage on/off. The
`design_auto` scalar (`off|epics|all`) is accepted and validated server-side
(`design_manifest.go:115-127`) but **has no frontend control to set it and no
backend consumer reads it** — it's dead, write-only config left over from the
unbuilt Phase 6. Today, entry is purely "does this issue's text contain a
Figma link."

When there are no signals, `deriveDesignStage` returns `skipped`
(`packages/core/issues/stage.ts:78-80`) and the stepper renders it dimmed,
never blocking — correct behavior for the majority of issues that touch no UI.

### Exit criteria — what does "design passed" mean?

This is where the model is genuinely ambiguous, because **two different
things both compete to mean "design is done":**

**(a) Proposal approval** — a human clicks Approve in `DesignReviewDialog`
(`packages/views/issues/components/design-review-dialog.tsx`), which calls
`POST /api/issues/{id}/design-review` (`design_review.go`). This swaps the
issue's label to `design:approved` and — for real, not a stub —
**decomposes the proposal into sub-issues** via
`decomposeApprovedProposal` (`design_decompose.go`). This is a workflow gate:
"the plan is signed off," not "the built thing matches the design."

**(b) Design verdict pass** — the stepper's actual exit condition
(`packages/core/issues/stage.ts:81-94`):

```ts
if (input.designVerdict === "pass" || input.qaVerdict === "pass" || status === "done") {
  return { stage: "design", state: "passed" };
}
```

`designVerdict` comes from `qa_evidence.result_json.design.verdict`
(`pass|fail|skipped`), produced by the **advisory design-compare check**
that `run_qa` runs when it has Figma refs + a readable credential
(`sliceActionDesignCompareContext`, `design_action.go:247-275`). Note the
`qaVerdict === "pass"` branch: **a plain functional QA pass counts as a
design pass too**, even if the design-compare check never ran (`skipped`) or
never happened at all. That's a real ambiguity — the stepper can show
"design: passed" on an issue whose UI was never actually checked against its
Figma source, purely because QA was green. It's a deliberate default-open
choice (documented as "an override" in the type comment,
`stage.ts:53`) so that non-pixel-perfect legacy work doesn't get stuck, but it
means the stepper's green checkmark on Design does not reliably mean "the
Figma comparison passed."

These two gates (approval vs. verdict) are **not linked by any signal** —
approving a proposal doesn't set a verdict, and a design verdict doesn't
touch the approval labels. A human reading the stepper has no way to tell
which kind of "done" they're looking at.

### Who acts

| Actor | Role | Resolved by |
|---|---|---|
| Design-aware agent | Writes the `design_proposal`, `gen_design_manifest`, `design_audit` outputs | `resolveDesignerAgent` (`design_action.go:33-48`): `project.settings.design_agent` → leader of a squad whose name contains "design" → falls through to the generic slice-action agent resolver |
| Human (any member) | Approves/rejects the proposal via `DesignReviewDialog`; the only actor who can trigger decomposition | `design_review.go` — member-gated, `requireUserID` |
| Whichever agent runs `run_qa` | Produces the design-compare verdict and design-lint findings | **Not** necessarily the design-aware agent — it's whoever executes `run_qa` on the issue (often a QA-squad agent or the implementer). This is a real role gap, see §4. |
| Human (project owner, informally) | Curates the design manifest by hand via the JSON editor | `PutProjectDesignManifest`/`PutWorkspaceDesignManifest` (`design_manifest.go:40,170`) — writes set `source:"manual"`, which blocks future agent auto-overwrites |

---

## 2. Signals — the label problem

The task's suspicion was correct: **the design verdict hides inside a JSON
blob and has no board-visible signal**, and that's the single highest-leverage
gap in the whole system.

**What exists as labels today:** `design:proposed` / `design:approved` /
`design:changes_requested` — mutually-exclusive **workflow** state labels,
attached/detached server-side (confirmed via `design_review.go` header
comment: *"Labels remain the human-visible state so the issue list can filter
on design:proposed / design:approved / design:changes_requested"*). These are
genuinely useful and board-filterable today.

**What does NOT exist:** any `design:pass` / `design:fail` label. Confirmed
by grep across `server/` and `packages/` — zero hits outside this research.
The verdict lives only in:

- `qa_evidence.result_json.design.verdict`, read by `designVerdictOf`
  (`design_action.go:401-415`) for the (dark, opt-in) done-gate, and
- the same field, read again independently by
  `use-stage-pipeline.ts:114-116` for the stepper.

Two independent consumers parsing the same undocumented JSON shape is exactly
the kind of drift the repo's own API-response-compatibility doctrine warns
about (`CLAUDE.md` → *API Response Compatibility*) — it just hasn't bitten
yet because both are server/first-party TS, not an external contract.

**Concretely, today, you cannot:**
- Filter the issue board for "designs that failed comparison"
- See a design-verdict status without opening the issue (or scrolling to the
  QA lens's design-compare section)
- Distinguish, in a list view, "design never checked" from "design checked
  and skipped" from "design checked and passed"

### Recommended minimal signal upgrade

Mirror the exact pattern `qa:pass`/`qa:fail` already uses — Agora has the
label-exclusivity machinery (`SetDesignStateLabel`-equivalent, built for the
proposal-state labels) ready to extend:

- When a `run_qa` result carries `design.verdict === "pass"`, attach
  `design:pass` (detach `design:fail` if present).
- When `"fail"`, attach `design:fail` (detach `design:pass`).
- When `"skipped"`, touch nothing — mirrors the "never fail for infra
  reasons" doctrine already baked into the recipe text
  (`design_action.go:271-274`).
- The stepper reads the label instead of (or in addition to, for
  backward-compat) parsing `qa_evidence` — one board-visible signal, one
  source read by every consumer.

This closes the exact gap the stage-cockpit plan itself flagged as a
follow-up but didn't schedule: *"design pass/fail label for board filtering"*
(`docs/sdlc-stage-cockpit-plan.md:169`).

---

## 3. Agent verbs — what exists vs. what's missing

| Verb | Mechanism | Status | What it produces |
|---|---|---|---|
| Read + propose | `design_proposal` slice action (`slice_action.go:199-239`) | ✅ shipped | `design-proposal` fenced JSON block: screens, component reuse/extend/new verdicts, deviations, sub-issue decomposition plan |
| Build/refresh design system | `gen_design_manifest` slice action (`slice_action.go:240-261`) | ✅ shipped | `design-manifest` block → `project.settings.design_manifest` (or workspace-level) |
| Repo-wide governance audit | `design_audit` slice action (`slice_action.go:262-285`) | ✅ shipped, **not in the original plan** | `design-audit` block: off-token values, duplicated markup, unmanaged components, proposed tokens |
| Turn an audit finding into work | `POST /api/issues/{id}/design-apply` (`design_apply.go`) | ✅ shipped, **not in the original plan** | A new codemod issue (token adoption or component extraction), assigned so the normal trigger starts it |
| Compare built UI to Figma | `sliceActionDesignCompareContext`, appended to `run_qa` only (`design_action.go:247-275`, wired at `slice_action.go:2878` and its `maybeRunQAOnInReview` equivalent) | ✅ shipped, but **not a standalone verb** — only runs as a sub-step of QA | `qa-result.design.{verdict, reference_node, mismatches[]}` — deterministic DOM/`getComputedStyle` assertions, explicitly never pixel-diffing |
| Lint a diff against the design system | `sliceActionDesignLintContext`, also appended to `run_qa` (`design_action.go:294-305`) | ✅ shipped, **not in the original plan** | `qa-result.design.lint[]` — off-token / duplicate-component findings scoped to the diff |
| Manifest → implementer handoff | `sliceActionDesignManifestContext` injected into `draft_code` (`slice_action.go:2844-2854`) **and** `run_qa` (`:2874`) **and** all three design actions themselves | ✅ shipped, confirmed at the exact call sites | Free-text token/component/convention block prepended to the implementer's instruction |

### What's missing

- **A design-compare verb independent of `run_qa`.** Today the only way to
  get a design verdict is to run full QA. There's no lightweight
  "just check design fidelity" action a human or a decomposed-child's
  implementer could fire on demand without also running the whole functional
  suite.
- **Design-stage task attribution.** `runningTaskStages` — the signal that
  makes the stepper show "running" — only ever resolves to `"qa"` or `"dev"`
  (`use-stage-pipeline.ts:85-87`, mirroring `qa-live-progress.tsx:88-110`).
  No caller ever attributes a task to `"design"`, so `deriveDesignStage`'s
  `running.has("design")` branch (`stage.ts:91-93`) is dead in practice — a
  `design_proposal`/`design_audit`/`gen_design_manifest` task in flight is
  indistinguishable from a `dev` task in the stepper. Fixing this is a
  frontend-only change: attribute a running task to `"design"` when its slice
  action kind is one of the three design actions (the task snapshot already
  carries `kind` for this purpose in QA's own attribution logic).
- **Figma comment sync** — posting agent findings back into Figma as
  comments. Explicitly deferred in the original plan (§8) for good reason
  (needs a paid Figma plan + `webhooks:write`); still true, not worth
  reopening.
- **Code Connect consumption** (see §5) — `design_proposal`'s reuse/extend/new
  classification is currently a screenshot-vs-repo guess, not grounded in an
  authoritative Figma-side component mapping.
- **A dedicated design-compare agent role.** The verdict is produced by
  whoever happens to run `run_qa` — not necessarily the design-aware agent
  `resolveDesignerAgent` resolves for proposals. External research (§5) flags
  this as a known reliability problem: an agent grading its own (or a peer's)
  visual output is a weaker check than routing it through an independent
  reviewer persona.

---

## 4. Lens content v2

`design-lens.tsx` (81 lines, `packages/views/issues/components/design-lens.tsx`)
today re-mounts, in order: `FigmaLinksSection` (:69) →
`DesignProposalSection` (:70) → `DesignAuditSection` (:71) →
`QADesignCompare` (:72), each self-fetching and self-gating, separated by
`hasSignals`-gated dashed empty state (:48-77). These are the **exact same
components** already mounted in `issue-detail.tsx`'s sidebar and `qa-lens.tsx`
— the lens adds a frame, not new content. That's fine per the cockpit's own
architecture ("every lens body is existing components re-mounted"), but it
means the *content* upgrade opportunities below are the real lever, not the
lens wrapper itself.

**Highest-value, lowest-effort: render the screenshots agents already
capture.** The `design_proposal` recipe instructs the agent to download and
attach a Figma reference render per screen
(`figma-<node-id>.png`, `slice_action.go:204-207`) — and
`DesignReviewDialog` already renders those as a real screens gallery
(`design-review-dialog.tsx:166-199`, matched by filename against comment
attachments). The design-compare recipe separately instructs the agent to
*"screenshot both sides and attach them as evidence"*
(`design_action.go:265`) — but `QADesignCompare`
(`packages/views/qa/components/qa-design-compare.tsx`) **never renders an
`<img>` anywhere in the file**; it only shows a verdict badge and a text
mismatch table. The exact same filename-matching pattern
`DesignReviewDialog` already uses could resolve those before/after
screenshots and render them side-by-side — this is a pure frontend change,
zero backend work, reusing data that's already being produced and thrown
away.

**Proposal → build traceability.** Nothing today visually connects "this
sub-issue came from proposal comment X, plan index N"
(`design_plan_index` metadata, stamped by `design_decompose.go`) back to its
own design-compare result. On a decomposed child, the lens could show the
originating screen thumbnail from the parent's proposal next to that child's
own verdict — closing the loop from proposal to implementation to
verification in one view.

**Token/audit rollup.** `gen_design_manifest` and `design_audit` both produce
structured data rendered per-issue (`DesignAuditSection` on whichever chore
issue ran the audit), but there's no cross-issue rollup — e.g. "N of M
proposed tokens have been applied" isn't visualized anywhere, even though
`ApplyDesignAudit` (`design_apply.go`) already creates a traceable codemod
issue per applied finding. Given "no design cockpit/queue page" is an
explicit, still-valid non-goal (2-10 person team, premature chrome), this
should be a compact strip inside the existing lens fed by the latest audit
chore issue — not a new page.

**Verdict badge once §2 ships.** With a `design:pass`/`design:fail` label in
place, the lens header can show a stable badge without re-parsing
`qa_evidence` on every render.

---

## 5. Figma integration depth — what the platform realistically gets

*(External research, 2025-2026 sources)*

**Official Figma Dev Mode MCP Server** launched publicly June 2025
([Figma blog](https://www.figma.com/blog/introducing-figma-mcp-server/)),
exposing `get_design_context`, `get_metadata`, `get_screenshot`,
`get_variable_defs`, `get_code_connect_map`/`add_code_connect_map`, and
`create_design_system_rules`
([tools docs](https://developers.figma.com/docs/figma-mcp-server/tools-and-prompts/)).
Two deployment modes exist — a desktop-app-resident server (requires Dev Mode
running locally) and a hosted remote server
(`https://mcp.figma.com/mcp`). **Neither works for an unattended background
agent**: the remote server authenticates via a browser OAuth flow and is
restricted to an allowlisted MCP-client catalog; personal access tokens are
explicitly unsupported
([Figma forum: "Support for PAT-based auth"](https://forum.figma.com/ask-the-community-7/support-for-pat-personal-access-token-based-auth-in-figma-remote-mcp-47465)).
This directly confirms Agora's Phase 1 decision (D1 in the original plan) to
use the community **Framelink `figma-developer-mcp`** server instead — pinned
at `figma_mcp.go:11,224` and `packages/core/mcp/types.ts:147` — remains the
only viable path for Agora's cloud daemon as of this research, not a
stopgap that's since been superseded.

**Code Connect** — Figma's node-to-real-component mapping, published via
`npx figma connect publish`
([quickstart](https://developers.figma.com/docs/code-connect/quickstart-guide/)) —
is a concrete capability Agora doesn't use yet. When present, `get_design_context`
returns an authoritative code reference for a mapped node instead of the
agent guessing from a screenshot. Figma even ships an **agent skill**
(`figma-code-connect`) designed for autonomous consumption: it scans a
selection for unmapped components, greps the codebase for likely matches, and
writes/publishes new mappings itself
([mcp-server-guide skill](https://github.com/figma/mcp-server-guide/blob/main/skills/figma-generate-library/references/code-connect-setup.md)).
Today, `design_proposal`'s REUSE/EXTEND/NEW classification
(`slice_action.go:210-214`) is a screenshot-vs-repo visual guess. A Code
Connect mapping (readable via the plain REST API even without the MCP
server) would upgrade this from a guess to a lookup — but requires someone
to author and maintain the mappings first; if they drift from the real
component API, the agent silently falls back to guessing again.

**Figma Variables API** — `gen_design_manifest`'s own recipe tells the agent
*"Do NOT attempt the Figma Variables API (enterprise-only)"*
(`slice_action.go:253`). Variables/token-tier access has shifted since that
line was written; it's worth a one-time revalidation against the workspace's
actual plan. If available, it would upgrade the manifest's token census —
today derived by frequency-ranking raw values out of markup — to
authoritative published values for token-based (non-legacy) projects.

**Token drift check.** Best current practice syncs Figma Variables through a
Style Dictionary build step into platform code (CSS vars, Tailwind config)
([pipeline writeup](https://medium.com/@alexdev82/how-we-built-a-reliable-design-token-pipeline-from-figma-to-react-fddf4725cdbe)).
Agora doesn't need the full pipeline to get a useful check: reuse
`get_variable_defs` (once/if available) or the manifest's own `tokens` block,
and diff those hex/numeric values against the equivalent resolved CSS custom
properties in the built app — the exact same `getComputedStyle` technique the
design-compare check already uses, aimed at token *definitions* instead of
per-screen implementation.

**Design review agents / visual regression landscape.** Percy, Chromatic, and
Applitools all lean on AI-noise-filtered *pixel* diffing to make screenshot
comparison usable for a human reviewer
([Percy blog](https://percy.io/blog/visual-testing-tools),
[Applitools](https://applitools.com/solutions/figma/)). Raw pixel diffing —
without that noise-filtering layer — has documented 20-40% false-positive
rates on CI from font-rendering/anti-aliasing/GPU variance alone
([dev.to: why visual regression tests fail](https://dev.to/maria_bueno/why-your-visual-regression-tests-are-failing-and-how-to-fix-them-26kg)).
Agora's explicit doctrine — *"Compare DETERMINISTICALLY, NOT by pixels"*
(`design_action.go:249-251,263`) — is a deliberate and, per this research,
well-founded choice for an **autonomous agent** doing the comparing (an
agent can't reliably eyeball-filter rendering noise the way a human can, and
Agora hasn't built — nor should it build — the ML noise-filtering layer that
Percy/Chromatic sell). The accepted tradeoff: DOM/computed-style assertions
only catch what's explicitly asserted, and can miss composed-visual problems
(element overlap, z-order, a broken image) that a screenshot shows a human
instantly
([Karate Labs: DOM vs. screenshot AI testing](https://karatelabs.io/blog/dom-vs-screenshot-ai-testing)).
That's exactly why rendering the screenshots the recipe already asks agents
to capture (§4) is worth doing as a **human-reviewed secondary channel**,
even while the automated verdict itself stays DOM-based.

**Self-grading caveat.** Multiple sources flag that an agent grading its own
visual output is unreliable — most directly, a Jane Street engineering post
on building UI with Claude Code notes *"Claude cannot grade its own
output"* reliably, relying instead on a human-in-the-loop
screenshot-critique cycle
([Jane Street blog](https://blog.janestreet.com/i-design-with-claude-code-more-than-figma-now-index/)).
Today the design-compare verdict runs inside whichever agent executes
`run_qa` — not necessarily the design-aware persona `resolveDesignerAgent`
resolves for proposals. Routing the compare step through that same
resolution chain when a design agent/squad exists is a candidate fix (see
Phase 2 below).

**Linear + Figma** ([docs](https://linear.app/docs/figma)) offers a plugin
for linking issues to frames with bidirectional status sync and an embed for
interactive previews — but preview only works for publicly shared files, and
there's no AI/agent-adjacent feature. Not much to borrow architecturally;
Agora's approach (parse Figma URLs out of free-text description, no
dedicated linking UI) is already lighter-weight and works today.

---

## 6. Handoff — design manifest into the dev-agent brief

This is the part of the system that's already working best, and worth stating
plainly so it isn't accidentally reinvented. `sliceActionDesignManifestContext`
(`design_action.go:173-182`) renders the **workspace-level manifest first,
then the project-level manifest** (so multiple projects sharing one design
system — e.g. the SalesDoctor apps — converge on one base while keeping
per-project specifics), and is injected at every point that matters:

- `draft_code` (the implementer action) — `slice_action.go:2854`, gated
  inside the `sliceActionOpensPR` branch, appended right after the
  QA-docs context so the implementer gets *both* the intended-behavior spec
  and the design system in the same prompt.
- `run_qa` — `slice_action.go:2874`, so the design-compare/lint checks judge
  against the same manifest the implementer built against.
- All three design actions themselves (`design_proposal`, `gen_design_manifest`,
  `design_audit`) — `slice_action.go:2854,2874,2922,2931,2935`.

Separately, for issues created by decomposition, `design_decompose.go`
composes a **"Design context" section directly into the child issue's
description** (Figma URLs with node-ids, applicable component verdicts) —
so even an implementer whose prompt-assembly path doesn't call
`sliceActionDesignManifestContext` still gets the design context, because
descriptions are always read.

**Nothing needs to change here for the handoff itself.** The one adjacent gap
is the missing design-stage task attribution (§3) — an implementer working a
design-decomposed child shows as `"dev"` in the stepper with no visual
distinction from a plain issue, even though its brief is materially richer.

---

## 7. Phased recommendation

Given how much is already shipped, this is not a rebuild — it's four small,
independently-shippable slices that close the concrete gaps above. None
require a migration; all reuse existing storage (`qa_evidence.result_json`,
label tables, `project.settings`).

### Phase 1 — Signal upgrade: `design:pass`/`design:fail` labels
**Effort: small, backend.** Extend the existing design-QA capture path
(wherever `qa-result.design.verdict` is currently only read, not acted on) to
attach/swap `design:pass`/`design:fail` mirroring the `qa:pass`/`qa:fail`
label pattern and the existing `design:proposed/approved/changes_requested`
exclusivity machinery. `skipped` touches nothing. Update
`deriveDesignStage` to prefer the label when present, falling back to the
JSON field for older evidence rows (no backfill needed — enum-drift-safe by
construction). This single change makes design status board-filterable and
collapses two independent JSON-parsing call sites into one label read.
*Unblocks:* board filtering, a stable lens badge (§4), any future project
health rollup.

### Phase 2 — Lens content v2: render what agents already capture
**Effort: small-medium, frontend only.** Two independent, additive UI
changes to `QADesignCompare` (`qa-design-compare.tsx`):
1. Resolve and render the Figma-reference and built-screen screenshots
   side-by-side (copy the attachment-matching pattern already proven in
   `DesignReviewDialog`, `design-review-dialog.tsx:166-199`) as a secondary,
   human-reviewed channel alongside the existing deterministic mismatch
   table — directly addressing the DOM-assertion blind spot documented in §5
   (composed-visual issues like overlap/z-order that no computed-style
   assertion catches).
2. Attribute running `design_proposal`/`gen_design_manifest`/`design_audit`
   tasks to the `"design"` stage in `use-stage-pipeline.ts:85-87` instead of
   defaulting to `"dev"` — a filter-by-`kind` change, same pattern QA
   attribution already uses.
*No backend changes; no new data.*

### Phase 3 — Independent design-compare verb + reviewer routing
**Effort: medium, backend + agent-prompt.** Two related fixes motivated
directly by the external research in §5:
1. Extract `sliceActionDesignCompareContext` into its own lightweight slice
   action (e.g. `design_compare`) that a human or a decomposed child's
   workflow can fire without running the full `run_qa` suite — useful for
   spot-checking a screen mid-implementation instead of waiting for the
   in-review QA pass.
2. When a design agent/squad is resolvable (`resolveDesignerAgent`), route
   the compare step through it instead of whichever agent happens to run
   QA — addressing the self-grading reliability concern
   ([Jane Street](https://blog.janestreet.com/i-design-with-claude-code-more-than-figma-now-index/)).
   Falls back to the current behavior when no design persona exists, so it's
   additive, not a breaking change to teams without a design squad.

### Phase 4 (optional, gated on real signal) — Code Connect + token drift
**Effort: medium-large, backend + external setup dependency.** Only worth
doing once a team actually maintains Code Connect mappings and/or has
Variables API access on their Figma plan — both are prerequisites outside
Agora's control. If/when available:
1. Read `get_code_connect_map` (via REST, since the headless daemon can't
   use the official MCP) and feed authoritative code refs into
   `design_proposal`'s classification step, replacing the current
   screenshot-guess for mapped components.
2. Add a token-drift check reusing the design-compare's `getComputedStyle`
   technique, aimed at the manifest's `tokens` block vs. resolved CSS custom
   properties, as an additional `qa-result.design` finding kind.
Revalidate the "Variables API is enterprise-only" assumption baked into
`slice_action.go:253` before scoping this phase.

**Deliberately not recommended:** rebuilding Phase 6 (Bitrix-epic
auto-fire + credential lifecycle notifications) as written. It's real,
useful automation, but nothing in this research surfaced urgency for it
beyond the original plan's own framing — it should stay a backlog item
scoped on its own, not bundled into closing the gaps this document found.
Also not recommended: a dedicated design queue/cockpit page — the original
plan's non-goal reasoning (premature chrome at 2-10-person scale) still
holds, and Phase 2's lens-level rollup gets most of the value without a new
surface.

---

## Appendix — sources

**Codebase** (all paths relative to repo root, verified 2026-07-09):
- `docs/design-stage-implementation-plan.md` — original 6-phase plan
- `docs/sdlc-stage-cockpit-plan.md` — stage-cockpit architecture
- `server/internal/handler/design_action.go`, `design_review.go`,
  `design_decompose.go`, `design_manifest.go`, `design_apply.go`,
  `figma_credential.go`, `figma_mcp.go`, `figma_links.go`
- `server/internal/handler/slice_action.go` (recipes + injection call sites)
- `packages/core/issues/stage.ts`
- `packages/views/issues/components/{design-lens,design-proposal-section,
  design-review-dialog,design-audit-section,figma-links-section,
  use-stage-pipeline}.tsx`
- `packages/views/qa/components/qa-design-compare.tsx`
- `packages/views/projects/components/project-design-section.tsx`
- `packages/core/mcp/types.ts`

**External** (2025-2026):
- Figma, [Introducing the Figma MCP Server](https://www.figma.com/blog/introducing-figma-mcp-server/) (June 2025)
- Figma, [Dev Mode MCP Server tools & prompts](https://developers.figma.com/docs/figma-mcp-server/tools-and-prompts/)
- Figma forum, [Support for PAT-based auth in Figma remote MCP](https://forum.figma.com/ask-the-community-7/support-for-pat-personal-access-token-based-auth-in-figma-remote-mcp-47465)
- Figma, [Code Connect quickstart](https://developers.figma.com/docs/code-connect/quickstart-guide/)
- Figma, [figma-code-connect agent skill](https://github.com/figma/mcp-server-guide/blob/main/skills/figma-generate-library/references/code-connect-setup.md)
- Design token pipeline writeup, [Medium](https://medium.com/@alexdev82/how-we-built-a-reliable-design-token-pipeline-from-figma-to-react-fddf4725cdbe)
- Linear, [Figma integration docs](https://linear.app/docs/figma)
- Percy, [Visual testing tools blog](https://percy.io/blog/visual-testing-tools)
- Applitools, [Figma solutions page](https://applitools.com/solutions/figma/)
- dev.to, [Why your visual regression tests are failing](https://dev.to/maria_bueno/why-your-visual-regression-tests-are-failing-and-how-to-fix-them-26kg)
- Karate Labs, [DOM vs. screenshot AI testing](https://karatelabs.io/blog/dom-vs-screenshot-ai-testing)
- Jane Street engineering blog, [I design with Claude Code more than Figma now](https://blog.janestreet.com/i-design-with-claude-code-more-than-figma-now-index/)
