"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Sparkles, ArrowUp, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { issueKeys } from "@agora/core/issues/queries";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { useWorkspaceId } from "@agora/core/hooks";

// The "put the agent to work" front-door — the one obvious entry into the
// co-code loop. Type plain intent ("add a /status endpoint"), it @mentions the
// selected agent and posts it, which triggers the agent to run on this issue's
// worktree (reusing it, so it edits the files you're viewing). No need to know
// the @mention syntax or hunt across tabs. onSent flips the panel to Activity so
// you immediately watch it work.

interface AskAgent {
  agent_id: string;
  agent_name: string;
}

export function EditorAskBar({
  issueId,
  agent,
  onSent,
}: {
  issueId: string;
  agent: AskAgent | null;
  onSent?: () => void;
}) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);

  const ask = async () => {
    const msg = text.trim();
    if (!msg || !agent || sending) return;
    setSending(true);
    try {
      await api.createComment(
        issueId,
        `[@${agent.agent_name}](mention://agent/${agent.agent_id}) ${msg}`,
      );
      setText("");
      qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      qc.invalidateQueries({
        queryKey: agentTaskSnapshotOptions(wsId).queryKey,
      });
      onSent?.();
    } finally {
      setSending(false);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void ask();
      }}
      className="flex shrink-0 items-center gap-2 border-b border-border bg-muted/20 px-3 py-2"
    >
      <Sparkles className="h-4 w-4 shrink-0 text-primary" />
      <input
        value={text}
        onChange={(e) => setText(e.target.value)}
        disabled={!agent}
        placeholder={
          agent
            ? `Ask ${agent.agent_name} to build, fix, or change something…`
            : "Assign an agent to this issue to start"
        }
        className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground disabled:opacity-60"
      />
      <button
        type="submit"
        disabled={!text.trim() || sending || !agent}
        className="inline-flex shrink-0 items-center gap-1 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
      >
        {sending ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : (
          <ArrowUp className="h-3.5 w-3.5" />
        )}
        Ask
      </button>
    </form>
  );
}
