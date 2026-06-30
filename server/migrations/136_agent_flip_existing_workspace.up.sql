-- One-time data flip: make EXISTING private agents workspace-visible so invited
-- members can see the agents already in their workspace. The column DEFAULT is
-- intentionally left 'private' (per decision) — agents created after this keep
-- defaulting to private and are managed individually. Idempotent: re-running
-- only ever touches rows still marked private.
UPDATE agent SET visibility = 'workspace' WHERE visibility = 'private';
