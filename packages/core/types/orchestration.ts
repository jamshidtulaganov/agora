export type OrchestrationRunStatus =
  | "draft"
  | "running"
  | "waiting_approval"
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
  task_id?: string;
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

export interface OrchestrationRun {
  id: string;
  issue_id: string;
  status: OrchestrationRunStatus;
  /** @deprecated Use progression_policy. */
  mode: "auto" | "manual";
  execution_strategy: ExecutionStrategy;
  progression_policy: ProgressionPolicy;
  policy: Record<string, unknown>;
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
