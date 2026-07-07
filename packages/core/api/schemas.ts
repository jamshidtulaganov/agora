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
  GroupedIssuesResponse,
  ListIssuesResponse,
  ListWebhookDeliveriesResponse,
  Squad,
  TimelineEntry,
  User,
  WebhookDelivery,
} from "../types";
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
  workspace_creation_disabled?: boolean;
  telegram_only?: boolean;
  remote_boxes_enabled?: boolean;
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
  remote_boxes_enabled: BooleanWithDefaultSchema(false).optional(),
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
// Remote Boxes (connected_box) — a developer's onboarded remote dev server.
// Lenient by design (`.loose()`, string status) so a future backend status
// value downgrades gracefully and never white-screens the runtimes page.
export const ConnectedBoxSchema = z.object({
  id: z.string(),
  workspace_id: z.string().default(""),
  owner_id: z.string().nullable().default(null),
  label: z.string().default(""),
  ssh_host: z.string().default(""),
  ssh_user: z.string().default(""),
  ssh_port: z.number().default(22),
  deploy_pubkey: z.string().default(""),
  daemon_id: z.string().nullable().default(null),
  status: z.string().default("pending"),
  last_error: z.string().default(""),
  repo_url: z.string().default(""),
  work_dir: z.string().default(""),
  last_branch: z.string().default(""),
  project_id: z.string().nullable().default(null),
  created_at: z.string().default(""),
}).loose();

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

export const ConnectedBoxListSchema = z.object({
  boxes: z.array(ConnectedBoxSchema).default([]),
}).loose();

// Fallback box for endpoints whose contract returns a single box (bind) or
// embeds one (sync result). All fields defaulted so a degraded response yields
// a benign empty box rather than a throw.
export const EMPTY_CONNECTED_BOX = {
  id: "",
  workspace_id: "",
  owner_id: null,
  label: "",
  ssh_host: "",
  ssh_user: "",
  ssh_port: 22,
  deploy_pubkey: "",
  daemon_id: null,
  status: "pending",
  last_error: "",
  repo_url: "",
  work_dir: "",
  last_branch: "",
  project_id: null,
  created_at: "",
} as const;

// Result of a box git-sync (branch sync / issue deploy-qa). Lenient + defaulted
// so a degraded response renders "deploy failed" rather than white-screening.
export const RemoteBoxSyncResultSchema = z.object({
  ok: z.boolean().default(false),
  branch: z.string().default(""),
  output: z.string().default(""),
  box: ConnectedBoxSchema,
}).loose();

// Result of a per-developer box provision (or a dry-run preview). Lenient +
// defaulted so a degraded response renders a benign empty preview rather than
// white-screening. box is nullable (null on a dry run, before any row exists).
export const ProvisionBoxResultSchema = z.object({
  handle: z.string().default(""),
  subdomain: z.string().default(""),
  work_dir: z.string().default(""),
  database: z.string().default(""),
  script: z.string().default(""),
  dry_run: z.boolean().default(false),
  ran: z.boolean().default(false),
  ok: z.boolean().default(false),
  output: z.string().default(""),
  box: ConnectedBoxSchema.nullable().default(null),
}).loose();

export const EMPTY_PROVISION_RESULT = {
  handle: "",
  subdomain: "",
  work_dir: "",
  database: "",
  script: "",
  dry_run: false,
  ran: false,
  ok: false,
  output: "",
  box: null,
} as const;

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
}).loose();

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

// GET /api/issues/:id/editor — resolves where (and how) to reach a live view
// of an issue's worktree. Two real shapes share one endpoint: self-host (a
// daemon_url + the agents that have a worktree, most-recent first) or cloud
// (a single proxied editor_url). `mode` is the discriminant; everything else
// is optional so an unrecognized/future mode degrades to "nothing to show"
// instead of crashing a consumer that only handles one mode.
export const EditorAgentSchema = z.object({
  agent_id: z.string().default(""),
  agent_name: z.string().default(""),
  work_dir: z.string().default(""),
  status: z.string().default(""),
}).loose();

export const GetIssueEditorResponseSchema = z.object({
  mode: z.string().default(""),
  daemon_url: z.string().default(""),
  user_id: z.string().default(""),
  agents: z.array(EditorAgentSchema).default([]),
  editor_url: z.string().default(""),
}).loose();

export const EMPTY_ISSUE_EDITOR = { mode: "", daemon_url: "", user_id: "", agents: [], editor_url: "" };

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
// Editor account integration (per-user PATs for the co-code editor env).
// Tokens are write-only; the API returns a masked tail per provider.
export const EditorTokensResponseSchema = z.object({
  tokens: z
    .array(
      z.object({
        provider: z.string().default(""),
        masked: z.string().default(""),
        workspace_id: z.string().default(""),
        updated_at: z.string().default(""),
      }).loose(),
    )
    .default([]),
}).loose();

export type EditorTokensResponse = z.infer<typeof EditorTokensResponseSchema>;

export const EMPTY_EDITOR_TOKENS: EditorTokensResponse = { tokens: [] };

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
