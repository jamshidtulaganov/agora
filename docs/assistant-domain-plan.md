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

## Phase 2 — living reports (not this PR)

- **Pin an artifact beyond its session**: attach a report artifact to a
  project or sprint so the team sees it without opening the author's chat.
  Needs an ownership/visibility model — artifacts are currently user-scoped.
- **Scheduled refresh**: automations THEN-action (or autopilot cron) re-runs a
  recipe and updates the pinned artifact — Monday 9:00 sprint report without a
  human in the loop.
- **Role-aware launcher**: order the prompt rows by the caller's role
  (workspace roster already carries it).

## Phase 3 — closing the loop (sketch)

- Recipe outputs feed the KB flywheel (a sprint report is a KB item).
- Release-notes recipe wired to the Release page's Ship loop.
- Cross-workspace rollup for owners running several workspaces.

## Non-goals

- No new server endpoints or DB migrations in Phase 1.
- No persona tabs/segmentation UI — flat, quiet launcher rows.
- No scheduled execution until Phase 2's ownership question is answered.
