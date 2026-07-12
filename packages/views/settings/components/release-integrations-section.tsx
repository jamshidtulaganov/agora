"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Webhook, Trash2, Plus } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
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
import { api } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ReleaseIntegration } from "@agora/core/types";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useT } from "../../i18n";

// Release integrations (release-hub Thread B / Phase 2). A workspace admin wires
// outbound webhooks that fire on release-lifecycle events (deploy:recorded /
// release:shipped). The webhook URL + optional signing secret are write-only —
// the list only reports has_secret. Status is member-visible; add/remove are
// admin-only (the backend enforces it, the UI hides the affordances to match).
const ALL_EVENTS = ["deploy_recorded", "release_shipped"] as const;

export function ReleaseIntegrationsSection() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const { data: integrations = [] } = useQuery({
    queryKey: ["release-integrations", wsId],
    queryFn: () => api.listReleaseIntegrations(wsId),
    enabled: !!wsId,
  });

  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [events, setEvents] = useState<string[]>([...ALL_EVENTS]);
  const [saving, setSaving] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<ReleaseIntegration | null>(null);
  const [removing, setRemoving] = useState(false);

  // Event labels resolved here (where `t` is bound to the settings namespace);
  // an unknown short name from a newer backend falls back to the raw string.
  const eventLabels: Record<string, string> = {
    deploy_recorded: t(($) => $.release_integrations.event_deploy_recorded),
    release_shipped: t(($) => $.release_integrations.event_release_shipped),
  };
  const eventLabel = (ev: string) => eventLabels[ev] ?? ev;

  const refresh = () => qc.invalidateQueries({ queryKey: ["release-integrations", wsId] });

  const toggleEvent = (ev: string) =>
    setEvents((prev) => (prev.includes(ev) ? prev.filter((e) => e !== ev) : [...prev, ev]));

  const add = async () => {
    if (!url.trim()) {
      toast.error(t(($) => $.release_integrations.error_url_required));
      return;
    }
    if (events.length === 0) {
      toast.error(t(($) => $.release_integrations.error_at_least_one_event));
      return;
    }
    setSaving(true);
    try {
      await api.createReleaseIntegration(wsId, {
        name: name.trim() || undefined,
        url: url.trim(),
        secret: secret.trim() || undefined,
        events,
      });
      setName("");
      setUrl("");
      setSecret("");
      setEvents([...ALL_EVENTS]);
      refresh();
      toast.success(t(($) => $.release_integrations.toast_added));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.release_integrations.toast_add_failed));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!pendingDelete || removing) return;
    setRemoving(true);
    try {
      await api.deleteReleaseIntegration(wsId, pendingDelete.id);
      setPendingDelete(null);
      refresh();
      toast.success(t(($) => $.release_integrations.toast_removed));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.release_integrations.toast_remove_failed));
    } finally {
      setRemoving(false);
    }
  };

  return (
    <section className="space-y-4">
      <h2 className="flex items-center gap-1.5 text-sm font-semibold">
        <Webhook className="h-4 w-4" /> {t(($) => $.release_integrations.section_title)}
      </h2>
      <Card>
        <CardContent className="space-y-4 pt-5">
          <p className="text-xs text-muted-foreground">{t(($) => $.release_integrations.description)}</p>

          {integrations.length > 0 ? (
            <ul className="divide-y divide-border rounded-md border border-border">
              {integrations.map((it) => (
                <li key={it.id} className="flex items-center gap-2 px-3 py-2 text-sm">
                  <span className="truncate font-medium">
                    {it.config?.name || t(($) => $.release_integrations.unnamed)}
                  </span>
                  {!it.enabled && (
                    <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                      {t(($) => $.release_integrations.disabled)}
                    </span>
                  )}
                  <span className="truncate text-xs text-muted-foreground">
                    {it.events.map((e) => eventLabel(e)).join(" · ")}
                  </span>
                  <ProbeStatusBadge probeStatus={it.probe_status} />
                  {canManage && (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="ml-auto h-7 w-7 shrink-0"
                      aria-label={t(($) => $.release_integrations.remove)}
                      onClick={() => setPendingDelete(it)}
                    >
                      <Trash2 className="h-3.5 w-3.5 text-destructive" />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-muted-foreground">{t(($) => $.release_integrations.not_configured)}</p>
          )}

          {canManage && (
            <div className="space-y-2">
              <Input
                placeholder={t(($) => $.release_integrations.name_placeholder)}
                aria-label={t(($) => $.release_integrations.name_label)}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <Input
                type="url"
                autoComplete="off"
                placeholder={t(($) => $.release_integrations.url_placeholder)}
                aria-label={t(($) => $.release_integrations.url_label)}
                value={url}
                onChange={(e) => setUrl(e.target.value)}
              />
              <Input
                type="password"
                autoComplete="off"
                placeholder={t(($) => $.release_integrations.secret_placeholder)}
                aria-label={t(($) => $.release_integrations.secret_label)}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t(($) => $.release_integrations.secret_hint)}</p>
              <div className="flex flex-wrap gap-4 pt-1">
                {ALL_EVENTS.map((ev) => (
                  <label key={ev} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={events.includes(ev)}
                      onCheckedChange={() => toggleEvent(ev)}
                      aria-label={eventLabel(ev)}
                    />
                    {eventLabel(ev)}
                  </label>
                ))}
              </div>
              <Button onClick={add} disabled={saving || !url.trim()} size="sm">
                <Plus className="mr-1 h-3.5 w-3.5" />
                {saving ? t(($) => $.release_integrations.adding) : t(($) => $.release_integrations.add)}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      <AlertDialog open={!!pendingDelete} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.release_integrations.remove_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.release_integrations.remove_confirm_body, {
                name: pendingDelete?.config?.name || t(($) => $.release_integrations.unnamed),
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={removing}>
              {t(($) => $.release_integrations.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={remove} disabled={removing}>
              {t(($) => $.release_integrations.confirm_remove)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function ProbeStatusBadge({ probeStatus }: { probeStatus: string }) {
  const { t } = useT("settings");
  // Unknown server-side statuses render nothing rather than a wrong badge.
  switch (probeStatus) {
    case "ok":
      return (
        <span className="shrink-0 rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
          {t(($) => $.release_integrations.status_ok)}
        </span>
      );
    case "invalid":
      return (
        <span className="shrink-0 rounded bg-destructive/15 px-1.5 py-0.5 text-[10px] font-medium text-destructive">
          {t(($) => $.release_integrations.status_invalid)}
        </span>
      );
    case "unreachable":
      return (
        <span className="shrink-0 rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
          {t(($) => $.release_integrations.status_unreachable)}
        </span>
      );
    default:
      return null;
  }
}
