-- task_context_stats records what the dispatch-time repo context pack did for
-- one task, and which experiment arm the task landed in.
--
-- Sibling of task_usage rather than columns on it: task_usage is grained per
-- (task, provider, model), while these are counters about a single prompt
-- build. Adding them to task_usage would duplicate one task's pack stats
-- across every model row and break any SUM over the table.
--
-- Rows are written for BOTH arms. A control task with no row would leave the
-- A/B without a denominator, so eligibility — not treatment — decides whether
-- a task is recorded.
CREATE TABLE task_context_stats (
    task_id         UUID PRIMARY KEY REFERENCES agent_task_queue(id) ON DELETE CASCADE,
    arm             SMALLINT NOT NULL,             -- 0 = control (no pack), 1 = treatment
    files_scanned   INTEGER  NOT NULL DEFAULT 0,   -- corpus the ranker saw
    files_in_pack   INTEGER  NOT NULL DEFAULT 0,   -- files rendered into the prompt
    symbols_in_pack INTEGER  NOT NULL DEFAULT 0,
    pack_tokens     INTEGER  NOT NULL DEFAULT 0,   -- what the pack COST; net savings need this
    build_ms        INTEGER  NOT NULL DEFAULT 0,
    degraded        BOOLEAN  NOT NULL DEFAULT FALSE, -- treatment arm but no usable pack: analyze separately or it dilutes the effect
    partial         BOOLEAN  NOT NULL DEFAULT FALSE, -- corpus truncated at the scan cap
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The analysis query is "group every task by arm and join its token usage",
-- so arm is the access path.
CREATE INDEX idx_task_context_stats_arm ON task_context_stats(arm);
