-- 125_agent_run_trace
--
-- Capture one durable trace row per terminal agent run — the foundation of the
-- fine-tuning data flywheel. The existing tables already hold the run's OUTPUT
-- (task_message turns, agent_task_queue.result) and token usage (task_usage),
-- and the INPUT is reconstructable from the issue + agent. What no table records
-- is the OUTCOME: did a human accept the agent's work, revise it, or reopen the
-- issue? That accept/correct signal is the preference data a future fine-tune
-- needs, so this table anchors each run and accrues outcome signals after it ends.
--
-- Intentionally lean: it stores anchors + outcome, NOT a copy of prompt/completion
-- (those join back in from task_message / issue / agent / task_usage by task_id).

CREATE TABLE agent_run_trace (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL UNIQUE REFERENCES agent_task_queue(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    issue_id UUID,

    -- Run anchor (snapshot at the run's terminal transition)
    task_status TEXT NOT NULL,             -- completed | failed | cancelled
    issue_status_at_run TEXT,              -- issue status when the run closed (outcome baseline)

    -- Outcome signals (accrued post-run by event hooks; the preference data)
    final_issue_status TEXT,
    human_revised BOOLEAN NOT NULL DEFAULT false,  -- a human edited/replaced the agent's output after the run
    reopened BOOLEAN NOT NULL DEFAULT false,       -- issue reopened after the agent closed it
    reaction_score INTEGER NOT NULL DEFAULT 0,     -- net reactions on the agent's comments
    outcome_label TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | corrected | rejected

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_run_trace_workspace_created ON agent_run_trace (workspace_id, created_at DESC);
CREATE INDEX idx_agent_run_trace_outcome ON agent_run_trace (outcome_label);
