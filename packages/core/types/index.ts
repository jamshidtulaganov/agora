export type { Issue, IssueStatus, IssuePriority, IssueAssigneeType, IssueMetadata, IssueMetadataValue, IssueReaction } from "./issue";
export type {
  Agent,
  AgentStatus,
  AgentRuntimeMode,
  AgentVisibility,
  AgentTask,
  AgentRunMode,
  AgentActivityBucket,
  AgentRunCount,
  TaskFailureReason,
  AgentRuntime,
  RuntimeDevice,
  CreateAgentRequest,
  AgentTemplate,
  AgentTemplateSummary,
  AgentTemplateSkillRef,
  CreateAgentFromTemplateRequest,
  CreateAgentFromTemplateResponse,
  CreateAgentFromTemplateFailure,
  UpdateAgentRequest,
  AgentEnvResponse,
  UpdateAgentEnvRequest,
  Skill,
  SkillSummary,
  AgentSkillSummary,
  SkillFile,
  CreateSkillRequest,
  UpdateSkillRequest,
  SetAgentSkillsRequest,
  RuntimeUsage,
  RuntimeHourlyActivity,
  RuntimeUsageByAgent,
  RuntimeUsageByHour,
  DashboardUsageDaily,
  DashboardUsageByAgent,
  DashboardAgentRunTime,
  DashboardRunTimeDaily,
  RuntimeUpdate,
  RuntimeUpdateStatus,
  RuntimeModel,
  RuntimeModelThinking,
  RuntimeModelThinkingLevel,
  RuntimeModelListRequest,
  RuntimeModelListStatus,
  RuntimeModelsResult,
  RuntimeLocalSkillStatus,
  RuntimeLocalSkillImportAction,
  RuntimeLocalSkillImportConflict,
  RuntimeLocalSkillSummary,
  RuntimeLocalSkillListRequest,
  CreateRuntimeLocalSkillImportRequest,
  RuntimeLocalSkillImportRequest,
  RuntimeLocalSkillsResult,
  RuntimeLocalSkillImportResult,
  IssueUsageSummary,
} from "./agent";
export type { Workspace, WorkspaceRepo, GitCredential, FigmaCredentialStatus, McpCredentialStatus, McpCredentialInput, WorkspaceMcpConfigResponse, ReleaseIntegration, ReleaseIntegrationInput, Member, MemberRole, User, MemberWithUser, ActorDirectoryEntry, Invitation, InvitationAuthInfo } from "./workspace";
export type { InboxItem, InboxSeverity, InboxItemType } from "./inbox";
export type { NotificationGroupKey, NotificationGroupValue, NotificationPreferences, NotificationPreferenceResponse } from "./notification-preference";
export type { Comment, CommentType, CommentAuthorType, CommentTriggerPreview, CommentTriggerPreviewAgent, CommentTriggerSource, Reaction } from "./comment";
export type { Label, CreateLabelRequest, UpdateLabelRequest, ListLabelsResponse, IssueLabelsResponse } from "./label";
export type {
  TimelineEntry,
  AssigneeFrequencyEntry,
} from "./activity";
export type { IssueSubscriber } from "./subscriber";
export type * from "./events";
export type * from "./api";
export type { Attachment } from "./attachment";
export { attachmentDownloadPath, attachmentIdFromDownloadURL, contentReferencesAttachment } from "./attachment-url";
export type {
  ChatSession,
  ChatMessage,
  ChatMessagesPage,
  ChatPendingTask,
  PendingChatTaskItem,
  PendingChatTasksResponse,
  SendChatMessageResponse,
  CancelledChatMessage,
  CancelTaskResponse,
} from "./chat";
export type { StorageAdapter } from "./storage";
export type {
  OrchestrationRun,
  OrchestrationRunStatus,
  ExecutionStrategy,
  ProgressionPolicy,
  ModelRoutingMode,
  OrchestrationStep,
  OrchestrationStepStatus,
  OrchestrationStage,
  OrchestrationEvent,
  OrchestrationHandoff,
  OrchestrationMessage,
  OrchestrationQuestion,
  OrchestrationPolicy,
  SquadRosterPolicyEntry,
  CreateOrchestrationRequest,
  CreateOrchestrationStepRequest,
  OrchestrationPlanRevision,
  EditOrchestrationRequest,
  RespondToOrchestrationStepRequest,
} from "./orchestration";
export type {
  Project,
  ProjectSettings,
  ProjectOrchestrationDefaults,
  ProjectPreviewTarget,
  ProjectExecutionStrategy,
  ProjectModelRoutingMode,
  ProjectStatus,
  ProjectPriority,
  CreateProjectRequest,
  UpdateProjectRequest,
  ListProjectsResponse,
  ProjectResource,
  ProjectResourceType,
  ProjectResourceRef,
  GithubRepoResourceRef,
  LocalDirectoryResourceRef,
  CreateProjectResourceRequest,
  UpdateProjectResourceRequest,
  ListProjectResourcesResponse,
} from "./project";
export type {
  Sprint,
  WorkspaceSprint,
  SprintStatus,
  CreateSprintRequest,
  UpdateSprintRequest,
  ListSprintsResponse,
  ListSprintIssuesResponse,
} from "./sprint";
export type { PinnedItem, PinnedItemType, CreatePinRequest, ReorderPinsRequest } from "./pin";
export type {
  GitHubInstallation,
  GitHubMergeableState,
  GitHubPullRequest,
  GitHubPullRequestChecksConclusion,
  GitHubPullRequestState,
  ListGitHubInstallationsResponse,
  GitHubConnectResponse,
} from "./github";

