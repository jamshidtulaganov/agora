import { describe, expect, it } from "vitest";
import {
  AppConfigSchema,
  EMPTY_APP_CONFIG,
  DashboardAgentRunTimeListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  DeployEventSchema,
  deployEnvironmentRequiresHuman,
  DuplicateIssueErrorBodySchema,
  EscalationSchema,
  EscalationListSchema,
  EMPTY_ESCALATION,
  EMPTY_ESCALATION_LIST,
  EMPTY_DEPLOY_EVENTS,
  EMPTY_FIGMA_CREDENTIAL_STATUS,
  EMPTY_LIST_TEST_CASES,
  EMPTY_TEST_CASE,
  EMPTY_AUTOPILOT_TELEGRAM_DESTINATION,
  EMPTY_EXTERNAL_IDENTITY_LINKS,
  EMPTY_TELEGRAM_INSTALLATIONS,
  EMPTY_TELEGRAM_LINK_START,
  EMPTY_USER,
  FigmaCredentialStatusSchema,
  AutopilotTelegramDestinationSchema,
  ListExternalIdentityLinksSchema,
  ListTelegramInstallationsSchema,
  TelegramLinkStartSchema,
  McpCredentialStatusSchema,
  McpCredentialListSchema,
  EMPTY_MCP_CREDENTIAL_STATUS,
  EMPTY_MCP_CREDENTIAL_LIST,
  IssueDeployEventsResponseSchema,
  StaleIssuesResponseSchema,
  EMPTY_STALE_ISSUES,
  OrchestrationRunSchema,
  ListIssuesResponseSchema,
  ListTestCasesResponseSchema,
  TestCaseSchema,
  parseDeployEnvironments,
  QAEvidenceSchema,
  QAVerdictsResponseSchema,
  ReviewVerdictSchema,
  EMPTY_REVIEW_VERDICT,
  ReviewDecisionResponseSchema,
  EMPTY_REVIEW_DECISION,
  ReleaseIntegrationListSchema,
  EMPTY_RELEASE_INTEGRATIONS,
  TestCaseRunsResponseSchema,
  EMPTY_TEST_CASE_RUNS,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  SquadListSchema,
  SquadSchema,
  UserSchema,
  AssistantSessionSchema,
  AssistantRunSchema,
  AssistantRunListSchema,
  EMPTY_ASSISTANT_RUN,
  AssistantSessionListSchema,
  EMPTY_ASSISTANT_SESSION,
  EMPTY_ASSISTANT_SESSION_LIST,
  AssistantMessageSchema,
  AssistantMessageListSchema,
  EMPTY_ASSISTANT_MESSAGE_LIST,
  AssistantAvailabilitySchema,
  EMPTY_ASSISTANT_AVAILABILITY,
  AssistantOperationDecisionSchema,
  AssistantOperationSchema,
  AssistantOperationListSchema,
  EMPTY_ASSISTANT_OPERATION,
  EMPTY_ASSISTANT_OPERATION_DECISION,
  AssistantArtifactSchema,
  PinnedReportSummarySchema,
  PinnedReportSchema,
  ProjectReportsResponseSchema,
  ReportScheduleSchema,
  ReportScheduleResponseSchema,
  EMPTY_REPORT_SCHEDULE_RESPONSE,
  EMPTY_PINNED_REPORT_LIST,
  EMPTY_PINNED_REPORT,
  ReportPinSchema,
  EMPTY_ASSISTANT_ARTIFACT,
} from "./schemas";
import {
  EMPTY_DAEMON_BROWSE_TARGET,
  EMPTY_FS_LIST,
  EMPTY_ISSUE_BROWSER,
  EMPTY_WORKSPACE_LABS,
  EMPTY_PROJECT_DEV_SERVERS,
  DaemonBrowseTargetSchema,
  FsListResponseSchema,
  IssueBrowserResponseSchema,
  WorkspaceLabsSchema,
  ProjectDevServersSchema,
  DecisionQueueResponseSchema,
  EMPTY_DECISION_QUEUE,
  RiskMapResponseSchema,
  EMPTY_RISK_MAP,
  IssueChangesResponseSchema,
  EMPTY_ISSUE_CHANGES,
  IssueChangePatchResponseSchema,
  EMPTY_ISSUE_CHANGE_PATCH,
} from "./schemas";
import { parseWithFallback } from "./schema";
import type { ListTestCasesResponse } from "../types/test-case";

const baseIssue = {
  id: "11111111-1111-1111-1111-111111111111",
  workspace_id: "ws-1",
  number: 1,
  identifier: "MUL-1",
  title: "Test",
  description: null,
  status: "todo",
  priority: "medium",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("IssueSchema (via ListIssuesResponseSchema)", () => {
  it("accepts a primitive metadata KV map", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          metadata: { pipeline_status: "waiting", pr_number: 3, is_blocked: true },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({
      pipeline_status: "waiting",
      pr_number: 3,
      is_blocked: true,
    });
  });

  it("defaults metadata to {} when the server omits it (older backend)", () => {
    const { metadata: _omit, ...issueWithoutMetadata } = baseIssue;
    const payload = { issues: [issueWithoutMetadata], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({});
  });

  // Metadata is a freeform JSON blob: the server stores rich shapes there (the
  // Bitrix sync keeps ARRAY values for incremental dedup). A scalar-only schema
  // rejected those and — because parseWithFallback fails the WHOLE response —
  // collapsed the entire issue list to empty for any imported issue. Metadata
  // must accept arbitrary values so a new shape can never white-screen the board.
  it("accepts metadata with array values (Bitrix synced-id sets)", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          metadata: {
            bitrix_task_id: "54657",
            bitrix_synced_comment_ids: ["chat-2129963", "chat-2129967"],
            bitrix_synced_file_ids: ["19683", "19685"],
          },
        },
      ],
      total: 1,
    };
    const res = ListIssuesResponseSchema.safeParse(payload);
    expect(res.success).toBe(true);
    expect(res.success && res.data.issues).toHaveLength(1);
    expect(res.success && res.data.issues[0]?.metadata.bitrix_synced_comment_ids).toEqual([
      "chat-2129963",
      "chat-2129967",
    ]);
  });

  it("accepts metadata with a nested object value", () => {
    const payload = {
      issues: [{ ...baseIssue, metadata: { nested: { x: 1 } } }],
      total: 1,
    };
    expect(ListIssuesResponseSchema.safeParse(payload).success).toBe(true);
  });
});

// The duplicate-issue branch in create-issue.tsx feeds ApiError.body
// (typed as `unknown`) through this schema. Any future server drift that
// loses the contract MUST fail the parse so the UI falls back to a normal
// error toast instead of rendering an empty / partial duplicate card.
describe("DuplicateIssueErrorBodySchema", () => {
  const valid = {
    code: "active_duplicate_issue",
    error: "An active issue with this title already exists: MUL-12 – Login bug",
    issue: {
      id: "11111111-1111-1111-1111-111111111111",
      identifier: "MUL-12",
      title: "Login bug",
    },
  };

  it("accepts a well-formed body", () => {
    expect(DuplicateIssueErrorBodySchema.safeParse(valid).success).toBe(true);
  });

  it("accepts unknown extra fields via .loose()", () => {
    const forwardCompat = {
      ...valid,
      hint: "Try a different title",
      issue: { ...valid.issue, workspace_id: "ws-1", status: "todo" },
    };
    expect(DuplicateIssueErrorBodySchema.safeParse(forwardCompat).success).toBe(true);
  });

  it("rejects a renamed code (so renames degrade to the generic toast)", () => {
    const renamed = { ...valid, code: "duplicate_issue" };
    expect(DuplicateIssueErrorBodySchema.safeParse(renamed).success).toBe(false);
  });

  it("rejects a missing issue object", () => {
    const { issue: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(false);
  });

  it("rejects a non-string issue.id", () => {
    const broken = { ...valid, issue: { ...valid.issue, id: 42 } };
    expect(DuplicateIssueErrorBodySchema.safeParse(broken).success).toBe(false);
  });

  it("accepts a missing error field (it is optional)", () => {
    const { error: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(true);
  });
});

// `user.timezone` (Viewing tz) was added in the timezone-architecture RFC.
// A desktop build older than the server — or a server predating the
// `user.timezone` migration — will return a `/api/me` body with no
// `timezone` key. The schema must not fail closed on that: the field
// defaults to `null`, which the frontend resolves to the browser-detected
// tz at render time.
describe("UserSchema timezone drift", () => {
  const base = {
    id: "11111111-1111-1111-1111-111111111111",
    name: "Ada",
    email: "ada@example.com",
  };

  it("defaults timezone to null when the field is absent", () => {
    const parsed = UserSchema.parse(base);
    expect(parsed.timezone).toBe(null);
  });

  it("preserves an explicit IANA timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: "Asia/Tokyo" });
    expect(parsed.timezone).toBe("Asia/Tokyo");
  });

  it("accepts an explicit null timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: null });
    expect(parsed.timezone).toBe(null);
  });

  // Wrong-type drift: a future server bug sending `timezone` as a number
  // must not throw into the UI. parseWithFallback degrades the whole user
  // object to the explicit fallback (EMPTY_USER) so /api/me callers keep a
  // valid shape instead of white-screening.
  it("falls back to EMPTY_USER when timezone is the wrong type", () => {
    const parsed = parseWithFallback(
      { ...base, timezone: 42 },
      UserSchema,
      EMPTY_USER,
      { endpoint: "GET /api/me" },
    );
    expect(parsed).toBe(EMPTY_USER);
  });
});

// `user.hidden_nav` (per-user sidebar customization) is newer than every
// installed desktop build. An older server omits the key entirely, and a
// drifted one could send a non-array — neither may blank the user object,
// because "no customization" is the correct degraded answer.
describe("UserSchema hidden_nav drift", () => {
  const base = {
    id: "11111111-1111-1111-1111-111111111111",
    name: "Ada",
    email: "ada@example.com",
  };

  it("defaults hidden_nav to [] when the field is absent", () => {
    expect(UserSchema.parse(base).hidden_nav).toEqual([]);
  });

  it("preserves an explicit hidden list", () => {
    const parsed = UserSchema.parse({ ...base, hidden_nav: ["usage", "mcp"] });
    expect(parsed.hidden_nav).toEqual(["usage", "mcp"]);
  });

  it("degrades a wrong-typed hidden_nav to [] without failing the parse", () => {
    const parsed = parseWithFallback(
      { ...base, hidden_nav: "usage" },
      UserSchema,
      EMPTY_USER,
      { endpoint: "GET /api/me" },
    );
    expect(parsed).not.toBe(EMPTY_USER);
    expect(parsed.hidden_nav).toEqual([]);
    expect(parsed.id).toBe(base.id);
  });

  it("degrades a null hidden_nav to []", () => {
    expect(UserSchema.parse({ ...base, hidden_nav: null }).hidden_nav).toEqual([]);
  });

  // hidden_nav_customized decides whether a workspace's team sidebar applies;
  // "never customized" is the safe reading for anything that isn't a boolean.
  it("defaults hidden_nav_customized to false when absent", () => {
    expect(UserSchema.parse(base).hidden_nav_customized).toBe(false);
  });

  it("preserves hidden_nav_customized: true", () => {
    expect(UserSchema.parse({ ...base, hidden_nav_customized: true }).hidden_nav_customized).toBe(true);
  });

  it.each([null, "true", 1])("degrades hidden_nav_customized %j to false without failing the parse", (value) => {
    const parsed = parseWithFallback(
      { ...base, hidden_nav_customized: value },
      UserSchema,
      EMPTY_USER,
      { endpoint: "GET /api/me" },
    );
    expect(parsed).not.toBe(EMPTY_USER);
    expect(parsed.hidden_nav_customized).toBe(false);
  });
});

describe("SquadListSchema member preview drift", () => {
  const baseSquad = {
    id: "squad-1",
    workspace_id: "ws-1",
    name: "Frontend Squad",
    description: "",
    instructions: "",
    avatar_url: null,
    leader_id: "agent-1",
    creator_id: "user-1",
    created_at: "2026-05-01T00:00:00Z",
    updated_at: "2026-05-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };

  it("defaults preview fields when an older backend omits them", () => {
    const parsed = SquadListSchema.parse([baseSquad]);
    expect(parsed[0]?.model_routing_mode).toBe("pinned");
    expect(parsed[0]?.member_count).toBe(0);
    expect(parsed[0]?.member_preview).toEqual([]);
  });

  it("defaults preview fields on a single squad response", () => {
    const parsed = SquadSchema.parse(baseSquad);
    expect(parsed.model_routing_mode).toBe("pinned");
    expect(parsed.member_count).toBe(0);
    expect(parsed.member_preview).toEqual([]);
  });

  it("preserves lightweight member preview rows", () => {
    const parsed = SquadListSchema.parse([
      {
        ...baseSquad,
        member_count: 2,
        member_preview: [
          { member_type: "agent", member_id: "agent-1", role: "leader" },
          { member_type: "member", member_id: "user-2", role: "member" },
        ],
      },
    ]);
    expect(parsed[0]?.member_count).toBe(2);
    expect(parsed[0]?.member_preview).toHaveLength(2);
    expect(parsed[0]?.member_preview?.[0]?.role).toBe("leader");
  });
});

// The workspace dashboard and runtime-detail pages were re-pointed at the
// unified `task_usage_hourly` rollup. Every numeric field drives chart /
// KPI math, and string keys (date / agent_id / model) bucket the series.
// The contract these schemas must hold: a row missing a field degrades
// that field to a sane default rather than dropping the WHOLE array to
// the `[]` fallback — one drifted row must not blank the entire chart.
describe("dashboard + runtime usage schema drift", () => {
  it("coerces a missing numeric field to 0 instead of dropping the array", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { date: "2026-05-19", model: "claude-opus-4-7", input_tokens: 100 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.output_tokens).toBe(0);
    expect(parsed[0]?.cache_read_tokens).toBe(0);
    expect(parsed[0]?.cache_write_tokens).toBe(0);
  });

  it("coerces a missing date key to \"\" so the rest of the series survives", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 5 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.date).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the agent-runtime panel", () => {
    const parsed = DashboardAgentRunTimeListSchema.parse([
      { total_seconds: 42, task_count: 3, failed_count: 0 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the usage-by-agent panel", () => {
    const parsed = DashboardUsageByAgentListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 7 },
    ]);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces missing fields on every runtime usage schema", () => {
    expect(RuntimeUsageListSchema.parse([{ date: "2026-05-19" }])[0]?.input_tokens).toBe(0);
    expect(RuntimeHourlyActivityListSchema.parse([{ hour: 9 }])[0]?.count).toBe(0);
    expect(RuntimeUsageByAgentListSchema.parse([{ model: "x" }])[0]?.agent_id).toBe("");
    expect(RuntimeUsageByHourListSchema.parse([{ hour: 9 }])[0]?.model).toBe("");
  });

  it("rejects a non-array body so parseWithFallback can return its fallback", () => {
    expect(DashboardUsageDailyListSchema.safeParse(null).success).toBe(false);
    expect(RuntimeUsageListSchema.safeParse({ rows: [] }).success).toBe(false);
  });

  it("keeps unknown server-side fields via .loose()", () => {
    const parsed = RuntimeUsageListSchema.parse([
      { date: "2026-05-19", region: "us-east" },
    ]);
    expect((parsed[0] as Record<string, unknown>).region).toBe("us-east");
  });
});

