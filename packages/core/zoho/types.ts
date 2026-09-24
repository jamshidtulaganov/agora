// Zoho import-browser types. Mirror the backend payloads in
// server/internal/handler/zohoprojects_endpoints.go (ZohoProjectResponse,
// ZohoImportRequest/Response) and zohosprints_endpoints.go
// (ZohoSprintsProjectResponse, ZohoSprintsImportRequest/Response).
//
// The dynamic-integration section below (workspace connection / CRM
// discovery / sync configs) mirrors server/internal/handler/zoho_connection.go
// and zohodyn_endpoints.go. The per-person Zoho account section at the end
// mirrors the account-scoped `/api/me/zoho` endpoints.

import { z } from "zod";

/** One Zoho Projects project in the configured portal. */
export interface ZohoProject {
  id: string;
  name: string;
  status: string;
}

/** Import selector for Zoho Projects: explicit ids and/or "all". */
export interface ZohoImportRequest {
  project_ids?: string[];
  all?: boolean;
  /** Restrict to a single Zoho user's tasks (zpuid). Empty = all owners. */
  owner_zpuid?: string;
}

/**
 * Tally of a Zoho import run. Asynchronous like the Bitrix import: the server
 * returns 202 with `accepted` (projects enqueued) once the set is resolved;
 * per-task reconcile runs in the background and issues stream in over the
 * websocket, so created/updated/skipped are 0 in this response.
 */
export interface ZohoImportResponse {
  created: number;
  updated: number;
  skipped: number;
  accepted: number;
  errors: string[];
}

/** One Zoho Sprints project in the configured team. */
export interface ZohoSprintsProject {
  id: string;
  name: string;
}

/** Import selector for Zoho Sprints. */
export interface ZohoSprintsImportRequest {
  project_ids?: string[];
  all?: boolean;
}

/** Tally of a Zoho Sprints import run (asynchronous, like Projects). */
export interface ZohoSprintsImportResponse {
  accepted: number;
  errors: string[];
}

// --- Dynamic Zoho integration (docs/zoho-dynamic-integration.md) -----------
//
// Wire schemas are deliberately lenient (`.loose()`, per-field defaults, null
// arrays coerced to []) per CLAUDE.md "API Response Compatibility": an older
// or newer server must degrade the UI to an empty/"not configured" state,
// never white-screen it. TS interfaces stay strict; parseWithFallback anchors
// the runtime value to them via the typed fallback.

/** Workspace Zoho connection status (`GET /api/workspaces/{id}/zoho-connection`).
 * Member-visible; never carries secret material. */
export interface ZohoConnectionStatus {
  configured: boolean;
  dc: string;
  client_id: string;
  scopes: string;
  crm_org_id: string;
  desk_org_id: string;
  probe_status: string;
  probed_at: string;
}

export const ZohoConnectionStatusSchema = z
  .object({
    configured: z.boolean().default(false),
    dc: z.string().default(""),
    client_id: z.string().default(""),
    scopes: z.string().default(""),
    crm_org_id: z.string().default(""),
    desk_org_id: z.string().default(""),
    probe_status: z.string().default(""),
    probed_at: z.string().default(""),
  })
  .loose();

export const EMPTY_ZOHO_CONNECTION_STATUS: ZohoConnectionStatus = {
  configured: false,
  dc: "",
  client_id: "",
  scopes: "",
  crm_org_id: "",
  desk_org_id: "",
  probe_status: "",
  probed_at: "",
};

/** Zoho data centers accepted by the backend (zohocrm.KnownDC). */
export const ZOHO_DCS = ["us", "eu", "in", "au", "jp", "sa", "ca"] as const;

/** Body of `PUT /api/workspaces/{id}/zoho-connection` (owner/admin). */
export interface PutZohoConnectionRequest {
  dc: string;
  client_id: string;
  client_secret: string;
  refresh_token: string;
  scopes?: string;
  crm_org_id?: string;
  desk_org_id?: string;
  projects_portal_id?: string;
  sprints_team_id?: string;
}

/** One discovered CRM module (`GET /api/workspaces/{id}/zoho/crm/modules`). */
export interface ZohoCRMModule {
  api_name: string;
  module_name: string;
  singular_label: string;
  plural_label: string;
  generated_type: string;
  api_supported: boolean;
  creatable: boolean;
}

