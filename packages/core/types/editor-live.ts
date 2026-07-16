// GET /api/issues/:id/browser — where the Live-testing bay reaches a CDP
// browser for this issue. Unlike the editor response this never depends on a
// worktree existing (the bay's primary use drives the DEPLOYED QA target).
export interface IssueBrowserResponse {
  mode: string; // "self-host" | "cloud" | "" (unresolvable / unrecognized)
  daemon_url: string; // self-host only — dial the local daemon directly
  browser_url: string; // cloud only — same-origin /browser/proxy/<token> base
}

// GET /api/runtimes/by-daemon/:daemonId/browse — where the web folder picker
// walks one daemon's filesystem (<daemon_url>/editor/fs/list). Keyed by the
// MACHINE rather than an issue: attaching a local folder to a project happens
// with no issue in hand.
export interface DaemonBrowseTarget {
  // "self-host" | "cloud" | "offline" | "" (unresolvable / unrecognized).
  // Read as a plain string: an unknown value must render the picker's generic
  // unavailable state rather than crash.
  mode: string;
  // Absolute http://127.0.0.1:<port> (self-host) or a same-origin
  // /browser/proxy/<token> base (cloud). Blank when the machine is offline.
  daemon_url: string;
}

// One directory listing from a daemon's folder picker. Directories only — the
// picker attaches folders, not files.
export interface FsListEntry {
  name: string;
  path: string;
  is_dir: boolean;
  is_git_repo: boolean;
  is_symlink: boolean;
}

export interface FsListResponse {
  path: string;
  // Blank at a browsable-root boundary: there is no "up" the daemon will serve.
  parent: string;
  home: string;
  entries: FsListEntry[];
  // The listing hit the daemon's per-directory cap.
  truncated: boolean;
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
