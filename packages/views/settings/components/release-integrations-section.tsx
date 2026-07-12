"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Trash2, Plus } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Button } from "@agora/ui/components/ui/button";
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
import { api } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ReleaseIntegration, ReleaseIntegrationInput } from "@agora/core/types";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useT } from "../../i18n";

// Release integrations (release-hub Thread B / Phase 3-4). A workspace admin
// wires outbound connectors that fire on release-lifecycle events
// (deploy:recorded / release:shipped). Six connector kinds are supported; each
// carries kind-specific NON-secret config plus a sealed, write-only secret the
// list never returns (only has_secret). Status is member-visible; add/remove are
// admin-only (the backend enforces it, the UI hides the affordances to match).
const ALL_EVENTS = ["deploy_recorded", "release_shipped"] as const;
const KINDS = ["webhook", "slack", "github_release", "gitlab_release", "sentry", "bitrix"] as const;
type Kind = (typeof KINDS)[number];
type SettingsT = ReturnType<typeof useT<"settings">>["t"];

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

  const [kind, setKind] = useState<Kind>("webhook");
  const [name, setName] = useState("");
  // Kind-specific string fields live in one map so switching kind can clear any
  // secret typed for the previous kind in a single reset.
  const [fields, setFields] = useState<Record<string, string>>({});
  const [events, setEvents] = useState<string[]>([...ALL_EVENTS]);
  const [saving, setSaving] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<ReleaseIntegration | null>(null);
  const [removing, setRemoving] = useState(false);

  const field = (k: string) => fields[k] ?? "";
  const setField = (k: string, v: string) => setFields((prev) => ({ ...prev, [k]: v }));

  const eventLabels: Record<string, string> = {
    deploy_recorded: t(($) => $.release_integrations.event_deploy_recorded),
    release_shipped: t(($) => $.release_integrations.event_release_shipped),
  };
  const eventLabel = (ev: string) => eventLabels[ev] ?? ev;

  const kindLabels: Record<string, string> = {
    webhook: t(($) => $.release_integrations.kind_webhook),
    slack: t(($) => $.release_integrations.kind_slack),
    github_release: t(($) => $.release_integrations.kind_github_release),
    gitlab_release: t(($) => $.release_integrations.kind_gitlab_release),
    sentry: t(($) => $.release_integrations.kind_sentry),
    bitrix: t(($) => $.release_integrations.kind_bitrix),
  };
  const kindLabel = (k: string) => kindLabels[k] ?? k;

  const refresh = () => qc.invalidateQueries({ queryKey: ["release-integrations", wsId] });

  const toggleEvent = (ev: string) =>
    setEvents((prev) => (prev.includes(ev) ? prev.filter((e) => e !== ev) : [...prev, ev]));

  const changeKind = (k: Kind) => {
    setKind(k);
    setFields({});
  };

  const resetForm = () => {
    setKind("webhook");
    setName("");
    setFields({});
    setEvents([...ALL_EVENTS]);
  };

  // canSubmit gates the button on the chosen kind's required fields (the backend
  // re-validates + probes; this is just an early affordance).
  const canSubmit = (() => {
    switch (kind) {
      case "webhook":
        return !!field("url").trim();
      case "slack":
        return !!field("webhook_url").trim();
      case "github_release":
        return !!(field("owner").trim() && field("repo").trim() && field("token").trim());
      case "gitlab_release":
        return !!(field("project_path").trim() && field("token").trim());
      case "sentry":
        return !!(field("org").trim() && field("project").trim() && field("token").trim());
      case "bitrix":
        return true;
      default:
        return false;
    }
  })();

  const buildPayload = (): ReleaseIntegrationInput => {
    const base: ReleaseIntegrationInput = { kind, name: name.trim() || undefined, events };
    switch (kind) {
      case "webhook":
        return { ...base, url: field("url").trim(), secret: field("secret").trim() || undefined };
      case "slack":
        return { ...base, webhook_url: field("webhook_url").trim(), channel_hint: field("channel_hint").trim() || undefined };
      case "github_release":
        return { ...base, owner: field("owner").trim(), repo: field("repo").trim(), token: field("token").trim() };
      case "gitlab_release":
        return { ...base, host: field("host").trim() || undefined, project_path: field("project_path").trim(), token: field("token").trim() };
      case "sentry":
        return { ...base, base_url: field("base_url").trim() || undefined, org: field("org").trim(), project: field("project").trim(), token: field("token").trim() };
      case "bitrix":
        return base;
      default:
        return base;
    }
  };

  const add = async () => {
    if (!canSubmit) {
      toast.error(t(($) => $.release_integrations.error_required_fields));
      return;
    }
    if (events.length === 0) {
      toast.error(t(($) => $.release_integrations.error_at_least_one_event));
      return;
    }
    setSaving(true);
    try {
      await api.createReleaseIntegration(wsId, buildPayload());
      resetForm();
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
    <>
      <div className="space-y-4">
        <p className="text-xs text-muted-foreground">{t(($) => $.release_integrations.description)}</p>

        {integrations.length > 0 ? (
          <ul className="divide-y divide-border rounded-md border border-border">
            {integrations.map((it) => (
              <li key={it.id} className="flex items-center gap-2 px-3 py-2 text-sm">
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                  {kindLabel(it.kind)}
                </span>
                <span className="truncate font-medium">
                  {it.config?.name || t(($) => $.release_integrations.unnamed)}
                </span>
                <ConfigSummary integration={it} />
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
            <NativeSelect
              className="w-full"
              aria-label={t(($) => $.release_integrations.kind_label)}
              value={kind}
              onChange={(e) => changeKind(e.target.value as Kind)}
            >
              {KINDS.map((k) => (
                <NativeSelectOption key={k} value={k}>
                  {kindLabel(k)}
                </NativeSelectOption>
              ))}
            </NativeSelect>

            <Input
              placeholder={t(($) => $.release_integrations.name_placeholder)}
              aria-label={t(($) => $.release_integrations.name_label)}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />

            <KindFields kind={kind} field={field} setField={setField} t={t} />

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
            <Button onClick={add} disabled={saving || !canSubmit} size="sm">
              <Plus className="mr-1 h-3.5 w-3.5" />
              {saving ? t(($) => $.release_integrations.adding) : t(($) => $.release_integrations.add)}
            </Button>
          </div>
        )}
      </div>

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
    </>
  );
}

// KindFields renders the inputs specific to the selected connector kind. Secret
// fields are write-only password inputs (cleared on kind change + after save).
function KindFields({
  kind,
  field,
  setField,
  t,
}: {
  kind: Kind;
  field: (k: string) => string;
  setField: (k: string, v: string) => void;
  t: SettingsT;
}) {
  const text = (
    key: string,
    label: string,
    placeholder: string,
    opts?: { password?: boolean },
  ) => (
    <Input
      type={opts?.password ? "password" : "text"}
      autoComplete="off"
      aria-label={label}
      placeholder={placeholder}
      value={field(key)}
      onChange={(e) => setField(key, e.target.value)}
    />
  );

  switch (kind) {
    case "webhook":
      return (
        <>
          {text("url", t(($) => $.release_integrations.url_label), t(($) => $.release_integrations.url_placeholder))}
          {text("secret", t(($) => $.release_integrations.secret_label), t(($) => $.release_integrations.secret_placeholder), { password: true })}
          <p className="text-[11px] text-muted-foreground">{t(($) => $.release_integrations.secret_hint)}</p>
        </>
      );
    case "slack":
      return (
        <>
          {text("webhook_url", t(($) => $.release_integrations.webhook_url_label), t(($) => $.release_integrations.webhook_url_placeholder), { password: true })}
          {text("channel_hint", t(($) => $.release_integrations.channel_hint_label), t(($) => $.release_integrations.channel_hint_placeholder))}
        </>
      );
    case "github_release":
      return (
        <>
          {text("owner", t(($) => $.release_integrations.owner_label), t(($) => $.release_integrations.owner_placeholder))}
          {text("repo", t(($) => $.release_integrations.repo_label), t(($) => $.release_integrations.repo_placeholder))}
          {text("token", t(($) => $.release_integrations.token_label), t(($) => $.release_integrations.token_placeholder), { password: true })}
          <p className="text-[11px] text-muted-foreground">{t(($) => $.release_integrations.token_hint)}</p>
        </>
      );
    case "gitlab_release":
      return (
        <>
          {text("host", t(($) => $.release_integrations.host_label), t(($) => $.release_integrations.host_placeholder))}
          {text("project_path", t(($) => $.release_integrations.project_path_label), t(($) => $.release_integrations.project_path_placeholder))}
          {text("token", t(($) => $.release_integrations.token_label), t(($) => $.release_integrations.token_placeholder), { password: true })}
          <p className="text-[11px] text-muted-foreground">{t(($) => $.release_integrations.token_hint)}</p>
        </>
      );
    case "sentry":
      return (
        <>
          {text("base_url", t(($) => $.release_integrations.base_url_label), t(($) => $.release_integrations.base_url_placeholder))}
          {text("org", t(($) => $.release_integrations.org_label), t(($) => $.release_integrations.org_placeholder))}
          {text("project", t(($) => $.release_integrations.project_label), t(($) => $.release_integrations.project_placeholder))}
          {text("token", t(($) => $.release_integrations.token_label), t(($) => $.release_integrations.token_placeholder), { password: true })}
          <p className="text-[11px] text-muted-foreground">{t(($) => $.release_integrations.token_hint)}</p>
        </>
      );
    case "bitrix":
      return <p className="text-[11px] text-muted-foreground">{t(($) => $.release_integrations.bitrix_note)}</p>;
    default:
      return null;
  }
}

// ConfigSummary renders a compact, kind-specific non-secret summary of a stored
// integration (owner/repo, project path, org · project, channel).
function ConfigSummary({ integration }: { integration: ReleaseIntegration }) {
  const { config, kind } = integration;
  let summary = "";
  switch (kind) {
    case "github_release":
      summary = [config.owner, config.repo].filter(Boolean).join("/");
      break;
    case "gitlab_release":
      summary = config.project_path ?? "";
      break;
    case "sentry":
      summary = [config.org, config.project].filter(Boolean).join(" · ");
      break;
    case "slack":
      summary = config.channel_hint ?? "";
      break;
    default:
      summary = "";
  }
  if (!summary) return null;
  return <span className="truncate text-xs text-muted-foreground">{summary}</span>;
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
