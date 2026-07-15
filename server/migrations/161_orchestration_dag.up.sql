CREATE TABLE orchestration_step_dependency (
    step_id UUID NOT NULL REFERENCES orchestration_step(id) ON DELETE CASCADE,
    depends_on_step_id UUID NOT NULL REFERENCES orchestration_step(id) ON DELETE CASCADE,
    PRIMARY KEY (step_id, depends_on_step_id),
    CHECK (step_id <> depends_on_step_id)
);

INSERT INTO orchestration_step_dependency (step_id, depends_on_step_id)
SELECT id, depends_on_step_id
FROM orchestration_step
WHERE depends_on_step_id IS NOT NULL
ON CONFLICT DO NOTHING;

CREATE INDEX idx_orchestration_dependency_parent
    ON orchestration_step_dependency(depends_on_step_id);
