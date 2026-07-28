import { z } from "zod";
import type {
  Agent,
  AgentTemplate,
  AgentTemplateSummary,
  Attachment,
  BillingBalance,
  BillingBatchesPage,
  BillingCheckoutSessionStatus,
  BillingPriceTier,
  BillingTopupsPage,
  BillingTransactionsPage,
  CancelTaskResponse,
  CreateAgentFromTemplateResponse,
  CreateBillingCheckoutSessionResponse,
  CreateBillingPortalSessionResponse,
  FigmaCredentialStatus,
  McpCredentialStatus,
  GroupedIssuesResponse,
  ListIssuesResponse,
  ListWebhookDeliveriesResponse,
  ReleaseIntegration,
  OrchestrationRun,
  IssueArtifactResponse,
  Squad,
  AutopilotTelegramDestination,
  TelegramBindLinkResponse,
  TelegramInstallation,
  TimelineEntry,
  User,
  WebhookDelivery,
} from "../types";

const OrchestrationStepSchema = z.object({
  id: z.string(),
  key: z.string(),
  title: z.string(),
  stage: z.string(),
  status: z.string(),
  position: z.number(),
  agent_id: z.string().optional(),
  model: z.string().optional(),
  task_id: z.string().optional(),
  approval_required: z.boolean().default(false),
  approved_by: z.string().optional(),
  attempt: z.number().default(0),
  max_attempts: z.number().default(1),
  instructions: z.string().default(""),
  output: z.unknown().optional(),
  error: z.string().optional(),
  depends_on_step_ids: z.array(z.string()).nullish().transform((value) => value ?? []),
  parent_step_id: z.string().optional(),
  squad_id: z.string().optional(),
  controller_agent_id: z.string().optional(),
  worktree_branch: z.string().optional(),
  base_sha: z.string().optional(),
  head_sha: z.string().optional(),
  merge_status: z.enum(["not_checked", "clean", "conflicts", "uncommitted", "unavailable"]).default("not_checked"),
  conflict_files: z.array(z.string()).nullish().transform((value) => value ?? []),
  kind: z.enum(["task", "integration"]).default("task"),
  capability: z.enum(["coordination", "implementation", "backend", "frontend", "mobile", "infrastructure", "documentation", "integration", "qa", "review", "release"]).default("implementation"),
  integration_status: z.enum(["not_required", "pending", "complete", "missing_heads", "conflicts"]).default("not_required"),
  integrated_head_shas: z.array(z.string()).nullish().transform((value) => value ?? []),
  missing_head_shas: z.array(z.string()).nullish().transform((value) => value ?? []),
}).loose();

const OrchestrationEventSchema = z.object({
  id: z.string(),
  step_id: z.string().optional(),
  kind: z.string(),
  actor_type: z.string(),
  actor_id: z.string().optional(),
  details: z.record(z.string(), z.unknown()).default({}),
  created_at: z.string(),
}).loose();

const OrchestrationPlanRevisionSchema = z.object({
  id: z.string(), version: z.number(), actor_type: z.string(), actor_id: z.string().optional(),
  reason: z.string(), patch: z.record(z.string(), z.unknown()).default({}), created_at: z.string(),
}).loose();

export const OrchestrationRunSchema = z.object({
  id: z.string(),
  issue_id: z.string(),
  status: z.string(),
  mode: z.string(),
  execution_strategy: z.enum(["human", "solo", "squad", "custom"]).default("custom"),
  progression_policy: z.enum(["automatic", "gated", "manual"]).default("automatic"),
  policy: z.record(z.string(), z.unknown()).default({}),
  owner_type: z.enum(["agent", "squad", "member", "unassigned"]).default("unassigned"),
  owner_id: z.string().optional(),
  controller_agent_id: z.string().optional(),
  base_git_states: z.array(z.object({ repo: z.string(), head_sha: z.string() }).loose()).nullish().transform((value) => value ?? []),
  execution_mode: z.enum(["direct", "squad", "orchestrated"]).default("orchestrated"),
  plan_version: z.number().default(1),
  revisions: z.array(OrchestrationPlanRevisionSchema).default([]),
  started_at: z.string().optional(),
  completed_at: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string(),
  steps: z.array(OrchestrationStepSchema).default([]),
  events: z.array(OrchestrationEventSchema).default([]),
}).loose();

export const EMPTY_ORCHESTRATION_RUN: OrchestrationRun | null = null;

const ArtifactRepoRefSchema = z.object({
  repo: z.string(),
  branch: z.string().optional(),
  base_sha: z.string(),
  head_sha: z.string(),
  merge_status: z.string(),
}).strip();

const IssueArtifactSummarySchema = z.object({
  id: z.string(),
  run_id: z.string(),
  step_id: z.string(),
  step_key: z.string(),
  title: z.string(),
  kind: z.string(),
  capability: z.string(),
  canonical: z.boolean().default(false),
  repos: z.array(ArtifactRepoRefSchema).default([]),
  completed_at: z.string().optional(),
}).strip();

export const IssueArtifactResponseSchema = z.object({
  run_id: z.string().default(""),
  run_status: z.string().default(""),
  ready: z.boolean().default(false),
  reason: z.string().optional(),
  artifact: IssueArtifactSummarySchema.optional(),
  components: z.array(IssueArtifactSummarySchema).default([]),
  daemon_url: z.string().default(""),
  capabilities: z.record(z.string(), z.string()).default({}),
}).strip();

export const EMPTY_ISSUE_ARTIFACT: IssueArtifactResponse = {
  run_id: "",
  run_status: "",
  ready: false,
  components: [],
  daemon_url: "",
  capabilities: {},
};
import type { CloudRuntimeNode } from "../runtimes/cloud-runtime";

export interface AppConfigResponse {
  cdn_domain: string;
  allow_signup: boolean;
  google_client_id?: string;
  telegram_bot_username?: string;
  posthog_key?: string;
  posthog_host?: string;
  analytics_environment?: string;
  daemon_server_url?: string;
  daemon_app_url?: string;
  cli_releases_url?: string;
  workspace_creation_disabled?: boolean;
  telegram_only?: boolean;
  bitrix_enabled?: boolean;
  zoho_enabled?: boolean;
  lark_enabled?: boolean;
  telegram_bots_enabled?: boolean;
}

// ---------------------------------------------------------------------------
// Schemas for the highest-risk API endpoints — those whose responses drive
// the issue detail page (timeline, comments, subscribers) and the issues
// list. These are the surfaces that white-screened in #2143 / #2147 / #2192.
//
// These schemas are intentionally LENIENT:
//   - String enums are stored as `z.string()` rather than `z.enum([...])`.
//     A new server-side enum value should render as a generic fallback in
//     the UI, never crash a `safeParse`.
//   - Optional fields are unioned with `null` and given fallbacks where
//     existing UI code already coerces them.
//   - Arrays default to `[]` so a missing `reactions` / `attachments` /
//     `entries` field doesn't take the page down.
//   - Every object schema ends with `.loose()` so unknown server-side
//     fields pass through unchanged. zod 4's `.object()` defaults to STRIP,
//     which would silently delete fields the schema didn't explicitly list
//     — fine while the TS type doesn't claim them, but the moment a future
//     PR adds a TS field without updating the schema, the cast `as T` lies
//     and the field shows up as `undefined` at runtime. `.loose()` removes
//     that synchronisation hazard.
//
// These schemas are deliberately not typed as `z.ZodType<TimelineEntry>` /
// `z.ZodType<Issue>` etc. — the strict TS types narrow string fields to
// literal unions, which would defeat the leniency above. `parseWithFallback`
// returns the parsed value cast to the caller-supplied `T`, so the strict
// type still flows out at the call site; the schema only guards shape.
// ---------------------------------------------------------------------------

const ReactionSchema = z.object({
  id: z.string(),
  comment_id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  emoji: z.string(),
  created_at: z.string(),
});

// Nested attachments embedded in timeline/comment responses stay lenient on
// purpose: a single malformed attachment must not knock the whole timeline
// into the fallback `[]`.
const AttachmentSchema = z.object({
  id: z.string(),
}).loose();

