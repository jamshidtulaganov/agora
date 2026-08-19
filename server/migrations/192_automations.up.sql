-- Automations: user-defined task-management rules — WHEN <trigger> IF <conditions>
-- THEN <actions>. The engine is scoped to TASK MANAGEMENT on purpose (statuses,
-- assignees, labels, comments, agent slice actions, Telegram notices); it is not a
-- general integration bus.
--
-- Why a table and not more env flags: the pipeline behaviours shipped so far
-- (auto review, review-fail routing, auto QA) are hardcoded hooks behind
-- project-scoped flags, so every new rule needs a release. A row lets a team
-- express "when this Bitrix column is entered, run a review and tell the group"
-- without one. The flags stay as the low-level switches those actions read, so
-- nothing regresses while both exist.
CREATE TABLE automation (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL REFERENCES workspace (id) ON DELETE CASCADE,
    -- NULL = every project in the workspace. A project-scoped automation only
    -- sees issues filed under that project.
    project_id     uuid REFERENCES project (id) ON DELETE CASCADE,
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    enabled        boolean NOT NULL DEFAULT true,
    -- One trigger per automation (issue.status_changed, tracker.stage_changed, …).
    -- Kept as text, not an enum: the engine validates against its own registry, and
    -- an unknown trigger must be inert rather than block a migration.
    trigger_type   text NOT NULL,
    trigger_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- [{field, op, value}] — ALL must hold (AND). An empty array always matches.
    conditions     jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- [{type, config}] — executed in order, each independently guarded.
    actions        jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- Which built-in recipe seeded this row ("" for hand-built). Lets the recipe
    -- gallery show what is already installed instead of offering a duplicate.
    recipe_key     text NOT NULL DEFAULT '',
    run_count      integer NOT NULL DEFAULT 0,
    last_run_at    timestamptz,
    created_by_type text NOT NULL DEFAULT 'member',
    created_by_id  uuid,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- The engine's hot path: every emitted event asks for the workspace's enabled
-- automations on one trigger.
CREATE INDEX idx_automation_ws_trigger ON automation (workspace_id, trigger_type) WHERE enabled;
CREATE INDEX idx_automation_project ON automation (project_id) WHERE project_id IS NOT NULL;

-- One row per evaluation that reached an automation, applied or not. This is the
-- audit trail the UI shows AND the loop guard: the engine reads the recent rows for
-- (automation, issue) to enforce a cooldown and an hourly cap, so a rule whose own
-- actions re-trigger it cannot run away.
CREATE TABLE automation_run (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    automation_id   uuid NOT NULL REFERENCES automation (id) ON DELETE CASCADE,
    workspace_id    uuid NOT NULL REFERENCES workspace (id) ON DELETE CASCADE,
    issue_id        uuid REFERENCES issue (id) ON DELETE SET NULL,
    trigger_type    text NOT NULL,
    -- 'applied' (actions ran), 'skipped' (conditions/guards said no), 'failed'
    status          text NOT NULL,
    actions_applied integer NOT NULL DEFAULT 0,
    -- {reason, actions:[{type, ok, detail}]} — what happened, per action.
    detail          jsonb NOT NULL DEFAULT '{}'::jsonb,
    error           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_automation_run_automation ON automation_run (automation_id, created_at DESC);
CREATE INDEX idx_automation_run_issue ON automation_run (issue_id, created_at DESC);
CREATE INDEX idx_automation_run_workspace ON automation_run (workspace_id, created_at DESC);
