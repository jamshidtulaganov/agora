// Remote Boxes (connected_box) — a developer's onboarded remote dev server
// (e.g. jamshid.sdteam.uz). Agora installs and runs a normal native self-host
// daemon on it; this type is the new, parallel onboarding layer.
export interface ConnectedBox {
  id: string;
  workspace_id: string;
  /** The developer who owns the box. Null when the owning user was removed. */
  owner_id: string | null;
  label: string;
  ssh_host: string;
  ssh_user: string;
  ssh_port: number;
  /** Public half of the Agora deploy key the dev authorizes (empty until set). */
  deploy_pubkey: string;
  /** Set once the box's daemon registers; null while pending/bootstrapping. */
  daemon_id: string | null;
  /** "pending" | "bootstrapping" | "online" | "offline" | "error" — treat as a
   *  free string so a future status downgrades gracefully. */
  status: string;
  last_error: string;
  /** https clone URL Agora fetches the branch from (token injected at sync). */
  repo_url: string;
  /** absolute path on the box where the branch is checked out (the served dir). */
  work_dir: string;
  /** most recently synced branch (for display). */
  last_branch: string;
  /** Project this box is bound to as its QA target; null when unbound. An issue
   *  in that project resolves to this box for deploy-qa. */
  project_id: string | null;
  created_at: string;
}

export interface CreateRemoteBoxRequest {
  label: string;
  ssh_host: string;
  ssh_user: string;
  ssh_port?: number;
  repo_url?: string;
  work_dir?: string;
}

// Result of a git-sync onto a box (branch sync / issue deploy-qa). ok is the
// remote git exit status; output is the remote git output (token redacted
// server-side).
export interface RemoteBoxSyncResult {
  ok: boolean;
  branch: string;
  output: string;
  box: ConnectedBox;
}

// Provision a per-developer QA box for a workspace member. handle is optional —
// it defaults server-side to a slug of the member's email local part. dry_run
// returns the runbook + placement WITHOUT touching the host (the review gate).
export interface ProvisionBoxRequest {
  member_id: string;
  handle?: string;
  dry_run?: boolean;
}

// Result of a provision (or a dry-run preview). On a dry run `ran` is false and
// `box` is null — only the computed placement + the (token-redacted) `script`
// are returned for review. On a real run `box` is the registered box and
// `output` is the redacted host output.
export interface ProvisionBoxResult {
  handle: string;
  subdomain: string;
  work_dir: string;
  database: string;
  script: string;
  dry_run: boolean;
  ran: boolean;
  ok: boolean;
  output: string;
  box: ConnectedBox | null;
}

// Settings → Labs workspace flags (GET/PUT /api/workspace-labs) — QA-env
// routing: per-dev boxes toggle + designated shared fallback box.
export interface WorkspaceLabs {
  qa_dev_boxes: boolean;
  qa_fallback_box_id: string;
}