describe("AppConfigSchema (integration capability flags)", () => {
  it("parses the bitrix/zoho/lark flags when present", () => {
    const parsed = AppConfigSchema.parse({
      cdn_domain: "cdn.example.com",
      allow_signup: true,
      bitrix_enabled: true,
      zoho_enabled: true,
      lark_enabled: true,
    });
    expect(parsed.bitrix_enabled).toBe(true);
    expect(parsed.zoho_enabled).toBe(true);
    expect(parsed.lark_enabled).toBe(true);
  });

  it("treats the flags as absent when the server omits them (older/general deployment)", () => {
    // omitempty on the Go side means a deployment without the integrations
    // sends no key at all — the optional schema leaves them undefined, and the
    // config store coerces `=== true` to false downstream.
    const parsed = AppConfigSchema.parse({ cdn_domain: "", allow_signup: true });
    expect(parsed.bitrix_enabled).toBeUndefined();
    expect(parsed.zoho_enabled).toBeUndefined();
    expect(parsed.lark_enabled).toBeUndefined();
  });

  it("survives a malformed body via parseWithFallback without throwing", () => {
    // A non-boolean flag must not reject the whole response — the preprocess
    // downgrades the bad value to the safe default (false) rather than
    // white-screening, and the rest of the config still parses.
    const parsed = parseWithFallback(
      { cdn_domain: "", allow_signup: true, bitrix_enabled: "yes" },
      AppConfigSchema,
      EMPTY_APP_CONFIG,
      { endpoint: "GET /api/config" },
    );
    expect(parsed.bitrix_enabled).toBe(false);
    expect(parsed.allow_signup).toBe(true);
  });
});

describe("ReleaseIntegrationListSchema (release-hub Thread B)", () => {
  it("parses a well-formed list and defaults missing optional fields", () => {
    const parsed = ReleaseIntegrationListSchema.parse([
      { id: "ri1", kind: "webhook", events: ["deploy_recorded"], enabled: true, has_secret: true },
    ]);
    const row = parsed[0]!;
    expect(row.probe_status).toBe("");
    expect(row.config).toEqual({});
    expect(row.has_secret).toBe(true);
    expect(row.created_at).toBe("");
  });

  it("falls back on a malformed body instead of throwing", () => {
    // Not an array → whole parse fails → fallback.
    expect(
      parseWithFallback({ nope: true }, ReleaseIntegrationListSchema, EMPTY_RELEASE_INTEGRATIONS, {
        endpoint: "GET /api/workspaces/{id}/release-integrations",
      }),
    ).toEqual(EMPTY_RELEASE_INTEGRATIONS);
    // A row whose events is the wrong type → row invalid → whole parse fails.
    expect(
      parseWithFallback([{ id: "ri1", events: "deploy_recorded" }], ReleaseIntegrationListSchema, EMPTY_RELEASE_INTEGRATIONS, {
        endpoint: "GET /api/workspaces/{id}/release-integrations",
      }),
    ).toEqual(EMPTY_RELEASE_INTEGRATIONS);
    // null body → fallback.
    expect(
      parseWithFallback(null, ReleaseIntegrationListSchema, EMPTY_RELEASE_INTEGRATIONS, { endpoint: "t" }),
    ).toEqual(EMPTY_RELEASE_INTEGRATIONS);
  });

  it("keeps unknown extra fields (loose) and unknown event strings (no enum)", () => {
    const parsed = ReleaseIntegrationListSchema.parse([
      { id: "ri1", kind: "slack", events: ["future_event"], server_only_field: 1 },
    ]);
    expect(parsed[0]!.events).toEqual(["future_event"]);
    expect((parsed[0] as Record<string, unknown>).server_only_field).toBe(1);
  });
});

describe("ReviewVerdictSchema (Review stage v2)", () => {
  it("parses a well-formed verdict including findings", () => {
    const parsed = ReviewVerdictSchema.parse({
      verdict: "fail",
      summary: "1 blocker in the auth path",
      commit_sha: "deadbeefcafe",
      files_reviewed: 7,
      findings: [
        {
          file: "server/internal/handler/auth.go",
          line: 42,
          severity: "blocker",
          title: "token compared with ==",
          detail: "Use subtle.ConstantTimeCompare.",
        },
        { file: "docs/x.md", line: null, severity: "minor", title: "typo", detail: "" },
      ],
      comment_id: "c1",
      reviewed_at: "2026-07-12T00:00:00Z",
      reviewer_agent_id: "a1",
    });
    expect(parsed.verdict).toBe("fail");
    expect(parsed.findings).toHaveLength(2);
    expect(parsed.findings[0]!.severity).toBe("blocker");
    expect(parsed.findings[1]!.line).toBeNull();
  });

  it("parses the endpoint's explicit 'none' answer (no review yet)", () => {
    const parsed = ReviewVerdictSchema.parse({ verdict: "none", findings: [] });
    expect(parsed.verdict).toBe("none");
    expect(parsed.findings).toEqual([]);
    expect(parsed.summary).toBe("");
    expect(parsed.commit_sha).toBe("");
  });

  it("falls back to the 'none' empty verdict on a malformed body instead of throwing", () => {
    for (const bad of [null, "nope", 42, { findings: "bad" }, { verdict: 7 }]) {
      const out = parseWithFallback(bad, ReviewVerdictSchema, EMPTY_REVIEW_VERDICT, {
        endpoint: "t",
      });
      expect(out).toEqual(EMPTY_REVIEW_VERDICT);
    }
  });

  it("defaults a partial finding instead of rejecting the payload (agent-authored)", () => {
    const parsed = ReviewVerdictSchema.parse({
      verdict: "pass",
      findings: [{ title: "note without file/line/severity" }],
    });
    expect(parsed.findings[0]).toMatchObject({
      file: "",
      line: null,
      severity: "minor",
      title: "note without file/line/severity",
      detail: "",
    });
  });

  it("keeps an unrecognized future severity/verdict as-is (enum drift downgrades)", () => {
    const parsed = ReviewVerdictSchema.parse({
      verdict: "pass_with_notes",
      findings: [{ file: "a.ts", line: 1, severity: "nitpick", title: "t", detail: "d" }],
    });
    expect(parsed.verdict).toBe("pass_with_notes");
    expect(parsed.findings[0]!.severity).toBe("nitpick");
  });
});

describe("ReviewDecisionResponseSchema (review-decision)", () => {
  it("parses both action shapes", () => {
    expect(
      ReviewDecisionResponseSchema.parse({ action: "approve", merged_dispatch: true }),
    ).toMatchObject({
      action: "approve", merged_dispatch: true, status: "", dispatched: false,
      plan_version: 0, revision_id: "", correction_step_id: "",
    });
    expect(
      ReviewDecisionResponseSchema.parse({
        action: "request_changes",
        status: "in_progress",
        dispatched: true,
        plan_version: 2,
        revision_id: "revision-2",
        correction_step_id: "changes-v2",
      }),
    ).toMatchObject({
      action: "request_changes", status: "in_progress", dispatched: true,
      plan_version: 2, revision_id: "revision-2", correction_step_id: "changes-v2",
    });
  });

  it("falls back to the zero-value decision on a malformed body", () => {
    const out = parseWithFallback("nope", ReviewDecisionResponseSchema, EMPTY_REVIEW_DECISION, {
      endpoint: "t",
    });
    expect(out).toEqual(EMPTY_REVIEW_DECISION);
  });
});

describe("QAEvidenceSchema (evidence-first QA)", () => {
  it("parses a well-formed evidence row including the command table", () => {
    const parsed = QAEvidenceSchema.parse({
      id: "e1",
      issue_id: "i1",
      baseline_ref: "",
      branch_sha: "",
      verdict: "fail",
      summary: "1 new failure",
      result: {
        verdict: "fail",
        summary: "1 new failure",
        commands: [
          {
            title: "API returns a greeting",
            expected: "A successful greeting response",
            observed: "The endpoint returned an error",
            cmd: "go test ./...",
            baseline_exit: 0,
            branch_exit: 1,
            kind: "new_failure",
          },
        ],
        screenshots: ["/var/www/x.png"],
      },
      captured_at: "2026-06-30T00:00:00Z",
    });
    expect(parsed.verdict).toBe("fail");
    expect(parsed.result?.commands[0]!.kind).toBe("new_failure");
    expect(parsed.result?.commands[0]!.title).toBe("API returns a greeting");
    expect(parsed.result?.commands[0]!.expected).toBe("A successful greeting response");
    expect(parsed.result?.commands[0]!.observed).toBe("The endpoint returned an error");
  });

  it("falls back to null on a malformed body instead of throwing", () => {
    // The endpoint returns null when no evidence exists — the client parses
    // against a nullable schema with a null fallback.
    const nullable = QAEvidenceSchema.nullable();
    expect(parseWithFallback(null, nullable, null, { endpoint: "t" })).toBeNull();
    expect(parseWithFallback("nope", nullable, null, { endpoint: "t" })).toBeNull();
    expect(parseWithFallback({ result: { commands: "bad" } }, nullable, null, { endpoint: "t" })).toBeNull();
  });

  it("tolerates a missing/partial result block (parse, don't trust)", () => {
    const parsed = QAEvidenceSchema.parse({ id: "e1", issue_id: "i1", verdict: "pass" });
    expect(parsed.result).toBeNull();
    expect(parsed.summary).toBe("");
    expect(parsed.captured_at).toBe("");
  });

  it("parses a well-formed design result", () => {
    const parsed = QAEvidenceSchema.parse({
      id: "e1", issue_id: "i1", verdict: "pass",
      result: {
        verdict: "pass", summary: "", commands: [], screenshots: [],
        design: { verdict: "fail", reference_node: "208:5147", mismatches: [{ kind: "color", selector: ".btn", expected: "#2563EB", actual: "#333" }] },
      },
      captured_at: "2026-06-30T00:00:00Z",
    });
    expect(parsed.result?.design?.verdict).toBe("fail");
    expect(parsed.result?.design?.mismatches[0]!.kind).toBe("color");
  });

  it("degrades a MALFORMED design block to null WITHOUT nuking the whole result", () => {
    // An agent emits a wrong-typed design (string shorthand). The verdict +
    // commands must survive; only the design sub-section drops (.catch(null)).
    const parsed = QAEvidenceSchema.parse({
      id: "e1", issue_id: "i1", verdict: "fail",
      result: {
        verdict: "fail", summary: "1 fail",
        commands: [{ cmd: "go test ./...", baseline_exit: 0, branch_exit: 1, kind: "new_failure" }],
        screenshots: [],
        design: "pass", // malformed — should be an object
      },
      captured_at: "2026-06-30T00:00:00Z",
    });
    expect(parsed.result?.verdict).toBe("fail");
    expect(parsed.result?.commands).toHaveLength(1);
    expect(parsed.result?.design).toBeNull();
  });

  describe("reconciled_state (Phase 2 — server-computed single source of truth)", () => {
    it("parses a known reconciled state through untouched", () => {
      const parsed = QAEvidenceSchema.parse({
        id: "e1", issue_id: "i1", verdict: "pass", reconciled_state: "pass_with_failing_cases",
      });
      expect(parsed.reconciled_state).toBe("pass_with_failing_cases");
    });

    it("defaults to \"\" when the field is absent — OLD SERVER compatibility", () => {
      // A server that predates Phase 2 never sends this field at all. The
      // client must fall back to its own label-derived computation, not
      // reject the whole evidence row — "" is the explicit signal for that.
      const parsed = QAEvidenceSchema.parse({ id: "e1", issue_id: "i1", verdict: "pass" });
      expect(parsed.reconciled_state).toBe("");
    });

    it("degrades an unrecognized/future state to a plain string, never throws", () => {
      // A newer server might ship an enum value this client doesn't know
      // about yet — must not reject the evidence row over it.
      const parsed = QAEvidenceSchema.parse({
        id: "e1", issue_id: "i1", verdict: "pass", reconciled_state: "some_future_state",
      });
      expect(parsed.reconciled_state).toBe("some_future_state");
    });

    it("defaults the Phase 3 identity fields when absent — OLD SERVER compatibility", () => {
      const parsed = QAEvidenceSchema.parse({ id: "e1", issue_id: "i1", verdict: "pass" });
      expect(parsed.commit_sha).toBe("");
      expect(parsed.triggered_by).toBe("");
      expect(parsed.started_at).toBe("");
      expect(parsed.finished_at).toBe("");
    });

    it("passes through populated identity fields", () => {
      const parsed = QAEvidenceSchema.parse({
        id: "e1", issue_id: "i1", verdict: "pass",
        commit_sha: "deadbeef1234", triggered_by: "auto",
        started_at: "2026-07-10T11:00:00Z", finished_at: "2026-07-10T11:20:00Z",
      });
      expect(parsed.commit_sha).toBe("deadbeef1234");
      expect(parsed.triggered_by).toBe("auto");
    });

    it("a wrong-typed reconciled_state (number) falls back to the row's own default via .nullable() null-fallback, not a throw", () => {
      // Whole-response malformed-field tolerance: a non-string reconciled_state
      // must not crash the endpoint — parseWithFallback's null fallback (the
      // real client path, see getQAEvidence) absorbs it.
      const result = parseWithFallback(
        { id: "e1", issue_id: "i1", verdict: "pass", reconciled_state: 42 },
        QAEvidenceSchema.nullable(),
        null,
        { endpoint: "t" },
      );
      expect(result).toBeNull();
    });
  });
});

