-- Prod squad rollout (2026-07-07): per-project QA regression automation.
-- Idempotent. Scope = QA-able projects only (repos>0). No overdue active
-- sprints exist, so binding autopilots cannot retro-fire the sprint-end
-- scheduler. Reviewed against the same rollout applied locally.
--
-- Run: cat scripts/prod-squad-rollout-2026-07-07.sql | fly ssh console -a sd-agora-db -C 'psql -U multica -d multica'

-- 1) sprint_mode=true for QA-able projects (idempotent merge)
UPDATE project p SET settings = COALESCE(p.settings,'{}'::jsonb) || '{"sprint_mode": true}'::jsonb
FROM workspace w WHERE w.id=p.workspace_id AND (
  (w.slug='sd-main' AND p.title IN ('sd-bridge','sd-cs')) OR
  (w.slug='octane'  AND p.title IN ('collection-department','servercrm','zoho-octane'))
);

-- 2) run-only "QA regression (sprint-end)" autopilot per project, assigned to
--    that workspace's QA Tester agent; skipped when a QA/regression-titled
--    run-only autopilot already exists (docs-titled ones don't count).
INSERT INTO autopilot (workspace_id, project_id, title, description, assignee_type, assignee_id, execution_mode, status, created_by_type, created_by_id)
SELECT p.workspace_id, p.id, p.title || ' QA regression (sprint-end)',
       'Whole-branch sprint regression: deploys the sprint branch to the QA target (when bound) and runs the base suite + attached-task cases; code-level build/test regression when no box is bound.',
       'agent',
       CASE w.slug WHEN 'sd-main' THEN '5f34c180-f6c3-4062-9c5a-e958f1430724'::uuid ELSE '35e44d47-377d-4043-af8b-887b603e91df'::uuid END,
       'run_only','active','member',
       CASE w.slug WHEN 'sd-main' THEN 'ccc0c7b4-0575-491a-8680-f52cff9c4815'::uuid ELSE '40bc3af1-d607-4a4e-9468-ad49a29666f5'::uuid END
FROM project p JOIN workspace w ON w.id=p.workspace_id
WHERE ((w.slug='sd-main' AND p.title IN ('sd-bridge','sd-cs')) OR
       (w.slug='octane'  AND p.title IN ('collection-department','servercrm','zoho-octane')))
  AND NOT EXISTS (SELECT 1 FROM autopilot a WHERE a.project_id=p.id AND a.execution_mode='run_only' AND a.status='active'
                  AND (lower(a.title) LIKE '%qa%' OR lower(a.title) LIKE '%regression%') AND lower(a.title) NOT LIKE '%docs%')
RETURNING title;
