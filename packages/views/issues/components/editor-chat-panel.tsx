/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useState } from "react";
import { Crosshair, X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { issueTimelineOptions, issueKeys } from "@agora/core/issues/queries";
import { useAuthStore } from "@agora/core/auth";
import { useActorName } from "@agora/core/workspace/hooks";
import type { TimelineEntry } from "@agora/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { CommentInput } from "./comment-input";

// Co-code chat panel (right of the editor modal). The input is the shared
// CommentInput, so typing "@" opens the full agent/member/squad mention
// autocomplete; posting an @agent comment triggers that agent to run on the
// issue, reusing its worktree → it edits the files the human is viewing. The
// conversation is the issue's own comment timeline.

interface ChatAgent {
  agent_id: string;
  agent_name: string;
}

// Collapse mention markdown to a readable @Name for the compact chat view.
function stripMentions(s: string): string {
  return s.replace(/\[@?(.+?)\]\(mention:\/\/[^)]+\)/g, "@$1");
}

export function EditorChatPanel({
  issueId,
  agent,
}: {
  issueId: string;
  agent: ChatAgent | null;
}) {
  const userId = useAuthStore((s) => s.user?.id);
  const { getActorName } = useActorName();
  const qc = useQueryClient();
  const [focus, setFocus] = useState("");
  const { data: timeline = [] } = useQuery(issueTimelineOptions(issueId));

  const comments = (timeline as TimelineEntry[]).filter(
    (e) => e.type === "comment",
  );

  const handleSubmit = async (content: string, attachmentIds?: string[]) => {
    // Narrow the agent to one file/function without changing the message —
    // mirrors the slice-action "Focus on: <scope>" clause.
    const focused = focus.trim();
    const body = focused ? `${content}\n\nFocus on \`${focused}\`.` : content;
    await api.createComment(
      issueId,
      body,
      undefined,
      undefined,
      attachmentIds,
    );
    setFocus("");
    qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
  };

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-border px-3 py-2">
        {agent ? (
          <>
            <ActorAvatar actorType="agent" actorId={agent.agent_id} size={18} />
            <div className="min-w-0">
              <div className="truncate text-xs font-medium">
                {agent.agent_name}
              </div>
              <div className="text-[10px] text-muted-foreground">
                Type @ to tag an agent to change the code
              </div>
            </div>
          </>
        ) : (
          <div className="text-xs font-medium">Chat</div>
        )}
      </div>

      <div className="flex-1 space-y-2.5 overflow-y-auto p-3">
        {comments.length === 0 && (
          <p className="text-xs text-muted-foreground">
            No messages yet — type @ to tag an agent and ask it to change
            something.
          </p>
        )}
        {comments.map((c) => {
          const mine = c.actor_type === "member" && c.actor_id === userId;
          return (
            <div key={c.id} className="flex gap-2">
              <ActorAvatar
                actorType={c.actor_type}
                actorId={c.actor_id}
                size={18}
                className="mt-0.5 shrink-0"
              />
              <div className="min-w-0 flex-1">
                <div className="text-[11px] font-medium">
                  {getActorName(c.actor_type, c.actor_id)}
                  {mine && (
                    <span className="ml-1 text-muted-foreground">(you)</span>
                  )}
                </div>
                <div className="whitespace-pre-wrap break-words text-xs text-muted-foreground">
                  {stripMentions(c.content || "")}
                </div>
              </div>
            </div>
          );
        })}
      </div>

      <div className="space-y-1.5 border-t border-border p-2">
        <div className="flex items-center gap-1 rounded-md border border-border bg-background px-1.5 focus-within:border-primary/50">
          <Crosshair className="h-3 w-3 shrink-0 text-muted-foreground" />
          <input
            type="text"
            value={focus}
            onChange={(e) => setFocus(e.target.value)}
            placeholder="Focus on a file or function (optional)"
            className="min-w-0 flex-1 bg-transparent py-1 text-[11px] outline-none placeholder:text-muted-foreground"
          />
          {focus && (
            <button
              type="button"
              onClick={() => setFocus("")}
              title="Clear focus"
              className="shrink-0 rounded p-0.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>
        <CommentInput issueId={issueId} onSubmit={handleSubmit} />
      </div>
    </div>
  );
}
