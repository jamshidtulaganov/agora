"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { RefreshCw, Trash2 } from "lucide-react";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { Switch } from "@agora/ui/components/ui/switch";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { Label } from "@agora/ui/components/ui/label";
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
import { cn } from "@agora/ui/lib/utils";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import { projectListOptions } from "@agora/core/projects/queries";
import {
  zohoConnectionOptions,
  zohoCRMModulesOptions,
  zohoSyncConfigsOptions,
  useCreateZohoSyncConfig,
  useDeleteZohoSyncConfig,
  useUpdateZohoSyncConfig,
} from "@agora/core/zoho";
import type {
  ZohoCRMModule,
  ZohoSyncConfig,
  ZohoSyncDirection,
} from "@agora/core/zoho";
import { useT } from "../../i18n";

// Prefilled skeletons for a fresh config: enough shape to show the operator
// what goes where without them reading the docs. Subject/Status exist on most
// stock modules; custom modules just edit the values.
const DEFAULT_FIELD_MAP = `{
  "title": "Subject",
  "status": "Status"
}`;
const DEFAULT_STATUS_MAP = `{
  "in": {},
  "out": {}
}`;

const DIRECTIONS: ZohoSyncDirection[] = ["both", "in", "out"];

/** Strict JSON-object parse for the map textareas: anything that isn't a
 * plain object (arrays, scalars, malformed JSON) is rejected. */
function parseJsonObject(text: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(text) as unknown;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      return v as Record<string, unknown>;
    }
    return null;
  } catch {
    return null;
  }
}

function moduleLabel(m: ZohoCRMModule): string {
  return m.plural_label || m.module_name || m.api_name;
}

/**
 * CRM module sync manager (docs/zoho-dynamic-integration.md §1.5, "Modules
 * tab"). Left: modules discovered from the workspace connection, badged
 * custom/default and "syncing" when a config exists. Right: create/edit form
 * for the selected module (direction, destination project, field/status maps
 * as validated JSON). Owner/admin only — the discovery and config endpoints
 * 403 for anyone else, so the panel gates itself to match.
 */
export function ZohoModuleSyncPanel() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!wsId,
  });
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const connectionQuery = useQuery({
    ...zohoConnectionOptions(wsId),
    enabled: !!wsId && canManage,
  });
  const configured = connectionQuery.data?.configured === true;

  const modulesQuery = useQuery({
    ...zohoCRMModulesOptions(wsId),
    enabled: !!wsId && canManage && configured,
  });
  const configsQuery = useQuery({
    ...zohoSyncConfigsOptions(wsId),
    enabled: !!wsId && canManage && configured,
  });
  const { data: projects = [] } = useQuery({
    ...projectListOptions(wsId),
    enabled: !!wsId && canManage && configured,
  });

  const [selectedApiName, setSelectedApiName] = useState<string | null>(null);

  if (!canManage) {
    return (
      <PanelNote>{t(($) => $.zoho.modules.admin_only)}</PanelNote>
    );
  }
  if (connectionQuery.isLoading) {
    return <PanelSkeleton />;
  }
  if (!configured) {
    return (
      <PanelNote>{t(($) => $.zoho.modules.requires_connection)}</PanelNote>
    );
  }

  const modules = modulesQuery.data ?? [];
  const configs = configsQuery.data ?? [];
  const configByModule = new Map(configs.map((c) => [c.module_api_name, c]));
  const selected =
    modules.find((m) => m.api_name === selectedApiName) ?? null;
  const selectedConfig = selected
    ? configByModule.get(selected.api_name) ?? null
    : null;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">
          {t(($) => $.zoho.modules.title)}
        </h2>
        <Button
          variant="ghost"
          size="sm"
          aria-label={t(($) => $.zoho.modules.refresh)}
          onClick={() => {
            void modulesQuery.refetch();
            void configsQuery.refetch();
          }}
          disabled={modulesQuery.isFetching}
        >
          <RefreshCw
            className={cn("h-4 w-4", modulesQuery.isFetching && "animate-spin")}
          />
        </Button>
      </div>

      {modulesQuery.isLoading || configsQuery.isLoading ? (
        <PanelSkeleton />
      ) : modulesQuery.isError ? (
        <PanelNote>{t(($) => $.zoho.modules.load_error)}</PanelNote>
      ) : modules.length === 0 ? (
        <PanelNote>{t(($) => $.zoho.modules.empty)}</PanelNote>
      ) : (
        <div className="flex flex-col gap-4 md:flex-row">
          <div className="w-full shrink-0 space-y-0.5 md:w-72">
            {modules.map((m) => {
              const isSelected = m.api_name === selectedApiName;
              const hasConfig = configByModule.has(m.api_name);
              return (
                <button
                  key={m.api_name}
                  type="button"
                  aria-current={isSelected ? "true" : undefined}
                  onClick={() => setSelectedApiName(m.api_name)}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted/50",
                    isSelected && "bg-muted",
                  )}
                >
                  <span
                    className="min-w-0 flex-1 truncate"
                    title={moduleLabel(m)}
                  >
                    {moduleLabel(m)}
                  </span>
                  <Badge variant="outline" className="shrink-0">
                    {m.generated_type === "custom"
                      ? t(($) => $.zoho.modules.badge_custom)
                      : t(($) => $.zoho.modules.badge_default)}
                  </Badge>
                  {hasConfig && (
                    <Badge variant="secondary" className="shrink-0">
                      {t(($) => $.zoho.modules.badge_syncing)}
                    </Badge>
                  )}
                </button>
              );
            })}
          </div>

          <div className="min-w-0 flex-1">
            {selected ? (
              <ModuleConfigForm
                // Remount on module switch AND when the config row appears /
                // changes after a save, so form state re-seeds from the server.
                key={`${selected.api_name}:${selectedConfig ? selectedConfig.updated_at : "new"}`}
                wsId={wsId}
                module={selected}
                config={selectedConfig}
                projects={projects}
              />
            ) : (
              <PanelNote>{t(($) => $.zoho.modules.select_hint)}</PanelNote>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function PanelNote({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
      {children}
    </div>
  );
}

function PanelSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 5 }).map((_, i) => (
        <Skeleton key={i} className="h-9 w-full" />
      ))}
    </div>
  );
}

