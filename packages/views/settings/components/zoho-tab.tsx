"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Input } from "@agora/ui/components/ui/input";
import {
  NativeSelect,
  NativeSelectOption,
} from "@agora/ui/components/ui/native-select";
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
import { ApiError } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  ZOHO_DCS,
  zohoConnectionOptions,
  zohoSyncConfigsOptions,
  zohoUserBindingOptions,
  useDeleteZohoConnection,
  useDeleteZohoUserBinding,
  useSaveZohoConnection,
  useSaveZohoUserBinding,
} from "@agora/core/zoho";
import type { ZohoConnectionStatus } from "@agora/core/zoho";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

/**
 * Zoho integration in Settings → Integrations, stacked sections
 * (docs/zoho-dynamic-integration.md §1.5):
 *
 * 1. Workspace connection — sealed OAuth credentials; status member-visible,
 *    manage (save/rotate/disconnect) owner/admin only. The backend enforces
 *    the roles; the UI hides the affordances to match (Figma tab pattern).
 * 2. Your Zoho account — per-member self-client grant binding, self-service
 *    for every member.
 * 3. CRM module sync — owner/admin summary + link to the module manager on
 *    the Zoho page. The Projects/Sprints import deep-link card stays as-is.
 */
export function ZohoTab() {
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const { data: connection } = useQuery({
    ...zohoConnectionOptions(wsId),
    enabled: !!wsId,
  });

  return (
    <div className="space-y-4">
      <ZohoConnectionCard
        wsId={wsId}
        canManage={canManage}
        connection={connection}
      />
      <ZohoUserBindingCard wsId={wsId} connection={connection} />
      {canManage && (
        <ZohoModuleSyncCard
          wsId={wsId}
          configured={connection?.configured === true}
        />
      )}
      <ZohoImportCard />
    </div>
  );
}

// --- Section 1: workspace connection ----------------------------------------

