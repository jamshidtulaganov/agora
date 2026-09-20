import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/**
 * Query key namespace for the Slack app surface.
 *
 * Everything is keyed on `wsId` — a Slack installation, its channel routes and
 * the channel picker are all workspace-scoped, so switching workspaces must
 * change the cache key and show the new workspace's wiring with no manual
 * invalidation (CLAUDE.md → State Management).
 */
export const slackKeys = {
  all: (wsId: string) => ["slack", wsId] as const,
  installations: (wsId: string) => [...slackKeys.all(wsId), "installations"] as const,
  routes: (wsId: string) => [...slackKeys.all(wsId), "routes"] as const,
  channels: (wsId: string, installationId: string) =>
    [...slackKeys.all(wsId), "channels", installationId] as const,
};

/** Which Slack teams this workspace is wired to, and whether the deployment
 *  can complete an install at all. */
export const slackInstallationsOptions = (wsId: string) =>
  queryOptions({
    queryKey: slackKeys.installations(wsId),
    queryFn: () => api.listSlackInstallations(wsId),
    enabled: !!wsId,
  });

/** The channel routes, plus the event vocabulary the SERVER understands. */
export const slackRoutesOptions = (wsId: string) =>
  queryOptions({
    queryKey: slackKeys.routes(wsId),
    queryFn: () => api.listSlackRoutes(wsId),
    enabled: !!wsId,
  });

/**
 * The channel picker.
 *
 * One page only. conversations.list is Tier 2 (20+/min per workspace) and the
 * server passes paging through rather than walking it, so a workspace with
 * hundreds of channels does not spend that budget on one settings page open.
 * `installationId` is part of the key because two installations on the same
 * workspace see different channel sets.
 */
export const slackChannelsOptions = (wsId: string, installationId: string) =>
  queryOptions({
    queryKey: slackKeys.channels(wsId, installationId),
    queryFn: () => api.listSlackChannels(wsId, installationId),
    enabled: !!wsId && !!installationId,
    // Channels change rarely, and re-opening the picker should not re-spend a
    // Tier-2 call every time.
    staleTime: 60_000,
  });
