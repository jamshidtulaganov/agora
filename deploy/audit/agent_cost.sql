-- Agent cost audit — per-issue / per-model token spend with a $ estimate.
--
-- Run:
--   docker exec -i multica-postgres-1 psql -U multica -d multica -v days=30 -f - < deploy/audit/agent_cost.sql
-- or pick a window inline:
--   docker exec -i multica-postgres-1 psql -U multica -d multica -v days=7  -f - < deploy/audit/agent_cost.sql
--
-- WHY: small tasks were observed costing opus[1m] money (a CSS block-align fix
-- ran at ~$2.82). This is the standing instrument to (a) confirm task_usage is
-- still recording and (b) quantify where spend goes so model-tiering can be
-- targeted. Prices are PUBLISHED-LIST APPROXIMATIONS (USD / MTok), not billed
-- amounts — keep the multipliers in pricing() in sync with current rates.
--   opus:   in 15    out 75   cache_rd 1.50  cache_wr 18.75
--   sonnet: in  3    out 15   cache_rd 0.30  cache_wr  3.75
--   haiku:  in  0.8  out  4   cache_rd 0.08  cache_wr  1.00
-- Model family is matched by name prefix so it covers opus-4-7, opus-4-8,
-- the [1m] long-context variants, dated haiku ids, etc.

\set days 30

\echo '== window: last' :days 'days =='

SELECT '--- by issue (re-run waste shows as high runs) ---' AS section;
WITH costed AS (
  SELECT t.issue_id, u.task_id, u.model,
    CASE WHEN u.model LIKE 'claude-opus%' THEN (u.input_tokens*15+u.output_tokens*75+u.cache_read_tokens*1.5+u.cache_write_tokens*18.75)/1e6
         WHEN u.model LIKE 'claude-sonnet%' THEN (u.input_tokens*3+u.output_tokens*15+u.cache_read_tokens*0.30+u.cache_write_tokens*3.75)/1e6
         ELSE (u.input_tokens*0.8+u.output_tokens*4+u.cache_read_tokens*0.08+u.cache_write_tokens*1.0)/1e6 END AS usd
  FROM task_usage u JOIN agent_task_queue t ON t.id=u.task_id
  WHERE u.created_at > now() - (:'days' || ' days')::interval
)
SELECT left(coalesce(iss.title,'(no issue)'),44) AS issue,
       count(DISTINCT c.task_id) AS runs,
       round(sum(c.usd)::numeric,2) AS total_usd,
       string_agg(DISTINCT regexp_replace(c.model,'claude-|-20251001','','g'), ',') AS models
FROM costed c LEFT JOIN issue iss ON iss.id=c.issue_id
GROUP BY iss.title ORDER BY total_usd DESC NULLS LAST LIMIT 40;

SELECT '--- by model ---' AS section;
WITH costed AS (
  SELECT u.model, u.input_tokens i, u.output_tokens o, u.cache_read_tokens cr, u.cache_write_tokens cw, u.task_id,
    CASE WHEN u.model LIKE 'claude-opus%' THEN (u.input_tokens*15+u.output_tokens*75+u.cache_read_tokens*1.5+u.cache_write_tokens*18.75)/1e6
         WHEN u.model LIKE 'claude-sonnet%' THEN (u.input_tokens*3+u.output_tokens*15+u.cache_read_tokens*0.30+u.cache_write_tokens*3.75)/1e6
         ELSE (u.input_tokens*0.8+u.output_tokens*4+u.cache_read_tokens*0.08+u.cache_write_tokens*1.0)/1e6 END AS usd
  FROM task_usage u
  WHERE u.created_at > now() - (:'days' || ' days')::interval
)
SELECT regexp_replace(model,'claude-|-20251001','','g') AS model,
       count(DISTINCT task_id) AS tasks,
       sum(o) AS out_tok, (sum(cr)/1000)::int AS cache_rd_k,
       round(sum(usd)::numeric,2) AS total_usd
FROM costed GROUP BY model ORDER BY total_usd DESC;

SELECT '--- totals ---' AS section;
WITH costed AS (
  SELECT u.cache_read_tokens cr, u.input_tokens i,
    CASE WHEN u.model LIKE 'claude-opus%' THEN (u.input_tokens*15+u.output_tokens*75+u.cache_read_tokens*1.5+u.cache_write_tokens*18.75)/1e6
         WHEN u.model LIKE 'claude-sonnet%' THEN (u.input_tokens*3+u.output_tokens*15+u.cache_read_tokens*0.30+u.cache_write_tokens*3.75)/1e6
         ELSE (u.input_tokens*0.8+u.output_tokens*4+u.cache_read_tokens*0.08+u.cache_write_tokens*1.0)/1e6 END AS usd
  FROM task_usage u
  WHERE u.created_at > now() - (:'days' || ' days')::interval
)
SELECT round(sum(usd)::numeric,2) AS total_usd,
       round((sum(cr)::numeric / NULLIF(sum(cr+i),0))*100,1) AS cache_hit_pct
FROM costed;
