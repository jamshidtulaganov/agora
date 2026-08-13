import type { TaskExecutionLevelPreference } from "./orchestration";

export type ProjectStatus = "planned" | "in_progress" | "paused" | "completed" | "cancelled";

export type ProjectPriority = "urgent" | "high" | "medium" | "low" | "none";

export type ProjectExecutionStrategy = "automatic" | "solo" | "squad" | "human";
export type ProjectModelRoutingMode = "pinned" | "cost" | "balanced" | "intelligence";

export interface ProjectOrchestrationDefaults {
  // Auto resolves scope and safety from the issue when each run is created.
  execution_level?: TaskExecutionLevelPreference;
  // "automatic" infers solo/squad/human from the issue owner at run creation.
  execution_strategy: ProjectExecutionStrategy;
  progression_policy: "automatic" | "gated" | "manual";
  max_concurrency: number;
  // When true, Build and run creates a draft proposal and waits for Start.
  review_plan_first: boolean;
  // Pinned preserves agent defaults. Adaptive modes resolve per-step pins.
  model_routing_mode?: ProjectModelRoutingMode;
}

export interface ProjectPreviewTarget {
  // Matches the repository/folder name shown in the artifact Preview selector.
  repo: string;
  // Optional relative app root for monorepos (for example apps/web).
  working_directory?: string;
  start_command?: string;
  test_command?: string;
}

// Per-project preferences blob (project.settings jsonb on the server, mirrors
// Workspace.settings). Always an object — the server normalizes empty/null to
// {}. Known keys are typed; the index signature keeps unknown server-side keys
// round-tripping through an update without being dropped.
export interface ProjectSettings {
  // Per-project "sprint mode". Absent means ON (the historical default from the
  // old localStorage stub); explicit false hides the Sprints UI for the project.
  sprint_mode?: boolean;
  // Defaults inherited by every new persisted orchestration run in this
  // project. A one-run issue customization can explicitly override them.
  orchestration?: ProjectOrchestrationDefaults;
  // QA smoke configuration consumed by the run_qa slice action. qa_smoke_cmd is
  // how to bring the app up (e.g. "pnpm dev"); qa_smoke_url is where it serves
  // (e.g. "http://localhost:5173"). When set, the QA gate uses these instead of
  // auto-detecting the smoke flow. Either may be set independently.
  qa_smoke_cmd?: string;
  qa_smoke_url?: string;
  // Test-command override used by project QA checks; when
  // unset the daemon auto-detects (package.json / Makefile / go.mod / composer).
  qa_test_cmd?: string;
  // Per-repository overrides for multi-root projects. The top-level QA command
  // fields remain the project fallback when no target matches the selected repo.
  preview_targets?: ProjectPreviewTarget[];
  // Documentation repository for the auto_docs slice action — a SEPARATE repo
  // from the code (e.g. a Docusaurus site). When set, auto_docs writes the docs
  // there and opens a PR against it.
  docs_repo?: string;
  // Agent UUID that runs auto_docs (the dedicated docs agent with the docs repo
  // access + sd-docs-author skill). When unset, auto_docs falls back to the
  // issue's assignee agent.
  docs_agent?: string;
  // RFC3339 timestamp of the last Bitrix sync for a Bitrix-linked project (set
  // by the per-project sync endpoint). Absent until the project is first synced.
  bitrix_synced_at?: string;
  // Agent UUID that runs gen_design_context / design_proposal (the designer
  // analyst). When unset, design actions fall back to a "design" squad leader.
  design_agent?: string;
  // Auto-fire policy for design proposals on incoming issues (Phase 6):
  // off | epics | all. Absent defaults to epics when the feature is enabled.
  design_auto?: string;
  // Ordered deploy targets for the deploy slice action (MCP-P1,
  // docs/deploy-mcp-integration.md §3). Non-secret routing only — the GitLab
  // PAT lives sealed in git_credential, never here. Read leniently via
  // parseDeployEnvironments in @agora/core/api/schemas.
  deploy_environments?: DeployEnvironmentSetting[];
  // Agent UUID that runs the deploy slice action. When unset, deploy falls
  // back to the issue's assignee agent, then the firing user's own agent.
  deploy_agent?: string;
  [key: string]: unknown;
}

