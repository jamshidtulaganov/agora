import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_ZOHO_ACCOUNT,
  EMPTY_ZOHO_CONNECT_RESPONSE,
  EMPTY_ZOHO_CONNECTION_STATUS,
  EMPTY_ZOHO_CRM_MODULES,
  EMPTY_ZOHO_SYNC_CONFIG,
  EMPTY_ZOHO_SYNC_CONFIGS,
  ZohoAccountSchema,
  ZohoConnectResponseSchema,
  ZohoConnectionStatusSchema,
  ZohoCRMModulesResponseSchema,
  ZohoSyncConfigSchema,
  ZohoSyncConfigsResponseSchema,
  zohoAccountState,
  type ZohoAccount,
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

describe("ZohoAccountSchema", () => {
  const parse = (raw: unknown): ZohoAccount =>
    parseWithFallback(raw, ZohoAccountSchema, EMPTY_ZOHO_ACCOUNT, endpoint);

  it("parses a full connected payload", () => {
    expect(
      parse({
        available: true,
        connected: true,
        status: "connected",
        email: "shohruh.a@octanefuel.com",
        name: "Shohruh A.",
        crm_role: "Collections Agent",
        crm_profile: "Standard",
        desk_departments: ["Collections"],
        checked_at: "2026-09-25T10:00:00Z",
      }),
    ).toEqual({
      available: true,
      connected: true,
      status: "connected",
      email: "shohruh.a@octanefuel.com",
      name: "Shohruh A.",
      crm_role: "Collections Agent",
      crm_profile: "Standard",
      desk_departments: ["Collections"],
      checked_at: "2026-09-25T10:00:00Z",
    });
  });

  it("defaults every field of the not-connected payload", () => {
    const parsed = parse({ available: true, connected: false });
    expect(parsed).toEqual({ ...EMPTY_ZOHO_ACCOUNT, available: true });
    expect(zohoAccountState(parsed)).toBe("not_connected");
  });

  it("coerces a null desk_departments to []", () => {
    const parsed = parse({
      available: true,
      connected: true,
      status: "connected",
      desk_departments: null,
    });
    expect(parsed.desk_departments).toEqual([]);
    expect(parsed.connected).toBe(true);
  });

  it("drops non-string and empty department entries", () => {
    const parsed = parse({
      available: true,
      connected: true,
      desk_departments: ["Collections", 7, "", null, "Billing"],
    });
    expect(parsed.desk_departments).toEqual(["Collections", "Billing"]);
  });

  it("degrades wrong-typed fields one by one instead of dropping the payload", () => {
    const parsed = parse({
      available: true,
      connected: true,
      status: "connected",
      email: 42,
      crm_role: { name: "Agent" },
      crm_profile: null,
      desk_departments: "Collections",
      checked_at: 1727258400,
    });
    expect(parsed).toEqual({
      ...EMPTY_ZOHO_ACCOUNT,
      available: true,
      connected: true,
      status: "connected",
    });
  });

  it("treats a wrong-typed connected flag as not connected", () => {
    const parsed = parse({ available: true, connected: "yes" });
    expect(parsed.connected).toBe(false);
    expect(zohoAccountState(parsed)).toBe("not_connected");
  });

  it("treats a wrong-typed available flag as unavailable", () => {
    const parsed = parse({ available: "true", connected: false });
    expect(zohoAccountState(parsed)).toBe("unavailable");
  });

  it("maps an unknown status to reconnect", () => {
    const parsed = parse({ available: true, connected: true, status: "expired" });
    expect(parsed.status).toBe("reconnect");
    expect(zohoAccountState(parsed)).toBe("reconnect");
  });

  it("maps a wrong-typed status to reconnect", () => {
    const parsed = parse({ available: true, connected: true, status: 1 });
    expect(parsed.status).toBe("reconnect");
  });

  it("trusts connected when status is absent", () => {
    const parsed = parse({ available: true, connected: true, email: "a@b.io" });
    expect(parsed.status).toBe("connected");
  });

  it("falls back to unavailable on a non-object body", () => {
    expect(parse(null)).toEqual(EMPTY_ZOHO_ACCOUNT);
    expect(parse("oops")).toEqual(EMPTY_ZOHO_ACCOUNT);
    expect(parse([])).toEqual(EMPTY_ZOHO_ACCOUNT);
    expect(zohoAccountState(parse(null))).toBe("unavailable");
  });
});

describe("zohoAccountState", () => {
  it("is unavailable while nothing is known", () => {
    expect(zohoAccountState(undefined)).toBe("unavailable");
  });

  it("keeps showing a connected account when new connections are off", () => {
    expect(
      zohoAccountState({
        ...EMPTY_ZOHO_ACCOUNT,
        available: false,
        connected: true,
        status: "connected",
      }),
    ).toBe("connected");
  });
});

describe("ZohoConnectResponseSchema", () => {
  it("parses the url", () => {
    expect(
      parseWithFallback(
        { url: "https://accounts.zoho.com/oauth/v2/auth?x=1" },
        ZohoConnectResponseSchema,
        EMPTY_ZOHO_CONNECT_RESPONSE,
        endpoint,
      ).url,
    ).toBe("https://accounts.zoho.com/oauth/v2/auth?x=1");
  });

  it("degrades a missing or wrong-typed url to empty", () => {
    for (const raw of [{}, { url: null }, { url: 5 }, null]) {
      expect(
        parseWithFallback(
          raw,
          ZohoConnectResponseSchema,
          EMPTY_ZOHO_CONNECT_RESPONSE,
          endpoint,
        ).url,
      ).toBe("");
    }
  });
});
