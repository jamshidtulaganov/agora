# Assistant domain plan — from generic chat to Agora-native reports

Status: Phase 1 in progress · Owner: Jamshid · Drafted 2026-09-19

## Thesis

The Assistant already has full parity plumbing — 65 tools spanning issues,
projects, sprints, labels, agents, skills, automations, autopilots, members,
usage, QA state, and a four-kind artifact workbench (markdown / table / chart /
html). What it lacks is **domain shape**: it answers whatever it is asked, but
nothing encodes the reports every Agora team actually needs each week. Users
must invent the prompt, the tool choreography, and the output format themselves
— so most never discover that "sprint report" is three tool calls away.

Domain value = **standing recipes**: one command → one well-shaped artifact,
with Agora's own concepts (sprints, QA queue, agents, usage) baked in. No new
backend tools are needed; the read layer (`list_issues`, `list_sprints`,
`qa_status`, `activity_digest`, `usage_summary`, `list_my_issues`,
`inbox_summary`) already covers every recipe below. Schema realities the
recipes are written against: `activity_digest` takes `{workspace_id?,
since_days?}` and the human/agent split comes from `actor_type` in its
result rows; `list_issues` has no sprint or label filter and its rows carry
no assignee — sprint reports scope by the sprint's project, and attribution
always goes through `get_issue`.

## Personas

| Persona | Weekly need | Recipe |
| --- | --- | --- |
| PM / lead | where is the sprint, what is stuck | **Sprint report** |
| PM / QA | is the QA queue healthy | **QA health** |
| PM | what shipped, in human words | **Release notes** |
| Developer | what needs me today | **My day** (exists as `/my_tasks` — recipe sharpens the answer shape) |
| Everybody | what happened since yesterday | **Standup digest** |
| Owner / admin | what did the agents burn | already served by `/usage` |

## Phase 1 — recipes (this PR)

**1. Backend — `writeReportRecipes` section in the system prompt**
(`server/internal/assistant/prompt.go`). Each recipe names its trigger
phrases, the tools to call, the artifact kind, and the standard section shape.
Recipes are prompt-level, not code-level: the model composes existing tools,
so a recipe costs no new endpoint and inherits every grounding/coverage rule
already in the prompt (scope objects, truncation honesty, no invented ids).

- **Sprint report** — active sprint via `list_sprints`, issues by status via
  `list_issues`, QA via `qa_status` → one **markdown artifact**: headline
  (done / total, days left), blocked list with owners, in-review + QA queue,
  risks. Status-distribution **chart** only when the user asks for a visual.
- **Standup digest** — `activity_digest` with `since_days: 1`, grouped by
  `actor` and split by `actor_type` → markdown, people then agents,
  most-active first, silent members omitted rather than listed as zeroes.
- **QA health** — `qa_status` + `list_issues` (in_review / blocked) →
  **table artifact** of the queue plus a two-line verdict.
- **Release notes** — `list_issues` (done, window or sprint) grouped by
  project/label → markdown changelog in product voice: what changed for the
  user, not issue titles verbatim.
- **My day** — `list_my_issues` + `inbox_summary` → short prioritized answer
  **in chat, no artifact** unless asked — a developer glances at this, they
  don't file it.

Shared recipe rules: reports of substance become artifacts (shareable,
revisable); re-running a recipe in the same session **updates** the existing
artifact via `update_artifact` instead of minting a duplicate; every report
states its window and its scope coverage.

**2. Frontend — four new slash commands**
(`packages/views/assistant/lib/slash-commands.ts` + `slash-menu.tsx`):
`/sprint_report`, `/standup`, `/qa_health`, `/release_notes` — all `send`
actions whose localized payloads phrase the request the way the backend recipe
expects. `/my_tasks` already covers the developer entry point.

**3. Frontend — domain prompts in the empty state** (`empty-state.tsx`):
extend the launcher rows from four generic prompts to eight, the new four
being the recipe entry points. Flat rows, no persona tabs or grouping chrome —
subtraction over configuration.

**4. Locales** — en / zh-Hans / ru / uz, keeping `parity.test.ts` green and
following the conventions glossary (docs page is the source of truth).

## Phase 2a — pinned reports (in progress)

An artifact is user-scoped by design (it may aggregate every workspace the
owner belongs to). Pinning is therefore a deliberate **publish act**: the
owner attaches a report to a project, and from then on every member of that
project's workspace can read it — like pasting a report into a comment, but
live: the pane always shows the artifact's current version, so re-running the
recipe IS the refresh.

Model — a separate pin table, the artifact stays user-owned:

    assistant_artifact_pin(id, artifact_id → assistant_artifact CASCADE,
        workspace_id, project_id, pinned_by, created_at,
        UNIQUE(artifact_id, project_id))

Pin targets a **project** (one obvious behavior; workspace_id is derived and
stored for scoping). Only the artifact's owner can pin; owner or a workspace
admin/owner can unpin. A pin grants members READ of the current content only
— revision browsing stays with the owner.

API (workspace rules as everywhere: membership gate, X-Workspace-ID):

- `POST /api/assistant/artifacts/{id}/pins` `{project_id}` → 201, owner-only,
  owner must be a member of the project's workspace.
- `DELETE /api/assistant/artifacts/{id}/pins/{pinId}` → 204, owner or
  workspace admin/owner.
- `GET /api/projects/{id}/reports` → pin metadata list (no content):
  pin_id, artifact_id, title, kind, version, updated_at, pinned_by, owner.
- `GET /api/reports/{pinId}` → metadata + current content, members only.

Frontend: a Pin action in the artifact pane header (owner side, with a plain
"everyone in this workspace can read it" line in the dialog); a quiet Reports
section on the project page listing pinned reports, opening in a read-only
viewer that reuses the artifact renderers. No new routes. WS event on
pin/unpin/update invalidates the project's reports query.

Non-goals for 2a: no assistant `pin_artifact` tool (UI-only publish keeps the
disclosure decision a human click), no sprint-level pins, no workspace-level
pins without a project.

## Phase 2b — scheduled refresh (next)

Autopilots are cron-scheduled **agent tasks** (runtime-backed) — the wrong
plane for this. 2b needs a small scheduler path of its own: cron → server-side
assistant run under the owner's identity with the recipe's fixed prompt,
updating the bound artifact. Open questions: provider spend without a human in
the loop, failure/retry policy, and where the run transcript lands. Design
after 2a ships.

## Phase 2c — role-aware launcher (cheap, anytime)

Order the launcher prompt rows by the caller's role (the workspace roster in
the prompt already carries it).

## Phase 3 — closing the loop (sketch)

- Recipe outputs feed the KB flywheel (a sprint report is a KB item).
- Release-notes recipe wired to the Release page's Ship loop.
- Cross-workspace rollup for owners running several workspaces.

## Non-goals

- No new server endpoints or DB migrations in Phase 1.
- No persona tabs/segmentation UI — flat, quiet launcher rows.
- No scheduled execution until Phase 2's ownership question is answered.
