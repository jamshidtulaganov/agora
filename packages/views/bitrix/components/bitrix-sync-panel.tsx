/* eslint-disable i18next/no-literal-string -- internal Bitrix import admin panel; i18n is a follow-up */
"use client";

import { useMemo, useState } from "react";
import { DatabaseZap, Loader2, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  bitrixGroupsOptions,
  useImportBitrixTasks,
  type BitrixImportResponse,
} from "@agora/core/bitrix";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { PageHeader } from "../../layout/page-header";

/**
 * Bitrix import browser: list workgroups (each annotated with the Agora
 * workspace it routes to), select groups, and bulk-import them. Each group
 * becomes a project; its tasks become enriched issues. Drives the
 * /api/bitrix/{groups,import} endpoints.
 */
export function BitrixSyncPanel() {
  const groupsQuery = useQuery(bitrixGroupsOptions());
  const importMut = useImportBitrixTasks();
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [result, setResult] = useState<BitrixImportResponse | null>(null);

  const groups = useMemo(() => groupsQuery.data ?? [], [groupsQuery.data]);
  const routable = useMemo(
    () => groups.filter((g) => g.workspace_slug),
    [groups],
  );
  const selectedIds = Object.keys(selected).filter((id) => selected[id]);
  const allSelected =
    routable.length > 0 && selectedIds.length === routable.length;

  function toggle(id: string) {
    setSelected((s) => ({ ...s, [id]: !s[id] }));
  }
  function toggleAll() {
    setSelected(
      allSelected ? {} : Object.fromEntries(routable.map((g) => [g.id, true])),
    );
  }
  function runImport() {
    if (!selectedIds.length) return;
    setResult(null);
    importMut.mutate(
      { group_ids: selectedIds },
      { onSuccess: (r) => setResult(r) },
    );
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <DatabaseZap className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">Bitrix import</h1>
          {groups.length > 0 && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
              {groups.length}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => groupsQuery.refetch()}
            disabled={groupsQuery.isFetching}
            aria-label="Refresh"
          >
            <RefreshCw
              className={`h-4 w-4 ${groupsQuery.isFetching ? "animate-spin" : ""}`}
            />
          </Button>
          <Button
            size="sm"
            onClick={runImport}
            disabled={!selectedIds.length || importMut.isPending}
          >
            {importMut.isPending && (
              <Loader2 className="mr-1 h-4 w-4 animate-spin" />
            )}
            Import{selectedIds.length ? ` ${selectedIds.length}` : ""}
          </Button>
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-5">
        <p className="mb-4 text-sm text-muted-foreground">
          Select Bitrix workgroups to import. Each group becomes a project; its
          tasks become issues with comments, attachments, and video frames.
        </p>

        {groupsQuery.isError && (
          <div className="mb-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            Failed to load Bitrix groups — is the integration configured?{" "}
            {(groupsQuery.error as Error)?.message}
          </div>
        )}

        {result && (
          <div className="mb-4 rounded-md border border-border bg-muted/40 p-3 text-sm">
            {result.accepted ? (
              <span>
                <span className="font-medium">Import started:</span>{" "}
                {result.accepted} task{result.accepted === 1 ? "" : "s"} queued —
                issues appear on the board as they sync (videos download in the
                background).
              </span>
            ) : (
              <span>
                <span className="font-medium">Imported:</span> {result.created}{" "}
                created, {result.updated} updated, {result.skipped} skipped.
              </span>
            )}
            {result.errors?.length ? (
              <ul className="mt-2 list-disc pl-5 text-destructive">
                {result.errors.slice(0, 5).map((e, i) => (
                  <li key={i}>{e}</li>
                ))}
              </ul>
            ) : null}
          </div>
        )}

        {groupsQuery.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : (
          <div className="rounded-md border border-border">
            <div className="flex items-center gap-3 border-b border-border bg-muted/30 px-3 py-2 text-xs font-medium text-muted-foreground">
              <Checkbox
                checked={allSelected}
                onCheckedChange={toggleAll}
                aria-label="Select all routable groups"
              />
              <span className="flex-1">Workgroup</span>
              <span className="w-40">Workspace</span>
            </div>
            {groups.map((g) => {
              const isRoutable = Boolean(g.workspace_slug);
              return (
                <div
                  key={g.id}
                  className="flex items-center gap-3 border-b border-border px-3 py-2 last:border-0"
                >
                  <Checkbox
                    checked={!!selected[g.id]}
                    onCheckedChange={() => toggle(g.id)}
                    disabled={!isRoutable}
                    aria-label={`Select ${g.name}`}
                  />
                  <span className="flex-1 text-sm">
                    {g.name}{" "}
                    <span className="text-muted-foreground/60">#{g.id}</span>
                  </span>
                  <span className="w-40 text-xs">
                    {isRoutable ? (
                      <span className="rounded bg-primary/10 px-1.5 py-0.5 text-primary">
                        {g.workspace_slug}
                      </span>
                    ) : (
                      <span className="text-muted-foreground/60">unrouted</span>
                    )}
                  </span>
                </div>
              );
            })}
            {!groups.length && (
              <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                No Bitrix groups found.
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** Dashboard page wrapper, re-exported as the route default by apps/web. */
export function BitrixPage() {
  return <BitrixSyncPanel />;
}
