-- QA evidence — the durable, evidence-first QA verdict per (issue, baseline, sha).
-- Persisted server-side from a run_qa comment's ```qa-result``` block so the QA
-- cockpit + issue QA section read one indexed row instead of re-parsing comments.

-- name: UpsertQAEvidence :one
-- Insert the parsed verdict, or refresh a same-(issue,baseline_ref,branch_sha)
-- re-run in place (a re-run on an ADVANCED sha writes a new row instead). Keeps
-- evidence immutable per tested commit while letting a repeated smoke update.
INSERT INTO qa_evidence (
    workspace_id,
    issue_id,
    baseline_ref,
    branch_sha,
    verdict,
    summary,
    result_json,
    captured_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (issue_id, baseline_ref, branch_sha) DO UPDATE
SET verdict     = EXCLUDED.verdict,
    summary     = EXCLUDED.summary,
    result_json = EXCLUDED.result_json,
    captured_at = now()
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
    summary,
    baseline_ref,
    branch_sha,
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