// Standalone attachment lookup (`GET /api/attachments/{id}`) is the source of
// truth for click-time download URLs. The two fields the download flow opens
// in a new tab — `download_url` and `url` — must be strings, otherwise we'd
// happily `window.open(undefined)`. `filename` gates the toast/title and is
// also enforced so a missing value falls back to the empty record below.
//
// `markdown_url` is parsed lenient: a server old enough to predate
// MUL-3192 omits the field, in which case the schema defaults it to "".
// Callers that need to persist a URL into markdown should go through the
// `useFileUpload` helper (which falls back to the legacy
// `attachmentDownloadPath` shape when `markdown_url` is empty), so the
// empty-string default does not silently break any persistence path.
export const AttachmentResponseSchema = z.object({
  id: z.string(),
  url: z.string(),
  download_url: z.string(),
  markdown_url: z.string().optional().default(""),
  filename: z.string(),
  chat_session_id: z.string().nullable().optional(),
  chat_message_id: z.string().nullable().optional(),
}).loose();

export const EMPTY_ATTACHMENT: Attachment = {
  id: "",
  workspace_id: "",
  issue_id: null,
  comment_id: null,
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "",
  uploader_id: "",
  filename: "",
  url: "",
  download_url: "",
  markdown_url: "",
  content_type: "",
  size_bytes: 0,
  created_at: "",
};

// All object schemas use `.loose()` so unknown server-side fields pass
// through unchanged. zod 4's `.object()` defaults to STRIP, which would
// silently drop new fields and surface as a "field neither showed up in
// the UI" mystery the next time the TS type adopted them but the schema
// wasn't updated in lock-step. `.loose()` removes that synchronisation
// hazard — the schema validates the shape it knows about and leaves the
// rest alone.
const TimelineEntrySchema = z.object({
  type: z.string(),
  id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  created_at: z.string(),
  action: z.string().optional(),
  details: z.record(z.string(), z.unknown()).optional(),
  content: z.string().optional(),
  parent_id: z.string().nullable().optional(),
  updated_at: z.string().optional(),
  comment_type: z.string().optional(),
  reactions: z.array(ReactionSchema).optional(),
  attachments: z.array(AttachmentSchema).optional(),
  bitrix_comment_id: z.string().optional(),
  coalesced_count: z.number().optional(),
}).loose();

// /timeline returns a flat array of TimelineEntry, oldest first. The
// previously cursor-paginated wrapper was removed (#1929) — at observed data
// sizes (p99 ~30 entries per issue) paged delivery only created bugs.
export const TimelineEntriesSchema = z.array(TimelineEntrySchema);

export const EMPTY_TIMELINE_ENTRIES: TimelineEntry[] = [];

const OptionalStringSchema = z.preprocess(
  (value) => (typeof value === "string" ? value : undefined),
  z.string().optional(),
);

const BooleanWithDefaultSchema = (fallback: boolean) =>
  z.preprocess(
    (value) => (typeof value === "boolean" ? value : undefined),
    z.boolean().default(fallback),
  );

export const AppConfigSchema = z.object({
  cdn_domain: z.string().default(""),
  allow_signup: BooleanWithDefaultSchema(true),
  google_client_id: OptionalStringSchema,
  telegram_bot_username: OptionalStringSchema,
  posthog_key: OptionalStringSchema,
  posthog_host: OptionalStringSchema,
  analytics_environment: OptionalStringSchema,
  daemon_server_url: OptionalStringSchema,
  daemon_app_url: OptionalStringSchema,
  workspace_creation_disabled: BooleanWithDefaultSchema(false).optional(),
  telegram_only: BooleanWithDefaultSchema(false).optional(),
  bitrix_enabled: BooleanWithDefaultSchema(false).optional(),
  zoho_enabled: BooleanWithDefaultSchema(false).optional(),
  lark_enabled: BooleanWithDefaultSchema(false).optional(),
  telegram_bots_enabled: BooleanWithDefaultSchema(false).optional(),
}).loose();

export const EMPTY_APP_CONFIG: AppConfigResponse = {
  cdn_domain: "",
  allow_signup: true,
  google_client_id: "",
  telegram_bot_username: "",
  daemon_server_url: "",
  daemon_app_url: "",
  workspace_creation_disabled: false,
};

export const CommentSchema = z.object({
  id: z.string(),
  issue_id: z.string(),
  author_type: z.string(),
  author_id: z.string(),
  content: z.string(),
  type: z.string(),
  parent_id: z.string().nullable(),
  reactions: z.array(ReactionSchema).default([]),
  attachments: z.array(AttachmentSchema).default([]),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CommentsListSchema = z.array(CommentSchema);

const CommentTriggerPreviewAgentSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  avatar_url: z.string().optional(),
  source: z.string().default(""),
  reason: z.string().default(""),
}).loose();

export const CommentTriggerPreviewSchema = z.object({
  agents: z.array(CommentTriggerPreviewAgentSchema).default([]),
}).loose();

// Metadata is primitive-only by API/DB contract. Stay lenient on shape:
// unknown keys land as `unknown` to a caller, but the field itself defaults
// to {} so consumers never need to nil-guard `issue.metadata`.
//
// Values are intentionally `unknown` (not scalar-only): metadata is a freeform
// JSON blob and the server stores rich shapes there — e.g. the Bitrix sync
// keeps ARRAY values (bitrix_synced_comment_ids / _file_ids) for incremental
// dedup. A scalar-only union rejected those, which (because parseWithFallback
// fails the WHOLE response) collapsed the entire issue list to empty for any
// imported issue. Keep this permissive so a new metadata shape can never
// white-screen the board.
const IssueMetadataSchema = z.record(z.string(), z.unknown()).default({});

export const IssueSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  number: z.number(),
  identifier: z.string(),
  title: z.string(),
  description: z.string().nullable(),
  status: z.string(),
  priority: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  // Detail-only: the orchestrator that owns this task's pipeline (see the Issue
  // type). Optional — list/broadcast paths omit it; nullish tolerates both a
  // missing key and an explicit null.
  orchestrator_agent_id: z.string().nullish(),
  creator_type: z.string(),
  creator_id: z.string(),
  parent_issue_id: z.string().nullable(),
  project_id: z.string().nullable(),
  position: z.number(),
  start_date: z.string().nullable(),
  due_date: z.string().nullable(),
  metadata: IssueMetadataSchema,
  reactions: z.array(z.unknown()).optional(),
  labels: z.array(z.unknown()).optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const ListIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_LIST_ISSUES_RESPONSE: ListIssuesResponse = {
  issues: [],
  total: 0,
};

const IssueAssigneeGroupSchema = z.object({
  id: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const GroupedIssuesResponseSchema = z.object({
  groups: z.array(IssueAssigneeGroupSchema).default([]),
}).loose();

export const EMPTY_GROUPED_ISSUES_RESPONSE: GroupedIssuesResponse = {
  groups: [],
};

const SubscriberSchema = z.object({
  issue_id: z.string(),
  user_type: z.string(),
  user_id: z.string(),
  reason: z.string(),
  created_at: z.string(),
}).loose();

export const SubscribersListSchema = z.array(SubscriberSchema);

export const ChildIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
}).loose();

export const CloudRuntimeNodeSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  instance_id: z.string(),
  region: z.string(),
  instance_type: z.string(),
  image_id: z.string(),
  subnet_id: z.string(),
  name: z.string(),
  status: z.string(),
  tags: z.record(z.string(), z.string()).default({}),
  metadata: z.record(z.string(), z.unknown()).default({}),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CloudRuntimeNodeListSchema = z.array(CloudRuntimeNodeSchema);

export const EMPTY_CLOUD_RUNTIME_NODE_LIST: CloudRuntimeNode[] = [];

export const EMPTY_CLOUD_RUNTIME_NODE: CloudRuntimeNode = {
  id: "",
  owner_id: "",
  instance_id: "",
  region: "",
  instance_type: "",
  image_id: "",
  subnet_id: "",
  name: "",
  status: "",
  tags: {},
  metadata: {},
  created_at: "",
  updated_at: "",
};

// ---------------------------------------------------------------------------
// Workspace dashboard schemas
//
// The dashboard hits three independent rollup endpoints. Each returns a flat
// array, and every field is consumed by chart / KPI math — a missing number
// silently degrades to NaN downstream, so we coerce missing numbers to 0.
// String fields default to "" (no enum narrowing) to survive future model /
// agent ID drift, and so a single null from tz-aware SQL bucketing fails
// only that row instead of dropping the whole array to the `[]` fallback.
// ---------------------------------------------------------------------------

