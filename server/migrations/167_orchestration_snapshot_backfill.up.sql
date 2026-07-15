-- Migration 166 can only read snapshots persisted by newer runs. Older custom
-- orchestration runs sometimes stored owner_type=unassigned while their steps
-- carried the real squad/controller routing. Repair those rows from immutable
-- step routing before ever considering the issue's mutable current assignee.
WITH legacy_routing AS (
    SELECT
        r.id,
        (
            SELECT s.squad_id
            FROM orchestration_step s
            WHERE s.run_id = r.id AND s.squad_id IS NOT NULL
            ORDER BY s.position, s.created_at
            LIMIT 1
        ) AS step_squad_id,
        (
            SELECT s.controller_agent_id
            FROM orchestration_step s
            WHERE s.run_id = r.id AND s.controller_agent_id IS NOT NULL
            ORDER BY s.position, s.created_at
            LIMIT 1
        ) AS step_controller_agent_id,
        (
            SELECT s.agent_id
            FROM orchestration_step s
            WHERE s.run_id = r.id AND s.agent_id IS NOT NULL
            ORDER BY s.position, s.created_at
            LIMIT 1
        ) AS first_step_agent_id
    FROM orchestration_run r
)
UPDATE orchestration_run r
SET execution_strategy = CASE
        WHEN r.policy->>'execution_strategy' IN ('human', 'solo', 'squad', 'custom')
            THEN r.policy->>'execution_strategy'
        WHEN legacy.step_squad_id IS NOT NULL THEN 'squad'
        WHEN r.policy->>'execution_mode' IN ('squad', 'orchestrated') THEN 'squad'
        WHEN r.policy->>'execution_mode' = 'direct' THEN 'solo'
        WHEN r.policy->>'owner_type' IN ('member', 'unassigned') THEN 'human'
        ELSE r.execution_strategy
    END,
    owner_type = CASE
        WHEN legacy.step_squad_id IS NOT NULL THEN 'squad'
        WHEN r.owner_type <> 'unassigned' THEN r.owner_type
        WHEN r.policy->>'owner_type' IN ('agent', 'squad', 'member')
            THEN r.policy->>'owner_type'
        WHEN r.policy->>'execution_mode' = 'direct' AND legacy.first_step_agent_id IS NOT NULL
            THEN 'agent'
        ELSE r.owner_type
    END,
    owner_id = COALESCE(r.owner_id, legacy.step_squad_id,
        CASE
            WHEN COALESCE(r.policy->>'owner_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
                THEN (r.policy->>'owner_id')::uuid
            WHEN r.policy->>'execution_mode' = 'direct' THEN legacy.first_step_agent_id
            ELSE NULL
        END),
    controller_agent_id = COALESCE(r.controller_agent_id, legacy.step_controller_agent_id,
        CASE
            WHEN COALESCE(r.policy->>'controller_agent_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
                THEN (r.policy->>'controller_agent_id')::uuid
            ELSE NULL
        END),
    updated_at = now()
FROM legacy_routing legacy
WHERE legacy.id = r.id
  AND (
      legacy.step_squad_id IS NOT NULL
      OR legacy.step_controller_agent_id IS NOT NULL
      OR r.owner_type = 'unassigned'
  );

COMMENT ON COLUMN orchestration_run.owner_type IS
    'Accountable owner snapshot at run creation; legacy rows are best-effort backfilled from immutable step routing.';