describe("DeployEventSchema / IssueDeployEventsResponseSchema (deploy P0)", () => {
  const endpoint = { endpoint: "GET /api/issues/:id/deploy-events" };

  it("parses a well-formed deploy event", () => {
    const parsed = DeployEventSchema.parse({
      id: "de-1",
      issue_id: "issue-1",
      ref: "feature/foo",
      target: "jamshid's box",
      status: "success",
      summary: "Switched to a new branch",
      captured_at: "2026-06-30T00:00:00Z",
    });
    expect(parsed.status).toBe("success");
    expect(parsed.ref).toBe("feature/foo");
  });

  it("defaults every field on a bare object (parse, don't trust)", () => {
    const parsed = DeployEventSchema.parse({});
    expect(parsed).toEqual({
      id: "",
      issue_id: "",
      ref: "",
      target: "",
      status: "",
      summary: "",
      captured_at: "",
    });
  });

  it("parses a well-formed issue deploy-events response (latest + recent)", () => {
    const raw = {
      latest: { id: "de-2", issue_id: "issue-1", ref: "main", target: "box-1", status: "failed", summary: "", captured_at: "2026-07-01T00:00:00Z" },
      recent: [
        { id: "de-2", issue_id: "issue-1", ref: "main", target: "box-1", status: "failed", summary: "", captured_at: "2026-07-01T00:00:00Z" },
        { id: "de-1", issue_id: "issue-1", ref: "main", target: "box-1", status: "success", summary: "", captured_at: "2026-06-30T00:00:00Z" },
      ],
    };
    const parsed = parseWithFallback(raw, IssueDeployEventsResponseSchema, EMPTY_DEPLOY_EVENTS, endpoint);
    expect(parsed.latest?.status).toBe("failed");
    expect(parsed.recent).toHaveLength(2);
  });

  it("degrades a never-deployed issue's null latest to the empty-list shape, not an error", () => {
    const parsed = parseWithFallback(
      { latest: null, recent: [] },
      IssueDeployEventsResponseSchema,
      EMPTY_DEPLOY_EVENTS,
      endpoint,
    );
    expect(parsed.latest).toBeNull();
    expect(parsed.recent).toEqual([]);
  });

  it("falls back to the empty shape on a malformed body instead of throwing", () => {
    expect(parseWithFallback(null, IssueDeployEventsResponseSchema, EMPTY_DEPLOY_EVENTS, endpoint)).toEqual(
      EMPTY_DEPLOY_EVENTS,
    );
    expect(parseWithFallback("nope", IssueDeployEventsResponseSchema, EMPTY_DEPLOY_EVENTS, endpoint)).toEqual(
      EMPTY_DEPLOY_EVENTS,
    );
  });

  it("drops a malformed recent entry's shape gracefully via per-field defaults rather than rejecting the whole response", () => {
    const parsed = parseWithFallback(
      { latest: null, recent: [{ status: "success" }] },
      IssueDeployEventsResponseSchema,
      EMPTY_DEPLOY_EVENTS,
      endpoint,
    );
    expect(parsed.recent).toHaveLength(1);
    expect(parsed.recent[0]!.status).toBe("success");
    expect(parsed.recent[0]!.ref).toBe("");
  });
});

describe("parseDeployEnvironments (deploy MCP-P1)", () => {
  it("parses a well-formed two-environment list", () => {
    const envs = parseDeployEnvironments({
      deploy_environments: [
        {
          key: "staging",
          label: "Staging",
          kind: "gitlab_pipeline",
          target: { project_path: "salesdoctor/sd-main", ref: "staging", environment: "staging" },
        },
        {
          key: "production",
          label: "Production",
          kind: "gitlab_pipeline",
          target: { project_path: "salesdoctor/sd-main", ref: "main" },
          requires_human: true,
        },
      ],
    });
    expect(envs).toHaveLength(2);
    expect(envs[0]!.key).toBe("staging");
    expect(envs[0]!.target.project_path).toBe("salesdoctor/sd-main");
    expect(envs[1]!.requires_human).toBe(true);
  });

  it("returns [] for missing, null, or non-object settings", () => {
    expect(parseDeployEnvironments(undefined)).toEqual([]);
    expect(parseDeployEnvironments(null)).toEqual([]);
    expect(parseDeployEnvironments("nope")).toEqual([]);
    expect(parseDeployEnvironments({})).toEqual([]);
  });

  it("returns [] when deploy_environments is not an array", () => {
    expect(parseDeployEnvironments({ deploy_environments: "staging" })).toEqual([]);
    expect(parseDeployEnvironments({ deploy_environments: { key: "staging" } })).toEqual([]);
  });

  it("skips malformed entries without hiding their siblings, and drops keyless entries", () => {
    const envs = parseDeployEnvironments({
      deploy_environments: [
        { key: "staging", target: { command: "make deploy" } },
        "not an object",
        { key: 42 },
        { label: "keyless" },
      ],
    });
    expect(envs).toHaveLength(1);
    expect(envs[0]!.key).toBe("staging");
  });

  it("degrades a malformed target to the empty target instead of rejecting the entry", () => {
    const envs = parseDeployEnvironments({
      deploy_environments: [{ key: "staging", target: "broken" }],
    });
    expect(envs).toHaveLength(1);
    expect(envs[0]!.target).toMatchObject({ project_path: "", ref: "", command: "" });
  });

  it("deployEnvironmentRequiresHuman: explicit flag or production-named key", () => {
    const env = (over: Record<string, unknown>) =>
      parseDeployEnvironments({ deploy_environments: [{ key: "staging", ...over }] })[0]!;
    expect(deployEnvironmentRequiresHuman(env({}))).toBe(false);
    expect(deployEnvironmentRequiresHuman(env({ requires_human: true }))).toBe(true);
    expect(deployEnvironmentRequiresHuman(env({ key: "production" }))).toBe(true);
    expect(deployEnvironmentRequiresHuman(env({ key: " PROD " }))).toBe(true);
  });
});

describe("FigmaCredentialStatusSchema", () => {
  const endpoint = { endpoint: "GET /api/workspaces/{id}/figma-credential" };

  it("parses a full status payload", () => {
    const parsed = parseWithFallback(
      {
        configured: true,
        label: "SD design",
        token_last4: "ab12",
        token_kind: "pat",
        expires_at: "2026-09-30T00:00:00Z",
        expiring_soon: false,
        seat_probe: "ok",
        probe_status: "ok",
        probed_at: "2026-07-02T00:00:00Z",
      },
      FigmaCredentialStatusSchema,
      EMPTY_FIGMA_CREDENTIAL_STATUS,
      endpoint,
    );
    expect(parsed.configured).toBe(true);
    expect(parsed.token_last4).toBe("ab12");
  });

  it("defaults every missing field (older server shape)", () => {
    const parsed = parseWithFallback(
      { configured: true },
      FigmaCredentialStatusSchema,
      EMPTY_FIGMA_CREDENTIAL_STATUS,
      endpoint,
    );
    expect(parsed.configured).toBe(true);
    expect(parsed.expiring_soon).toBe(false);
    expect(parsed.probe_status).toBe("");
  });

  it("falls back on wrong-typed fields", () => {
    const parsed = parseWithFallback(
      { configured: "yes", expires_at: 123 },
      FigmaCredentialStatusSchema,
      EMPTY_FIGMA_CREDENTIAL_STATUS,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_FIGMA_CREDENTIAL_STATUS);
  });

  it("falls back on null / non-object bodies", () => {
    for (const body of [null, [], "nope"]) {
      const parsed = parseWithFallback(
        body,
        FigmaCredentialStatusSchema,
        EMPTY_FIGMA_CREDENTIAL_STATUS,
        endpoint,
      );
      expect(parsed.configured).toBe(false);
    }
  });

  it("passes unknown future fields through (loose)", () => {
    const parsed = parseWithFallback(
      { configured: true, some_future_field: 1 },
      FigmaCredentialStatusSchema,
      EMPTY_FIGMA_CREDENTIAL_STATUS,
      endpoint,
    ) as unknown as Record<string, unknown>;
    expect(parsed.some_future_field).toBe(1);
  });
});

describe("McpCredentialStatusSchema", () => {
  const endpoint = { endpoint: "GET /api/workspaces/{id}/mcp-credentials" };

  it("parses a full status payload", () => {
    const parsed = parseWithFallback(
      {
        id: "cred-1",
        server_name: "linear",
        has_secret: true,
        last4: "1234",
        created_at: "2026-07-13T00:00:00Z",
        updated_at: "2026-07-13T00:00:00Z",
      },
      McpCredentialStatusSchema,
      EMPTY_MCP_CREDENTIAL_STATUS,
      endpoint,
    );
    expect(parsed.server_name).toBe("linear");
    expect(parsed.has_secret).toBe(true);
    expect(parsed.last4).toBe("1234");
  });

  it("defaults every missing field (older server shape)", () => {
    const parsed = parseWithFallback(
      { server_name: "linear" },
      McpCredentialStatusSchema,
      EMPTY_MCP_CREDENTIAL_STATUS,
      endpoint,
    );
    expect(parsed.server_name).toBe("linear");
    expect(parsed.has_secret).toBe(false);
    expect(parsed.last4).toBe("");
  });

  it("never surfaces token material even if a drifted server leaks it", () => {
    // A `.loose()` schema passes unknown fields through, but the typed shape the
    // panel reads has no secret field — the token can't be rendered by mistake.
    const parsed = parseWithFallback(
      { server_name: "linear", has_secret: true, secret: "Bearer LEAK" },
      McpCredentialStatusSchema,
      EMPTY_MCP_CREDENTIAL_STATUS,
      endpoint,
    );
    expect(parsed.has_secret).toBe(true);
    expect((parsed as unknown as Record<string, unknown>).last4 ?? "").not.toContain("LEAK");
  });

  it("list schema downgrades a malformed / non-array body to an empty list", () => {
    for (const body of [null, "nope", { server_name: "x" }, 42]) {
      const parsed = parseWithFallback(body, McpCredentialListSchema, EMPTY_MCP_CREDENTIAL_LIST, endpoint);
      expect(parsed).toEqual([]);
    }
  });

  it("list schema keeps well-formed rows and defaults their gaps", () => {
    const parsed = parseWithFallback(
      [{ server_name: "linear", has_secret: true }],
      McpCredentialListSchema,
      EMPTY_MCP_CREDENTIAL_LIST,
      endpoint,
    );
    expect(parsed).toHaveLength(1);
    expect(parsed[0]!.server_name).toBe("linear");
    expect(parsed[0]!.last4).toBe("");
  });
});

describe("IssueBrowserResponseSchema", () => {
  const endpoint = { endpoint: "GET /api/issues/:id/browser" };

  it("parses both modes and defaults missing fields", () => {
    const selfHost = parseWithFallback(
      { mode: "self-host", daemon_url: "http://127.0.0.1:19514" },
      IssueBrowserResponseSchema,
      EMPTY_ISSUE_BROWSER,
      endpoint,
    );
    expect(selfHost.mode).toBe("self-host");
    expect(selfHost.daemon_url).toBe("http://127.0.0.1:19514");
    expect(selfHost.browser_url).toBe(""); // absent → defaulted, never undefined

    const cloud = parseWithFallback(
      { mode: "cloud", browser_url: "/browser/proxy/abc123" },
      IssueBrowserResponseSchema,
      EMPTY_ISSUE_BROWSER,
      endpoint,
    );
    expect(cloud.browser_url).toBe("/browser/proxy/abc123");
    expect(cloud.daemon_url).toBe("");
  });

  it("degrades wrong-typed fields to the empty fallback instead of throwing", () => {
    const parsed = parseWithFallback(
      { mode: 7, daemon_url: null },
      IssueBrowserResponseSchema,
      EMPTY_ISSUE_BROWSER,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_ISSUE_BROWSER);
  });

  it("falls back on null / non-object bodies", () => {
    for (const body of [null, [], "nope"]) {
      const parsed = parseWithFallback(body, IssueBrowserResponseSchema, EMPTY_ISSUE_BROWSER, endpoint);
      expect(parsed.mode).toBe("");
    }
  });

  it("tolerates an unknown future mode (consumer checks mode itself)", () => {
    const parsed = parseWithFallback(
      { mode: "edge-pop", browser_url: "/browser/proxy/x" },
      IssueBrowserResponseSchema,
      EMPTY_ISSUE_BROWSER,
      endpoint,
    );
    expect(parsed.mode).toBe("edge-pop"); // renders as "unavailable", not a crash
  });
});

