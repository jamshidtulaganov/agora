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
  // Self-host only: the caller's editor account tokens (Settings → editor
  // integration) to forward verbatim in the daemon /editor/launch body, so
  // gh CLI / HTTPS git in the editor terminal are authenticated.
  editor_env?: Record<string, string>;
}

// GET /api/issues/:id/qa-preview-url — the issue's resolved QA target (a
// deployed connected box, or the project's configured qa_smoke_url), for
// workspaces whose QA target is a standing deployed environment rather than
// a per-issue daemon worktree (e.g. a monolith QA'd by deploying a branch to
// a box). "" means nothing resolves.
export interface IssueQAPreviewURLResponse {
  url: string;
  // Server-checked (X-Frame-Options / CSP frame-ancestors) — false means an
  // iframe embed would render blank; the caller should offer an "Open" link
  // instead of attempting to embed it.
  embeddable: boolean;
}
