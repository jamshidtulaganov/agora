import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_ZOHO_CONNECTION_STATUS,
  EMPTY_ZOHO_CRM_MODULES,
  EMPTY_ZOHO_SYNC_CONFIG,
  EMPTY_ZOHO_SYNC_CONFIGS,
  EMPTY_ZOHO_USER_BINDING_STATUS,
  ZohoConnectionStatusSchema,
  ZohoCRMModulesResponseSchema,
  ZohoSyncConfigSchema,
  ZohoSyncConfigsResponseSchema,
  ZohoUserBindingStatusSchema,
} from "./types";

// Contract tests for the dynamic-Zoho wire schemas (CLAUDE.md "API Response
// Compatibility"): every case feeds a malformed / drifted payload through the
// same parseWithFallback path the ApiClient uses and asserts the UI-facing
// value degrades instead of throwing.

const endpoint = { endpoint: "test" };

describe("ZohoConnectionStatusSchema", () => {
  it("parses a full status payload", () => {
    const parsed = parseWithFallback(
      {
        configured: true,
        dc: "us",
        client_id: "1000.abc",
        scopes: "ZohoCRM.modules.ALL",
        crm_org_id: "org1",
        desk_org_id: "",
        probe_status: "ok",
        probed_at: "2026-07-01T00:00:00Z",
      },
      ZohoConnectionStatusSchema,
      EMPTY_ZOHO_CONNECTION_STATUS,
      endpoint,
    );
    expect(parsed.configured).toBe(true);
    expect(parsed.dc).toBe("us");
    expect(parsed.probe_status).toBe("ok");
  });

  it("defaults omitted fields (backend omitempty on unconfigured)", () => {
    const parsed = parseWithFallback(
      { configured: false },
      ZohoConnectionStatusSchema,
      EMPTY_ZOHO_CONNECTION_STATUS,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_ZOHO_CONNECTION_STATUS);
  });

  it("falls back on a wrong-typed field", () => {
    const parsed = parseWithFallback(
      { configured: "yes", dc: "us" },
      ZohoConnectionStatusSchema,
      EMPTY_ZOHO_CONNECTION_STATUS,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_ZOHO_CONNECTION_STATUS);
  });

  it("falls back on a non-object body", () => {
    const parsed = parseWithFallback(
      null,
      ZohoConnectionStatusSchema,
      EMPTY_ZOHO_CONNECTION_STATUS,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_ZOHO_CONNECTION_STATUS);
  });
});

describe("ZohoUserBindingStatusSchema", () => {
  it("parses a bound payload and defaults missing probe fields", () => {
    const parsed = parseWithFallback(
      { bound: true, zoho_user_email: "j@x.io" },
      ZohoUserBindingStatusSchema,
      EMPTY_ZOHO_USER_BINDING_STATUS,
      endpoint,
    );
    expect(parsed.bound).toBe(true);
    expect(parsed.zoho_user_email).toBe("j@x.io");
    expect(parsed.probe_status).toBe("");
  });

  it("falls back when bound has the wrong type", () => {
    const parsed = parseWithFallback(
      { bound: 1 },
      ZohoUserBindingStatusSchema,
      EMPTY_ZOHO_USER_BINDING_STATUS,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_ZOHO_USER_BINDING_STATUS);
  });
});

