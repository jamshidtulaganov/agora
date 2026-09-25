"use client";

import { useState } from "react";
import { Pencil } from "lucide-react";
import { toast } from "sonner";
import { useUpdateWorkspaceInstructions } from "@agora/core/knowledge";
import type { Workspace } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { useT } from "../../i18n";

/**
 * "Instructions for AI" — the workspace's `context`, always handed to the
 * Assistant and every agent in this workspace. Owners and admins edit it in
 * place; everyone else reads it.
 */
export function InstructionsCard({
  workspace,
  canManage,
}: {
  workspace: Workspace;
  canManage: boolean;
}) {
  const { t } = useT("knowledge");
  const update = useUpdateWorkspaceInstructions();
  const [draft, setDraft] = useState<string | null>(null);
  const editing = draft !== null;
  const value = workspace.context ?? "";

  const save = () => {
    if (draft === null) return;
    const next = draft;
    // Optimistic: the text shows as saved straight away. On failure the editor
    // comes back with what was typed, so nothing is lost.
    setDraft(null);
    update.mutate(
      { workspaceId: workspace.id, context: next },
      {
        onError: (err) => {
          setDraft(next);
          toast.error(t(($) => $.instructions.save_failed), {
            description: err instanceof Error ? err.message : undefined,
          });
        },
      },
    );
  };

  return (
    <section
      aria-labelledby="knowledge-instructions-title"
      className="rounded-lg border bg-card p-4"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id="knowledge-instructions-title" className="text-sm font-semibold">
            {t(($) => $.instructions.title)}
          </h2>
          <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.instructions.hint)}</p>
        </div>
        {canManage && !editing && (
          <Button variant="ghost" size="sm" onClick={() => setDraft(value)}>
            <Pencil className="h-3 w-3" />
            {t(($) => $.instructions.edit)}
          </Button>
        )}
      </div>

      {editing ? (
        <div className="mt-3 space-y-2">
          <Textarea
            autoFocus
            aria-label={t(($) => $.instructions.title)}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.preventDefault();
                setDraft(null);
              } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                save();
              }
            }}
            rows={6}
            className="resize-y text-sm"
            placeholder={t(($) => $.instructions.placeholder)}
          />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setDraft(null)}>
              {t(($) => $.instructions.cancel)}
            </Button>
            <Button size="sm" onClick={save} disabled={draft === value}>
              {t(($) => $.instructions.save)}
            </Button>
          </div>
        </div>
      ) : value.trim() ? (
        <p className="mt-3 max-h-64 overflow-y-auto whitespace-pre-wrap break-words text-sm">{value}</p>
      ) : (
        <p className="mt-3 text-sm text-muted-foreground">{t(($) => $.instructions.empty)}</p>
      )}
    </section>
  );
}
