-- Reconcile the Multica -> Agora rebrand drift on lark_user_binding.
--
-- Migration 109 was edited IN PLACE during the rebrand to rename the column
-- multica_user_id -> agora_user_id. The migration runner tracks applied
-- versions by filename only (no checksum), so any database that already
-- recorded version 109 will FOREVER skip the re-edited file. Those databases
-- still carry the column `multica_user_id` while the generated sqlc code reads
-- `agora_user_id` -> SQLSTATE 42703 ("column agora_user_id does not exist") on
-- every lark_user_binding read AND write, which crashes the inbound dispatcher
-- and the outbound notify lookup.
--
-- This forward migration converges all three possible live states idempotently.
-- It is safe to run on a fresh DB (agora-only, no-op), a legacy DB (rename), or
-- a DB that was hand-patched out of band (agora-only, no-op).
DO $$
DECLARE
    has_legacy BOOLEAN;
    has_canon  BOOLEAN;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'lark_user_binding'
          AND column_name = 'multica_user_id'
    ) INTO has_legacy;

    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'lark_user_binding'
          AND column_name = 'agora_user_id'
    ) INTO has_canon;

    IF has_legacy AND NOT has_canon THEN
        -- Legacy-only: rename in place. The composite member FK and the
        -- (user, workspace) index that reference the column follow the rename
        -- automatically, so no constraint/index rebuild is needed.
        ALTER TABLE lark_user_binding RENAME COLUMN multica_user_id TO agora_user_id;
    ELSIF has_legacy AND has_canon THEN
        -- Both columns present (a prior partial fix). Backfill the canonical
        -- column where empty, then drop the legacy one.
        UPDATE lark_user_binding SET agora_user_id = multica_user_id WHERE agora_user_id IS NULL;
        ALTER TABLE lark_user_binding DROP COLUMN multica_user_id;
    END IF;
    -- agora-only (fresh DBs + already hand-fixed prod): nothing to do.
END $$;