describe("ZohoCRMModulesResponseSchema", () => {
  it("parses modules and defaults missing per-module fields", () => {
    const parsed = parseWithFallback(
      {
        modules: [
          {
            api_name: "Tasks",
            module_name: "Tasks",
            singular_label: "Task",
            plural_label: "Tasks",
            generated_type: "default",
            api_supported: true,
            creatable: true,
          },
          // Older/newer server shape: only api_name present.
          { api_name: "CustomModule34" },
        ],
      },
      ZohoCRMModulesResponseSchema,
      EMPTY_ZOHO_CRM_MODULES,
      endpoint,
    );
    expect(parsed.modules).toHaveLength(2);
    expect(parsed.modules[1]).toEqual({
      api_name: "CustomModule34",
      module_name: "",
      singular_label: "",
      plural_label: "",
      generated_type: "",
      api_supported: false,
      creatable: false,
    });
  });

  it("coerces a null modules array to empty", () => {
    const parsed = parseWithFallback(
      { modules: null },
      ZohoCRMModulesResponseSchema,
      EMPTY_ZOHO_CRM_MODULES,
      endpoint,
    );
    expect(parsed.modules).toEqual([]);
  });

  it("coerces a missing modules key to empty", () => {
    const parsed = parseWithFallback(
      {},
      ZohoCRMModulesResponseSchema,
      EMPTY_ZOHO_CRM_MODULES,
      endpoint,
    );
    expect(parsed.modules).toEqual([]);
  });

  it("drops a drifted entry instead of sinking the whole list", () => {
    const parsed = parseWithFallback(
      {
        modules: [
          { api_name: "Tasks", generated_type: "default" },
          { api_name: 123, generated_type: null },
        ],
      },
      ZohoCRMModulesResponseSchema,
      EMPTY_ZOHO_CRM_MODULES,
      endpoint,
    );
    expect(parsed.modules.map((m) => m.api_name)).toEqual(["Tasks"]);
  });

  it("falls back when modules is not an array", () => {
    const parsed = parseWithFallback(
      { modules: "nope" },
      ZohoCRMModulesResponseSchema,
      EMPTY_ZOHO_CRM_MODULES,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_ZOHO_CRM_MODULES);
  });
});

describe("ZohoSyncConfigsResponseSchema", () => {
  const baseConfig = {
    id: "11111111-1111-1111-1111-111111111111",
    workspace_id: "ws-1",
    connection_id: "conn-1",
    channel: "crm",
    module_api_name: "Tasks",
    enabled: true,
    direction: "both",
    field_map: { title: "Subject" },
    status_map: { in: {}, out: {} },
    filter_coql: "",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
  };

  it("parses a config list; omitted optional fields default", () => {
    const parsed = parseWithFallback(
      { configs: [baseConfig] },
      ZohoSyncConfigsResponseSchema,
      EMPTY_ZOHO_SYNC_CONFIGS,
      endpoint,
    );
    expect(parsed.configs).toHaveLength(1);
    const cfg = parsed.configs[0]!;
    expect(cfg.module_api_name).toBe("Tasks");
    expect(cfg.field_map).toEqual({ title: "Subject" });
    // project_id / cursor are omitempty on the wire.
    expect(cfg.project_id).toBe("");
    expect(cfg.cursor).toBe("");
  });

  it("coerces a null configs array to empty", () => {
    const parsed = parseWithFallback(
      { configs: null },
      ZohoSyncConfigsResponseSchema,
      EMPTY_ZOHO_SYNC_CONFIGS,
      endpoint,
    );
    expect(parsed.configs).toEqual([]);
  });

  it("degrades null/wrong-typed maps to {} instead of dropping the config", () => {
    const parsed = parseWithFallback(
      { configs: [{ ...baseConfig, field_map: null, status_map: "oops" }] },
      ZohoSyncConfigsResponseSchema,
      EMPTY_ZOHO_SYNC_CONFIGS,
      endpoint,
    );
    expect(parsed.configs).toHaveLength(1);
    expect(parsed.configs[0]!.field_map).toEqual({});
    expect(parsed.configs[0]!.status_map).toEqual({});
  });

  it("drops a config without an id (cannot be updated or deleted)", () => {
    const { id: _omit, ...noId } = baseConfig;
    const parsed = parseWithFallback(
      { configs: [noId, baseConfig] },
      ZohoSyncConfigsResponseSchema,
      EMPTY_ZOHO_SYNC_CONFIGS,
      endpoint,
    );
    expect(parsed.configs).toHaveLength(1);
    expect(parsed.configs[0]!.id).toBe(baseConfig.id);
  });

  it("single-config schema (create/update response) tolerates drift", () => {
    const parsed = parseWithFallback(
      { ...baseConfig, enabled: "true", direction: 5 },
      ZohoSyncConfigSchema,
      EMPTY_ZOHO_SYNC_CONFIG,
      endpoint,
    );
    // Per-field catch: wrong-typed scalars degrade to their zero values.
    expect(parsed.enabled).toBe(false);
    expect(parsed.direction).toBe("");
    expect(parsed.module_api_name).toBe("Tasks");
  });
});
