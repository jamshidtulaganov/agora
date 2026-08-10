export type OrchestrationRunStatus =
  | "draft"
  | "running"
  | "waiting_approval"
  | "waiting_input"
  | "blocked"
  | "completed"
  | "failed"
  | "cancelled";

export type ExecutionStrategy = "human" | "solo" | "squad" | "custom";
export type ProgressionPolicy = "automatic" | "gated" | "manual";

export type OrchestrationStepStatus =
  | "pending"
  | "queued"
  | "running"
  | "waiting_approval"
  | "waiting_input"
  | "blocked"
  | "completed"
  | "failed"
  | "cancelled"
  | "skipped";

export type OrchestrationStage = "plan" | "dev" | "qa" | "review" | "release";
export type OrchestrationStepKind = "task" | "integration";
export type OrchestrationCapability =
  | "coordination"
  | "implementation"
  | "backend"
  | "frontend"
  | "mobile"
  | "infrastructure"
  | "documentation"
  | "integration"
  | "qa"
  | "review"
  | "release";
export type OrchestrationIntegrationStatus =
  | "not_required"
  | "pending"
  | "complete"
  | "missing_heads"
  | "conflicts";

export interface OrchestrationStep {
  id: string;
  key: string;
  title: string;
  stage: OrchestrationStage;
  status: OrchestrationStepStatus;
  position: number;
  agent_id?: string;
  model?: string;
  /** Persisted execution pin; empty string means provider-default/no thinking. */
  thinking_level?: string;
  task_id?: string;
  question_id?: string;
  approval_required: boolean;
  approved_by?: string;
  attempt: number;
  max_attempts: number;
  instructions: string;
  output?: unknown;
  error?: string;
  depends_on_step_ids: string[];
  parent_step_id?: string;
  squad_id?: string;
  controller_agent_id?: string;
  worktree_branch?: string;
  base_sha?: string;
  head_sha?: string;
  merge_status: "not_checked" | "clean" | "conflicts" | "uncommitted" | "unavailable";
  conflict_files: string[];
  kind: OrchestrationStepKind;
  capability: OrchestrationCapability;
  integration_status: OrchestrationIntegrationStatus;
  integrated_head_shas: string[];
  missing_head_shas: string[];
}

export interface OrchestrationEvent {
  id: string;
  step_id?: string;
  kind: string;
  actor_type: "system" | "member" | "agent";
  actor_id?: string;
  details: Record<string, unknown>;
  created_at: string;
}

export interface OrchestrationQuestion {
  prompt: string;
  target: "human" | "controller" | "agent";
  target_id?: string;
  blocking: boolean;
}

export interface OrchestrationHandoff {
  schema_version: 1;
  stage: OrchestrationStage;
  outcome: "completed" | "waiting_input" | "blocked";
  verdict?: "pass" | "fail" | "not_applicable";
  summary: string;
  decisions: string[];
  contracts: string[];
  artifacts: Array<{ kind: string; ref: string; description?: string }>;
  verification: Array<{ name: string; status: "passed" | "failed" | "skipped"; details?: string }>;
  findings: string[];
  risks: string[];
  blockers: string[];
  next_actions: string[];
  question?: OrchestrationQuestion;
  legacy?: boolean;
}

export interface OrchestrationMessage {
  id: string;
  step_id: string;
  kind: "instruction" | "progress" | "question" | "answer" | "blocker" | "handoff" | "ack" | "escalation";
  actor_type: "system" | "member" | "agent";
  actor_id?: string;
  target_type: "run" | "step" | "human" | "controller" | "agent";
  target_id?: string;
  body: Record<string, unknown>;
  plan_version: number;
  correlation_id: string;
  causation_id?: string;
  reply_to_id?: string;
  expects_reply: boolean;
  acknowledged_at?: string;
  resolved_at?: string;
  created_at: string;
}

export interface SquadRosterPolicyEntry {
  agent_id: string;
  name: string;
  role: string;
  capability: OrchestrationCapability;
  /** Execution-pinned on each generated step when non-empty. */
  model?: string;
  /** Creation-time source copied into each generated step's execution pin. */
  thinking_level?: string;
  /** Global agent capacity, not additional slots inside this issue run. */
  max_concurrent_tasks: number;
  is_leader?: boolean;
}

export interface OrchestrationPolicy extends Record<string, unknown> {
  max_concurrency?: number;
  parallel_workers?: number;
  squad_id?: string;
  squad_roster?: SquadRosterPolicyEntry[];
}

export interface OrchestrationRun {
  id: string;
  issue_id: string;
  status: OrchestrationRunStatus;
  /** @deprecated Use progression_policy. */
  mode: "auto" | "manual";
  execution_strategy: ExecutionStrategy;
  progression_policy: ProgressionPolicy;
  policy: OrchestrationPolicy;
  owner_type: "agent" | "squad" | "member" | "unassigned";
  owner_id?: string;
  controller_agent_id?: string;
  base_git_states: Array<{ repo: string; head_sha: string }>;
  /** @deprecated Use execution_strategy. */
  execution_mode: "direct" | "squad" | "orchestrated";
  plan_version: number;
  revisions: OrchestrationPlanRevision[];
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  steps: OrchestrationStep[];
  events: OrchestrationEvent[];
  messages: OrchestrationMessage[];
}

export interface OrchestrationPlanRevision {
  id: string;
  version: number;
  actor_type: "system" | "member" | "agent";
  actor_id?: string;
  reason: string;
  patch: Record<string, unknown>;
  created_at: string;
}

export interface EditOrchestrationRequest {
  expected_version: number;
  reason: string;
  operation: "reroute" | "retire" | "add_child";
  step_id: string;
  agent_id?: string;
  model?: string;
  instructions?: string;
  child?: CreateOrchestrationStepRequest;
}

export interface RespondToOrchestrationStepRequest {
  question_id: string;
  message: string;
}

export interface CreateOrchestrationStepRequest {
  key: string;
  title: string;
  stage: OrchestrationStage;
  agent_id?: string;
  model?: string;
  instructions?: string;
  approval_required?: boolean;
  max_attempts?: number;
  depends_on_keys?: string[];
  parent_key?: string;
  squad_id?: string;
  kind?: OrchestrationStepKind;
  capability?: OrchestrationCapability;
  depends_on_step_ids?: string[];
}

export interface CreateOrchestrationRequest {
  execution_strategy?: ExecutionStrategy;
  progression_policy?: ProgressionPolicy;
  /** Explicit roster used by squad execution when issue ownership differs. */
  squad_id?: string;
  /** @deprecated Use progression_policy. */
  mode?: "auto" | "manual";
  auto_start?: boolean;
  policy?: Record<string, unknown>;
  steps?: CreateOrchestrationStepRequest[];
}
