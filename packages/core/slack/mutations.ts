import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { slackKeys } from "./queries";
import type { SlackRouteInput } from "./schemas";

// Slack writes are NOT optimistic, and that is deliberate.
//
// The default in this codebase is optimistic-with-rollback, because a user
// should not wait for the server to see their own edit. These four are the
// exception: each one's real effect happens in Slack — an OAuth consent
// screen, a bot leaving a channel, a route that starts or stops posting into a
// room full of people — and an optimistic "connected" that the server then
// refuses would be a lie about a third party's state. So every mutation here
// invalidates on SETTLE (success or failure) and lets the server's answer be
// the answer.

/**
 * Start the workspace install: mint the sealed state and hand back Slack's
 * consent URL. Writes nothing on its own — the installation row is created by
 * the OAuth callback — so there is nothing to invalidate until the browser
 * comes back.
 */
export function useBeginSlackInstall(wsId: string) {
  return useMutation({
    mutationFn: () => api.beginSlackInstall(wsId),
  });
}

/**
 * Disconnect one Slack team from this workspace.
 *
 * The row is marked revoked (not deleted) and its routes are dropped, so the
 * list must resync either way — including on failure, where the server may
 * have completed the route delete before failing the status flip.
 */
export function useDeleteSlackInstallation(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (installationId: string) => api.deleteSlackInstallation(wsId, installationId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: slackKeys.installations(wsId) });
      qc.invalidateQueries({ queryKey: slackKeys.routes(wsId) });
    },
  });
}

/**
 * Create or edit a channel route.
 *
 * Without a `routeId` the server upserts by (installation, channel, project),
 * which is the same tuple its unique index enforces — so picking a channel
 * that is already routed edits that route instead of failing on a constraint.
 */
export function useSaveSlackRoute(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ routeId, input }: { routeId?: string; input: SlackRouteInput }) =>
      api.saveSlackRoute(wsId, input, routeId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: slackKeys.routes(wsId) });
    },
  });
}

/** Stop posting to a channel. */
export function useDeleteSlackRoute(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (routeId: string) => api.deleteSlackRoute(wsId, routeId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: slackKeys.routes(wsId) });
    },
  });
}

/**
 * Start the PERSONAL link — one person binding their Slack identity to their
 * Agora account, so unfurls can tell what they may see and their inbox DMs can
 * find them. The consent screen it produces requests no bot scopes.
 */
export function useBeginSlackUserLink() {
  return useMutation({
    mutationFn: () => api.beginSlackUserLink(),
  });
}
