// GET /api/issues/:id/editor — resolves where to reach a live view of an
// issue's worktree. See packages/core/api/schemas.ts GetIssueEditorResponseSchema
// for the parse boundary.

export interface EditorAgent {
  agent_id: string;
  agent_name: string;
  work_dir: string;
  status: string;
}

export interface GetIssueEditorResponse {
  mode: string; // "self-host" | "cloud" | "" (no worktree yet / unrecognized)
  daemon_url: string; // self-host only
  user_id: string; // self-host only
  agents: EditorAgent[]; // self-host only, most-recently-active first
  editor_url: string; // cloud only
}
