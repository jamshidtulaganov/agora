-- Data-only historical repair. Reverting the corrected snapshots would
-- knowingly restore inaccurate owner/controller labels, so rollback is a no-op.
SELECT 1;
