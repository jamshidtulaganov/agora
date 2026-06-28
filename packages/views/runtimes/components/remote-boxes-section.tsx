"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Server, Plus, Trash2, Loader2 } from "lucide-react";
import { toast } from "sonner";
import {
  remoteBoxesOptions,
  useCreateRemoteBox,
  useDeleteRemoteBox,
} from "@agora/core/runtimes";
import type { ConnectedBox } from "@agora/core/types";
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

  const canSubmit =
    label.trim() !== "" && host.trim() !== "" && user.trim() !== "" && !createBox.isPending;

  const handleAdd = async () => {
    if (!canSubmit) return;
    try {
      await createBox.mutateAsync({
        label: label.trim(),
        ssh_host: host.trim(),
        ssh_user: user.trim(),
      });
      setLabel("");
      setHost("");
      setUser("");
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
          No remote boxes yet. Add a developer&apos;s server below — Agora installs a
          native daemon on it over SSH.
        </p>
      ) : (
        <ul className="space-y-1.5">
          {boxes.map((box) => (
            <li
              key={box.id}
              className="flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs group"
            >
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
                onClick={() => void handleDelete(box)}
                className="opacity-0 transition-opacity group-hover:opacity-100 rounded-sm p-0.5 hover:bg-accent"
              >
                <Trash2 className="size-3 text-muted-foreground" />
              </button>
            </li>
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
    </div>
  );
}
