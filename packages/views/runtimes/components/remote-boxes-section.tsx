/* eslint-disable i18next/no-literal-string -- runtime admin panel; i18n follow-up */
"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Server, Plus, Trash2, Loader2, GitBranch, UserPlus } from "lucide-react";
import { toast } from "sonner";
import {
  remoteBoxesOptions,
  useCreateRemoteBox,
  useDeleteRemoteBox,
  useSyncRemoteBox,
  useProvisionRemoteBox,
} from "@agora/core/runtimes";
import { memberListOptions } from "@agora/core/workspace/queries";
import type { ConnectedBox, ProvisionBoxResult } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";

// Remote Boxes (opt-in) — onboard a developer's own remote dev server. Backend
// CRUD is gated behind AGORA_REMOTE_BOXES_ENABLED (list falls back to [] when
// off). This section is the management surface: add a box (label + SSH target),
// see status, remove it. The SSH bootstrap + editor tunnel are later phases; a
// freshly-added box sits in "pending".

const STATUS_STYLE: Record<string, string> = {
  online: "text-emerald-600 dark:text-emerald-400",
  pending: "text-amber-600 dark:text-amber-400",
  bootstrapping: "text-amber-600 dark:text-amber-400",
  offline: "text-muted-foreground",
  error: "text-destructive",
};

export function RemoteBoxesSection({ wsId }: { wsId: string }) {
  const { data: boxes = [] } = useQuery(remoteBoxesOptions(wsId));
  const createBox = useCreateRemoteBox(wsId);
  const deleteBox = useDeleteRemoteBox(wsId);

  const [label, setLabel] = useState("");
  const [host, setHost] = useState("");
  const [user, setUser] = useState("");
  const [repoUrl, setRepoUrl] = useState("");
  const [workDir, setWorkDir] = useState("");

  const canSubmit =
    label.trim() !== "" && host.trim() !== "" && user.trim() !== "" && !createBox.isPending;

  const handleAdd = async () => {
    if (!canSubmit) return;
    try {
      await createBox.mutateAsync({
        label: label.trim(),
        ssh_host: host.trim(),
        ssh_user: user.trim(),
        repo_url: repoUrl.trim() || undefined,
        work_dir: workDir.trim() || undefined,
      });
      setLabel("");
      setHost("");
      setUser("");
      setRepoUrl("");
      setWorkDir("");
      toast.success("Remote box added");
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to add remote box",
      );
    }
  };

  const handleDelete = async (box: ConnectedBox) => {
    try {
      await deleteBox.mutateAsync(box.id);
      toast.success("Remote box removed");
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to remove remote box",
      );
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Server className="size-3.5" />
        Remote boxes
      </div>

      {boxes.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">
          No remote boxes yet. Add a developer&apos;s server below — Agora SSHes in
          and checks out a branch so the box serves it (for QA / review).
        </p>
      ) : (
        <ul className="space-y-1.5">
          {boxes.map((box) => (
            <BoxRow key={box.id} box={box} wsId={wsId} onRemove={() => void handleDelete(box)} />
          ))}
        </ul>
      )}

      <div className="flex flex-wrap items-center gap-1.5">
        <input
          type="text"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="label (e.g. jamshid)"
          aria-label="Box label"
          className="h-7 w-28 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
        <input
          type="text"
          value={host}
          onChange={(e) => setHost(e.target.value)}
          placeholder="ssh host (jamshid.sdteam.uz)"
          aria-label="SSH host"
          className="h-7 w-48 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
        <input
          type="text"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          placeholder="ssh user"
          aria-label="SSH user"
          className="h-7 w-24 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
        <input
          type="text"
          value={repoUrl}
          onChange={(e) => setRepoUrl(e.target.value)}
          placeholder="repo url (https://github.com/org/repo.git)"
          aria-label="Repo URL"
          className="h-7 w-56 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
        <input
          type="text"
          value={workDir}
          onChange={(e) => setWorkDir(e.target.value)}
          placeholder="work dir (/var/www/site)"
          aria-label="Work dir"
          className="h-7 w-44 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-7 px-2 text-xs"
          disabled={!canSubmit}
          onClick={() => void handleAdd()}
        >
          {createBox.isPending ? (
            <Loader2 className="size-3 animate-spin" />
          ) : (
            <Plus className="size-3" />
          )}
          Add box
        </Button>
      </div>

      <ProvisionBoxForm wsId={wsId} />
    </div>
  );
}