describe("DaemonBrowseTargetSchema", () => {
  const endpoint = { endpoint: "GET /api/runtimes/by-daemon/:daemonId/browse" };

  it("parses both modes and defaults missing fields", () => {
    const selfHost = parseWithFallback(
      { mode: "self-host", daemon_url: "http://127.0.0.1:19514" },
      DaemonBrowseTargetSchema,
      EMPTY_DAEMON_BROWSE_TARGET,
      endpoint,
    );
    expect(selfHost.daemon_url).toBe("http://127.0.0.1:19514");

    const cloud = parseWithFallback(
      { mode: "cloud", daemon_url: "/browser/proxy/abc123" },
      DaemonBrowseTargetSchema,
      EMPTY_DAEMON_BROWSE_TARGET,
      endpoint,
    );
    expect(cloud.daemon_url).toBe("/browser/proxy/abc123");

    // A registered-but-stopped machine: mode carries the state, url is blank.
    const offline = parseWithFallback({ mode: "offline" }, DaemonBrowseTargetSchema, EMPTY_DAEMON_BROWSE_TARGET, endpoint);
    expect(offline.mode).toBe("offline");
    expect(offline.daemon_url).toBe(""); // absent → defaulted, never undefined
  });

  it("degrades wrong-typed fields to the empty fallback instead of throwing", () => {
    const parsed = parseWithFallback(
      { mode: 7, daemon_url: null },
      DaemonBrowseTargetSchema,
      EMPTY_DAEMON_BROWSE_TARGET,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_DAEMON_BROWSE_TARGET);
  });

  it("falls back on null / non-object bodies", () => {
    for (const body of [null, [], "nope"]) {
      const parsed = parseWithFallback(body, DaemonBrowseTargetSchema, EMPTY_DAEMON_BROWSE_TARGET, endpoint);
      expect(parsed.mode).toBe("");
    }
  });

  it("tolerates an unknown future mode (consumer checks mode itself)", () => {
    const parsed = parseWithFallback(
      { mode: "mesh", daemon_url: "/browser/proxy/x" },
      DaemonBrowseTargetSchema,
      EMPTY_DAEMON_BROWSE_TARGET,
      endpoint,
    );
    expect(parsed.mode).toBe("mesh");
  });
});

describe("FsListResponseSchema", () => {
  const endpoint = { endpoint: "GET /editor/fs/list" };

  it("parses a listing and defaults absent entry flags", () => {
    const parsed = parseWithFallback(
      {
        path: "/Users/dev",
        parent: "",
        home: "/Users/dev",
        entries: [{ name: "code", path: "/Users/dev/code", is_dir: true, is_git_repo: true }],
      },
      FsListResponseSchema,
      EMPTY_FS_LIST,
      endpoint,
    );
    expect(parsed.path).toBe("/Users/dev");
    expect(parsed.parent).toBe(""); // root boundary — UI hides "up one level"
    expect(parsed.entries[0]!.is_git_repo).toBe(true);
    expect(parsed.entries[0]!.is_symlink).toBe(false); // absent → defaulted
    expect(parsed.truncated).toBe(false);
  });

  it("defaults entries to [] so an older daemon renders empty, not undefined", () => {
    const parsed = parseWithFallback({ path: "/srv" }, FsListResponseSchema, EMPTY_FS_LIST, endpoint);
    expect(parsed.entries).toEqual([]);
  });

  it("degrades a null entries array to the empty fallback instead of throwing", () => {
    const parsed = parseWithFallback(
      { path: "/srv", entries: null },
      FsListResponseSchema,
      EMPTY_FS_LIST,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_FS_LIST);
  });

  it("degrades wrong-typed entry fields to the empty fallback", () => {
    const parsed = parseWithFallback(
      { path: "/srv", entries: [{ name: 5, path: false }] },
      FsListResponseSchema,
      EMPTY_FS_LIST,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_FS_LIST);
  });

  it("falls back on null / non-object bodies", () => {
    for (const body of [null, [], "nope"]) {
      const parsed = parseWithFallback(body, FsListResponseSchema, EMPTY_FS_LIST, endpoint);
      expect(parsed.entries).toEqual([]);
    }
  });

  it("tolerates unknown future fields on the listing and its entries", () => {
    const parsed = parseWithFallback(
      {
        path: "/Users/dev",
        entries: [{ name: "code", path: "/Users/dev/code", is_dir: true, mtime: 123 }],
        cursor: "next",
      },
      FsListResponseSchema,
      EMPTY_FS_LIST,
      endpoint,
    );
    expect(parsed.entries[0]!.name).toBe("code");
  });
});

describe("WorkspaceLabsSchema", () => {
  const endpoint = { endpoint: "GET /api/workspace-labs" };

  it("defaults absent fields (fresh workspace has no labs block)", () => {
    const parsed = parseWithFallback({}, WorkspaceLabsSchema, EMPTY_WORKSPACE_LABS, endpoint);
    expect(parsed.qa_dev_boxes).toBe(true);
    expect(parsed.qa_fallback_box_id).toBe("");
  });

  it("degrades wrong-typed fields to the fallback instead of throwing", () => {
    const parsed = parseWithFallback(
      { qa_dev_boxes: "yes", qa_fallback_box_id: 7 },
      WorkspaceLabsSchema,
      EMPTY_WORKSPACE_LABS,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_WORKSPACE_LABS);
  });

  it("falls back on null / non-object bodies", () => {
    for (const body of [null, [], "nope"]) {
      const parsed = parseWithFallback(body, WorkspaceLabsSchema, EMPTY_WORKSPACE_LABS, endpoint);
      expect(parsed.qa_dev_boxes).toBe(true);
    }
  });
});

describe("TestCaseSchema metadata (preconditions / priority / modality)", () => {
  const endpoint = { endpoint: "GET /api/issues/:id/test-cases" };
  const listEndpoint = { endpoint: "GET /api/issues/:id/test-cases" };
  // EMPTY_LIST_TEST_CASES's literal type is { test_cases: never[] } — anchor
  // parseWithFallback's T to the real response shape, as the client does.
  const emptyList: ListTestCasesResponse = EMPTY_LIST_TEST_CASES;
  const legacyCase = {
    id: "tc-1",
    issue_id: "issue-1",
    title: "login works",
    steps: "1. open login",
    expected: "dashboard",
    kind: "manual",
    source: "human",
    author_type: "member",
    category: "positive",
    created_at: "2026-01-01T00:00:00Z",
    latest_run: null,
  };

  it("defaults absent metadata fields — an OLD server's response parses as a legacy row", () => {
    const parsed = parseWithFallback(
      { test_cases: [legacyCase] },
      ListTestCasesResponseSchema,
      emptyList,
      listEndpoint,
    );
    expect(parsed.test_cases).toHaveLength(1);
    expect(parsed.test_cases[0]?.preconditions).toBe("");
    expect(parsed.test_cases[0]?.priority).toBe("p2");
    expect(parsed.test_cases[0]?.modality).toBe("");
    expect(parsed.test_cases[0]?.criterion_ref).toBe("");
  });

  it("keeps provided metadata and tolerates unknown enum drift (plain strings)", () => {
    const parsed = parseWithFallback(
      {
        test_cases: [
          { ...legacyCase, preconditions: "admin seeded", priority: "p1", modality: "ui", criterion_ref: "AC2" },
          // A FUTURE server's new enum value must still parse (downgrade, not crash).
          { ...legacyCase, id: "tc-2", priority: "p0", modality: "mobile" },
        ],
      },
      ListTestCasesResponseSchema,
      emptyList,
      listEndpoint,
    );
    expect(parsed.test_cases[0]?.priority).toBe("p1");
    expect(parsed.test_cases[0]?.modality).toBe("ui");
    expect(parsed.test_cases[0]?.preconditions).toBe("admin seeded");
    expect(parsed.test_cases[0]?.criterion_ref).toBe("AC2");
    expect(parsed.test_cases[1]?.priority).toBe("p0");
    expect(parsed.test_cases[1]?.modality).toBe("mobile");
  });

  it("falls back to the inert empty case on wrong-typed metadata (single-row endpoints)", () => {
    const parsed = parseWithFallback(
      { ...legacyCase, priority: 1, modality: ["ui"], preconditions: { text: "x" } },
      TestCaseSchema,
      EMPTY_TEST_CASE,
      endpoint,
    );
    expect(parsed).toEqual(EMPTY_TEST_CASE);
    expect(parsed.priority).toBe("p2");
  });

  it("falls back to an empty list on a null test_cases array", () => {
    const parsed = parseWithFallback(
      { test_cases: null },
      ListTestCasesResponseSchema,
      emptyList,
      listEndpoint,
    );
    expect(parsed.test_cases).toEqual([]);
  });
});

describe("TestCaseRunsResponseSchema (Phase 3 run history)", () => {
  const endpoint = { endpoint: "GET /api/test-cases/:id/runs" };

  it("parses a well-formed history with identity fields", () => {
    const parsed = TestCaseRunsResponseSchema.parse({
      runs: [
        {
          id: "r1", status: "pass", run_source: "agent", created_at: "2026-07-10T12:00:00Z",
          commit_sha: "deadbeef1234", session_id: "s1",
          started_at: "", finished_at: "2026-07-10T12:01:00Z",
        },
      ],
    });
    expect(parsed.runs).toHaveLength(1);
    expect(parsed.runs[0]!.commit_sha).toBe("deadbeef1234");
  });

  it("defaults identity fields on legacy runs (pre-157 rows)", () => {
    const parsed = TestCaseRunsResponseSchema.parse({
      runs: [{ id: "r1", status: "fail", run_source: "human", created_at: "2026-01-01T00:00:00Z" }],
    });
    expect(parsed.runs[0]!.commit_sha).toBe("");
    expect(parsed.runs[0]!.session_id).toBe("");
  });

  it("falls back to an empty history on a malformed body instead of throwing", () => {
    expect(parseWithFallback(null, TestCaseRunsResponseSchema, EMPTY_TEST_CASE_RUNS, endpoint).runs).toEqual([]);
    expect(parseWithFallback({ runs: "bad" }, TestCaseRunsResponseSchema, EMPTY_TEST_CASE_RUNS, endpoint).runs).toEqual([]);
    expect(parseWithFallback("nope", TestCaseRunsResponseSchema, EMPTY_TEST_CASE_RUNS, endpoint).runs).toEqual([]);
  });

  it("defaults a missing runs array to []", () => {
    expect(TestCaseRunsResponseSchema.parse({}).runs).toEqual([]);
  });
});

describe("QAVerdictsResponseSchema — Phase 3 reconciled_state per entry", () => {
  it("passes reconciled_state + triggered_by through and defaults them when absent (old server)", () => {
    const parsed = QAVerdictsResponseSchema.parse({
      verdicts: {
        "issue-1": { verdict: "pass", reconciled_state: "stale", triggered_by: "auto" },
        "issue-2": { verdict: "fail" },
      },
    });
    expect(parsed.verdicts["issue-1"]!.reconciled_state).toBe("stale");
    expect(parsed.verdicts["issue-1"]!.triggered_by).toBe("auto");
    expect(parsed.verdicts["issue-2"]!.reconciled_state).toBe("");
  });
});

