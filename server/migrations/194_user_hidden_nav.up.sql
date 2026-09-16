-- Per-user sidebar customization: the nav keys this user chose to hide.
--
-- Stored on "user" rather than per-workspace on purpose: "I never use
-- Runtimes" is a statement about how a person works, not about one
-- workspace, so the choice follows them across workspaces and devices.
-- Array of nav-key strings, e.g. ["usage","mcp"]. Empty = show everything.
ALTER TABLE "user" ADD COLUMN hidden_nav JSONB NOT NULL DEFAULT '[]';
