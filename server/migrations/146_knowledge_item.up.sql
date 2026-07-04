-- Structured knowledge base ("KB flywheel"). Each row is one durable learning
-- (a gotcha, convention, architecture fact, nav hint, or decision) captured
-- from completed work or entered by a human. The project's <slug>-kb skill
-- content is no longer hand-edited by agents: the server deterministically
-- compiles ACTIVE items into a marker-delimited managed region of that skill
-- (see internal/service/knowledge_compile.go), ranked by hits + recency so
-- the size budget is a ranking cutoff, not truncation.
--
-- kb_name is the resolved KB skill name (service.ProjectKBSkillName) at
-- ingest time and is the compile + dedupe key: MANY projects may share one
-- KB skill (settings.kb_skill override — e.g. Bitrix sprint-bucket projects
-- all pointing at "sd-main-kb"), so keying by project_id would let sibling
-- projects clobber each other's compiled region. project_id is provenance.
--
-- Agent-proposed items of instruction-bearing kinds land as 'proposed' and
-- require human approval (prompt-injection review gate).
CREATE TABLE knowledge_item (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    kb_name text NOT NULL,                      -- resolved KB skill name; compile + dedupe key
    module text NOT NULL DEFAULT '',            -- matches the project's module:* label vocabulary; '' = project-wide
    kind text NOT NULL DEFAULT 'gotcha',        -- 'architecture' | 'gotcha' | 'convention' | 'nav' | 'decision'
    title text NOT NULL,                        -- one factual sentence, <=160 runes (enforced at ingest)
    body text NOT NULL DEFAULT '',              -- short markdown, <=1200 runes (enforced at ingest)
    norm_title text NOT NULL,                   -- normalized dedupe key (see normalizeKnowledgeTitle)
    source_issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
    created_by_type text NOT NULL DEFAULT 'agent',  -- 'agent' | 'member' | 'system'
    created_by_id uuid,                         -- agent.id or "user".id; nullable (system)
    status text NOT NULL DEFAULT 'active',      -- 'active' | 'proposed' | 'archived'
    hits integer NOT NULL DEFAULT 0,            -- times re-confirmed (trusted proposers only)
    last_confirmed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_knowledge_item_kb ON knowledge_item(workspace_id, kb_name, status);
CREATE INDEX idx_knowledge_item_project ON knowledge_item(project_id);
-- Exact-dedupe arbiter: one live item per normalized title per KB (NOT per
-- project — sibling sprint buckets sharing a KB must confirm, not duplicate).
-- Partial (excludes archived) so a retired item can be re-learned.
CREATE UNIQUE INDEX knowledge_item_kb_norm_title_idx
    ON knowledge_item(workspace_id, kb_name, norm_title) WHERE status <> 'archived';

-- Per-task model escalation for system-enqueued runs (first user: KB capture
-- on large issue threads runs sonnet instead of haiku). Precedence at claim:
-- wins over the agent's configured model, loses to issue cost-tier labels
-- (applyIssueCostTier). NULL = no override, use the agent's model.
ALTER TABLE agent_task_queue
    ADD COLUMN model_override text;
