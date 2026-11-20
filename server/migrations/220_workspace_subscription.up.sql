-- The workspace's plan: what it bought, or the trial it is on.
--
-- Written by exactly two paths: workspace creation (starts the 14-day Team
-- trial) and the operator's admintool (marks a workspace paid). No
-- tenant-facing endpoint writes this table — workspace.settings and
-- instance_config are both tenant-writable, so a plan stored there could be
-- self-granted (docs/monetization-plan.md §1.7).
--
-- The row stores facts (dates, plan, status). What the workspace may do right
-- now — trial expired, payment overdue, grace — is DERIVED at read time in
-- server/internal/billing, so nothing has to flip a row when a date passes.
CREATE TABLE workspace_subscription (
    workspace_id       uuid PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    plan               text NOT NULL DEFAULT 'free',
    status             text NOT NULL DEFAULT 'active',
    billing_interval   text NOT NULL DEFAULT '',
    seats_paid         int  NOT NULL DEFAULT 0,
    trial_ends_at      timestamptz,
    current_period_end timestamptz,
    provider           text NOT NULL DEFAULT 'manual',
    note               text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT workspace_subscription_plan_check CHECK (plan IN ('free', 'team', 'enterprise')),
    CONSTRAINT workspace_subscription_status_check CHECK (status IN ('trialing', 'active', 'canceled')),
    CONSTRAINT workspace_subscription_interval_check CHECK (billing_interval IN ('', 'month', 'year')),
    CONSTRAINT workspace_subscription_trial_check CHECK (status <> 'trialing' OR trial_ends_at IS NOT NULL)
);

-- Every workspace that exists before billing ships keeps everything it has:
-- Team, active, no end date. Only workspaces created from now on start a trial.
INSERT INTO workspace_subscription (workspace_id, plan, status, provider, note)
SELECT id, 'team', 'active', 'grandfathered', 'existed before billing'
FROM workspace
ON CONFLICT (workspace_id) DO NOTHING;