// One entry of settings.deploy_environments. kind="gitlab_pipeline" targets a
// GitLab CI/CD pipeline (project_path + ref, optional GitLab environment
// name); target.command is the stack-agnostic Tier-2 fallback the agent runs
// on its daemon. requires_human environments (and production-named keys) are
// human-only triggers, enforced server-side.
export interface DeployEnvironmentTargetSetting {
  kind?: string;
  project_path?: string;
  ref?: string;
  environment?: string;
  command?: string;
  [key: string]: unknown;
}

export interface DeployEnvironmentSetting {
  key?: string;
  label?: string;
  kind?: string;
  requires_human?: boolean;
  target?: DeployEnvironmentTargetSetting;
  [key: string]: unknown;
}

export interface Project {
  id: string;
  workspace_id: string;
  title: string;
  description: string | null;
  icon: string | null;
  status: ProjectStatus;
  priority: ProjectPriority;
  lead_type: "member" | "agent" | null;
  lead_id: string | null;
  // When set, binds the project to a single squad: only that squad and its
  // member agents (or its leader) may be assigned to the project's issues.
  // null = unbound (any agent/squad may work it). Enforced server-side.
  squad_id: string | null;
  settings: ProjectSettings;
  created_at: string;
  updated_at: string;
  issue_count: number;
  done_count: number;
  resource_count: number;
}

export interface CreateProjectRequest {
  title: string;
  description?: string;
  icon?: string;
  status?: ProjectStatus;
  priority?: ProjectPriority;
  lead_type?: "member" | "agent";
  lead_id?: string;
  // Bind the new project to a squad (only that squad's agents work its issues).
  squad_id?: string | null;
  // Resources to attach in the same transaction as the project. Server returns
  // 4xx (and rolls back) if any one is invalid or duplicate.
  resources?: CreateProjectResourceRequest[];
}

export interface UpdateProjectRequest {
  title?: string;
  description?: string | null;
  icon?: string | null;
  status?: ProjectStatus;
  priority?: ProjectPriority;
  lead_type?: "member" | "agent" | null;
  lead_id?: string | null;
  // Bind/unbind the project's squad. Send null to unbind; omit to leave as-is.
  squad_id?: string | null;
  // When present, replaces the whole settings blob. Callers send the merged
  // object ({ ...project.settings, <changed key> }).
  settings?: ProjectSettings;
}

export interface ListProjectsResponse {
  projects: Project[];
  total: number;
}

// ProjectResource is a typed pointer from a project to an external resource.
// The resource_ref shape depends on resource_type. New types add a case in
// validateAndNormalizeResourceRef on the server and a renderer in the UI.
//
// Known types (UI must default-case unknown server-side additions):
//   - github_repo: cloud-side git checkout, ref = { url, default_branch_hint? }
//   - local_directory: in-place agent execution on a specific daemon,
//     ref = { local_path, daemon_id, label? }
export type ProjectResourceType = "github_repo" | "local_directory";

export interface GithubRepoResourceRef {
  url: string;
  default_branch_hint?: string;
}

export interface LocalDirectoryResourceRef {
  local_path: string;
  daemon_id: string;
  label?: string;
  /** "in_place" (default) or "worktree" — how the agent workdir is derived. */
  isolation?: "in_place" | "worktree";
  /** Resource permission. "write" (default) allows in-place edits or worktree
   *  write-back; "read" forces worktree isolation and blocks every write-back
   *  path, so agents treat the folder as reference material only. */
  access?: "read" | "write";
  /** The developer's own locally-running dev server (e.g. http://localhost:3000).
   *  When set, the issue Preview surface proxies to it instead of spawning one.
   *  localhost / 127.0.0.1 / private-LAN only. */
  preview_url?: string;
}

export type ProjectResourceRef =
  | GithubRepoResourceRef
  | LocalDirectoryResourceRef
  | Record<string, unknown>;

export interface ProjectResource {
  id: string;
  project_id: string;
  workspace_id: string;
  resource_type: ProjectResourceType;
  resource_ref: ProjectResourceRef;
  label: string | null;
  position: number;
  created_at: string;
  created_by: string | null;
}

export interface CreateProjectResourceRequest {
  resource_type: ProjectResourceType;
  resource_ref: ProjectResourceRef;
  label?: string;
  position?: number;
}

// resource_type is immutable server-side; partial-update payload mirrors that.
// Sending only the field(s) you want to change is fine — the server merges
// the request body with the existing row, including resource_ref shortcuts.
export interface UpdateProjectResourceRequest {
  resource_ref?: ProjectResourceRef;
  label?: string | null;
  position?: number;
}

export interface ListProjectResourcesResponse {
  resources: ProjectResource[];
  total: number;
}
