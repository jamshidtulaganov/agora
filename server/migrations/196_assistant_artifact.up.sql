-- Assistant artifacts — standalone, re-openable rich outputs the Agora
-- Assistant produces (chart / table / markdown / html), rendered in their own
-- pane rather than inlined into chat markdown.
-- See docs/agora-assistant-artifacts-plan.md §3-§4.
--
-- Code-scoped as `assistant_artifact` on purpose: "artifact" already means a
-- coding agent's repo snapshot/preview elsewhere in this schema, and the two
-- concepts share nothing.
--
-- User-scoped like the session that produced it, with NO workspace_id: one
-- artifact may aggregate data from every workspace the person belongs to, so
-- access is session ownership, not workspace membership. user_id is the
-- denormalized owner (always the session's owner) so a list never needs the
-- join; the session join stays the authoritative ownership check.
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
