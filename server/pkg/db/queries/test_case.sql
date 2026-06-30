-- QA test cases + runs — the QA team's test-management instruments.

-- name: CreateTestCase :one
INSERT INTO test_case (
    workspace_id, issue_id, project_id, title, steps, expected, kind, source, author_type, author_id
)
VALUES ($1, sqlc.narg(issue_id), sqlc.narg(project_id), $2, $3, $4, $5, $6, $7, sqlc.narg(author_id))
RETURNING *;

-- name: ListTestCasesForIssue :many
-- Active (non-archived) cases for an issue, newest first.
SELECT * FROM test_case
WHERE issue_id = $1 AND workspace_id = $2 AND archived_at IS NULL
ORDER BY created_at DESC;

-- name: GetTestCase :one
SELECT * FROM test_case
WHERE id = $1 AND workspace_id = $2;

-- name: CountActiveTestCasesForIssue :one
SELECT count(*) FROM test_case
WHERE issue_id = $1 AND workspace_id = $2 AND archived_at IS NULL;

-- name: ListAutomatedTestCasesForIssue :many
-- Automated cases an agent can drive deterministically (run_test_cases).
SELECT * FROM test_case
WHERE issue_id = $1 AND workspace_id = $2 AND archived_at IS NULL AND kind = 'automated'
ORDER BY created_at ASC;

-- name: ArchiveTestCase :exec
UPDATE test_case
SET archived_at = now(), updated_at = now()
WHERE id = $1 AND workspace_id = $2;

-- name: CreateTestRun :one
INSERT INTO test_run (
    workspace_id, test_case_id, issue_id, status, output, run_source, run_by_type, run_by_id
)
VALUES ($1, $2, sqlc.narg(issue_id), $3, $4, $5, $6, sqlc.narg(run_by_id))
RETURNING *;

-- name: ListLatestRunsForIssueCases :many
-- The latest run per test case for an issue (drives each case's status chip).
SELECT DISTINCT ON (r.test_case_id)
    r.test_case_id,
    r.status,
    r.run_source,
    r.created_at,
    r.output
FROM test_run r
JOIN test_case c ON c.id = r.test_case_id
WHERE c.issue_id = $1 AND c.workspace_id = $2
ORDER BY r.test_case_id, r.created_at DESC;

-- name: ListTestRunsForCase :many
SELECT * FROM test_run
WHERE test_case_id = $1 AND workspace_id = $2
ORDER BY created_at DESC
LIMIT $3;
