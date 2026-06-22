export type SprintStatus = "planned" | "active" | "completed";

export interface Sprint {
  id: string;
  workspace_id: string;
  project_id: string;
  name: string;
  goal: string | null;
  status: SprintStatus;
  // Calendar dates as nullable ISO strings. Display-only on the frontend —
  // the backend owns the canonical values.
  start_date: string | null;
  end_date: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateSprintRequest {
  name: string;
  goal?: string;
  status?: SprintStatus;
  start_date?: string | null;
  end_date?: string | null;
}

// PUT replaces the editable fields wholesale (matches the backend contract:
// `{name, goal, status, start_date, end_date}`).
export interface UpdateSprintRequest {
  name: string;
  goal: string | null;
  status: SprintStatus;
  start_date: string | null;
  end_date: string | null;
}

export interface ListSprintsResponse {
  sprints: Sprint[];
  total: number;
}

export interface ListSprintIssuesResponse {
  issues: import("./issue").Issue[];
}