const DashboardUsageDailySchema = z.object({
  date: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const DashboardUsageDailyListSchema = z.array(DashboardUsageDailySchema);

const DashboardUsageByAgentSchema = z.object({
  agent_id: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const DashboardUsageByAgentListSchema = z.array(DashboardUsageByAgentSchema);

const DashboardAgentRunTimeSchema = z.object({
  agent_id: z.string().default(""),
  total_seconds: z.number().default(0),
  task_count: z.number().default(0),
  failed_count: z.number().default(0),
}).loose();

export const DashboardAgentRunTimeListSchema = z.array(DashboardAgentRunTimeSchema);

const DashboardRunTimeDailySchema = z.object({
  date: z.string().default(""),
  total_seconds: z.number().default(0),
  task_count: z.number().default(0),
  failed_count: z.number().default(0),
}).loose();

export const DashboardRunTimeDailyListSchema = z.array(DashboardRunTimeDailySchema);

// ---------------------------------------------------------------------------
// Runtime usage schemas — the runtime-detail page's four usage endpoints
// (`/api/runtimes/:id/usage*`). Same leniency rules as the dashboard
// schemas above: numbers default to 0, strings to "", `.loose()` passes
// unknown fields.
// ---------------------------------------------------------------------------

const RuntimeUsageSchema = z.object({
  runtime_id: z.string().default(""),
  date: z.string().default(""),
  provider: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
}).loose();

export const RuntimeUsageListSchema = z.array(RuntimeUsageSchema);

const RuntimeHourlyActivitySchema = z.object({
  hour: z.number().default(0),
  count: z.number().default(0),
}).loose();

export const RuntimeHourlyActivityListSchema = z.array(RuntimeHourlyActivitySchema);

const RuntimeUsageByAgentSchema = z.object({
  agent_id: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const RuntimeUsageByAgentListSchema = z.array(RuntimeUsageByAgentSchema);

const RuntimeUsageByHourSchema = z.object({
  hour: z.number().default(0),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const RuntimeUsageByHourListSchema = z.array(RuntimeUsageByHourSchema);

// ---------------------------------------------------------------------------
// Task cancellation (`POST /api/tasks/:id/cancel`)
//
// This response is consumed directly by chat recovery. The embedded task
// object stays loose so daemon/runtime fields can drift, but the optional
// `cancelled_chat_message` payload must be well-formed before the UI deletes
// a message from cache or restores text into the input.
// ---------------------------------------------------------------------------

const AgentTaskResponseSchema = z.object({
  id: z.string(),
  agent_id: z.string().default(""),
  runtime_id: z.string().default(""),
  issue_id: z.string().default(""),
  status: z.string().default("cancelled"),
  priority: z.number().default(0),
  dispatched_at: z.string().nullable().default(null),
  started_at: z.string().nullable().default(null),
  completed_at: z.string().nullable().default(null),
  result: z.unknown().default(null),
  error: z.string().nullable().default(null),
  failure_reason: z.string().optional(),
  created_at: z.string().default(""),
  chat_session_id: z.string().optional(),
  autopilot_run_id: z.string().optional(),
  parent_task_id: z.string().optional(),
  attempt: z.number().optional(),
  trigger_comment_id: z.string().optional(),
  trigger_summary: z.string().optional(),
  kind: z.string().optional(),
  work_dir: z.string().optional(),
  relative_work_dir: z.string().optional(),
}).loose();

const CancelledChatMessageSchema = z.object({
  chat_session_id: z.string(),
  message_id: z.string(),
  content: z.string(),
  restore_to_input: z.boolean().default(false),
}).loose();

export const CancelTaskResponseSchema = AgentTaskResponseSchema.extend({
  cancelled_chat_message: CancelledChatMessageSchema.nullish()
    .transform((value) => value ?? undefined),
}).loose();

export const EMPTY_CANCEL_TASK_RESPONSE: CancelTaskResponse = {
  id: "",
  agent_id: "",
  runtime_id: "",
  issue_id: "",
  status: "cancelled",
  priority: 0,
  dispatched_at: null,
  started_at: null,
  completed_at: null,
  result: null,
  error: null,
  created_at: "",
};

// ---------------------------------------------------------------------------
// Agent template catalog — `/api/agent-templates*` and the
// create-from-template response. The desktop app's create-agent picker
// reaches these endpoints, and a future server change to the template shape
// would white-screen older installed builds (#2192 pattern) without these
// parsers. Lenient by the same rules as IssueSchema above: arrays default to
// `[]`, optional fields stay optional, `.loose()` lets unknown fields pass
// through unchanged.
// ---------------------------------------------------------------------------

const AgentTemplateSkillRefSchema = z.object({
  source_url: z.string(),
  cached_name: z.string().default(""),
  cached_description: z.string().default(""),
}).loose();

const AgentTemplateSummarySchemaBase = z.object({
  slug: z.string(),
  name: z.string(),
  description: z.string().default(""),
  category: z.string().optional(),
  icon: z.string().optional(),
  accent: z.string().optional(),
  // skills MUST default to [] — picker code reads `template.skills.length`
  // and `.map(...)`, both of which crash on `undefined`. The most common
  // future drift (field renamed / wrapped) lands here.
  skills: z.array(AgentTemplateSkillRefSchema).default([]),
}).loose();

export const AgentTemplateSummarySchema = AgentTemplateSummarySchemaBase;

// List endpoint historically returns a bare array. Server could legitimately
// migrate to `{templates: [...]}` later — we accept either shape so an old
// desktop survives the upgrade.
export const AgentTemplateSummaryListSchema = z.union([
  z.array(AgentTemplateSummarySchemaBase),
  z.object({ templates: z.array(AgentTemplateSummarySchemaBase).default([]) })
    .loose()
    .transform((v) => v.templates),
]);

export const EMPTY_AGENT_TEMPLATE_SUMMARY_LIST: AgentTemplateSummary[] = [];

export const AgentTemplateSchema = AgentTemplateSummarySchemaBase.extend({
  // Detail-only field. Default "" so a malformed detail still renders the
  // header + skill list; the user just sees an empty Instructions block.
  instructions: z.string().default(""),
}).loose();

// Used as the parse fallback for `GET /api/agent-templates/:slug`. Slug comes
// from the URL, so we round-trip the requested one back into the fallback
// at the call site (see `getAgentTemplate` in client.ts).
export const EMPTY_AGENT_TEMPLATE_DETAIL: AgentTemplate = {
  slug: "",
  name: "",
  description: "",
  skills: [],
  instructions: "",
};

// `agent` is a full Agent record — schematising every field would duplicate
// a 50-field interface and bit-rot fast. We keep it loose and require only
// `id`, the one field the create-from-template flow consumes (used to
// navigate to the new agent's detail page). Downstream code already
// optional-chains the rest.
const MinimalAgentSchema = z.object({
  id: z.string(),
}).loose();

export const CreateAgentFromTemplateResponseSchema = z.object({
  agent: MinimalAgentSchema,
  imported_skill_ids: z.array(z.string()).default([]),
  reused_skill_ids: z.array(z.string()).default([]),
}).loose();

// Fallback when the success response fails to parse. The agent server-side
// has likely been created already, so we can't pretend nothing happened —
// the caller (`create-agent-dialog.tsx`) is responsible for noticing
// `agent.id === ""` and skipping navigation while keeping the list
// invalidation, so the user finds their new agent in the list.
export const EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE: CreateAgentFromTemplateResponse = {
  agent: { id: "" } as Agent,
  imported_skill_ids: [],
  reused_skill_ids: [],
};

// Squad list responses carry lightweight membership previews used by hover
// cards. The preview fields are additive API fields, so older backends default
// cleanly to no preview instead of breaking newer frontends.
const SquadMemberPreviewSchema = z.object({
  member_type: z.string(),
  member_id: z.string(),
  role: z.string().default(""),
}).loose();

export const SquadSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  name: z.string(),
  description: z.string().default(""),
  instructions: z.string().default(""),
  avatar_url: z.string().nullable().optional().transform((v) => v ?? null),
  leader_id: z.string(),
  creator_id: z.string(),
  created_at: z.string(),
  updated_at: z.string(),
  archived_at: z.string().nullable().optional().transform((v) => v ?? null),
  archived_by: z.string().nullable().optional().transform((v) => v ?? null),
  member_count: z.number().default(0),
  member_preview: z.array(SquadMemberPreviewSchema).default([]),
}).loose();

export const SquadListSchema = z.array(SquadSchema);
export const EMPTY_SQUAD_LIST: Squad[] = [];
export const EMPTY_SQUAD: Squad = {
  id: "",
  workspace_id: "",
  name: "",
  description: "",
  instructions: "",
  avatar_url: null,
  leader_id: "",
  creator_id: "",
  created_at: "",
  updated_at: "",
  archived_at: null,
  archived_by: null,
  member_count: 0,
  member_preview: [],
};

// Squad member status — backs the Squad detail page's Members tab. status
// is `string | null` (not the narrow `SquadMemberStatusValue` union) so a
// new server-side status doesn't fail the parse; the UI defaults to a
// neutral pill for unknown values.
const SquadActiveIssueBriefSchema = z.object({
  issue_id: z.string(),
  identifier: z.string(),
  title: z.string(),
  issue_status: z.string(),
}).loose();

const SquadMemberStatusSchema = z.object({
  member_type: z.string(),
  member_id: z.string(),
  status: z.string().nullable().optional().transform((v) => v ?? null),
  active_issues: z.array(SquadActiveIssueBriefSchema).default([]),
  last_active_at: z.string().nullable().optional().transform((v) => v ?? null),
}).loose();

export const SquadMemberStatusListResponseSchema = z.object({
  members: z.array(SquadMemberStatusSchema).default([]),
}).loose();

export const EMPTY_SQUAD_MEMBER_STATUS_LIST = { members: [] };

// ---------------------------------------------------------------------------
// Structured error body — POST /api/workspaces/:wsId/issues 409 conflict.
//
// When the server detects an active issue with the same title in the same
// workspace, it returns `{ code: "active_duplicate_issue", error, issue }`
// instead of letting the create through. The UI uses the embedded issue ref
// to offer "view existing" rather than dropping the user into a generic
// "create failed" toast.
//
// Strict guarantees:
//   - `code` is a literal so a future server rename (e.g. `duplicate_issue`)
//     fails the parse and falls back to a normal error toast — drift never
//     ships as a broken duplicate UI.
//   - `issue` is required; without an id/identifier/title the "view existing"
//     button has nothing to point at, so we'd rather fall back than guess.
//   - `issue.status` is intentionally OMITTED: the duplicate toast doesn't
//     render a StatusIcon (which has no fallback for unknown enum values),
//     so a future server-side rename of `status` must not knock this branch
//     out. `.loose()` lets the field pass through unchanged for any other
//     consumer.
// ---------------------------------------------------------------------------

export const DuplicateIssueErrorBodySchema = z.object({
  code: z.literal("active_duplicate_issue"),
  error: z.string().optional(),
  issue: z.object({
    id: z.string(),
    identifier: z.string(),
    title: z.string(),
  }).loose(),
}).loose();

export interface DuplicateIssueErrorBody {
  code: "active_duplicate_issue";
  error?: string;
  issue: {
    id: string;
    identifier: string;
    title: string;
  };
}

// ---------------------------------------------------------------------------
// Webhook delivery schemas — backing the Autopilot Deliveries section. Enums
// (`status`, `signature_status`, `provider`) are kept as `z.string()` so a
// future server-side value (e.g. a Stripe provider, a new dedupe state)
// degrades to a generic UI fallback rather than collapsing the list into
// the empty array. `.loose()` lets unknown fields pass through, matching
// the rule used by every other endpoint here.
// ---------------------------------------------------------------------------

const WebhookDeliverySchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  autopilot_id: z.string(),
  trigger_id: z.string(),
  provider: z.string(),
  event: z.string(),
  dedupe_key: z.string().nullable(),
  dedupe_source: z.string().nullable(),
  signature_status: z.string(),
  status: z.string(),
  attempt_count: z.number().default(0),
  content_type: z.string().nullable(),
  response_status: z.number().nullable(),
  autopilot_run_id: z.string().nullable(),
  replayed_from_delivery_id: z.string().nullable(),
  error: z.string().nullable(),
  received_at: z.string(),
  last_attempt_at: z.string(),
  created_at: z.string(),
  // Detail-only fields. The list endpoint omits them; the detail endpoint
  // populates raw_body / selected_headers / response_body.
  selected_headers: z.record(z.string(), z.unknown()).nullable().optional(),
  raw_body: z.string().nullable().optional(),
  response_body: z.string().nullable().optional(),
}).loose();

export const ListWebhookDeliveriesResponseSchema = z.object({
  deliveries: z.array(WebhookDeliverySchema).default([]),
  total: z.number().default(0),
}).loose();

export const WebhookDeliveryResponseSchema = WebhookDeliverySchema;

export const EMPTY_LIST_WEBHOOK_DELIVERIES_RESPONSE: ListWebhookDeliveriesResponse = {
  deliveries: [],
  total: 0,
};

export const EMPTY_WEBHOOK_DELIVERY: WebhookDelivery = {
  id: "",
  workspace_id: "",
  autopilot_id: "",
  trigger_id: "",
  provider: "",
  event: "",
  dedupe_key: null,
  dedupe_source: null,
  signature_status: "not_required",
  status: "queued",
  attempt_count: 0,
  content_type: null,
  response_status: null,
  autopilot_run_id: null,
  replayed_from_delivery_id: null,
  error: null,
  received_at: "",
  last_attempt_at: "",
  created_at: "",
};

// ---------------------------------------------------------------------------
// User (`/api/me` GET + PATCH). The auth store and Settings → Account both
// trust this shape — a drift here would knock both surfaces out. Kept
// lenient by the same rules as IssueSchema: enums stay `z.string()`,
// nullable fields are unioned with `null`, unknown server fields pass
// through via `.loose()`. `profile_description` is the field added in
// MUL-2406; the server emits `""` when unset (NOT NULL DEFAULT ''), so
// the schema defaults to `""` too — keeps the type tight without
// breaking older backends that don't return the column yet.
// ---------------------------------------------------------------------------

export const UserSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  email: z.string().default(""),
  avatar_url: z.string().nullable().default(null),
  onboarded_at: z.string().nullable().default(null),
  onboarding_questionnaire: z.record(z.string(), z.unknown()).default({}),
  starter_content_state: z.string().nullable().default(null),
  language: z.string().nullable().default(null),
  profile_description: z.string().default(""),
  timezone: z.string().nullable().default(null),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const EMPTY_USER: User = {
  id: "",
  name: "",
  email: "",
  avatar_url: null,
  onboarded_at: null,
  onboarding_questionnaire: {},
  starter_content_state: null,
  language: null,
  profile_description: "",
  timezone: null,
  created_at: "",
  updated_at: "",
};

// ---------------------------------------------------------------------------
// Settings → Labs workspace flags (GET/PUT /api/workspace-labs). qa_dev_boxes
// routes QA to the assignee-developer's own box; qa_fallback_box_id is the
// shared box QA lands on when nothing else matches.
export const WorkspaceLabsSchema = z
  .object({
    qa_dev_boxes: z.boolean().default(true),
    qa_fallback_box_id: z.string().default(""),
    qa_dev_runtimes: z.boolean().default(false),
    qa_dev_runtimes_strict: z.boolean().default(false),
  })
  .loose();

export const EMPTY_WORKSPACE_LABS = {
  qa_dev_boxes: true,
  qa_fallback_box_id: "",
  qa_dev_runtimes: false,
  qa_dev_runtimes_strict: false,
};

// ---------------------------------------------------------------------------
// Policy Agent — fleet watchdog. Lenient + defaulted (arrays default to []) so a
// degraded response renders an empty cockpit rather than white-screening.
export const PolicyFleetHealthSchema = z.object({
  stall_minutes: z.number().default(20),
  loop_threshold: z.number().default(4),
  agents: z.array(z.object({
    agent_id: z.string().default(""),
    agent_name: z.string().default(""),
    task_count: z.number().default(0),
    failed_count: z.number().default(0),
    avg_run_seconds: z.number().default(0),
    p95_run_seconds: z.number().default(0),
    avg_queue_seconds: z.number().default(0),
  }).loose()).default([]),
  stalled: z.array(z.object({
    task_id: z.string().default(""),
    agent_id: z.string().default(""),
    agent_name: z.string().default(""),
    issue_id: z.string().default(""),
    started_at: z.string().nullable().default(null),
    attempt: z.number().default(1),
  }).loose()).default([]),
  recent_failures: z.array(z.object({
    task_id: z.string().default(""),
    agent_id: z.string().default(""),
    agent_name: z.string().default(""),
    issue_id: z.string().default(""),
    failure_reason: z.string().default(""),
    error: z.string().default(""),
    started_at: z.string().nullable().default(null),
    completed_at: z.string().nullable().default(null),
    attempt: z.number().default(1),
  }).loose()).default([]),
  looping: z.array(z.object({
    issue_id: z.string().default(""),
    task_count: z.number().default(0),
    last_task_at: z.string().nullable().default(null),
  }).loose()).default([]),
}).loose();

export const EMPTY_POLICY_FLEET_HEALTH = {
  stall_minutes: 20,
  loop_threshold: 4,
  agents: [],
  stalled: [],
  recent_failures: [],
  looping: [],
};

// QA evidence — the durable run_qa verdict for an issue. Lenient: the command
// table is agent-authored, so every field defaults and the whole `result` block
// is nullable. The endpoint returns null when no evidence exists yet, so the
// client parses against `.nullable()` with a null fallback (see getQAEvidence).
export const QAResultSchema = z.object({
  verdict: z.string().default("unknown"),
  summary: z.string().default(""),
  commands: z.array(z.object({
    cmd: z.string().default(""),
    // Human-readable QA evidence. Optional for compatibility with historical
    // command-only verdicts; the presentation layer supplies safe fallbacks.
    title: z.string().default(""),
    expected: z.string().default(""),
    observed: z.string().default(""),
    baseline_exit: z.number().nullable().default(null),
    // Nullable + symmetric with baseline_exit: a command that ran on only ONE
    // side reports null for the other (real agents emit branch_exit:null for a
    // baseline-only command). A non-nullable number here rejected the whole
    // verdict — found in the demo run, SD-320.
    branch_exit: z.number().nullable().default(null),
    kind: z.string().default("pass"),
    // Short failure reason (stderr tail / assertion message) — optional, only
    // older/non-conforming agents omit it; empty string degrades cleanly.
    error: z.string().default(""),
  }).loose()).default([]),
  screenshots: z.array(z.string()).default([]),
  // Optional design-verification result (design-aware QA): present only when the
  // issue implements a Figma design. Advisory — a `skipped` verdict never fails
  // the gate. The agent emits this freehand, so a wrong-typed design block must
  // degrade to null WITHOUT rejecting the whole qa-result (which would hide the
  // verdict, commands, and screenshots that parsed fine) — hence `.catch(null)`.
  design: z.object({
    verdict: z.string().default("skipped"),
    reference_node: z.string().default(""),
    mismatches: z.array(z.object({
      kind: z.string().default("other"),
      selector: z.string().default(""),
      expected: z.string().default(""),
      actual: z.string().default(""),
    }).loose()).default([]),
    // Diff-scoped design-system lint findings (design_lint): values/components
    // the CHANGE introduced that erode the design system.
    lint: z.array(z.object({
      kind: z.string().default("other"),
      where: z.string().default(""),
      issue: z.string().default(""),
      severity: z.string().default("warn"),
    }).loose()).default([]),
  }).loose().nullable().catch(null).default(null),
}).loose();

// Freshest verdict per in_review issue for the cockpit rows (GET /api/qa/verdicts).
export const QAVerdictsResponseSchema = z.object({
  verdicts: z
    .record(
      z.string(),
      z.object({
        verdict: z.string().default(""),
        source: z.string().default(""),
        summary: z.string().default(""),
        captured_at: z.string().default(""),
        // Phase 3: the server-computed reconciled state, batch-computed per
        // in_review issue ("" = old server / not reconciled — the client
        // falls back to its label-derived state), and who fired the gate.
        reconciled_state: z.string().default(""),
        triggered_by: z.string().default(""),
      }).loose(),
    )
    .default({}),
}).loose();

export type QAVerdictsResponse = z.infer<typeof QAVerdictsResponseSchema>;

export const EMPTY_QA_VERDICTS: QAVerdictsResponse = { verdicts: {} };

export const QAEvidenceSchema = z.object({
  id: z.string().default(""),
  issue_id: z.string().default(""),
  baseline_ref: z.string().default(""),
  branch_sha: z.string().default(""),
  verdict: z.string().default(""),
  source: z.string().default(""),
  summary: z.string().default(""),
  result: QAResultSchema.nullable().default(null),
  captured_at: z.string().default(""),
  // The server-computed single source of truth (Phase 2 of the QA-stage
  // review — service.ReconcileQAState on the backend): "running" | "pass" |
  // "fail" | "blocked" | "stale" | "never_ran" | "pass_with_failing_cases".
  // A plain string, not a strict enum, on purpose — an unrecognized value (a
  // future state, or "" from a server that predates this field) must degrade
  // gracefully rather than reject the whole evidence row. "" is the explicit
  // "not provided" signal consumers (qa-lens, qa-lane) fall back to their own
  // label-derived computation on.
  reconciled_state: z.string().default(""),
  // Run identity (Phase 3, migration 157) — all default so an old server
  // (or a legacy row) degrades to "" instead of rejecting the response.
  commit_sha: z.string().default(""),
  triggered_by: z.string().default(""),
  started_at: z.string().default(""),
  finished_at: z.string().default(""),
}).loose();

// Review verdict — the latest run_review code-review result for an issue
// (GET /api/issues/:id/review-verdict). Lenient like its QA siblings: the
// findings come from an agent-authored ```review-result``` block, so every
// field defaults and an unrecognized severity/verdict degrades instead of
// rejecting the payload. verdict "none" (the endpoint's explicit "no review
// yet" answer) doubles as the parse fallback — a malformed response renders
// the same empty state as a never-reviewed issue.
export const ReviewFindingSchema = z.object({
  file: z.string().default(""),
  line: z.number().nullable().default(null),
  severity: z.string().default("minor"),
  title: z.string().default(""),
  detail: z.string().default(""),
}).loose();

export const ReviewVerdictSchema = z.object({
  verdict: z.string().default("none"),
  summary: z.string().default(""),
  commit_sha: z.string().default(""),
  files_reviewed: z.number().default(0),
  findings: z.array(ReviewFindingSchema).default([]),
  comment_id: z.string().default(""),
  reviewed_at: z.string().default(""),
  reviewer_agent_id: z.string().default(""),
}).loose();

export const EMPTY_REVIEW_VERDICT = {
  verdict: "none",
  summary: "",
  commit_sha: "",
  files_reviewed: 0,
  findings: [],
  comment_id: "",
  reviewed_at: "",
  reviewer_agent_id: "",
};

// POST /api/issues/:id/review-decision — request_changes also returns the
// versioned correction DAG identity. A lenient schema keeps approve responses
// compatible while preserving the durable handoff when present.
export const ReviewDecisionResponseSchema = z.object({
  action: z.string().default(""),
  merged_dispatch: z.boolean().default(false),
  status: z.string().default(""),
  dispatched: z.boolean().default(false),
  plan_version: z.number().default(0),
  revision_id: z.string().default(""),
  correction_step_id: z.string().default(""),
}).loose();

export const EMPTY_REVIEW_DECISION = {
  action: "",
  merged_dispatch: false,
  status: "",
  dispatched: false,
  plan_version: 0,
  revision_id: "",
  correction_step_id: "",
};

// A test case's run history (GET /api/test-cases/:id/runs) — Phase 3 run
// identity. Lenient like its QA siblings: every field defaults, a malformed
// entry degrades instead of rejecting the whole history.
export const TestCaseRunsResponseSchema = z.object({
  runs: z.array(z.object({
    id: z.string().default(""),
    status: z.string().default(""),
    run_source: z.string().default(""),
    created_at: z.string().default(""),
    output: z.string().default(""),
    trace_path: z.string().default(""),
    commit_sha: z.string().default(""),
    session_id: z.string().default(""),
    started_at: z.string().default(""),
    finished_at: z.string().default(""),
  }).loose()).default([]),
}).loose();

export type TestCaseRunsParsed = z.infer<typeof TestCaseRunsResponseSchema>;

export const EMPTY_TEST_CASE_RUNS: TestCaseRunsParsed = { runs: [] };

// Deploy events — the durable Tier-1 (QA-box git-sync) deploy signal (deploy
// P0, docs/deploy-stage-research.md §3.3). Lenient like QAEvidenceSchema: an
// agent/server-authored record, every field defaults so a partial or
// malformed row degrades instead of rejecting the whole response.
export const DeployEventSchema = z.object({
  id: z.string().default(""),
  issue_id: z.string().default(""),
  ref: z.string().default(""),
  target: z.string().default(""),
  status: z.string().default(""),
  summary: z.string().default(""),
  captured_at: z.string().default(""),
}).loose();

export type DeployEvent = z.infer<typeof DeployEventSchema>;

// GET /api/issues/:id/deploy-events — the freshest event plus a short recent
// history. latest is null for a never-deployed issue (a normal response, not
// an error — mirrors QAEvidenceSchema.nullable()'s null-fallback contract).
export const IssueDeployEventsResponseSchema = z.object({
  latest: DeployEventSchema.nullable().default(null),
  recent: z.array(DeployEventSchema).default([]),
}).loose();

export type IssueDeployEventsResponse = z.infer<typeof IssueDeployEventsResponseSchema>;

export const EMPTY_DEPLOY_EVENTS: IssueDeployEventsResponse = { latest: null, recent: [] };

// Deploy environments — project.settings.deploy_environments (MCP-P1,
// docs/deploy-mcp-integration.md §3). Human-authored JSONB routing config:
// each entry names an environment (key) and its non-secret machine target
// (GitLab project/ref for kind="gitlab_pipeline", or a Tier-2 command). The
// GitLab PAT itself never appears here — it lives sealed in git_credential
// and is injected server-side at claim time.
const EMPTY_DEPLOY_ENVIRONMENT_TARGET = {
  kind: "",
  project_path: "",
  ref: "",
  environment: "",
  command: "",
};

export const DeployEnvironmentTargetSchema = z
  .object({
    kind: z.string().default(""),
    project_path: z.string().default(""),
    ref: z.string().default(""),
    environment: z.string().default(""),
    command: z.string().default(""),
  })
  .loose();

export const DeployEnvironmentSchema = z
  .object({
    key: z.string().default(""),
    label: z.string().default(""),
    kind: z.string().default(""),
    requires_human: z.boolean().default(false),
    target: DeployEnvironmentTargetSchema.default(EMPTY_DEPLOY_ENVIRONMENT_TARGET).catch(
      EMPTY_DEPLOY_ENVIRONMENT_TARGET,
    ),
  })
  .loose();

export type DeployEnvironment = z.infer<typeof DeployEnvironmentSchema>;

// parseDeployEnvironments reads deploy_environments out of an untyped project
// settings blob defensively, mirroring the server's parser: a malformed blob
// or non-array value yields [], a malformed ENTRY is skipped (one bad entry
// must not hide its siblings), and keyless entries are dropped (the key is
// the routing handle the Deploy button and the slice-action scope address).
export function parseDeployEnvironments(settings: unknown): DeployEnvironment[] {
  if (!settings || typeof settings !== "object") return [];
  const raw = (settings as { deploy_environments?: unknown }).deploy_environments;
  if (!Array.isArray(raw)) return [];
  const out: DeployEnvironment[] = [];
  for (const item of raw) {
    const parsed = DeployEnvironmentSchema.safeParse(item);
    if (parsed.success && parsed.data.key.trim() !== "") out.push(parsed.data);
  }
  return out;
}

// deployEnvironmentRequiresHuman mirrors the server-side gate (which is the
// real enforcement — this is display-only): the explicit flag, or a
// production-named key as defense in depth.
export function deployEnvironmentRequiresHuman(env: DeployEnvironment): boolean {
  if (env.requires_human) return true;
  const key = env.key.trim().toLowerCase();
  return key === "production" || key === "prod";
}

// QA test cases — agent- or human-authored, with the latest run's verdict.
// Lenient: status/kind/source are plain strings (enum drift downgrades), and a
// degraded response yields an empty list rather than white-screening the panel.
export const TestCaseSchema = z.object({
  id: z.string().default(""),
  issue_id: z.string().default(""),
  title: z.string().default(""),
  steps: z.string().default(""),
  expected: z.string().default(""),
  kind: z.string().default("manual"),
  source: z.string().default("human"),
  author_type: z.string().default(""),
  category: z.string().default("positive"),
  script: z.string().optional(),
  // Phase-2/3 metadata (migrations 155/156). Defaults keep an OLD server's
  // response (fields absent) parsing as legacy rows: priority p2, modality
  // unspecified, no criterion traceability.
  preconditions: z.string().default(""),
  priority: z.string().default("p2"),
  modality: z.string().default(""),
  criterion_ref: z.string().default(""),
  created_at: z.string().default(""),
  latest_run: z.object({
    id: z.string().default(""),
    status: z.string().default(""),
    run_source: z.string().default(""),
    created_at: z.string().default(""),
    output: z.string().default(""),
    // Non-empty when this run captured a Playwright trace; the panel gates its
    // "View trace" affordance on it and passes `id` to the launch endpoint.
    trace_path: z.string().default(""),
  }).loose().nullable().default(null),
}).loose();

// GET /api/qa/trace/:runId — launches `playwright show-trace` on the daemon that
// holds the run's trace and returns a same-origin reverse-proxy URL to iframe.
export const LaunchTraceResponseSchema = z.object({
  trace_url: z.string().default(""),
}).loose();

export const EMPTY_LAUNCH_TRACE = { trace_url: "" };

export const ListTestCasesResponseSchema = z.object({
  test_cases: z.array(TestCaseSchema).default([]),
}).loose();

export const EMPTY_LIST_TEST_CASES = { test_cases: [] };

// A single test case row — POST /api/projects/:id/test-cases returns one when a
// standing base case is authored. Lenient like the list schema; the fallback is
// an inert empty case so a degraded create response can't crash the caller.
export const EMPTY_TEST_CASE = {
  id: "",
  issue_id: "",
  title: "",
  steps: "",
  expected: "",
  kind: "automated",
  source: "human",
  author_type: "",
  category: "positive",
  preconditions: "",
  priority: "p2",
  modality: "",
  criterion_ref: "",
  created_at: "",
  latest_run: null,
};

// POST /api/projects/:id/base-suite/build — fires the QA-lead authoring run and
// returns the tracking issue it opened (202 Accepted). Only status/issue_id are
// read; the toast confirms the run was queued.
export const BuildBaseSuiteResponseSchema = z.object({
  status: z.string().default(""),
  issue_id: z.string().default(""),
}).loose();

export const EMPTY_BUILD_BASE_SUITE = { status: "", issue_id: "" };

// Where the Live-testing bay reaches a CDP browser for an issue — self-host
// (direct daemon_url) or cloud (a same-origin /browser/proxy/<token> base the
// backend reverse-proxies to the daemon). Unlike the editor response this
// never depends on a worktree existing, so the bay stays up for QA-target
// browsing even when no dev task ever ran.
export const IssueBrowserResponseSchema = z
  .object({
    mode: z.string().default(""),
    daemon_url: z.string().default(""),
    browser_url: z.string().default(""),
  })
  .loose();

export const EMPTY_ISSUE_BROWSER = { mode: "", daemon_url: "", browser_url: "" };

// Where the web folder picker walks ONE daemon's filesystem — self-host (a
// direct http://127.0.0.1:<port> base) or cloud (a same-origin
// /browser/proxy/<token> base). mode is server-driven and read as a plain
// string: an unknown future value must render the picker's generic
// "unavailable" state, never crash. mode="offline" means the machine is
// registered but not running, and daemon_url is blank.
export const DaemonBrowseTargetSchema = z
  .object({
    mode: z.string().default(""),
    daemon_url: z.string().default(""),
  })
  .loose();

export const EMPTY_DAEMON_BROWSE_TARGET = { mode: "", daemon_url: "" };

// One directory listing from the daemon's folder picker (GET /editor/fs/list).
// entries defaults to [] so a drifted/older daemon renders "no subfolders"
// instead of spinning. parent is blank at a browsable-root boundary — the UI
// uses that to hide "up one level" rather than walking out of the allowed roots.
export const FsListEntrySchema = z
  .object({
    name: z.string().default(""),
    path: z.string().default(""),
    is_dir: z.boolean().default(false),
    is_git_repo: z.boolean().default(false),
    is_symlink: z.boolean().default(false),
  })
  .loose();

export const FsListResponseSchema = z
  .object({
    path: z.string().default(""),
    parent: z.string().default(""),
    home: z.string().default(""),
    entries: z.array(FsListEntrySchema).default([]),
    truncated: z.boolean().default(false),
  })
  .loose();

export type FsListParsed = z.infer<typeof FsListResponseSchema>;

// Typed so `entries` keeps its element type through parseWithFallback — an
// untyped [] infers as never[] and poisons every consumer of a fallen-back
// listing.
export const EMPTY_FS_LIST: FsListParsed = {
  path: "",
  parent: "",
  home: "",
  entries: [],
  truncated: false,
};

// The issue's resolved QA preview target — a deployed connected box (e.g. a
// per-developer or per-project QA box) or the project's configured
// qa_smoke_url. "" means nothing resolves; the frontend shows its own empty
// state rather than treating that as an error.
export const IssueQAPreviewURLResponseSchema = z.object({
  url: z.string().default(""),
  // Server-checked (X-Frame-Options / CSP frame-ancestors) so the frontend
  // never attempts an iframe embed that would render silently blank.
  embeddable: z.boolean().default(false),
}).loose();

export const EMPTY_ISSUE_QA_PREVIEW_URL = { url: "", embeddable: false };

// QA speed / regression metrics (the QA Metrics page). Every section defaults
// to empty so a partial/older backend payload still renders.
export const QAMetricsResponseSchema = z
  .object({
    totals: z
      .object({
        total: z.number().default(0),
        passed: z.number().default(0),
        failed: z.number().default(0),
        skipped: z.number().default(0),
      })
      .loose()
      .default({ total: 0, passed: 0, failed: 0, skipped: 0 }),
    by_day: z
      .array(
        z
          .object({
            day: z.string().default(""),
            total: z.number().default(0),
            failed: z.number().default(0),
          })
          .loose(),
      )
      .default([]),
    agents: z
      .array(
        z
          .object({
            agent: z.string().default(""),
            runs: z.number().default(0),
            avg_sec: z.number().default(0),
            min_sec: z.number().default(0),
            max_sec: z.number().default(0),
          })
          .loose(),
      )
      .default([]),
    coverage: z
      .object({
        automated: z.number().default(0),
        scripted: z.number().default(0),
      })
      .loose()
      .default({ automated: 0, scripted: 0 }),
    recent_runs: z
      .array(
        z
          .object({
            id: z.string().default(""),
            status: z.string().default(""),
            created_at: z.string().default(""),
            run_source: z.string().default(""),
            case_title: z.string().default(""),
            issue_number: z.number().nullable().default(null),
          })
          .loose(),
      )
      .default([]),
  })
  .loose();

export type QAMetricsResponse = z.infer<typeof QAMetricsResponseSchema>;

export const EMPTY_QA_METRICS: QAMetricsResponse = {
  totals: { total: 0, passed: 0, failed: 0, skipped: 0 },
  by_day: [],
  agents: [],
  coverage: { automated: 0, scripted: 0 },
  recent_runs: [],
};

// Sprint QA-readiness — per-active-sprint mergeable rollup + issue rows.
export const SprintReadinessResponseSchema = z
  .object({
    sprints: z
      .array(
        z
          .object({
            sprint_id: z.string().default(""),
            name: z.string().default(""),
            branch: z.string().default(""),
            project_id: z.string().default(""),
            project_title: z.string().default(""),
            total: z.number().default(0),
            passed: z.number().default(0),
            failed: z.number().default(0),
            pending: z.number().default(0),
            no_qa: z.number().default(0),
            mergeable: z.boolean().default(false),
            regression: z
              .object({
                status: z.string().default(""),
                source: z.string().default(""),
                triggered_at: z.string().default(""),
                completed_at: z.string().default(""),
                reason: z.string().default(""),
                run_issue_id: z.string().default(""),
              })
              .loose()
              .nullable()
              .default(null),
            issues: z
              .array(
                z
                  .object({
                    id: z.string().default(""),
                    number: z.number().default(0),
                    title: z.string().default(""),
                    status: z.string().default(""),
                    qa_pass: z.boolean().default(false),
                    qa_fail: z.boolean().default(false),
                    runs_pass: z.number().default(0),
                    runs_fail: z.number().default(0),
                    runs_total: z.number().default(0),
                    verdict: z.string().default("pending"),
                  })
                  .loose(),
              )
              .default([]),
          })
          .loose(),
      )
      .default([]),
  })
  .loose();

export type SprintReadinessResponse = z.infer<typeof SprintReadinessResponseSchema>;

export const EMPTY_SPRINT_READINESS: SprintReadinessResponse = { sprints: [] };

// ---------------------------------------------------------------------------
// Billing schemas (cloud-billing proxy surface)
//
// All billing JSON we receive comes from agora-cloud verbatim — we proxy
// the bytes without re-shaping. These schemas use `loose()` so a future
// non-breaking field addition on the cloud side doesn't crash us; required
// fields are still strictly enforced. EMPTY_* constants supply the
// fallback parseWithFallback uses when the upstream response is malformed
// or unparseable.

export const BillingBalanceSchema = z.object({
  owner_id: z.string(),
  balance_micro: z.number(),
  balance_credit: z.number(),
  updated_at: z.string(),
}).loose();

export const EMPTY_BILLING_BALANCE: BillingBalance = {
  owner_id: "",
  balance_micro: 0,
  balance_credit: 0,
  updated_at: "",
};

// `tx_type` and `source` are kept as plain strings here; the cloud doc
// enumerates the canonical values but the frontend display tolerates
// unknown ones gracefully. Strict enums would crash the page on a future
// addition (e.g. a new `topup` source kind).
export const BillingTransactionSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  idempotency_key: z.string().default(""),
  tx_type: z.string(),
  source: z.string(),
  amount_micro: z.number(),
  balance_after: z.number(),
  reference_id: z.string().default(""),
  description: z.string().default(""),
  metadata: z.record(z.string(), z.unknown()).default({}),
  created_at: z.string(),
}).loose();

export const BillingTransactionsPageSchema = z.object({
  items: z.array(BillingTransactionSchema).default([]),
  total: z.number().default(0),
  page: z.number().default(1),
  page_size: z.number().default(20),
}).loose();

export const EMPTY_BILLING_TRANSACTIONS_PAGE: BillingTransactionsPage = {
  items: [],
  total: 0,
  page: 1,
  page_size: 20,
};

export const BillingBatchSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  source_tx_id: z.string().default(""),
  source_type: z.string(),
  total_micro: z.number(),
  remaining_micro: z.number(),
  // Cloud either omits the key (never expires) or sends a string
  // timestamp. Null is also tolerated since some serializers emit
  // explicit nulls for absent timestamps.
  expires_at: z.string().nullable().optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const BillingBatchesPageSchema = z.object({
  items: z.array(BillingBatchSchema).default([]),
  total: z.number().default(0),
  page: z.number().default(1),
  page_size: z.number().default(20),
}).loose();

export const EMPTY_BILLING_BATCHES_PAGE: BillingBatchesPage = {
  items: [],
  total: 0,
  page: 1,
  page_size: 20,
};

export const BillingTopupSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  amount_cents: z.number(),
  currency: z.string().default("usd"),
  credits: z.number(),
  bonus_credits: z.number().default(0),
  status: z.string(),
  tier_id: z.string().default(""),
  stripe_checkout_id: z.string().default(""),
  // Only set after status reaches `credited` — leave optional rather
  // than coerce to "" so a UI can branch on existence.
  purchase_batch_id: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const BillingTopupsPageSchema = z.object({
  items: z.array(BillingTopupSchema).default([]),
  total: z.number().default(0),
  page: z.number().default(1),
  page_size: z.number().default(20),
}).loose();

export const EMPTY_BILLING_TOPUPS_PAGE: BillingTopupsPage = {
  items: [],
  total: 0,
  page: 1,
  page_size: 20,
};

export const BillingPriceTierSchema = z.object({
  id: z.string(),
  // Cloud doc says display_name falls back to id; tolerate empty too.
  display_name: z.string().default(""),
  amount_cents: z.number(),
  credits: z.number(),
  bonus_credits: z.number().optional(),
  bonus_expires_in: z.string().optional(),
}).loose();

export const BillingPriceTierListSchema = z.array(BillingPriceTierSchema);

export const EMPTY_BILLING_PRICE_TIER_LIST: BillingPriceTier[] = [];

export const CreateBillingCheckoutSessionResponseSchema = z.object({
  order_id: z.string(),
  session_id: z.string(),
  url: z.string(),
}).loose();

export const EMPTY_CREATE_BILLING_CHECKOUT_SESSION_RESPONSE: CreateBillingCheckoutSessionResponse = {
  order_id: "",
  session_id: "",
  url: "",
};

export const BillingCheckoutSessionStatusSchema = z.object({
  order_id: z.string(),
  status: z.string(),
  amount_cents: z.number(),
  credits: z.number(),
  bonus_credits: z.number().default(0),
  currency: z.string().default("usd"),
  tier_id: z.string().default(""),
}).loose();

export const EMPTY_BILLING_CHECKOUT_SESSION_STATUS: BillingCheckoutSessionStatus = {
  order_id: "",
  status: "pending",
  amount_cents: 0,
  credits: 0,
  bonus_credits: 0,
  currency: "usd",
  tier_id: "",
};

export const CreateBillingPortalSessionResponseSchema = z.object({
  url: z.string(),
}).loose();

export const EMPTY_CREATE_BILLING_PORTAL_SESSION_RESPONSE: CreateBillingPortalSessionResponse = {
  url: "",
};

// Workspace Figma credential status (`GET /api/workspaces/{id}/figma-credential`).
// Everything beyond `configured` defaults so an older server shape degrades to
// a "not configured" style rendering instead of knocking the settings section
// into an error. Check `configured === true` explicitly downstream.
export const FigmaCredentialStatusSchema = z.object({
  configured: z.boolean().default(false),
  label: z.string().default(""),
  token_last4: z.string().default(""),
  token_kind: z.string().default(""),
  expires_at: z.string().default(""),
  expiring_soon: z.boolean().default(false),
  seat_probe: z.string().default(""),
  probe_status: z.string().default(""),
  probed_at: z.string().default(""),
}).loose();

export const EMPTY_FIGMA_CREDENTIAL_STATUS: FigmaCredentialStatus = {
  configured: false,
  label: "",
  token_last4: "",
  token_kind: "",
  expires_at: "",
  expiring_soon: false,
  seat_probe: "",
  probe_status: "",
  probed_at: "",
};

// Remote-MCP credential status (sealed auth for a remote http/sse MCP server).
// Every field is defaulted so a drifted backend row downgrades to a benign
// shape rather than throwing into the panel. Token material is never present —
// has_secret + last4 only.
export const McpCredentialStatusSchema = z.object({
  id: z.string().default(""),
  server_name: z.string().default(""),
  has_secret: z.boolean().default(false),
  last4: z.string().default(""),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

// The list endpoint returns an array; a non-array or malformed body downgrades
// to an empty list (the panel shows no sealed-auth badges rather than crashing).
export const McpCredentialListSchema = z.array(McpCredentialStatusSchema).catch([]);

export const EMPTY_MCP_CREDENTIAL_LIST: McpCredentialStatus[] = [];

export const EMPTY_MCP_CREDENTIAL_STATUS: McpCredentialStatus = {
  id: "",
  server_name: "",
  has_secret: false,
  last4: "",
  created_at: "",
  updated_at: "",
};

// Release integrations (release-hub Thread B). Every field is defaulted so a
// drifted backend row (missing/wrong-typed field) downgrades to a benign shape
// instead of throwing into the settings UI. The sealed URL is never present —
// has_secret is the only secret signal.
export const ReleaseIntegrationSchema = z.object({
  id: z.string().default(""),
  kind: z.string().default("webhook"),
  config: z
    .object({
      name: z.string().optional(),
      channel_hint: z.string().optional(),
      owner: z.string().optional(),
      repo: z.string().optional(),
      project_path: z.string().optional(),
      org: z.string().optional(),
      project: z.string().optional(),
    })
    .loose()
    .default({}),
  events: z.array(z.string()).default([]),
  enabled: z.boolean().default(false),
  probe_status: z.string().default(""),
  has_secret: z.boolean().default(false),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const ReleaseIntegrationListSchema = z.array(ReleaseIntegrationSchema);

export const EMPTY_RELEASE_INTEGRATIONS: ReleaseIntegration[] = [];

// Per-project pipeline config (GET /api/projects/{id}/config). Lenient — a
// forward-compat key/kind/source we don't recognise still renders (enum drift
// downgrades, never white-screens the settings section).
export const ProjectConfigEntrySchema = z.object({
  key: z.string(),
  kind: z.string().default("bool"),
  category: z.string().default(""),
  label: z.string().default(""),
  description: z.string().default(""),
  value: z.string().default(""),
  source: z.string().default("default"),
  overridden_by_project: z.boolean().default(false),
}).loose();

export const ProjectConfigListSchema = z.object({
  configs: z.array(ProjectConfigEntrySchema).default([]),
});

export type ProjectConfigEntry = z.infer<typeof ProjectConfigEntrySchema>;

export const EMPTY_PROJECT_CONFIG: { configs: ProjectConfigEntry[] } = { configs: [] };

// Per-agent Telegram bots. Every field is defaulted: this response is consumed
// by an installed desktop build that will outlive the server it was built
// against, and a settings panel that white-screens on a drifted field is worse
// than one showing a stale-but-benign row (CLAUDE.md → API Response
// Compatibility).
export const TelegramInstallationSchema = z.object({
  agent_id: z.string().default(""),
  bot_username: z.string().default(""),
  bot_user_id: z.string().default(""),
  chat_id: z.string().optional(),
  status: z.string().default("active"),
  installed_at: z.string().optional(),
  // Unknown policy values must not crash the panel — the UI renders a generic
  // fallback and the operator can still correct it.
  access_policy: z.string().default("closed"),
  // Ids arrive as strings so a 64-bit chat id survives JSON; a malformed list
  // downgrades to empty rather than dropping the whole installation.
  allowed_user_ids: z.array(z.string()).catch([]).optional(),
  allowed_chat_ids: z.array(z.string()).catch([]).optional(),
}).loose();

export const ListTelegramInstallationsSchema = z.object({
  installations: z.array(TelegramInstallationSchema).catch([]),
  // Defaults to FALSE, not true: claiming the deployment is configured when
  // the field is missing would show an install form that cannot succeed.
  configured: z.boolean().default(false),
}).loose();

export const EMPTY_TELEGRAM_INSTALLATIONS: {
  installations: TelegramInstallation[];
  configured: boolean;
} = { installations: [], configured: false };

export const EMPTY_TELEGRAM_INSTALLATION: TelegramInstallation = {
  agent_id: "",
  bot_username: "",
  bot_user_id: "",
  status: "active",
  access_policy: "closed",
};

export const TelegramBindLinkSchema = z.object({
  group_url: z.string().default(""),
  bot_username: z.string().default(""),
  expires_at: z.string().default(""),
}).loose();

export const EMPTY_TELEGRAM_BIND_LINK: TelegramBindLinkResponse = {
  group_url: "",
  bot_username: "",
  expires_at: "",
};

export const AutopilotTelegramDestinationSchema = z.object({
  // Defaults to FALSE: a server that predates this endpoint, or a drifted
  // response, must read as "nothing will be sent" rather than promising a
  // delivery the dialog cannot back up.
  delivers: z.boolean().default(false),
  via: z.string().optional(),
  bot_username: z.string().optional(),
  chat_id: z.string().optional(),
  from_project_config: z.boolean().default(false),
}).loose();

export const EMPTY_AUTOPILOT_TELEGRAM_DESTINATION: AutopilotTelegramDestination = {
  delivers: false,
  from_project_config: false,
};