// Every field defaulted so a single drifted entry degrades to a skippable
// blank instead of sinking the whole list into the fallback (the
// "empty list but API returns data" footgun). Entries without an api_name
// are unusable for sync config and are filtered out by the transform.
const ZohoCRMModuleSchema = z
  .object({
    api_name: z.string().catch(""),
    module_name: z.string().catch(""),
    singular_label: z.string().catch(""),
    plural_label: z.string().catch(""),
    generated_type: z.string().catch(""),
    api_supported: z.boolean().catch(false),
    creatable: z.boolean().catch(false),
  })
  .loose();

export const ZohoCRMModulesResponseSchema = z
  .object({
    modules: z
      .array(ZohoCRMModuleSchema)
      .nullish()
      .transform((v) => (v ?? []).filter((m) => m.api_name !== "")),
  })
  .loose();

export const EMPTY_ZOHO_CRM_MODULES: { modules: ZohoCRMModule[] } = {
  modules: [],
};

/** One selectable value of a CRM picklist field. */
export interface ZohoCRMPicklistValue {
  display_value: string;
  actual_value: string;
}

/** One CRM module field (`GET /api/workspaces/{id}/zoho/crm/fields?module=X`). */
export interface ZohoCRMField {
  api_name: string;
  field_label: string;
  data_type: string;
  read_only: boolean;
  system_mandatory: boolean;
  pick_list_values: ZohoCRMPicklistValue[];
}

const ZohoCRMFieldSchema = z
  .object({
    api_name: z.string().catch(""),
    field_label: z.string().catch(""),
    data_type: z.string().catch(""),
    read_only: z.boolean().catch(false),
    system_mandatory: z.boolean().catch(false),
    pick_list_values: z
      .array(
        z
          .object({
            display_value: z.string().catch(""),
            actual_value: z.string().catch(""),
          })
          .loose(),
      )
      .nullish()
      .transform((v) => v ?? [])
      .catch([]),
  })
  .loose();

export interface ZohoCRMFieldsResponse {
  module: string;
  fields: ZohoCRMField[];
}

export const ZohoCRMFieldsResponseSchema = z
  .object({
    module: z.string().default(""),
    fields: z
      .array(ZohoCRMFieldSchema)
      .nullish()
      .transform((v) => (v ?? []).filter((f) => f.api_name !== "")),
  })
  .loose();

export const EMPTY_ZOHO_CRM_FIELDS: ZohoCRMFieldsResponse = {
  module: "",
  fields: [],
};

/** Sync direction for a per-module config. The wire value stays a plain
 * string in `ZohoSyncConfig` so an unknown server-side value degrades
 * instead of failing the parse. */
export type ZohoSyncDirection = "in" | "out" | "both";

/** One per-module sync config (`/api/workspaces/{id}/zoho/sync-configs`). */
export interface ZohoSyncConfig {
  id: string;
  workspace_id: string;
  connection_id: string;
  channel: string;
  module_api_name: string;
  project_id: string;
  enabled: boolean;
  direction: string;
  field_map: Record<string, unknown>;
  status_map: Record<string, unknown>;
  filter_coql: string;
  cursor: string;
  created_at: string;
  updated_at: string;
}

// field_map / status_map are freeform JSON objects on the wire; a null or
// non-object value degrades to {} rather than failing the whole config list.
const zohoJsonObject = z
  .record(z.string(), z.unknown())
  .nullish()
  .transform((v) => v ?? {})
  .catch({});

export const ZohoSyncConfigSchema = z
  .object({
    id: z.string().catch(""),
    workspace_id: z.string().catch(""),
    connection_id: z.string().catch(""),
    channel: z.string().catch(""),
    module_api_name: z.string().catch(""),
    project_id: z.string().catch(""),
    enabled: z.boolean().catch(false),
    direction: z.string().catch(""),
    field_map: zohoJsonObject,
    status_map: zohoJsonObject,
    filter_coql: z.string().catch(""),
    cursor: z.string().catch(""),
    created_at: z.string().catch(""),
    updated_at: z.string().catch(""),
  })
  .loose();

export const EMPTY_ZOHO_SYNC_CONFIG: ZohoSyncConfig = {
  id: "",
  workspace_id: "",
  connection_id: "",
  channel: "",
  module_api_name: "",
  project_id: "",
  enabled: false,
  direction: "",
  field_map: {},
  status_map: {},
  filter_coql: "",
  cursor: "",
  created_at: "",
  updated_at: "",
};

