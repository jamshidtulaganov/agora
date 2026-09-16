# Assistant Artifacts — rich outputs from the Agora Assistant

Status: PLAN (2026-09-16). Owner: Jamshid. Companion to `docs/agora-assistant-plan.md`.

## 1. What it is

The Agora Assistant gains the ability to produce **artifacts** — standalone,
re-openable rich outputs rendered inside the app, Claude-style: analytics
dashboards, charts, report documents, comparison tables, small interactive
HTML tools. "Show me a chart of agent usage by day" stops being a text list
and becomes a themed chart in a side pane; "make me a sprint report" becomes a
document the user can reopen and iterate on ("add the QA numbers").

## 2. Grounding — what already exists (verified 2026-09-16)

- **Sandbox primitive exists and is proven**: `packages/views/editor/code-block-iframe.tsx`
  renders untrusted HTML via `srcDoc` + `sandbox="allow-scripts"` (never
  `allow-same-origin` — opaque origin, no cookies/parent access). Already used
  by HTML attachments, editor code-block previews, the full-screen preview
  modal and `/{slug}/attachments/{id}/preview`. Its comments cite Claude
  artifacts as the model. **This is the rendering core; no new sandbox design.**
- **Charts**: `recharts@3.8.0` + `packages/ui/components/ui/chart.tsx`
  (`ChartContainer`, CSS-var theme colors) — the Usage page stack. A structured
  "chart" artifact renders through this natively, staying theme-correct in
  light/dark, instead of shipping a chart library inside artifact HTML.
- **Markdown pipeline sanitizes away scripts/styles** (react-markdown +
  rehype-sanitize) — right for chat text, wrong for interactive artifacts.
  Artifacts therefore are their own render path, never injected into chat
  markdown.
- **Naming collision**: "artifact" already means a coding-agent's repo
  snapshot/preview (daemon-backed, `handler/artifact.go`). The new concept is
  code-scoped as `assistant_artifact` everywhere; it does NOT touch the daemon
  or capability-grant machinery.
- **No storage hook today**: `assistant_message` is text/JSONB only; the
  `attachment` table has no assistant linkage. v1 stores artifact content in
  its own small table (content is capped text — no object storage needed);
  the attachment/Storage pipeline stays available for a later "export/download"
  step.

## 3. Content model — four kinds, two render paths

| kind | content | rendered by | why |
|---|---|---|---|
| `chart` | JSON spec: `{type: bar\|line\|area\|pie, x, series[], rows[]}` | native Recharts via `ChartContainer` | theme-consistent, data-only (no code to sandbox), the analytics workhorse |
| `table` | JSON `{columns[], rows[][]}` | native table component | sortable later; data-only |
| `markdown` | markdown text | existing sanitized `Markdown` | reports, briefs, digests |
| `html` | ONE self-contained HTML document (inline CSS/JS) | `CodeBlockIframe` sandbox | escape hatch for interactive one-offs |

The model is steered (prompt guidance) to prefer `chart`/`table`/`markdown`
and use `html` only when interactivity genuinely needs it — structured kinds
are cheaper, safer, and always match the app theme.

## 4. Data model (migration 196)

```sql
CREATE TABLE assistant_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES assistant_session(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,                -- chart | table | markdown | html
    content TEXT NOT NULL,             -- spec JSON or document body
    version INT NOT NULL DEFAULT 1,    -- bumped by update_artifact
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_assistant_artifact_session ON assistant_artifact(session_id, created_at);
```

Caps: content ≤ 256 KB (validated server-side), ≤ 20 artifacts per session.
User-scoped like the session; no workspace column (an artifact may aggregate
cross-workspace data — access = session ownership).

## 5. Tools + API

New assistant tools (same executor/permission pattern as the rest):

- `create_artifact(title, kind, content)` → `{artifact_id, title, kind}`.
  Validates kind, size, JSON-parses chart/table specs against a schema so a
  malformed spec bounces back to the model as a correctable tool error.
- `update_artifact(artifact_id, content, title?)` → bumps `version`.
  Ownership: artifact.session must belong to the caller.

Endpoints (user-scoped block):

```
GET /api/assistant/artifacts/{id}          owner-only, full content
GET /api/assistant/sessions/{id}/artifacts list (id, title, kind, version, updated_at)
```

