// GET /api/issues/:id/browser — where the Live-testing bay reaches a CDP
// browser for this issue. Unlike the editor response this never depends on a
// worktree existing (the bay's primary use drives the DEPLOYED QA target).
export interface IssueBrowserResponse {
  mode: string; // "self-host" | "cloud" | "" (unresolvable / unrecognized)
  daemon_url: string; // self-host only — dial the local daemon directly
  browser_url: string; // cloud only — same-origin /browser/proxy/<token> base
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
