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
  EMPTY_DEPLOY_EVENTS,
  EMPTY_FIGMA_CREDENTIAL_STATUS,
  EMPTY_LIST_TEST_CASES,
  EMPTY_TEST_CASE,
  EMPTY_AUTOPILOT_TELEGRAM_DESTINATION,
  EMPTY_TELEGRAM_INSTALLATIONS,
  EMPTY_USER,
  FigmaCredentialStatusSchema,
  AutopilotTelegramDestinationSchema,
  ListTelegramInstallationsSchema,
  McpCredentialStatusSchema,
  McpCredentialListSchema,
  EMPTY_MCP_CREDENTIAL_STATUS,
  EMPTY_MCP_CREDENTIAL_LIST,
  IssueDeployEventsResponseSchema,
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
} from "./schemas";
import {
  EMPTY_DAEMON_BROWSE_TARGET,
  EMPTY_FS_LIST,
  EMPTY_ISSUE_BROWSER,
  EMPTY_WORKSPACE_LABS,
  DaemonBrowseTargetSchema,
  FsListResponseSchema,
  IssueBrowserResponseSchema,
  WorkspaceLabsSchema,
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
    expect(parsed[0]?.member_count).toBe(0);
    expect(parsed[0]?.member_preview).toEqual([]);
  });

  it("defaults preview fields on a single squad response", () => {
    const parsed = SquadSchema.parse(baseSquad);
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
