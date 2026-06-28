export type ProjectStatus = "planned" | "in_progress" | "paused" | "completed" | "cancelled";

export type ProjectPriority = "urgent" | "high" | "medium" | "low" | "none";

// Per-project preferences blob (project.settings jsonb on the server, mirrors
// Workspace.settings). Always an object — the server normalizes empty/null to
// {}. Known keys are typed; the index signature keeps unknown server-side keys
// round-tripping through an update without being dropped.
export interface ProjectSettings {
  // Per-project "sprint mode". Absent means ON (the historical default from the
  // old localStorage stub); explicit false hides the Sprints UI for the project.
  sprint_mode?: boolean;
  // QA smoke configuration consumed by the run_qa slice action. qa_smoke_cmd is
  // how to bring the app up (e.g. "pnpm dev"); qa_smoke_url is where it serves
  // (e.g. "http://localhost:5173"). When set, the QA gate uses these instead of
  // auto-detecting the smoke flow. Either may be set independently.
  qa_smoke_cmd?: string;
  qa_smoke_url?: string;
  // Documentation repository for the auto_docs slice action — a SEPARATE repo
  // from the code (e.g. a Docusaurus site). When set, auto_docs writes the docs
  // there and opens a PR against it.
  docs_repo?: string;
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