`tool_result` for create/update carries `{artifact_id, title, kind, version}`
— the transcript renders an ArtifactCard from that without a second fetch;
the viewer fetches content lazily.

## 6. Frontend

- **ArtifactCard** replaces the generic tool chip for `create_artifact`/
  `update_artifact` results: icon per kind, title, "v2" badge on updates;
  click opens the viewer.
- **Viewer = right split pane** on the assistant page (Claude-style): header
  (title, kind, version, copy / download buttons), body switches on kind →
  `ChartArtifact` (ChartContainer), `TableArtifact`, `Markdown`,
  `CodeBlockIframe`. Pane state in the assistant store (ephemeral). In the
  quick panel (floating Assistant popup) the card opens the full assistant
  page with the pane open — no split pane inside the small popup.
- Zod schemas + `parseWithFallback` for artifact responses; malformed chart
  spec from an old server renders a "can't render this artifact" card, never
  a crash (enum-drift rule: unknown `kind` → downgrade to code view of raw
  content).
- i18n ×4.

## 7. Security invariants

1. `html` artifacts render ONLY inside `CodeBlockIframe`'s sandbox
   (`allow-scripts`, opaque origin). Never `allow-same-origin`, never inline
   into the page, never through the markdown pipeline.
2. `chart`/`table` render from parsed data structures — content is never
   interpreted as markup.
3. Artifact content is model-generated from tool results the user could read
   anyway — same trust envelope as chat text; the sandbox exists because the
   model can be steered by injected issue content into emitting hostile HTML.
4. Owner-only access; no sharing in v1 (sharing = explicit later phase with
   its own review).

## 8. Phases

**Phase 1 (backend)**: migration 196, sqlc, the two tools + prompt guidance
("prefer chart/table/markdown; html only for interactivity"), two endpoints,
tests: ownership isolation, size/kind/spec validation, chart-spec round-trip,
update bumps version, permission meta-test coverage.

**Phase 2 (frontend)**: schemas, ArtifactCard, split-pane viewer with the four
renderers, store wiring, quick-panel behavior, i18n, tests (drift tests for
artifact schemas; kind-downgrade rendering test; card→pane open flow).

**Phase 3 — Live artifacts + autopilot binding (owner request 2026-09-16)**

Goal: an artifact stops being a snapshot — an autopilot (or a schedule) keeps
it fresh, and the assistant can create those autopilots itself.

1. **Autopilot tools in the assistant catalog** (same in-process handler
   pattern): `list_autopilots`, `create_autopilot(...)` mirroring exactly what
   the autopilot HTTP handlers accept (schedule, prompt, agent/squad,
   project), `update_autopilot` (enable/disable, schedule, prompt),
   `run_autopilot_now` if the handler exposes it. Autopilots leave the
   excluded list; automations (WHEN/IF/THEN engine) stay excluded for now.
2. **Scheduled artifact refresh — the "live" mechanism.** Refresh runs INSIDE
   the assistant architecture, not the daemon pipeline: a stored refresh spec
   `{artifact_id, prompt, schedule}` re-runs a headless assistant run as the
   owning user whose brief is "re-ground the data with tools, then
   update_artifact" — cheap server-side loop, no runtime needed, permission
   envelope identical to the user asking by hand. Implemented on the existing
   `internal/scheduler`. New tool: `schedule_artifact_refresh(artifact_id,
   cron, prompt?)` (prompt defaults to the artifact's producing conversation
   turn).
3. **Binding to an autopilot**: `bind_artifact_to_autopilot(artifact_id,
   autopilot_id)` — on that autopilot's task-completed event, trigger the same
   refresh run, so a dashboard updates the moment its autopilot finishes
   (e.g. nightly QA sweep → QA dashboard artifact refreshes). Listener rides
   the existing bus (`EventTaskCompleted`), storage = a nullable
   `autopilot_id` + refresh columns on `assistant_artifact` (migration 197).
4. Version history stays append-on-update (`version` bump) so a refreshed
   dashboard is still inspectable over time (UI: version picker later).

Sequencing: builds strictly AFTER Phase 1/2 land — same files
(`tools.go`, `assistant_tools.go`, migration chain).

**Phase 4 (later, separate review)**: download/export via attachment pipeline,
share-with-workspace, artifact gallery per session.
