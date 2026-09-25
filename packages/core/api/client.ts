import type {
  Issue,
  IssueMetadata,
  IssueMetadataValue,
  CreateIssueRequest,
  UpdateIssueRequest,
  GroupedIssuesResponse,
  ListIssuesResponse,
  SearchIssuesResponse,
  SearchProjectsResponse,
  UpdateMeRequest,
  CreateMemberRequest,
  UpdateMemberRequest,
  ListIssuesParams,
  ListGroupedIssuesParams,
  Agent,
  CreateAgentRequest,
  AgentTemplate,
  AgentTemplateSummary,
  CreateAgentFromTemplateRequest,
  CreateAgentFromTemplateResponse,
  UpdateAgentRequest,
  AgentEnvResponse,
  UpdateAgentEnvRequest,
  AgentTask,
  AgentActivityBucket,
  AgentRunCount,
  AgentRuntime,
  InboxItem,
  Escalation,
  IssueSubscriber,
  Comment,
  CommentTriggerPreview,
  Reaction,
  IssueReaction,
  Workspace,
  WorkspaceRepo,
  GitCredential,
  FigmaCredentialStatus,
  McpCredentialStatus,
  McpCredentialInput,
  WorkspaceMcpConfigResponse,
  ReleaseIntegration,
  ReleaseIntegrationInput,
  MemberWithUser,
  ActorDirectoryEntry,
  User,
  Skill,
  SkillSummary,
  CreateSkillRequest,
  UpdateSkillRequest,
  SetAgentSkillsRequest,
  PersonalAccessToken,
  CreatePersonalAccessTokenRequest,
  CreatePersonalAccessTokenResponse,
  RuntimeUsage,
  IssueUsageSummary,
  RuntimeHourlyActivity,
  RuntimeUsageByAgent,
  RuntimeUsageByHour,
  DashboardUsageDaily,
  DashboardUsageByAgent,
  DashboardAgentRunTime,
  DashboardRunTimeDaily,
  RuntimeUpdate,
  RuntimeModelListRequest,
  RuntimeLocalSkillListRequest,
  CreateRuntimeLocalSkillImportRequest,
  RuntimeLocalSkillImportRequest,
  TimelineEntry,
  AssigneeFrequencyEntry,
  TaskMessagePayload,
  Attachment,
  ChatSession,
  ChatMessage,
  ChatMessagesPage,
  ChatPendingTask,
  PendingChatTasksResponse,
  SendChatMessageResponse,
  CancelTaskResponse,
  AssistantSession,
  AssistantRun,
  AssistantOperation,
  SendAssistantMessageRequest,
  AssistantMessage,
  AssistantAvailability,
  AssistantArtifact,
  PinnedReport,
  PinnedReportSummary,
  ReportPin,
  ReportSchedule,
  ReportScheduleInput,
  AssistantOperationDecision,
  SendAssistantMessageResponse,
  CreateAssistantSessionRequest,
  PatchAssistantSessionRequest,
  OrchestrationRun,
  CreateOrchestrationRequest,
  RespondToOrchestrationStepRequest,
  Project,
  WorkspaceLabs,
  PolicyFleetHealth,
  QAEvidence,
  TestCase,
  ListTestCasesResponse,
  CreateTestCaseRequest,
  UpdateTestCaseRequest,
  CreateTestRunRequest,
  BuildBaseSuiteResponse,
  IssueBrowserResponse,
  DaemonBrowseTarget,
  IssueQAPreviewURLResponse,
  IssueArtifactResponse,
  CreateProjectRequest,
  UpdateProjectRequest,
  ListProjectsResponse,
  ProjectResource,
  CreateProjectResourceRequest,
  UpdateProjectResourceRequest,
  ListProjectResourcesResponse,
  Sprint,
  WorkspaceSprint,
  CreateSprintRequest,
  UpdateSprintRequest,
  ListSprintsResponse,
  ListSprintIssuesResponse,
  Label,
  CreateLabelRequest,
  UpdateLabelRequest,
  ListLabelsResponse,
  IssueLabelsResponse,
  PinnedItem,
  CreatePinRequest,
  PinnedItemType,
  ReorderPinsRequest,
  Invitation,
  InvitationAuthInfo,
  Autopilot,
  AutopilotTrigger,
  AutopilotRun,
  CreateAutopilotRequest,
  UpdateAutopilotRequest,
  CreateAutopilotTriggerRequest,
  UpdateAutopilotTriggerRequest,
  ListAutopilotsResponse,
  GetAutopilotResponse,
  ListAutopilotRunsResponse,
  ListWebhookDeliveriesResponse,
  WebhookDelivery,
  NotificationPreferenceResponse,
  NotificationPreferences,
  GitHubPullRequest,
  IssueChangesResponse,
  IssueChangePatchResponse,
  MergeReadiness,
  ReviewVerdict,
  ReviewDecisionResponse,
  ListGitHubInstallationsResponse,
  GitHubConnectResponse,
  ListLarkInstallationsResponse,
  AutopilotTelegramDestination,
  ExternalIdentityLink,
  ListExternalIdentityLinksResponse,
  ListTelegramInstallationsResponse,
  SetTelegramAccessRequest,
  TelegramBindLinkResponse,
  TelegramInstallation,
  TelegramLinkStartResponse,
  BeginLarkInstallResponse,
  LarkInstallStatusResponse,
  RedeemLarkBindingTokenResponse,
  Squad,
  SquadMember,
  SquadMemberStatusListResponse,
  CreateSquadRequest,
  UpdateSquadRequest,
  BillingBalance,
  BillingTransactionsPage,
  BillingBatchesPage,
  BillingTopupsPage,
  BillingPriceTier,
  CreateBillingCheckoutSessionRequest,
  CreateBillingCheckoutSessionResponse,
  BillingCheckoutSessionStatus,
  CreateBillingPortalSessionResponse,
  StaleIssuesResponse,
  DecisionQueueResponse,
  RiskMapResponse,
  RiskMapEntry,
  DepartmentSetupStatus,
} from "../types";
import type { OnboardingCompletionPath } from "../onboarding/types";
import type {
  BitrixGroup,
  BitrixUser,
  BitrixTask,
  BitrixImportRequest,
  BitrixImportResponse,
  BitrixImportProgress,
  BitrixImportCancelResponse,
  BitrixSyncResult,
} from "../bitrix/types";
import {
  BitrixImportResponseSchema,
  EMPTY_BITRIX_IMPORT_RESPONSE,
} from "../bitrix/types";
import type {
  ZohoProject,
  ZohoImportRequest,
  ZohoImportResponse,
  ZohoSprintsProject,
  ZohoSprintsImportRequest,
  ZohoSprintsImportResponse,
  ZohoConnectionStatus,
  PutZohoConnectionRequest,
  ZohoAccount,
  ZohoConnectResponse,
  ZohoCRMModule,
  ZohoCRMFieldsResponse,
  ZohoSyncConfig,
  CreateZohoSyncConfigRequest,
  UpdateZohoSyncConfigRequest,
} from "../zoho/types";
import {
  ZohoConnectionStatusSchema,
  EMPTY_ZOHO_CONNECTION_STATUS,
  ZohoAccountSchema,
  EMPTY_ZOHO_ACCOUNT,
  ZohoConnectResponseSchema,
  EMPTY_ZOHO_CONNECT_RESPONSE,
  ZohoCRMModulesResponseSchema,
  EMPTY_ZOHO_CRM_MODULES,
  ZohoCRMFieldsResponseSchema,
  EMPTY_ZOHO_CRM_FIELDS,
  ZohoSyncConfigSchema,
  EMPTY_ZOHO_SYNC_CONFIG,
  ZohoSyncConfigsResponseSchema,
  EMPTY_ZOHO_SYNC_CONFIGS,
} from "../zoho/types";
import type {
  ImportConnection,
  CreateImportConnectionRequest,
  ImportDryRunRequest,
  ImportDryRunResult,
  ImportJob,
  StartImportRequest,
  StartImportResult,
} from "../imports/types";
import {
  ImportConnectionsResponseSchema,
  ImportDryRunResponseSchema,
  ImportJobSchema,
  StartImportResponseSchema,
  EMPTY_IMPORT_CONNECTIONS,
  EMPTY_IMPORT_DRY_RUN,
  EMPTY_IMPORT_JOB,
  EMPTY_START_IMPORT,
} from "../imports/types";
import type { Plugin, CreatePluginRequest } from "../plugins/types";
import type {
  CloudRuntimeNode,
  CreateCloudRuntimeNodeRequest,
  ListCloudRuntimeNodesParams,
} from "../runtimes/cloud-runtime";
import {
  EMPTY_SLACK_BEGIN,
  EMPTY_SLACK_CHANNELS,
  EMPTY_SLACK_INSTALLATIONS,
  EMPTY_SLACK_ROUTE,
  EMPTY_SLACK_ROUTES,
  ListSlackChannelsSchema,
  ListSlackInstallationsSchema,
  ListSlackRoutesSchema,
  SlackBeginSchema,
  SlackChannelRouteSchema,
} from "../slack/schemas";
import type {
  ListSlackChannelsResponse,
  ListSlackInstallationsResponse,
  ListSlackRoutesResponse,
  SlackBeginResponse,
  SlackChannelRoute,
  SlackRouteInput,
} from "../slack/schemas";
import { type Logger, noopLogger } from "../logger";
import { createRequestId } from "../utils";
import { getCurrentSlug } from "../platform/workspace-storage";
import { parseWithFallback } from "./schema";
import {
  parseAutomationCatalogResponse,
  parseAutomationRecipesResponse,
  parseAutomationResponse,
  parseAutomationRunResponse,
  parseAutomationRunsResponse,
  parseAutomationsResponse,
  parseInstallRecipeResponse,
} from "../automations/schema";
import {
  parseKnowledgeDocDetailResponse,
  parseKnowledgeDocResponse,
  parseKnowledgeListResponse,
  parseKnowledgeSearchResponse,
} from "../knowledge/schema";
import type {
  CreateKnowledgeDocRequest,
  KnowledgeDoc,
  KnowledgeDocDetail,
  KnowledgeListResponse,
  KnowledgeSearchResponse,
  UpdateKnowledgeDocRequest,
} from "../knowledge/types";
import type {
  Automation,
  AutomationCatalog,
  AutomationRun,
  AutomationWriteRequest,
  InstallAutomationRecipeResponse,
  ListAutomationRecipesResponse,
  ListAutomationRunsResponse,
  ListAutomationsResponse,
} from "../automations/types";
import {
  DesignContextRevisionSchema,
  DesignContextStateSchema,
  EMPTY_DESIGN_CONTEXT_REVISION,
  EMPTY_DESIGN_CONTEXT_STATE,
  type DesignContextDocument,
  type DesignContextRevision,
  type DesignContextState,
} from "../design/context";
import {
  AgentTemplateSchema,
  AgentTemplateSummaryListSchema,
  AttachmentResponseSchema,
  CancelTaskResponseSchema,
  ChildIssuesResponseSchema,
  StaleIssuesResponseSchema,
  EMPTY_STALE_ISSUES,
  DecisionQueueResponseSchema,
  EMPTY_DECISION_QUEUE,
  RiskMapResponseSchema,
  EMPTY_RISK_MAP,
  CommentsListSchema,
  CommentTriggerPreviewSchema,
  CloudRuntimeNodeListSchema,
  CloudRuntimeNodeSchema,
  CreateAgentFromTemplateResponseSchema,
  DashboardAgentRunTimeListSchema,
  DashboardRunTimeDailyListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  EMPTY_AGENT_TEMPLATE_DETAIL,
  EMPTY_AGENT_TEMPLATE_SUMMARY_LIST,
  EMPTY_APP_CONFIG,
  EMPTY_ATTACHMENT,
  EMPTY_CLOUD_RUNTIME_NODE,
  EMPTY_CLOUD_RUNTIME_NODE_LIST,
  EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE,
  EMPTY_GROUPED_ISSUES_RESPONSE,
  EMPTY_LIST_ISSUES_RESPONSE,
  EMPTY_SQUAD,
  EMPTY_SQUAD_LIST,
  EMPTY_SQUAD_MEMBER_STATUS_LIST,
  EMPTY_TIMELINE_ENTRIES,
  EMPTY_USER,
  EMPTY_WORKSPACE,
  EMPTY_LIST_WEBHOOK_DELIVERIES_RESPONSE,
  EMPTY_WEBHOOK_DELIVERY,
  AppConfigSchema,
  type AppConfigResponse,
  GroupedIssuesResponseSchema,
  ListIssuesResponseSchema,
  ListWebhookDeliveriesResponseSchema,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  SquadSchema,
  SquadListSchema,
  SquadMemberStatusListResponseSchema,
  SubscribersListSchema,
  TimelineEntriesSchema,
  UserSchema,
  WorkspaceSchema,
  WebhookDeliveryResponseSchema,
  BillingBalanceSchema,
  BillingTransactionsPageSchema,
  BillingBatchesPageSchema,
  BillingTopupsPageSchema,
  BillingPriceTierListSchema,
  CreateBillingCheckoutSessionResponseSchema,
  BillingCheckoutSessionStatusSchema,
  CreateBillingPortalSessionResponseSchema,
  EMPTY_BILLING_BALANCE,
  EMPTY_BILLING_TRANSACTIONS_PAGE,
  EMPTY_BILLING_BATCHES_PAGE,
  EMPTY_BILLING_TOPUPS_PAGE,
  EMPTY_BILLING_PRICE_TIER_LIST,
  EMPTY_CREATE_BILLING_CHECKOUT_SESSION_RESPONSE,
  EMPTY_BILLING_CHECKOUT_SESSION_STATUS,
  EMPTY_CREATE_BILLING_PORTAL_SESSION_RESPONSE,
  EMPTY_CANCEL_TASK_RESPONSE,
  WorkspaceLabsSchema,
  EMPTY_WORKSPACE_LABS,
  PolicyFleetHealthSchema,
  EMPTY_POLICY_FLEET_HEALTH,
  QAEvidenceSchema,
  IssueDeployEventsResponseSchema,
  EMPTY_DEPLOY_EVENTS,
  type IssueDeployEventsResponse,
  ListTestCasesResponseSchema,
  EMPTY_LIST_TEST_CASES,
  TestCaseSchema,
  EMPTY_TEST_CASE,
  BuildBaseSuiteResponseSchema,
  EMPTY_BUILD_BASE_SUITE,
  TestCaseRunsResponseSchema,
  EMPTY_TEST_CASE_RUNS,
  type TestCaseRunsParsed,
  LaunchTraceResponseSchema,
  EMPTY_LAUNCH_TRACE,
  EMPTY_ISSUE_BROWSER,
  IssueBrowserResponseSchema,
  EMPTY_DAEMON_BROWSE_TARGET,
  DaemonBrowseTargetSchema,
  IssueQAPreviewURLResponseSchema,
  IssueArtifactResponseSchema,
  EMPTY_ISSUE_ARTIFACT,
  IssueChangesResponseSchema,
  EMPTY_ISSUE_CHANGES,
  IssueChangePatchResponseSchema,
  EMPTY_ISSUE_CHANGE_PATCH,
  EMPTY_ISSUE_QA_PREVIEW_URL,
  QAMetricsResponseSchema,
  EMPTY_QA_METRICS,
  type QAMetricsResponse,
  SprintReadinessResponseSchema,
  EMPTY_SPRINT_READINESS,
  type SprintReadinessResponse,
  FigmaCredentialStatusSchema,
  EMPTY_FIGMA_CREDENTIAL_STATUS,
  McpCredentialStatusSchema,
  McpCredentialListSchema,
  EMPTY_MCP_CREDENTIAL_LIST,
  AutopilotTelegramDestinationSchema,
  EMPTY_AUTOPILOT_TELEGRAM_DESTINATION,
  EMPTY_TELEGRAM_BIND_LINK,
  EMPTY_TELEGRAM_INSTALLATION,
  EMPTY_TELEGRAM_INSTALLATIONS,
  EMPTY_EXTERNAL_IDENTITY_LINKS,
  EMPTY_TELEGRAM_LINK_START,
  ListExternalIdentityLinksSchema,
  ListTelegramInstallationsSchema,
  TelegramBindLinkSchema,
  TelegramInstallationSchema,
  TelegramLinkStartSchema,
  ExternalIdentityLinkSchema,
  EMPTY_MCP_CREDENTIAL_STATUS,
  ReleaseIntegrationListSchema,
  EMPTY_RELEASE_INTEGRATIONS,
  ProjectConfigListSchema,
  EMPTY_PROJECT_CONFIG,
  type ProjectConfigEntry,
  ProjectDevServersSchema,
  EMPTY_PROJECT_DEV_SERVERS,
  type UserDevServerEntry,
  QAVerdictsResponseSchema,
  EMPTY_QA_VERDICTS,
  type QAVerdictsResponse,
  ReviewVerdictSchema,
  EMPTY_REVIEW_VERDICT,
  EscalationSchema,
  EscalationListSchema,
  EMPTY_ESCALATION_LIST,
  EMPTY_ESCALATION,
  ReviewDecisionResponseSchema,
  EMPTY_REVIEW_DECISION,
  OrchestrationRunSchema,
  EMPTY_ORCHESTRATION_RUN,
  AssistantSessionSchema,
  AssistantRunSchema,
  AssistantOperationSchema,
  AssistantOperationListSchema,
  EMPTY_ASSISTANT_OPERATION,
  EMPTY_ASSISTANT_OPERATION_LIST,
  AssistantRunListSchema,
  EMPTY_ASSISTANT_RUN,
  EMPTY_ASSISTANT_RUN_LIST,
  AssistantSessionListSchema,
  EMPTY_ASSISTANT_SESSION,
  EMPTY_ASSISTANT_SESSION_LIST,
  AssistantMessageListSchema,
  EMPTY_ASSISTANT_MESSAGE_LIST,
  AssistantAvailabilitySchema,
  EMPTY_ASSISTANT_AVAILABILITY,
  SendAssistantMessageResponseSchema,
  EMPTY_SEND_ASSISTANT_MESSAGE_RESPONSE,
  AssistantArtifactSchema,
  EMPTY_ASSISTANT_ARTIFACT,
  AssistantOperationDecisionSchema,
  EMPTY_ASSISTANT_OPERATION_DECISION,
  ProjectReportsResponseSchema,
  EMPTY_PINNED_REPORT_LIST,
  PinnedReportSchema,
  EMPTY_PINNED_REPORT,
  ReportPinSchema,
  EMPTY_REPORT_PIN,
  ReportScheduleResponseSchema,
  EMPTY_REPORT_SCHEDULE_RESPONSE,
} from "./schemas";

/** Identifies the calling client to the server.
 *  Sent on every HTTP request as X-Client-Platform / X-Client-Version /
 *  X-Client-OS so the backend can log, gate, or split metrics by client.
 *  See server/internal/middleware/client.go for the receiving end. */
export interface ApiClientIdentity {
  /** Logical client kind. Server expects: "web" | "desktop" | "cli" | "daemon". */
  platform?: string;
  /** Client/app version string (e.g. "0.1.0", git tag, commit). */
  version?: string;
  /** Operating system the client is running on: "macos" | "windows" | "linux". */
  os?: string;
}

export interface ApiClientOptions {
  logger?: Logger;
  onUnauthorized?: () => void;
  /** Identifies the client to the server. Sent as X-Client-* headers. */
  identity?: ApiClientIdentity;
}

export interface LoginResponse {
  token: string;
  user: User;
}

/** Result of POST /api/issues/{id}/slice-actions. The backend owns the
 *  instruction templates per `kind`; it echoes the resolved kind/scope, the
 *  rendered instruction, the agent it dispatched to, and the comment it
 *  posted on the issue (which carries the agent's draft). */
export interface SliceActionResponse {
  kind: string;
  scope: string;
  instruction: string;
  agent_id: string;
  comment: import("../types").Comment;
}

export class ApiError extends Error {
  readonly status: number;
  readonly statusText: string;
  // Raw decoded JSON body (when the server returned one). Carries structured
  // error fields like `code` so callers can branch on machine-readable
  // identifiers instead of pattern-matching the human-readable message.
  readonly body?: unknown;

  constructor(message: string, status: number, statusText: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.body = body;
  }
}

