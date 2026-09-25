"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Copy, Trash2 } from "lucide-react";
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
import { copyText } from "@agora/ui/lib/clipboard";
import { ApiError } from "@agora/core/api";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  ZOHO_DCS,
  zohoConnectionOptions,
  zohoSyncConfigsOptions,
  useDeleteZohoConnection,
  useSaveZohoConnection,
} from "@agora/core/zoho";
import type { ZohoConnectionStatus } from "@agora/core/zoho";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { ZohoAccountCard } from "./zoho-account-card";

/**
 * Zoho integration in Settings → Integrations, stacked sections:
 *
 * 1. Zoho connector — the workspace's Zoho client. Owner/admin set it up
 *    (and edit/remove it); plain members only see whether it is ready. The
 *    backend enforces the roles; the UI hides the affordances to match.
 * 2. Your Zoho account — the person's own, read-only account. It works in
 *    every workspace; connecting goes through this workspace's connector.
 * 3. CRM module sync — owner/admin, only once the connector carries the
 *    org-level sync grant (the sync cannot run without it).
 * 4. Projects/Sprints import deep link.
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
  const canSync =
    connection?.configured === true && connection?.has_sync_grant === true;

  return (
    <div className="space-y-4">
      <ZohoConnectorCard
        wsId={wsId}
        canManage={canManage}
        connection={connection}
      />
      <ZohoAccountCard wsId={wsId} />
      {canManage && canSync && <ZohoModuleSyncCard wsId={wsId} />}
      <ZohoImportCard />
    </div>
  );
}

// --- Section 1: Zoho connector -----------------------------------------------

function ZohoConnectorCard({
  wsId,
  canManage,
  connection,
}: {
  wsId: string;
  canManage: boolean;
  connection: ZohoConnectionStatus | undefined;
}) {
  const { t } = useT("settings");
  const deleteMut = useDeleteZohoConnection(wsId);
  const [editing, setEditing] = useState(false);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);

  const configured = connection?.configured === true;
  const redirectUri = connection?.redirect_uri?.trim() ?? "";
  const hasSyncGrant = connection?.has_sync_grant === true;

  const disconnect = async () => {
    if (deleteMut.isPending) return;
    try {
      await deleteMut.mutateAsync();
      toast.success(t(($) => $.zoho.connection.toast_disconnected));
      setConfirmDisconnect(false);
      setEditing(false);
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.zoho.connection.error_disconnect_failed),
      );
    }
  };

  const title = (
    <h3 className="text-sm font-medium">{t(($) => $.zoho.connection.title)}</h3>
  );

  // Status still loading: title only, so neither the setup steps nor the
  // member line flash before the real state arrives.
  if (!connection) {
    return (
      <Card>
        <CardContent className="pt-5">{title}</CardContent>
      </Card>
    );
  }

  if (!canManage) {
    return (
      <Card>
        <CardContent className="space-y-1 pt-5">
          {title}
          <p className="text-xs text-muted-foreground">
            {configured
              ? t(($) => $.zoho.connection.member_ready)
              : t(($) => $.zoho.connection.member_not_configured)}
          </p>
        </CardContent>
      </Card>
    );
  }

  if (!configured) {
    return (
      <Card>
        <CardContent className="space-y-4 pt-5">
          <div className="space-y-1">
            {title}
            <p className="text-xs text-muted-foreground">
              {t(($) => $.zoho.connection.description_setup)}
            </p>
          </div>
          <ol className="list-decimal space-y-2 pl-4 text-xs">
            <li>{t(($) => $.zoho.connection.step_create)}</li>
            {redirectUri && (
              <li className="space-y-1.5">
                <span>{t(($) => $.zoho.connection.step_redirect)}</span>
                <ZohoCopyField value={redirectUri} />
              </li>
            )}
            <li>{t(($) => $.zoho.connection.step_credentials)}</li>
          </ol>
          <ZohoConnectorForm wsId={wsId} connection={connection} />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardContent className="space-y-4 pt-5">
        <div className="space-y-1">
          {title}
          <p className="text-xs text-muted-foreground">
            {t(($) => $.zoho.connection.description_ready)}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2 text-sm">
          <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] uppercase text-muted-foreground">
            {connection.dc}
          </span>
          <span className="min-w-0 truncate font-mono text-xs">
            {connection.client_id}
          </span>
          <ZohoProbeBadge status={connection.probe_status} />
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {!editing && (
              <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
                {t(($) => $.zoho.connection.edit)}
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7"
              aria-label={t(($) => $.zoho.connection.disconnect)}
              onClick={() => setConfirmDisconnect(true)}
              disabled={deleteMut.isPending}
            >
              <Trash2 className="h-3.5 w-3.5 text-destructive" />
            </Button>
          </div>
        </div>

        {redirectUri && (
          <div className="space-y-1.5">
            <p className="text-xs text-muted-foreground">
              {t(($) => $.zoho.connection.redirect_uri_label)}
            </p>
            <ZohoCopyField value={redirectUri} />
          </div>
        )}

        <p className="text-xs text-muted-foreground">
          {hasSyncGrant
            ? t(($) => $.zoho.connection.sync_on)
            : t(($) => $.zoho.connection.sync_off)}
        </p>

        {editing && (
          <ZohoConnectorForm
            wsId={wsId}
            connection={connection}
            onDone={() => setEditing(false)}
          />
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

/** Setup / edit form for the connector (owner/admin). Editing starts from
 * the saved data center, Client ID and org IDs; secrets are write-only and
 * always typed again. `onDone` (edit mode only) closes the form: it offers
 * Cancel and runs after a successful save. */
