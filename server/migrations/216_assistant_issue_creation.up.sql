-- Immutable provenance: edits and user-controlled metadata are not evidence
-- that the Assistant created an issue. Existing rows deliberately stay untrusted.
CREATE TABLE assistant_issue_creation (
    issue_id UUID PRIMARY KEY REFERENCES issue(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
