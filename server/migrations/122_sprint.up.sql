CREATE TABLE sprint (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace (id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project (id) ON DELETE CASCADE,
    name text NOT NULL,
    goal text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','active','completed')),
    start_date timestamptz,
    end_date timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sprint_project ON sprint (project_id);
CREATE INDEX idx_sprint_workspace ON sprint (workspace_id);

CREATE TABLE issue_to_sprint (
    issue_id uuid PRIMARY KEY REFERENCES issue (id) ON DELETE CASCADE,
    sprint_id uuid NOT NULL REFERENCES sprint (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_issue_to_sprint_sprint ON issue_to_sprint (sprint_id);
