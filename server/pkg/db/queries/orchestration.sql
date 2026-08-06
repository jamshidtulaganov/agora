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

-- name: LockOrchestrationRun :one
SELECT * FROM orchestration_run WHERE id = $1 FOR UPDATE;

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
WHERE issue_id = $1
  AND status IN ('draft', 'running', 'waiting_approval', 'waiting_input', 'blocked')
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
    completed_at = CASE
        WHEN $2 IN ('completed', 'failed', 'cancelled') THEN COALESCE(completed_at, now())
        ELSE NULL
    END,
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

-- name: ListOrchestrationRunsWithRunnablePendingSteps :many
-- Durable repair edge for post-commit dispatch failures (human answer,
-- approval, explicit retry, or a completed predecessor). Manual runs are
-- eligible only while an explicit action's durable authorization bit is set;
-- completing or failing any work unit clears that bit before the next batch.
SELECT run.id
FROM orchestration_run run
WHERE run.status IN ('running', 'waiting_approval', 'waiting_input', 'blocked')
  AND (
      run.progression_policy <> 'manual'
      OR run.policy @> '{"manual_dispatch_authorized": true}'::jsonb
  )
  AND EXISTS (
      SELECT 1
      FROM orchestration_step candidate
      WHERE candidate.run_id = run.id
        AND candidate.status = 'pending'
        AND (
            candidate.depends_on_step_id IS NULL
            OR EXISTS (
                SELECT 1 FROM orchestration_step legacy_dependency
                WHERE legacy_dependency.id = candidate.depends_on_step_id
                  AND legacy_dependency.status IN ('completed', 'skipped')
            )
        )
        AND NOT EXISTS (
            SELECT 1
            FROM orchestration_step_dependency dependency
            JOIN orchestration_step required ON required.id = dependency.depends_on_step_id
            WHERE dependency.step_id = candidate.id
              AND required.status NOT IN ('completed', 'skipped')
        )
  )
ORDER BY run.updated_at ASC
LIMIT 100;

-- name: GetOrchestrationStep :one
SELECT * FROM orchestration_step WHERE id = $1;

-- name: SetOrchestrationManualDispatchAuthorization :one
-- Manual runs use this durable bit to distinguish an explicit Start/answer/
-- retry/approval from an idle batch that must remain paused. Keeping it inside
-- policy avoids adding a public API field while still making crash recovery
-- queryable by the sweeper.
UPDATE orchestration_run
SET policy = jsonb_set(COALESCE(policy, '{}'::jsonb), '{manual_dispatch_authorized}', to_jsonb(sqlc.arg('authorized')::boolean), true),
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND progression_policy = 'manual'
  AND status IN ('draft', 'running', 'waiting_approval', 'waiting_input', 'blocked')
RETURNING *;

-- name: PinOrchestrationArtifactLocation :one
-- Backfills the durable daemon/runtime admission decision for legacy active
-- runs. Once present it is immutable: later agent rebindings and plan edits
-- must continue to resolve to the same physical artifact location.
UPDATE orchestration_run
SET policy = jsonb_set(COALESCE(policy, '{}'::jsonb), '{artifact_location}', to_jsonb(sqlc.arg('location')::text), true),
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND (
      COALESCE(policy->>'artifact_location', '') = ''
      OR policy->>'artifact_location' = sqlc.arg('location')::text
  )
RETURNING *;

-- name: GetOrchestrationStepByTask :one
SELECT * FROM orchestration_step WHERE task_id = $1;

-- name: GetLatestTaskForOrchestrationStep :one
SELECT * FROM agent_task_queue
WHERE orchestration_step_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

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
  AND (s.depends_on_step_id IS NULL OR dependency.status IN ('completed', 'skipped'))
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
SELECT dependency.id, dependency.step_key, dependency.worktree_branch, dependency.head_sha, dependency.git_states, dependency.output
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
WHERE orchestration_step.id = $1
  AND orchestration_step.status IN ('queued', 'running')
  AND orchestration_step.task_id IS NULL
  AND $2 = (
      SELECT agent_task_queue.id
      FROM agent_task_queue
      WHERE orchestration_step_id = $1
      ORDER BY created_at DESC, id DESC
      LIMIT 1
  )
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

-- name: FinalizeOrchestrationStepAfterTerminalRun :one
-- A parallel task can finish after another branch has already made the run
-- terminal. Preserve its handoff for audit, but retire the work unit without
-- reopening the sticky run status or leaving a forever-running step behind.
UPDATE orchestration_step
SET status = sqlc.arg('status'),
    output = sqlc.arg('output'),
    error = sqlc.arg('error'),
    completed_at = CASE WHEN sqlc.arg('status') = 'completed' THEN now() ELSE NULL END,
    updated_at = now()
WHERE orchestration_step.id = (
    SELECT orchestration_step_id
    FROM agent_task_queue
    WHERE agent_task_queue.id = sqlc.arg('task_id')
)
  AND status IN ('queued', 'running')
  AND sqlc.arg('status') IN ('completed', 'blocked')
RETURNING *;

-- name: WaitOrchestrationStepInput :one
UPDATE orchestration_step
SET status = 'waiting_input', output = $2, completed_at = NULL, updated_at = now()
WHERE orchestration_step.id = (SELECT orchestration_step_id FROM agent_task_queue WHERE agent_task_queue.id = $1)
  AND status IN ('queued', 'running')
