# Zoho Dynamic Integration — Design

> Supersedes the *static per-module* portion of `zoho-suite-integration-plan.md` for CRM
> (and any future Zoho app with operator-defined schemas). Work-item channels with fixed
> semantics — Projects tasks, Sprints items, Desk tickets — keep the proven static
> skeleton (`zohoprojects_sync.go` mirror). This doc covers everything where the schema
> is **not known at compile time**: Zoho CRM's per-org modules (Octane: ~58 custom
> modules), and the connection/discovery layer shared by all channels.

## 0. Why dynamic

Octane's CRM is not stock: `Transactions`, `Money_Codes`, `Cards`, `Tickets`
(CustomModule34), `Billing_Submissions`, `Collection_Cases`, `Chase/Stripe/Zelle/MX`
transaction logs, plus Books/Expense links. A static Go `mapping.go` per module means a
code change + deploy for every module an operator wants connected. Dynamic means:
**connect once, discover modules at runtime, configure sync per module in the UI, and
one generic engine reconciles any of them bidirectionally.**

The agent tool plane is already dynamic — the hosted Zoho MCP servers expose
schema-aware tools (`getModules`, `getFields`, COQL, CRUD) that work on any module,
including custom ones, and the workspace `default_mcp_config` (migration 141) delivers
them to every agent automatically. Structural sync (this doc) is for the *issue-tracking
loop*: records that represent work land as Agora issues and changes flow back. Anything
that is not a work item (Books invoices, deal fields, ticket replies authored by agents)
stays on the tool plane.

## 1. Layers

### 1.1 `zoho_connection` — one credential row per workspace

Replaces env-var credentials for the dynamic path (env stays as fallback for the static
channels until migrated).

```sql
CREATE TABLE zoho_connection (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    dc             text NOT NULL DEFAULT 'us',          -- data center, host table derives from it
    client_id      text NOT NULL,
    client_secret  bytea NOT NULL,                      -- sealed (AGORA_ZOHO_SECRET_KEY, secretbox — figma_credential pattern)
    refresh_token  bytea NOT NULL,                      -- sealed
    scopes         text NOT NULL,                       -- granted scope list, for re-consent detection
    crm_org_id     text, desk_org_id text, projects_portal_id text, sprints_team_id text,
    probe_status   text, probed_at timestamptz,         -- nightly revalidation (figma probe pattern)
    created_by     uuid, created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id)
);
```

Access token cache in-process per (connection, dc) — same ~55m TTL discipline as
`zohoprojects/client.go`. All dynamic-engine calls and (eventually) static channels
share it. Endpoints: `GET/PUT/DELETE /api/zoho/connection` (owner/admin, agent actors
rejected, audited — mirror `workspace_mcp.go` / MUL-2600 contract).

### 1.2 Discovery — runtime metadata, not compile-time mapping

- `GET /api/zoho/crm/modules` → proxied `settings/modules` (filtered: `generated_type IN
  (default, custom)`, API-supported, creatable) + per-module `getFields` on demand.
- `GET /api/zoho/desk/departments`, `GET /api/zoho/projects/portal-projects` — already
  exist or ship with the static channels.
- Response includes which modules already have a sync config, so the UI renders
  connect/connected states.

### 1.3 `zoho_sync_config` — per-module sync definition (the "dynamic" core)

```sql
CREATE TABLE zoho_sync_config (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    connection_id  uuid NOT NULL REFERENCES zoho_connection(id) ON DELETE CASCADE,
    channel        text NOT NULL,             -- 'crm' now; engine is channel-agnostic by design
    module_api_name text NOT NULL,            -- e.g. 'Tasks', 'CustomModule34'
    project_id     uuid REFERENCES project(id),  -- destination Agora project (created on first sync when null)
    enabled        boolean NOT NULL DEFAULT true,
    direction      text NOT NULL DEFAULT 'both',  -- 'in' | 'out' | 'both'
    field_map      jsonb NOT NULL,            -- {"title": "Subject", "description": "Description", "due_date": "Due_Date", ...}
    status_map     jsonb NOT NULL,            -- {"in": {"Open": "todo", "Escalated": "in_progress", ...}, "out": {"done": "Closed", ...}}
    filter_coql    text,                      -- optional WHERE fragment (e.g. only records owned by a queue)
    cursor         timestamptz,               -- Modified_Time incremental cursor
    watch_channel_id text, watch_expires_at timestamptz,  -- CRM notifications channel (≤1 week, renewal cron)
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, channel, module_api_name)
);
```

