-- Per-module sync definition for the dynamic Zoho integration
-- (docs/zoho-dynamic-integration.md §1.3): which CRM module syncs where, in
-- which direction, and how its fields/statuses map onto Agora issues. One
-- generic engine (zohodyn_sync.go) reconciles every row — no per-module code.
-- field_map values are Zoho field API names keyed by the whitelisted Agora
-- fields (title/description/priority/due_date/status); status_map carries the
-- inbound (Zoho→Agora) and outbound (Agora→Zoho) status tables. cursor is the
-- Modified_Time incremental watermark; watch_* columns are reserved for the
-- D4 realtime notification channel.
CREATE TABLE zoho_sync_config (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    connection_id    uuid NOT NULL REFERENCES zoho_connection(id) ON DELETE CASCADE,
    channel          text NOT NULL DEFAULT 'crm',
    module_api_name  text NOT NULL,
    project_id       uuid REFERENCES project(id) ON DELETE SET NULL,
    enabled          boolean NOT NULL DEFAULT true,
    direction        text NOT NULL DEFAULT 'both' CHECK (direction IN ('in', 'out', 'both')),
    field_map        jsonb NOT NULL DEFAULT '{}',
    status_map       jsonb NOT NULL DEFAULT '{}',
    filter_coql      text NOT NULL DEFAULT '',
    cursor           timestamptz,
    watch_channel_id text NOT NULL DEFAULT '',
    watch_expires_at timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, channel, module_api_name)
);
