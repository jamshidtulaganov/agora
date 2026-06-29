"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Layers, Loader2, Check } from "lucide-react";
import { api } from "@agora/core/api";
import { agentListOptions } from "@agora/core/workspace/queries";
import { issueKeys } from "@agora/core/issues/queries";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";

// Parallel variants — the modern-dev "try N approaches, pick the best". Fires
// the same task to several agents at once by posting ONE comment that @mentions
// them all; Agora's mention fan-out enqueues a task per agent, each in its own
// worktree/branch. The human then compares them with the editor's per-agent
// chips and Accepts the winner (Discards the rest) via the review bar — so this
// is pure dispatch, reusing the review surfaces already built.

export function EditorVariantsDialog({
  issueId,
  open,
  onOpenChange,
}: {
  issueId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState("");
  const [running, setRunning] = useState(false);

  const usable = agents
    .filter((a) => !a.archived_at)
    .sort((a, b) => {
      const ao = a.status === "offline" ? 1 : 0;
      const bo = b.status === "offline" ? 1 : 0;
      return ao - bo || a.name.localeCompare(b.name);
    });

  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  const run = async () => {
    const ids = [...selected];
    if (ids.length === 0 || !prompt.trim()) return;
    setRunning(true);
    try {
      const mentions = ids
        .map((id) => {
          const a = agents.find((x) => x.id === id);
          return `[@${a?.name ?? "agent"}](mention://agent/${id})`;
        })
        .join(" ");
      await api.createComment(issueId, `${prompt.trim()}\n\n${mentions}`);
      qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      qc.invalidateQueries({
        queryKey: agentTaskSnapshotOptions(wsId).queryKey,
      });
      onOpenChange(false);
      setPrompt("");
      setSelected(new Set());
    } finally {
      setRunning(false);
    }
  };

  const count = selected.size;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85vh] max-w-md flex-col gap-3 overflow-hidden p-4">
        <DialogTitle className="flex items-center gap-1.5 text-sm">
          <Layers className="h-4 w-4 text-primary" />
          Run parallel variants
        </DialogTitle>
        <p className="text-xs leading-snug text-muted-foreground">
          Fire the same task to several agents at once — each works on its own
          branch. Compare them with the agent chips, then Accept the best (and
          Discard the rest).
        </p>

        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder="What should they each do? e.g. Implement the CSV export endpoint with tests."
          className="h-24 w-full resize-none rounded-md border border-border bg-background p-2 text-xs outline-none focus:border-primary/50"
        />

        <div className="flex min-h-0 flex-1 flex-col gap-1">
          <div className="text-[11px] font-medium text-muted-foreground">
            Agents{count > 0 ? ` · ${count} selected` : ""}
          </div>
          <div className="min-h-0 flex-1 space-y-1 overflow-y-auto">
            {usable.length === 0 && (
              <p className="px-1 py-2 text-[11px] text-muted-foreground">
                No agents in this workspace yet.
              </p>
            )}
            {usable.map((a) => {
              const on = selected.has(a.id);
              const offline = a.status === "offline";
              return (
                <button
                  key={a.id}
                  type="button"
                  onClick={() => toggle(a.id)}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md border px-2 py-1.5 text-xs transition-colors",
                    on
                      ? "border-primary bg-primary/10"
                      : "border-border hover:bg-accent",
                  )}
                >
                  <ActorAvatar actorType="agent" actorId={a.id} size={18} />
                  <span className="min-w-0 flex-1 truncate text-left">
                    {a.name}
                  </span>
                  {offline && (
                    <span className="shrink-0 text-[10px] text-muted-foreground">
                      offline
                    </span>
                  )}
                  {on && <Check className="h-3.5 w-3.5 shrink-0 text-primary" />}
                </button>
              );
            })}
          </div>
        </div>

        <button
          type="button"
          onClick={() => void run()}
          disabled={running || count === 0 || !prompt.trim()}
          className="inline-flex items-center justify-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
        >
          {running ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
          {running
            ? "Dispatching…"
            : `Run ${count || ""} variant${count === 1 ? "" : "s"}`.replace(
                "  ",
                " ",
              )}
        </button>
      </DialogContent>
    </Dialog>
  );
}
