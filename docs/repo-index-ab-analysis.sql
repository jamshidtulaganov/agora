-- Repo context pack — A/B readout.
--
-- Run against the Agora DB once enough tasks have run through both arms:
--   docker exec agora-postgres-1 psql -U agora -d agora -f docs/repo-index-ab-analysis.sql
--   (prod: fly ssh console -a sd-agora-db, then psql -U multica -d multica)
--
-- Arm 0 = control (no pack). Arm 1 = treatment (pack pushed into the prompt).
-- Assignment is deterministic: hash(task_id), so a retried task keeps its arm.
--
-- READ THIS BEFORE READING THE NUMBERS
--
-- The currency is COST-WEIGHTED tokens, not raw tokens. Cache reads are
-- discounted roughly 10x against fresh input, and repeated file reads are
-- mostly cache hits — so a raw-token comparison can inverting the conclusion.
-- Weights below are Claude's pricing ratios (input=1, cache_read=0.1,
-- cache_write=1.25, output=5). Re-derive them if the dispatched model mix
-- changes materially.
--
-- Savings that degrade task success are a net loss. Query 3 is not optional.

\echo ''
\echo '=== 1. Cost-weighted tokens per task, by arm ==============================='
\echo '(treatment must also be read against query 2: a degraded task got no pack)'
\echo ''

WITH cw AS (
    SELECT
        s.task_id,
        s.arm,
        s.degraded,
        s.pack_tokens,
        SUM(u.input_tokens * 1.0
          + u.cache_read_tokens * 0.1
          + u.cache_write_tokens * 1.25
          + u.output_tokens * 5.0) AS cost_weighted
    FROM task_context_stats s
    JOIN task_usage u ON u.task_id = s.task_id
    GROUP BY s.task_id, s.arm, s.degraded, s.pack_tokens
)
SELECT
    CASE arm WHEN 0 THEN 'control' ELSE 'treatment' END AS arm,
    COUNT(*)                                    AS tasks,
    ROUND(AVG(cost_weighted))                   AS avg_cost_weighted_tokens,
    ROUND(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY cost_weighted)) AS median,
    ROUND(AVG(pack_tokens))                     AS avg_pack_cost_tokens
FROM cw
-- A treatment task that received no pack is behaviourally a control task.
-- Leaving it in the treatment arm dilutes the effect toward zero.
WHERE NOT degraded
GROUP BY arm
ORDER BY arm;

\echo ''
\echo '=== 2. Pack delivery health (treatment arm) ==============================='
\echo '(high degraded % means the effect in query 1 is measured on few tasks)'
\echo ''

SELECT
    COUNT(*)                                              AS treatment_tasks,
    COUNT(*) FILTER (WHERE degraded)                      AS degraded_no_pack,
    ROUND(100.0 * COUNT(*) FILTER (WHERE degraded) / NULLIF(COUNT(*), 0), 1) AS pct_degraded,
    ROUND(AVG(files_in_pack) FILTER (WHERE NOT degraded), 1) AS avg_files_in_pack,
    ROUND(AVG(pack_tokens)   FILTER (WHERE NOT degraded))    AS avg_pack_tokens,
    ROUND(AVG(build_ms))                                  AS avg_build_ms,
    MAX(build_ms)                                         AS max_build_ms,
    COUNT(*) FILTER (WHERE partial)                       AS corpus_truncated
FROM task_context_stats
WHERE arm = 1;

\echo ''
\echo '=== 3. Outcome guardrail: did the pack hurt success? ======================'
\echo '(a cheaper arm that fails more is a regression, not a saving)'
\echo ''

SELECT
    CASE s.arm WHEN 0 THEN 'control' ELSE 'treatment' END AS arm,
    COUNT(*)                                                       AS tasks,
    COUNT(*) FILTER (WHERE t.status = 'completed')                 AS completed,
    ROUND(100.0 * COUNT(*) FILTER (WHERE t.status = 'completed')
          / NULLIF(COUNT(*), 0), 1)                                AS pct_completed,
    ROUND(AVG(t.attempt), 2)                                       AS avg_attempts
FROM task_context_stats s
JOIN agent_task_queue t ON t.id = s.task_id
GROUP BY s.arm
ORDER BY s.arm;

\echo ''
\echo '=== 4. Did the pack actually reduce exploration? =========================='
\echo '(the mechanism check: fewer Read/Grep/cat/find round trips per task)'
\echo ''

WITH tool_results AS (
    SELECT
        m.task_id,
        m.tool,
        LOWER(COALESCE((SELECT u.input ->> 'command'
                        FROM task_message u
                        WHERE u.task_id = m.task_id
                          AND u.seq = m.seq - 1
                          AND u.type = 'tool_use'), '')) AS cmd
    FROM task_message m
    WHERE m.type = 'tool_result'
),
classified AS (
    SELECT
        task_id,
        CASE
            WHEN tool IN ('Read', 'Grep', 'Glob', 'LS') THEN 1
            WHEN tool = 'Bash' AND cmd ~ '(^|[|;& ])(rg|grep|ag|ack|cat|head|tail|less|find|ls|tree|fd|sed -n|awk)( |$)' THEN 1
            WHEN tool = 'Bash' AND cmd ~ '(^|[|;& ])git (log|show|diff|blame|ls-files|ls-tree)' THEN 1
            ELSE 0
        END AS is_explore
    FROM tool_results
),
per_task AS (
    SELECT task_id, SUM(is_explore) AS explore_calls, COUNT(*) AS tool_calls
    FROM classified
    GROUP BY task_id
)
SELECT
    CASE s.arm WHEN 0 THEN 'control' ELSE 'treatment' END AS arm,
    COUNT(*)                          AS tasks,
    ROUND(AVG(p.explore_calls), 1)    AS avg_explore_calls,
    ROUND(AVG(p.tool_calls), 1)       AS avg_tool_calls,
    ROUND(100.0 * SUM(p.explore_calls) / NULLIF(SUM(p.tool_calls), 0), 1) AS pct_calls_exploration
FROM task_context_stats s
JOIN per_task p ON p.task_id = s.task_id
WHERE NOT s.degraded
GROUP BY s.arm
ORDER BY s.arm;