export const ZohoSyncConfigsResponseSchema = z
  .object({
    configs: z
      .array(ZohoSyncConfigSchema)
      .nullish()
      // A config without an id cannot be updated or deleted — drop it
      // rather than rendering a dead row.
      .transform((v) => (v ?? []).filter((c) => c.id !== "")),
  })
  .loose();

export const EMPTY_ZOHO_SYNC_CONFIGS: { configs: ZohoSyncConfig[] } = {
  configs: [],
};

/** Body of `POST /api/workspaces/{id}/zoho/sync-configs`. */
export interface CreateZohoSyncConfigRequest {
  module_api_name: string;
  project_id?: string;
  enabled?: boolean;
  direction?: ZohoSyncDirection;
  field_map?: Record<string, unknown>;
  status_map?: Record<string, unknown>;
  filter_coql?: string;
}

/** Body of `PUT /api/workspaces/{id}/zoho/sync-configs/{configId}` —
 * partial update; module_api_name is immutable on the server. */
export type UpdateZohoSyncConfigRequest = Omit<
  CreateZohoSyncConfigRequest,
  "module_api_name"
>;

// --- Personal Zoho account (`/api/me/zoho`) ----------------------------------
//
// One Zoho account per person, connected once through Zoho's sign-in page and
// used in every workspace. Read-only: the person's own Zoho role decides what
// Agora can see. Account-scoped, so nothing here carries a workspace id.

/** Wire status of a connected account. Anything the server sends that is not
 * "connected" is treated as "reconnect" (enum-drift downgrades, not crashes). */
export type ZohoAccountStatus = "connected" | "reconnect";

/** The caller's own Zoho account (`GET /api/me/zoho`). */
export interface ZohoAccount {
  /** False when the server has no Zoho sign-in client configured. */
  available: boolean;
  connected: boolean;
  status: ZohoAccountStatus;
  email: string;
  name: string;
  crm_role: string;
  crm_profile: string;
  desk_departments: string[];
  checked_at: string | null;
}

export const EMPTY_ZOHO_ACCOUNT: ZohoAccount = {
  available: false,
  connected: false,
  status: "reconnect",
  email: "",
  name: "",
  crm_role: "",
  crm_profile: "",
  desk_departments: [],
  checked_at: null,
};

// Per-field `.catch` so one drifted field degrades on its own instead of
// sinking the whole payload into the fallback. A non-object body still falls
// back to EMPTY_ZOHO_ACCOUNT (hides the Connect button) via parseWithFallback.
export const ZohoAccountSchema = z
  .object({
    available: z.boolean().catch(false),
    connected: z.boolean().catch(false),
    status: z.unknown().optional(),
    email: z.string().catch(""),
    name: z.string().catch(""),
    crm_role: z.string().catch(""),
    crm_profile: z.string().catch(""),
    desk_departments: z
      .array(z.unknown())
      .nullish()
      .transform((v) =>
        (v ?? []).filter((d): d is string => typeof d === "string" && d !== ""),
      )
      .catch([]),
    checked_at: z
      .string()
      .nullish()
      .transform((v) => (v ? v : null))
      .catch(null),
  })
  .loose()
  .transform(({ status, ...rest }) => ({
    ...rest,
    status: normalizeZohoAccountStatus(status, rest.connected),
  }));

// An absent status on a connected account trusts `connected`; any present
// value other than "connected" (a new server-side state, a wrong type) means
// the connection needs attention.
function normalizeZohoAccountStatus(
  status: unknown,
  connected: boolean,
): ZohoAccountStatus {
  if (status === undefined || status === null) {
    return connected ? "connected" : "reconnect";
  }
  return status === "connected" ? "connected" : "reconnect";
}

/** What the account card should show. */
export type ZohoAccountState =
  | "unavailable"
  | "not_connected"
  | "connected"
  | "reconnect";

/** Collapse the account payload into one UI state. A connected account is
 * shown even when the server can no longer start new connections, so the
 * person can still see and remove it. */
export function zohoAccountState(
  account: ZohoAccount | undefined,
): ZohoAccountState {
  if (account?.connected === true) {
    return account.status === "connected" ? "connected" : "reconnect";
  }
  return account?.available === true ? "not_connected" : "unavailable";
}

/** Response of `POST /api/me/zoho/connect`: the Zoho sign-in page to open. */
export interface ZohoConnectResponse {
  url: string;
}

export const ZohoConnectResponseSchema = z
  .object({ url: z.string().catch("") })
  .loose();

export const EMPTY_ZOHO_CONNECT_RESPONSE: ZohoConnectResponse = { url: "" };