function ModuleConfigForm({
  wsId,
  module,
  config,
  projects,
}: {
  wsId: string;
  module: ZohoCRMModule;
  config: ZohoSyncConfig | null;
  projects: { id: string; title: string }[];
}) {
  const { t } = useT("settings");
  const isEdit = config !== null;

  const [direction, setDirection] = useState<string>(
    config?.direction || "both",
  );
  const [projectId, setProjectId] = useState<string>(config?.project_id ?? "");
  const [enabled, setEnabled] = useState<boolean>(
    config ? config.enabled === true : true,
  );
  const [fieldMapText, setFieldMapText] = useState<string>(
    config ? JSON.stringify(config.field_map ?? {}, null, 2) : DEFAULT_FIELD_MAP,
  );
  const [statusMapText, setStatusMapText] = useState<string>(
    config
      ? JSON.stringify(config.status_map ?? {}, null, 2)
      : DEFAULT_STATUS_MAP,
  );
  const [fieldMapInvalid, setFieldMapInvalid] = useState(false);
  const [statusMapInvalid, setStatusMapInvalid] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const createMut = useCreateZohoSyncConfig(wsId);
  const updateMut = useUpdateZohoSyncConfig(wsId);
  const deleteMut = useDeleteZohoSyncConfig(wsId);
  const saving = createMut.isPending || updateMut.isPending;

  const directionLabel = (d: ZohoSyncDirection): string => {
    switch (d) {
      case "in":
        return t(($) => $.zoho.modules.direction_in);
      case "out":
        return t(($) => $.zoho.modules.direction_out);
      default:
        return t(($) => $.zoho.modules.direction_both);
    }
  };

  const submit = async () => {
    if (saving) return;
    // Client-side JSON validation blocks the submit before any request.
    const fieldMap = parseJsonObject(fieldMapText);
    const statusMap = parseJsonObject(statusMapText);
    setFieldMapInvalid(fieldMap === null);
    setStatusMapInvalid(statusMap === null);
    if (fieldMap === null || statusMap === null) return;
    setFormError(null);
    try {
      if (isEdit && config) {
        await updateMut.mutateAsync({
          configId: config.id,
          req: {
            direction: direction as ZohoSyncDirection,
            // Always send the key on edit: the backend treats an explicit ""
            // as "clear the project" (the None option) — omitting it would
            // silently keep the old assignment.
            project_id: projectId,
            enabled,
            field_map: fieldMap,
            status_map: statusMap,
          },
        });
        toast.success(t(($) => $.zoho.modules.toast_updated));
      } else {
        await createMut.mutateAsync({
          module_api_name: module.api_name,
          direction: direction as ZohoSyncDirection,
          project_id: projectId || undefined,
          field_map: fieldMap,
          status_map: statusMap,
        });
        toast.success(t(($) => $.zoho.modules.toast_created));
      }
    } catch (e) {
      setFormError(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.zoho.modules.error_save_failed),
      );
    }
  };

  const remove = async () => {
    if (!config || deleteMut.isPending) return;
    try {
      await deleteMut.mutateAsync(config.id);
      toast.success(t(($) => $.zoho.modules.toast_deleted));
      setConfirmDelete(false);
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.zoho.modules.error_delete_failed),
      );
    }
  };

  return (
    <div className="space-y-3 rounded-lg border border-border p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="min-w-0 truncate text-sm font-medium" title={module.api_name}>
          {moduleLabel(module)}{" "}
          <span className="font-mono text-xs text-muted-foreground">
            {module.api_name}
          </span>
        </h3>
        {isEdit && (
          <div className="flex shrink-0 items-center gap-2">
            <Label className="text-xs text-muted-foreground">
              {t(($) => $.zoho.modules.enabled_label)}
            </Label>
            <Switch
              checked={enabled}
              onCheckedChange={(v) => setEnabled(v === true)}
              aria-label={t(($) => $.zoho.modules.enabled_label)}
            />
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">
            {t(($) => $.zoho.modules.direction_label)}
          </Label>
          <NativeSelect
            className="w-full"
            aria-label={t(($) => $.zoho.modules.direction_label)}
            value={direction}
            onChange={(e) => setDirection(e.target.value)}
          >
            {DIRECTIONS.map((d) => (
              <NativeSelectOption key={d} value={d}>
                {directionLabel(d)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">
            {t(($) => $.zoho.modules.project_label)}
          </Label>
          <NativeSelect
            className="w-full"
            aria-label={t(($) => $.zoho.modules.project_label)}
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
          >
            <NativeSelectOption value="">
              {t(($) => $.zoho.modules.project_none)}
            </NativeSelectOption>
            {projects.map((p) => (
              <NativeSelectOption key={p.id} value={p.id}>
                {p.title}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
      </div>

      <div className="space-y-1">
        <Label className="text-xs text-muted-foreground">
          {t(($) => $.zoho.modules.field_map_label)}
        </Label>
        <Textarea
          className="font-mono text-xs"
          rows={5}
          aria-label={t(($) => $.zoho.modules.field_map_label)}
          aria-invalid={fieldMapInvalid || undefined}
          value={fieldMapText}
          onChange={(e) => setFieldMapText(e.target.value)}
        />
        {fieldMapInvalid && (
          <p className="text-xs text-destructive">
            {t(($) => $.zoho.modules.invalid_json)}
          </p>
        )}
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.zoho.modules.field_map_hint)}
        </p>
      </div>

      <div className="space-y-1">
        <Label className="text-xs text-muted-foreground">
          {t(($) => $.zoho.modules.status_map_label)}
        </Label>
        <Textarea
          className="font-mono text-xs"
          rows={5}
          aria-label={t(($) => $.zoho.modules.status_map_label)}
          aria-invalid={statusMapInvalid || undefined}
          value={statusMapText}
          onChange={(e) => setStatusMapText(e.target.value)}
        />
        {statusMapInvalid && (
          <p className="text-xs text-destructive">
            {t(($) => $.zoho.modules.invalid_json)}
          </p>
        )}
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.zoho.modules.status_map_hint)}
        </p>
      </div>

      {formError && (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {formError}
        </p>
      )}

      <div className="flex items-center justify-between gap-2">
        <Button size="sm" onClick={submit} disabled={saving}>
          {isEdit
            ? updateMut.isPending
              ? t(($) => $.zoho.modules.updating)
              : t(($) => $.zoho.modules.update)
            : createMut.isPending
              ? t(($) => $.zoho.modules.creating)
              : t(($) => $.zoho.modules.create)}
        </Button>
        {isEdit && (
          <Button
            size="sm"
            variant="destructive"
            onClick={() => setConfirmDelete(true)}
            disabled={deleteMut.isPending}
          >
            <Trash2 className="h-3 w-3" />
            {t(($) => $.zoho.modules.delete)}
          </Button>
        )}
      </div>

      <AlertDialog
        open={confirmDelete}
        onOpenChange={(v) => {
          if (!v && !deleteMut.isPending) setConfirmDelete(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.zoho.modules.delete_confirm_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.zoho.modules.delete_confirm_description, {
                module: moduleLabel(module),
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteMut.isPending}>
              {t(($) => $.zoho.modules.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={remove} disabled={deleteMut.isPending}>
              {deleteMut.isPending
                ? t(($) => $.zoho.modules.deleting)
                : t(($) => $.zoho.modules.delete)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
