-- Separate who executes from how the run advances. The legacy `mode` column
-- and policy.execution_mode remain readable during the compatibility window,
-- but every new API response is backed by these stable run snapshots.
ALTER TABLE orchestration_run
    ADD COLUMN execution_strategy TEXT NOT NULL DEFAULT 'custom'
        CHECK (execution_strategy IN ('human', 'solo', 'squad', 'custom')),
    ADD COLUMN progression_policy TEXT NOT NULL DEFAULT 'automatic'
        CHECK (progression_policy IN ('automatic', 'gated', 'manual')),
    ADD COLUMN owner_type TEXT NOT NULL DEFAULT 'unassigned'
        CHECK (owner_type IN ('agent', 'squad', 'member', 'unassigned')),
    ADD COLUMN owner_id UUID,
    -- Snapshot IDs deliberately have no FK. Reassignment or later actor
    -- deletion must not rewrite the historical owner/controller of a run.
    ADD COLUMN controller_agent_id UUID;

UPDATE orchestration_run
SET owner_type = CASE
        WHEN policy->>'owner_type' IN ('agent', 'squad', 'member', 'unassigned')
            THEN policy->>'owner_type'
        ELSE 'unassigned'
    END,
    owner_id = CASE
        WHEN COALESCE(policy->>'owner_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            THEN (policy->>'owner_id')::uuid
        ELSE NULL
    END,
    controller_agent_id = CASE
        WHEN COALESCE(policy->>'controller_agent_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            THEN (policy->>'controller_agent_id')::uuid
        ELSE NULL
    END,
    execution_strategy = CASE
        WHEN policy->>'execution_strategy' IN ('human', 'solo', 'squad', 'custom')
            THEN policy->>'execution_strategy'
        WHEN policy->>'execution_mode' = 'direct' THEN 'solo'
        WHEN policy->>'execution_mode' IN ('squad', 'orchestrated') THEN 'squad'
        WHEN policy->>'owner_type' IN ('member', 'unassigned') THEN 'human'
        ELSE 'custom'
    END,
    progression_policy = CASE
        WHEN policy->>'progression_policy' IN ('automatic', 'gated', 'manual')
            THEN policy->>'progression_policy'
        WHEN mode = 'manual' THEN 'manual'
        ELSE 'automatic'
    END;

COMMENT ON COLUMN orchestration_run.execution_strategy IS
    'Who performs the issue: human, one agent, a lead-managed squad, or an explicitly routed custom DAG.';
COMMENT ON COLUMN orchestration_run.progression_policy IS
    'How ready steps advance: automatic, explicit configured gates, or manual controller progression.';
COMMENT ON COLUMN orchestration_run.mode IS
    'Deprecated compatibility alias for progression_policy (auto/manual).';
