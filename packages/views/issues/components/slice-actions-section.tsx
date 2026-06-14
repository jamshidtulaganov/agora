"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, FileCode2, FileText, FlaskConical, Loader2, ScanEye } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { issueKeys, issueDetailOptions } from "@agora/core/issues/queries";
import { agentListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { useAuthStore } from "@agora/core/auth";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { useT } from "../../i18n";

// Right-panel section that lets a human protagonist hand a *slice* of work to
// the issue's agent without writing a comment by hand. Each button POSTs to
// `/api/issues/{id}/slice-actions` with a `kind` (and an optional free-form
// `scope`). The backend owns the instruction templates and posts the agent
// draft as a comment, which streams back into the timeline + ExecutionLog —
// so this section only fires the action and surfaces a toast; it does NOT
// render the draft itself (that's the execution log's job, mounted directly
// below this section).
//
// Gating mirrors the comment composer's trigger model: a slice action only
// does something if *some* agent will pick it up. We treat the issue as
// "agent-reachable" when either (a) the issue is assigned to an agent, or
// (b) the caller owns at least one ready (non-archived, online-ish) agent
// that the backend can fall back to. When neither holds we replace the
// buttons with a hint pointing at the Assignee picker, the same place the
// user fixes this everywhere else.

interface SliceActionsSectionProps {
  issueId: string;
}

// Action kinds match the backend enum exactly. The instruction text lives on
// the server (do not duplicate it here) — the front-end only sends the kind.
type SliceActionKind = "draft_code" | "write_docs" | "write_tests" | "review_part";

const ACTIONS: ReadonlyArray<{
  kind: SliceActionKind;
  icon: React.ComponentType<{ className?: string }>;
}> = [
  { kind: "draft_code", icon: FileCode2 },
  { kind: "write_docs", icon: FileText },
  { kind: "write_tests", icon: FlaskConical },
  { kind: "review_part", icon: ScanEye },
];

export function SliceActionsSection({ issueId }: SliceActionsSectionProps) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const userId = useAuthStore((s) => s.user?.id);

  const [open, setOpen] = useState(true);
  const [scope, setScope] = useState("");
  // Which kind is mid-flight; null when idle. One in-flight action at a time —
  // firing two slices at once is almost always an accidental double-fire.
  const [pendingKind, setPendingKind] = useState<SliceActionKind | null>(null);

  const { data: issue = null } = useQuery({
    ...issueDetailOptions(wsId, issueId),
  });
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  // Agent-reachable when the issue is assigned to an agent OR the caller owns
  // a ready agent the backend can fall back to. "Ready" = not archived and
  // not offline (mirrors the presence model used by the comment composer).
  const hasAgentAssignee = issue?.assignee_type === "agent" && !!issue?.assignee_id;
  const hasOwnReadyAgent = agents.some(
    (a) => a.owner_id === userId && !a.archived_at && a.status !== "offline",
  );
  const agentReachable = hasAgentAssignee || hasOwnReadyAgent;

  const fireAction = async (kind: SliceActionKind) => {
    if (pendingKind) return;
    setPendingKind(kind);
    const trimmedScope = scope.trim();
    try {
      await api.sliceAction(issueId, {
        kind,
        ...(trimmedScope ? { scope: trimmedScope } : {}),
      });
      // The agent draft arrives as a comment + task; nudge the execution log
      // and timeline so the run appears without waiting for the WS round-trip.
      void queryClient.invalidateQueries({ queryKey: issueKeys.tasks(issueId) });
      void queryClient.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      toast.success(t(($) => $.slice_actions.toast_fired));
      setScope("");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.slice_actions.toast_failed));
    } finally {
      setPendingKind(null);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${
          open ? "" : "text-muted-foreground hover:text-foreground"
        }`}
        onClick={() => setOpen(!open)}
      >
        {t(($) => $.slice_actions.section)}
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${
            open ? "rotate-90" : ""
          }`}
        />
      </button>
      {open && (
        <div className="space-y-2 pl-2">
          {agentReachable ? (
            <>
              <Input
                value={scope}
                onChange={(e) => setScope(e.target.value)}
                placeholder={t(($) => $.slice_actions.scope_placeholder)}
                aria-label={t(($) => $.slice_actions.scope_placeholder)}
                className="h-7 text-xs"
              />
              <div className="grid grid-cols-2 gap-1.5">
                {ACTIONS.map(({ kind, icon: Icon }) => {
                  const busy = pendingKind === kind;
                  return (
                    <Button
                      key={kind}
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={!!pendingKind}
                      onClick={() => void fireAction(kind)}
                      className="justify-start"
                    >
                      {busy ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <Icon className="size-3.5" />
                      )}
                      <span className="truncate">
                        {t(($) => $.slice_actions[`action_${kind}`])}
                      </span>
                    </Button>
                  );
                })}
              </div>
            </>
          ) : (
            <p className="px-1 text-xs leading-5 text-muted-foreground">
              {t(($) => $.slice_actions.no_agent_hint)}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