function ZohoConnectionCard({
  wsId,
  canManage,
  connection,
}: {
  wsId: string;
  canManage: boolean;
  connection: ZohoConnectionStatus | undefined;
}) {
  const { t } = useT("settings");
  const saveMut = useSaveZohoConnection(wsId);
  const deleteMut = useDeleteZohoConnection(wsId);

  const [dc, setDc] = useState<string>("us");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [refreshToken, setRefreshToken] = useState("");
  const [scopes, setScopes] = useState("");
  const [showOrgIds, setShowOrgIds] = useState(false);
  const [crmOrgId, setCrmOrgId] = useState("");
  const [deskOrgId, setDeskOrgId] = useState("");
  const [projectsPortalId, setProjectsPortalId] = useState("");
  const [sprintsTeamId, setSprintsTeamId] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);

  const configured = connection?.configured === true;
  const canSave =
    clientId.trim() !== "" &&
    clientSecret.trim() !== "" &&
    refreshToken.trim() !== "";

  const save = async () => {
    if (!canSave || saveMut.isPending) return;
    setFormError(null);
    try {
      await saveMut.mutateAsync({
        dc,
        client_id: clientId.trim(),
        client_secret: clientSecret.trim(),
        refresh_token: refreshToken.trim(),
        scopes: scopes.trim() || undefined,
        crm_org_id: crmOrgId.trim() || undefined,
        desk_org_id: deskOrgId.trim() || undefined,
        projects_portal_id: projectsPortalId.trim() || undefined,
        sprints_team_id: sprintsTeamId.trim() || undefined,
      });
      // Secrets are write-only: clear them so they never linger in the DOM.
      setClientSecret("");
      setRefreshToken("");
      toast.success(t(($) => $.zoho.connection.toast_saved));
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) {
        setFormError(t(($) => $.zoho.connection.error_invalid_credentials));
      } else if (e instanceof ApiError && e.status === 503) {
        setFormError(t(($) => $.zoho.connection.error_sealing_unavailable));
      } else {
        setFormError(
          e instanceof Error && e.message
            ? e.message
            : t(($) => $.zoho.connection.error_save_failed),
        );
      }
    }
  };

  const disconnect = async () => {
    if (deleteMut.isPending) return;
    try {
      await deleteMut.mutateAsync();
      toast.success(t(($) => $.zoho.connection.toast_disconnected));
      setConfirmDisconnect(false);
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.zoho.connection.error_disconnect_failed),
      );
    }
  };

  return (
    <Card>
      <CardContent className="space-y-4 pt-5">
        <div className="space-y-1">
          <h3 className="text-sm font-medium">
            {t(($) => $.zoho.connection.title)}
          </h3>
          <p className="text-xs text-muted-foreground">
            {t(($) => $.zoho.connection.description)}
          </p>
        </div>

        {configured && connection ? (
          <div className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2 text-sm">
            <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] uppercase text-muted-foreground">
              {connection.dc}
            </span>
            <span className="truncate font-mono text-xs">
              {connection.client_id}
            </span>
            <ZohoProbeBadge status={connection.probe_status} />
            {canManage && (
              <Button
                variant="ghost"
                size="icon"
                className="ml-auto h-7 w-7 shrink-0"
                aria-label={t(($) => $.zoho.connection.disconnect)}
                onClick={() => setConfirmDisconnect(true)}
                disabled={deleteMut.isPending}
              >
                <Trash2 className="h-3.5 w-3.5 text-destructive" />
              </Button>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            {t(($) => $.zoho.connection.not_configured)}
          </p>
        )}

        {canManage && (
          <div className="space-y-2">
            <div className="grid grid-cols-2 gap-2">
              <NativeSelect
                className="w-full"
                aria-label={t(($) => $.zoho.connection.dc_label)}
                value={dc}
                onChange={(e) => setDc(e.target.value)}
              >
                {ZOHO_DCS.map((d) => (
                  <NativeSelectOption key={d} value={d}>
                    {d.toUpperCase()}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
              <Input
                autoComplete="off"
                placeholder={t(($) => $.zoho.connection.client_id_placeholder)}
                aria-label={t(($) => $.zoho.connection.client_id_label)}
                value={clientId}
                onChange={(e) => setClientId(e.target.value)}
              />
            </div>
            <Input
              type="password"
              autoComplete="off"
              placeholder={t(($) => $.zoho.connection.client_secret_label)}
              aria-label={t(($) => $.zoho.connection.client_secret_label)}
              value={clientSecret}
              onChange={(e) => setClientSecret(e.target.value)}
            />
            <Input
              type="password"
              autoComplete="off"
              placeholder={t(($) => $.zoho.connection.refresh_token_label)}
              aria-label={t(($) => $.zoho.connection.refresh_token_label)}
              value={refreshToken}
              onChange={(e) => setRefreshToken(e.target.value)}
            />
            <Input
              autoComplete="off"
              placeholder={t(($) => $.zoho.connection.scopes_placeholder)}
              aria-label={t(($) => $.zoho.connection.scopes_label)}
              value={scopes}
              onChange={(e) => setScopes(e.target.value)}
            />
            <button
              type="button"
              className="text-[11px] text-muted-foreground underline-offset-2 hover:underline"
              onClick={() => setShowOrgIds((v) => !v)}
            >
              {t(($) => $.zoho.connection.advanced_toggle)}
            </button>
            {showOrgIds && (
              <div className="grid grid-cols-2 gap-2">
                <Input
                  autoComplete="off"
                  placeholder={t(($) => $.zoho.connection.crm_org_id_label)}
                  aria-label={t(($) => $.zoho.connection.crm_org_id_label)}
                  value={crmOrgId}
                  onChange={(e) => setCrmOrgId(e.target.value)}
                />
                <Input
                  autoComplete="off"
                  placeholder={t(($) => $.zoho.connection.desk_org_id_label)}
                  aria-label={t(($) => $.zoho.connection.desk_org_id_label)}
                  value={deskOrgId}
                  onChange={(e) => setDeskOrgId(e.target.value)}
                />
                <Input
                  autoComplete="off"
                  placeholder={t(
                    ($) => $.zoho.connection.projects_portal_id_label,
                  )}
                  aria-label={t(
                    ($) => $.zoho.connection.projects_portal_id_label,
                  )}
                  value={projectsPortalId}
                  onChange={(e) => setProjectsPortalId(e.target.value)}
                />
                <Input
                  autoComplete="off"
                  placeholder={t(($) => $.zoho.connection.sprints_team_id_label)}
                  aria-label={t(($) => $.zoho.connection.sprints_team_id_label)}
                  value={sprintsTeamId}
                  onChange={(e) => setSprintsTeamId(e.target.value)}
                />
              </div>
            )}
            {formError && (
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
                {formError}
              </p>
            )}
            <Button onClick={save} disabled={saveMut.isPending || !canSave} size="sm">
              {saveMut.isPending
                ? t(($) => $.zoho.connection.saving)
                : t(($) => $.zoho.connection.save)}
            </Button>
          </div>
        )}

        <AlertDialog
          open={confirmDisconnect}
          onOpenChange={(v) => {
            if (!v && !deleteMut.isPending) setConfirmDisconnect(false);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t(($) => $.zoho.connection.disconnect_confirm_title)}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.zoho.connection.disconnect_confirm_description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={deleteMut.isPending}>
                {t(($) => $.zoho.connection.cancel)}
              </AlertDialogCancel>
              <AlertDialogAction onClick={disconnect} disabled={deleteMut.isPending}>
                {deleteMut.isPending
                  ? t(($) => $.zoho.connection.disconnecting)
                  : t(($) => $.zoho.connection.disconnect)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}

// --- Section 2: personal binding ---------------------------------------------

function ZohoUserBindingCard({
  wsId,
  connection,
}: {
  wsId: string;
  connection: ZohoConnectionStatus | undefined;
}) {
  const { t } = useT("settings");
  const { data: binding } = useQuery({
    ...zohoUserBindingOptions(wsId),
    enabled: !!wsId,
  });
  const bindMut = useSaveZohoUserBinding(wsId);
  const unbindMut = useDeleteZohoUserBinding(wsId);

  const [grantCode, setGrantCode] = useState("");
  const [bindError, setBindError] = useState<string | null>(null);

  const bound = binding?.bound === true;
  const configured = connection?.configured === true;

  const connect = async () => {
    if (!grantCode.trim() || bindMut.isPending) return;
    setBindError(null);
    try {
      await bindMut.mutateAsync(grantCode.trim());
      setGrantCode("");
      toast.success(t(($) => $.zoho.binding.toast_bound));
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) {
        setBindError(t(($) => $.zoho.binding.error_grant_invalid));
      } else {
        // 400 carries a meaningful server message (e.g. "workspace zoho
        // connection must be configured before binding user accounts").
        setBindError(
          e instanceof Error && e.message
            ? e.message
            : t(($) => $.zoho.binding.error_connect_failed),
        );
      }
    }
  };

  const unbind = async () => {
    if (unbindMut.isPending) return;
    try {
      await unbindMut.mutateAsync();
      toast.success(t(($) => $.zoho.binding.toast_unbound));
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.zoho.binding.error_unbind_failed),
      );
    }
  };

  return (
    <Card>
      <CardContent className="space-y-4 pt-5">
        <div className="space-y-1">
          <h3 className="text-sm font-medium">{t(($) => $.zoho.binding.title)}</h3>
          <p className="text-xs text-muted-foreground">
            {t(($) => $.zoho.binding.description)}
          </p>
        </div>

        {bound && binding ? (
          <div className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2 text-sm">
            <span className="truncate font-medium">
              {binding.zoho_user_email !== ""
                ? t(($) => $.zoho.binding.bound_as, {
                    email: binding.zoho_user_email,
                  })
                : t(($) => $.zoho.binding.bound)}
            </span>
            <ZohoProbeBadge status={binding.probe_status} />
            <Button
              variant="ghost"
              size="icon"
              className="ml-auto h-7 w-7 shrink-0"
              aria-label={t(($) => $.zoho.binding.unbind)}
              onClick={unbind}
              disabled={unbindMut.isPending}
            >
              <Trash2 className="h-3.5 w-3.5 text-destructive" />
            </Button>
          </div>
        ) : !configured ? (
          <p className="text-sm text-muted-foreground">
            {t(($) => $.zoho.binding.requires_connection)}
          </p>
        ) : (
          <div className="space-y-2">
            <Input
              type="password"
              autoComplete="off"
              placeholder={t(($) => $.zoho.binding.grant_code_placeholder)}
              aria-label={t(($) => $.zoho.binding.grant_code_label)}
              value={grantCode}
              onChange={(e) => setGrantCode(e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">
              {t(($) => $.zoho.binding.grant_help)}
            </p>
            {connection?.scopes ? (
              <p className="break-all font-mono text-[11px] text-muted-foreground">
                {t(($) => $.zoho.binding.scopes_hint, {
                  scopes: connection.scopes,
                })}
              </p>
            ) : null}
            {bindError && (
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
                {bindError}
              </p>
            )}
            <Button
              onClick={connect}
              disabled={bindMut.isPending || !grantCode.trim()}
              size="sm"
            >
              {bindMut.isPending
                ? t(($) => $.zoho.binding.connecting)
                : t(($) => $.zoho.binding.connect)}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// --- Section 3: CRM module sync summary (owner/admin) ------------------------

function ZohoModuleSyncCard({
  wsId,
  configured,
}: {
  wsId: string;
  configured: boolean;
}) {
  const { t } = useT("settings");
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  const { data: configs = [] } = useQuery({
    ...zohoSyncConfigsOptions(wsId),
    enabled: !!wsId && configured,
  });
  const activeCount = configs.filter((c) => c.enabled === true).length;

  return (
    <Card>
      <CardContent className="space-y-3 pt-5">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <h3 className="text-sm font-medium">
              {t(($) => $.zoho.modules.title)}
            </h3>
            <p className="max-w-prose text-xs text-muted-foreground">
              {t(($) => $.zoho.modules.description)}
            </p>
            <p className="text-xs text-muted-foreground">
              {configured
                ? t(($) => $.zoho.modules.synced_count, { count: activeCount })
                : t(($) => $.zoho.modules.requires_connection)}
            </p>
          </div>
          <Button
            size="sm"
            variant="outline"
            className="shrink-0"
            disabled={!configured}
            onClick={() => nav.push(paths.zoho())}
          >
            {t(($) => $.zoho.modules.manage)}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- Section 4: Projects/Sprints import deep link (pre-existing behavior) ----

function ZohoImportCard() {
  const { t } = useT("settings");
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  return (
    <Card>
      <CardContent className="pt-5">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <h3 className="text-sm font-medium">{t(($) => $.zoho.import.title)}</h3>
            <p className="max-w-prose text-xs text-muted-foreground">
              {t(($) => $.zoho.import.description)}
            </p>
          </div>
          <Button size="sm" className="shrink-0" onClick={() => nav.push(paths.zoho())}>
            {t(($) => $.zoho.import.open)}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- Shared probe badge -------------------------------------------------------

/** Probe badge for connection/binding status. Unknown server-side statuses
 * render nothing rather than a wrong badge (enum-drift rule). */
function ZohoProbeBadge({ status }: { status: string | undefined }) {
  const { t } = useT("settings");
  switch (status) {
    case "ok":
      return (
        <span className="rounded bg-success/15 px-1.5 py-0.5 text-[10px] font-medium text-success">
          {t(($) => $.zoho.probe.ok)}
        </span>
      );
    case "invalid":
      return (
        <span className="rounded bg-destructive/15 px-1.5 py-0.5 text-[10px] font-medium text-destructive">
          {t(($) => $.zoho.probe.invalid)}
        </span>
      );
    case "unreachable":
      return (
        <span className="rounded bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning">
          {t(($) => $.zoho.probe.unreachable)}
        </span>
      );
    default:
      return null;
  }
}
