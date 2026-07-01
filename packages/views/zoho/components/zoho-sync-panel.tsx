/* eslint-disable i18next/no-literal-string -- internal Zoho import admin panel; i18n is a follow-up */
"use client";

import { useMemo, useState } from "react";
import { DatabaseZap, Loader2, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  zohoProjectsOptions,
  zohoSprintsProjectsOptions,
  useImportZohoProjects,
  useImportZohoSprintsProjects,
} from "@agora/core/zoho";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";

// Channels are the Zoho apps this connector can pull from. Projects + Sprints
// are wired to their existing import endpoints; Desk + CRM are placeholders
// until their backend channels ship (see docs/zoho-suite-integration-plan.md).
type Channel = "projects" | "sprints" | "desk" | "crm";

const CHANNELS: { key: Channel; label: string; ready: boolean }[] = [
  { key: "projects", label: "Projects", ready: true },
  { key: "sprints", label: "Sprints", ready: true },
  { key: "desk", label: "Desk", ready: false },
  { key: "crm", label: "CRM", ready: false },
];

/**
 * Zoho import browser. A channel strip (Projects · Sprints · Desk · CRM) picks
 * the Zoho app; each ready channel lists its projects with checkboxes and imports
 * the selection (or all) into the current workspace. Drives
 * /api/zoho-projects/{projects,import} and /api/zoho-sprints/{projects,import}.
 */
export function ZohoSyncPanel() {
  const [channel, setChannel] = useState<Channel>("projects");
  // Selections kept per-channel so switching tabs doesn't mix Projects + Sprints.
  const [selectedProjects, setSelectedProjects] = useState<Record<string, boolean>>({});
  const [selectedSprints, setSelectedSprints] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<string[]>([]);

  const projectsQuery = useQuery({ ...zohoProjectsOptions(), enabled: channel === "projects" });
  const sprintsQuery = useQuery({ ...zohoSprintsProjectsOptions(), enabled: channel === "sprints" });
  const importProjects = useImportZohoProjects();
  const importSprints = useImportZohoSprintsProjects();

  const isProjects = channel === "projects";
  const query = isProjects ? projectsQuery : sprintsQuery;
  // Normalize both channels to a common row shape (Sprints projects have no
  // status) so the list render doesn't fight the union-of-arrays type.
  const rows: { id: string; name: string; status?: string }[] = isProjects
    ? projectsQuery.data ?? []
    : sprintsQuery.data ?? [];
  const selected = isProjects ? selectedProjects : selectedSprints;
  const setSelected = isProjects ? setSelectedProjects : setSelectedSprints;
  const importMut = isProjects ? importProjects : importSprints;

  const selectedIds = useMemo(
    () => Object.keys(selected).filter((id) => selected[id]),
    [selected],
  );
  const allSelected = rows.length > 0 && rows.every((p) => selected[p.id]);

  const toggle = (id: string) => setSelected((s) => ({ ...s, [id]: !s[id] }));
  const toggleAll = () =>
    setSelected(allSelected ? {} : Object.fromEntries(rows.map((p) => [p.id, true])));

  const runImport = async (all: boolean) => {
    setErrors([]);
    try {
      const res = isProjects
        ? await importProjects.mutateAsync(all ? { all: true } : { project_ids: selectedIds })
        : await importSprints.mutateAsync(all ? { all: true } : { project_ids: selectedIds });
      if (res.errors?.length) setErrors(res.errors);
      if (!all) setSelected({});
    } catch (e) {
      setErrors([e instanceof Error ? e.message : "Import failed"]);
    }
  };

  const ready = CHANNELS.find((c) => c.key === channel)?.ready ?? false;

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <DatabaseZap className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">Zoho import</h1>
        </div>
        {ready && (
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
            <Button
              variant="outline"
              size="sm"
              onClick={() => runImport(true)}
              disabled={importMut.isPending || rows.length === 0}
            >
              Import all
            </Button>
            <Button
              size="sm"
              onClick={() => runImport(false)}
              disabled={!selectedIds.length || importMut.isPending}
            >
              {importMut.isPending && <Loader2 className="mr-1 h-4 w-4 animate-spin" />}
              Import{selectedIds.length ? ` ${selectedIds.length}` : ""}
            </Button>
          </div>
        )}
      </PageHeader>

      <div className="flex-1 overflow-auto p-5">
        {/* Channel strip */}
        <div className="mb-4 inline-flex rounded-md border border-border p-0.5">
          {CHANNELS.map((c) => (
            <button
              key={c.key}
              type="button"
              onClick={() => setChannel(c.key)}
              className={cn(
                "rounded px-3 py-1 text-xs font-medium transition-colors",
                channel === c.key
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {c.label}
              {!c.ready && <span className="ml-1 opacity-60">· soon</span>}
            </button>
          ))}
        </div>

        {errors.length > 0 && (
          <div className="mb-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
            {errors.map((e, i) => (
              <div key={i}>{e}</div>
            ))}
          </div>
        )}

        {!ready ? (
          <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
            {CHANNELS.find((c) => c.key === channel)?.label} import is coming soon.
          </div>
        ) : query.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : query.isError ? (
          <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
            Couldn&apos;t load Zoho {isProjects ? "projects" : "sprints projects"}. Check the
            Zoho credentials on the server, then refresh.
          </div>
        ) : rows.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
            No {isProjects ? "projects" : "sprint projects"} found in the configured Zoho{" "}
            {isProjects ? "portal" : "team"}.
          </div>
        ) : (
          <div className="space-y-1">
            <label className="flex items-center gap-2 px-2 py-1.5 text-xs font-medium text-muted-foreground">
              <Checkbox checked={allSelected} onCheckedChange={toggleAll} />
              Select all ({rows.length})
            </label>
            {rows.map((p) => (
              <label
                key={p.id}
                className="flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/50"
              >
                <Checkbox checked={!!selected[p.id]} onCheckedChange={() => toggle(p.id)} />
                <span className="flex-1 truncate text-sm">{p.name}</span>
                {p.status ? (
                  <span className="shrink-0 text-xs text-muted-foreground">{p.status}</span>
                ) : null}
              </label>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export function ZohoPage() {
  return <ZohoSyncPanel />;
}