// Thrown by getAttachmentTextContent when the server refuses to inline a
// file because it exceeds the 2 MB cap. UI maps to a "too large, please
// download" affordance with the Download CTA still available.
export class PreviewTooLargeError extends Error {
  constructor() {
    super("attachment too large for inline preview");
    this.name = "PreviewTooLargeError";
  }
}

// Thrown by getAttachmentTextContent when the server's text whitelist
// rejects the content type. Normally the client's isPreviewable() guard
// catches this earlier, but the two whitelists can drift — surfacing the
// 415 as a typed error makes the drift visible.
export class PreviewUnsupportedError extends Error {
  constructor() {
    super("attachment type not supported for inline preview");
    this.name = "PreviewUnsupportedError";
  }
}

export class ApiClient {
  private baseUrl: string;
  private token: string | null = null;
  private logger: Logger;
  private options: ApiClientOptions;

  constructor(baseUrl: string, options?: ApiClientOptions) {
    this.baseUrl = baseUrl;
    this.options = options ?? {};
    this.logger = options?.logger ?? noopLogger;
  }

  getBaseUrl(): string {
    return this.baseUrl;
  }

  setToken(token: string | null) {
    this.token = token;
  }

  private readCsrfToken(): string | null {
    if (typeof document === "undefined") return null;
    const match = document.cookie
      .split("; ")
      .find((c) => c.startsWith("agora_csrf="));
    return match ? match.split("=")[1] ?? null : null;
  }

  private authHeaders(): Record<string, string> {
    const headers: Record<string, string> = {};
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const slug = getCurrentSlug();
    if (slug) headers["X-Workspace-Slug"] = slug;
    const csrf = this.readCsrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
    const id = this.options.identity;
    if (id?.platform) headers["X-Client-Platform"] = id.platform;
    if (id?.version) headers["X-Client-Version"] = id.version;
    if (id?.os) headers["X-Client-OS"] = id.os;
    return headers;
  }

  private handleUnauthorized() {
    this.token = null;
    // Workspace id is owned by the URL-driven workspace-storage singleton
    // (set by [workspaceSlug]/layout.tsx). On 401, the auth flow navigates
    // to /login which leaves the workspace route, and the next workspace
    // entry will overwrite the id. No clear needed here.
    this.options.onUnauthorized?.();
  }

  private async parseErrorMessage(res: Response, fallback: string): Promise<string> {
    try {
      const data = await res.json() as { error?: string };
      if (typeof data.error === "string" && data.error) return data.error;
    } catch {
      // Ignore non-JSON error bodies.
    }
    return fallback;
  }

  // Reads the response body once for both human-readable error message and
  // structured fields. The Response stream can only be consumed once, so
  // both pieces have to come from a single read.
  private async parseErrorBody(res: Response, fallback: string): Promise<{ message: string; body: unknown }> {
    try {
      const data = await res.json() as { error?: string };
      const message = typeof data.error === "string" && data.error ? data.error : fallback;
      return { message, body: data };
    } catch {
      return { message: fallback, body: undefined };
    }
  }

  // Sends the request with the standard headers (auth, CSRF, request id,
  // client identity) and runs the shared error path (401 → handleUnauthorized,
  // structured ApiError, status-aware log level). Returns the raw Response so
  // callers can decide how to decode the body — JSON for the typed `fetch<T>`
  // path, plain text for the attachment-preview proxy, etc.
  private async fetchRaw(
    path: string,
    init?: RequestInit & { extraHeaders?: Record<string, string> },
  ): Promise<Response> {
    const rid = createRequestId();
    const start = Date.now();
    const method = init?.method ?? "GET";

    const headers: Record<string, string> = {
      "X-Request-ID": rid,
      ...this.authHeaders(),
      ...(init?.extraHeaders ?? {}),
      ...((init?.headers as Record<string, string>) ?? {}),
    };

    this.logger.info(`→ ${method} ${path}`, { rid });

    const res = await fetch(`${this.baseUrl}${path}`, {
      ...init,
      headers,
      credentials: "include",
    });

    if (!res.ok) {
      if (res.status === 401) this.handleUnauthorized();
      const { message, body } = await this.parseErrorBody(res, `API error: ${res.status} ${res.statusText}`);
      // 401 is an expected probe result on public/unauthenticated pages
      // (e.g. GET /api/me on the landing page) and 404 is a benign miss —
      // neither should surface as a console error.
      const logLevel = res.status === 404 || res.status === 401 ? "warn" : "error";
      this.logger[logLevel](`← ${res.status} ${path}`, { rid, duration: `${Date.now() - start}ms`, error: message });
      throw new ApiError(message, res.status, res.statusText, body);
    }

    this.logger.info(`← ${res.status} ${path}`, { rid, duration: `${Date.now() - start}ms` });
    return res;
  }