export type { ReviewFinding, ReviewVerdict, ReviewDecisionResponse } from "./review";

// Deterministic merge-readiness gate verdict (GET /api/issues/{id}/merge-readiness).
export interface MergeGateStatus {
  name: string; // "ci" | "qa" | "security" | "code-review"
  status: "pass" | "fail" | "pending";
}
export interface MergeReadiness {
  ready: boolean;
  tier: "trivial" | "light" | "full";
  gates: MergeGateStatus[];
  blocked?: string[];
  reviews: string[];
}

export type {
  LarkInstallation,
  ListLarkInstallationsResponse,
  BeginLarkInstallResponse,
  LarkInstallStatusResponse,
  RedeemLarkBindingTokenResponse,
} from "./lark";
export type {
  TelegramInstallation,
  ListTelegramInstallationsResponse,
  TelegramBindLinkResponse,
  SetTelegramAccessRequest,
  AutopilotTelegramDestination,
} from "./telegram";
export type {
  ExternalIdentityLink,
  ListExternalIdentityLinksResponse,
  TelegramLinkStartResponse,
} from "./external-identity";
export type {
  Autopilot,
  AutopilotStatus,
  AutopilotExecutionMode,
  AutopilotAssigneeType,
  AutopilotTrigger,
  AutopilotTriggerKind,
  AutopilotRun,
  AutopilotRunStatus,
  AutopilotRunSource,
  WebhookEventFilter,
  CreateAutopilotRequest,
  UpdateAutopilotRequest,
  CreateAutopilotTriggerRequest,
  UpdateAutopilotTriggerRequest,
  ListAutopilotsResponse,
  GetAutopilotResponse,
  ListAutopilotRunsResponse,
  WebhookDelivery,
  WebhookDeliveryStatus,
  WebhookSignatureStatus,
  ListWebhookDeliveriesResponse,
} from "./autopilot";
export type {
  Squad,
  SquadMember,
  SquadMemberType,
  SquadMemberPreview,
  SquadActivityLog,
  SquadActivityOutcome,
  CreateSquadRequest,
  UpdateSquadRequest,
  AddSquadMemberRequest,
  RemoveSquadMemberRequest,
  UpdateSquadMemberRoleRequest,
  CreateSquadActivityLogRequest,
  SquadMemberStatusValue,
  SquadActiveIssueBrief,
  SquadMemberStatus,
  SquadMemberStatusListResponse,
} from "./squad";
export type {
  BillingBalance,
  BillingTransaction,
  BillingTransactionsPage,
  BillingTxType,
  BillingTxSource,
  BillingBatch,
  BillingBatchesPage,
  BillingBatchSourceType,
  BillingTopup,
  BillingTopupsPage,
  BillingTopupStatus,
  BillingPriceTier,
  CreateBillingCheckoutSessionRequest,
  CreateBillingCheckoutSessionResponse,
  BillingCheckoutSessionStatus,
  CreateBillingPortalSessionResponse,
} from "./billing";
export type { WorkspaceLabs } from "./workspace-labs";
export type {
  PolicyFleetHealth,
  PolicyAgentSpeed,
  PolicyStalledTask,
  PolicyFailedTask,
  PolicyLoopingIssue,
} from "./policy";
export type { QACommand, QAResult, QAEvidence, QADesignResult, QADesignMismatch, QADesignLint } from "./qa-evidence";
export type {
  TestRunLite,
  TestCase,
  ListTestCasesResponse,
  CreateTestCaseRequest,
  UpdateTestCaseRequest,
  CreateTestRunRequest,
  BuildBaseSuiteResponse,
  TestRunHistoryItem,
  TestCaseRunsResponse,
} from "./test-case";
export type {
  IssueBrowserResponse,
  IssueQAPreviewURLResponse,
  DaemonBrowseTarget,
  FsListEntry,
  FsListResponse,
} from "./editor-live";
export type {
  ArtifactRepoRef,
  IssueArtifactSummary,
  IssueArtifactResponse,
  ArtifactChangedFile,
  ArtifactRepoChanges,
  ArtifactChangesResponse,
  ArtifactFileResponse,
  ArtifactPreviewResponse,
  ArtifactChecksResponse,
} from "./artifact";
