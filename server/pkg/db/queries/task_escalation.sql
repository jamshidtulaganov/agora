-- Escalations — the agent's out-of-band "I am stuck" hatch
-- (docs/orchestration-upgrade-plan.md §B1). Every query is workspace-scoped;
-- the issue_id filters are secondary, not the tenancy boundary.

-- name: CreateTaskEscalation :one
-- Raise an escalation. The partial unique index
-- idx_task_escalation_open_issue makes "one open row per issue" a database
-- guarantee, so a second raise on an issue that already has an open
-- escalation UPDATES it (the agent refined what it needs) instead of
-- stacking a second question onto the same human.
INSERT INTO task_escalation (
    workspace_id, issue_id, task_id, agent_id,
    kind, prompt, detail, options, risk_tier
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
ON CONFLICT (issue_id) WHERE status = 'open'
DO UPDATE SET
    task_id   = EXCLUDED.task_id,
    agent_id  = EXCLUDED.agent_id,
    kind      = EXCLUDED.kind,
    prompt    = EXCLUDED.prompt,
    detail    = EXCLUDED.detail,
    options   = EXCLUDED.options,
    risk_tier = EXCLUDED.risk_tier,
    raised_at = now()
RETURNING *;

-- name: GetOpenTaskEscalationForIssue :one
SELECT * FROM task_escalation
WHERE issue_id = $1 AND workspace_id = $2 AND status = 'open';

-- name: GetTaskEscalation :one
SELECT * FROM task_escalation
WHERE id = $1 AND workspace_id = $2;

-- name: ListTaskEscalationsForIssue :many
-- Open first, then the answered history, newest first. The card shows the
-- open one; the history explains what the humans already decided.
SELECT * FROM task_escalation
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY (status = 'open') DESC, raised_at DESC
LIMIT $3;

-- name: ListOpenTaskEscalations :many
-- The workspace decision queue. Oldest first: until the ranked queue lands
-- (plan §A2), AGE is the ranking — the question nobody has looked at longest
-- is the one costing the most.
SELECT * FROM task_escalation
WHERE workspace_id = $1 AND status = 'open'
ORDER BY raised_at ASC
LIMIT $2;

-- name: AnswerTaskEscalation :one
-- Record the human decision. Guarded on status = 'open' so two humans
-- answering at once produce one winner and one no-rows (the caller reports
-- "already answered" rather than double-resuming the task).
UPDATE task_escalation
SET status = 'answered',
    answer = $3,
    answered_by = $4,
    answered_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'open'
RETURNING *;

-- name: SetTaskEscalationResumedTask :one
UPDATE task_escalation
SET resumed_task_id = $3
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CancelTaskEscalation :one
-- Withdraw an open escalation without answering it (issue cancelled, the
-- task was terminated, or a human decided the question is moot).
UPDATE task_escalation
SET status = 'cancelled',
    answered_by = $3,
    answered_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'open'
RETURNING *;

-- name: CountOpenTaskEscalations :one
SELECT COUNT(*) FROM task_escalation
WHERE workspace_id = $1 AND status = 'open';
