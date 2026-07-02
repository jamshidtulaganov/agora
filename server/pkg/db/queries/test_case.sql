-- QA test cases + runs — the QA team's test-management instruments.

-- name: CreateTestCase :one
INSERT INTO test_case (
    workspace_id, issue_id, project_id, title, steps, expected, kind, source, author_type, author_id, category, script
)
VALUES ($1, sqlc.narg(issue_id), sqlc.narg(project_id), $2, $3, $4, $5, $6, $7, sqlc.narg(author_id), $8, $9)
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

-- name: ListAutomatedTestCasesForProject :many
-- The project's STANDING base scripts: automated golden-path regression cases
-- pinned to the project (no issue) — injected into EVERY run_qa / run_test_cases.
SELECT * FROM test_case
WHERE project_id = $1 AND workspace_id = $2
  AND issue_id IS NULL AND kind = 'automated' AND archived_at IS NULL
ORDER BY created_at ASC;

-- name: ListTestCasesForProject :many
-- The project's standing base cases (issue_id NULL), newest first — the
-- project-level base-suite list. All kinds; only automated ones are injected
-- into run_qa / run_test_cases.
SELECT * FROM test_case
WHERE project_id = $1 AND workspace_id = $2
  AND issue_id IS NULL AND archived_at IS NULL
ORDER BY created_at DESC;

-- name: PromoteIssueTestCasesToProject :execrows
-- Self-growing base suite: when an issue completes, COPY its automated cases
-- into the project's standing base scripts (issue_id NULL) with a "[KEY] "
-- title prefix, so every future QA run regression-tests the finished work.
-- Dedupe by prefixed title against live base rows — re-fires are no-ops.
INSERT INTO test_case
  (workspace_id, issue_id, project_id, title, steps, expected, kind, source, author_type, author_id, category, script)
SELECT tc.workspace_id, NULL, $2, '[' || sqlc.arg(issue_key)::text || '] ' || tc.title,
       tc.steps, tc.expected, 'automated', 'promoted', tc.author_type, tc.author_id, tc.category, tc.script
FROM test_case tc
WHERE tc.issue_id = $1 AND tc.kind = 'automated' AND tc.archived_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM test_case e
    WHERE e.project_id = $2 AND e.issue_id IS NULL AND e.archived_at IS NULL
      AND lower(e.title) = lower('[' || sqlc.arg(issue_key)::text || '] ' || tc.title)
  );

-- name: ListLatestRunsForProjectBaseCases :many
-- The latest run per project base case across ALL issues' runs (status chips
-- for the project-level base-suite list).
SELECT DISTINCT ON (r.test_case_id)
    r.test_case_id,
    r.status,
    r.run_source,
    r.created_at,
    r.output
FROM test_run r
JOIN test_case c ON c.id = r.test_case_id
WHERE c.project_id = $1 AND c.workspace_id = $2 AND c.issue_id IS NULL
ORDER BY r.test_case_id, r.created_at DESC;

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
-- Covers the issue's OWN cases plus project base scripts (issue_id NULL) whose
-- runs were recorded against this issue — a base-script FAIL is a regression
-- verdict and must render on the issue, not vanish.
SELECT DISTINCT ON (r.test_case_id)
    r.test_case_id,
    r.status,
    r.run_source,
    r.created_at,
    r.output
FROM test_run r
JOIN test_case c ON c.id = r.test_case_id
WHERE c.workspace_id = $2
  AND (c.issue_id = $1 OR (c.issue_id IS NULL AND r.issue_id = $1))
ORDER BY r.test_case_id, r.created_at DESC;

-- name: ListTestRunsForCase :many
SELECT * FROM test_run
WHERE test_case_id = $1 AND workspace_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: SetTestCaseScript :exec
-- Persist a compiled Playwright script onto an existing case (the background
-- compile_tests flow / on-demand recompile). Workspace-scoped write.
UPDATE test_case
SET script = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2;
