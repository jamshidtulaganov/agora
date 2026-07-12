/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Layers, Loader2, Check } from "lucide-react";
import { api } from "@agora/core/api";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { useWorkspaceId } from "@agora/core/hooks";

// Context panel — the prompt-engineer's lever. The human writes per-issue
// context (rules, files to focus on, links, constraints) that the backend
// injects into EVERY agent run on this issue (editor or prompts), the same way
// the co-code note layers onto a run. Stored on the issue metadata key
// "agent_context". Output quality = f(context), made editable.

export function EditorContextPanel({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { data: issue } = useQuery(issueDetailOptions(wsId, issueId));
  const saved =
    typeof issue?.metadata?.agent_context === "string"
      ? issue.metadata.agent_context
      : "";

  const [draft, setDraft] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const value = draft ?? saved;
  const dirty = draft !== null && draft.trim() !== saved.trim();

  const save = async () => {
    if (!dirty) return;
    setSaving(true);
    try {
      await api.setIssueMetadataKey(
        issueId,
        "agent_context",
        (draft ?? "").trim(),
      );
      await qc.invalidateQueries({
        queryKey: issueDetailOptions(wsId, issueId).queryKey,
      });
      setDraft(null);
      setJustSaved(true);
      window.setTimeout(() => setJustSaved(false), 1500);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col">
      <div className="shrink-0 border-b border-border px-3 py-2">
        <div className="flex items-center gap-1.5 text-xs font-medium">
          <Layers className="h-3.5 w-3.5 text-primary" />
          Agent context
        </div>
        <p className="mt-0.5 text-[10px] text-muted-foreground">
          Injected into every agent run on this issue.
        </p>
      </div>

      <div className="flex-1 space-y-2 overflow-y-auto p-3">
        <textarea
          value={value}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={
            "Rules, files to focus on, links, constraints…\n\ne.g. Only touch src/billing/*. Use functional React + hooks. Follow the patterns in invoice.tsx. API spec: https://…"
          }
          className="h-44 w-full resize-none rounded-md border border-border bg-background p-2 text-xs outline-none focus:border-primary/50"
        />
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => void save()}
            disabled={!dirty || saving}
            className="inline-flex items-center gap-1 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
          >
            {saving ? (
              <Loader2 className="h-3 w-3 animate-spin" />
            ) : justSaved ? (
              <Check className="h-3 w-3" />
            ) : null}
            {saving ? "Saving…" : justSaved ? "Saved" : "Save context"}
          </button>
          {dirty && !saving && (
            <span className="text-[10px] text-muted-foreground">unsaved</span>
          )}
        </div>

        <div className="rounded-md border border-border bg-muted/30 p-2 text-[10px] leading-snug text-muted-foreground">
          <span className="font-medium text-foreground">
            The agent also sees:
          </span>{" "}
          the connected repo(s), the project knowledge base, its skills, and the
          issue + comments. Add only what&apos;s missing or must be emphasized.
        </div>
      </div>
    </div>
  );
}
