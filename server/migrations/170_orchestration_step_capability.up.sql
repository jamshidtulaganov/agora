ALTER TABLE orchestration_step
    ADD COLUMN capability TEXT NOT NULL DEFAULT 'implementation';

UPDATE orchestration_step
SET capability = CASE
    WHEN step_kind = 'integration' THEN 'integration'
    WHEN stage = 'plan' THEN 'coordination'
    WHEN stage = 'qa' THEN 'qa'
    WHEN stage = 'review' THEN 'review'
    WHEN stage = 'release' THEN 'release'
    ELSE 'implementation'
END;

ALTER TABLE orchestration_step
    ADD CONSTRAINT orchestration_step_capability_check CHECK (capability IN (
        'coordination', 'implementation', 'backend', 'frontend', 'mobile',
        'infrastructure', 'documentation', 'integration', 'qa', 'review', 'release'
    ));

COMMENT ON COLUMN orchestration_step.capability IS
    'Stable responsibility category used by the squad planner, rerouting validation, and Active Work UI.';
