-- Intentionally a no-op (no schema change on rollback).
--
-- The up migration only converges the rebrand drift toward the canonical
-- column name `agora_user_id`, which the current code requires. Renaming back
-- to `multica_user_id` on rollback would re-introduce SQLSTATE 42703 on every
-- lark_user_binding query and crash the running server — a strictly worse
-- state than leaving the column correctly named. The rename is forward-only by
-- design; this down exists so the runner can drop version 131 from the ledger
-- without mutating the schema.
DO $$
BEGIN
END $$;
