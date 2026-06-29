/* eslint-disable i18next/no-literal-string -- internal Bitrix import admin panel; i18n is a follow-up */
"use client";

import { useMemo, useState } from "react";
import { DatabaseZap, Loader2, RefreshCw, Search } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  bitrixGroupsOptions,
  bitrixUsersOptions,
  bitrixImportProgressOptions,
  useImportBitrixTasks,
  type BitrixImportResponse,
} from "@agora/core/bitrix";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";

type Mode = "groups" | "users";

/**
 * Bitrix import browser. Import EITHER by workgroup (each group → a project,
 * routed to its workspace) OR by responsible user (all the tasks that person
 * owns). A filter box narrows the visible list; selected rows import in one
 * call. Drives /api/bitrix/{groups,users,import}.
 */
export function BitrixSyncPanel() {
  const [mode, setMode] = useState<Mode>("groups");
  const [filter, setFilter] = useState("");
  // Selections are kept per-mode so switching tabs doesn't mix groups + users.
  const [selectedGroups, setSelectedGroups] = useState<Record<string, boolean>>({});
  const [selectedUsers, setSelectedUsers] = useState<Record<string, boolean>>({});
  const [result, setResult] = useState<BitrixImportResponse | null>(null);

  const groupsQuery = useQuery(bitrixGroupsOptions());
  const usersQuery = useQuery(bitrixUsersOptions());
  const importMut = useImportBitrixTasks();

  // Poll live import progress while a run is in flight (started this session).
  const [polling, setPolling] = useState(false);
  const progressQuery = useQuery(bitrixImportProgressOptions(polling));
  const progress = progressQuery.data;
  // Stop polling once the backend reports the run finished.
  if (polling && progress && !progress.running) {
    // defer state update out of render
    queueMicrotask(() => setPolling(false));
  }

  const groups = useMemo(() => groupsQuery.data ?? [], [groupsQuery.data]);
  const users = useMemo(() => usersQuery.data ?? [], [usersQuery.data]);

  const q = filter.trim().toLowerCase();
  const visibleGroups = useMemo(
    () => (q ? groups.filter((g) => `${g.name} ${g.id}`.toLowerCase().includes(q)) : groups),
    [groups, q],
  );
  const visibleUsers = useMemo(
    () =>
      q
        ? users.filter((u) => `${u.name} ${u.email} ${u.position} ${u.id}`.toLowerCase().includes(q))
        : users,
    [users, q],
  );

  const selected = mode === "groups" ? selectedGroups : selectedUsers;
  const setSelected = mode === "groups" ? setSelectedGroups : setSelectedUsers;

  // Selections from BOTH tabs import together — a run can mix workgroups and
  // responsibles. The visible checkboxes track the active mode; the Import
  // button + request carry both.
  const selectedGroupIds = Object.keys(selectedGroups).filter((id) => selectedGroups[id]);
  const selectedUserIds = Object.keys(selectedUsers).filter((id) => selectedUsers[id]);
  const totalSelected = selectedGroupIds.length + selectedUserIds.length;

  // "Select all" acts on the currently-visible (filtered) rows; for groups only
  // routable ones can be picked.
  const selectable =
    mode === "groups"
      ? visibleGroups.filter((g) => g.workspace_slug).map((g) => g.id)
      : visibleUsers.map((u) => u.id);
  const allSelected = selectable.length > 0 && selectable.every((id) => selected[id]);

  const loading = mode === "groups" ? groupsQuery.isLoading : usersQuery.isLoading;
  const query = mode === "groups" ? groupsQuery : usersQuery;

  function toggle(id: string) {
    setSelected((s) => ({ ...s, [id]: !s[id] }));
  }
  function toggleAll() {
    setSelected((s) => {
      if (allSelected) {
        const next = { ...s };
        selectable.forEach((id) => delete next[id]);
        return next;
      }
      return { ...s, ...Object.fromEntries(selectable.map((id) => [id, true])) };
    });
  }
  function runImport() {
    if (!totalSelected) return;
    setResult(null);
    importMut.mutate(
      { group_ids: selectedGroupIds, user_ids: selectedUserIds },
      {
        onSuccess: (r) => {
          setResult(r);
          if (r.accepted) setPolling(true);
        },
      },
    );
  }

  function switchMode(next: Mode) {
    setMode(next);
    setFilter("");
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <DatabaseZap className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">Bitrix import</h1>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => query.refetch()}
            disabled={query.isFetching}
            aria-label="Refresh"
          >
            <RefreshCw className={`h-4 w-4 ${query.isFetching ? "animate-spin" : ""}`} />
          </Button>
          <Button size="sm" onClick={runImport} disabled={!totalSelected || importMut.isPending}>
            {importMut.isPending && <Loader2 className="mr-1 h-4 w-4 animate-spin" />}
            Import{totalSelected ? ` ${totalSelected}` : ""}
          </Button>
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-5">
        {/* Mode tabs + filter */}
        <div className="mb-4 flex flex-wrap items-center gap-3">
          <div className="inline-flex rounded-md border border-border p-0.5">
            {(["groups", "users"] as Mode[]).map((m) => {
              const count = m === "groups" ? selectedGroupIds.length : selectedUserIds.length;
              return (
                <button
                  key={m}
                  type="button"
                  onClick={() => switchMode(m)}
                  className={cn(
                    "rounded px-3 py-1 text-xs font-medium transition-colors",
                    mode === m ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {m === "groups" ? "By group" : "By user"}
                  {count > 0 ? <span className="ml-1 opacity-70">({count})</span> : null}
                </button>
              );
            })}
          </div>
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder={mode === "groups" ? "Filter workgroups…" : "Filter users by name / email…"}
              className="h-8 w-full rounded-md border bg-transparent pl-7 pr-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
            />
          </div>
        </div>

        <p className="mb-4 text-sm text-muted-foreground">
          {mode === "groups"
            ? "Select Bitrix workgroups to import. Each group's tasks become issues with comments, attachments, and live status/stage sync."
            : "Select Bitrix users to import every task they're responsible for — routed by the workspace's project rules."}
        </p>

        {query.isError && (
          <div className="mb-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            Failed to load Bitrix {mode} — is the integration configured?{" "}
            {(query.error as Error)?.message}
          </div>
        )}

        {progress && progress.total > 0 && (polling || progress.running) && (
          <div className="mb-4 rounded-md border border-border bg-muted/30 p-3">
            <div className="mb-1.5 flex items-center justify-between text-xs">
              <span className="font-medium">
                {progress.running ? "Importing…" : "Import complete"}
              </span>
              <span className="font-mono tabular-nums text-muted-foreground">
                {progress.synced}/{progress.total}
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary transition-all"
                style={{
                  width: `${progress.total ? Math.round((progress.synced / progress.total) * 100) : 0}%`,
                }}
              />
            </div>
          </div>
        )}

        {result && (
          <div className="mb-4 rounded-md border border-border bg-muted/40 p-3 text-sm">
            {result.accepted ? (
              <span>
                <span className="font-medium">Import started:</span> {result.accepted} task
                {result.accepted === 1 ? "" : "s"} queued — issues appear on the board as they sync.
              </span>
            ) : (
              <span>
                <span className="font-medium">Imported:</span> {result.created} created, {result.updated} updated,{" "}
                {result.skipped} skipped.
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

        {loading ? (
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : (
          <div className="rounded-md border border-border">
            <div className="flex items-center gap-3 border-b border-border bg-muted/30 px-3 py-2 text-xs font-medium text-muted-foreground">
              <Checkbox checked={allSelected} onCheckedChange={toggleAll} aria-label="Select all" />
              <span className="flex-1">{mode === "groups" ? "Workgroup" : "User"}</span>
              <span className="w-44">{mode === "groups" ? "Workspace" : "Email"}</span>
            </div>

            {mode === "groups"
              ? visibleGroups.map((g) => {
                  const isRoutable = Boolean(g.workspace_slug);
                  return (
                    <div key={g.id} className="flex items-center gap-3 border-b border-border px-3 py-2 last:border-0">
                      <Checkbox
                        checked={!!selected[g.id]}
                        onCheckedChange={() => toggle(g.id)}
                        disabled={!isRoutable}
                        aria-label={`Select ${g.name}`}
                      />
                      <span className="flex-1 text-sm">
                        {g.name} <span className="text-muted-foreground/60">#{g.id}</span>
                      </span>
                      <span className="w-44 text-xs">
                        {isRoutable ? (
                          <span className="rounded bg-primary/10 px-1.5 py-0.5 text-primary">{g.workspace_slug}</span>
                        ) : (
                          <span className="text-muted-foreground/60">unrouted</span>
                        )}
                      </span>
                    </div>
                  );
                })
              : visibleUsers.map((u) => (
                  <div key={u.id} className="flex items-center gap-3 border-b border-border px-3 py-2 last:border-0">
                    <Checkbox
                      checked={!!selected[u.id]}
                      onCheckedChange={() => toggle(u.id)}
                      aria-label={`Select ${u.name}`}
                    />
                    <span className="flex-1 text-sm">
                      {u.name}
                      {u.position ? <span className="text-muted-foreground/60"> · {u.position}</span> : null}
                    </span>
                    <span className="w-44 truncate text-xs text-muted-foreground">{u.email}</span>
                  </div>
                ))}

            {((mode === "groups" && !visibleGroups.length) || (mode === "users" && !visibleUsers.length)) && (
              <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                {q ? `No Bitrix ${mode} match "${filter}".` : `No Bitrix ${mode} found.`}
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
