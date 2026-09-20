export type GitHubPullRequestState = "open" | "closed" | "merged" | "draft";

/** Aggregated CI status for a PR's current head SHA, computed server-side from
 * the latest check_suite per app. `null` when no completed suite has been seen
 * yet (e.g. PR just opened, or repository has no CI configured). */
export type GitHubPullRequestChecksConclusion = "passed" | "failed" | "pending";

/** Raw mirror of GitHub's `mergeable_state`. The UI only surfaces `clean` and
 * `dirty`; the other values (`blocked`, `behind`, `unstable`, `unknown`,
 * `has_hooks`, `draft`) round-trip but render as unknown to avoid asserting
 * "conflicts" for blocking reasons that aren't actual conflicts. */
export type GitHubMergeableState = string;

export interface GitHubInstallation {
  id: string;
  workspace_id: string;
  /** GitHub's numeric installation id — the management handle used by the
   * connect / disconnect flows. Omitted when the caller cannot manage
   * integrations (see `ListGitHubInstallationsResponse.can_manage`). */
  installation_id?: number;
  account_login: string;
  account_type: "User" | "Organization";
  account_avatar_url: string | null;
  created_at: string;
  /** Display name of the workspace member who connected this installation.
   * Optional because older backends and minimum-visibility deployments may
   * omit it; the UI renders the "connected by" line only when present. */
  connected_by?: string;
}

export interface GitHubPullRequest {
  id: string;
  workspace_id: string;
  repo_owner: string;
  repo_name: string;
  number: number;
  title: string;
  state: GitHubPullRequestState;
  html_url: string;
  branch: string | null;
  author_login: string | null;
  author_avatar_url: string | null;
  merged_at: string | null;
  closed_at: string | null;
  pr_created_at: string;
  pr_updated_at: string;
  /** Optional; older backends omit this field. */
  mergeable_state?: GitHubMergeableState | null;
  /** Optional; older backends omit this field. */
  checks_conclusion?: GitHubPullRequestChecksConclusion | null;
  /** Per-suite counts that feed the segmented progress bar. Older backends
   * omit these; treat absence as 0 (the card renders only when sum > 0). */
  checks_passed?: number;
  checks_failed?: number;
  checks_pending?: number;
  /** Diff stats from GitHub's `pull_request` payload. Older backends omit
   * these fields; we treat 0/0/0 as "unknown" and hide the stats row. */
  additions?: number;
  deletions?: number;
  changed_files?: number;
}

export interface ListGitHubInstallationsResponse {
  installations: GitHubInstallation[];
  /** Whether the deployment has GitHub App credentials configured. When false, the Connect button is hidden / disabled. */
  configured: boolean;
  /** Whether the caller can connect / disconnect installations. Non-admin
   * members get `false` along with installations that omit `installation_id`.
   * Older backends predating MUL-2413 omit the field; treat absence as
   * `false` for read-only safety. */
  can_manage?: boolean;
}

export interface GitHubConnectResponse {
  /** The GitHub App install URL the browser should open. Empty when `configured` is false. */
  url?: string;
  configured: boolean;
}

/** Where an issue's file list came from. `github` = the live Files API, with
 * per-file status and +/- counts. `stored` = `changed_paths` persisted by the
 * PR webhook sync — paths only, so zero counts mean UNKNOWN, not "zero lines".
 * `none` = nothing known about this PR's files. */
export type IssueChangeFilesSource = "github" | "stored" | "none";

/** GitHub's per-file verb. Kept as a widened string because GitHub can add a
 * value at any time — render unknown values with a neutral glyph, never crash. */
export type IssueChangeFileStatus =
  | "added"
  | "modified"
  | "removed"
  | "renamed"
  | "copied"
  | "changed"
  | "unchanged"
  | (string & {});

export interface IssueChangeFile {
  path: string;
  /** Empty when the list came from stored paths and the verb is unknown. */
  status: IssueChangeFileStatus;
  /** Present on a rename, so a moved file doesn't read as an unrelated add. */
  previous_path?: string;
  additions: number;
  deletions: number;
}

/** One pull request's contribution to "what did the agent change". */
export interface IssueChange {
  pr_number: number;
  title: string;
  state: GitHubPullRequestState;
  html_url: string;
  repo_owner: string;
  repo_name: string;
  additions: number;
  deletions: number;
  changed_files: number;
  files_source: IssueChangeFilesSource;
  files: IssueChangeFile[];
}

export interface IssueChangesResponse {
  changes: IssueChange[];
}

/** One file's unified diff, fetched on demand. `patch` is null whenever there
 * is nothing to show, and `reason` always says why — an empty diff pane and a
 * failed fetch must never look the same. */
export interface IssueChangePatchResponse {
  patch: string | null;
  reason: string;
}
