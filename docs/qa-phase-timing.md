# Measuring where a QA gate spends its time

`run_qa` reports one duration per task, which is useless for deciding whether a
recipe change helped. The gate now emits a `phases` array inside its
```` ```qa-result ```` fence, and because `CaptureQAEvidence` persists that
fence **verbatim** into `qa_evidence.result_json`, the timings are queryable
with plain SQL — no migration, no new endpoint.

## The contract

Emitted by `server/internal/handler/slice_action_templates/run_qa.md` step (5),
parsed by `qaResultPayload.PhaseTimings()` in
`server/internal/service/qa_evidence.go`.

```json
"phases": [
  {"phase": "checks",   "started_at": "2026-07-27T10:00:00Z", "finished_at": "2026-07-27T10:04:00Z"},
  {"phase": "baseline", "skipped": true, "note": "every branch command passed"},
  {"phase": "smoke",    "started_at": "2026-07-27T10:04:00Z", "finished_at": "2026-07-27T10:05:30Z"},
  {"phase": "cases",    "started_at": "2026-07-27T10:05:30Z", "finished_at": "2026-07-27T10:07:10Z"}
]
```

Five fixed phase names, matching the recipe's numbered steps: `checks`,
`baseline`, `smoke`, `cases`, `materialize`. A phase that RAN carries both
timestamps and no `skipped`. A phase that was SKIPPED carries `skipped: true`
and a one-line `note`, and **omits** the timestamps — a skipped step is not a
zero-length one, and recording it as `finished_at == started_at` would pollute
the percentiles.

**Telemetry is never a gate.** `phases` is held as `json.RawMessage` so any
valid JSON there still captures the verdict; `PhaseTimings()` returns nil on
anything it can't decode. An agent that emits garbage timings loses its
timings, never its verdict. Agents on an older pinned template emit nothing
here at all, so treat a missing array as "unknown", not "zero".

## What this exists to answer

The gate used to build the merge-base first and the branch second, running the
full build+lint+test matrix twice. It now runs the branch first and only
re-runs the commands that went **red** against the base. The question that
change begs is: how often is the baseline skipped entirely, and what does that
save? That is exactly the `{"phase":"baseline","skipped":true}` entry.

### Baseline skip rate

```sql
SELECT
  count(*)                                            AS gates,
  count(*) FILTER (WHERE ph->>'skipped' = 'true')     AS baseline_skipped,
  round(100.0 * count(*) FILTER (WHERE ph->>'skipped' = 'true') / count(*), 1) AS pct_skipped
FROM qa_evidence e
CROSS JOIN LATERAL jsonb_array_elements(e.result_json->'phases') ph
WHERE jsonb_typeof(e.result_json->'phases') = 'array'
  AND ph->>'phase' = 'baseline';
```

### Per-phase duration percentiles

```sql
WITH p AS (
  SELECT ph->>'phase' AS phase,
         EXTRACT(EPOCH FROM (
           (ph->>'finished_at')::timestamptz - (ph->>'started_at')::timestamptz
         )) AS secs
  FROM qa_evidence e
  CROSS JOIN LATERAL jsonb_array_elements(e.result_json->'phases') ph
  WHERE jsonb_typeof(e.result_json->'phases') = 'array'
    AND ph->>'started_at' IS NOT NULL
    AND ph->>'finished_at' IS NOT NULL
)
SELECT phase, count(*) AS n,
       round(percentile_cont(0.5) WITHIN GROUP (ORDER BY secs)::numeric, 0) AS p50_s,
       round(percentile_cont(0.9) WITHIN GROUP (ORDER BY secs)::numeric, 0) AS p90_s
FROM p
WHERE secs BETWEEN 0 AND 7200   -- drop clock-skew and copy-paste nonsense
GROUP BY phase
ORDER BY p50_s DESC;
```

### What the lazy baseline actually saved

Compare gates that skipped the baseline against gates that had to run it. The
difference in total gate time is the saving, and the `baseline` row's own p50
is the per-gate cost avoided.

```sql
WITH g AS (
  SELECT e.id,
         bool_or(ph->>'phase' = 'baseline' AND ph->>'skipped' = 'true') AS skipped_baseline,
         sum(EXTRACT(EPOCH FROM (
           (ph->>'finished_at')::timestamptz - (ph->>'started_at')::timestamptz
         ))) AS total_secs
  FROM qa_evidence e
  CROSS JOIN LATERAL jsonb_array_elements(e.result_json->'phases') ph
  WHERE jsonb_typeof(e.result_json->'phases') = 'array'
  GROUP BY e.id
)
SELECT skipped_baseline, count(*) AS n,
       round(percentile_cont(0.5) WITHIN GROUP (ORDER BY total_secs)::numeric, 0) AS p50_total_s
FROM g
WHERE total_secs BETWEEN 0 AND 7200
GROUP BY skipped_baseline;
```

Read that last one carefully before drawing a conclusion: a gate skips the
baseline precisely *because* every branch command passed, so the two groups are
not comparable populations — the skipped group is biased toward healthy
branches, which are cheaper for reasons beyond the baseline. The honest
per-gate saving is the `baseline` phase's own p50 from the second query. Use
the third only to sanity-check that the totals moved in the direction the
first two predict.

## Local connection

The default DSN baked into the Go test harness is `multica:multica@…/multica`,
which is **not** this checkout's database. Use the values from `.env`:

```bash
psql "postgres://agora:agora@localhost:5432/agora?sslmode=disable" -c '\d qa_evidence'
```
