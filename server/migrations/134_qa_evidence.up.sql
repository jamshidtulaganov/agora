-- QA evidence (evidence-first QA model). When a run_qa verdict comment lands on
-- an issue, the server parses its ```qa-result``` block and persists ONE durable,
-- immutable row here. The QA cockpit + the issue's QA section then read THIS row
-- (a single indexed Postgres SELECT) instead of re-parsing comments — so opening
-- any of N in-review tasks is a cheap read, never a per-task box/lease. Evidence
-- is the default QA "environment": a frozen, attributable verdict (command table
-- with new-failure attribution vs the sprint last-green ref, captured snapshots).
--
-- Immutable-per-(issue, baseline_ref, branch_sha): two viewers see byte-identical
-- bytes; a re-run on an advanced branch sha writes a NEW row (UPSERT only refreshes
-- a same-(ref,sha) re-run). baseline_ref is the diff base (refs/sprint/<id>/last-green
-- for scope=task, merge-base otherwise); branch_sha is the tested commit; result_json
-- is the verbatim parsed qa-result payload (verdict/summary/commands/screenshots).
CREATE TABLE IF NOT EXISTS qa_evidence (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id      uuid NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    baseline_ref  text NOT NULL DEFAULT '',
    branch_sha    text NOT NULL DEFAULT '',
    verdict       text NOT NULL DEFAULT '',
    summary       text NOT NULL DEFAULT '',
    result_json   jsonb NOT NULL DEFAULT '{}',
    captured_at   timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issue_id, baseline_ref, branch_sha)
);

CREATE INDEX IF NOT EXISTS idx_qa_evidence_issue ON qa_evidence (issue_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_qa_evidence_workspace ON qa_evidence (workspace_id);