  private async fetch<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await this.fetchRaw(path, {
      ...init,
      extraHeaders: { "Content-Type": "application/json" },
    });
    // Handle 204 No Content
    if (res.status === 204) {
      return undefined as T;
    }
    return res.json() as Promise<T>;
  }

  // Auth
  async sendCode(email: string, intent?: "login" | "signup"): Promise<void> {
    await this.fetch("/auth/send-code", {
      method: "POST",
      body: JSON.stringify({ email, ...(intent ? { intent } : {}) }),
    });
  }

  async verifyCode(
    email: string,
    code: string,
    intent?: "login" | "signup",
  ): Promise<LoginResponse> {
    return this.fetch("/auth/verify-code", {
      method: "POST",
      body: JSON.stringify({ email, code, ...(intent ? { intent } : {}) }),
    });
  }

  async googleLogin(code: string, redirectUri: string): Promise<LoginResponse> {
    return this.fetch("/auth/google", {
      method: "POST",
      body: JSON.stringify({ code, redirect_uri: redirectUri }),
    });
  }

  async telegramStartLogin(): Promise<{ nonce: string; deep_link: string }> {
    return this.fetch("/auth/telegram/start", {
      method: "POST",
    });
  }

  async telegramVerifyLogin(
    nonce: string,
    code: string,
  ): Promise<LoginResponse> {
    return this.fetch("/auth/telegram/verify", {
      method: "POST",
      body: JSON.stringify({ nonce, code }),
    });
  }

  /**
   * Telegram Mini App login: exchanges a signed window.Telegram.WebApp.initData
   * string for a session. Same LoginResponse shape as the other login paths;
   * the Mini App stores the returned token for Authorization: Bearer.
   */
  async telegramMiniAppLogin(initData: string): Promise<LoginResponse> {
    return this.fetch("/auth/telegram/miniapp", {
      method: "POST",
      body: JSON.stringify({ init_data: initData }),
    });
  }

  async logout(): Promise<void> {
    await this.fetch("/auth/logout", { method: "POST" });
  }

  async issueCliToken(): Promise<{ token: string }> {
    return this.fetch("/api/cli-token", { method: "POST" });
  }

  async getMe(): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me");
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "GET /api/me",
    });
  }

  async markOnboardingComplete(payload?: {
    completion_path?: OnboardingCompletionPath;
    workspace_id?: string;
  }): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me/onboarding/complete", {
      method: "POST",
      body: payload ? JSON.stringify(payload) : undefined,
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "POST /api/me/onboarding/complete",
    });
  }

  async joinCloudWaitlist(payload: {
    email: string;
    reason?: string;
  }): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me/onboarding/cloud-waitlist", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "POST /api/me/onboarding/cloud-waitlist",
    });
  }

  async patchOnboarding(payload: {
    questionnaire?: Record<string, unknown>;
  }): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me/onboarding", {
      method: "PATCH",
      body: JSON.stringify(payload),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "PATCH /api/me/onboarding",
    });
  }

  async updateMe(data: UpdateMeRequest): Promise<User> {
    const raw = await this.fetch<unknown>("/api/me", {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, UserSchema, EMPTY_USER, {
      endpoint: "PATCH /api/me",
    });
  }

  async deleteMe(confirmation: string): Promise<void> {
    await this.fetch("/api/me", {
      method: "DELETE",
      body: JSON.stringify({ confirmation }),
    });
  }

  async listMyExternalLinks(): Promise<ListExternalIdentityLinksResponse> {
    const raw = await this.fetch<unknown>("/api/me/links");
    return parseWithFallback(raw, ListExternalIdentityLinksSchema, EMPTY_EXTERNAL_IDENTITY_LINKS, {
      endpoint: "GET /api/me/links",
    });
  }

  async startTelegramLink(): Promise<TelegramLinkStartResponse> {
    const raw = await this.fetch<unknown>("/api/me/links/telegram/start", {
      method: "POST",
    });
    return parseWithFallback(raw, TelegramLinkStartSchema, EMPTY_TELEGRAM_LINK_START, {
      endpoint: "POST /api/me/links/telegram/start",
    });
  }

  async verifyTelegramLink(nonce: string, code: string): Promise<ExternalIdentityLink> {
    const raw = await this.fetch<unknown>("/api/me/links/telegram/verify", {
      method: "POST",
      body: JSON.stringify({ nonce, code }),
    });
    return parseWithFallback(raw, ExternalIdentityLinkSchema, { provider: "", external_id: "" }, {
      endpoint: "POST /api/me/links/telegram/verify",
    });
  }

  async unlinkTelegramIdentity(): Promise<void> {
    await this.fetch("/api/me/links/telegram", { method: "DELETE" });
  }

  // Issues
  async listIssues(params?: ListIssuesParams): Promise<ListIssuesResponse> {
    const search = new URLSearchParams();
    if (params?.limit) search.set("limit", String(params.limit));
    if (params?.offset) search.set("offset", String(params.offset));
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.status) search.set("status", params.status);
    if (params?.priority) search.set("priority", params.priority);
    if (params?.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params?.assignee_ids?.length) search.set("assignee_ids", params.assignee_ids.join(","));
    if (params?.creator_id) search.set("creator_id", params.creator_id);
    if (params?.project_id) search.set("project_id", params.project_id);
    if (params?.involves_user_id) search.set("involves_user_id", params.involves_user_id);
    if (params?.metadata && Object.keys(params.metadata).length > 0) {
      search.set("metadata", JSON.stringify(params.metadata));
    }
    if (params?.open_only) search.set("open_only", "true");
    if (params?.scheduled) search.set("scheduled", "true");
    if (params?.sort_by) search.set("sort", params.sort_by);
    if (params?.sort_direction) search.set("direction", params.sort_direction);
    const path = `/api/issues?${search}`;
    const raw = await this.fetch<unknown>(path);
    return parseWithFallback(raw, ListIssuesResponseSchema, EMPTY_LIST_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues",
    });
  }

  async listGroupedIssues(params: ListGroupedIssuesParams): Promise<GroupedIssuesResponse> {
    const search = new URLSearchParams({ group_by: params.group_by });
    if (params.limit) search.set("limit", String(params.limit));
    if (params.offset) search.set("offset", String(params.offset));
    if (params.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params.statuses?.length) search.set("statuses", params.statuses.join(","));
    if (params.priorities?.length) search.set("priorities", params.priorities.join(","));
    if (params.assignee_types?.length) search.set("assignee_types", params.assignee_types.join(","));
    if (params.assignee_id) search.set("assignee_id", params.assignee_id);
    if (params.assignee_ids?.length) search.set("assignee_ids", params.assignee_ids.join(","));
    if (params.creator_id) search.set("creator_id", params.creator_id);
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.involves_user_id) search.set("involves_user_id", params.involves_user_id);
    if (params.metadata && Object.keys(params.metadata).length > 0) {
      search.set("metadata", JSON.stringify(params.metadata));
    }
    if (params.assignee_filters?.length) {
      search.set("assignee_filters", params.assignee_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params.include_no_assignee) search.set("include_no_assignee", "true");
    if (params.creator_filters?.length) {
      search.set("creator_filters", params.creator_filters.map((f) => `${f.type}:${f.id}`).join(","));
    }
    if (params.project_ids?.length) search.set("project_ids", params.project_ids.join(","));
    if (params.include_no_project) search.set("include_no_project", "true");
    if (params.label_ids?.length) search.set("label_ids", params.label_ids.join(","));
    if (params.group_assignee_type) search.set("group_assignee_type", params.group_assignee_type);
    if (params.group_assignee_id) search.set("group_assignee_id", params.group_assignee_id);
    if (params.sort_by) search.set("sort", params.sort_by);
    if (params.sort_direction) search.set("direction", params.sort_direction);
    const raw = await this.fetch<unknown>(`/api/issues/grouped?${search}`);
    return parseWithFallback(raw, GroupedIssuesResponseSchema, EMPTY_GROUPED_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues/grouped",
    });
  }

  async searchIssues(params: { q: string; limit?: number; offset?: number; include_closed?: boolean; signal?: AbortSignal }): Promise<SearchIssuesResponse> {
    const search = new URLSearchParams({ q: params.q });
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    if (params.include_closed) search.set("include_closed", "true");
    return this.fetch(`/api/issues/search?${search}`, params.signal ? { signal: params.signal } : undefined);
  }

  async searchProjects(params: { q: string; limit?: number; offset?: number; include_closed?: boolean; signal?: AbortSignal }): Promise<SearchProjectsResponse> {
    const search = new URLSearchParams({ q: params.q });
    if (params.limit !== undefined) search.set("limit", String(params.limit));
    if (params.offset !== undefined) search.set("offset", String(params.offset));
    if (params.include_closed) search.set("include_closed", "true");
    return this.fetch(`/api/projects/search?${search}`, params.signal ? { signal: params.signal } : undefined);
  }

  async getIssue(id: string): Promise<Issue> {
    return this.fetch(`/api/issues/${id}`);
  }

  async createIssue(data: CreateIssueRequest): Promise<Issue> {
    return this.fetch("/api/issues", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async quickCreateIssue(data: {
    agent_id?: string;
    squad_id?: string;
    prompt: string;
    project_id?: string | null;
    parent_issue_id?: string | null;
    attachment_ids?: string[];
  }): Promise<{ task_id: string }> {
    return this.fetch("/api/issues/quick-create", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async createFeedback(data: {
    message: string;
    url?: string;
    workspace_id?: string;
  }): Promise<{ id: string; created_at: string }> {
    return this.fetch("/api/feedback", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateIssue(id: string, data: UpdateIssueRequest): Promise<Issue> {
    return this.fetch(`/api/issues/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async setIssueMetadataKey(
    id: string,
    key: string,
    value: IssueMetadataValue,
  ): Promise<{ metadata: IssueMetadata }> {
    return this.fetch(`/api/issues/${id}/metadata/${encodeURIComponent(key)}`, {
      method: "PUT",
      body: JSON.stringify({ value }),
    });
  }

  async deleteIssueMetadataKey(id: string, key: string): Promise<void> {
    await this.fetch(`/api/issues/${id}/metadata/${encodeURIComponent(key)}`, {
      method: "DELETE",
    });
  }

  async listChildIssues(id: string): Promise<{ issues: Issue[] }> {
    const raw = await this.fetch<unknown>(`/api/issues/${id}/children`);
    return parseWithFallback(raw, ChildIssuesResponseSchema, { issues: [] }, {
      endpoint: "GET /api/issues/:id/children",
    });
  }

  /** Batched variant — returns children for multiple parents in one request.
   *  Avoids an N-request fan-out in Swimlane (one per visible parent lane).
   *  parentIds must be non-empty; pass a sorted, deduplicated list so the
   *  React Query cache key is stable across renders. */
  async listChildrenByParents(parentIds: string[]): Promise<{ issues: Issue[] }> {
    const raw = await this.fetch<unknown>(
      `/api/issues/children?parent_ids=${parentIds.join(",")}`,
    );
    return parseWithFallback(raw, ChildIssuesResponseSchema, { issues: [] }, {
      endpoint: "GET /api/issues/children",
    });
  }

  async getChildIssueProgress(): Promise<{ progress: { parent_issue_id: string; total: number; done: number }[] }> {
    return this.fetch("/api/issues/child-progress");
  }

  /**
   * Issues that look stale right now (docs/living-truth-plan.md Tier 2).
   *
   * Workspace-scoped through the ambient `X-Workspace-ID` header like every
   * other workspace read — the caller keeps `wsId` in the query key, not in
   * this signature. Deliberately a SEPARATE request from the issue list: the
   * hot list/board path must stay untouched, and a missing/failing staleness
   * response costs the nudge and nothing else.
   */
  async getIssueStaleness(projectId?: string): Promise<StaleIssuesResponse> {
    const query = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
    const raw = await this.fetch<unknown>(`/api/issues/staleness${query}`);
    return parseWithFallback(raw, StaleIssuesResponseSchema, EMPTY_STALE_ISSUES, {
      endpoint: "GET /api/issues/staleness",
    });
  }

  /**
   * Everything waiting on a human right now, ranked
   * (docs/orchestration-upgrade-plan.md §A2).
   *
   * A SIBLING of the staleness read, for the same reason: it is ranked on
   * read from signals that already exist, and it must never become a join
   * into the hot issue-list path. Workspace-scoped through the ambient
   * `X-Workspace-ID` header; the caller keeps `wsId` in the query key.
   *
   * Unlike staleness, a failure here is not free — a blank queue reads as
   * "nothing needs you". The parse degrades row-by-row and the caller
   * surfaces a load error rather than an all-clear.
   */
  async getDecisionQueue(projectId?: string): Promise<DecisionQueueResponse> {
    const query = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
    const raw = await this.fetch<unknown>(`/api/issues/decision-queue${query}`);
    return parseWithFallback(raw, DecisionQueueResponseSchema, EMPTY_DECISION_QUEUE, {
      endpoint: "GET /api/issues/decision-queue",
    });
  }

  async deleteIssue(id: string): Promise<void> {
    await this.fetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  async batchUpdateIssues(issueIds: string[], updates: UpdateIssueRequest): Promise<{ updated: number }> {
    return this.fetch("/api/issues/batch-update", {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds, updates }),
    });
  }

  async batchDeleteIssues(issueIds: string[]): Promise<{ deleted: number }> {
    return this.fetch("/api/issues/batch-delete", {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds }),
    });
  }

  // Comments
  async listComments(issueId: string): Promise<Comment[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments`);
    return parseWithFallback(raw, CommentsListSchema, [], {
      endpoint: "GET /api/issues/:id/comments",
    });
  }

  // Post an agent's final summary (branch, bug causes) to the issue's linked
  // Bitrix task. Human-only on the server (RequireHumanActor); the caller passes
  // the human-reviewed text. The server returns {status, posted_at} but callers
  // only care about success/failure.
  async postBitrixSummary(issueId: string, text: string): Promise<void> {
    await this.fetch(`/api/issues/${issueId}/bitrix-summary`, {
      method: "POST",
      body: JSON.stringify({ text }),
    });
  }

  async createComment(
    issueId: string,
    content: string,
    type?: string,
    parentId?: string,
    attachmentIds?: string[],
    suppressAgentIds?: string[],
    runMode?: import("../types").AgentRunMode,
  ): Promise<Comment> {
    return this.fetch(`/api/issues/${issueId}/comments`, {
      method: "POST",
      body: JSON.stringify({
        content,
        type: type ?? "comment",
        ...(parentId ? { parent_id: parentId } : {}),
        ...(attachmentIds?.length ? { attachment_ids: attachmentIds } : {}),
        ...(suppressAgentIds?.length ? { suppress_agent_ids: suppressAgentIds } : {}),
        ...(runMode ? { run_mode: runMode } : {}),
      }),
    });
  }

  // Resolves an issue UUID to the workspace it lives in (across the caller's
  // memberships), independent of the current X-Workspace header. Lets a deep
  // link opened in the wrong workspace switch before loading the issue.
  async locateIssue(
    issueId: string,
  ): Promise<{ issue_id: string; workspace_id: string; workspace_slug: string } | null> {
    try {
      const raw = await this.fetch<unknown>(`/api/issues/${issueId}/locate`);
      const o = (raw ?? {}) as { workspace_slug?: unknown; workspace_id?: unknown };
      if (typeof o.workspace_slug !== "string" || !o.workspace_slug) return null;
      return {
        issue_id: issueId,
        workspace_id: typeof o.workspace_id === "string" ? o.workspace_id : "",
        workspace_slug: o.workspace_slug,
      };
    } catch {
      // 404 (deleted / not a member / identifier not a UUID) — caller falls back.
      return null;
    }
  }

  // Summarizes an issue's comment thread with the free Agora base model.
  // Returns { summary } — empty string when there's nothing to summarize.
  async summarizeComments(issueId: string): Promise<{ summary: string }> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments/summarize`, {
      method: "POST",
    });
    const obj = (raw ?? {}) as { summary?: unknown };
    return { summary: typeof obj.summary === "string" ? obj.summary : "" };
  }

  // Fires a scoped AI slice-action against the issue's agent. The backend
  // renders the instruction template for `kind`, dispatches a task, and posts
  // the agent draft as a comment (surfaced in the execution log). 201 ->
  // { kind, scope, instruction, agent_id, comment }.
  // `ref` applies to kind="deploy" only: it overrides the environment's
  // configured target ref (the sprint Deploy panel passes the sprint branch);
  // the server validates it and ignores it for every other kind.
  async sliceAction(
    issueId: string,
    body: { kind: string; scope?: string; agentId?: string; ref?: string },
  ): Promise<SliceActionResponse> {
    return this.fetch(`/api/issues/${issueId}/slice-actions`, {
      method: "POST",
      body: JSON.stringify({
        kind: body.kind,
        ...(body.scope ? { scope: body.scope } : {}),
        ...(body.agentId ? { agent_id: body.agentId } : {}),
        ...(body.ref ? { ref: body.ref } : {}),
      }),
    });
  }

  async previewCommentTriggers(issueId: string, content: string, parentId?: string): Promise<CommentTriggerPreview> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/comments/trigger-preview`, {
      method: "POST",
      body: JSON.stringify({
        content,
        ...(parentId ? { parent_id: parentId } : {}),
      }),
    });
    return parseWithFallback(raw, CommentTriggerPreviewSchema, { agents: [] }, {
      endpoint: "POST /api/issues/:id/comments/trigger-preview",
    });
  }

  async listTimeline(issueId: string): Promise<TimelineEntry[]> {
    const raw = await this.fetch<unknown>(
      `/api/issues/${issueId}/timeline`,
    );
    return parseWithFallback(raw, TimelineEntriesSchema, EMPTY_TIMELINE_ENTRIES, {
      endpoint: "GET /api/issues/:id/timeline",
    });
  }

  async getAssigneeFrequency(): Promise<AssigneeFrequencyEntry[]> {
    return this.fetch("/api/assignee-frequency");
  }

  async updateComment(commentId: string, content: string, attachmentIds?: string[]): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}`, {
      method: "PUT",
      body: JSON.stringify({ content, attachment_ids: attachmentIds }),
    });
  }

  async deleteComment(commentId: string): Promise<void> {
    await this.fetch(`/api/comments/${commentId}`, { method: "DELETE" });
  }

  async resolveComment(commentId: string): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}/resolve`, { method: "POST" });
  }

  async unresolveComment(commentId: string): Promise<Comment> {
    return this.fetch(`/api/comments/${commentId}/resolve`, { method: "DELETE" });
  }

  async addReaction(commentId: string, emoji: string): Promise<Reaction> {
    return this.fetch(`/api/comments/${commentId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeReaction(commentId: string, emoji: string): Promise<void> {
    await this.fetch(`/api/comments/${commentId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  async addIssueReaction(issueId: string, emoji: string): Promise<IssueReaction> {
    return this.fetch(`/api/issues/${issueId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeIssueReaction(issueId: string, emoji: string): Promise<void> {
    await this.fetch(`/api/issues/${issueId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  // Subscribers
  async listIssueSubscribers(issueId: string): Promise<IssueSubscriber[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/subscribers`);
    return parseWithFallback(raw, SubscribersListSchema, [], {
      endpoint: "GET /api/issues/:id/subscribers",
    });
  }

  async subscribeToIssue(issueId: string, userId?: string, userType?: string): Promise<void> {
    const body: Record<string, string> = {};
    if (userId) body.user_id = userId;
    if (userType) body.user_type = userType;
    await this.fetch(`/api/issues/${issueId}/subscribe`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async unsubscribeFromIssue(issueId: string, userId?: string, userType?: string): Promise<void> {
    const body: Record<string, string> = {};
    if (userId) body.user_id = userId;
    if (userType) body.user_type = userType;
    await this.fetch(`/api/issues/${issueId}/unsubscribe`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  // Agents
  async listAgents(params?: { workspace_id?: string; include_archived?: boolean }): Promise<Agent[]> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.include_archived) search.set("include_archived", "true");
    return this.fetch(`/api/agents?${search}`);
  }

  async getAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/agents/${id}`);
  }

  async createAgent(data: CreateAgentRequest): Promise<Agent> {
    return this.fetch("/api/agents", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listAgentTemplates(): Promise<AgentTemplateSummary[]> {
    const raw = await this.fetch<unknown>("/api/agent-templates");
    return parseWithFallback(
      raw,
      AgentTemplateSummaryListSchema,
      EMPTY_AGENT_TEMPLATE_SUMMARY_LIST,
      { endpoint: "GET /api/agent-templates" },
    );
  }

  async getAgentTemplate(slug: string): Promise<AgentTemplate> {
    const raw = await this.fetch<unknown>(
      `/api/agent-templates/${encodeURIComponent(slug)}`,
    );
    // Round-trip the requested slug into the fallback so a malformed
    // detail response still produces a navigable record matching the URL
    // the user clicked.
    return parseWithFallback(
      raw,
      AgentTemplateSchema,
      { ...EMPTY_AGENT_TEMPLATE_DETAIL, slug },
      { endpoint: "GET /api/agent-templates/:slug" },
    );
  }

  /** Creates an agent from a curated template. The server fetches every
   *  referenced skill URL in parallel, materializes them into the workspace
   *  (find-or-create by name), and writes the agent + skill bindings in a
   *  single transaction. On any upstream fetch failure, the entire write is
   *  rolled back and the API returns 422 with `failed_urls`. */
  async createAgentFromTemplate(
    data: CreateAgentFromTemplateRequest,
  ): Promise<CreateAgentFromTemplateResponse> {
    const raw = await this.fetch<unknown>("/api/agents/from-template", {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(
      raw,
      CreateAgentFromTemplateResponseSchema,
      EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE,
      { endpoint: "POST /api/agents/from-template" },
    );
  }

  async updateAgent(id: string, data: UpdateAgentRequest): Promise<Agent> {
    return this.fetch(`/api/agents/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async archiveAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/agents/${id}/archive`, { method: "POST" });
  }

  /**
   * Returns the plaintext `custom_env` map for an agent. Owner/admin
   * only; calls from agent-actor sessions get a 403. Every successful
   * call writes an `agent_env_revealed` activity_log row server-side.
   * MUL-2600.
   */
  async getAgentEnv(id: string): Promise<AgentEnvResponse> {
    return this.fetch(`/api/agents/${id}/env`);
  }

  /**
   * Replaces an agent's `custom_env` wholesale. Values equal to
   * `"****"` are preserved server-side (the **** guard) so a partial
   * UI edit doesn't overwrite real secrets with the masked
   * placeholder. Owner/admin only; agent actors get a 403. Every
   * successful call writes an `agent_env_updated` activity_log row.
   * MUL-2600.
   */
  async updateAgentEnv(id: string, data: UpdateAgentEnvRequest): Promise<AgentEnvResponse> {
    return this.fetch(`/api/agents/${id}/env`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async restoreAgent(id: string): Promise<Agent> {
    return this.fetch(`/api/agents/${id}/restore`, { method: "POST" });
  }

  // Bulk-cancel every active task (queued/dispatched/running) for the agent.
  // Permission: agent owner or workspace admin/owner. Server returns the
  // count of cancelled rows; broadcasts task:cancelled for each so other
  // surfaces can clear their live cards.
  async cancelAgentTasks(id: string): Promise<{ cancelled: number }> {
    return this.fetch(`/api/agents/${id}/cancel-tasks`, { method: "POST" });
  }

  async listRuntimes(params?: { workspace_id?: string; owner?: "me" }): Promise<AgentRuntime[]> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    if (params?.owner) search.set("owner", params.owner);
    return this.fetch(`/api/runtimes?${search}`);
  }

  async listCloudRuntimeNodes(
    params?: ListCloudRuntimeNodesParams,
  ): Promise<CloudRuntimeNode[]> {
    const search = new URLSearchParams();
    if (params?.limit !== undefined) search.set("limit", String(params.limit));
    if (params?.offset !== undefined) search.set("offset", String(params.offset));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-runtime/nodes${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      CloudRuntimeNodeListSchema,
      EMPTY_CLOUD_RUNTIME_NODE_LIST,
      { endpoint: "GET /api/cloud-runtime/nodes" },
    );
  }

  async createCloudRuntimeNode(
    data: CreateCloudRuntimeNodeRequest,
  ): Promise<CloudRuntimeNode> {
    const res = await this.fetchRaw("/api/cloud-runtime/nodes", {
      method: "POST",
      body: JSON.stringify(data),
      extraHeaders: { "Content-Type": "application/json" },
    });
    const raw = await res.json() as unknown;
    return parseWithFallback(
      raw,
      CloudRuntimeNodeSchema,
      EMPTY_CLOUD_RUNTIME_NODE,
      { endpoint: "POST /api/cloud-runtime/nodes" },
    );
  }

  async deleteCloudRuntimeNode(instanceId: string): Promise<void> {
    await this.fetchRaw("/api/cloud-runtime/nodes", {
      method: "DELETE",
      body: JSON.stringify({ instance_id: instanceId }),
      extraHeaders: { "Content-Type": "application/json" },
    });
  }

  // ---------------------------------------------------------------------
  // Cloud Billing — proxies to agora-cloud /api/v1/billing/*. The
  // agora-api server stamps X-User-ID and forwards bytes; everything
  // here is upstream-shaped. See packages/core/types/billing.ts for the
  // response field documentation.
  // ---------------------------------------------------------------------

  async getCloudBillingBalance(): Promise<BillingBalance> {
    const raw = await this.fetch<unknown>("/api/cloud-billing/balance");
    return parseWithFallback(raw, BillingBalanceSchema, EMPTY_BILLING_BALANCE, {
      endpoint: "GET /api/cloud-billing/balance",
    });
  }

  async listCloudBillingTransactions(
    params?: { page?: number; page_size?: number },
  ): Promise<BillingTransactionsPage> {
    const search = new URLSearchParams();
    if (params?.page !== undefined) search.set("page", String(params.page));
    if (params?.page_size !== undefined) search.set("page_size", String(params.page_size));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/transactions${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      BillingTransactionsPageSchema,
      EMPTY_BILLING_TRANSACTIONS_PAGE,
      { endpoint: "GET /api/cloud-billing/transactions" },
    );
  }

  async listCloudBillingBatches(
    params?: { page?: number; page_size?: number },
  ): Promise<BillingBatchesPage> {
    const search = new URLSearchParams();
    if (params?.page !== undefined) search.set("page", String(params.page));
    if (params?.page_size !== undefined) search.set("page_size", String(params.page_size));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/batches${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      BillingBatchesPageSchema,
      EMPTY_BILLING_BATCHES_PAGE,
      { endpoint: "GET /api/cloud-billing/batches" },
    );
  }

  async listCloudBillingTopups(
    params?: { page?: number; page_size?: number },
  ): Promise<BillingTopupsPage> {
    const search = new URLSearchParams();
    if (params?.page !== undefined) search.set("page", String(params.page));
    if (params?.page_size !== undefined) search.set("page_size", String(params.page_size));
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/topups${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(
      raw,
      BillingTopupsPageSchema,
      EMPTY_BILLING_TOPUPS_PAGE,
      { endpoint: "GET /api/cloud-billing/topups" },
    );
  }

  async listCloudBillingPriceTiers(): Promise<BillingPriceTier[]> {
    const raw = await this.fetch<unknown>("/api/cloud-billing/price-tiers");
    return parseWithFallback(
      raw,
      BillingPriceTierListSchema,
      EMPTY_BILLING_PRICE_TIER_LIST,
      { endpoint: "GET /api/cloud-billing/price-tiers" },
    );
  }

  async createCloudBillingCheckoutSession(
    data: CreateBillingCheckoutSessionRequest,
  ): Promise<CreateBillingCheckoutSessionResponse> {
    const res = await this.fetchRaw("/api/cloud-billing/checkout-sessions", {
      method: "POST",
      body: JSON.stringify(data),
      extraHeaders: { "Content-Type": "application/json" },
    });
    const raw = (await res.json()) as unknown;
    return parseWithFallback(
      raw,
      CreateBillingCheckoutSessionResponseSchema,
      EMPTY_CREATE_BILLING_CHECKOUT_SESSION_RESPONSE,
      { endpoint: "POST /api/cloud-billing/checkout-sessions" },
    );
  }

  async getCloudBillingCheckoutSession(
    sessionId: string,
  ): Promise<BillingCheckoutSessionStatus> {
    // Stripe session ids are `cs_<base62>` so they're URL-safe by
    // construction; encodeURIComponent is paranoia for the case where a
    // future Stripe format change adds a non-alphanumeric character. The
    // server has its own allow-list rejection for unsafe ids.
    const raw = await this.fetch<unknown>(
      `/api/cloud-billing/checkout-sessions/${encodeURIComponent(sessionId)}`,
    );
    return parseWithFallback(
      raw,
      BillingCheckoutSessionStatusSchema,
      EMPTY_BILLING_CHECKOUT_SESSION_STATUS,
      { endpoint: "GET /api/cloud-billing/checkout-sessions/{sessionId}" },
    );
  }

  async createCloudBillingPortalSession(): Promise<CreateBillingPortalSessionResponse> {
    const res = await this.fetchRaw("/api/cloud-billing/portal-sessions", {
      method: "POST",
      // Body is intentionally absent — the upstream endpoint requires no
      // payload today. fetchRaw with no body skips the Content-Type
      // default; that's fine because there's nothing to declare.
    });
    const raw = (await res.json()) as unknown;
    return parseWithFallback(
      raw,
      CreateBillingPortalSessionResponseSchema,
      EMPTY_CREATE_BILLING_PORTAL_SESSION_RESPONSE,
      { endpoint: "POST /api/cloud-billing/portal-sessions" },
    );
  }

  async deleteRuntime(runtimeId: string): Promise<void> {
    await this.fetch(`/api/runtimes/${runtimeId}`, { method: "DELETE" });
  }

  // Settings → Labs: workspace-level experimental flags (QA-env routing).
  async getWorkspaceLabs(): Promise<WorkspaceLabs> {
    const raw = await this.fetch<unknown>("/api/workspace-labs");
    return parseWithFallback(raw, WorkspaceLabsSchema, EMPTY_WORKSPACE_LABS, {
      endpoint: "GET /api/workspace-labs",
    });
  }

  async updateWorkspaceLabs(labs: WorkspaceLabs): Promise<WorkspaceLabs> {
    const raw = await this.fetch<unknown>("/api/workspace-labs", {
      method: "PUT",
      body: JSON.stringify(labs),
    });
    return parseWithFallback(raw, WorkspaceLabsSchema, EMPTY_WORKSPACE_LABS, {
      endpoint: "PUT /api/workspace-labs",
    });
  }

  // Cascade variant of deleteRuntime. The strict DELETE refuses with
  // structured 409 (`code: "runtime_has_active_agents"`, body carries the
  // blocking agents) when active agents are bound; the front-end then opens
  // the cascade-mode confirmation dialog and submits the user-confirmed
  // active agent set here. Server compares the snapshot to the live set
  // inside the transaction and refuses with `code: "runtime_delete_plan_changed"`
  // (same shape, fresh `active_agents`) if they don't match — caller should
  // re-render the agent list and force the user to re-confirm.
  async archiveAgentsAndDeleteRuntime(
    runtimeId: string,
    expectedActiveAgentIds: string[],
  ): Promise<{ status: string; agents_archived: number; tasks_cancelled: number }> {
    return this.fetch(`/api/runtimes/${runtimeId}/archive-agents-and-delete`, {
      method: "POST",
      body: JSON.stringify({ expected_active_agent_ids: expectedActiveAgentIds }),
    });
  }

  async updateRuntime(
    runtimeId: string,
    patch: { visibility?: "private" | "public" },
  ): Promise<AgentRuntime> {
    return this.fetch(`/api/runtimes/${runtimeId}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    });
  }

  async getRuntimeUsage(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsage[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    // `tz` drives the calendar-day boundary for the trend chart (Viewing
    // layer). Caller-supplied; the backend falls back to user.timezone /
    // UTC if omitted.
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage?${search}`,
    );
    return parseWithFallback<RuntimeUsage[]>(raw, RuntimeUsageListSchema, [], {
      endpoint: "GET /api/runtimes/:id/usage",
    });
  }

  async getRuntimeTaskActivity(
    runtimeId: string,
    params?: { tz?: string },
  ): Promise<RuntimeHourlyActivity[]> {
    // Hour-of-day heatmap follows the viewer's tz, like the other reports on
    // this page. Pass the viewer's IANA zone so the server buckets correctly.
    const search = new URLSearchParams();
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/activity?${search}`,
    );
    return parseWithFallback<RuntimeHourlyActivity[]>(
      raw,
      RuntimeHourlyActivityListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/activity" },
    );
  }

  async getRuntimeUsageByAgent(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsageByAgent[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage/by-agent?${search}`,
    );
    return parseWithFallback<RuntimeUsageByAgent[]>(
      raw,
      RuntimeUsageByAgentListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/usage/by-agent" },
    );
  }

  async getRuntimeUsageByHour(
    runtimeId: string,
    params?: { days?: number; tz?: string },
  ): Promise<RuntimeUsageByHour[]> {
    const search = new URLSearchParams();
    if (params?.days) search.set("days", String(params.days));
    if (params?.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/usage/by-hour?${search}`,
    );
    return parseWithFallback<RuntimeUsageByHour[]>(
      raw,
      RuntimeUsageByHourListSchema,
      [],
      { endpoint: "GET /api/runtimes/:id/usage/by-hour" },
    );
  }

  // ---------------------------------------------------------------------------
  // Workspace dashboard — three independent rollups for `/{slug}/dashboard`.
  // Each accepts an optional `project_id` to narrow the scope to one project.
  // Cost is computed client-side from the model pricing table (same contract
  // as the per-runtime endpoints above).
  // ---------------------------------------------------------------------------

  async getDashboardUsageDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardUsageDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/usage/daily?${search}`);
    return parseWithFallback<DashboardUsageDaily[]>(
      raw,
      DashboardUsageDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/usage/daily" },
    );
  }

  async getDashboardUsageByAgent(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardUsageByAgent[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/usage/by-agent?${search}`);
    return parseWithFallback<DashboardUsageByAgent[]>(
      raw,
      DashboardUsageByAgentListSchema,
      [],
      { endpoint: "GET /api/dashboard/usage/by-agent" },
    );
  }

  async getDashboardAgentRunTime(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardAgentRunTime[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` aligns the "last N days" cutoff with the viewer's calendar,
    // matching the per-agent token card.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/agent-runtime?${search}`);
    return parseWithFallback<DashboardAgentRunTime[]>(
      raw,
      DashboardAgentRunTimeListSchema,
      [],
      { endpoint: "GET /api/dashboard/agent-runtime" },
    );
  }

  async getDashboardRunTimeDaily(
    params: { days?: number; project_id?: string | null; tz?: string },
  ): Promise<DashboardRunTimeDaily[]> {
    const search = new URLSearchParams();
    if (params.days) search.set("days", String(params.days));
    if (params.project_id) search.set("project_id", params.project_id);
    // `tz` cuts the day buckets in the viewer's calendar so Time / Tasks
    // align with the Cost / Tokens charts.
    if (params.tz) search.set("tz", params.tz);
    const raw = await this.fetch<unknown>(`/api/dashboard/runtime/daily?${search}`);
    return parseWithFallback<DashboardRunTimeDaily[]>(
      raw,
      DashboardRunTimeDailyListSchema,
      [],
      { endpoint: "GET /api/dashboard/runtime/daily" },
    );
  }

  async initiateUpdate(
    runtimeId: string,
    targetVersion: string,
  ): Promise<RuntimeUpdate> {
    return this.fetch(`/api/runtimes/${runtimeId}/update`, {
      method: "POST",
      body: JSON.stringify({ target_version: targetVersion }),
    });
  }

  async getUpdateResult(
    runtimeId: string,
    updateId: string,
  ): Promise<RuntimeUpdate> {
    return this.fetch(`/api/runtimes/${runtimeId}/update/${updateId}`);
  }

  async initiateListModels(runtimeId: string): Promise<RuntimeModelListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/models`, { method: "POST" });
  }

  async getListModelsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeModelListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/models/${requestId}`);
  }

  async initiateListLocalSkills(
    runtimeId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills`, {
      method: "POST",
    });
  }

  async getListLocalSkillsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/${requestId}`);
  }

  async initiateImportLocalSkill(
    runtimeId: string,
    data: CreateRuntimeLocalSkillImportRequest,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async getImportLocalSkillResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch(`/api/runtimes/${runtimeId}/local-skills/import/${requestId}`);
  }

  async listAgentTasks(agentId: string): Promise<AgentTask[]> {
    return this.fetch(`/api/agents/${agentId}/tasks`);
  }

  // Workspace-scoped agent task snapshot: every active task
  // (queued/dispatched/running) plus each agent's most recent terminal task.
  // Powers the front-end's "active wins, else latest terminal" presence
  // derivation; one fetch backs every per-agent presence read in the app.
  // Workspace is resolved server-side from the X-Workspace-Slug header.
  async getAgentTaskSnapshot(): Promise<AgentTask[]> {
    return this.fetch(`/api/agent-task-snapshot`);
  }

  // Per-agent daily activity for the last 30 days, anchored on
  // completed_at. One workspace-wide fetch backs both the Agents-list
  // sparkline (uses trailing 7 buckets) and the agent detail "Last 30
  // days" panel (uses all 30).
  async getWorkspaceAgentActivity30d(): Promise<AgentActivityBucket[]> {
    return this.fetch(`/api/agent-activity-30d`);
  }

  // Per-agent 30-day total run count for the Agents-list RUNS column.
  async getWorkspaceAgentRunCounts(): Promise<AgentRunCount[]> {
    return this.fetch(`/api/agent-run-counts`);
  }

  async getActiveTasksForIssue(issueId: string): Promise<{ tasks: AgentTask[] }> {
    return this.fetch(`/api/issues/${issueId}/active-task`);
  }

  async listTaskMessages(taskId: string): Promise<TaskMessagePayload[]> {
    return this.fetch(`/api/tasks/${taskId}/messages`);
  }

  async listTasksByIssue(issueId: string): Promise<AgentTask[]> {
    return this.fetch(`/api/issues/${issueId}/task-runs`);
  }

  async getIssueUsage(issueId: string): Promise<IssueUsageSummary> {
    return this.fetch(`/api/issues/${issueId}/usage`);
  }

  async getIssueOrchestration(issueId: string): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration`);
    if (raw === null) return null;
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "GET /api/issues/{id}/orchestration",
    });
  }

  async getIssueArtifact(issueId: string, stepId?: string): Promise<IssueArtifactResponse> {
    const query = stepId ? `?step_id=${encodeURIComponent(stepId)}` : "";
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/artifact${query}`);
    return parseWithFallback(raw, IssueArtifactResponseSchema, EMPTY_ISSUE_ARTIFACT, {
      endpoint: "GET /api/issues/{id}/artifact",
    });
  }

  async createIssueOrchestration(issueId: string, data: CreateOrchestrationRequest): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "POST /api/issues/{id}/orchestration",
    });
  }

  async startIssueOrchestration(issueId: string): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration/start`, { method: "POST" });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "POST /api/issues/{id}/orchestration/start",
    });
  }

  async editIssueOrchestration(issueId: string, data: import("../types").EditOrchestrationRequest): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration`, { method: "PATCH", body: JSON.stringify(data) });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, { endpoint: "PATCH /api/issues/{id}/orchestration" });
  }

  async approveOrchestrationStep(issueId: string, stepId: string): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration/steps/${stepId}/approve`, { method: "POST" });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "POST /api/issues/{id}/orchestration/steps/{stepId}/approve",
    });
  }

  async cancelOrchestrationBranch(issueId: string, stepId: string): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration/steps/${stepId}/cancel-branch`, { method: "POST" });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "POST /api/issues/{id}/orchestration/steps/{stepId}/cancel-branch",
    });
  }

  async retryOrchestrationStep(issueId: string, stepId: string): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration/steps/${stepId}/retry`, { method: "POST" });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "POST /api/issues/{id}/orchestration/steps/{stepId}/retry",
    });
  }

  async respondToOrchestrationStep(issueId: string, stepId: string, data: RespondToOrchestrationStepRequest): Promise<OrchestrationRun | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/orchestration/steps/${stepId}/respond`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, OrchestrationRunSchema, EMPTY_ORCHESTRATION_RUN, {
      endpoint: "POST /api/issues/{id}/orchestration/steps/{stepId}/respond",
    });
  }

  async cancelTask(issueId: string, taskId: string): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/tasks/${taskId}/cancel`, {
      method: "POST",
    });
  }

  async rerunIssue(issueId: string, taskId?: string): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/rerun`, {
      method: "POST",
      body: JSON.stringify(taskId ? { task_id: taskId } : {}),
    });
  }

  // Queue a "steer" for an agent mid-task on the issue: force-enqueue a
  // resuming follow-up (bypassing the in-flight mention dedupe) that the agent
  // picks up the moment its current turn ends, keeping context. Post the
  // steering message as a comment first and pass its id as commentId.
  async steerIssue(
    issueId: string,
    agentId: string,
    commentId?: string,
  ): Promise<AgentTask> {
    return this.fetch(`/api/issues/${issueId}/steer`, {
      method: "POST",
      body: JSON.stringify({ agent_id: agentId, comment_id: commentId }),
    });
  }

  // Deterministic merge-readiness gate (ci/qa/security/code-review verdicts from
  // labels, tiered by blast radius). Read-only.
  async mergeReadiness(issueId: string): Promise<MergeReadiness> {
    return this.fetch(`/api/issues/${issueId}/merge-readiness`);
  }

  // The Review lens' evidence read: the latest run_review code-review verdict
  // for an issue, resolved server-side from the newest agent comment carrying
  // a parsable ```review-result``` block. verdict "none" (a normal response
  // for a never-reviewed issue, and this parse's fallback) renders the "No
  // review yet" empty state.
  async getReviewVerdict(issueId: string): Promise<ReviewVerdict> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/review-verdict`);
    return parseWithFallback(raw, ReviewVerdictSchema, EMPTY_REVIEW_VERDICT, {
      endpoint: "GET /api/issues/:id/review-verdict",
    });
  }

  // --- Escalations ---------------------------------------------------------
  // An agent stopped and asked. Read is open to anyone who can see the issue;
  // resolve/cancel are human-only server-side (RequireHumanActor).

  async listIssueEscalations(issueId: string): Promise<Escalation[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/escalations`);
    return parseWithFallback(raw, EscalationListSchema, EMPTY_ESCALATION_LIST, {
      endpoint: "GET /api/issues/:id/escalations",
    }).escalations;
  }

  // Every open escalation in the workspace, oldest first — the decision
  // queue's read.
  async listOpenEscalations(): Promise<Escalation[]> {
    const raw = await this.fetch<unknown>("/api/escalations");
    return parseWithFallback(raw, EscalationListSchema, EMPTY_ESCALATION_LIST, {
      endpoint: "GET /api/escalations",
    }).escalations;
  }

  // Answer an escalation. The answer is posted as a comment and becomes the
  // resumed run's prompt, so free text and a picked option are the same
  // mechanism — which is why "redirect the agent" needs no separate verb.
  // 409 when someone else answered first.
  async resolveEscalation(escalationId: string, answer: string): Promise<Escalation> {
    const raw = await this.fetch<unknown>(`/api/escalations/${escalationId}/resolve`, {
      method: "POST",
      body: JSON.stringify({ answer }),
    });
    return parseWithFallback(raw, EscalationSchema, EMPTY_ESCALATION, {
      endpoint: "POST /api/escalations/:id/resolve",
    });
  }

  // Withdraw the question without answering it. The parked run is NOT
  // resumed — whoever cancels owns what happens next.
  async cancelEscalation(escalationId: string): Promise<Escalation> {
    const raw = await this.fetch<unknown>(`/api/escalations/${escalationId}/cancel`, {
      method: "POST",
    });
    return parseWithFallback(raw, EscalationSchema, EMPTY_ESCALATION, {
      endpoint: "POST /api/escalations/:id/cancel",
    });
  }

  // The human half of "agent reviews, human approves". approve verifies the
  // deterministic gates server-side (409 with qa_gate_not_passed / qa_failed /
  // review_failed in the message when they block; merge:override bypasses)
  // and dispatches the merge order to the squad lead; request_changes needs a
  // non-empty note and atomically creates the next correction DAG revision.
  // Human-only: the route 403s machine actors.
  async reviewDecision(
    issueId: string,
    body: {
      action: "approve" | "request_changes";
      note?: string;
      expectedVersion?: number;
      targetStepId?: string;
    },
  ): Promise<ReviewDecisionResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/review-decision`, {
      method: "POST",
      body: JSON.stringify({
        action: body.action,
        ...(body.note ? { note: body.note } : {}),
        ...(body.expectedVersion ? { expected_version: body.expectedVersion } : {}),
        ...(body.targetStepId ? { target_step_id: body.targetStepId } : {}),
      }),
    });
    return parseWithFallback(raw, ReviewDecisionResponseSchema, EMPTY_REVIEW_DECISION, {
      endpoint: "POST /api/issues/:id/review-decision",
    });
  }

  // Inbox
  async listInbox(): Promise<InboxItem[]> {
    return this.fetch("/api/inbox");
  }

  async markInboxRead(id: string): Promise<InboxItem> {
    return this.fetch(`/api/inbox/${id}/read`, { method: "POST" });
  }

  async archiveInbox(id: string): Promise<InboxItem> {
    return this.fetch(`/api/inbox/${id}/archive`, { method: "POST" });
  }

  async getUnreadInboxCount(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/unread-count");
  }

  async markAllInboxRead(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/mark-all-read", { method: "POST" });
  }

  async archiveAllInbox(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/archive-all", { method: "POST" });
  }

  async archiveAllReadInbox(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/archive-all-read", { method: "POST" });
  }

  async archiveCompletedInbox(): Promise<{ count: number }> {
    return this.fetch("/api/inbox/archive-completed", { method: "POST" });
  }

  // Notification preferences
  //
  // `workspaceSlug` overrides the default `X-Workspace-Slug` header (which
  // follows the active workspace) so a caller can read a SPECIFIC workspace's
  // preferences — e.g. honoring the mute setting of the workspace an inbox
  // notification came from while the user is viewing a different one (#3766).
  async getNotificationPreferences(workspaceSlug?: string): Promise<NotificationPreferenceResponse> {
    return this.fetch(
      "/api/notification-preferences",
      workspaceSlug ? { headers: { "X-Workspace-Slug": workspaceSlug } } : undefined,
    );
  }

  async updateNotificationPreferences(preferences: NotificationPreferences): Promise<NotificationPreferenceResponse> {
    return this.fetch("/api/notification-preferences", {
      method: "PUT",
      body: JSON.stringify({ preferences }),
    });
  }

  // App Config
  async getConfig(): Promise<AppConfigResponse> {
    const raw = await this.fetch<unknown>("/api/config");
    return parseWithFallback<AppConfigResponse>(raw, AppConfigSchema, EMPTY_APP_CONFIG, {
      endpoint: "GET /api/config",
    });
  }

  // Workspaces
  async listWorkspaces(): Promise<Workspace[]> {
    return this.fetch("/api/workspaces");
  }

  async getWorkspace(id: string): Promise<Workspace> {
    return this.fetch(`/api/workspaces/${id}`);
  }

  async createWorkspace(data: { name: string; slug: string; description?: string; context?: string }): Promise<Workspace> {
    return this.fetch("/api/workspaces", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateWorkspace(id: string, data: { name?: string; description?: string; context?: string; settings?: Record<string, unknown>; repos?: WorkspaceRepo[]; issue_prefix?: string; avatar_url?: string }): Promise<Workspace> {
    return this.fetch(`/api/workspaces/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  /**
   * The sidebar members of this workspace get until they set their own
   * (PUT /api/workspaces/{id}/team-sidebar, owner/admin). `hidden` is a list
   * of nav keys; `settings` is rejected server-side, `[]` means "show all".
   * Returns the whole workspace, or EMPTY_WORKSPACE (id "") on drift.
   */
  async updateTeamSidebar(workspaceId: string, hidden: string[]): Promise<Workspace> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/team-sidebar`, {
      method: "PUT",
      body: JSON.stringify({ hidden }),
    });
    return parseWithFallback(raw, WorkspaceSchema, EMPTY_WORKSPACE, {
      endpoint: "PUT /api/workspaces/:id/team-sidebar",
    });
  }

  /**
   * Records that the department setup was finished or skipped, so owners and
   * admins are not prompted again (POST /api/workspaces/{id}/department-setup).
   * Returns the whole workspace, or EMPTY_WORKSPACE (id "") on drift.
   */
  async setDepartmentSetup(
    workspaceId: string,
    status: DepartmentSetupStatus,
  ): Promise<Workspace> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/department-setup`, {
      method: "POST",
      body: JSON.stringify({ status }),
    });
    return parseWithFallback(raw, WorkspaceSchema, EMPTY_WORKSPACE, {
      endpoint: "POST /api/workspaces/:id/department-setup",
    });
  }

  // Workspace-default MCP servers. The JSON document uses the same
  // { mcpServers: { ... } } shape as Claude Desktop and Cursor mcp.json.
  // It is merged into every claimed agent task; an agent-level server with
  // the same name wins as an explicit override.
  async getWorkspaceMcpConfig(id: string): Promise<WorkspaceMcpConfigResponse> {
    return this.fetch(`/api/workspaces/${id}/default-mcp-config`);
  }

  async updateWorkspaceMcpConfig(
    id: string,
    mcpConfig: unknown | null,
  ): Promise<WorkspaceMcpConfigResponse> {
    return this.fetch(`/api/workspaces/${id}/default-mcp-config`, {
      method: "PUT",
      body: JSON.stringify({ mcp_config: mcpConfig }),
    });
  }

  // Members
  async listMembers(workspaceId: string): Promise<MemberWithUser[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/members`);
  }

  // Display info for every user referenced in the workspace (comment authors,
  // assignees, creators, members) — including people no longer on the team. Used
  // only for name/avatar resolution, never for pickers.
  async listActorDirectory(workspaceId: string): Promise<ActorDirectoryEntry[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/actor-directory`);
  }

  async createMember(workspaceId: string, data: CreateMemberRequest): Promise<Invitation> {
    return this.fetch(`/api/workspaces/${workspaceId}/members`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateMember(workspaceId: string, memberId: string, data: UpdateMemberRequest): Promise<MemberWithUser> {
    return this.fetch(`/api/workspaces/${workspaceId}/members/${memberId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async deleteMember(workspaceId: string, memberId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/members/${memberId}`, {
      method: "DELETE",
    });
  }

  async leaveWorkspace(workspaceId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/leave`, {
      method: "POST",
    });
  }

  // Invitations
  async listWorkspaceInvitations(workspaceId: string): Promise<Invitation[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/invitations`);
  }

  async revokeInvitation(workspaceId: string, invitationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/invitations/${invitationId}`, {
      method: "DELETE",
    });
  }

  async listMyInvitations(): Promise<Invitation[]> {
    return this.fetch("/api/invitations");
  }

  async getInvitationAuthInfo(invitationId: string): Promise<InvitationAuthInfo> {
    return this.fetch(`/auth/invitations/${invitationId}`);
  }

  async getInvitation(invitationId: string): Promise<Invitation> {
    return this.fetch(`/api/invitations/${invitationId}`);
  }

  async acceptInvitation(invitationId: string): Promise<MemberWithUser> {
    return this.fetch(`/api/invitations/${invitationId}/accept`, {
      method: "POST",
    });
  }

  async declineInvitation(invitationId: string): Promise<void> {
    await this.fetch(`/api/invitations/${invitationId}/decline`, {
      method: "POST",
    });
  }

  async deleteWorkspace(workspaceId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}`, {
      method: "DELETE",
    });
  }

  // Skills
  async listSkills(): Promise<SkillSummary[]> {
    return this.fetch("/api/skills");
  }

  async getSkill(id: string): Promise<Skill> {
    return this.fetch(`/api/skills/${id}`);
  }

  async createSkill(data: CreateSkillRequest): Promise<Skill> {
    return this.fetch("/api/skills", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateSkill(id: string, data: UpdateSkillRequest): Promise<Skill> {
    return this.fetch(`/api/skills/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteSkill(id: string): Promise<void> {
    await this.fetch(`/api/skills/${id}`, { method: "DELETE" });
  }

  async importSkill(data: { url: string }): Promise<Skill> {
    return this.fetch("/api/skills/import", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async listAgentSkills(agentId: string): Promise<SkillSummary[]> {
    return this.fetch(`/api/agents/${agentId}/skills`);
  }

  async setAgentSkills(agentId: string, data: SetAgentSkillsRequest): Promise<void> {
    await this.fetch(`/api/agents/${agentId}/skills`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // Idempotently attaches skills to an agent — server does ON CONFLICT DO
  // NOTHING, so it never clobbers existing agent_skill links (unlike
  // setAgentSkills, which replaces the set wholesale via PUT).
  async addAgentSkills(agentId: string, skillIds: string[]): Promise<void> {
    await this.fetch(`/api/agents/${agentId}/skills/add`, {
      method: "POST",
      body: JSON.stringify({ skill_ids: skillIds }),
    });
  }

  // Plugins
  // A plugin bundles workspace skills + MCP connectors and installs them onto
  // agents as a unit. Workspace is resolved server-side from the
  // X-Workspace-Slug header (the same path the rest of the workspace-scoped
  // endpoints use). The list response redacts `mcp_config` env values to "***".
  async listPlugins(): Promise<Plugin[]> {
    const res = await this.fetch<{ plugins?: Plugin[] }>("/api/plugins");
    return res.plugins ?? [];
  }

  async createPlugin(data: CreatePluginRequest): Promise<{ id: string }> {
    return this.fetch("/api/plugins", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async deletePlugin(id: string): Promise<void> {
    await this.fetch(`/api/plugins/${id}`, { method: "DELETE" });
  }

  // Binds the plugin's skills + merges its connectors into each agent's
  // mcp_config (server-side merge preserves existing data). Returns the count
  // of agents the plugin was installed onto.
  async installPlugin(id: string, agentIds: string[]): Promise<{ installed: number }> {
    return this.fetch(`/api/plugins/${id}/install`, {
      method: "POST",
      body: JSON.stringify({ agent_ids: agentIds }),
    });
  }

  async uninstallPlugin(id: string, agentIds: string[]): Promise<{ uninstalled: number }> {
    return this.fetch(`/api/plugins/${id}/uninstall`, {
      method: "POST",
      body: JSON.stringify({ agent_ids: agentIds }),
    });
  }

  // Personal Access Tokens
  async listPersonalAccessTokens(): Promise<PersonalAccessToken[]> {
    return this.fetch("/api/tokens");
  }

  async createPersonalAccessToken(data: CreatePersonalAccessTokenRequest): Promise<CreatePersonalAccessTokenResponse> {
    return this.fetch("/api/tokens", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async revokePersonalAccessToken(id: string): Promise<void> {
    await this.fetch(`/api/tokens/${id}`, { method: "DELETE" });
  }

  // File Upload & Attachments
  async uploadFile(
    file: File,
    opts?: { issueId?: string; commentId?: string; chatSessionId?: string; workspaceId?: string },
  ): Promise<Attachment> {
    const formData = new FormData();
    formData.append("file", file);
    if (opts?.issueId) formData.append("issue_id", opts.issueId);
    if (opts?.commentId) formData.append("comment_id", opts.commentId);
    if (opts?.chatSessionId) formData.append("chat_session_id", opts.chatSessionId);

    const rid = createRequestId();
    const start = Date.now();
    this.logger.info("→ POST /api/upload-file", { rid });

    const headers = this.authHeaders();
    if (opts?.workspaceId) {
      // The upload route resolves the slug header before the ID header.
      // A selected assistant workspace may differ from the current page.
      delete headers["X-Workspace-Slug"];
      headers["X-Workspace-ID"] = opts.workspaceId;
    }
    const res = await fetch(`${this.baseUrl}/api/upload-file`, {
      method: "POST",
      headers,
      body: formData,
      credentials: "include",
    });

    if (!res.ok) {
      if (res.status === 401) this.handleUnauthorized();
      const message = await this.parseErrorMessage(res, `Upload failed: ${res.status}`);
      this.logger.error(`← ${res.status} /api/upload-file`, { rid, duration: `${Date.now() - start}ms`, error: message });
      throw new Error(message);
    }

    this.logger.info(`← ${res.status} /api/upload-file`, { rid, duration: `${Date.now() - start}ms` });
    const raw = (await res.json()) as unknown;
    return parseWithFallback(raw, AttachmentResponseSchema, EMPTY_ATTACHMENT, {
      endpoint: "POST /api/upload-file",
    });
  }

  // Chat Sessions
  async listChatSessions(params?: { status?: string }): Promise<ChatSession[]> {
    const query = params?.status ? `?status=${params.status}` : "";
    return this.fetch(`/api/chat/sessions${query}`);
  }

  async getChatSession(id: string): Promise<ChatSession> {
    return this.fetch(`/api/chat/sessions/${id}`);
  }

  async createChatSession(data: { agent_id: string; title?: string }): Promise<ChatSession> {
    return this.fetch("/api/chat/sessions", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async deleteChatSession(id: string): Promise<void> {
    await this.fetch(`/api/chat/sessions/${id}`, { method: "DELETE" });
  }

  async updateChatSession(id: string, data: { title: string }): Promise<ChatSession> {
    return this.fetch(`/api/chat/sessions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async listChatMessages(sessionId: string): Promise<ChatMessage[]> {
    return this.fetch(`/api/chat/sessions/${sessionId}/messages`);
  }

  async listChatMessagesPage(
    sessionId: string,
    params: { before?: { created_at: string; id: string } | null; limit?: number } = {},
  ): Promise<ChatMessagesPage> {
    const limit = params.limit ?? 50;
    const query = new URLSearchParams({ limit: String(limit) });
    if (params.before) {
      query.set("before_created_at", params.before.created_at);
      query.set("before_id", params.before.id);
    }
    try {
      return await this.fetch(
        `/api/chat/sessions/${sessionId}/messages/page?${query.toString()}`,
      );
    } catch (err) {
      // Deployment-order compatibility: a backend deployed before this endpoint
      // existed returns 404 for the unknown route. Fall back to the legacy
      // full-list endpoint so chat never white-screens regardless of whether
      // the server or the client deploys first. Only the initial (cursorless)
      // page falls back — the legacy endpoint returns every message at once, so
      // the fallback page reports has_more: false and there is no follow-up
      // request to translate. A 404 on a cursor request is an unexpected state
      // and propagates instead of duplicating the whole list.
      if (err instanceof ApiError && err.status === 404 && !params.before) {
        const messages = await this.listChatMessages(sessionId);
        return { messages, limit, has_more: false, next_cursor: null };
      }
      throw err;
    }
  }

  async sendChatMessage(
    sessionId: string,
    content: string,
    attachmentIds?: string[],
  ): Promise<SendChatMessageResponse> {
    const body: { content: string; attachment_ids?: string[] } = { content };
    if (attachmentIds && attachmentIds.length > 0) {
      body.attachment_ids = attachmentIds;
    }
    return this.fetch(`/api/chat/sessions/${sessionId}/messages`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async getPendingChatTask(sessionId: string): Promise<ChatPendingTask> {
    return this.fetch(`/api/chat/sessions/${sessionId}/pending-task`);
  }

  async listPendingChatTasks(): Promise<PendingChatTasksResponse> {
    return this.fetch(`/api/chat/pending-tasks`);
  }

  async markChatSessionRead(sessionId: string): Promise<void> {
    await this.fetch(`/api/chat/sessions/${sessionId}/read`, { method: "POST" });
  }

  // Agora Assistant — user-scoped (no X-Workspace-ID needed). See
  // docs/agora-assistant-plan.md and server/internal/handler/assistant.go.

  async createAssistantSession(
    data?: CreateAssistantSessionRequest,
  ): Promise<AssistantSession> {
    const raw = await this.fetch<unknown>("/api/assistant/sessions", {
      method: "POST",
      body: data ? JSON.stringify(data) : undefined,
    });
    return parseWithFallback(raw, AssistantSessionSchema, EMPTY_ASSISTANT_SESSION, {
      endpoint: "POST /api/assistant/sessions",
    });
  }

  async listAssistantSessions(): Promise<AssistantSession[]> {
    const raw = await this.fetch<unknown>("/api/assistant/sessions");
    return parseWithFallback(raw, AssistantSessionListSchema, EMPTY_ASSISTANT_SESSION_LIST, {
      endpoint: "GET /api/assistant/sessions",
    });
  }

  async getAssistantSession(id: string): Promise<AssistantSession> {
    const raw = await this.fetch<unknown>(`/api/assistant/sessions/${id}`);
    return parseWithFallback(raw, AssistantSessionSchema, EMPTY_ASSISTANT_SESSION, {
      endpoint: "GET /api/assistant/sessions/{id}",
    });
  }

  async updateAssistantSession(
    id: string,
    data: PatchAssistantSessionRequest,
  ): Promise<AssistantSession> {
    const raw = await this.fetch<unknown>(`/api/assistant/sessions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, AssistantSessionSchema, EMPTY_ASSISTANT_SESSION, {
      endpoint: "PATCH /api/assistant/sessions/{id}",
    });
  }

  async deleteAssistantSession(id: string): Promise<void> {
    await this.fetch(`/api/assistant/sessions/${id}`, { method: "DELETE" });
  }

  async listAssistantMessages(sessionId: string): Promise<AssistantMessage[]> {
    const raw = await this.fetch<unknown>(`/api/assistant/sessions/${sessionId}/messages`);
    return parseWithFallback(raw, AssistantMessageListSchema, EMPTY_ASSISTANT_MESSAGE_LIST, {
      endpoint: "GET /api/assistant/sessions/{id}/messages",
    });
  }

  async listAssistantRuns(sessionId: string): Promise<AssistantRun[]> {
    const raw = await this.fetch<unknown>(`/api/assistant/sessions/${sessionId}/runs`);
    return parseWithFallback(raw, AssistantRunListSchema, EMPTY_ASSISTANT_RUN_LIST, {
      endpoint: "GET /api/assistant/sessions/{id}/runs",
    });
  }

  async getAssistantRun(runId: string): Promise<AssistantRun> {
    const raw = await this.fetch<unknown>(`/api/assistant/runs/${runId}`);
    return parseWithFallback(raw, AssistantRunSchema, EMPTY_ASSISTANT_RUN, {
      endpoint: "GET /api/assistant/runs/{id}",
    });
  }

  async sendAssistantMessage(
    sessionId: string,
    input: SendAssistantMessageRequest,
  ): Promise<SendAssistantMessageResponse> {
    const raw = await this.fetch<unknown>(`/api/assistant/sessions/${sessionId}/messages`, {
      method: "POST",
      body: JSON.stringify(input),
    });
    return parseWithFallback(
      raw,
      SendAssistantMessageResponseSchema,
      EMPTY_SEND_ASSISTANT_MESSAGE_RESPONSE,
      { endpoint: "POST /api/assistant/sessions/{id}/messages" },
    );
  }

  async cancelAssistantRun(runId: string): Promise<void> {
    await this.fetch(`/api/assistant/runs/${runId}/cancel`, { method: "POST" });
  }

  async getAssistantAvailability(): Promise<AssistantAvailability> {
    const raw = await this.fetch<unknown>("/api/assistant/availability");
    return parseWithFallback(raw, AssistantAvailabilitySchema, EMPTY_ASSISTANT_AVAILABILITY, {
      endpoint: "GET /api/assistant/availability",
    });
  }

  async getAssistantArtifact(id: string): Promise<AssistantArtifact> {
    const raw = await this.fetch<unknown>(`/api/assistant/artifacts/${id}`);
    return parseWithFallback(raw, AssistantArtifactSchema, EMPTY_ASSISTANT_ARTIFACT, {
      endpoint: "GET /api/assistant/artifacts/{id}",
    });
  }

  /**
   * Records the out-of-band human confirmation for a pending destructive
   * operation and executes it server-side. See docs/agora-assistant-final-plan.md
   * ("Pinned wire contract").
   *
   * Throws `ApiError` with status 409 when the bound operation changed or
   * expired (the card then flips to its expired state), and 404 while a
   * runtime that predates confirmation binding is still deployed — the card
   * degrades to a toast in both cases rather than crashing the transcript.
   */
  async confirmAssistantOperation(
    operationId: string,
    /**
     * 0-based rows the user unchecked on a plan card. The body is sent ONLY
     * when something is actually skipped — an empty or omitted list produces
     * byte-for-byte the bodiless POST single-operation confirms have always
     * sent, so a runtime that predates plans sees no change at all.
     */
    skippedItems?: readonly number[],
  ): Promise<AssistantOperationDecision> {
    const skipped = (skippedItems ?? []).filter((index) => Number.isInteger(index) && index >= 0);
    const raw = await this.fetch<unknown>(
      `/api/assistant/operations/${operationId}/confirm`,
      {
        method: "POST",
        ...(skipped.length > 0 ? { body: JSON.stringify({ skipped_items: skipped }) } : {}),
      },
    );
    return this.assistantOperationDecision(raw, "POST /api/assistant/operations/{id}/confirm");
  }

  async getAssistantOperation(operationId: string): Promise<AssistantOperation> {
    const raw = await this.fetch<unknown>(`/api/assistant/operations/${operationId}`);
    return parseWithFallback(raw, AssistantOperationSchema, EMPTY_ASSISTANT_OPERATION, {
      endpoint: "GET /api/assistant/operations/{id}",
    });
  }

  async listAssistantOperations(sessionId: string): Promise<AssistantOperation[]> {
    const raw = await this.fetch<unknown>(`/api/assistant/sessions/${sessionId}/operations`);
    return parseWithFallback(raw, AssistantOperationListSchema, EMPTY_ASSISTANT_OPERATION_LIST, {
      endpoint: "GET /api/assistant/sessions/{id}/operations",
    });
  }

  /** Declines a pending operation. Specified as 204 + a "cancelled" tool message. */
  async rejectAssistantOperation(operationId: string): Promise<AssistantOperationDecision> {
    const raw = await this.fetch<unknown>(
      `/api/assistant/operations/${operationId}/reject`,
      { method: "POST" },
    );
    return this.assistantOperationDecision(raw, "POST /api/assistant/operations/{id}/reject");
  }

  /** Flattens the `{operation, message}` confirm body; an empty 204 body (the
   *  reject contract) is a success, not drift, so it skips the schema warning. */
  private assistantOperationDecision(raw: unknown, endpoint: string): AssistantOperationDecision {
    if (raw === undefined || raw === null) return EMPTY_ASSISTANT_OPERATION_DECISION;
    const parsed = parseWithFallback(
      raw,
      AssistantOperationDecisionSchema,
      { status: "", items: [], operation: { id: "", status: "" }, message: { id: "" } },
      { endpoint },
    );
    return {
      // A plan confirm reports its status at the top level; a single confirm
      // nests it under `operation`. Prefer the nested one so nothing about
      // the existing shape changes.
      status: parsed.operation.status || parsed.status,
      operation_id: parsed.operation.id,
      message_id: parsed.message.id,
      // Omitted entirely when there are no per-item outcomes, which keeps the
      // single-operation decision object exactly the shape it has always had.
      ...(parsed.items.length > 0 ? { items: parsed.items } : {}),
    };
  }

  // --- Pinned reports ---------------------------------------------------
  // docs/assistant-domain-plan.md Phase 2a. Pinning publishes an owner-scoped
  // artifact to a project so the whole workspace can read its current body.

  /**
   * Pins an artifact to a project. Owner-only server-side.
   *
   * Idempotent by contract: pinning an artifact that is already pinned to the
   * same project returns the EXISTING pin (200) instead of erroring, which is
   * what lets the UI offer a single "Pin" affordance without first having to
   * ask whether a pin exists (there is no read-pins-for-artifact endpoint in
   * 2a). The response is normalized because the identifier arrives as `id`
   * here but as `pin_id` on the list endpoint.
   */
  async pinArtifact(artifactId: string, projectId: string): Promise<ReportPin> {
    const raw = await this.fetch<unknown>(`/api/assistant/artifacts/${artifactId}/pins`, {
      method: "POST",
      body: JSON.stringify({ project_id: projectId }),
    });
    // The fallback carries both spellings so the normalization below is
    // total; an unreadable body yields EMPTY_REPORT_PIN (id "") and the UI
    // treats the pin as "created, but we can't address it" — no Unpin offer.
    const parsed = parseWithFallback(
      raw,
      ReportPinSchema,
      { id: "", pin_id: "", artifact_id: "", project_id: "", created_at: "" },
      { endpoint: "POST /api/assistant/artifacts/{id}/pins" },
    );
    const id = parsed.id || parsed.pin_id;
    if (!id) return EMPTY_REPORT_PIN;
    return {
      id,
      artifact_id: parsed.artifact_id || artifactId,
      project_id: parsed.project_id || projectId,
      created_at: parsed.created_at,
    };
  }

  /** Removes a pin. 204; owner or a workspace admin/owner. */
  async unpinArtifact(artifactId: string, pinId: string): Promise<void> {
    await this.fetch(`/api/assistant/artifacts/${artifactId}/pins/${pinId}`, {
      method: "DELETE",
    });
  }

  /** Reports pinned to a project — metadata only, no bodies. */
  async listProjectReports(projectId: string): Promise<PinnedReportSummary[]> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/reports`);
    const parsed = parseWithFallback(
      raw,
      ProjectReportsResponseSchema,
      { reports: EMPTY_PINNED_REPORT_LIST },
      { endpoint: "GET /api/projects/{id}/reports" },
    );
    return parsed.reports;
  }

  /** One pinned report with its CURRENT body — members of the workspace. */
  async getReport(pinId: string): Promise<PinnedReport> {
    const raw = await this.fetch<unknown>(`/api/reports/${pinId}`);
    return parseWithFallback(raw, PinnedReportSchema, EMPTY_PINNED_REPORT, {
      endpoint: "GET /api/reports/{pinId}",
    });
  }

  /**
   * Creates or replaces the pin's refresh schedule (Phase 2b). Owner-only and
   * human-actor-gated server-side: a standing schedule is standing spend.
   *
   * `weekday` only travels for the weekly preset — the caller composes the
   * body, so daily/weekdays never send a day the server would have to ignore.
   */
  async setReportSchedule(
    artifactId: string,
    pinId: string,
    body: ReportScheduleInput,
  ): Promise<ReportSchedule | null> {
    const raw = await this.fetch<unknown>(
      `/api/assistant/artifacts/${artifactId}/pins/${pinId}/schedule`,
      { method: "PUT", body: JSON.stringify(body) },
    );
    const parsed = parseWithFallback(
      raw,
      ReportScheduleResponseSchema,
      EMPTY_REPORT_SCHEDULE_RESPONSE,
      { endpoint: "PUT /api/assistant/artifacts/{id}/pins/{pinId}/schedule" },
    );
    // null here means "saved, but this build couldn't read the echo" — the
    // authoritative cadence comes back with the invalidated reports query.
    return parsed.schedule ?? null;
  }

  /** Removes the pin's schedule. 204; owner-only. */
  async deleteReportSchedule(artifactId: string, pinId: string): Promise<void> {
    await this.fetch(`/api/assistant/artifacts/${artifactId}/pins/${pinId}/schedule`, {
      method: "DELETE",
    });
  }

  async cancelTaskById(taskId: string): Promise<CancelTaskResponse> {
    const raw = await this.fetch<unknown>(`/api/tasks/${taskId}/cancel`, { method: "POST" });
    return parseWithFallback(raw, CancelTaskResponseSchema, EMPTY_CANCEL_TASK_RESPONSE, {
      endpoint: "POST /api/tasks/{taskId}/cancel",
    });
  }

  async listAttachments(issueId: string): Promise<Attachment[]> {
    return this.fetch(`/api/issues/${issueId}/attachments`);
  }

  // Fetches a fresh attachment metadata record. The server re-signs
  // `download_url` on every call (30 min expiry), so the click-time
  // download flow uses this endpoint to avoid handing the user a stale
  // signed URL cached in TanStack Query.
  async getAttachment(id: string): Promise<Attachment> {
    const raw = await this.fetch<unknown>(`/api/attachments/${id}`);
    return parseWithFallback(raw, AttachmentResponseSchema, EMPTY_ATTACHMENT, {
      endpoint: "GET /api/attachments/{id}",
    });
  }

  async deleteAttachment(id: string): Promise<void> {
    await this.fetch(`/api/attachments/${id}`, { method: "DELETE" });
  }

  // Fetches the raw bytes of a text-previewable attachment.
  //
  // The endpoint sidesteps CloudFront CORS (not configured on the CDN) and
  // bypasses Content-Disposition: attachment for the `text/*` family, both
  // of which would otherwise prevent the renderer from getting the body.
  // The server always replies with `text/plain; charset=utf-8` for safety;
  // the original MIME ships back in the `X-Original-Content-Type` header so
  // the preview dispatcher can choose between markdown / html / plain code.
  //
  // Routes through `fetchRaw` so it inherits the standard auth headers,
  // 401 → handleUnauthorized recovery, request-id logging, and ApiError
  // shape. 413 / 415 are translated to typed `Preview*Error` instances so
  // the modal can render specific fallbacks instead of generic failure.
  async getAttachmentTextContent(
    id: string,
  ): Promise<{ text: string; originalContentType: string }> {
    let res: Response;
    try {
      res = await this.fetchRaw(`/api/attachments/${id}/content`);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 413) throw new PreviewTooLargeError();
        if (err.status === 415) throw new PreviewUnsupportedError();
      }
      throw err;
    }
    return {
      text: await res.text(),
      originalContentType: res.headers.get("X-Original-Content-Type") ?? "",
    };
  }

  // Fetches attachment bytes through the authenticated download endpoint.
  //
  // Used by token-mode clients (Electron desktop, mobile webview) that cannot
  // attach an Authorization header to a bare <img>/<video> resource load.
  // Web cookie-auth clients load `/api/attachments/{id}/download` natively
  // via the same-origin proxy and do not need this path.
  //
  // Intended for proxy-mode deployments where the endpoint streams the
  // object (HTTP 200). CloudFront / presign modes should prefer the signed
  // `download_url` from metadata instead — following their 302 in a CORS
  // fetch would fail because the CDN does not expose CORS headers.
  async getAttachmentDownloadBlob(id: string): Promise<Blob> {
    const res = await this.fetchRaw(`/api/attachments/${id}/download`);
    return res.blob();
  }

  // Projects
  async listProjects(params?: { status?: string }): Promise<ListProjectsResponse> {
    const search = new URLSearchParams();
    if (params?.status) search.set("status", params.status);
    return this.fetch(`/api/projects?${search}`);
  }

  async getProject(id: string): Promise<Project> {
    return this.fetch(`/api/projects/${id}`);
  }

  async createProject(data: CreateProjectRequest): Promise<Project> {
    return this.fetch("/api/projects", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateProject(id: string, data: UpdateProjectRequest): Promise<Project> {
    return this.fetch(`/api/projects/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // Per-project pipeline config — the ProjectScoped QA / review / automation
  // flags this project overrides for its own issues. GET lists them with the
  // effective value + source; PUT/DELETE set/clear one override (owner/admin).
  async getProjectConfig(id: string): Promise<ProjectConfigEntry[]> {
    const raw = await this.fetch<unknown>(`/api/projects/${id}/config`);
    return parseWithFallback(raw, ProjectConfigListSchema, EMPTY_PROJECT_CONFIG, {
      endpoint: "GET /api/projects/{id}/config",
    }).configs;
  }

  async setProjectConfig(id: string, key: string, value: string): Promise<void> {
    await this.fetch(`/api/projects/${id}/config/${encodeURIComponent(key)}`, {
      method: "PUT",
      body: JSON.stringify({ value }),
    });
  }

  async resetProjectConfig(id: string, key: string): Promise<void> {
    await this.fetch(`/api/projects/${id}/config/${encodeURIComponent(key)}`, {
      method: "DELETE",
    });
  }

  // Per-project risk map — which path globs sit in which risk tier
  // (docs/orchestration-upgrade-plan.md §A1). The tier a change lands in
  // gates how deep QA goes, whether a human must sign off, and whether the
  // change may auto-merge; until this endpoint existed the map was read-only
  // and the tier was self-reported by the agent.
  //
  // A project with no risk map answers with an empty map, which is the same
  // thing the fallback renders — an unconfigured project and an endpoint an
  // older server does not serve look identical, and both are safe to edit.
  async getProjectRiskMap(id: string): Promise<RiskMapResponse> {
    const raw = await this.fetch<unknown>(`/api/projects/${id}/risk-map`);
    return parseWithFallback(raw, RiskMapResponseSchema, EMPTY_RISK_MAP, {
      endpoint: "GET /api/projects/{id}/risk-map",
    });
  }

  // Whole-list write (replace-on-write). The response is parsed through the
  // same schema so the caller settles on what the SERVER stored — it may
  // normalise globs, reorder entries, or keep a tier this client never sent
  // — rather than on the body we posted.
  async updateProjectRiskMap(id: string, entries: RiskMapEntry[]): Promise<RiskMapResponse> {
    const raw = await this.fetch<unknown>(`/api/projects/${id}/risk-map`, {
      method: "PUT",
      body: JSON.stringify({ risk_map: entries }),
    });
    return parseWithFallback(raw, RiskMapResponseSchema, EMPTY_RISK_MAP, {
      endpoint: "PUT /api/projects/{id}/risk-map",
    });
  }

  // Per-developer standing dev servers ("preview per project → per user") —
  // each member's own deployed box for this project. Read = any member;
  // writes are SELF-only (the /me endpoints).
  async listProjectDevServers(id: string): Promise<UserDevServerEntry[]> {
    const raw = await this.fetch<unknown>(`/api/projects/${id}/dev-servers`);
    return parseWithFallback(raw, ProjectDevServersSchema, EMPTY_PROJECT_DEV_SERVERS, {
      endpoint: "GET /api/projects/{id}/dev-servers",
    }).dev_servers;
  }

  async setMyProjectDevServer(id: string, baseUrl: string): Promise<void> {
    await this.fetch(`/api/projects/${id}/dev-servers/me`, {
      method: "PUT",
      body: JSON.stringify({ base_url: baseUrl }),
    });
  }

  async deleteMyProjectDevServer(id: string): Promise<void> {
    await this.fetch(`/api/projects/${id}/dev-servers/me`, {
      method: "DELETE",
    });
  }

  async deleteProject(id: string): Promise<void> {
    await this.fetch(`/api/projects/${id}`, { method: "DELETE" });
  }

  // Triggers the lead agent to study the project's connected repos and write
  // the per-project `<slug>-kb` knowledge skill. 202 = queued.
  async buildProjectKnowledge(id: string): Promise<{ status: string }> {
    return this.fetch(`/api/projects/${id}/knowledge/build`, {
      method: "POST",
    });
  }

  // Project resources
  async listProjectResources(
    projectId: string,
  ): Promise<ListProjectResourcesResponse> {
    return this.fetch(`/api/projects/${projectId}/resources`);
  }

  async createProjectResource(
    projectId: string,
    data: CreateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch(`/api/projects/${projectId}/resources`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateProjectResource(
    projectId: string,
    resourceId: string,
    data: UpdateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch(`/api/projects/${projectId}/resources/${resourceId}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteProjectResource(
    projectId: string,
    resourceId: string,
  ): Promise<void> {
    await this.fetch(`/api/projects/${projectId}/resources/${resourceId}`, {
      method: "DELETE",
    });
  }

  // Sprints
  // Workspace scoping rides the `workspace_id` query param on the list call
  // (mirrors listIssues); the rest are id-scoped and resolve the workspace
  // server-side.
  async listProjectSprints(
    projectId: string,
    params?: { workspace_id?: string },
  ): Promise<ListSprintsResponse> {
    const search = new URLSearchParams();
    if (params?.workspace_id) search.set("workspace_id", params.workspace_id);
    return this.fetch(`/api/projects/${projectId}/sprints?${search}`);
  }

  async createSprint(
    projectId: string,
    data: CreateSprintRequest,
  ): Promise<Sprint> {
    return this.fetch(`/api/projects/${projectId}/sprints`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async getSprint(id: string): Promise<Sprint> {
    return this.fetch(`/api/sprints/${id}`);
  }

  async updateSprint(id: string, data: UpdateSprintRequest): Promise<Sprint> {
    return this.fetch(`/api/sprints/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteSprint(id: string): Promise<void> {
    await this.fetch(`/api/sprints/${id}`, { method: "DELETE" });
  }

  async listSprintIssues(id: string): Promise<ListSprintIssuesResponse> {
    return this.fetch(`/api/sprints/${id}/issues`);
  }

  // Assign / clear an issue's sprint. Mirrors attachLabel/detachLabel — the
  // issue is the resource being mutated, so these live on the issue path.
  // PUT echoes the assigned sprint (`{sprint}`); DELETE is 204.
  async assignIssueSprint(issueId: string, sprintId: string): Promise<Sprint> {
    const res = await this.fetch<{ sprint: Sprint }>(
      `/api/issues/${issueId}/sprint`,
      {
        method: "PUT",
        body: JSON.stringify({ sprint_id: sprintId }),
      },
    );
    return res.sprint;
  }

  async clearIssueSprint(issueId: string): Promise<void> {
    await this.fetch(`/api/issues/${issueId}/sprint`, { method: "DELETE" });
  }

  // Bulk "move to sprint": set the sprint for many issues at once. Issues in a
  // different project than the sprint are skipped server-side (sprints are
  // project-scoped); `moved` reports how many actually changed.
  async batchSetIssueSprint(
    issueIds: string[],
    sprintId: string,
  ): Promise<{ moved: number }> {
    return this.fetch(`/api/issues/batch-sprint`, {
      method: "POST",
      body: JSON.stringify({ issue_ids: issueIds, sprint_id: sprintId }),
    });
  }

  // Every non-completed sprint across the workspace's projects, for the bulk
  // move-to-sprint picker (grouped client-side by project_title).
  async listWorkspaceSprints(): Promise<WorkspaceSprint[]> {
    const res = await this.fetch<{ sprints: WorkspaceSprint[] }>(`/api/sprints`);
    return res.sprints ?? [];
  }

  // Fetch the issue's current sprint directly (server returns `{sprint}` or
  // `{sprint: null}`), avoiding a scan of every sprint's issue list.
  async getIssueSprint(issueId: string): Promise<Sprint | null> {
    const res = await this.fetch<{ sprint: Sprint | null }>(
      `/api/issues/${issueId}/sprint`,
    );
    return res.sprint;
  }

  // Labels
  async listLabels(): Promise<ListLabelsResponse> {
    return this.fetch(`/api/labels`);
  }

  async getLabel(id: string): Promise<Label> {
    return this.fetch(`/api/labels/${id}`);
  }

  async createLabel(data: CreateLabelRequest): Promise<Label> {
    return this.fetch(`/api/labels`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  // --- Bitrix import browser -------------------------------------------------
  // Workspace scoping rides the X-Workspace-Slug header (added by fetchRaw), so
  // these take no explicit workspace param.
  async listBitrixGroups(): Promise<BitrixGroup[]> {
    return this.fetch(`/api/bitrix/groups`);
  }

  async listBitrixUsers(): Promise<BitrixUser[]> {
    return this.fetch(`/api/bitrix/users`);
  }

  async getBitrixImportProgress(): Promise<BitrixImportProgress> {
    return this.fetch(`/api/bitrix/import/progress`);
  }

  /** Stop the in-flight import. Tasks already imported are kept; this halts the
   * remaining queue. Returns cancelled=false when no run was live. */
  async cancelBitrixImport(): Promise<BitrixImportCancelResponse> {
    return this.fetch(`/api/bitrix/import/cancel`, { method: "POST" });
  }

  async listBitrixTasks(groupId: string): Promise<BitrixTask[]> {
    return this.fetch(
      `/api/bitrix/tasks?group_id=${encodeURIComponent(groupId)}`,
    );
  }

  async importBitrixTasks(
    req: BitrixImportRequest,
  ): Promise<BitrixImportResponse> {
    const raw = await this.fetch<unknown>(`/api/bitrix/import`, {
      method: "POST",
      body: JSON.stringify(req),
    });
    return parseWithFallback(raw, BitrixImportResponseSchema, EMPTY_BITRIX_IMPORT_RESPONSE, {
      endpoint: "POST /api/bitrix/import",
    });
  }

  /** Import only tasks assigned to the caller's linked Bitrix identity. */
  async importMyBitrixTasks(): Promise<BitrixImportResponse> {
    const raw = await this.fetch<unknown>(`/api/bitrix/import/mine`, {
      method: "POST",
    });
    return parseWithFallback(raw, BitrixImportResponseSchema, EMPTY_BITRIX_IMPORT_RESPONSE, {
      endpoint: "POST /api/bitrix/import/mine",
    });
  }

  // Re-sync a single Bitrix-linked project on demand (pulls new + changed tasks;
  // stamps project.settings.bitrix_synced_at). Async on the server (202); the
  // refreshed project carries the new last-sync timestamp.
  async syncBitrixProject(projectId: string): Promise<BitrixSyncResult> {
    return this.fetch(`/api/projects/${projectId}/bitrix/sync`, {
      method: "POST",
    });
  }

  // --- Zoho import browser (Projects + Sprints channels) ---
  async listZohoProjects(): Promise<ZohoProject[]> {
    return this.fetch(`/api/zoho-projects/projects`);
  }

  async importZohoProjects(req: ZohoImportRequest): Promise<ZohoImportResponse> {
    return this.fetch(`/api/zoho-projects/import`, {
      method: "POST",
      body: JSON.stringify(req),
    });
  }

  async listZohoSprintsProjects(): Promise<ZohoSprintsProject[]> {
    return this.fetch(`/api/zoho-sprints/projects`);
  }

  async importZohoSprintsProjects(
    req: ZohoSprintsImportRequest,
  ): Promise<ZohoSprintsImportResponse> {
    return this.fetch(`/api/zoho-sprints/import`, {
      method: "POST",
      body: JSON.stringify(req),
    });
  }

  // --- Dynamic Zoho integration (docs/zoho-dynamic-integration.md) ---
  // Workspace connection (sealed OAuth credentials, owner/admin writes),
  // CRM module/field discovery and per-module sync configs. Status responses
  // never carry secrets. The personal Zoho account lives under /api/me/zoho.

  async getZohoConnection(workspaceId: string): Promise<ZohoConnectionStatus> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/zoho-connection`);
    return parseWithFallback(raw, ZohoConnectionStatusSchema, EMPTY_ZOHO_CONNECTION_STATUS, {
      endpoint: "GET /api/workspaces/{id}/zoho-connection",
    });
  }

  async putZohoConnection(
    workspaceId: string,
    data: PutZohoConnectionRequest,
  ): Promise<ZohoConnectionStatus> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/zoho-connection`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, ZohoConnectionStatusSchema, EMPTY_ZOHO_CONNECTION_STATUS, {
      endpoint: "PUT /api/workspaces/{id}/zoho-connection",
    });
  }

  async deleteZohoConnection(workspaceId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/zoho-connection`, {
      method: "DELETE",
    });
  }

  async listZohoCRMModules(workspaceId: string): Promise<ZohoCRMModule[]> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/zoho/crm/modules`);
    const parsed = parseWithFallback(raw, ZohoCRMModulesResponseSchema, EMPTY_ZOHO_CRM_MODULES, {
      endpoint: "GET /api/workspaces/{id}/zoho/crm/modules",
    });
    return parsed.modules;
  }

  async listZohoCRMFields(
    workspaceId: string,
    module: string,
  ): Promise<ZohoCRMFieldsResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/zoho/crm/fields?module=${encodeURIComponent(module)}`,
    );
    return parseWithFallback(raw, ZohoCRMFieldsResponseSchema, EMPTY_ZOHO_CRM_FIELDS, {
      endpoint: "GET /api/workspaces/{id}/zoho/crm/fields",
    });
  }

  async listZohoSyncConfigs(workspaceId: string): Promise<ZohoSyncConfig[]> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/zoho/sync-configs`);
    const parsed = parseWithFallback(raw, ZohoSyncConfigsResponseSchema, EMPTY_ZOHO_SYNC_CONFIGS, {
      endpoint: "GET /api/workspaces/{id}/zoho/sync-configs",
    });
    return parsed.configs;
  }

  async createZohoSyncConfig(
    workspaceId: string,
    req: CreateZohoSyncConfigRequest,
  ): Promise<ZohoSyncConfig> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/zoho/sync-configs`, {
      method: "POST",
      body: JSON.stringify(req),
    });
    return parseWithFallback(raw, ZohoSyncConfigSchema, EMPTY_ZOHO_SYNC_CONFIG, {
      endpoint: "POST /api/workspaces/{id}/zoho/sync-configs",
    });
  }

  async updateZohoSyncConfig(
    workspaceId: string,
    configId: string,
    req: UpdateZohoSyncConfigRequest,
  ): Promise<ZohoSyncConfig> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/zoho/sync-configs/${configId}`,
      {
        method: "PUT",
        body: JSON.stringify(req),
      },
    );
    return parseWithFallback(raw, ZohoSyncConfigSchema, EMPTY_ZOHO_SYNC_CONFIG, {
      endpoint: "PUT /api/workspaces/{id}/zoho/sync-configs/{configId}",
    });
  }

  async deleteZohoSyncConfig(workspaceId: string, configId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/zoho/sync-configs/${configId}`, {
      method: "DELETE",
    });
  }

  // --- Personal Zoho account ---
  // The account is the person's own and works in every workspace, but the
  // sign-in client comes from a workspace's Zoho connector, so reads and
  // connects name the workspace.

  async getMyZohoAccount(workspaceId: string): Promise<ZohoAccount> {
    const raw = await this.fetch<unknown>(
      `/api/me/zoho?workspace_id=${encodeURIComponent(workspaceId)}`,
    );
    return parseWithFallback(raw, ZohoAccountSchema, EMPTY_ZOHO_ACCOUNT, {
      endpoint: "GET /api/me/zoho",
    });
  }

  /** Returns the Zoho sign-in page to open. Throws when the server answers
   * without a usable http(s) url, so the caller never opens "undefined". */
  async connectZohoAccount(workspaceId: string): Promise<ZohoConnectResponse> {
    const raw = await this.fetch<unknown>("/api/me/zoho/connect", {
      method: "POST",
      body: JSON.stringify({ workspace_id: workspaceId }),
    });
    const { url } = parseWithFallback(
      raw,
      ZohoConnectResponseSchema,
      EMPTY_ZOHO_CONNECT_RESPONSE,
      { endpoint: "POST /api/me/zoho/connect" },
    );
    if (!/^https?:\/\//i.test(url.trim())) {
      throw new Error("Zoho connect response did not include a sign-in url");
    }
    return { url: url.trim() };
  }

  async disconnectZohoAccount(): Promise<void> {
    await this.fetch("/api/me/zoho", { method: "DELETE" });
  }

  // Policy Agent — the workspace's agent-fleet speed + health (per-agent run
  // duration / queue wait, stalled / failed / looping tasks).
  async getPolicyFleetHealth(): Promise<PolicyFleetHealth> {
    const raw = await this.fetch<unknown>("/api/policy/fleet-health");
    return parseWithFallback(raw, PolicyFleetHealthSchema, EMPTY_POLICY_FLEET_HEALTH, {
      endpoint: "GET /api/policy/fleet-health",
    });
  }

  // Resolves where the Live-testing bay reaches a CDP browser for the issue:
  // self-host (daemon_url, dialed directly) or cloud (browser_url — a
  // same-origin reverse-proxy base). Never requires a worktree; 404 (issue
  // gone) degrades to the empty fallback so consumers just check `mode`.
  async getIssueBrowser(issueId: string): Promise<IssueBrowserResponse> {
    try {
      const raw = await this.fetch<unknown>(`/api/issues/${issueId}/browser`);
      return parseWithFallback(raw, IssueBrowserResponseSchema, EMPTY_ISSUE_BROWSER, {
        endpoint: "GET /api/issues/:id/browser",
      });
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return EMPTY_ISSUE_BROWSER;
      throw e;
    }
  }

  // Resolves where the web folder picker walks one daemon's filesystem. Keyed
  // by daemon_id (the machine) rather than an issue: attaching a local folder
  // to a project happens with no issue in hand. The backend gates this on the
  // caller owning that daemon's runtime (or being a workspace admin). A
  // registered-but-stopped machine returns mode="offline" with a blank
  // daemon_url; 404 degrades to the empty fallback so consumers check `mode`.
  async getDaemonBrowseTarget(daemonId: string): Promise<DaemonBrowseTarget> {
    try {
      const raw = await this.fetch<unknown>(
        `/api/runtimes/by-daemon/${encodeURIComponent(daemonId)}/browse`,
      );
      return parseWithFallback(raw, DaemonBrowseTargetSchema, EMPTY_DAEMON_BROWSE_TARGET, {
        endpoint: "GET /api/runtimes/by-daemon/:daemonId/browse",
      });
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return EMPTY_DAEMON_BROWSE_TARGET;
      throw e;
    }
  }

  // Resolves the issue's deployed QA target (a connected box or the project's
  // qa_smoke_url) — the Live testing bay's fallback for workspaces without a
  // per-issue daemon worktree. Always 200 with url: "" when nothing resolves,
  // so a QA-page consumer just checks `url` rather than try/catching.
  async getIssueQAPreviewURL(issueId: string): Promise<IssueQAPreviewURLResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/qa-preview-url`);
    return parseWithFallback(raw, IssueQAPreviewURLResponseSchema, EMPTY_ISSUE_QA_PREVIEW_URL, {
      endpoint: "GET /api/issues/:id/qa-preview-url",
    });
  }

  // QA speed / regression metrics for the workspace (the QA Metrics page):
  // run totals + daily trend + per-QA-agent durations + script coverage.
  // projectId scopes metrics to one project (the cockpit project selector);
  // omit for the workspace-wide "all projects" view.
  async getQAMetrics(projectId?: string): Promise<QAMetricsResponse> {
    const qs = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
    const raw = await this.fetch<unknown>(`/api/qa/metrics${qs}`);
    return parseWithFallback(raw, QAMetricsResponseSchema, EMPTY_QA_METRICS, {
      endpoint: "GET /api/qa/metrics",
    });
  }

  // Freshest QA verdict per in_review issue (reason + provenance + age for the
  // cockpit rows), keyed by issue id. projectId scopes like the cockpit.
  async listQAVerdicts(projectId?: string): Promise<QAVerdictsResponse> {
    const qs = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
    const raw = await this.fetch<unknown>(`/api/qa/verdicts${qs}`);
    return parseWithFallback(raw, QAVerdictsResponseSchema, EMPTY_QA_VERDICTS, {
      endpoint: "GET /api/qa/verdicts",
    });
  }

  // Per-active-sprint QA readiness (the QA cockpit Sprint tab): each sprint's
  // issue rows by verdict + a mergeable rollup. projectId scopes to one project.
  async getSprintReadiness(projectId?: string): Promise<SprintReadinessResponse> {
    const qs = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
    const raw = await this.fetch<unknown>(`/api/qa/sprint-readiness${qs}`);
    return parseWithFallback(raw, SprintReadinessResponseSchema, EMPTY_SPRINT_READINESS, {
      endpoint: "GET /api/qa/sprint-readiness",
    });
  }

  // Fire the whole-branch regression for a sprint (the Sprint tab's action).
  // The response (an autopilot-run record) carries nothing the UI renders.
  async runSprintRegression(sprintId: string): Promise<void> {
    await this.fetch(`/api/sprints/${sprintId}/run-regression`, { method: "POST" });
  }

  // Fires the SAME whole-branch regression (scope=regression vs sprint-root)
  // the sprint-end scheduler runs automatically — manually, from wherever a
  // human is already looking at one of the sprint's issues. The response body
  // (an autopilot-run record) carries nothing the UI renders — only success
  // vs failure matters here — so it's intentionally left unparsed.
  async runIssueSprintRegression(issueId: string): Promise<void> {
    await this.fetch(`/api/issues/${issueId}/run-regression`, { method: "POST" });
  }

  // The QA section's evidence-first read: one indexed row, or null when no
  // run_qa verdict has been captured yet (the section then prompts a re-run).
  async getQAEvidence(issueId: string): Promise<QAEvidence | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/qa-evidence`);
    return parseWithFallback(raw, QAEvidenceSchema.nullable(), null, {
      endpoint: "GET /api/issues/:id/qa-evidence",
    });
  }

  // Human QA override with provenance: flips the qa:pass/qa:fail label AND
  // replaces the current evidence row with a human-sourced one (the reason
  // becomes the summary; the actor is stamped into result_json.override) plus
  // a timeline comment — one attributed decision instead of two bare label
  // calls. Returns the fresh evidence row (with reconciled_state) so callers
  // can update the cache; a malformed body degrades to null (the caller's
  // invalidations then refetch).
  async overrideQAVerdict(
    issueId: string,
    body: { verdict: "pass" | "fail"; reason?: string },
  ): Promise<QAEvidence | null> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/qa-override`, {
      method: "POST",
      body: JSON.stringify({ verdict: body.verdict, ...(body.reason ? { reason: body.reason } : {}) }),
    });
    return parseWithFallback(raw, QAEvidenceSchema.nullable(), null, {
      endpoint: "POST /api/issues/:id/qa-override",
    });
  }

  // The Deploy lens / stepper's evidence-first read: the freshest Tier-1
  // (QA-box git-sync) deploy for an issue plus a short recent history.
  // Empty (latest: null, recent: []) is a normal response for a never-
  // deployed issue, not an error.
  async getIssueDeployEvents(issueId: string): Promise<IssueDeployEventsResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/deploy-events`);
    return parseWithFallback(raw, IssueDeployEventsResponseSchema, EMPTY_DEPLOY_EVENTS, {
      endpoint: "GET /api/issues/:id/deploy-events",
    });
  }

  // QA test cases — the QA team's test-management instruments.
  async getIssueTestCases(issueId: string): Promise<ListTestCasesResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/test-cases`);
    return parseWithFallback(raw, ListTestCasesResponseSchema, EMPTY_LIST_TEST_CASES, {
      endpoint: "GET /api/issues/:id/test-cases",
    });
  }

  async createIssueTestCase(issueId: string, data: CreateTestCaseRequest): Promise<TestCase> {
    return this.fetch(`/api/issues/${issueId}/test-cases`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  // A project's STANDING base regression suite — test cases with issue_id NULL,
  // injected into every run_qa / run_test_cases on the project's issues (the
  // "stoppage" release gate). Same response shape as the issue-scoped list; base
  // rows carry issue_id "". Falls back to an empty list so the Suite tab renders.
  async listProjectTestCases(projectId: string): Promise<ListTestCasesResponse> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/test-cases`);
    return parseWithFallback(raw, ListTestCasesResponseSchema, EMPTY_LIST_TEST_CASES, {
      endpoint: "GET /api/projects/:id/test-cases",
    });
  }

  // Author a standing base case for a project (issue_id stays NULL). Kind
  // defaults to "automated" server-side — only automated base cases are injected
  // into runs. Returns the created row; a degraded response yields an empty case.
  async createProjectTestCase(projectId: string, data: CreateTestCaseRequest): Promise<TestCase> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/test-cases`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, TestCaseSchema, EMPTY_TEST_CASE, {
      endpoint: "POST /api/projects/:id/test-cases",
    });
  }

  // Fire the QA-squad lead to author the project's golden-path base suite from
  // its QA manifest (202 Accepted → the tracking issue it opened). The UI only
  // needs the queued status; a degraded response yields empty status/issue_id.
  async buildProjectBaseSuite(projectId: string): Promise<BuildBaseSuiteResponse> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/base-suite/build`, {
      method: "POST",
    });
    return parseWithFallback(raw, BuildBaseSuiteResponseSchema, EMPTY_BUILD_BASE_SUITE, {
      endpoint: "POST /api/projects/:id/base-suite/build",
    });
  }

  async recordTestCaseRun(caseId: string, data: CreateTestRunRequest): Promise<unknown> {
    return this.fetch(`/api/test-cases/${caseId}/runs`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  // A case's recent run history with run identity (sha/session/timing) —
  // the QA panel's last-5 strip. Defensive: a malformed body degrades to an
  // empty history, never a crash (old servers 404 → the caller's query error
  // state simply shows no strip).
  async listTestCaseRuns(caseId: string): Promise<TestCaseRunsParsed> {
    const raw = await this.fetch<unknown>(`/api/test-cases/${caseId}/runs`);
    return parseWithFallback(raw, TestCaseRunsResponseSchema, EMPTY_TEST_CASE_RUNS, {
      endpoint: "GET /api/test-cases/:id/runs",
    });
  }

  // Edit a test case (title/steps/expected/kind/category/script). Only the
  // provided fields change. Used by the QA cockpit Suite tab so an engineer can
  // fix a wrong/flaky golden-path case in place.
  async updateTestCase(caseId: string, data: UpdateTestCaseRequest): Promise<TestCase> {
    const raw = await this.fetch<unknown>(`/api/test-cases/${caseId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, TestCaseSchema, EMPTY_TEST_CASE, {
      endpoint: "PATCH /api/test-cases/:id",
    });
  }

  async archiveTestCase(caseId: string): Promise<void> {
    await this.fetch(`/api/test-cases/${caseId}/archive`, { method: "POST" });
  }

  // Fire a QA-Squad agent to author test cases for the issue (gen_test_cases).
  async generateTestCases(issueId: string): Promise<unknown> {
    return this.sliceAction(issueId, { kind: "gen_test_cases" });
  }

  // Fire a QA-Squad agent to RUN the issue's automated cases on the box
  // (run_test_cases) — results land as test_run rows via the capture path.
  async runTestCases(issueId: string): Promise<unknown> {
    return this.sliceAction(issueId, { kind: "run_test_cases" });
  }

  // Launch the Playwright trace viewer for one test run and get a same-origin
  // URL to iframe. The backend spawns `playwright show-trace` on the daemon that
  // holds the trace and reverse-proxies it. Returns an empty trace_url on a
  // degraded response so the caller can just check `trace_url` before opening.
  async launchTrace(testRunId: string): Promise<{ trace_url: string }> {
    const raw = await this.fetch<unknown>(`/api/qa/trace/${testRunId}`);
    return parseWithFallback(raw, LaunchTraceResponseSchema, EMPTY_LAUNCH_TRACE, {
      endpoint: "GET /api/qa/trace/:runId",
    });
  }

  async updateLabel(id: string, data: UpdateLabelRequest): Promise<Label> {
    return this.fetch(`/api/labels/${id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteLabel(id: string): Promise<void> {
    await this.fetch(`/api/labels/${id}`, { method: "DELETE" });
  }

  async listLabelsForIssue(issueId: string): Promise<IssueLabelsResponse> {
    return this.fetch(`/api/issues/${issueId}/labels`);
  }

  async attachLabel(issueId: string, labelId: string): Promise<IssueLabelsResponse> {
    return this.fetch(`/api/issues/${issueId}/labels`, {
      method: "POST",
      body: JSON.stringify({ label_id: labelId }),
    });
  }

  async detachLabel(issueId: string, labelId: string): Promise<IssueLabelsResponse> {
    return this.fetch(`/api/issues/${issueId}/labels/${labelId}`, {
      method: "DELETE",
    });
  }

  // Workspace knowledge base (docs/workspace-knowledge-plan.md §5). Scoped
  // like labels by the ambient workspace header. Every response runs through a
  // lenient schema: statuses and fields are server-driven, and the page has to
  // keep rendering a list that a newer server extended.
  async listKnowledge(): Promise<KnowledgeListResponse> {
    return parseKnowledgeListResponse(await this.fetch<unknown>("/api/knowledge"));
  }

  async getKnowledgeDoc(id: string): Promise<KnowledgeDocDetail> {
    return parseKnowledgeDocDetailResponse(
      await this.fetch<unknown>(`/api/knowledge/${encodeURIComponent(id)}`),
    );
  }

  /** An uploaded attachment (`attachment_id`) or a Markdown note. 413 / 415
   *  carry the server's reason in the thrown ApiError's message. */
  async createKnowledgeDoc(req: CreateKnowledgeDocRequest): Promise<KnowledgeDoc> {
    return parseKnowledgeDocResponse(
      await this.fetch<unknown>("/api/knowledge", { method: "POST", body: JSON.stringify(req) }),
      "POST /api/knowledge",
    );
  }

  async updateKnowledgeDoc(id: string, req: UpdateKnowledgeDocRequest): Promise<KnowledgeDoc> {
    return parseKnowledgeDocResponse(
      await this.fetch<unknown>(`/api/knowledge/${encodeURIComponent(id)}`, {
        method: "PATCH",
        body: JSON.stringify(req),
      }),
      "PATCH /api/knowledge/{id}",
    );
  }

  async deleteKnowledgeDoc(id: string): Promise<void> {
    await this.fetch<void>(`/api/knowledge/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  async reprocessKnowledgeDoc(id: string): Promise<KnowledgeDoc> {
    return parseKnowledgeDocResponse(
      await this.fetch<unknown>(`/api/knowledge/${encodeURIComponent(id)}/reprocess`, {
        method: "POST",
      }),
      "POST /api/knowledge/{id}/reprocess",
    );
  }

  async searchKnowledge(query: string, limit = 10): Promise<KnowledgeSearchResponse> {
    const params = new URLSearchParams({ q: query, limit: String(limit) });
    return parseKnowledgeSearchResponse(
      await this.fetch<unknown>(`/api/knowledge/search?${params.toString()}`),
    );
  }

  // Pins
  async listPins(): Promise<PinnedItem[]> {
    return this.fetch("/api/pins");
  }

  async createPin(data: CreatePinRequest): Promise<PinnedItem> {
    return this.fetch("/api/pins", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async deletePin(itemType: PinnedItemType, itemId: string): Promise<void> {
    await this.fetch(`/api/pins/${itemType}/${itemId}`, { method: "DELETE" });
  }

  async reorderPins(data: ReorderPinsRequest): Promise<void> {
    await this.fetch("/api/pins/reorder", {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // Squads
  async listSquads(): Promise<Squad[]> {
    const raw = await this.fetch<unknown>(`/api/squads`);
    return parseWithFallback(raw, SquadListSchema, EMPTY_SQUAD_LIST, {
      endpoint: "GET /api/squads",
    }) as Squad[];
  }

  async getSquad(id: string): Promise<Squad> {
    const raw = await this.fetch<unknown>(`/api/squads/${id}`);
    return parseWithFallback(raw, SquadSchema, EMPTY_SQUAD, {
      endpoint: "GET /api/squads/:id",
    }) as Squad;
  }

  async createSquad(data: CreateSquadRequest): Promise<Squad> {
    const raw = await this.fetch<unknown>("/api/squads", { method: "POST", body: JSON.stringify(data) });
    return parseWithFallback(raw, SquadSchema, EMPTY_SQUAD, {
      endpoint: "POST /api/squads",
    }) as Squad;
  }

  async updateSquad(id: string, data: UpdateSquadRequest): Promise<Squad> {
    const raw = await this.fetch<unknown>(`/api/squads/${id}`, { method: "PUT", body: JSON.stringify(data) });
    return parseWithFallback(raw, SquadSchema, EMPTY_SQUAD, {
      endpoint: "PUT /api/squads/:id",
    }) as Squad;
  }

  async deleteSquad(id: string): Promise<void> {
    await this.fetch(`/api/squads/${id}`, { method: "DELETE" });
  }

  /**
   * Suggest a roster-aware orchestrator brief for a squad's Instructions field.
   * Server does not persist it — the caller fills the editable textarea with the
   * result. Trivial one-field response; defensively read the string and default
   * to "" so a drifted/empty body never throws into the UI.
   */
  async suggestSquadInstructions(id: string): Promise<string> {
    const raw = await this.fetch<unknown>(`/api/squads/${id}/suggest-instructions`, { method: "POST" });
    const value = (raw as { instructions?: unknown } | null)?.instructions;
    return typeof value === "string" ? value : "";
  }

  async listSquadMembers(squadId: string): Promise<SquadMember[]> {
    return this.fetch(`/api/squads/${squadId}/members`);
  }

  async addSquadMember(squadId: string, data: { member_type: string; member_id: string; role?: string }): Promise<SquadMember> {
    return this.fetch(`/api/squads/${squadId}/members`, { method: "POST", body: JSON.stringify(data) });
  }

  async removeSquadMember(squadId: string, data: { member_type: string; member_id: string }): Promise<void> {
    await this.fetch(`/api/squads/${squadId}/members`, { method: "DELETE", body: JSON.stringify(data) });
  }

  async updateSquadMemberRole(squadId: string, data: { member_type: string; member_id: string; role: string }): Promise<SquadMember> {
    return this.fetch(`/api/squads/${squadId}/members/role`, { method: "PATCH", body: JSON.stringify(data) });
  }

  // Per-squad members status snapshot: one row per member with derived
  // working/idle/offline/unstable plus the issues each agent is currently
  // running. Parsed with a lenient schema so a new server-side status
  // value or extra field can't white-screen the Squad page (#2143).
  async getSquadMemberStatus(squadId: string): Promise<SquadMemberStatusListResponse> {
    const raw = await this.fetch<unknown>(`/api/squads/${squadId}/members/status`);
    return parseWithFallback(raw, SquadMemberStatusListResponseSchema, EMPTY_SQUAD_MEMBER_STATUS_LIST, {
      endpoint: "GET /api/squads/:id/members/status",
    }) as SquadMemberStatusListResponse;
  }

  // Automations (task-management flow rules). Every response runs through a
  // lenient schema: the trigger/step names are server-driven, so an older client
  // must keep rendering a flow that mentions a node type it does not know.
  async listAutomations(): Promise<ListAutomationsResponse> {
    return parseAutomationsResponse(await this.fetch<unknown>("/api/automations"));
  }

  async getAutomation(id: string): Promise<Automation> {
    return parseAutomationResponse(await this.fetch<unknown>(`/api/automations/${id}`));
  }

  async createAutomation(data: AutomationWriteRequest): Promise<Automation> {
    return parseAutomationResponse(
      await this.fetch<unknown>("/api/automations", { method: "POST", body: JSON.stringify(data) }),
    );
  }

  async updateAutomation(id: string, data: AutomationWriteRequest): Promise<Automation> {
    return parseAutomationResponse(
      await this.fetch<unknown>(`/api/automations/${id}`, { method: "PATCH", body: JSON.stringify(data) }),
    );
  }

  async setAutomationEnabled(id: string, enabled: boolean): Promise<Automation> {
    return parseAutomationResponse(
      await this.fetch<unknown>(`/api/automations/${id}/enabled`, {
        method: "POST",
        body: JSON.stringify({ enabled }),
      }),
    );
  }

  async deleteAutomation(id: string): Promise<void> {
    await this.fetch<void>(`/api/automations/${id}`, { method: "DELETE" });
  }

  async listAutomationRuns(id: string, limit?: number): Promise<ListAutomationRunsResponse> {
    const search = new URLSearchParams();
    if (limit) search.set("limit", String(limit));
    return parseAutomationRunsResponse(
      await this.fetch<unknown>(`/api/automations/${id}/runs?${search}`),
    );
  }

  async rerunAutomationRun(id: string, runId: string): Promise<AutomationRun> {
    return parseAutomationRunResponse(
      await this.fetch<unknown>(`/api/automations/${id}/runs/${runId}/rerun`, { method: "POST" }),
    );
  }

  async getAutomationCatalog(): Promise<AutomationCatalog> {
    return parseAutomationCatalogResponse(await this.fetch<unknown>("/api/automations/catalog"));
  }

  async listAutomationRecipes(): Promise<ListAutomationRecipesResponse> {
    return parseAutomationRecipesResponse(await this.fetch<unknown>("/api/automations/recipes"));
  }

  async installAutomationRecipe(
    key: string,
    body?: { project_id?: string | null; enabled?: boolean },
  ): Promise<InstallAutomationRecipeResponse> {
    return parseInstallRecipeResponse(
      await this.fetch<unknown>(`/api/automations/recipes/${key}/install`, {
        method: "POST",
        body: JSON.stringify(body ?? {}),
      }),
    );
  }

  // Autopilots
  async listAutopilots(params?: { status?: string }): Promise<ListAutopilotsResponse> {
    const search = new URLSearchParams();
    if (params?.status) search.set("status", params.status);
    return this.fetch(`/api/autopilots?${search}`);
  }

  async getAutopilot(id: string): Promise<GetAutopilotResponse> {
    return this.fetch(`/api/autopilots/${id}`);
  }

  async createAutopilot(data: CreateAutopilotRequest): Promise<Autopilot> {
    return this.fetch("/api/autopilots", {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateAutopilot(id: string, data: UpdateAutopilotRequest): Promise<Autopilot> {
    return this.fetch(`/api/autopilots/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async deleteAutopilot(id: string): Promise<void> {
    await this.fetch(`/api/autopilots/${id}`, { method: "DELETE" });
  }

  async triggerAutopilot(id: string): Promise<AutopilotRun> {
    return this.fetch(`/api/autopilots/${id}/trigger`, { method: "POST" });
  }

  async listAutopilotRuns(id: string, params?: { limit?: number; offset?: number }): Promise<ListAutopilotRunsResponse> {
    const search = new URLSearchParams();
    if (params?.limit) search.set("limit", params.limit.toString());
    if (params?.offset) search.set("offset", params.offset.toString());
    return this.fetch(`/api/autopilots/${id}/runs?${search}`);
  }

  // Returns a single run including its full trigger_payload. List responses
  // omit trigger_payload to keep them small (a webhook envelope can be
  // up to 256 KiB × limit rows), so the detail view fetches via this route.
  async getAutopilotRun(autopilotId: string, runId: string): Promise<AutopilotRun> {
    return this.fetch(`/api/autopilots/${autopilotId}/runs/${runId}`);
  }

  async createAutopilotTrigger(autopilotId: string, data: CreateAutopilotTriggerRequest): Promise<AutopilotTrigger> {
    return this.fetch(`/api/autopilots/${autopilotId}/triggers`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async updateAutopilotTrigger(autopilotId: string, triggerId: string, data: UpdateAutopilotTriggerRequest): Promise<AutopilotTrigger> {
    return this.fetch(`/api/autopilots/${autopilotId}/triggers/${triggerId}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async deleteAutopilotTrigger(autopilotId: string, triggerId: string): Promise<void> {
    await this.fetch(`/api/autopilots/${autopilotId}/triggers/${triggerId}`, { method: "DELETE" });
  }

  async rotateAutopilotTriggerWebhookToken(
    autopilotId: string,
    triggerId: string,
  ): Promise<AutopilotTrigger> {
    return this.fetch(
      `/api/autopilots/${autopilotId}/triggers/${triggerId}/rotate-webhook-token`,
      { method: "POST" },
    );
  }

  // Webhook deliveries — list is slim (no raw_body / selected_headers /
  // response_body); detail returns the full row. Both responses are parsed
  // through a lenient schema so an unknown server-side `status` /
  // `signature_status` value degrades to a generic row instead of dropping
  // the whole list.
  async listAutopilotDeliveries(
    autopilotId: string,
    params?: { limit?: number; offset?: number },
  ): Promise<ListWebhookDeliveriesResponse> {
    const search = new URLSearchParams();
    if (params?.limit) search.set("limit", params.limit.toString());
    if (params?.offset) search.set("offset", params.offset.toString());
    const raw = await this.fetch<unknown>(
      `/api/autopilots/${autopilotId}/deliveries?${search}`,
    );
    return parseWithFallback(
      raw,
      ListWebhookDeliveriesResponseSchema,
      EMPTY_LIST_WEBHOOK_DELIVERIES_RESPONSE,
      { endpoint: "GET /api/autopilots/:id/deliveries" },
    );
  }

  async getAutopilotDelivery(
    autopilotId: string,
    deliveryId: string,
  ): Promise<WebhookDelivery> {
    const raw = await this.fetch<unknown>(
      `/api/autopilots/${autopilotId}/deliveries/${deliveryId}`,
    );
    return parseWithFallback(
      raw,
      WebhookDeliveryResponseSchema,
      { ...EMPTY_WEBHOOK_DELIVERY, id: deliveryId, autopilot_id: autopilotId },
      { endpoint: "GET /api/autopilots/:id/deliveries/:deliveryId" },
    );
  }

  // Replay creates a NEW delivery row referencing the original via
  // `replayed_from_delivery_id`. Server rejects replays of
  // signature-invalid / rejected deliveries with 400 — the UI keeps the
  // button disabled for those rows, but the server is the source of truth.
  async replayAutopilotDelivery(
    autopilotId: string,
    deliveryId: string,
  ): Promise<WebhookDelivery> {
    const raw = await this.fetch<unknown>(
      `/api/autopilots/${autopilotId}/deliveries/${deliveryId}/replay`,
      { method: "POST" },
    );
    return parseWithFallback(
      raw,
      WebhookDeliveryResponseSchema,
      { ...EMPTY_WEBHOOK_DELIVERY, autopilot_id: autopilotId },
      { endpoint: "POST /api/autopilots/:id/deliveries/:deliveryId/replay" },
    );
  }

  // GitHub integration
  async getGitHubConnectURL(workspaceId: string): Promise<GitHubConnectResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/github/connect`);
  }

  async listGitHubInstallations(workspaceId: string): Promise<ListGitHubInstallationsResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/github/installations`);
  }

  async deleteGitHubInstallation(workspaceId: string, installationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/github/installations/${installationId}`, {
      method: "DELETE",
    });
  }

  // Git credentials — per-workspace PATs for cloning private repos across
  // several accounts. The secret (token) is write-only; the list never returns it.
  async listGitCredentials(workspaceId: string): Promise<GitCredential[]> {
    return this.fetch(`/api/workspaces/${workspaceId}/git-credentials`);
  }

  async addGitCredential(
    workspaceId: string,
    data: { label?: string; provider?: string; host?: string; owner: string; username?: string; secret: string },
  ): Promise<GitCredential> {
    return this.fetch(`/api/workspaces/${workspaceId}/git-credentials`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async deleteGitCredential(workspaceId: string, credentialId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/git-credentials/${credentialId}`, {
      method: "DELETE",
    });
  }

  // Figma credential — one PAT per workspace so agents can read Figma designs
  // referenced by issues. The token is write-only; status carries last4 only.
  async getFigmaCredentialStatus(workspaceId: string): Promise<FigmaCredentialStatus> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/figma-credential`);
    return parseWithFallback(raw, FigmaCredentialStatusSchema, EMPTY_FIGMA_CREDENTIAL_STATUS, {
      endpoint: "GET /api/workspaces/{id}/figma-credential",
    });
  }

  async putFigmaCredential(
    workspaceId: string,
    data: { token: string; label?: string; expires_at?: string; probe_file_key?: string },
  ): Promise<FigmaCredentialStatus> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/figma-credential`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, FigmaCredentialStatusSchema, EMPTY_FIGMA_CREDENTIAL_STATUS, {
      endpoint: "PUT /api/workspaces/{id}/figma-credential",
    });
  }

  async deleteFigmaCredential(workspaceId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/figma-credential`, {
      method: "DELETE",
    });
  }

  // Remote-MCP credentials — sealed auth (bearer tokens) for remote http/sse
  // MCP servers, keyed by server name. The secret is write-only; the list never
  // returns it (has_secret + last4 only).
  async listMcpCredentials(workspaceId: string): Promise<McpCredentialStatus[]> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/mcp-credentials`);
    return parseWithFallback(raw, McpCredentialListSchema, EMPTY_MCP_CREDENTIAL_LIST, {
      endpoint: "GET /api/workspaces/{id}/mcp-credentials",
    });
  }

  async putMcpCredential(
    workspaceId: string,
    serverName: string,
    data: McpCredentialInput,
  ): Promise<McpCredentialStatus> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/mcp-credentials/${encodeURIComponent(serverName)}`,
      { method: "PUT", body: JSON.stringify(data) },
    );
    return parseWithFallback(
      raw,
      McpCredentialStatusSchema,
      { ...EMPTY_MCP_CREDENTIAL_STATUS, server_name: serverName, has_secret: true },
      { endpoint: "PUT /api/workspaces/{id}/mcp-credentials/{serverName}" },
    );
  }

  async deleteMcpCredential(workspaceId: string, serverName: string): Promise<void> {
    await this.fetch(
      `/api/workspaces/${workspaceId}/mcp-credentials/${encodeURIComponent(serverName)}`,
      { method: "DELETE" },
    );
  }

  // Release integrations — per-workspace outbound connectors that fire on
  // release-lifecycle events (Phase 2: signed webhook). The webhook URL +
  // signing secret are write-only; the list never returns them (has_secret only).
  async listReleaseIntegrations(workspaceId: string): Promise<ReleaseIntegration[]> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/release-integrations`);
    return parseWithFallback(raw, ReleaseIntegrationListSchema, EMPTY_RELEASE_INTEGRATIONS, {
      endpoint: "GET /api/workspaces/{id}/release-integrations",
    });
  }

  async createReleaseIntegration(
    workspaceId: string,
    data: ReleaseIntegrationInput,
  ): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/release-integrations`, {
      method: "POST",
      // kind defaults to webhook server-side when omitted.
      body: JSON.stringify({ kind: "webhook", ...data }),
    });
  }

  async updateReleaseIntegration(
    workspaceId: string,
    integrationId: string,
    data: ReleaseIntegrationInput,
  ): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/release-integrations/${integrationId}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  async deleteReleaseIntegration(workspaceId: string, integrationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/release-integrations/${integrationId}`, {
      method: "DELETE",
    });
  }

  // Design review — approve or request changes on an issue's design proposal.
  // sub_issue_overrides trims/edits the proposed sub-issues (applied at
  // decomposition, Phase 4). Returns the resulting design state.
  async createDesignReview(
    issueId: string,
    body: {
      action: "approve" | "request_changes";
      note?: string;
      supersede_previous?: boolean;
      sub_issue_overrides?: {
        index: number;
        include: boolean;
        title?: string;
        description?: string;
      }[];
    },
  ): Promise<{ action: string; state: string }> {
    return this.fetch(`/api/issues/${issueId}/design-review`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async listIssuePullRequests(issueId: string): Promise<{ pull_requests: GitHubPullRequest[] }> {
    return this.fetch(`/api/issues/${issueId}/pull-requests`);
  }

  /** Files changed per pull request linked to the issue — the in-app Changes
   *  view. Degrades to `{ changes: [] }` (which renders nothing) rather than
   *  throwing: this section is an addition to the issue page, and it must never
   *  be able to take the page down with it. */
  async getIssueChanges(issueId: string): Promise<IssueChangesResponse> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/changes`);
    return parseWithFallback(raw, IssueChangesResponseSchema, EMPTY_ISSUE_CHANGES, {
      endpoint: "GET /api/issues/{id}/changes",
    });
  }

  /** One file's unified diff, fetched live. `patch: null` with a `reason` is a
   *  NORMAL answer here (binary file, diff too large, no GitHub App), not an
   *  error — the UI turns each reason into a sentence. */
  async getIssueChangePatch(
    issueId: string,
    prNumber: number,
    path: string,
  ): Promise<IssueChangePatchResponse> {
    const query = `pr=${encodeURIComponent(String(prNumber))}&path=${encodeURIComponent(path)}`;
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/changes/patch?${query}`);
    return parseWithFallback(raw, IssueChangePatchResponseSchema, EMPTY_ISSUE_CHANGE_PATCH, {
      endpoint: "GET /api/issues/{id}/changes/patch",
    });
  }

  async getProjectDesignContext(projectId: string): Promise<DesignContextState> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/design-context`);
    return parseWithFallback(raw, DesignContextStateSchema, EMPTY_DESIGN_CONTEXT_STATE, {
      endpoint: "GET /api/projects/{id}/design-context",
    });
  }

  async proposeProjectDesignContext(
    projectId: string,
    context: DesignContextDocument,
    expectedRevision: number,
  ): Promise<DesignContextRevision> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/design-context`, {
      method: "PUT",
      body: JSON.stringify({ context, expected_revision: expectedRevision }),
    });
    return parseWithFallback(raw, DesignContextRevisionSchema, EMPTY_DESIGN_CONTEXT_REVISION, {
      endpoint: "PUT /api/projects/{id}/design-context",
    });
  }

  async reviewProjectDesignContext(
    projectId: string,
    action: "approve" | "reject",
    expectedRevision: number,
    reason = "",
  ): Promise<DesignContextRevision> {
    const raw = await this.fetch<unknown>(`/api/projects/${projectId}/design-context/${action}`, {
      method: "POST",
      body: JSON.stringify({ expected_revision: expectedRevision, reason }),
    });
    return parseWithFallback(raw, DesignContextRevisionSchema, EMPTY_DESIGN_CONTEXT_REVISION, {
      endpoint: `POST /api/projects/{id}/design-context/${action}`,
    });
  }

  async syncDesignContext(projectId: string): Promise<{ status: string; issue_id: string }> {
    return this.fetch(`/api/projects/${projectId}/design-context/sync`, {
      method: "POST",
    });
  }

  async auditDesignContext(projectId: string): Promise<{ status: string; issue_id: string }> {
    return this.fetch(`/api/projects/${projectId}/design-context/audit`, {
      method: "POST",
    });
  }

  // Apply one design-audit finding: create a codemod issue (adopt a token or
  // extract a component) from the audit block on the given issue. Returns the
  // new implementation issue.
  async applyDesignAudit(
    issueId: string,
    body: { kind: "token" | "component"; index: number },
  ): Promise<{ issue_id: string; title: string }> {
    return this.fetch(`/api/issues/${issueId}/design-apply`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  // Lark integration
  async getAutopilotTelegramDestination(autopilotId: string): Promise<AutopilotTelegramDestination> {
    const raw = await this.fetch<unknown>(`/api/autopilots/${autopilotId}/telegram-destination`);
    return parseWithFallback(raw, AutopilotTelegramDestinationSchema, EMPTY_AUTOPILOT_TELEGRAM_DESTINATION, {
      endpoint: "GET /api/autopilots/{id}/telegram-destination",
    });
  }

  // ---- Per-agent Telegram bots ----
  //
  // Parsed, not cast. This response feeds a settings panel inside an installed
  // desktop build that will outlive the server it was compiled against, so a
  // drifted field must downgrade rather than white-screen the page
  // (CLAUDE.md → API Response Compatibility).
  async listTelegramInstallations(workspaceId: string): Promise<ListTelegramInstallationsResponse> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/telegram/installations`);
    return parseWithFallback(raw, ListTelegramInstallationsSchema, EMPTY_TELEGRAM_INSTALLATIONS, {
      endpoint: "GET /api/workspaces/{id}/telegram/installations",
    });
  }

  // The per-agent routes are NOT workspace-scoped — the server resolves the
  // workspace from the agent. workspaceId is still taken so callers can key
  // the cache invalidation that follows.
  async installAgentTelegramBot(
    workspaceId: string,
    agentId: string,
    botToken: string,
    chatId?: string,
  ): Promise<TelegramInstallation> {
    void workspaceId;
    const raw = await this.fetch<unknown>(`/api/agents/${agentId}/telegram`, {
      method: "PUT",
      body: JSON.stringify({ bot_token: botToken, chat_id: chatId ?? "" }),
    });
    return parseWithFallback(raw, TelegramInstallationSchema, EMPTY_TELEGRAM_INSTALLATION, {
      endpoint: "PUT /api/agents/{id}/telegram",
    });
  }

  async deleteAgentTelegramBot(workspaceId: string, agentId: string): Promise<void> {
    void workspaceId;
    await this.fetch(`/api/agents/${agentId}/telegram`, { method: "DELETE" });
  }

  async setAgentTelegramAccess(
    workspaceId: string,
    agentId: string,
    body: SetTelegramAccessRequest,
  ): Promise<TelegramInstallation> {
    void workspaceId;
    const raw = await this.fetch<unknown>(`/api/agents/${agentId}/telegram/access`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
    return parseWithFallback(raw, TelegramInstallationSchema, EMPTY_TELEGRAM_INSTALLATION, {
      endpoint: "PUT /api/agents/{id}/telegram/access",
    });
  }

  async createAgentTelegramBindLink(
    workspaceId: string,
    agentId: string,
  ): Promise<TelegramBindLinkResponse> {
    void workspaceId;
    const raw = await this.fetch<unknown>(`/api/agents/${agentId}/telegram/bind-link`, {
      method: "POST",
    });
    return parseWithFallback(raw, TelegramBindLinkSchema, EMPTY_TELEGRAM_BIND_LINK, {
      endpoint: "POST /api/agents/{id}/telegram/bind-link",
    });
  }

  async listLarkInstallations(workspaceId: string): Promise<ListLarkInstallationsResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/lark/installations`);
  }

  async beginLarkInstall(
    workspaceId: string,
    agentId: string,
    region: "feishu" | "lark",
  ): Promise<BeginLarkInstallResponse> {
    // The user picks the cloud explicitly in the UI ("Bind to Feishu"
    // vs "Bind to Lark"), and the backend POSTs the device-flow `begin`
    // against the corresponding accounts host (accounts.feishu.cn vs
    // accounts.larksuite.com) so the QR renders against the right
    // cloud up front. Empty / omitted region still resolves to Feishu
    // server-side (RegionOrDefault) — we surface region as a required
    // arg here so every call site is forced to make a deliberate
    // choice rather than silently defaulting to mainland.
    const search = new URLSearchParams({ agent_id: agentId, region });
    return this.fetch(`/api/workspaces/${workspaceId}/lark/install/begin?${search.toString()}`, {
      method: "POST",
    });
  }

  async getLarkInstallStatus(workspaceId: string, sessionId: string): Promise<LarkInstallStatusResponse> {
    return this.fetch(`/api/workspaces/${workspaceId}/lark/install/${sessionId}/status`);
  }

  async deleteLarkInstallation(workspaceId: string, installationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/lark/installations/${installationId}`, {
      method: "DELETE",
    });
  }

  async redeemLarkBindingToken(token: string): Promise<RedeemLarkBindingTokenResponse> {
    return this.fetch(`/api/lark/binding/redeem`, {
      method: "POST",
      body: JSON.stringify({ token }),
    });
  }

  // --- Slack app (docs/slack-integration-plan.md §Phase 1) ------------------
  //
  // Parsed, not cast, every one of them. This surface drives a settings panel
  // that decides whether to show a Connect button and which channels are
  // wired to which events — a drifted response must degrade into a panel that
  // still renders, never a white screen or an install that cannot complete.
  //
  // The schemas live in ../slack/schemas beside the queries that consume
  // them, so the whole Slack contract reads in one file.

  async listSlackInstallations(workspaceId: string): Promise<ListSlackInstallationsResponse> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/slack/installations`);
    return parseWithFallback(raw, ListSlackInstallationsSchema, EMPTY_SLACK_INSTALLATIONS, {
      endpoint: "GET /api/workspaces/{id}/slack/installations",
    });
  }

  /** Mint the sealed state and hand back Slack's consent URL. Nothing is
   *  written until the OAuth callback returns. */
  async beginSlackInstall(workspaceId: string): Promise<SlackBeginResponse> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/slack/install/begin`, {
      method: "POST",
    });
    return parseWithFallback(raw, SlackBeginSchema, EMPTY_SLACK_BEGIN, {
      endpoint: "POST /api/workspaces/{id}/slack/install/begin",
    });
  }

  async deleteSlackInstallation(workspaceId: string, installationId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/slack/installations/${installationId}`, {
      method: "DELETE",
    });
  }

  /** Routes plus the event vocabulary the SERVER understands — shipped
   *  together so the UI cannot offer a checkbox that does nothing. */
  async listSlackRoutes(workspaceId: string): Promise<ListSlackRoutesResponse> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/slack/routes`);
    return parseWithFallback(raw, ListSlackRoutesSchema, EMPTY_SLACK_ROUTES, {
      endpoint: "GET /api/workspaces/{id}/slack/routes",
    });
  }

  /** Create (no routeId) or edit (routeId) one channel route. */
  async saveSlackRoute(
    workspaceId: string,
    input: SlackRouteInput,
    routeId?: string,
  ): Promise<SlackChannelRoute> {
    const path = routeId
      ? `/api/workspaces/${workspaceId}/slack/routes/${routeId}`
      : `/api/workspaces/${workspaceId}/slack/routes`;
    const raw = await this.fetch<unknown>(path, {
      method: "PUT",
      body: JSON.stringify(input),
    });
    return parseWithFallback(raw, SlackChannelRouteSchema, EMPTY_SLACK_ROUTE, {
      endpoint: "PUT /api/workspaces/{id}/slack/routes",
    });
  }

  async deleteSlackRoute(workspaceId: string, routeId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/slack/routes/${routeId}`, {
      method: "DELETE",
    });
  }

  /** One page of the channel picker. Paging is passed through rather than
   *  walked, because conversations.list is Tier 2 and a workspace with
   *  hundreds of channels would spend that budget on one page load. */
  async listSlackChannels(
    workspaceId: string,
    installationId?: string,
    cursor?: string,
  ): Promise<ListSlackChannelsResponse> {
    const search = new URLSearchParams();
    if (installationId) search.set("installation_id", installationId);
    if (cursor) search.set("cursor", cursor);
    const query = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/slack/channels${query ? `?${query}` : ""}`,
    );
    return parseWithFallback(raw, ListSlackChannelsSchema, EMPTY_SLACK_CHANNELS, {
      endpoint: "GET /api/workspaces/{id}/slack/channels",
    });
  }

  /** Start the PERSONAL link — one person binding their Slack identity to
   *  their Agora account. Requests no bot scopes. */
  async beginSlackUserLink(): Promise<SlackBeginResponse> {
    const raw = await this.fetch<unknown>("/api/me/links/slack/begin", { method: "POST" });
    return parseWithFallback(raw, SlackBeginSchema, EMPTY_SLACK_BEGIN, {
      endpoint: "POST /api/me/links/slack/begin",
    });
  }

  // --- Importers (docs/importers-plan.md §6) --------------------------------
  //
  // Every response here goes through a schema with an explicit fallback. An
  // import is the one place in the product where a drifted response would be
  // read as "nothing to import" — a blank preview over a workspace full of
  // issues — so parse-don't-cast is not optional on this surface.
  //
  // The source API key appears in exactly ONE direction: the body of
  // createImportConnection. Nothing reads it back, and no method here returns
  // anything derived from it.

  async listImportConnections(workspaceId: string): Promise<ImportConnection[]> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/import/connections`);
    return parseWithFallback(raw, ImportConnectionsResponseSchema, EMPTY_IMPORT_CONNECTIONS, {
      endpoint: "GET /api/workspaces/{id}/import/connections",
    });
  }

  async createImportConnection(
    workspaceId: string,
    data: CreateImportConnectionRequest,
  ): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/import/connections`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  async probeImportConnection(workspaceId: string, connectionId: string): Promise<void> {
    await this.fetch(
      `/api/workspaces/${workspaceId}/import/connections/${connectionId}/probe`,
      { method: "POST" },
    );
  }

  async deleteImportConnection(workspaceId: string, connectionId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/import/connections/${connectionId}`, {
      method: "DELETE",
    });
  }

  async dryRunImport(
    workspaceId: string,
    data: ImportDryRunRequest,
  ): Promise<ImportDryRunResult> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/import/dry-run`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, ImportDryRunResponseSchema, EMPTY_IMPORT_DRY_RUN, {
      endpoint: "POST /api/workspaces/{id}/import/dry-run",
    });
  }

  async startImport(workspaceId: string, data: StartImportRequest): Promise<StartImportResult> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/import/jobs`, {
      method: "POST",
      body: JSON.stringify(data),
    });
    return parseWithFallback(raw, StartImportResponseSchema, EMPTY_START_IMPORT, {
      endpoint: "POST /api/workspaces/{id}/import/jobs",
    });
  }

  async getImportJob(workspaceId: string, jobId: string): Promise<ImportJob> {
    const raw = await this.fetch<unknown>(`/api/workspaces/${workspaceId}/import/jobs/${jobId}`);
    return parseWithFallback(raw, ImportJobSchema, EMPTY_IMPORT_JOB, {
      endpoint: "GET /api/workspaces/{id}/import/jobs/{jid}",
    });
  }

  async cancelImport(workspaceId: string, jobId: string): Promise<void> {
    await this.fetch(`/api/workspaces/${workspaceId}/import/jobs/${jobId}/cancel`, {
      method: "POST",
    });
  }
}
