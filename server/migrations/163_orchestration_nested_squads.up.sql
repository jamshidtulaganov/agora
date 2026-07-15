ALTER TABLE orchestration_step
    ADD COLUMN squad_id UUID REFERENCES squad(id) ON DELETE SET NULL,
    ADD COLUMN controller_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL;

CREATE INDEX idx_orchestration_step_parent ON orchestration_step(parent_step_id);
CREATE INDEX idx_orchestration_step_squad ON orchestration_step(squad_id) WHERE squad_id IS NOT NULL;
