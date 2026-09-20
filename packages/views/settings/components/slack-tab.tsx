"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@agora/ui/components/ui/alert-dialog";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import {
  slackChannelsOptions,
  slackInstallationsOptions,
  slackRoutesOptions,
  useBeginSlackInstall,
  useDeleteSlackInstallation,
  useDeleteSlackRoute,
  useSaveSlackRoute,
  SLACK_DEFAULT_ROUTE_EVENTS,
  SLACK_ROUTE_EVENTS,
} from "@agora/core/slack";
import type { SlackChannelRoute, SlackInstallation } from "@agora/core/slack";
import { useT } from "../../i18n";

// Settings → Integrations → Slack.
//
// Two things happen on this panel and nothing else:
//
//   1. An owner/admin connects the workspace to a Slack team (OAuth v2 — the
//      button opens Slack's consent screen and the server's callback writes
//      the installation).
//   2. They choose which channels hear which events.
//
// The event defaults ARE the product decision. Every competitor's Slack app
// dies of volume, so a fresh route hears only `failed`, `qa_verdict` and
// `review_verdict` — the three that mean a human is needed — and everything
// else is one checkbox away and stays the admin's choice. `commented` is not
// offered at all: a comment is context for one person, and in a channel it is
// the thing that makes people mute.
//
// Personal DMs are NOT configured here. They hang off the inbox, so a user's
// existing notification preferences already govern them; the "Connect Slack"
// row lives under Settings → Notifications.

const SLACK_ACTIVE = "active";

