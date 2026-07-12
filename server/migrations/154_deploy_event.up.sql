-- Deploy event (deploy P0, docs/deploy-stage-research.md §3.3). Mirrors
-- qa_evidence's structural pattern: an immutable-append, evidence-style table —
-- every sync attempt writes a NEW row (no upsert), and readers take the latest
-- row per issue. This is the durable signal the SDLC stepper's Deploy stage was
-- missing entirely (deploySynced was previously derived client-side from
-- connected_box.last_branch, a lossy proxy — see use-stage-pipeline.ts).
--
-- Tier-1 only for P0: every row today comes from a QA-box git-sync
-- (DeployIssueQA in connected_box.go). `target` names the box (label or host)
-- rather than an "environment" key, since Agora has no staging/production
-- concept yet (deploy-stage-research.md §3.1 proposes that as a later phase).
-- issue_id is nullable to leave room for a sprint-level deploy event with no
-- single issue (DeploySprintBranch) without another migration — P0 itself only
-- ever writes issue-scoped rows; the sprint path is deferred (see the comment
-- at DeploySprintBranch's call site).
CREATE TABLE IF NOT EXISTS deploy_event (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id      uuid REFERENCES issue(id) ON DELETE CASCADE,
    ref           text NOT NULL DEFAULT '',
    target        text NOT NULL DEFAULT '',
    status        text NOT NULL DEFAULT '',
    summary       text NOT NULL DEFAULT '',
    captured_at   timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_deploy_event_issue ON deploy_event (issue_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_deploy_event_workspace ON deploy_event (workspace_id);