RETURNING *;

-- name: BlockOrchestrationStep :one
UPDATE orchestration_step
SET status = 'blocked', output = $2, completed_at = NULL, updated_at = now()
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
SET status = 'pending', task_id = NULL, error = NULL,
    attempt = CASE WHEN status = 'blocked' THEN GREATEST(attempt - 1, 0) ELSE attempt END,
    updated_at = now()
WHERE id = $1 AND status IN ('failed', 'blocked')
  AND (status = 'blocked' OR attempt < max_attempts)
RETURNING *;

-- name: ResumeOrchestrationStepAfterInput :one
UPDATE orchestration_step
SET status = 'pending', task_id = NULL, error = NULL,
    attempt = GREATEST(attempt - 1, 0), updated_at = now()
WHERE id = $1 AND status = 'waiting_input'
RETURNING *;

-- name: WaitOrchestrationStepApproval :one
UPDATE orchestration_step
SET status = 'waiting_approval', approved_by = NULL, approved_at = NULL, updated_at = now()
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

-- name: CreateOrchestrationMessage :one
INSERT INTO orchestration_message (
    run_id, step_id, kind, actor_type, actor_id, target_type, target_id, body,
    plan_version, correlation_id, causation_id, reply_to_id, idempotency_key,
    expects_reply
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (run_id, idempotency_key) DO UPDATE
SET idempotency_key = EXCLUDED.idempotency_key
RETURNING *;

-- name: ListOrchestrationMessages :many
SELECT * FROM orchestration_message
WHERE run_id = $1
ORDER BY created_at, id;

-- name: ListOrchestrationStepMessages :many
SELECT * FROM (
    SELECT * FROM orchestration_message
    WHERE step_id = $1
    ORDER BY created_at DESC, id DESC
    LIMIT 12
) recent
ORDER BY created_at, id;

-- name: CountOrchestrationStepQuestions :one
SELECT count(*) FROM orchestration_message
WHERE step_id = $1 AND kind = 'question';

-- name: GetLatestOpenOrchestrationQuestion :one
SELECT * FROM orchestration_message
WHERE step_id = $1 AND kind = 'question' AND expects_reply AND resolved_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: GetLatestOrchestrationQuestion :one
-- The response path runs this inside a transaction. Locking the question row
-- serializes concurrent answers; after one answer resolves it, a waiter sees
-- the new resolved_at value and can only replay the exact same answer.
SELECT * FROM orchestration_message
WHERE step_id = $1 AND kind = 'question' AND expects_reply
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: GetOrchestrationQuestionForUpdate :one
-- The caller supplies the question identity it rendered. Lock that exact row
-- so a delayed response can never drift to a newer question on the same step.
SELECT * FROM orchestration_message
WHERE id = $1 AND step_id = $2 AND kind = 'question' AND expects_reply
FOR UPDATE;

-- name: GetOrchestrationMessageByIdempotencyKey :one
SELECT * FROM orchestration_message
WHERE run_id = $1 AND idempotency_key = $2;

-- name: ResolveOrchestrationMessage :one
UPDATE orchestration_message
SET acknowledged_at = COALESCE(acknowledged_at, now()), resolved_at = now()
WHERE id = $1 AND resolved_at IS NULL
RETURNING *;

-- name: AdvanceOrchestrationPlanVersion :one
UPDATE orchestration_run
SET plan_version = plan_version + 1, updated_at = now()
WHERE id = $1 AND plan_version = $2
  AND status IN ('draft', 'running', 'waiting_approval', 'waiting_input', 'blocked')
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
WHERE id = $1
  AND status IN ('pending', 'queued', 'running', 'waiting_approval', 'waiting_input', 'blocked')
RETURNING *;

-- name: RetirePendingOrchestrationStep :one
UPDATE orchestration_step
SET status = 'skipped', retired_in_version = $2, completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('pending', 'waiting_approval')
RETURNING *;

-- name: ReroutePendingOrchestrationStep :one
-- "Pending" here means "execution is still ahead of it", which covers
-- waiting_approval as well as pending — the same sense
-- RetirePendingOrchestrationStep above uses.
--
-- A step reaches waiting_approval from either side of a run
-- (WaitOrchestrationStepApproval parks a pending step before it dispatches;
-- CompleteOrchestrationStep parks an approval_required step that reported
-- completion), but both release the same way: ApproveOrchestrationStep sets
-- status back to 'pending' with agent_id = COALESCE(agent_id,
-- controller_agent_id). So a waiting_approval step's agent_id governs the run
-- that FOLLOWS approval, and rerouting before approval is what decides who
-- executes it.
--
-- Unlike retire / add_child, reroute is NOT restricted to draft runs (see
-- EditIssueOrchestration) — it serves live runs, and a live run is precisely
-- what pauses at waiting_approval. Narrowing this to status = 'pending' would
-- make reroute silently no-op at the one moment a human is looking at the gate.
-- Pinned by TestReroutePendingOrchestrationStepCoversWaitingApproval.
UPDATE orchestration_step
SET agent_id = $2, model_override = $3, instructions = $4, updated_at = now()
WHERE id = $1 AND status IN ('pending', 'waiting_approval')
RETURNING *;