export function SlackTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);

  const { data: members } = useQuery(memberListOptions(wsId));
  const canManage = useMemo(() => {
    const me = members?.find((m) => m.user_id === currentUser?.id);
    return me?.role === "owner" || me?.role === "admin";
  }, [members, currentUser?.id]);

  const { data, isLoading } = useQuery(slackInstallationsOptions(wsId));
  const installations = data?.installations ?? [];
  // Explicit === true: a missing field must read as "not configured", never as
  // a Connect button that dies at the OAuth exchange.
  const configured = data?.configured === true;
  const activeInstall = installations.find((i) => i.status === SLACK_ACTIVE) ?? null;

  const beginInstall = useBeginSlackInstall(wsId);
  const deleteInstall = useDeleteSlackInstallation(wsId);
  const [disconnectTarget, setDisconnectTarget] = useState<SlackInstallation | null>(null);

  const connect = async () => {
    try {
      const res = await beginInstall.mutateAsync();
      if (!res.authorize_url) {
        toast.error(t(($) => $.slack.toast_connect_failed));
        return;
      }
      // A new tab, not a redirect: the person comes back to a settings page
      // that still has their place in it.
      window.open(res.authorize_url, "_blank", "noopener,noreferrer");
    } catch {
      toast.error(t(($) => $.slack.toast_connect_failed));
    }
  };

  const disconnect = async () => {
    if (!disconnectTarget) return;
    try {
      await deleteInstall.mutateAsync(disconnectTarget.id);
      toast.success(t(($) => $.slack.toast_disconnected));
      setDisconnectTarget(null);
    } catch {
      toast.error(t(($) => $.slack.toast_disconnect_failed));
    }
  };

  if (!configured && installations.length === 0) {
    return (
      <Card>
        <CardContent className="py-6">
          <p className="text-sm font-medium">{t(($) => $.slack.not_configured_title)}</p>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(($) => $.slack.not_configured_description)}
          </p>
        </CardContent>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">{t(($) => $.slack.page_description)}</p>
        {canManage && configured ? (
          <Button size="sm" onClick={connect} disabled={beginInstall.isPending}>
            {beginInstall.isPending ? (
              <Loader2 className="mr-1.5 size-4 animate-spin" />
            ) : (
              <Plus className="mr-1.5 size-4" />
            )}
            {t(($) => $.slack.connect_button)}
          </Button>
        ) : null}
      </div>

      {isLoading ? (
        <Card>
          <CardContent className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            {t(($) => $.slack.loading)}
          </CardContent>
        </Card>
      ) : installations.length === 0 ? (
        <Card>
          <CardContent className="py-6">
            <p className="text-sm font-medium">{t(($) => $.slack.empty_title)}</p>
            <p className="mt-1 text-sm text-muted-foreground">
              {t(($) => $.slack.empty_description)}
            </p>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="divide-y p-0">
            {installations.map((installation) => (
              <InstallationRow
                key={installation.id}
                installation={installation}
                canManage={canManage}
                onDisconnect={() => setDisconnectTarget(installation)}
              />
            ))}
          </CardContent>
        </Card>
      )}

      {activeInstall ? (
        <RoutesSection installationId={activeInstall.id} canManage={canManage} />
      ) : null}

      <AlertDialog
        open={!!disconnectTarget}
        onOpenChange={(open) => !open && setDisconnectTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.slack.disconnect_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.slack.disconnect_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteInstall.isPending}>
              {t(($) => $.slack.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={disconnect} disabled={deleteInstall.isPending}>
              {t(($) => $.slack.disconnect_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

/** One connected Slack team. Never shows a credential — the server does not
 *  return one, not even masked. */
function InstallationRow({
  installation,
  canManage,
  onDisconnect,
}: {
  installation: SlackInstallation;
  canManage: boolean;
  onDisconnect: () => void;
}) {
  const { t } = useT("settings");
  const isActive = installation.status === SLACK_ACTIVE;
  return (
    <div className="flex flex-wrap items-center gap-3 px-4 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">
            {installation.team_name || installation.team_id}
          </span>
          {isActive ? (
            <span className="size-1.5 shrink-0 rounded-full bg-emerald-500" />
          ) : (
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
              {t(($) => $.slack.status_revoked)}
            </span>
          )}
        </div>
        <p className="truncate text-xs text-muted-foreground">
          {t(($) => $.slack.bot_label, { id: installation.bot_user_id })}
        </p>
      </div>
      {canManage ? (
        <Button size="sm" variant="ghost" onClick={onDisconnect} aria-label={t(($) => $.slack.disconnect)}>
          <Trash2 className="size-4" />
        </Button>
      ) : null}
    </div>
  );
}

/** Which channels hear which events. */
function RoutesSection({
  installationId,
  canManage,
}: {
  installationId: string;
  canManage: boolean;
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const { data, isLoading } = useQuery(slackRoutesOptions(wsId));
  const routes = data?.routes ?? [];
  // The vocabulary comes from the SERVER, so the UI cannot drift into
  // offering a checkbox that silently does nothing. The bundled list is only
  // the fallback for a response that carried none.
  const availableEvents =
    data?.available_events && data.available_events.length > 0
      ? data.available_events
      : [...SLACK_ROUTE_EVENTS];
  const defaultEvents =
    data?.default_events && data.default_events.length > 0
      ? data.default_events
      : SLACK_DEFAULT_ROUTE_EVENTS;

  return (
    <section className="space-y-3">
      <div>
        <h3 className="text-sm font-semibold">{t(($) => $.slack.routes_title)}</h3>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(($) => $.slack.routes_description)}
        </p>
      </div>

      {isLoading ? (
        <Card>
          <CardContent className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            {t(($) => $.slack.routes_loading)}
          </CardContent>
        </Card>
      ) : routes.length === 0 ? (
        <Card>
          <CardContent className="py-6">
            <p className="text-sm text-muted-foreground">{t(($) => $.slack.routes_empty)}</p>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="divide-y p-0">
            {routes.map((route) => (
              <RouteRow
                // The key carries updated_at so a route changed elsewhere
                // (another admin, another tab) remounts and resyncs its local
                // checkbox state instead of showing a stale subscription.
                key={`${route.id}:${route.updated_at}`}
                route={route}
                availableEvents={availableEvents}
                canManage={canManage}
              />
            ))}
          </CardContent>
        </Card>
      )}

      {canManage ? (
        <AddRoute installationId={installationId} defaultEvents={defaultEvents} />
      ) : null}
    </section>
  );
}

function RouteRow({
  route,
  availableEvents,
  canManage,
}: {
  route: SlackChannelRoute;
  availableEvents: string[];
  canManage: boolean;
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const saveRoute = useSaveSlackRoute(wsId);
  const deleteRoute = useDeleteSlackRoute(wsId);
  const [selected, setSelected] = useState<string[]>(route.events ?? []);
  const [pendingDelete, setPendingDelete] = useState(false);

  // An event kind the server stored but this build does not know about is
  // rendered as a row of its own — unchecked-but-kept is not an option in a
  // checkbox list, so it is shown, checked, at the end. Dropping it on save
  // would silently delete a newer server's routing.
  const unknownSelected = selected.filter((kind) => !availableEvents.includes(kind));
  const kinds = [...availableEvents, ...unknownSelected];

  const dirty =
    selected.length !== (route.events ?? []).length ||
    selected.some((kind) => !(route.events ?? []).includes(kind));

  const toggle = (kind: string) => {
    setSelected((prev) =>
      prev.includes(kind) ? prev.filter((k) => k !== kind) : [...prev, kind],
    );
  };

  const save = async () => {
    try {
      await saveRoute.mutateAsync({ routeId: route.id, input: { events: selected } });
      toast.success(t(($) => $.slack.toast_route_saved));
    } catch {
      toast.error(t(($) => $.slack.toast_route_save_failed));
    }
  };

  const remove = async () => {
    try {
      await deleteRoute.mutateAsync(route.id);
      toast.success(t(($) => $.slack.toast_route_removed));
      setPendingDelete(false);
    } catch {
      toast.error(t(($) => $.slack.toast_route_remove_failed));
    }
  };

  return (
    <div className="space-y-3 px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <span className="truncate text-sm font-medium">
          #{route.channel_name || route.channel_id}
        </span>
        {canManage ? (
          <div className="flex items-center gap-1.5">
            <Button
              size="sm"
              variant="outline"
              disabled={!dirty || saveRoute.isPending || selected.length === 0}
              onClick={save}
            >
              {saveRoute.isPending ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : null}
              {t(($) => $.slack.save)}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setPendingDelete(true)}
              aria-label={t(($) => $.slack.remove)}
            >
              <Trash2 className="size-4" />
            </Button>
          </div>
        ) : null}
      </div>

      <div className="flex flex-wrap gap-x-4 gap-y-2">
        {kinds.map((kind) => (
          <label key={kind} className="flex items-center gap-2 text-sm">
            {/* The wrapping <label> is the accessible name; an aria-label
                here would be concatenated with it and read twice. */}
            <Checkbox
              checked={selected.includes(kind)}
              onCheckedChange={() => toggle(kind)}
              disabled={!canManage}
            />
            {eventLabel(t, kind)}
          </label>
        ))}
      </div>

      <AlertDialog open={pendingDelete} onOpenChange={(open) => !open && setPendingDelete(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.slack.remove_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.slack.remove_description, {
                channel: route.channel_name || route.channel_id,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteRoute.isPending}>
              {t(($) => $.slack.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={remove} disabled={deleteRoute.isPending}>
              {t(($) => $.slack.remove_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

/** Pick a channel; it starts on the quiet default and is edited above. */
function AddRoute({
  installationId,
  defaultEvents,
}: {
  installationId: string;
  defaultEvents: string[];
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const [channelId, setChannelId] = useState("");
  const { data, isLoading, isError } = useQuery(slackChannelsOptions(wsId, installationId));
  const channels = data?.channels ?? [];
  const saveRoute = useSaveSlackRoute(wsId);

  const add = async () => {
    const channel = channels.find((c) => c.id === channelId);
    if (!channel) return;
    try {
      await saveRoute.mutateAsync({
        input: {
          installation_id: installationId,
          channel_id: channel.id,
          channel_name: channel.name,
          events: defaultEvents,
        },
      });
      setChannelId("");
      toast.success(t(($) => $.slack.toast_route_saved));
    } catch {
      toast.error(t(($) => $.slack.toast_route_save_failed));
    }
  };

  if (isError) {
    return <p className="text-xs text-muted-foreground">{t(($) => $.slack.channels_failed)}</p>;
  }

  const selectedChannel = channels.find((c) => c.id === channelId);

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <NativeSelect
          className="w-56"
          aria-label={t(($) => $.slack.channel_label)}
          value={channelId}
          disabled={isLoading || channels.length === 0}
          onChange={(e) => setChannelId(e.target.value)}
        >
          <NativeSelectOption value="">
            {isLoading
              ? t(($) => $.slack.channels_loading)
              : channels.length === 0
                ? t(($) => $.slack.channels_empty)
                : t(($) => $.slack.channel_placeholder)}
          </NativeSelectOption>
          {channels.map((channel) => (
            <NativeSelectOption key={channel.id} value={channel.id}>
              #{channel.name}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Button size="sm" onClick={add} disabled={!channelId || saveRoute.isPending}>
          {saveRoute.isPending ? (
            <Loader2 className="mr-1.5 size-4 animate-spin" />
          ) : (
            <Plus className="mr-1.5 size-4" />
          )}
          {t(($) => $.slack.add_route)}
        </Button>
      </div>
      {/* A public channel the app has not joined is postable only through
          chat:write.public, and a private one not at all — say so before the
          first message silently fails. */}
      {selectedChannel && selectedChannel.is_member !== true ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.slack.not_member_hint)}</p>
      ) : (
        <p className="text-xs text-muted-foreground">{t(($) => $.slack.events_hint)}</p>
      )}
    </div>
  );
}

/**
 * Label for one route event kind.
 *
 * The default branch returns the raw kind on purpose: a newer server's event
 * must render as itself rather than as "Unknown", so an admin can see what
 * their route actually subscribes to.
 */
function eventLabel(t: ReturnType<typeof useT<"settings">>["t"], kind: string): string {
  switch (kind) {
    case "failed":
      return t(($) => $.slack.events.failed);
    case "qa_verdict":
      return t(($) => $.slack.events.qa_verdict);
    case "review_verdict":
      return t(($) => $.slack.events.review_verdict);
    case "assigned":
      return t(($) => $.slack.events.assigned);
    case "mentioned":
      return t(($) => $.slack.events.mentioned);
    case "agent_done":
      return t(($) => $.slack.events.agent_done);
    case "status_changed":
      return t(($) => $.slack.events.status_changed);
    case "created":
      return t(($) => $.slack.events.created);
    default:
      return kind;
  }
}
