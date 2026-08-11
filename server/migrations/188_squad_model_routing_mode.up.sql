ALTER TABLE squad
ADD COLUMN model_routing_mode text NOT NULL DEFAULT 'pinned'
CHECK (model_routing_mode IN ('pinned', 'cost', 'balanced', 'intelligence'));
