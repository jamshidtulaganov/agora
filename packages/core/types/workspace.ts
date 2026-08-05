export type MemberRole = "owner" | "admin" | "member";

export interface WorkspaceRepo {
  url: string;
  description?: string;
}

// A per-workspace git credential (e.g. a GitHub PAT) the daemon uses to clone
// private repos for the matching host+owner. The secret is never returned.
export interface GitCredential {
  id: string;
  label: string;
  provider: string;
  host: string;
  owner: string;
  username: string;
  auth_kind: string;
  created_at: string;
}

// Status of the workspace's single Figma credential (the PAT agents use to
// read Figma designs referenced by issues). Token material is never returned
// — token_last4 only. All fields beyond `configured` are display metadata.
export interface FigmaCredentialStatus {
  configured: boolean;
  label: string;
  token_last4: string;
  token_kind: string;
  expires_at: string;
  expiring_soon: boolean;
  seat_probe: string;
  probe_status: string;
  probed_at: string;
}

// Status of one workspace remote-MCP credential (the sealed auth for a remote
// http/sse MCP server, keyed by server name). Token material is never returned
// — `has_secret` reports one is stored and `last4` is a display hint only.
export interface McpCredentialStatus {
  id: string;
  server_name: string;
  has_secret: boolean;
  last4: string;
  created_at: string;
  updated_at: string;
}

// Write payload for sealing a remote-MCP credential. The auth value is
// write-only: either a single header (header_name + secret) — the common bearer
// case — or a full header map. The server seals it and never echoes it back.
export interface McpCredentialInput {
  header_name?: string;
  secret?: string;
  headers?: Record<string, string>;
}

// Dedicated workspace MCP document. This is intentionally separate from the
// generic Workspace response because command/env/header values may contain
// sensitive material and are only readable by workspace owners/admins.
export interface WorkspaceMcpConfigResponse {
  workspace_id: string;
  mcp_config: unknown | null;
}

// A per-workspace release integration (release-hub Thread B). One of the named
// connector kinds (webhook | slack | bitrix | github_release | gitlab_release |
// sentry) fires on release-lifecycle events. The sealed secret (webhook/bot/API
// token or URL) is never returned — `has_secret` reports only that one is
// stored. `config` is the NON-secret, per-kind display/routing metadata.
// `events` are the short lifecycle names that fire it.
export interface ReleaseIntegration {
  id: string;
  kind: string;
  config: {
    name?: string;
    channel_hint?: string; // slack
    owner?: string; // github_release
    repo?: string; // github_release
    project_path?: string; // gitlab_release
    org?: string; // sentry
    project?: string; // sentry
  } & Record<string, unknown>;
  events: string[];
  enabled: boolean;
  probe_status: string;
  has_secret: boolean;
  created_at: string;
  updated_at: string;
}

// Write payload for creating/updating a release integration. Only the fields
// relevant to the chosen `kind` are sent; every secret-bearing field (url,
// webhook_url, token, host, base_url, secret) is write-only — the server seals
// it and never echoes it back.
export interface ReleaseIntegrationInput {
  kind?: string;
  name?: string;
  events: string[];
  enabled?: boolean;
  // webhook
  url?: string;
  secret?: string;
  // slack
  webhook_url?: string;
  channel_hint?: string;
  // github_release / gitlab_release / sentry
  token?: string;
  // github_release
  owner?: string;
  repo?: string;
  // gitlab_release
  host?: string;
  project_path?: string;
  // sentry
  base_url?: string;
  org?: string;
  project?: string;
}

export interface Workspace {
  id: string;
  name: string;
  slug: string;
  description: string | null;
  context: string | null;
  settings: Record<string, unknown>;
  repos: WorkspaceRepo[];
  issue_prefix: string;
  avatar_url: string | null;
  created_at: string;
  updated_at: string;
}

export interface Member {
  id: string;
  workspace_id: string;
  user_id: string;
  role: MemberRole;
  created_at: string;
}

export interface User {
  id: string;
  name: string;
  email: string;
  avatar_url: string | null;
  onboarded_at: string | null;
  /**
   * JSONB payload from the server. Typed as `unknown` here so this module
   * stays independent of the questionnaire shape — the onboarding views
   * cast into `Partial<QuestionnaireAnswers>` when reading. Server always
   * returns an object (defaults to `{}`), never null.
   */
  onboarding_questionnaire: Record<string, unknown>;
  /**
   * Legacy column from the removed starter-content dialog. The column is
   * still written to (always 'imported' for new accounts after the
   * mark-onboarded paths run) so older desktop builds — which still render
   * the dialog on NULL — don't show it to anyone created on a newer server.
   * Kept as `string | null` for forward compatibility.
   */
  starter_content_state: string | null;
  /** Preferred UI language. null means "follow client/system". */
  language: string | null;
  /**
   * Free-form self-description (role, stack, preferences). Injected into
   * the agent brief so coding agents have cheap, durable context about
   * who is requesting the work. Server always returns a string —
   * NOT NULL DEFAULT '' at the column level, empty when unset.
   */
  profile_description: string;
  /** Pinned IANA tz; null means "use browser-detected tz at render time". */
  timezone: string | null;
  created_at: string;
  updated_at: string;
}

export interface MemberWithUser {
  id: string;
  workspace_id: string;
  user_id: string;
  role: MemberRole;
  created_at: string;
  name: string;
  email: string;
  avatar_url: string | null;
}

// ActorDirectoryEntry is a displayable user (name + avatar) referenced anywhere
// in the workspace — comment authors, assignees, creators, members — including
// people who are no longer, or never were, team members. Used only to resolve
// names/avatars for display (e.g. an imported Bitrix comment author who isn't on
// the team); it does NOT feed pickers, so the team roster stays the member list.
export interface ActorDirectoryEntry {
  user_id: string;
  name: string;
  avatar_url: string | null;
}

export interface Invitation {
  id: string;
  workspace_id: string;
  inviter_id: string;
  invitee_email: string;
  invitee_user_id: string | null;
  role: MemberRole;
  status: "pending" | "accepted" | "declined" | "expired";
  created_at: string;
  updated_at: string;
  expires_at: string;
  inviter_name?: string;
  inviter_email?: string;
  workspace_name?: string;
}

export interface InvitationAuthInfo {
  invitee_email: string;
  account_exists: boolean;
}
