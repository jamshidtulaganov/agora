"use client";

import { useState } from "react";
import { Database, FlaskConical, Globe, Laptop, Loader2, PlugZap, Server, Trash2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import type { WorkspaceLabs } from "@agora/core/types";
import { remoteBoxesOptions, remoteBoxKeys } from "@agora/core/runtimes";
import { Button } from "@agora/ui/components/ui/button";
import { memberListOptions } from "@agora/core/workspace/queries";
import { projectListOptions } from "@agora/core/projects";
import { runtimeListOptions } from "@agora/core/runtimes/queries";
import { Switch } from "@agora/ui/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agora/ui/components/ui/select";
import { useWorkspaceId } from "@agora/core/hooks";
import { useT } from "../../i18n";

// Settings → Labs. First experiment: QA-environment routing — QA runs against
// the WORKING DEVELOPER's own box (shahzod.sdteam.uz when Shahzod's issue is
// under test), falling back to a designated shared box (sandbox.sdteam.uz)
// when no per-dev box matches. The mapping table below is what makes the
// per-dev match real: each box names the member who owns it.

const CLEAR_VALUE = "__none__";

// The https URL a box serves, derived the same way the backend does
// (work_dir /var/www/<subdomain> → https://<subdomain>).
function boxURL(workDir: string): string {
  const wd = workDir.replace(/\/+$/, "");
  const sub = wd.slice(wd.lastIndexOf("/") + 1);
  return sub ? `https://${sub}` : "";
}

export function LabsTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const labsQuery = useQuery({
    queryKey: ["workspace-labs", wsId],
    queryFn: () => api.getWorkspaceLabs(),
  });
  const boxesQuery = useQuery(remoteBoxesOptions(wsId));
  const membersQuery = useQuery(memberListOptions(wsId));
  const projectsQuery = useQuery(projectListOptions(wsId));
  const runtimesQuery = useQuery(runtimeListOptions(wsId));

  // Track only the fields the user changed; render from server state otherwise.
  const [draft, setDraft] = useState<Partial<WorkspaceLabs>>({});
  const labs: WorkspaceLabs = {
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

  const testBox = useMutation({
    mutationFn: (boxId: string) => api.testRemoteBox(boxId),
    onSuccess: (res) => {
      if (res.ok) toast.success(t(($) => $.labs.test_ok) + (res.latency_ms ? ` (${res.latency_ms}ms)` : ""));
      else toast.error(t(($) => $.labs.test_failed) + ": " + res.output.slice(0, 160));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.labs.test_failed)),
  });

  const seedBox = useMutation({
    mutationFn: (boxId: string) => api.seedRemoteBox(boxId),
    onSuccess: (res) => {
      if (res.ok) toast.success(t(($) => $.labs.seed_ok));
      else toast.error(t(($) => $.labs.seed_failed) + ": " + res.output.slice(0, 160));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.labs.seed_failed)),
  });

  const deleteBox = useMutation({
    mutationFn: (boxId: string) => api.deleteRemoteBox(boxId),
    onSuccess: () => {
      toast.success(t(($) => $.labs.disconnected));
      void qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.labs.save_failed)),
  });

  // One mutation serves both columns: owner (memberId) and project scope.
  const bindBox = useMutation({
    mutationFn: ({ boxId, projectId, memberId }: { boxId: string; projectId: string; memberId?: string }) =>
      api.bindConnectedBox(boxId, projectId, memberId),
    onSuccess: () => {
      toast.success(t(($) => $.labs.owner_saved));
      void qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.labs.save_failed)),
  });

  const boxes = boxesQuery.data ?? [];
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
              <h3 className="text-[13px] font-medium">{t(($) => $.labs.qa_dev_env_title)}</h3>
              <p className="mt-0.5 text-[12px] leading-relaxed text-muted-foreground">
                {t(($) => $.labs.qa_dev_env_description)}
              </p>
            </div>
          </div>
          <Switch
            checked={labs.qa_dev_boxes}
            onCheckedChange={(v) => apply({ qa_dev_boxes: v === true })}
            disabled={labsQuery.isLoading || saveLabs.isPending}
          />
        </div>

        <div className="mt-4 border-t pt-4">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <h4 className="text-[12px] font-medium">{t(($) => $.labs.qa_dev_runtimes_title)}</h4>
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
            <Switch
              checked={labs.qa_dev_runtimes}
              onCheckedChange={(v) => apply({ qa_dev_runtimes: v === true })}
              disabled={labsQuery.isLoading || saveLabs.isPending}
            />
          </div>
        </div>

        <div className="mt-4 border-t pt-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0">
              <h4 className="text-[12px] font-medium">{t(($) => $.labs.qa_fallback_title)}</h4>
              <p className="mt-0.5 text-[12px] text-muted-foreground">
                {t(($) => $.labs.qa_fallback_description)}
              </p>
            </div>
            <Select
              value={labs.qa_fallback_box_id || CLEAR_VALUE}
              onValueChange={(v) => apply({ qa_fallback_box_id: !v || v === CLEAR_VALUE ? "" : v })}
              disabled={labsQuery.isLoading || saveLabs.isPending}
            >
              <SelectTrigger className="h-8 w-[220px] text-[12px]">
                <SelectValue>
                  {() => {
                    const id = labs.qa_fallback_box_id;
                    const box = id ? boxes.find((x) => x.id === id) : undefined;
                    return box
                      ? box.label || boxURL(box.work_dir) || box.id.slice(0, 8)
                      : t(($) => $.labs.qa_fallback_none);
                  }}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={CLEAR_VALUE}>{t(($) => $.labs.qa_fallback_none)}</SelectItem>
                {boxes.map((b) => (
                  <SelectItem key={b.id} value={b.id}>
                    {b.label || boxURL(b.work_dir) || b.id.slice(0, 8)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
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

      <div className="rounded-xl border bg-card">
        <div className="flex items-center gap-2 border-b px-4 py-3">
          <Server className="size-4 text-muted-foreground" />
          <h3 className="text-[13px] font-medium">{t(($) => $.labs.boxes_title)}</h3>
          <span className="text-[12px] text-muted-foreground">{boxes.length}</span>
        </div>
        {boxesQuery.isLoading ? (
          <div className="flex items-center gap-2 px-4 py-6 text-[12px] text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" />
            {t(($) => $.labs.boxes_loading)}
          </div>
        ) : boxes.length === 0 ? (
          <p className="px-4 py-6 text-[12px] text-muted-foreground">
            {t(($) => $.labs.boxes_empty)}
          </p>
        ) : (
          <ul className="divide-y">
            {boxes.map((b) => {
              const url = boxURL(b.work_dir);
              // The Select speaks MEMBER ids; the box row carries the owner's
              // USER id — map it back for display.
              const ownerMember = members.find((m) => m.user_id === b.owner_id);
              return (
                <li key={b.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                  <span
                    aria-hidden
                    className={
                      "size-2 shrink-0 rounded-full " +
                      (b.status === "online" ? "bg-emerald-500" : "bg-muted-foreground/40")
                    }
                    title={b.status}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-medium">{b.label || b.id.slice(0, 8)}</div>
                    {url && (
                      <a
                        href={url}
                        target="_blank"
                        rel="noreferrer"
                        className="mt-0.5 inline-flex items-center gap-1 truncate text-[11px] text-muted-foreground hover:text-foreground hover:underline"
                      >
                        <Globe className="size-3 shrink-0" />
                        {url}
                      </a>
                    )}
                  </div>
                  <Select
                    value={b.project_id ?? CLEAR_VALUE}
                    onValueChange={(v) =>
                      bindBox.mutate({
                        boxId: b.id,
                        projectId: !v || v === CLEAR_VALUE ? "" : v,
                      })
                    }
                    disabled={bindBox.isPending}
                  >
                    <SelectTrigger className="h-8 w-[180px] text-[12px]">
                      <SelectValue>
                        {() =>
                          projects.find((p) => p.id === b.project_id)?.title ??
                          t(($) => $.labs.project_none)
                        }
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={CLEAR_VALUE}>{t(($) => $.labs.project_none)}</SelectItem>
                      {projects.map((p) => (
                        <SelectItem key={p.id} value={p.id}>
                          {p.title}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Select
                    value={ownerMember?.id ?? CLEAR_VALUE}
                    onValueChange={(v) =>
                      bindBox.mutate({
                        boxId: b.id,
                        projectId: b.project_id ?? "",
                        memberId: !v || v === CLEAR_VALUE ? "" : v,
                      })
                    }
                    disabled={bindBox.isPending}
                  >
                    <SelectTrigger className="h-8 w-[200px] text-[12px]">
                      <SelectValue>
                        {() =>
                          ownerMember?.name ||
                          ownerMember?.email ||
                          t(($) => $.labs.owner_none)
                        }
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={CLEAR_VALUE}>{t(($) => $.labs.owner_none)}</SelectItem>
                      {members.map((m) => (
                        <SelectItem key={m.id} value={m.id}>
                          {m.name || m.email || m.id.slice(0, 8)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <div className="flex shrink-0 items-center gap-1">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-8 gap-1 px-2 text-[11px]"
                      title={t(($) => $.labs.test_button_title)}
                      disabled={testBox.isPending}
                      onClick={() => testBox.mutate(b.id)}
                    >
                      {testBox.isPending && testBox.variables === b.id ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <PlugZap className="size-3.5" />
                      )}
                      {t(($) => $.labs.test_button)}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-8 gap-1 px-2 text-[11px]"
                      title={t(($) => $.labs.seed_button_title)}
                      disabled={seedBox.isPending}
                      onClick={() => {
                        if (window.confirm(t(($) => $.labs.seed_confirm))) seedBox.mutate(b.id);
                      }}
                    >
                      {seedBox.isPending && seedBox.variables === b.id ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <Database className="size-3.5" />
                      )}
                      {t(($) => $.labs.seed_button)}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-8 px-2 text-destructive hover:text-destructive"
                      title={t(($) => $.labs.disconnect_button_title)}
                      disabled={deleteBox.isPending}
                      onClick={() => {
                        if (window.confirm(t(($) => $.labs.disconnect_confirm))) deleteBox.mutate(b.id);
                      }}
                    >
                      <Trash2 className="size-3.5" />
                    </Button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
        <p className="border-t px-4 py-3 text-[11px] leading-relaxed text-muted-foreground">
          {t(($) => $.labs.resolution_note)}
        </p>
      </div>
    </div>
  );
}
