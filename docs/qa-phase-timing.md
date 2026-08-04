# Measuring where a QA gate spends its time

`run_qa` reports one duration per task, which is useless for deciding whether a
recipe change helped. The gate now emits a `phases` array inside its
```` ```qa-result ```` fence, and because `CaptureQAEvidence` persists that
fence into `qa_evidence.result_json`, the timings are queryable with plain SQL
— no migration, no new endpoint. The stored object is the agent's fence exactly
as written, plus server-owned keys prefixed with `_` (today just
`_phase_timing`, see **Trust** below).

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

## Trust — read this before averaging anything

The fence's own timestamps are **self-reported by the agent**, and the first
live gate showed exactly how little that is worth: every boundary landed on an
exact minute, every phase was exactly 120s, and the reported `started_at` was 98
seconds *before* the gate's own dispatch comment existed. The agent
reconstructed the numbers afterwards instead of reading a clock.

So the platform stopped asking it to. The recipe now has the agent ANNOUNCE
each phase in-band — a bare `PHASE: checks` line, or
`PHASE: baseline skipped — every branch command passed` — and the daemon
timestamps those lines as it streams them into `task_message.created_at`. The
agent supplies the NAME; the platform supplies the CLOCK. Same convention as
the `PROGRESS:` headline and the `RUNNING test_case:` markers.

At capture, `derivePhasesFromStream` rebuilds the timeline from those message
timestamps and stores it at **`result_json._phase_timing.measured`**, with
`trust = "measured"`. When present it SUPERSEDES the fence's `phases` entirely —
aggregate `measured`, never `phases`. The underscore marks the key
server-owned; everything without one is the agent's fence as it wrote it.

A run with no markers (an older pinned template, or an agent that ignored the
instruction) falls back to reconciling the self-report against the dispatch and
capture times, which yields the weaker levels below.

| `trust` | meaning | safe to aggregate? |
|---|---|---|
| `measured` | rebuilt from stream markers — a platform clock | **yes, this is the real one** |
| `ok` | self-reported, inside the real window, not obviously synthesised | weakly |
| `estimated` | plausible, but every boundary is on an exact minute | directionally only |
| `implausible` | contradicts harness truth (pre-dispatch start, post-capture end, negative duration) | **no** |
| `absent` | no timed phases (all skipped) | n/a |

Rows captured before this check shipped carry no `_phase_timing` at all — treat
those as unknown, not as `ok`. **Every query below filters on trust.** A
`skipped` flag needs no such caveat: it is a boolean the agent either set or
didn't, with no clock involved, so the baseline skip rate is trustworthy even
when the durations are not.

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
CROSS JOIN LATERAL jsonb_array_elements(
  COALESCE(e.result_json->'_phase_timing'->'measured', e.result_json->'phases')
) ph
WHERE jsonb_typeof(COALESCE(e.result_json->'_phase_timing'->'measured', e.result_json->'phases')) = 'array'
  AND ph->>'phase' = 'baseline';
```

### How much of the data is even usable

Run this first. If most rows are `estimated`, the percentiles below are
describing the agents' rounding habits, not the gate.

```sql
SELECT COALESCE(result_json->'_phase_timing'->>'trust', 'unchecked') AS trust,
       count(*) AS gates
FROM qa_evidence
WHERE jsonb_typeof(result_json->'phases') = 'array'
GROUP BY 1 ORDER BY gates DESC;
```

### Per-phase duration percentiles

```sql
WITH p AS (
  SELECT ph->>'phase' AS phase,
         EXTRACT(EPOCH FROM (
           (ph->>'finished_at')::timestamptz - (ph->>'started_at')::timestamptz
         )) AS secs
  FROM qa_evidence e
  CROSS JOIN LATERAL jsonb_array_elements(e.result_json->'_phase_timing'->'measured') ph
  WHERE e.result_json->'_phase_timing'->>'trust' = 'measured'  -- platform clock only
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
  CROSS JOIN LATERAL jsonb_array_elements(e.result_json->'_phase_timing'->'measured') ph
  WHERE e.result_json->'_phase_timing'->>'trust' = 'measured'
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
per-gate saving is the `baseline` phase's own p50 from the per-phase query. Use
this last one only to sanity-check that the totals moved in the direction the
others predict.

## Local connection

The default DSN baked into the Go test harness is `agora:agora@…/agora`,
which is **not** this checkout's database. Use the values from `.env`:

```bash
psql "postgres://agora:agora@localhost:5432/agora?sslmode=disable" -c '\d qa_evidence'
```