`field_map` / `status_map` get **suggested defaults** generated from `getFields`
metadata (picklist values → status buckets by name heuristics) which the operator can
edit in the UI. Unknown inbound picklist value → default bucket, never a crash
(enum-drift rule).

### 1.4 Generic reconcile engine — one loop for N modules

`server/internal/handler/zohodyn_sync.go` + `server/internal/integrations/zohocrm/`
(thin COQL client: `RunCOQL`, `GetRecord`, `UpsertRecord`, `ListFields`,
`WatchSubscribe/Renew` — no per-module code).

Inbound per enabled config row (poll every `ZOHO_CRM_SYNC_INTERVAL`, default 15m, plus
watch-channel pushes):

1. `SELECT id, Modified_Time, <mapped fields> FROM <module> WHERE Modified_Time > <cursor> [AND <filter_coql>] ORDER BY Modified_Time` (COQL, `page_token` pagination).
2. Per record: advisory lock `"<wsID>:zohodyn:<module>:<recordID>"`; dedup by metadata
   `{"zoho_rec_id": "<module>:<id>"}` (single generic key — module-qualified, one GIN
   filter for every module).
3. Found → RAW bus-free UPDATE of mapped fields + status (echo-guard). Not found →
   `IssueService.Create` with `AllowDuplicate: true`, then stamp metadata:
   `zoho_rec_id`, `zoho_module`, `zoho_record_url`, `zoho_owner_email`,
   `zoho_status_name`.
4. Assignee: `Owner.email` → member email match (no provisioning v1).

Outbound (`registerZohoDynOutbound`, event bus `issue:updated`):

1. Skip unless issue metadata has `zoho_rec_id` and its module's config has
   `direction IN ('out','both')`.
2. Map Agora status via `status_map.out`; unmapped → skip + debug log (Zoho blueprints
   may reject arbitrary jumps — never guess).
3. `PUT /crm/v8/<module>/<id>` with mapped fields. Inbound RAW writes guarantee no echo.

Realtime: `POST /crm/v8/actions/watch` per module on config create/enable; callback
`POST /zoho-crm/webhook` (public route: constant-time secret compare, always-200,
rate-limited, detached single-record reconcile — `BitrixWebhook` pattern); renewal cron
re-subscribes before `watch_expires_at` (≤1 week). Poll stays on as the reconciliation
backstop — watch delivery is best-effort.

### 1.5 UI — Settings → Integrations → Zoho

- Connection card: connect (OAuth authorize URL w/ full scope superset → callback stores
  sealed tokens), status (probe), disconnect.
- Modules tab: discovered module list, toggle per module, per-module drawer for
  field/status map + direction + destination project. Desk/Projects tabs remain the
  static channels' import panels.

## 2. What stays static (and why)

| Channel | Why static |
|---|---|
| Projects tasks / Sprints items | Fixed work-item schema; skeleton already shipped |
| Desk tickets | Fixed ticket schema; in flight (P2+P4 bidirectional) |
| Books / Expense / Cliq / everything non-work-item | Agent tool plane (MCP), no structural sync |

The dynamic engine deliberately does NOT try to subsume these in v1 — `channel` +
engine interfaces are shaped so Desk *could* migrate onto it later if maintaining two
skeletons hurts.

## 3. Build order

1. **D1 — connection + discovery**: `zoho_connection` migration, sealed-credential
   endpoints (+OAuth callback), CRM modules/fields proxy endpoints. Unblocks UI.
2. **D2 — engine inbound**: `zoho_sync_config` migration, COQL client, generic
   reconcile + poller, config CRUD endpoints, suggested-default map generation.
3. **D3 — engine outbound**: status/field mirror, echo-guard tests.
4. **D4 — realtime**: watch channels + webhook callback + renewal cron.
5. **D5 — UI**: connection card + modules tab (`packages/views/settings`, `packages/core/zoho`).
6. **D6 (optional) — migrate Desk/Projects onto the engine** once stable.

Each phase gated: engine idle unless a connection + ≥1 enabled config exists.

## 4. Security invariants

- Tokens sealed at rest (`AGORA_ZOHO_SECRET_KEY`), never in workspace.settings, never
  in generic responses, never logged; decrypt server-side only (claim/poll paths).
- Config endpoints owner/admin-only, agent actors rejected, mutations audited
  (server-name/module-name detail only) — same contract as `workspace_mcp.go`.
- Webhook callback: public but always-200, constant-time secret, IP rate-limited,
  single-record bounded work.
- Field maps cannot target Agora-internal columns beyond the whitelisted set
  (title/description/status/priority/due_date/assignee) — validated at config write.
