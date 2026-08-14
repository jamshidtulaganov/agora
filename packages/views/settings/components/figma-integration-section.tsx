"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ExternalLink, Trash2 } from "lucide-react";
import { Input } from "@agora/ui/components/ui/input";
import { Button } from "@agora/ui/components/ui/button";
import { api, ApiError } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useT } from "../../i18n";
import { openExternal } from "../../platform";

const FIGMA_TOKEN_HELP_URL =
  "https://help.figma.com/hc/en-us/articles/8085703771159-Manage-personal-access-tokens";

// Workspace Figma credential: one PAT that lets agents read Figma designs
// referenced by issues (the backend fills the agent's MCP config at claim
// time). Status is member-visible; save/remove are admin-only — the backend
// enforces it, the UI hides the affordances to match. The token is write-only
// and never re-displayed.
export function FigmaIntegrationSection() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const { data: status } = useQuery({
    queryKey: ["figma-credential", wsId],
    queryFn: () => api.getFigmaCredentialStatus(wsId),
    enabled: !!wsId,
  });
  const configured = status?.configured === true;

  const [token, setToken] = useState("");
  const [label, setLabel] = useState("");
  const [expiresAt, setExpiresAt] = useState(defaultExpiryDate());
  const [probeFileKey, setProbeFileKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);

  const refresh = () => qc.invalidateQueries({ queryKey: ["figma-credential", wsId] });

  const save = async () => {
    if (!token.trim() || saving) return;
    setSaving(true);
    try {
      await api.putFigmaCredential(wsId, {
        token: token.trim(),
        label: label.trim() || undefined,
        expires_at: expiresAt || undefined,
        probe_file_key: probeFileKey.trim() || undefined,
      });
      setToken("");
      setProbeFileKey("");
      refresh();
      toast.success(t(($) => $.figma.toast_saved));
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) {
        toast.error(t(($) => $.figma.error_invalid_token));
      } else {
        toast.error(e instanceof Error ? e.message : t(($) => $.figma.toast_save_failed));
      }
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (removing) return;
    setRemoving(true);
    try {
      await api.deleteFigmaCredential(wsId);
      refresh();
      toast.success(t(($) => $.figma.toast_removed));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.figma.toast_remove_failed));
    } finally {
      setRemoving(false);
    }
  };

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">{t(($) => $.figma.description)}</p>

      {configured && status ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2 text-sm">
          <span className="truncate font-medium">{status.label}</span>
          {status.token_last4 && (
            <span className="font-mono text-xs text-muted-foreground">…{status.token_last4}</span>
          )}
          {status.expires_at && (
            <span className="text-xs text-muted-foreground">
              {t(($) => $.figma.expires_on, { date: status.expires_at.slice(0, 10) })}
            </span>
          )}
          {status.expiring_soon === true && (
            <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
              {t(($) => $.figma.expiring_soon)}
            </span>
          )}
          <ProbeStatusBadge probeStatus={status.probe_status} />
          {canManage && (
            <Button
              variant="ghost"
              size="icon"
              className="ml-auto h-7 w-7 shrink-0"
              aria-label={t(($) => $.figma.remove)}
              onClick={remove}
              disabled={removing}
            >
              <Trash2 className="h-3.5 w-3.5 text-destructive" />
            </Button>
          )}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">{t(($) => $.figma.not_configured)}</p>
      )}

      {configured && status?.seat_probe === "low_seat" && (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {t(($) => $.figma.seat_warning)}
        </p>
      )}

      {!configured && !canManage && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.figma.member_read_only)}
        </p>
      )}

      {canManage && (
        <div className="space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-[11px] text-muted-foreground">
              {t(($) => $.figma.token_help)}
            </p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-7 gap-1.5"
              onClick={() => openExternal(FIGMA_TOKEN_HELP_URL)}
            >
              {t(($) => $.figma.token_help_link)}
              <ExternalLink className="size-3" aria-hidden="true" />
            </Button>
          </div>
          <Input
            type="password"
            autoComplete="off"
            placeholder={t(($) => $.figma.token_placeholder)}
            aria-label={t(($) => $.figma.token_label)}
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <div className="grid grid-cols-2 gap-2">
            <Input
              placeholder={t(($) => $.figma.label_placeholder)}
              aria-label={t(($) => $.figma.label_label)}
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
            <Input
              type="date"
              aria-label={t(($) => $.figma.expires_label)}
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </div>
          <p className="text-[11px] text-muted-foreground">{t(($) => $.figma.expires_hint)}</p>
          <Input
            placeholder={t(($) => $.figma.probe_file_label)}
            aria-label={t(($) => $.figma.probe_file_label)}
            value={probeFileKey}
            onChange={(e) => setProbeFileKey(e.target.value)}
          />
          <p className="text-[11px] text-muted-foreground">{t(($) => $.figma.probe_file_hint)}</p>
          <Button onClick={save} disabled={saving || !token.trim()} size="sm">
            {saving ? t(($) => $.figma.saving) : t(($) => $.figma.save)}
          </Button>
        </div>
      )}
    </div>
  );
}

function ProbeStatusBadge({ probeStatus }: { probeStatus: string }) {
  const { t } = useT("settings");
  // Unknown server-side statuses render nothing rather than a wrong badge.
  switch (probeStatus) {
    case "ok":
      return (
        <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
          {t(($) => $.figma.status_ok)}
        </span>
      );
    case "invalid":
      return (
        <span className="rounded bg-destructive/15 px-1.5 py-0.5 text-[10px] font-medium text-destructive">
          {t(($) => $.figma.status_invalid)}
        </span>
      );
    case "expired":
      return (
        <span className="rounded bg-destructive/15 px-1.5 py-0.5 text-[10px] font-medium text-destructive">
          {t(($) => $.figma.status_expired)}
        </span>
      );
    case "unreachable":
      return (
        <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
          {t(($) => $.figma.status_unreachable)}
        </span>
      );
    default:
      return null;
  }
}

// Figma PATs cap at 90 days; prefill the expiry so the renewal warning
// machinery has a date even when the operator doesn't know the exact one.
function defaultExpiryDate(): string {
  const d = new Date();
  d.setDate(d.getDate() + 90);
  return d.toISOString().slice(0, 10);
}