describe("OrchestrationRunSchema — execution semantics", () => {
  const baseRun = {
    id: "run-1",
    issue_id: "issue-1",
    status: "running",
    mode: "auto",
    policy: {},
    plan_version: 1,
    revisions: [],
    created_at: "2026-07-15T00:00:00Z",
    updated_at: "2026-07-15T00:00:00Z",
    steps: [],
    events: [],
  };

  it("parses the stable owner/controller snapshot independently from strategy", () => {
    const parsed = OrchestrationRunSchema.parse({
      ...baseRun,
      execution_strategy: "squad",
      progression_policy: "gated",
      owner_type: "squad",
      owner_id: "squad-1",
      controller_agent_id: "agent-lead",
      execution_mode: "squad",
    });

    expect(parsed.execution_strategy).toBe("squad");
    expect(parsed.progression_policy).toBe("gated");
    expect(parsed.owner_id).toBe("squad-1");
    expect(parsed.controller_agent_id).toBe("agent-lead");
  });

  it("parses the squad roster policy contract", () => {
    const parsed = OrchestrationRunSchema.parse({
      ...baseRun,
      policy: {
        max_concurrency: 2,
        squad_roster: [{
          agent_id: "agent-1",
          name: "Frontend",
          role: "Frontend engineer",
          capability: "frontend",
          model: "gpt-5",
          thinking_level: "medium",
          max_concurrent_tasks: 4,
        }],
      },
      steps: [{
        id: "step-1", key: "work", title: "Work", stage: "dev", status: "pending", position: 0,
        model: "gpt-5", thinking_level: "", approval_required: false, depends_on_step_ids: [],
      }],
    });
    expect(parsed.policy.max_concurrency).toBe(2);
    expect(parsed.policy.squad_roster?.[0]?.model).toBe("gpt-5");
    expect(parsed.steps[0]?.thinking_level).toBe("");
  });

  it("parses the resolved task execution level and its audit signals", () => {
    const parsed = OrchestrationRunSchema.parse({
      ...baseRun,
      policy: {
        task_level: {
          requested: "auto",
          resolved: "coordinated",
          policy_version: 1,
          score: 6,
          signals: ["frontend_and_backend", "data_or_schema_migration"],
        },
      },
    });

    expect(parsed.policy.task_level).toEqual({
      requested: "auto",
      resolved: "coordinated",
      policy_version: 1,
      score: 6,
      signals: ["frontend_and_backend", "data_or_schema_migration"],
    });
  });

  it("drops a malformed task level without making an older desktop lose the run", () => {
    const parsed = OrchestrationRunSchema.parse({
      ...baseRun,
      policy: { task_level: { requested: "future", resolved: null } },
    });

    expect(parsed.id).toBe("run-1");
    expect(parsed.policy.task_level).toBeUndefined();
  });

  it("keeps an older server response readable during the compatibility window", () => {
    const parsed = OrchestrationRunSchema.parse(baseRun);
    expect(parsed.execution_strategy).toBe("custom");
    expect(parsed.progression_policy).toBe("automatic");
    expect(parsed.owner_type).toBe("unassigned");
    expect(parsed.execution_mode).toBe("orchestrated");
    expect(parsed.base_git_states).toEqual([]);
  });

  it("parses an immutable multi-repository base snapshot", () => {
    const parsed = OrchestrationRunSchema.parse({
      ...baseRun,
      base_git_states: [
        { repo: "api", head_sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
        { repo: "web", head_sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" },
      ],
    });
    expect(parsed.base_git_states.map((state) => state.repo)).toEqual(["api", "web"]);
  });

  it("preserves the exact open question identity on a waiting step", () => {
    const waitingRun = {
      ...baseRun,
      steps: [{
        id: "step-1",
        key: "plan",
        title: "Clarify scope",
        stage: "plan",
        status: "waiting_input",
        position: 0,
        question_id: "question-2",
      }],
    };
    const parsed = OrchestrationRunSchema.parse(waitingRun);

    expect(parsed.steps[0]?.question_id).toBe("question-2");
    expect(parseWithFallback(
      { ...waitingRun, steps: [{ ...waitingRun.steps[0], question_id: 42 }] },
      OrchestrationRunSchema,
      null,
      { endpoint: "GET /api/issues/{id}/orchestration" },
    )).toBeNull();
  });
});

describe("ListTelegramInstallationsSchema", () => {
  it("parses a well-formed response", () => {
    const parsed = parseWithFallback(
      {
        installations: [
          {
            agent_id: "a1",
            bot_username: "sd_pm_agent_bot",
            bot_user_id: "8935986908",
            status: "active",
            access_policy: "allowlist",
            allowed_user_ids: ["905434593"],
            allowed_chat_ids: ["-1004336001519"],
          },
        ],
        configured: true,
      },
      ListTelegramInstallationsSchema,
      EMPTY_TELEGRAM_INSTALLATIONS,
      { endpoint: "GET /api/workspaces/{id}/telegram/installations" },
    );
    expect(parsed.configured).toBe(true);
    expect(parsed.installations[0]?.bot_username).toBe("sd_pm_agent_bot");
    expect(parsed.installations[0]?.allowed_chat_ids).toEqual(["-1004336001519"]);
  });

  it("defaults configured to false when the field is missing", () => {
    // Claiming the deployment is configured would show an install form that
    // cannot succeed — the operator finds out only after pasting a live token.
    const parsed = parseWithFallback(
      { installations: [] },
      ListTelegramInstallationsSchema,
      EMPTY_TELEGRAM_INSTALLATIONS,
      { endpoint: "GET /api/workspaces/{id}/telegram/installations" },
    );
    expect(parsed.configured).toBe(false);
  });

  it("survives a malformed installation row", () => {
    // A settings panel that white-screens on a drifted field is worse than one
    // showing a stale-but-benign row.
    const parsed = parseWithFallback(
      {
        installations: [{ agent_id: "a1", allowed_user_ids: "not-an-array", access_policy: 42 }],
        configured: true,
      },
      ListTelegramInstallationsSchema,
      EMPTY_TELEGRAM_INSTALLATIONS,
      { endpoint: "GET /api/workspaces/{id}/telegram/installations" },
    );
    // access_policy is the wrong type, so the row fails and the array's catch
    // downgrades the list rather than throwing into the UI.
    expect(Array.isArray(parsed.installations)).toBe(true);
  });

  it("falls back when the body is not an object at all", () => {
    const parsed = parseWithFallback(
      null,
      ListTelegramInstallationsSchema,
      EMPTY_TELEGRAM_INSTALLATIONS,
      { endpoint: "GET /api/workspaces/{id}/telegram/installations" },
    );
    expect(parsed).toEqual(EMPTY_TELEGRAM_INSTALLATIONS);
  });

  it("keeps chat ids as strings so a 64-bit id survives", () => {
    // Chat ids are past 2^53. Parsed as numbers they round silently, and the
    // bot then answers a chat that does not exist.
    const parsed = parseWithFallback(
      { installations: [{ agent_id: "a", allowed_chat_ids: ["-1004336001519"] }], configured: true },
      ListTelegramInstallationsSchema,
      EMPTY_TELEGRAM_INSTALLATIONS,
      { endpoint: "GET /api/workspaces/{id}/telegram/installations" },
    );
    expect(parsed.installations[0]?.allowed_chat_ids?.[0]).toBe("-1004336001519");
  });
});

describe("ListExternalIdentityLinksSchema", () => {
  it("parses linked providers", () => {
    const parsed = parseWithFallback(
      { links: [{ provider: "telegram", external_id: "42" }] },
      ListExternalIdentityLinksSchema,
      EMPTY_EXTERNAL_IDENTITY_LINKS,
      { endpoint: "GET /api/me/links" },
    );
    expect(parsed.links[0]?.provider).toBe("telegram");
    expect(parsed.links[0]?.external_id).toBe("42");
  });

  it("falls back on malformed bodies", () => {
    expect(
      parseWithFallback(null, ListExternalIdentityLinksSchema, EMPTY_EXTERNAL_IDENTITY_LINKS, {
        endpoint: "GET /api/me/links",
      }),
    ).toEqual(EMPTY_EXTERNAL_IDENTITY_LINKS);
  });
});

describe("TelegramLinkStartSchema", () => {
  it("parses nonce and deep_link", () => {
    const parsed = parseWithFallback(
      { nonce: "abc", deep_link: "https://t.me/bot?start=login_abc" },
      TelegramLinkStartSchema,
      EMPTY_TELEGRAM_LINK_START,
      { endpoint: "POST /api/me/links/telegram/start" },
    );
    expect(parsed.nonce).toBe("abc");
    expect(parsed.deep_link).toContain("t.me");
  });

  it("falls back on malformed bodies", () => {
    expect(parseWithFallback(
      { nonce: 42, deep_link: [] },
      TelegramLinkStartSchema,
      EMPTY_TELEGRAM_LINK_START,
      { endpoint: "POST /api/me/links/telegram/start" },
    )).toEqual(EMPTY_TELEGRAM_LINK_START);
  });
});

describe("AutopilotTelegramDestinationSchema", () => {
  it("parses an agent delivery", () => {
    const parsed = parseWithFallback(
      {
        delivers: true,
        via: "agent",
        bot_username: "sd_pm_agent_bot",
        chat_id: "-1004336001519",
        from_project_config: true,
      },
      AutopilotTelegramDestinationSchema,
      EMPTY_AUTOPILOT_TELEGRAM_DESTINATION,
      { endpoint: "GET /api/autopilots/{id}/telegram-destination" },
    );
    expect(parsed).toMatchObject({
      delivers: true,
      via: "agent",
      chat_id: "-1004336001519",
      from_project_config: true,
    });
  });

  it("fails closed on a malformed response", () => {
    const parsed = parseWithFallback(
      { delivers: "yes", chat_id: null },
      AutopilotTelegramDestinationSchema,
      EMPTY_AUTOPILOT_TELEGRAM_DESTINATION,
      { endpoint: "GET /api/autopilots/{id}/telegram-destination" },
    );
    expect(parsed).toEqual(EMPTY_AUTOPILOT_TELEGRAM_DESTINATION);
  });
});

describe("ProjectDevServersSchema", () => {
  it("parses a dev-server list and tolerates unknown extra fields", () => {
    const parsed = parseWithFallback(
      {
        dev_servers: [
          {
            user_id: "u-1",
            base_url: "https://jamshid.sdteam.uz",
            updated_at: "2026-08-14T10:00:00Z",
            future_field: true,
          },
        ],
      },
      ProjectDevServersSchema,
      EMPTY_PROJECT_DEV_SERVERS,
      { endpoint: "GET /api/projects/{id}/dev-servers" },
    );
    expect(parsed.dev_servers).toHaveLength(1);
    expect(parsed.dev_servers[0]).toMatchObject({
      user_id: "u-1",
      base_url: "https://jamshid.sdteam.uz",
    });
  });

  it("defaults a missing dev_servers array instead of failing", () => {
    const parsed = parseWithFallback(
      {},
      ProjectDevServersSchema,
      EMPTY_PROJECT_DEV_SERVERS,
      { endpoint: "GET /api/projects/{id}/dev-servers" },
    );
    expect(parsed).toEqual(EMPTY_PROJECT_DEV_SERVERS);
  });

  it("fails closed on a malformed response (null array)", () => {
    const parsed = parseWithFallback(
      { dev_servers: null },
      ProjectDevServersSchema,
      EMPTY_PROJECT_DEV_SERVERS,
      { endpoint: "GET /api/projects/{id}/dev-servers" },
    );
    expect(parsed).toEqual(EMPTY_PROJECT_DEV_SERVERS);
  });
});

// Agora Assistant — user-scoped session/message/availability contracts.
describe("AssistantRunSchema drift", () => {
  const run = {
    id: "run-1", session_id: "session-1", message_id: "message-1",
    status: "running", active_tool: "search_issues", error: null,
    created_at: "now", updated_at: "now", finished_at: null,
    version: 2, context: { workspace_id: "ws-1", timezone: "Asia/Tashkent" },
  };

  it.each(["queued", "running", "completed", "failed", "cancelled", "interrupted", "future_status"])(
    "accepts %s without crashing", (status) => {
      expect(AssistantRunSchema.parse({ ...run, status }).status).toBe(status);
    },
  );

  it("falls back for malformed detail and list responses", () => {
    expect(parseWithFallback(null, AssistantRunSchema, EMPTY_ASSISTANT_RUN, { endpoint: "run" })).toBe(EMPTY_ASSISTANT_RUN);
    expect(AssistantRunListSchema.parse(null)).toEqual([]);
  });

  it("degrades malformed optional run fields", () => {
    expect(AssistantRunSchema.parse({ ...run, active_tool: 42, context: null }).context).toEqual({ workspace_id: null });
  });

  it("preserves project and file IDs while safely dropping malformed optional fields", () => {
    expect(AssistantRunSchema.parse({ ...run, context: {
      workspace_id: "ws-1", project_id: "project-1", attachment_ids: ["file-1", "file-2"],
    } }).context).toEqual({ workspace_id: "ws-1", project_id: "project-1", attachment_ids: ["file-1", "file-2"] });
    expect(AssistantRunSchema.parse({ ...run, context: {
      workspace_id: "ws-1", project_id: 7, attachment_ids: "bad",
    } }).context).toEqual({ workspace_id: "ws-1", project_id: null, attachment_ids: [] });
  });
});

// See docs/agora-assistant-plan.md and server/internal/handler/assistant.go.
describe("AssistantSessionSchema drift", () => {
  const valid = {
    id: "11111111-1111-1111-1111-111111111111",
    title: "Plan my week",
    focus_workspace_id: "22222222-2222-2222-2222-222222222222",
    created_at: "2026-09-16T10:00:00Z",
    updated_at: "2026-09-16T10:00:00Z",
  };

  it("parses a well-formed session", () => {
    const parsed = AssistantSessionSchema.parse(valid);
    expect(parsed).toMatchObject(valid);
  });

  it("defaults focus_workspace_id to null when absent (cross-workspace session)", () => {
    const { focus_workspace_id: _omit, ...without } = valid;
    const parsed = AssistantSessionSchema.parse(without);
    expect(parsed.focus_workspace_id).toBe(null);
  });

  it("falls back to EMPTY_ASSISTANT_SESSION when a required field has the wrong type", () => {
    const parsed = parseWithFallback(
      { ...valid, id: 42 },
      AssistantSessionSchema,
      EMPTY_ASSISTANT_SESSION,
      { endpoint: "GET /api/assistant/sessions/{id}" },
    );
    expect(parsed).toBe(EMPTY_ASSISTANT_SESSION);
  });

  it("drops a malformed row from the list instead of blanking the whole switcher", () => {
    const parsed = AssistantSessionListSchema.parse([valid, { id: 123 }]);
    // A row that fails validation is caught by the list-level `.catch([])`,
    // which — unlike a per-item catch — degrades the WHOLE array. This
    // documents that behavior: parseWithFallback still returns a working
    // (empty) list rather than throwing into the UI.
    expect(parsed).toEqual([]);
  });

  it("degrades a missing sessions array response to EMPTY_ASSISTANT_SESSION_LIST", () => {
    const parsed = parseWithFallback(
      null,
      AssistantSessionListSchema,
      EMPTY_ASSISTANT_SESSION_LIST,
      { endpoint: "GET /api/assistant/sessions" },
    );
    expect(parsed).toEqual([]);
  });
});

describe("AssistantMessageSchema drift", () => {
  const userMessage = {
    id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    session_id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
    role: "user",
    content: "What's on my plate today?",
    created_at: "2026-09-16T10:00:00Z",
  };

  it("parses a plain user message", () => {
    const parsed = AssistantMessageSchema.parse(userMessage);
    expect(parsed.role).toBe("user");
    expect(parsed.tool_calls).toBeUndefined();
  });

  it("parses an assistant message carrying tool_calls", () => {
    const parsed = AssistantMessageSchema.parse({
      ...userMessage,
      role: "assistant",
      content: "",
      tool_calls: [{ id: "call_1", name: "list_my_issues", arguments: "{}" }],
    });
    expect(parsed.tool_calls).toHaveLength(1);
    expect(parsed.tool_calls?.[0]).toMatchObject({ name: "list_my_issues" });
  });

  it("keeps an unrecognized future role as-is (enum drift downgrades, not crashes)", () => {
    // `role` is a lenient string, not z.enum — a future role the server adds
    // must still parse; the UI's role switch has a default branch.
    const parsed = AssistantMessageSchema.parse({ ...userMessage, role: "system" });
    expect(parsed.role).toBe("system");
  });

  it("degrades a malformed tool_calls entry to [] instead of failing the row", () => {
    const parsed = AssistantMessageSchema.parse({
      ...userMessage,
      role: "assistant",
      tool_calls: "not-an-array",
    });
    expect(parsed.tool_calls).toEqual([]);
  });

  it("accepts an arbitrary tool_result shape (tool-specific JSON)", () => {
    const parsed = AssistantMessageSchema.parse({
      id: "cccccccc-cccc-cccc-cccc-cccccccccccc",
      session_id: userMessage.session_id,
      role: "tool",
      content: "{}",
      tool_call_id: "call_1",
      tool_name: "create_issue",
      tool_result: { issue_key: "MUL-931", url: "/acme/issues/MUL-931" },
      created_at: userMessage.created_at,
    });
    expect(parsed.tool_result).toMatchObject({ issue_key: "MUL-931" });
  });

  it("degrades a null messages array to EMPTY_ASSISTANT_MESSAGE_LIST", () => {
    const parsed = parseWithFallback(
      { not: "an array" },
      AssistantMessageListSchema,
      EMPTY_ASSISTANT_MESSAGE_LIST,
      { endpoint: "GET /api/assistant/sessions/{id}/messages" },
    );
    expect(parsed).toEqual([]);
  });
});

describe("AssistantAvailabilitySchema drift", () => {
  it("parses a well-formed enabled response", () => {
    const parsed = AssistantAvailabilitySchema.parse({
      enabled: true,
      model_label: "Agora (glm-4.5-flash)",
    });
    expect(parsed).toEqual({ enabled: true, model_label: "Agora (glm-4.5-flash)" });
  });

  // Defaults to disabled: an older server predating this endpoint, or a
  // drifted response missing `enabled`, must hide the nav item / page rather
  // than render a chat UI that 503s on first send.
  it("defaults to disabled when the response is missing fields", () => {
    const parsed = parseWithFallback(
      {},
      AssistantAvailabilitySchema,
      EMPTY_ASSISTANT_AVAILABILITY,
      { endpoint: "GET /api/assistant/availability" },
    );
    expect(parsed.enabled).toBe(false);
  });

  it("falls back to EMPTY_ASSISTANT_AVAILABILITY when enabled has the wrong type", () => {
    const parsed = parseWithFallback(
      { enabled: "yes", model_label: "Agora" },
      AssistantAvailabilitySchema,
      EMPTY_ASSISTANT_AVAILABILITY,
      { endpoint: "GET /api/assistant/availability" },
    );
    expect(parsed).toBe(EMPTY_ASSISTANT_AVAILABILITY);
  });
});

// Confirmation binding — the confirm/reject endpoints ship after this UI, so
// "the runtime doesn't have them yet" is a real production state. See
// docs/agora-assistant-final-plan.md ("Pinned wire contract").
describe("AssistantOperationSchema drift", () => {
  const operation = {
    id: "op-1", tool_name: "delete_issue", summary: "Delete MUL-1",
    workspace_slug: "acme", target: { type: "issue", identifier: "MUL-1", title: "Test" },
    status: "confirmed", outcome: "succeeded",
  };

  it.each(["pending", "confirmed", "rejected", "expired", "uncertain", "future_status"])(
    "preserves %s for safe UI handling", (status) => {
      expect(AssistantOperationSchema.parse({ ...operation, status }).status).toBe(status);
    },
  );

  it("preserves known and unknown outcomes", () => {
    expect(AssistantOperationSchema.parse({ ...operation, outcome: "failed" }).outcome).toBe("failed");
    expect(AssistantOperationSchema.parse({ ...operation, outcome: "future_outcome" }).outcome).toBe("future_outcome");
  });

  it("fails closed on a malformed receipt and list", () => {
    expect(parseWithFallback({ ...operation, status: 42 }, AssistantOperationSchema, EMPTY_ASSISTANT_OPERATION, { endpoint: "operation" })).toBe(EMPTY_ASSISTANT_OPERATION);
    expect(AssistantOperationListSchema.parse(null)).toEqual([]);
  });

  // Plan operations — docs/assistant-domain-plan.md, "3a wire contract".
  it("reads a single operation as kind-less with no rows", () => {
    const parsed = AssistantOperationSchema.parse(operation);
    expect(parsed.kind).toBe("");
    expect(parsed.items).toEqual([]);
  });

  it("keeps the plan rows and every per-item outcome, known or not", () => {
    const parsed = AssistantOperationSchema.parse({
      ...operation,
      kind: "plan",
      items: [
        { index: 0, tool: "create_sprint", summary: "Create sprint 12", outcome: "ok", identifier: "Sprint 12" },
        { index: 1, outcome: "deferred_to_agent" },
      ],
    });
    expect(parsed.kind).toBe("plan");
    expect(parsed.items).toHaveLength(2);
    expect(parsed.items[0]).toMatchObject({ index: 0, tool: "create_sprint", outcome: "ok" });
    // An outcome this build doesn't know must survive the parse — the card
    // downgrades it to a neutral row (enum-drift rule).
    expect(parsed.items[1]?.outcome).toBe("deferred_to_agent");
  });

  it("empties a null or non-list items field instead of failing the operation", () => {
    expect(AssistantOperationSchema.parse({ ...operation, kind: "plan", items: null }).items).toEqual([]);
    expect(AssistantOperationSchema.parse({ ...operation, kind: "plan", items: "two" }).items).toEqual([]);
    expect(AssistantOperationSchema.parse({ ...operation, kind: 7 }).kind).toBe("");
  });

  it("defaults the fields a single plan row dropped", () => {
    const parsed = AssistantOperationSchema.parse({ ...operation, kind: "plan", items: [{ index: "1" }] });
    expect(parsed.items[0]).toMatchObject({ index: -1, tool: "", summary: "", outcome: "", error: "" });
  });
});

describe("AssistantOperationDecisionSchema drift", () => {
  it("parses the decision body the confirm handler returns", () => {
    expect(
      AssistantOperationDecisionSchema.parse({
        operation: { id: "op-1", tool_name: "delete_issue", status: "confirmed" },
        message: { id: "msg-9", role: "tool", tool_name: "delete_issue" },
      }),
    ).toMatchObject({
      operation: { id: "op-1", status: "confirmed" },
      message: { id: "msg-9" },
    });
  });

  it("defaults every field — the receipt message is the real outcome", () => {
    const parsed = parseWithFallback(
      {},
      AssistantOperationDecisionSchema,
      EMPTY_ASSISTANT_OPERATION_DECISION,
      { endpoint: "POST /api/assistant/operations/{id}/confirm" },
    );
    expect(parsed).toMatchObject({ operation: { id: "", status: "" }, message: { id: "" } });
  });

  it("survives wrong field types instead of throwing into the card", () => {
    const parsed = AssistantOperationDecisionSchema.parse({
      operation: { id: null, status: 204 },
      message: "msg-9",
    });
    expect(parsed).toMatchObject({ operation: { id: "", status: "" }, message: { id: "" } });
  });

  it("falls back for a non-object body", () => {
    const parsed = parseWithFallback(
      "confirmed",
      AssistantOperationDecisionSchema,
      EMPTY_ASSISTANT_OPERATION_DECISION,
      { endpoint: "POST /api/assistant/operations/{id}/reject" },
    );
    expect(parsed).toBe(EMPTY_ASSISTANT_OPERATION_DECISION);
  });

  it("parses the flat `{status, items}` body a PLAN confirm answers", () => {
    const parsed = AssistantOperationDecisionSchema.parse({
      status: "confirmed",
      items: [
        { index: 0, outcome: "ok", identifier: "MUL-9" },
        { index: 1, outcome: "failed", error: "Issue archived" },
        { index: 2, outcome: "not_run" },
      ],
    });
    expect(parsed.status).toBe("confirmed");
    expect(parsed.items.map((item) => item.outcome)).toEqual(["ok", "failed", "not_run"]);
  });

  it("keeps a plan decision usable when items are missing, null or wrongly typed", () => {
    expect(AssistantOperationDecisionSchema.parse({ status: "confirmed" }).items).toEqual([]);
    expect(AssistantOperationDecisionSchema.parse({ status: "confirmed", items: null }).items).toEqual([]);
    expect(AssistantOperationDecisionSchema.parse({ status: "confirmed", items: 3 }).items).toEqual([]);
  });
});

// Assistant artifacts — see docs/agora-assistant-artifacts-plan.md §5.
// These endpoints ship AFTER this frontend, so every one of these cases is a
// real production shape for some window of time, not a hypothetical.
describe("AssistantArtifactSchema drift", () => {
  const valid = {
    id: "dddddddd-dddd-dddd-dddd-dddddddddddd",
    session_id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
    title: "Agent usage by day",
    kind: "chart",
    content: '{"type":"bar","x":"day","series":[{"key":"runs"}],"rows":[]}',
    version: 2,
    created_at: "2026-09-16T10:00:00Z",
    updated_at: "2026-09-16T11:00:00Z",
  };

  it("parses a well-formed artifact", () => {
    const parsed = AssistantArtifactSchema.parse(valid);
    expect(parsed).toMatchObject(valid);
  });

  it("defaults a missing version to 1 rather than rendering `vNaN`", () => {
    const { version: _omit, ...without } = valid;
    const parsed = AssistantArtifactSchema.parse(without);
    expect(parsed.version).toBe(1);
  });

  it("keeps an unknown future kind as-is (enum drift downgrades, not crashes)", () => {
    // The pane's kind switch has a default branch that shows raw content —
    // failing to parse here would instead blank a renderable artifact.
    const parsed = AssistantArtifactSchema.parse({ ...valid, kind: "mermaid" });
    expect(parsed.kind).toBe("mermaid");
  });

  it("survives a drifted cosmetic field (version as a string) without losing content", () => {
    const parsed = AssistantArtifactSchema.parse({ ...valid, version: "2" });
    expect(parsed.version).toBe(1);
    expect(parsed.content).toBe(valid.content);
  });

  it("falls back to EMPTY_ASSISTANT_ARTIFACT when content has the wrong type", () => {
    const parsed = parseWithFallback(
      { ...valid, content: { rows: [] } },
      AssistantArtifactSchema,
      EMPTY_ASSISTANT_ARTIFACT,
      { endpoint: "GET /api/assistant/artifacts/{id}" },
    );
    expect(parsed).toBe(EMPTY_ASSISTANT_ARTIFACT);
  });

  it("degrades a null response (endpoint not deployed yet) to the empty artifact", () => {
    const parsed = parseWithFallback(
      null,
      AssistantArtifactSchema,
      EMPTY_ASSISTANT_ARTIFACT,
      { endpoint: "GET /api/assistant/artifacts/{id}" },
    );
    expect(parsed.id).toBe("");
  });
});

// Pinned reports — docs/assistant-domain-plan.md Phase 2a. Same posture as the
// artifact schemas above: these routes ship after (or beside) this frontend,
// so a 404, a drifted row and a re-shaped envelope are all real production
// shapes for some window of time.
describe("pinned report schemas drift", () => {
  const row = {
    pin_id: "11111111-1111-1111-1111-111111111111",
    artifact_id: "dddddddd-dddd-dddd-dddd-dddddddddddd",
    title: "Sprint report",
    kind: "markdown",
    version: 3,
    updated_at: "2026-09-18T10:00:00Z",
    created_at: "2026-09-17T10:00:00Z",
    pinned_by: { id: "u-1", name: "Jamshid" },
    owner: { id: "u-1", name: "Jamshid" },
  };

  it("parses a well-formed row and a full report", () => {
    expect(PinnedReportSummarySchema.parse(row)).toMatchObject(row);
    expect(PinnedReportSchema.parse({ ...row, content: "# body" })).toMatchObject({
      ...row,
      content: "# body",
    });
  });

  it("unwraps the {reports: [...]} envelope", () => {
    const parsed = parseWithFallback(
      { reports: [row] },
      ProjectReportsResponseSchema,
      { reports: EMPTY_PINNED_REPORT_LIST },
      { endpoint: "GET /api/projects/{id}/reports" },
    );
    expect(parsed.reports).toHaveLength(1);
    expect(parsed.reports[0]?.pin_id).toBe(row.pin_id);
  });

  it("degrades a missing field to the row's fallback rather than dropping the report", () => {
    // version is cosmetic ("v{n}" in the subtitle); the row is still openable.
    const { version: _omit, ...without } = row;
    expect(PinnedReportSummarySchema.parse(without).version).toBe(1);
  });

  it("degrades a wrong-typed cosmetic field without losing the body", () => {
    const parsed = PinnedReportSchema.parse({ ...row, version: "3", title: 7, content: "# body" });
    expect(parsed.version).toBe(1);
    expect(parsed.title).toBe("");
    expect(parsed.content).toBe("# body");
  });

  it("degrades a malformed actor to an empty byline", () => {
    const parsed = PinnedReportSummarySchema.parse({ ...row, owner: "Jamshid" });
    expect(parsed.owner).toEqual({ id: "", name: "" });
    expect(parsed.pin_id).toBe(row.pin_id);
  });

  it("degrades a null reports array to an empty list (section then renders nothing)", () => {
    const parsed = parseWithFallback(
      { reports: null },
      ProjectReportsResponseSchema,
      { reports: EMPTY_PINNED_REPORT_LIST },
      { endpoint: "GET /api/projects/{id}/reports" },
    );
    expect(parsed.reports).toEqual([]);
  });

  it("degrades one malformed row to an empty list", () => {
    expect(ProjectReportsResponseSchema.parse({ reports: [row, { pin_id: 7 }] }).reports).toEqual([]);
  });

  it("degrades a bare array (envelope dropped) to an empty list", () => {
    const parsed = parseWithFallback(
      [row],
      ProjectReportsResponseSchema,
      { reports: EMPTY_PINNED_REPORT_LIST },
      { endpoint: "GET /api/projects/{id}/reports" },
    );
    expect(parsed.reports).toEqual([]);
  });

  it("degrades a null response (endpoint not deployed yet) to the empty report", () => {
    const parsed = parseWithFallback(null, PinnedReportSchema, EMPTY_PINNED_REPORT, {
      endpoint: "GET /api/reports/{pinId}",
    });
    // pin_id "" is what the viewer checks before it renders a body.
    expect(parsed).toBe(EMPTY_PINNED_REPORT);
    expect(parsed.pin_id).toBe("");
  });

  it("falls back to the empty report when content has the wrong type", () => {
    const parsed = parseWithFallback(
      { ...row, content: { body: "x" } },
      PinnedReportSchema,
      EMPTY_PINNED_REPORT,
      { endpoint: "GET /api/reports/{pinId}" },
    );
    expect(parsed.pin_id).toBe("");
  });

  it("keeps an unknown future kind as-is (enum drift downgrades, not crashes)", () => {
    expect(PinnedReportSummarySchema.parse({ ...row, kind: "mermaid" }).kind).toBe("mermaid");
  });

  it("accepts either spelling of the pin identifier on the create response", () => {
    // The create route names it `id`; the list route names the same value
    // `pin_id`. Both parse, and the client normalizes to one field.
    expect(ReportPinSchema.parse({ id: "p-1", project_id: "proj-1" }).id).toBe("p-1");
    expect(ReportPinSchema.parse({ pin_id: "p-1", project_id: "proj-1" }).pin_id).toBe("p-1");
    // A drifted (non-string) id degrades to "" instead of throwing — the UI
    // then treats the pin as created but unaddressable, offering no Unpin.
    expect(ReportPinSchema.parse({ id: 7, project_id: "proj-1" }).id).toBe("");
  });
});

// Scheduled refresh — docs/assistant-domain-plan.md Phase 2b. The rule these
// tests pin down: a schedule is decoration on a report row, so no schedule
// shape may cost the row. Every case below asserts the ROW survives.
describe("report schedule schema drift", () => {
  const row = {
    pin_id: "11111111-1111-1111-1111-111111111111",
    artifact_id: "dddddddd-dddd-dddd-dddd-dddddddddddd",
    title: "Sprint report",
    kind: "markdown",
    version: 3,
    updated_at: "2026-09-18T10:00:00Z",
    created_at: "2026-09-17T10:00:00Z",
    pinned_by: { id: "u-1", name: "Jamshid" },
    owner: { id: "u-1", name: "Jamshid" },
  };
  const schedule = {
    frequency: "weekly",
    time: "09:00",
    weekday: 1,
    timezone: "Asia/Tashkent",
    enabled: true,
    last_run_at: "2026-09-18T04:00:00Z",
    last_status: "ok",
    next_run_at: "2026-09-25T04:00:00Z",
  };

  it("parses a full schedule on a row", () => {
    const parsed = PinnedReportSummarySchema.parse({ ...row, schedule });
    expect(parsed.schedule).toMatchObject(schedule);
  });

  it("leaves the row untouched when the backend predates 2b (no schedule key)", () => {
    const parsed = PinnedReportSummarySchema.parse(row);
    // Not null, not a synthetic object: simply absent, which reads as "no
    // cadence badge" everywhere downstream.
    expect(parsed.schedule).toBeUndefined();
    expect(parsed.pin_id).toBe(row.pin_id);
  });

  it("keeps an explicit null schedule (pinned, never scheduled)", () => {
    expect(PinnedReportSummarySchema.parse({ ...row, schedule: null }).schedule).toBeNull();
  });

  it("drops the schedule — never the report — on an unknown frequency", () => {
    // "hourly" is exactly the drift 2b's preset list forbids. The badge has no
    // honest rendering for it, so the schedule goes and the row stays.
    const parsed = PinnedReportSummarySchema.parse({
      ...row,
      schedule: { ...schedule, frequency: "hourly" },
    });
    expect(parsed.schedule).toBeNull();
    expect(parsed.title).toBe("Sprint report");
  });

  it("drops a wholly wrong-typed schedule without failing the row", () => {
    const parsed = PinnedReportSummarySchema.parse({ ...row, schedule: "daily at 9" });
    expect(parsed.schedule).toBeNull();
    expect(parsed.pin_id).toBe(row.pin_id);
  });

  it("keeps a list readable when one row's schedule drifted", () => {
    const parsed = ProjectReportsResponseSchema.parse({
      reports: [{ ...row, schedule: { ...schedule, frequency: 7 } }],
    });
    expect(parsed.reports).toHaveLength(1);
    expect(parsed.reports[0]?.schedule).toBeNull();
  });

  it("degrades wrong-typed schedule fields in place", () => {
    const parsed = PinnedReportSummarySchema.parse({
      ...row,
      schedule: { ...schedule, time: 900, timezone: null, enabled: "yes" },
    });
    expect(parsed.schedule).toMatchObject({
      frequency: "weekly",
      time: "",
      timezone: "",
      // A schedule the server sent is live until it says otherwise.
      enabled: true,
    });
  });

  it("tolerates a null weekday (the daily / weekdays presets send one)", () => {
    const parsed = ReportScheduleSchema.parse({ ...schedule, frequency: "daily", weekday: null });
    expect(parsed.weekday).toBeNull();
    // A missing key degrades to the same null rather than failing the object.
    const { weekday: _omit, ...withoutWeekday } = schedule;
    expect(ReportScheduleSchema.parse({ ...withoutWeekday, frequency: "daily" }).weekday).toBeNull();
  });

  it("downgrades an unknown last_status to \"\" (no indicator, no crash)", () => {
    expect(ReportScheduleSchema.parse({ ...schedule, last_status: "throttled" }).last_status).toBe("");
    expect(ReportScheduleSchema.parse({ ...schedule, last_status: null }).last_status).toBe("");
    // "failed" is the one value that renders something, so it must survive.
    expect(ReportScheduleSchema.parse({ ...schedule, last_status: "failed" }).last_status).toBe("failed");
    // "running" is a real transient value (written when the scheduler claims
    // the slot), so it parses as itself rather than degrading to "".
    expect(ReportScheduleSchema.parse({ ...schedule, last_status: "running" }).last_status).toBe("running");
  });

  it("unwraps the PUT envelope and falls back to null on a drifted body", () => {
    const ok = parseWithFallback(
      { schedule },
      ReportScheduleResponseSchema,
      EMPTY_REPORT_SCHEDULE_RESPONSE,
      { endpoint: "PUT /api/assistant/artifacts/{id}/pins/{pinId}/schedule" },
    );
    expect(ok.schedule).toMatchObject({ frequency: "weekly", time: "09:00" });

    const drifted = parseWithFallback(
      { schedule: { frequency: "never" } },
      ReportScheduleResponseSchema,
      EMPTY_REPORT_SCHEDULE_RESPONSE,
      { endpoint: "PUT /api/assistant/artifacts/{id}/pins/{pinId}/schedule" },
    );
    expect(drifted.schedule).toBeNull();

    const missing = parseWithFallback(
      null,
      ReportScheduleResponseSchema,
      EMPTY_REPORT_SCHEDULE_RESPONSE,
      { endpoint: "PUT /api/assistant/artifacts/{id}/pins/{pinId}/schedule" },
    );
    expect(missing.schedule).toBeNull();
  });
});

describe("StaleIssuesResponseSchema", () => {
  const endpoint = { endpoint: "GET /api/issues/staleness" };
  const row = {
    issue_id: "issue-1",
    identifier: "MUL-123",
    title: "Wire the staleness endpoint",
    status: "in_review",
    reason: "review_done",
    since: "2026-09-16T00:00:00Z",
  };

  function parse(data: unknown) {
    return parseWithFallback(data, StaleIssuesResponseSchema, EMPTY_STALE_ISSUES, endpoint);
  }

  it("parses a well-formed response", () => {
    expect(parse({ stale: [row] })).toEqual({ stale: [row] });
  });

  it("falls back to an empty list when the stale key is missing entirely", () => {
    // An older backend that doesn't serve this shape yet costs the nudge,
    // not the page.
    expect(parse({})).toEqual({ stale: [] });
    expect(parse(null)).toEqual({ stale: [] });
    expect(parse("not json at all")).toEqual({ stale: [] });
  });

  it("degrades a null / wrong-typed array to an empty list", () => {
    expect(parse({ stale: null })).toEqual({ stale: [] });
    expect(parse({ stale: "nope" })).toEqual({ stale: [] });
    expect(parse({ stale: 7 })).toEqual({ stale: [] });
  });

  it("keeps a row whose reason the client has never heard of", () => {
    // Enum drift downgrades, not crashes: a fifth rule from a newer server
    // must still reach the UI, which renders it generically.
    const drifted = parse({ stale: [{ ...row, reason: "date_slipped_in_slack" }] });
    expect(drifted.stale).toHaveLength(1);
    expect(drifted.stale[0]?.reason).toBe("date_slipped_in_slack");
  });

  it("fills a partial row instead of dropping the response", () => {
    const partial = parse({ stale: [{ issue_id: "issue-2" }] });
    expect(partial.stale[0]).toEqual({
      issue_id: "issue-2",
      identifier: "",
      title: "",
      status: "",
      reason: "",
      since: "",
    });
  });

  it("degrades a wrong-typed row to no staleness at all", () => {
    // A number where a string belongs is real drift, not an omission — the
    // array `.catch([])` swallows it so the rest of the page keeps rendering.
    expect(parse({ stale: [{ ...row, issue_id: 42 }] })).toEqual({ stale: [] });
  });

  it("ignores unknown extra fields a newer server adds", () => {
    const wider = parse({ stale: [{ ...row, confidence: 0.8 }], computed_at: "now" });
    expect(wider.stale[0]?.issue_id).toBe("issue-1");
  });
});

// Escalations — the one endpoint whose failure mode is a run stuck forever.
// If a drifted response blanks the card, nobody answers and nobody knows why,
// so every one of these cases must degrade to "no escalation" (the card
// renders nothing) or to a filled-in row, never to a throw.
describe("EscalationSchema / EscalationListSchema", () => {
  const row = {
    id: "esc-1",
    workspace_id: "ws-1",
    issue_id: "issue-1",
    task_id: "task-1",
    agent_id: "agent-1",
    kind: "question",
    prompt: "CSV or XLSX?",
    detail: "Read the issue; it does not say.",
    options: ["CSV", "XLSX"],
    risk_tier: "guarded",
    status: "open",
    answer: "",
    answered_by: "",
    answered_at: "",
    resumed_task_id: "",
    raised_at: "2026-09-20T10:00:00Z",
  };
  const parseList = (data: unknown) =>
    parseWithFallback(data, EscalationListSchema, EMPTY_ESCALATION_LIST, {
      endpoint: "GET /api/issues/:id/escalations",
    });
  const parseOne = (data: unknown) =>
    parseWithFallback(data, EscalationSchema, EMPTY_ESCALATION, {
      endpoint: "POST /api/escalations/:id/resolve",
    });

  it("parses a well-formed list", () => {
    expect(parseList({ escalations: [row] })).toEqual({ escalations: [row] });
  });

  it("falls back to no escalations on a shape it cannot read at all", () => {
    expect(parseList({})).toEqual({ escalations: [] });
    expect(parseList(null)).toEqual({ escalations: [] });
    expect(parseList("not json at all")).toEqual({ escalations: [] });
  });

  it("degrades a null escalations array to an empty list", () => {
    // A Go `[]TaskEscalation(nil)` marshals to null the day someone drops
    // emit_empty_slices. That must read as "none", not as a crash.
    expect(parseList({ escalations: null })).toEqual({ escalations: [] });
  });

  it("treats null options as no options rather than dropping the row", () => {
    const parsed = parseList({ escalations: [{ ...row, options: null }] });
    expect(parsed.escalations).toHaveLength(1);
    expect(parsed.escalations[0]?.options).toEqual([]);
  });

  it("treats a null answer as an empty answer", () => {
    // answer is NULL in the database while an escalation is open.
    expect(parseOne({ ...row, answer: null }).answer).toBe("");
  });

  it("keeps a kind and a status the client has never heard of", () => {
    // Enum drift downgrades, not crashes: the card renders a new kind
    // generically instead of vanishing.
    const parsed = parseOne({ ...row, kind: "needs_design_decision", status: "deferred" });
    expect(parsed.kind).toBe("needs_design_decision");
    expect(parsed.status).toBe("deferred");
  });

  it("fills a partial row instead of dropping the whole response", () => {
    const parsed = parseOne({ id: "esc-9" });
    expect(parsed.id).toBe("esc-9");
    expect(parsed.prompt).toBe("");
    expect(parsed.options).toEqual([]);
    expect(parsed.status).toBe("open");
  });

  it("degrades a wrong-typed row to no escalation at all", () => {
    expect(parseList({ escalations: [{ ...row, prompt: 42 }] })).toEqual({ escalations: [] });
    expect(parseOne({ ...row, id: 42 })).toEqual(EMPTY_ESCALATION);
  });

  it("ignores unknown extra fields a newer server adds", () => {
    const parsed = parseOne({ ...row, escalated_by_policy: true });
    expect(parsed.id).toBe("esc-1");
  });
});

// ---------------------------------------------------------------------------
// The decision queue (docs/orchestration-upgrade-plan.md §A2). Its failure
// mode is the product's worst: a blank list says "nothing needs you" while
// agents sit parked. Every case below must degrade to a SMALLER list or a
// filled-in row — never to a throw, and never to silently dropping a row the
// UI could still have opened.
// ---------------------------------------------------------------------------
describe("DecisionQueueResponseSchema", () => {
  const endpoint = { endpoint: "GET /api/issues/decision-queue" };
  const row = {
    kind: "escalation",
    issue_id: "issue-1",
    identifier: "MUL-123",
    title: "Export invoices as PDF",
    status: "in_progress",
    project_id: "proj-1",
    risk_tier: "critical",
    risk_tier_source: "risk_map",
    since: "2026-09-20T10:00:00Z",
    age_hours: 2,
    needed: "Answer: CSV or XLSX?",
    needed_code: "escalation_open",
    score: 174,
    stale_reason: "",
    open_pr_count: 0,
    labels: ["risk:critical"],
    escalation: {
      id: "esc-1",
      kind: "question",
      prompt: "CSV or XLSX?",
      detail: "Read the issue; it does not say.",
      options: ["CSV", "XLSX"],
    },
  };
  const counts = { escalation: 1, merge_ready: 0, qa_failed: 0, review_failed: 0 };
  const zeroCounts = { escalation: 0, merge_ready: 0, qa_failed: 0, review_failed: 0 };

  function parse(data: unknown) {
    return parseWithFallback(data, DecisionQueueResponseSchema, EMPTY_DECISION_QUEUE, endpoint);
  }

  it("parses a well-formed response", () => {
    expect(parse({ items: [row], total: 1, counts })).toEqual({ items: [row], total: 1, counts });
  });

  it("falls back to an empty queue on a shape it cannot read at all", () => {
    expect(parse(null)).toEqual(EMPTY_DECISION_QUEUE);
    expect(parse("not json at all")).toEqual(EMPTY_DECISION_QUEUE);
  });

  it("reads a missing items key as an empty queue", () => {
    // An older backend that does not serve this route yet.
    expect(parse({}).items).toEqual([]);
    expect(parse({}).total).toBe(0);
    expect(parse({}).counts).toEqual(zeroCounts);
  });

  it("degrades a null / wrong-typed items array to an empty list", () => {
    expect(parse({ items: null }).items).toEqual([]);
    expect(parse({ items: "nope" }).items).toEqual([]);
    expect(parse({ items: 7 }).items).toEqual([]);
  });

  it("keeps a row whose kind this build has never heard of", () => {
    // Enum drift downgrades, not crashes: the row still renders and still
    // opens its issue, generically.
    const drifted = parse({ items: [{ ...row, kind: "deploy_approval" }], total: 1, counts });
    expect(drifted.items).toHaveLength(1);
    expect(drifted.items[0]?.kind).toBe("deploy_approval");
  });

  it("keeps a row whose risk tier is null, with no opinion on the tier", () => {
    // `""` means "this project has no risk map" — the row renders NO chip,
    // which is not the same as rendering a "safe" one.
    const parsed = parse({ items: [{ ...row, risk_tier: null }], total: 1, counts });
    expect(parsed.items[0]?.risk_tier).toBe("");
  });

  it("keeps a row whose age fields are wrong-typed", () => {
    const parsed = parse({ items: [{ ...row, age_hours: "2", since: null }], total: 1, counts });
    expect(parsed.items[0]?.age_hours).toBe(0);
    expect(parsed.items[0]?.since).toBe("");
    expect(parsed.items[0]?.issue_id).toBe("issue-1");
  });

  it("degrades a null labels array and a wrong-typed escalation without losing the row", () => {
    const parsed = parse({
      items: [{ ...row, labels: null, escalation: "soon" }],
      total: 1,
      counts,
    });
    expect(parsed.items[0]?.labels).toEqual([]);
    expect(parsed.items[0]?.escalation).toBeUndefined();
    expect(parsed.items[0]?.issue_id).toBe("issue-1");
  });

  it("fills a partial row instead of dropping it", () => {
    expect(parse({ items: [{ issue_id: "issue-2" }] }).items[0]).toEqual({
      kind: "",
      issue_id: "issue-2",
      identifier: "",
      title: "",
      status: "",
      project_id: "",
      risk_tier: "",
      risk_tier_source: "",
      since: "",
      age_hours: 0,
      needed: "",
      needed_code: "",
      score: 0,
      stale_reason: "",
      open_pr_count: 0,
      labels: [],
    });
  });

  it("drops one unreadable row and lists the rest", () => {
    // Row-by-row parsing: a single bad row costs that row, not the queue.
    const parsed = parse({
      items: [row, 42, null, { ...row, issue_id: "issue-2" }],
      total: 4,
      counts,
    });
    expect(parsed.items.map((i) => i.issue_id)).toEqual(["issue-1", "issue-2"]);
  });

  it("degrades a wrong-typed total / counts without losing the rows", () => {
    const parsed = parse({ items: [row], total: "many", counts: "soon" });
    expect(parsed.items).toHaveLength(1);
    expect(parsed.total).toBe(0);
    expect(parsed.counts).toEqual(zeroCounts);
  });

  it("keeps a counts object a newer server widened", () => {
    const parsed = parse({
      items: [row],
      total: 2,
      counts: { ...counts, deploy_approval: 1 },
    });
    expect(parsed.counts.escalation).toBe(1);
    expect((parsed.counts as unknown as Record<string, number>).deploy_approval).toBe(1);
  });

  it("ignores unknown extra fields a newer server adds", () => {
    const wider = parse({ items: [{ ...row, blast_radius: 3 }], total: 1, counts, computed_at: "now" });
    expect(wider.items[0]?.issue_id).toBe("issue-1");
  });
});

// ---------------------------------------------------------------------------
// The project risk map (§A1) — a list of module entries. The tier vocabulary
// is server-owned, so an entry whose tier this build has no copy for must
// round-trip rather than be deleted on the next save.
// ---------------------------------------------------------------------------
describe("RiskMapResponseSchema", () => {
  const endpoint = { endpoint: "GET /api/projects/{id}/risk-map" };
  const entry = {
    module: "auth",
    tier: "critical",
    paths: ["server/internal/auth/**"],
    owner: "jamshid",
    notes: "Session + token issuance",
  };
  const map = {
    project_id: "proj-1",
    configured: true,
    risk_map: [entry],
    default_tier: "guarded",
    tiers: ["critical", "guarded", "safe"],
    max_entries: 40,
  };
  const parse = (data: unknown) =>
    parseWithFallback(data, RiskMapResponseSchema, EMPTY_RISK_MAP, endpoint);

  it("parses a well-formed map", () => {
    expect(parse(map)).toEqual(map);
  });

  it("reads a missing / null body as an unconfigured project", () => {
    expect(parse({})).toEqual(EMPTY_RISK_MAP);
    expect(parse({ risk_map: null }).risk_map).toEqual([]);
    expect(parse(null)).toEqual(EMPTY_RISK_MAP);
  });

  it("keeps an entry whose tier this build has no copy for", () => {
    const parsed = parse({ ...map, risk_map: [{ ...entry, tier: "nuclear" }], tiers: ["nuclear"] });
    expect(parsed.risk_map[0]?.tier).toBe("nuclear");
    expect(parsed.tiers).toEqual(["nuclear"]);
  });

  it("degrades a wrong-typed paths list to no globs, keeping the entry", () => {
    const parsed = parse({ ...map, risk_map: [{ ...entry, paths: "server/**" }] });
    expect(parsed.risk_map[0]?.paths).toEqual([]);
    expect(parsed.risk_map[0]?.module).toBe("auth");
  });

  it("drops one unreadable entry and keeps the rest", () => {
    const parsed = parse({ ...map, risk_map: [entry, 42, { ...entry, module: "billing" }] });
    expect(parsed.risk_map.map((e) => e.module)).toEqual(["auth", "billing"]);
  });

  it("degrades a wrong-typed cap / default tier without losing the entries", () => {
    const parsed = parse({ ...map, max_entries: "forty", default_tier: null });
    expect(parsed.max_entries).toBe(0);
    expect(parsed.default_tier).toBe("");
    expect(parsed.risk_map).toHaveLength(1);
  });

  it("ignores unknown extra fields a newer server adds", () => {
    const parsed = parse({ ...map, derived_from_diff: true });
    expect(parsed.risk_map[0]?.module).toBe("auth");
  });
});

// ── In-app Changes view ─────────────────────────────────────────────────────
//
// This section claims to show what an agent changed. A claim is worse than
// silence when it is wrong, so the contract is: a drifted field costs a column,
// a drifted ROW is dropped, and an unreadable response renders nothing at all.

describe("IssueChangesResponseSchema", () => {
  const file = {
    path: "server/internal/handler/auth.go",
    status: "modified",
    additions: 10,
    deletions: 2,
  };
  const change = {
    pr_number: 42,
    title: "Fix the login redirect",
    state: "open",
    html_url: "https://github.com/acme/widget/pull/42",
    repo_owner: "acme",
    repo_name: "widget",
    additions: 12,
    deletions: 3,
    changed_files: 1,
    files_source: "github",
    files: [file],
  };
  const parse = (raw: unknown) =>
    parseWithFallback(raw, IssueChangesResponseSchema, EMPTY_ISSUE_CHANGES, {
      endpoint: "GET /api/issues/{id}/changes",
    });

  it("reads a well-formed response", () => {
    const parsed = parse({ changes: [change] });
    expect(parsed.changes).toHaveLength(1);
    expect(parsed.changes[0]?.files[0]?.path).toBe("server/internal/handler/auth.go");
    expect(parsed.changes[0]?.files[0]?.additions).toBe(10);
  });

  it("reads a missing / null changes array as nothing to show", () => {
    expect(parse({})).toEqual(EMPTY_ISSUE_CHANGES);
    expect(parse({ changes: null })).toEqual(EMPTY_ISSUE_CHANGES);
    expect(parse(null)).toEqual(EMPTY_ISSUE_CHANGES);
  });

  it("degrades wrong-typed counts to 0 without losing the file", () => {
    const parsed = parse({
      changes: [{ ...change, files: [{ ...file, additions: "ten", deletions: null }] }],
    });
    expect(parsed.changes[0]?.files[0]?.path).toBe("server/internal/handler/auth.go");
    expect(parsed.changes[0]?.files[0]?.additions).toBe(0);
    expect(parsed.changes[0]?.files[0]?.deletions).toBe(0);
  });

  it("keeps a file whose status this build has no glyph for", () => {
    const parsed = parse({ changes: [{ ...change, files: [{ ...file, status: "teleported" }] }] });
    expect(parsed.changes[0]?.files[0]?.status).toBe("teleported");
  });

  it("drops a file row with no path and keeps its siblings", () => {
    const parsed = parse({
      changes: [{ ...change, files: [file, 42, { ...file, path: "docs/a.md" }] }],
    });
    expect(parsed.changes[0]?.files.map((f) => f.path)).toEqual([
      "server/internal/handler/auth.go",
      "docs/a.md",
    ]);
  });

  it("degrades a wrong-typed files list to no files, keeping the PR header", () => {
    const parsed = parse({ changes: [{ ...change, files: "server/a.go" }] });
    expect(parsed.changes[0]?.pr_number).toBe(42);
    expect(parsed.changes[0]?.files).toEqual([]);
  });

  it("defaults an absent files_source to none, so counts are read as unknown", () => {
    const parsed = parse({ changes: [{ ...change, files_source: undefined }] });
    expect(parsed.changes[0]?.files_source).toBe("none");
  });

  it("ignores unknown extra fields a newer server adds", () => {
    const parsed = parse({ changes: [{ ...change, head_sha: "deadbeef" }] });
    expect(parsed.changes[0]?.pr_number).toBe(42);
  });
});

describe("IssueChangePatchResponseSchema", () => {
  const parse = (raw: unknown) =>
    parseWithFallback(raw, IssueChangePatchResponseSchema, EMPTY_ISSUE_CHANGE_PATCH, {
      endpoint: "GET /api/issues/{id}/changes/patch",
    });

  it("reads a patch and an explicit null-with-reason alike", () => {
    expect(parse({ patch: "@@ -1 +1 @@", reason: "" }).patch).toBe("@@ -1 +1 @@");
    const absent = parse({ patch: null, reason: "patch_unavailable" });
    expect(absent.patch).toBeNull();
    expect(absent.reason).toBe("patch_unavailable");
  });

  it("reads a wrong-typed patch as no patch rather than rendering a number", () => {
    expect(parse({ patch: 42, reason: "" }).patch).toBeNull();
  });

  it("falls back to the generic explanation when the response is unreadable", () => {
    expect(parse(null)).toEqual(EMPTY_ISSUE_CHANGE_PATCH);
    expect(parse({}).patch).toBeNull();
  });

  it("keeps a reason this build has no sentence for, so the UI can default", () => {
    expect(parse({ patch: null, reason: "some_future_reason" }).reason).toBe("some_future_reason");
  });
});
