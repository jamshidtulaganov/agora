-- name: CreateOrchestrationRun :one
INSERT INTO orchestration_run (
    workspace_id, issue_id, mode, policy, created_by,
    execution_strategy, progression_policy, owner_type, owner_id,
    controller_agent_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetOrchestrationRun :one
SELECT * FROM orchestration_run WHERE id = $1;

-- name: GetOrchestrationRunByStep :one
SELECT run.*
FROM orchestration_run run
JOIN orchestration_step step ON step.run_id = run.id
WHERE step.id = $1;

-- name: PinOrchestrationRunBaseGitStates :one
UPDATE orchestration_run
SET base_git_states = sqlc.arg('base_git_states'), updated_at = now()
WHERE orchestration_run.id = (
    SELECT orchestration_step.run_id
    FROM orchestration_step
    WHERE orchestration_step.id = sqlc.arg('step_id')
)
  AND orchestration_run.base_git_states = '[]'::jsonb
RETURNING *;

-- name: GetActiveOrchestrationRunForIssue :one
SELECT * FROM orchestration_run
WHERE issue_id = $1 AND status IN ('draft', 'running', 'waiting_approval')
ORDER BY created_at DESC LIMIT 1;

-- name: GetLatestOrchestrationRunForIssue :one
SELECT * FROM orchestration_run
WHERE issue_id = $1
ORDER BY created_at DESC LIMIT 1;

-- name: StartOrchestrationRun :one
UPDATE orchestration_run
SET status = 'running', started_at = COALESCE(started_at, now()), updated_at = now()
WHERE id = $1 AND status = 'draft'
RETURNING *;

-- name: SetOrchestrationRunStatus :one
UPDATE orchestration_run
SET status = $2,
    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled') THEN now() ELSE completed_at END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateOrchestrationStep :one
INSERT INTO orchestration_step (
    run_id, step_key, title, stage, position, agent_id, model_override,
    depends_on_step_id, approval_required, max_attempts, instructions,
    parent_step_id, squad_id, controller_agent_id, introduced_in_version,
    step_kind, integration_status, capability
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
    CASE WHEN $16 = 'integration' THEN 'pending' ELSE 'not_required' END, $17)
RETURNING *;

-- name: StageOrchestrationStepPositionShift :exec
UPDATE orchestration_step
SET position = -position - 1, updated_at = now()
WHERE run_id = sqlc.arg('run_id') AND position >= sqlc.arg('position');

-- name: FinishOrchestrationStepPositionShift :exec
UPDATE orchestration_step
SET position = -position, updated_at = now()
WHERE run_id = sqlc.arg('run_id') AND position < 0;

-- name: ListOrchestrationSteps :many
SELECT * FROM orchestration_step WHERE run_id = $1 ORDER BY position, created_at;

-- name: GetOrchestrationStep :one
SELECT * FROM orchestration_step WHERE id = $1;

-- name: GetOrchestrationStepByTask :one
SELECT * FROM orchestration_step WHERE task_id = $1;

-- name: UpdateOrchestrationStepGitStateByTask :exec
UPDATE orchestration_step
SET worktree_branch = NULLIF(sqlc.arg('worktree_branch')::text, ''),
    base_sha = NULLIF(sqlc.arg('base_sha')::text, ''),
    head_sha = NULLIF(sqlc.arg('head_sha')::text, ''),
    merge_status = sqlc.arg('merge_status'),
    conflict_files = sqlc.arg('conflict_files'),
    integration_status = sqlc.arg('integration_status'),
    integrated_head_shas = sqlc.arg('integrated_head_shas'),
    missing_head_shas = sqlc.arg('missing_head_shas'),
    git_states = sqlc.arg('git_states'),
    updated_at = now()
WHERE task_id = sqlc.arg('task_id');

-- name: GetNextRunnableOrchestrationStep :one
SELECT s.* FROM orchestration_step s
LEFT JOIN orchestration_step dependency ON dependency.id = s.depends_on_step_id
WHERE s.run_id = $1
  AND s.status = 'pending'
  AND (s.depends_on_step_id IS NULL OR dependency.status = 'completed')
ORDER BY s.position
LIMIT 1;

-- name: AddOrchestrationStepDependency :exec
INSERT INTO orchestration_step_dependency (step_id, depends_on_step_id)
VALUES ($1, $2);

-- name: ListOrchestrationStepDependencies :many
SELECT d.step_id, d.depends_on_step_id
FROM orchestration_step_dependency d
JOIN orchestration_step s ON s.id = d.step_id
WHERE s.run_id = $1
ORDER BY s.position, d.depends_on_step_id;

-- name: ListOrchestrationStepGitDependencies :many
SELECT dependency.id, dependency.step_key, dependency.worktree_branch, dependency.head_sha, dependency.git_states
FROM orchestration_step_dependency d
JOIN orchestration_step dependency ON dependency.id = d.depends_on_step_id
WHERE d.step_id = $1 AND dependency.status <> 'skipped'
ORDER BY dependency.position, dependency.created_at;

-- name: ListOrchestrationBranchSteps :many
WITH RECURSIVE branch AS (
    SELECT orchestration_step.* FROM orchestration_step WHERE orchestration_step.id = $1
    UNION
    SELECT child.*
    FROM orchestration_step child
    JOIN branch parent ON child.parent_step_id = parent.id
       OR EXISTS (
           SELECT 1 FROM orchestration_step_dependency d
           WHERE d.step_id = child.id AND d.depends_on_step_id = parent.id
       )
)
SELECT * FROM branch ORDER BY position, created_at;

-- name: ListRunnableOrchestrationSteps :many
SELECT s.*
FROM orchestration_step s
WHERE s.run_id = $1
  AND s.status = 'pending'
  AND NOT EXISTS (
      SELECT 1
      FROM orchestration_step_dependency d
      JOIN orchestration_step dependency ON dependency.id = d.depends_on_step_id
      WHERE d.step_id = s.id AND dependency.status NOT IN ('completed', 'skipped')
  )
ORDER BY s.position;

-- name: QueueOrchestrationStep :one
UPDATE orchestration_step
SET status = 'queued', attempt = attempt + 1,
    error = NULL, started_at = COALESCE(started_at, now()), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'failed') AND attempt < max_attempts
