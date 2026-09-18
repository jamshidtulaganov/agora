-- Pinned reports — the PUBLISH act that makes one user-owned assistant
-- artifact readable by a project's workspace
-- (docs/assistant-domain-plan.md §Phase 2a).
--
-- Why a separate table rather than a workspace_id column on
-- assistant_artifact: an artifact is USER-scoped by construction (migration
-- 196) because it may aggregate numbers from every workspace its owner
-- belongs to, so there is no single workspace that could own it. Pinning does
-- not change that. The artifact stays the owner's; the pin is a separate,
-- revocable grant that says "this project's members may read the CURRENT body
-- of this report". Unpinning withdraws the grant and leaves the artifact
-- untouched, which is only expressible if the grant is its own row.
--
-- The grant is deliberately narrow. It carries the current content and
-- nothing else: revision history stays with the owner, because the earlier
-- versions were written before any decision to publish was made and may hold
-- numbers from workspaces the reader is not in. The pane always renders the
-- artifact's current version, so re-running the recipe IS the refresh — which
-- is the whole point of pinning rather than pasting the report into a comment.
--
-- project_id is the target: one obvious behavior, a report belongs on a
-- project page. workspace_id is DERIVED from that project when the pin is
-- written and then stored, not joined for on every read — every list, read
-- and WS fanout in this schema is workspace-scoped, and "who may see this"
-- must not become unanswerable because a project row moved or a join got
-- dropped from one query.
--
-- UNIQUE (artifact_id, project_id) is the idempotency backstop, not
-- decoration: pinning the same report to the same project is one pin, and the
-- write path answers a conflict with the row that already exists, so a
-- double-click (or a retried request) is a no-op instead of a duplicate on
-- the project page or a 500 in the user's face.
CREATE TABLE assistant_artifact_pin (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL REFERENCES assistant_artifact(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    pinned_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (artifact_id, project_id)
);

-- The project page's read, workspace-scoped like every other list here. The
-- workspace_id lead column is not redundant with project_id: it keeps the
-- scoping predicate and the lookup in one index, so a read can never be
-- served by an index that ignored the tenant.
--
-- The fanout read ("which workspaces must hear that this artifact changed?")
-- needs no index of its own: the UNIQUE constraint's index is keyed on
-- artifact_id first and covers it.
CREATE INDEX idx_assistant_artifact_pin_project ON assistant_artifact_pin(workspace_id, project_id);
