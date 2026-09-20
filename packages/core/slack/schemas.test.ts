import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_SLACK_CHANNELS,
  EMPTY_SLACK_INSTALLATIONS,
  EMPTY_SLACK_ROUTES,
  ListSlackChannelsSchema,
  ListSlackInstallationsSchema,
  ListSlackRoutesSchema,
  SlackChannelRouteSchema,
  SlackInstallationSchema,
} from "./schemas";

// The Slack settings panel ships inside a desktop build that will outlive the
// server it was compiled against. Every case below is a response shape that
// WILL eventually arrive, and every assertion is "the panel still renders"
// rather than "the parse succeeded".

const opts = { endpoint: "test" };

describe("ListSlackInstallationsSchema", () => {
  it("parses a well-formed response", () => {
    const parsed = ListSlackInstallationsSchema.parse({
      installations: [
        {
          id: "i1",
          workspace_id: "w1",
          team_id: "T1",
          team_name: "Acme",
          app_id: "A1",
          bot_user_id: "U1",
          scopes: ["chat:write", "links:read"],
          installer_user_id: "u1",
          status: "active",
          installed_at: "2026-09-19T09:00:00Z",
          updated_at: "2026-09-19T09:00:00Z",
        },
      ],
      configured: true,
    });
    expect(parsed.installations).toHaveLength(1);
    expect(parsed.configured).toBe(true);
  });

  it("defaults `configured` to false when the server omits it", () => {
    // The opposite default would show a Connect button that dies at the OAuth
    // exchange, after an admin has already granted scopes in Slack.
    const parsed = ListSlackInstallationsSchema.parse({ installations: [] });
    expect(parsed.configured).toBe(false);
  });

  it("survives a missing installations array", () => {
    const parsed = ListSlackInstallationsSchema.parse({ configured: true });
    expect(parsed.installations).toEqual([]);
  });

  it("downgrades a null array instead of throwing", () => {
    const parsed = ListSlackInstallationsSchema.parse({ installations: null, configured: true });
    expect(parsed.installations).toEqual([]);
  });

  it("keeps an unknown status so the row still renders", () => {
    const parsed = SlackInstallationSchema.parse({ id: "i1", status: "suspended_by_slack" });
    expect(parsed.status).toBe("suspended_by_slack");
  });

  it("fills every missing field rather than dropping the installation", () => {
    const parsed = SlackInstallationSchema.parse({ id: "i1" });
    expect(parsed.team_name).toBe("");
    expect(parsed.scopes).toEqual([]);
  });

  it("falls back whole when the body is not an object at all", () => {
    expect(parseWithFallback("nope", ListSlackInstallationsSchema, EMPTY_SLACK_INSTALLATIONS, opts))
      .toEqual(EMPTY_SLACK_INSTALLATIONS);
    expect(parseWithFallback(null, ListSlackInstallationsSchema, EMPTY_SLACK_INSTALLATIONS, opts))
      .toEqual(EMPTY_SLACK_INSTALLATIONS);
  });

  it("ignores fields a newer server added", () => {
    const parsed = ListSlackInstallationsSchema.parse({
      installations: [],
      configured: true,
      enterprise_install: true,
    });
    expect(parsed.configured).toBe(true);
  });
});

describe("ListSlackRoutesSchema", () => {
  it("preserves an event kind this client has never heard of", () => {
    // Enum drift downgrades: the editor renders the unknown kind unchecked and
    // keeps it on save, so an older client editing one checkbox does not
    // silently delete a newer server's route.
    const parsed = SlackChannelRouteSchema.parse({
      id: "r1",
      channel_id: "C1",
      events: ["failed", "deploy_recorded"],
    });
    expect(parsed.events).toEqual(["failed", "deploy_recorded"]);
  });

  it("defaults the vocabulary when the server does not ship one", () => {
    const parsed = parseWithFallback({ routes: [] }, ListSlackRoutesSchema, EMPTY_SLACK_ROUTES, opts);
    expect(parsed.routes).toEqual([]);
    expect(parsed.available_events).toEqual([]);
  });

  it("falls back to the quiet default set when the response is unusable", () => {
    const parsed = parseWithFallback(42, ListSlackRoutesSchema, EMPTY_SLACK_ROUTES, opts);
    expect(parsed.default_events).toEqual(["failed", "qa_verdict", "review_verdict"]);
  });

  it("treats a route with a wrong-typed events field as having none", () => {
    const parsed = SlackChannelRouteSchema.parse({ id: "r1", events: "failed" });
    expect(parsed.events).toEqual([]);
  });

  it("defaults `enabled` to true so a route is not silently muted", () => {
    const parsed = SlackChannelRouteSchema.parse({ id: "r1" });
    expect(parsed.enabled).toBe(true);
  });
});

describe("ListSlackChannelsSchema", () => {
  it("parses the picker page", () => {
    const parsed = ListSlackChannelsSchema.parse({
      channels: [{ id: "C1", name: "eng", is_private: false, is_member: true }],
      next_cursor: "dXNlcjpVMDYx",
      installation_id: "i1",
    });
    expect(parsed.channels[0]?.name).toBe("eng");
    expect(parsed.next_cursor).toBe("dXNlcjpVMDYx");
  });

  it("defaults is_member to false so the UI can say 'invite the app first'", () => {
    const parsed = ListSlackChannelsSchema.parse({ channels: [{ id: "C1", name: "eng" }] });
    expect(parsed.channels[0]?.is_member).toBe(false);
  });

  it("falls back on a malformed body", () => {
    expect(parseWithFallback({ channels: "many" }, ListSlackChannelsSchema, EMPTY_SLACK_CHANNELS, opts))
      .toEqual(EMPTY_SLACK_CHANNELS);
  });
});
