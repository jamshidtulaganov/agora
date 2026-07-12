/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ShieldCheck, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { issueKeys } from "@agora/core/issues/queries";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { useWorkspaceId } from "@agora/core/hooks";

// Run-QA — fires the agent to QA its own change as a DETERMINISTIC gate (report
// by exit code, not opinion) and set the qa:pass/qa:fail label that the Gates
// panel + MergeReadiness consume. Delegates to the backend `run_qa` slice action
// (the SINGLE source of truth for the QA recipe — build/lint/test + Playwright
// preview smoke, project-configurable via project.settings) rather than carrying
// its own copy of the prompt, so the editor and the issue Prompts panel can
// never drift. agentId targets the editor's selected agent (its worktree).

export function EditorRunQA({
  issueId,
  agent,
}: {
  issueId: string;
  agent: { agent_id: string; agent_name: string } | null;
}) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);

  const run = async () => {
    if (!agent || busy) return;
    setBusy(true);
    try {
      await api.sliceAction(issueId, {
        kind: "run_qa",
        agentId: agent.agent_id,
      });
      qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      qc.invalidateQueries({
        queryKey: agentTaskSnapshotOptions(wsId).queryKey,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <button
      type="button"
      onClick={() => void run()}
      disabled={!agent || busy}
      title="Run a deterministic QA gate (checks + Playwright preview smoke) → sets the qa label"
      className="inline-flex items-center gap-1 text-[10px] text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
    >
      {busy ? (
        <Loader2 className="h-3 w-3 animate-spin" />
      ) : (
        <ShieldCheck className="h-3 w-3" />
      )}
      Run QA
    </button>
  );
}
