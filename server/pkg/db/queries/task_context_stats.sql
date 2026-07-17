-- name: UpsertTaskContextStats :exec
-- One row per task. Upsert (not insert) because a task retried under the same
-- ID must overwrite its earlier row rather than fail the whole completion
-- report over telemetry.
INSERT INTO task_context_stats (
    task_id, arm, files_scanned, files_in_pack, symbols_in_pack,
    pack_tokens, build_ms, degraded, partial
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (task_id)
DO UPDATE SET
    arm             = EXCLUDED.arm,
    files_scanned   = EXCLUDED.files_scanned,
    files_in_pack   = EXCLUDED.files_in_pack,
    symbols_in_pack = EXCLUDED.symbols_in_pack,
    pack_tokens     = EXCLUDED.pack_tokens,
    build_ms        = EXCLUDED.build_ms,
    degraded        = EXCLUDED.degraded,
    partial         = EXCLUDED.partial;

-- name: GetTaskContextStats :one
SELECT * FROM task_context_stats WHERE task_id = $1;