RETURNING *;

-- name: AttachTaskToOrchestrationStep :one
UPDATE orchestration_step SET task_id = $2, updated_at = now()
WHERE id = $1 AND status = 'queued'
RETURNING *;

-- name: DeferOrchestrationStepDispatch :one
UPDATE orchestration_step
SET status = 'pending',
    attempt = GREATEST(attempt - 1, 0),
    started_at = CASE WHEN attempt <= 1 THEN NULL ELSE started_at END,
    error = NULL,
    updated_at = now()
WHERE id = $1 AND status = 'queued' AND task_id IS NULL
RETURNING *;

-- name: SetOrchestrationStepRunning :one
UPDATE orchestration_step SET status = 'running', updated_at = now()
WHERE orchestration_step.id = (SELECT orchestration_step_id FROM agent_task_queue WHERE agent_task_queue.id = $1)
  AND status = 'queued'
RETURNING *;

-- name: CompleteOrchestrationStep :one
UPDATE orchestration_step
SET status = CASE WHEN approval_required AND approved_at IS NULL THEN 'waiting_approval' ELSE 'completed' END,
    output = $2, completed_at = CASE WHEN approval_required AND approved_at IS NULL THEN NULL ELSE now() END,
    updated_at = now()
WHERE orchestration_step.id = (SELECT orchestration_step_id FROM agent_task_queue WHERE agent_task_queue.id = $1)
  AND status IN ('queued', 'running')
RETURNING *;

-- name: FailOrchestrationStep :one
UPDATE orchestration_step
SET status = 'failed', error = $2, updated_at = now()
WHERE orchestration_step.id = (SELECT orchestration_step_id FROM agent_task_queue WHERE agent_task_queue.id = $1)
  AND status IN ('queued', 'running')
RETURNING *;

-- name: FailOrchestrationStepByID :one
UPDATE orchestration_step
SET status = 'failed', error = $2, updated_at = now()
WHERE id = $1 AND status IN ('pending', 'queued', 'running')
RETURNING *;

-- name: ResetOrchestrationStepForRetry :one
UPDATE orchestration_step
SET status = 'pending', task_id = NULL, error = NULL, updated_at = now()
WHERE id = $1 AND status = 'failed' AND attempt < max_attempts
RETURNING *;

-- name: WaitOrchestrationStepApproval :one
UPDATE orchestration_step
SET status = 'waiting_approval', updated_at = now()
WHERE id = $1 AND status = 'pending' AND approval_required
RETURNING *;

-- name: ApproveOrchestrationStep :one
UPDATE orchestration_step
SET agent_id = COALESCE(agent_id, controller_agent_id),
    status = CASE WHEN COALESCE(agent_id, controller_agent_id) IS NULL THEN 'completed' ELSE 'pending' END,
    approved_by = $2,
    approved_at = now(),
    completed_at = CASE WHEN COALESCE(agent_id, controller_agent_id) IS NULL THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1 AND status = 'waiting_approval'
RETURNING *;

-- name: CreateOrchestrationEvent :one
INSERT INTO orchestration_event (run_id, step_id, kind, actor_type, actor_id, details)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListOrchestrationEvents :many
SELECT * FROM orchestration_event WHERE run_id = $1 ORDER BY created_at, id;

-- name: AdvanceOrchestrationPlanVersion :one
UPDATE orchestration_run
SET plan_version = plan_version + 1, updated_at = now()
WHERE id = $1 AND plan_version = $2 AND status IN ('draft', 'running', 'waiting_approval')
RETURNING *;

-- name: CreateOrchestrationPlanRevision :one
INSERT INTO orchestration_plan_revision (run_id, version, actor_type, actor_id, reason, patch)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListOrchestrationPlanRevisions :many
SELECT * FROM orchestration_plan_revision WHERE run_id = $1 ORDER BY version DESC;

-- name: CancelOrchestrationStepByTask :one
UPDATE orchestration_step
SET status = 'cancelled', completed_at = now(), updated_at = now(), error = 'cancelled by user'
WHERE orchestration_step.id = (SELECT orchestration_step_id FROM agent_task_queue WHERE agent_task_queue.id = $1)
  AND orchestration_step.status IN ('queued', 'running')
RETURNING *;

-- name: CancelOrchestrationStep :one
UPDATE orchestration_step
SET status = 'cancelled', completed_at = now(), updated_at = now(), error = 'cancelled by user'
WHERE id = $1 AND status IN ('pending', 'queued', 'running', 'waiting_approval', 'blocked')
RETURNING *;

-- name: RetirePendingOrchestrationStep :one
UPDATE orchestration_step
SET status = 'skipped', retired_in_version = $2, completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'waiting_approval')
RETURNING *;

-- name: ReroutePendingOrchestrationStep :one
UPDATE orchestration_step
SET agent_id = $2, model_override = $3, instructions = $4, updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;
