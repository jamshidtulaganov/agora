-- QA evidence — the durable, evidence-first QA verdict per (issue, baseline, sha).
-- Persisted server-side from a run_qa comment's ```qa-result``` block so the QA
-- cockpit + issue QA section read one indexed row instead of re-parsing comments.

-- name: UpsertQAEvidence :one
-- Insert the parsed verdict, or refresh a same-(issue,baseline_ref,branch_sha)
-- re-run in place (a re-run on an ADVANCED sha writes a new row instead). Keeps
-- evidence immutable per tested commit while letting a repeated smoke update.
--
-- commit_sha / triggered_by / started_at / finished_at (migration 157) are
-- run-identity METADATA on the single current row — deliberately NOT part of
-- the (issue_id, baseline_ref, branch_sha) conflict key, which keeps its
-- one-current-row overwrite semantics untouched (see the migration comment).
-- finished_at is stamped at capture time; started_at only when reported.
INSERT INTO qa_evidence (
    workspace_id,
    issue_id,
    baseline_ref,
    branch_sha,
    verdict,
    summary,
    result_json,
    source,
    commit_sha,
    triggered_by,
    started_at,
    finished_at,
    captured_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, sqlc.narg(started_at), now(), now())
ON CONFLICT (issue_id, baseline_ref, branch_sha) DO UPDATE
SET verdict      = EXCLUDED.verdict,
    summary      = EXCLUDED.summary,
    result_json  = EXCLUDED.result_json,
    source       = EXCLUDED.source,
    commit_sha   = EXCLUDED.commit_sha,
    triggered_by = EXCLUDED.triggered_by,
    started_at   = EXCLUDED.started_at,
    finished_at  = now(),
    captured_at  = now()
RETURNING *;

-- name: GetLatestQAEvidenceForIssue :one
-- The freshest evidence row for an issue (what the issue's QA section renders).
SELECT * FROM qa_evidence
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY captured_at DESC
LIMIT 1;

-- name: ListQAEvidenceSummariesForIssues :many
-- Latest verdict per issue across a set (the QA cockpit board: one chip per row).
-- DISTINCT ON keeps the freshest row per issue.
SELECT DISTINCT ON (issue_id)
    issue_id,
    verdict,
    source,
    summary,
    baseline_ref,
    branch_sha,
    commit_sha,
    triggered_by,
    captured_at
FROM qa_evidence
WHERE workspace_id = $1
  AND issue_id = ANY($2::uuid[])
ORDER BY issue_id, captured_at DESC;

-- name: PruneOldQAEvidenceForIssue :exec
-- Retain only the most recent $2 evidence rows per issue (the sole teardown).
DELETE FROM qa_evidence AS e
WHERE e.issue_id = $1
  AND e.id NOT IN (
    SELECT keep.id FROM qa_evidence keep
    WHERE keep.issue_id = $1
    ORDER BY keep.captured_at DESC
    LIMIT $2
  );
