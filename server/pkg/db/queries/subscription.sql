-- name: GetWorkspaceSubscription :one
SELECT * FROM workspace_subscription WHERE workspace_id = $1;

-- name: StartWorkspaceTrial :one
-- Called once, at workspace creation. DO NOTHING keeps a second call from
-- restarting a trial that already ran.
INSERT INTO workspace_subscription (workspace_id, plan, status, trial_ends_at)
VALUES ($1, 'team', 'trialing', $2)
ON CONFLICT (workspace_id) DO NOTHING
RETURNING *;

-- name: SetWorkspaceSubscription :one
-- Operator path only (admintool). Never wire this to a tenant-facing endpoint.
INSERT INTO workspace_subscription (
    workspace_id, plan, status, billing_interval, seats_paid,
    trial_ends_at, current_period_end, provider, note
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (workspace_id) DO UPDATE SET
    plan = EXCLUDED.plan,
    status = EXCLUDED.status,
    billing_interval = EXCLUDED.billing_interval,
    seats_paid = EXCLUDED.seats_paid,
    trial_ends_at = EXCLUDED.trial_ends_at,
    current_period_end = EXCLUDED.current_period_end,
    provider = EXCLUDED.provider,
    note = EXCLUDED.note,
    updated_at = now()
RETURNING *;

-- name: GetWorkspaceUsageCounts :one
-- What the billing page shows next to each limit. Seats are human members only:
-- agents live in their own table and are never a seat.
SELECT
    (SELECT count(*) FROM member m WHERE m.workspace_id = $1)::int AS seats,
    (SELECT count(*) FROM automation a WHERE a.workspace_id = $1 AND a.enabled)::int AS automations,
    (SELECT count(*) FROM assistant_report_schedule s
        JOIN assistant_artifact_pin p ON p.id = s.pin_id
        WHERE p.workspace_id = $1 AND s.enabled)::int AS report_schedules,
    (SELECT count(*) FROM autopilot ap WHERE ap.workspace_id = $1 AND ap.status = 'active')::int AS autopilots;

-- name: ListAllowedAutomationIDs :many
-- The oldest N enabled rules: the ones a plan limit lets run.
SELECT id FROM automation
WHERE workspace_id = $1 AND enabled
ORDER BY created_at ASC, id ASC
LIMIT $2;

-- name: ListAllowedReportScheduleIDs :many
SELECT s.id FROM assistant_report_schedule s
JOIN assistant_artifact_pin p ON p.id = s.pin_id
WHERE p.workspace_id = $1 AND s.enabled
ORDER BY s.created_at ASC, s.id ASC
LIMIT $2;

-- name: ListAllowedAutopilotIDs :many
SELECT id FROM autopilot
WHERE workspace_id = $1 AND status = 'active'
ORDER BY created_at ASC, id ASC
LIMIT $2;

-- name: GetReportScheduleWorkspaceID :one
SELECT p.workspace_id FROM assistant_report_schedule s
JOIN assistant_artifact_pin p ON p.id = s.pin_id
WHERE s.id = $1;
