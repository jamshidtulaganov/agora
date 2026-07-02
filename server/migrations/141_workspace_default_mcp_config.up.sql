-- Workspace-level default MCP servers. Merged into every agent's mcp_config
-- at task-claim time (agent-level entries win on name collision), so agents
-- created through any path (UI, CLI, template, agent-created) get the
-- workspace's shared tool servers without per-agent configuration.
-- Deliberately a dedicated column, NOT a key inside workspace.settings:
-- settings is returned whole to every member and replaced whole on update,
-- which would leak the config's auth material and clobber it on unrelated
-- settings writes.
ALTER TABLE workspace ADD COLUMN default_mcp_config jsonb;