function ZohoConnectorForm({
  wsId,
  connection,
  onDone,
}: {
  wsId: string;
  connection: ZohoConnectionStatus;
  onDone?: () => void;
}) {
  const { t } = useT("settings");
  const saveMut = useSaveZohoConnection(wsId);
  const configured = connection.configured === true;
  const knownDc = (ZOHO_DCS as readonly string[]).includes(connection.dc);

  const [dc, setDc] = useState<string>(configured && knownDc ? connection.dc : "us");
  const [clientId, setClientId] = useState(configured ? connection.client_id : "");
  const [clientSecret, setClientSecret] = useState("");
  const [refreshToken, setRefreshToken] = useState("");
  const [showOrgIds, setShowOrgIds] = useState(false);
  const [crmOrgId, setCrmOrgId] = useState(configured ? connection.crm_org_id : "");
  const [deskOrgId, setDeskOrgId] = useState(configured ? connection.desk_org_id : "");
  const [projectsPortalId, setProjectsPortalId] = useState("");
  const [sprintsTeamId, setSprintsTeamId] = useState("");
  const [formError, setFormError] = useState<string | null>(null);

  const canSave = clientId.trim() !== "" && clientSecret.trim() !== "";
  // Saving replaces the stored grant, so an empty field on an edit turns
  // CRM sync off — say so when sync is on today.
  const refreshHint =
    connection.has_sync_grant === true
      ? t(($) => $.zoho.connection.refresh_token_hint_replace)
      : t(($) => $.zoho.connection.refresh_token_hint);

  const save = async () => {
    if (!canSave || saveMut.isPending) return;
    setFormError(null);
    try {
      await saveMut.mutateAsync({
        dc,
        client_id: clientId.trim(),
        client_secret: clientSecret.trim(),
        refresh_token: refreshToken.trim() || undefined,
        crm_org_id: crmOrgId.trim() || undefined,
        desk_org_id: deskOrgId.trim() || undefined,
        projects_portal_id: projectsPortalId.trim() || undefined,
        sprints_team_id: sprintsTeamId.trim() || undefined,
      });
      // Secrets are write-only: clear them so they never linger in the DOM.
      setClientSecret("");
      setRefreshToken("");
      toast.success(t(($) => $.zoho.connection.toast_saved));
      onDone?.();
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

  return (
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
      <div className="space-y-1">
        <Input
          type="password"
          autoComplete="off"
          placeholder={t(($) => $.zoho.connection.refresh_token_label)}
          aria-label={t(($) => $.zoho.connection.refresh_token_label)}
          value={refreshToken}
          onChange={(e) => setRefreshToken(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">{refreshHint}</p>
      </div>
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
            placeholder={t(($) => $.zoho.connection.projects_portal_id_label)}
            aria-label={t(($) => $.zoho.connection.projects_portal_id_label)}
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
      <div className="flex items-center gap-2">
        <Button onClick={save} disabled={saveMut.isPending || !canSave} size="sm">
          {saveMut.isPending
            ? t(($) => $.zoho.connection.saving)
            : t(($) => $.zoho.connection.save)}
        </Button>
        {onDone && (
          <Button
            size="sm"
            variant="ghost"
            onClick={onDone}
            disabled={saveMut.isPending}
          >
            {t(($) => $.zoho.connection.cancel)}
          </Button>
        )}
      </div>
    </div>
  );
}

/** Read-only value with a Copy button (the redirect URI the admin pastes into
 * Zoho's console). Selecting the field on focus keeps a manual copy easy
 * when the clipboard is blocked. */
function ZohoCopyField({ value }: { value: string }) {
  const { t } = useT("settings");
  const copy = async () => {
    if (await copyText(value)) {
      toast.success(t(($) => $.zoho.connection.copied));
    } else {
      toast.error(t(($) => $.zoho.connection.error_copy_failed));
    }
  };
  return (
    <div className="flex items-center gap-2">
      <Input
        readOnly
        value={value}
        aria-label={t(($) => $.zoho.connection.redirect_uri_label)}
        className="min-w-0 font-mono text-xs"
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button size="sm" variant="outline" className="shrink-0" onClick={copy}>
        <Copy className="h-3.5 w-3.5" />
        {t(($) => $.zoho.connection.copy)}
      </Button>
    </div>
  );
}

// --- CRM module sync summary (owner/admin, sync grant present) ---------------

function ZohoModuleSyncCard({ wsId }: { wsId: string }) {
  const { t } = useT("settings");
  const nav = useNavigation();
  const paths = useWorkspacePaths();
  const { data: configs = [] } = useQuery({
    ...zohoSyncConfigsOptions(wsId),
    enabled: !!wsId,
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
              {t(($) => $.zoho.modules.synced_count, { count: activeCount })}
            </p>
          </div>
          <Button
            size="sm"
            variant="outline"
            className="shrink-0"
            onClick={() => nav.push(paths.zoho())}
          >
            {t(($) => $.zoho.modules.manage)}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- Projects/Sprints import deep link (pre-existing behavior) ---------------

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

/** Probe badge for the Zoho connector status. Unknown server-side statuses
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
