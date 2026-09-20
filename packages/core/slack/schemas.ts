import { z } from "zod";

// Slack app wire types and their parse schemas.
//
// These live beside the queries rather than in api/schemas.ts because the
// Slack surface is one self-contained feature (docs/slack-integration-plan.md
// §Phase 1) and keeping its contract in one file makes the drift question —
// "what does the server actually promise here?" — answerable by reading one
// screen.
//
// Every schema is lenient and every field is defaulted, for the reason
// CLAUDE.md gives under API Response Compatibility: this panel ships inside a
// desktop build that will outlive the server it was compiled against. A
// response that gained, lost or retyped a field must degrade into a panel that
// still renders, never a white screen. In particular:
//
//   - `status` and every route event stay `z.string()`, NOT an enum. A status
//     or event kind a newer server invented must survive the parse and be
//     rendered as a generic row; rejecting it would delete the whole
//     installation from the UI.
//   - `configured` defaults to FALSE. Claiming a deployment is configured when
//     the field is missing would show a Connect button that dies at the OAuth
//     exchange, after an admin has already granted scopes in Slack.
//   - Arrays `.catch([])` so one malformed element cannot take the list with
//     it.

/** One Slack workspace this Agora workspace is wired to. Carries no token —
 *  the server never returns one, not even masked. */
export interface SlackInstallation {
  id: string;
  workspace_id: string;
  team_id: string;
  team_name: string;
  enterprise_id?: string;
  app_id: string;
  bot_user_id: string;
  scopes: string[];
  installer_user_id: string;
  /** "active" | "revoked" — kept as a string so an unknown status renders a
   *  generic badge instead of vanishing. */
  status: string;
  installed_at: string;
  updated_at: string;
}

export interface ListSlackInstallationsResponse {
  installations: SlackInstallation[];
  /** Whether an install could succeed at all on this deployment (all four
   *  Slack keys present). */
  configured: boolean;
}

/** Which Agora events reach which Slack channel. */
export interface SlackChannelRoute {
  id: string;
  workspace_id: string;
  installation_id: string;
  channel_id: string;
  channel_name: string;
  project_id?: string;
  events: string[];
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ListSlackRoutesResponse {
  routes: SlackChannelRoute[];
  /** The event vocabulary the SERVER understands, in the order to offer it.
   *  Shipped by the server so the UI cannot drift into offering a checkbox
   *  that silently does nothing. */
  available_events: string[];
  /** What a freshly created route hears: the quiet default. */
  default_events: string[];
}

/** One row in the channel picker. */
export interface SlackChannel {
  id: string;
  name: string;
  is_private: boolean;
  /** False for a public channel the app has not joined — postable only via
   *  chat:write.public, and not at all for a private one, so the UI says
   *  "invite the app first". */
  is_member: boolean;
}

export interface ListSlackChannelsResponse {
  channels: SlackChannel[];
  next_cursor: string;
  installation_id: string;
}

/** The URL the browser opens to consent — install or personal link. */
export interface SlackBeginResponse {
  authorize_url: string;
}

/** What a route write sends. Every field optional so a toggle-one-checkbox
 *  edit does not have to restate the channel. */
export interface SlackRouteInput {
  installation_id?: string;
  channel_id?: string;
  channel_name?: string;
  project_id?: string;
  events?: string[];
  enabled?: boolean;
}

export const SlackInstallationSchema = z
  .object({
    id: z.string().default(""),
    workspace_id: z.string().default(""),
    team_id: z.string().default(""),
    team_name: z.string().default(""),
    enterprise_id: z.string().optional(),
    app_id: z.string().default(""),
    bot_user_id: z.string().default(""),
    scopes: z.array(z.string()).catch([]).default([]),
    installer_user_id: z.string().default(""),
    status: z.string().default("active"),
    installed_at: z.string().default(""),
    updated_at: z.string().default(""),
  })
  .loose();

export const ListSlackInstallationsSchema = z
  .object({
    installations: z.array(SlackInstallationSchema).catch([]),
    configured: z.boolean().default(false),
  })
  .loose();

export const EMPTY_SLACK_INSTALLATIONS: ListSlackInstallationsResponse = {
  installations: [],
  configured: false,
};

export const SlackChannelRouteSchema = z
  .object({
    id: z.string().default(""),
    workspace_id: z.string().default(""),
    installation_id: z.string().default(""),
    channel_id: z.string().default(""),
    channel_name: z.string().default(""),
    project_id: z.string().optional(),
    // An unknown event kind is PRESERVED, not dropped: the editor renders it
    // unchecked but keeps it on save, so a newer server's route survives an
    // older client editing a different checkbox.
    events: z.array(z.string()).catch([]).default([]),
    enabled: z.boolean().default(true),
    created_at: z.string().default(""),
    updated_at: z.string().default(""),
  })
  .loose();

export const ListSlackRoutesSchema = z
  .object({
    routes: z.array(SlackChannelRouteSchema).catch([]),
    available_events: z.array(z.string()).catch([]).default([]),
    default_events: z.array(z.string()).catch([]).default([]),
  })
  .loose();

/** The client-side fallback vocabulary, used only when the server said
 *  nothing at all. `commented` is deliberately absent: it is DM-only by
 *  construction, because a comment in a channel is the thing that makes
 *  people mute. */
export const SLACK_ROUTE_EVENTS = [
  "failed",
  "qa_verdict",
  "review_verdict",
  "assigned",
  "mentioned",
  "agent_done",
  "status_changed",
  "created",
] as const;

/** The quiet default: a channel hears only what needs a human. */
export const SLACK_DEFAULT_ROUTE_EVENTS = ["failed", "qa_verdict", "review_verdict"];

export const EMPTY_SLACK_ROUTES: ListSlackRoutesResponse = {
  routes: [],
  available_events: [...SLACK_ROUTE_EVENTS],
  default_events: [...SLACK_DEFAULT_ROUTE_EVENTS],
};

export const EMPTY_SLACK_ROUTE: SlackChannelRoute = {
  id: "",
  workspace_id: "",
  installation_id: "",
  channel_id: "",
  channel_name: "",
  events: [],
  enabled: true,
  created_at: "",
  updated_at: "",
};

export const SlackChannelSchema = z
  .object({
    id: z.string().default(""),
    name: z.string().default(""),
    is_private: z.boolean().default(false),
    is_member: z.boolean().default(false),
  })
  .loose();

export const ListSlackChannelsSchema = z
  .object({
    channels: z.array(SlackChannelSchema).catch([]),
    next_cursor: z.string().default(""),
    installation_id: z.string().default(""),
  })
  .loose();

export const EMPTY_SLACK_CHANNELS: ListSlackChannelsResponse = {
  channels: [],
  next_cursor: "",
  installation_id: "",
};

export const SlackBeginSchema = z
  .object({
    authorize_url: z.string().default(""),
  })
  .loose();

export const EMPTY_SLACK_BEGIN: SlackBeginResponse = { authorize_url: "" };
