-- QA test management (the QA team's instruments). A test_case is a reusable
-- test spec for a ticket/feature; a QA human authors one by hand OR a QA-Squad
-- agent generates it from the ticket (description + acceptance criteria + diff).
-- A test_run is one execution of a case — pass/fail/blocked + output — recorded
-- by a human (manual) or an agent (automated, run on the box). This is the
-- substrate for agent-authored cases, automated runs, sprint regression, and
-- test reports. source/kind/status are plain text (enum drift downgrades, never
-- crashes) per the API-compat rules.
CREATE TABLE IF NOT EXISTS test_case (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id     uuid REFERENCES issue(id) ON DELETE CASCADE,
    project_id   uuid REFERENCES project(id) ON DELETE SET NULL,
    title        text NOT NULL,
    steps        text NOT NULL DEFAULT '',
    expected     text NOT NULL DEFAULT '',
    kind         text NOT NULL DEFAULT 'manual',   -- manual | automated
    source       text NOT NULL DEFAULT 'human',    -- human | agent
    author_type  text NOT NULL DEFAULT '',         -- member | agent
    author_id    uuid,
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_test_case_issue ON test_case (issue_id) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_test_case_workspace ON test_case (workspace_id);
CREATE INDEX IF NOT EXISTS idx_test_case_project ON test_case (project_id);

CREATE TABLE IF NOT EXISTS test_run (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    test_case_id uuid NOT NULL REFERENCES test_case(id) ON DELETE CASCADE,
    issue_id     uuid REFERENCES issue(id) ON DELETE SET NULL,
    status       text NOT NULL DEFAULT 'pass',     -- pass | fail | skip | blocked
    output       text NOT NULL DEFAULT '',
    run_source   text NOT NULL DEFAULT 'human',    -- human | agent
    run_by_type  text NOT NULL DEFAULT '',
    run_by_id    uuid,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_test_run_case ON test_run (test_case_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_test_run_issue ON test_run (issue_id, created_at DESC);
