"use client";

import { useState } from "react";
import { FlaskConical, Laptop } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import type { WorkspaceLabs } from "@agora/core/types";
import { memberListOptions } from "@agora/core/workspace/queries";
import { projectListOptions } from "@agora/core/projects";
import { runtimeListOptions } from "@agora/core/runtimes/queries";
import { Switch } from "@agora/ui/components/ui/switch";
import { useWorkspaceId } from "@agora/core/hooks";
import { useT } from "../../i18n";

// Settings → Labs. QA-environment routing: run QA on the WORKING DEVELOPER's
// own machine — their daemon's declared dev app for the issue's project
// (daemon-per-dev) — instead of a shared target. The "Developer machines"
// table below shows which runtimes have declared a dev-served app.

export function LabsTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const labsQuery = useQuery({
    queryKey: ["workspace-labs", wsId],
    queryFn: () => api.getWorkspaceLabs(),
  });
  const membersQuery = useQuery(memberListOptions(wsId));
  const projectsQuery = useQuery(projectListOptions(wsId));
  const runtimesQuery = useQuery(runtimeListOptions(wsId));

  // Track only the fields the user changed; render from server state otherwise.
  const [draft, setDraft] = useState<Partial<WorkspaceLabs>>({});
  const labs: WorkspaceLabs = {
    // qa_dev_boxes / qa_fallback_box_id are dormant (no UI) — carried through so
    // the PUT round-trips a complete WorkspaceLabs.
    qa_dev_boxes: draft.qa_dev_boxes ?? labsQuery.data?.qa_dev_boxes ?? true,
    qa_fallback_box_id: draft.qa_fallback_box_id ?? labsQuery.data?.qa_fallback_box_id ?? "",
    qa_dev_runtimes: draft.qa_dev_runtimes ?? labsQuery.data?.qa_dev_runtimes ?? false,
    qa_dev_runtimes_strict:
      draft.qa_dev_runtimes_strict ?? labsQuery.data?.qa_dev_runtimes_strict ?? false,
  };

  const saveLabs = useMutation({
    mutationFn: (next: WorkspaceLabs) => api.updateWorkspaceLabs(next),
    onSuccess: () => {
      toast.success(t(($) => $.labs.saved));
      void qc.invalidateQueries({ queryKey: ["workspace-labs", wsId] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.labs.save_failed)),
  });

  const members = membersQuery.data ?? [];
  const projects = projectsQuery.data ?? [];
  // Developer machines: personal runtimes (owner set) that declared at least
  // one dev-served app — what the qa_dev_runtimes toggle routes to.
  const devMachines = (runtimesQuery.data ?? [])
    .map((r) => {
      const apps = (r.metadata?.dev_apps ?? {}) as Record<string, string>;
      const entries = Object.entries(apps).filter(([, url]) => !!url);
      return { runtime: r, entries };
    })
    .filter((m) => m.runtime.owner_id && m.entries.length > 0);
  const projectTitle = (id: string) => projects.find((p) => p.id === id)?.title ?? id.slice(0, 8);
  const ownerName = (userId: string | null) => {
    const m = members.find((mm) => mm.user_id === userId);
    return m?.name || m?.email || "—";
  };

  const apply = (patch: Partial<WorkspaceLabs>) => {
    const next = { ...labs, ...patch };
    setDraft((d) => ({ ...d, ...patch }));
    saveLabs.mutate(next);
  };

  return (
    <div className="space-y-6">
      <div className="rounded-xl border bg-card p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="flex min-w-0 items-start gap-3">
            <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md bg-muted">
              <FlaskConical className="size-4 text-muted-foreground" />
            </span>
            <div className="min-w-0">
              <h3 className="text-[13px] font-medium">{t(($) => $.labs.qa_dev_runtimes_title)}</h3>
              <p className="mt-0.5 text-[12px] leading-relaxed text-muted-foreground">
                {t(($) => $.labs.qa_dev_runtimes_description)}
              </p>
              {labs.qa_dev_runtimes && (
                <label className="mt-2 flex items-center gap-2 text-[12px] text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={labs.qa_dev_runtimes_strict}
                    onChange={(e) => apply({ qa_dev_runtimes_strict: e.target.checked })}
                    disabled={saveLabs.isPending}
                    className="size-3.5 accent-primary"
                  />
                  {t(($) => $.labs.qa_dev_runtimes_strict_label)}
                </label>
              )}
            </div>
          </div>
          <Switch
            checked={labs.qa_dev_runtimes}
            onCheckedChange={(v) => apply({ qa_dev_runtimes: v === true })}
            disabled={labsQuery.isLoading || saveLabs.isPending}
          />
        </div>
      </div>

      <div className="rounded-xl border bg-card">
        <div className="flex items-center gap-2 border-b px-4 py-3">
          <Laptop className="size-4 text-muted-foreground" />
          <h3 className="text-[13px] font-medium">{t(($) => $.labs.dev_machines_title)}</h3>
          <span className="text-[12px] text-muted-foreground">{devMachines.length}</span>
        </div>
        {devMachines.length === 0 ? (
          <p className="px-4 py-4 text-[12px] leading-relaxed text-muted-foreground">
            {t(($) => $.labs.dev_machines_empty)}
          </p>
        ) : (
          <ul className="divide-y">
            {devMachines.map(({ runtime, entries }) => (
              <li key={runtime.id} className="px-4 py-3">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    aria-hidden
                    className={
                      "size-2 shrink-0 rounded-full " +
                      (runtime.status === "online" ? "bg-emerald-500" : "bg-muted-foreground/40")
                    }
                    title={runtime.status}
                  />
                  <span className="text-[13px] font-medium">{runtime.name}</span>
                  <span className="text-[11px] text-muted-foreground">
                    {ownerName(runtime.owner_id)}
                  </span>
                </div>
                <ul className="mt-1.5 space-y-0.5 pl-4">
                  {entries.map(([projectId, url]) => (
                    <li key={projectId} className="flex flex-wrap items-center gap-2 font-mono text-[11px]">
                      <span className="text-muted-foreground">{projectTitle(projectId)}</span>
                      <span className="text-foreground/80">{url}</span>
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
