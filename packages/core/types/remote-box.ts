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