// Provision a per-developer QA box: pick a member, optionally name the handle,
// PREVIEW the exact runbook (dry-run — touches nothing), then provision for
// real. The box is carved out of the shared QA host (AGORA_QA_HOST_*) as
// `<handle>.<base-domain>` and registered owned by that member, so the member's
// branches deploy to their own isolated environment.
function ProvisionBoxForm({ wsId }: { wsId: string }) {
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const provision = useProvisionRemoteBox(wsId);
  const [memberId, setMemberId] = useState("");
  const [handle, setHandle] = useState("");
  const [preview, setPreview] = useState<ProvisionBoxResult | null>(null);

  const run = async (dryRun: boolean) => {
    if (!memberId || provision.isPending) return;
    try {
      const res = await provision.mutateAsync({
        member_id: memberId,
        handle: handle.trim() || undefined,
        dry_run: dryRun,
      });
      if (dryRun) {
        setPreview(res);
        return;
      }
      setPreview(null);
      setHandle("");
      if (res.ok) toast.success(`Provisioned ${res.subdomain}`);
      else toast.error(`Provision failed: ${res.output?.slice(0, 200) || "see box status"}`);
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : "Provision failed");
    }
  };

  return (
    <div className="space-y-2 rounded-md border border-dashed p-2.5">
      <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
        <UserPlus className="size-3.5" />
        Provision a per-developer box
      </div>
      <p className="text-[10.5px] text-muted-foreground">
        Carves <span className="font-mono">{"<handle>.<qa-host>"}</span> out of the shared QA host
        for a member. Preview shows the exact runbook before anything runs.
      </p>
      <div className="flex flex-wrap items-center gap-1.5">
        <select
          value={memberId}
          onChange={(e) => {
            setMemberId(e.target.value);
            setPreview(null);
          }}
          aria-label="Member"
          className="h-7 w-44 rounded-md border bg-transparent px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          <option value="">Select member…</option>
          {members.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name || m.email}
            </option>
          ))}
        </select>
        <input
          type="text"
          value={handle}
          onChange={(e) => {
            setHandle(e.target.value);
            setPreview(null);
          }}
          placeholder="handle (optional, e.g. shakhzod)"
          aria-label="Box handle"
          className="h-7 w-52 rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-7 px-2 text-xs"
          disabled={!memberId || provision.isPending}
          onClick={() => void run(true)}
        >
          {provision.isPending && !preview ? <Loader2 className="size-3 animate-spin" /> : "Preview"}
        </Button>
      </div>

      {preview && (
        <div className="space-y-1.5">
          <div className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-[10.5px]">
            <span className="text-muted-foreground">Subdomain</span>
            <span className="font-mono">{preview.subdomain}</span>
            <span className="text-muted-foreground">Work dir</span>
            <span className="font-mono">{preview.work_dir}</span>
            <span className="text-muted-foreground">Database</span>
            <span className="font-mono">{preview.database}</span>
          </div>
          <pre className="max-h-48 overflow-auto rounded-md border bg-muted/40 p-2 text-[10px] leading-relaxed whitespace-pre-wrap break-all">
            {preview.script}
          </pre>
          <div className="flex items-center gap-1.5">
            <Button
              type="button"
              size="sm"
              className="h-7 px-2 text-xs"
              disabled={provision.isPending}
              onClick={() => void run(false)}
            >
              {provision.isPending ? <Loader2 className="size-3 animate-spin" /> : "Provision for real"}
            </Button>
            <span className="text-[10.5px] text-muted-foreground">
              Runs the runbook above on the QA host.
            </span>
          </div>
        </div>
      )}
    </div>
  );
}

// One box row: target + status + a branch-sync action (checkout a branch on the
// box so it serves that branch) + remove.
function BoxRow({
  box,
  wsId,
  onRemove,
}: {
  box: ConnectedBox;
  wsId: string;
  onRemove: () => void;
}) {
  const syncBox = useSyncRemoteBox(wsId);
  const [branch, setBranch] = useState(box.last_branch || "");

  const handleSync = async () => {
    const b = branch.trim();
    if (!b || syncBox.isPending) return;
    try {
      const res = await syncBox.mutateAsync({ id: box.id, branch: b });
      if (res.ok) toast.success(`Synced ${box.label} → ${b}`);
      else toast.error(`Sync failed: ${res.output?.slice(0, 200) || "see box status"}`);
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : "Failed to sync box",
      );
    }
  };

  const canSync = box.repo_url !== "" && box.work_dir !== "";

  return (
    <li className="rounded-md border px-2.5 py-1.5 text-xs group">
      <div className="flex items-center gap-2">
        <span className="font-medium">{box.label}</span>
        <span className="truncate font-mono text-[10.5px] text-muted-foreground">
          {box.ssh_user}@{box.ssh_host}
          {box.ssh_port !== 22 ? `:${box.ssh_port}` : ""}
        </span>
        <span
          className={cn(
            "ml-auto text-[10.5px] font-medium",
            STATUS_STYLE[box.status] ?? "text-muted-foreground",
          )}
        >
          {box.status}
        </span>
        <button
          type="button"
          aria-label={`Remove ${box.label}`}
          onClick={onRemove}
          className="opacity-0 transition-opacity group-hover:opacity-100 rounded-sm p-0.5 hover:bg-accent"
        >
          <Trash2 className="size-3 text-muted-foreground" />
        </button>
      </div>
      {canSync && (
        <div className="mt-1.5 flex items-center gap-1.5">
          <GitBranch className="size-3 shrink-0 text-muted-foreground" />
          <input
            type="text"
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void handleSync();
            }}
            placeholder="branch to check out"
            aria-label={`Branch for ${box.label}`}
            className="h-6 flex-1 rounded-md border bg-transparent px-2 font-mono text-[10.5px] outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-6 px-2 text-[10.5px]"
            disabled={branch.trim() === "" || syncBox.isPending}
            onClick={() => void handleSync()}
          >
            {syncBox.isPending ? <Loader2 className="size-3 animate-spin" /> : "Sync"}
          </Button>
        </div>
      )}
    </li>
  );
}
