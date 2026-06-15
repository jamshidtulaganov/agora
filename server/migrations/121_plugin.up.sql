-- Plugins: a named bundle of skills + MCP connectors, installable onto agents
-- as a unit (mirrors the Claude plugin model: a plugin groups skills +
-- connectors). Skills are shared workspace skills (skill table); connectors are
-- stored as an mcp_config blob ({"mcpServers": {...}}) merged into the agent's
-- own mcp_config on install.
CREATE TABLE IF NOT EXISTS plugin (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    mcp_config jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_plugin_workspace ON plugin (workspace_id);

-- Skills bundled into a plugin.
CREATE TABLE IF NOT EXISTS plugin_skill (
    plugin_id uuid NOT NULL REFERENCES plugin (id) ON DELETE CASCADE,
    skill_id uuid NOT NULL REFERENCES skill (id) ON DELETE CASCADE,
    PRIMARY KEY (plugin_id, skill_id)
);

-- Tracks which agents have a plugin installed (so the UI can show install state
-- and an uninstall can remove what the plugin added).
CREATE TABLE IF NOT EXISTS agent_plugin (
    agent_id uuid NOT NULL REFERENCES agent (id) ON DELETE CASCADE,
    plugin_id uuid NOT NULL REFERENCES plugin (id) ON DELETE CASCADE,
    installed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, plugin_id)
);
