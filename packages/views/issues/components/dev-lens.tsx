"use client";

import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { useT } from "../../i18n";
import { EditorWorkbench, useEditorSession } from "./editor-workbench";

// The Dev lens — the co-code editor workbench mounted as the cockpit's Dev
// stage (docs/sdlc-stage-cockpit-plan.md, phase F). The exact surface the
// issue view's editor Dialog hosts (editor-section.tsx), re-homed as a frame
// content region: the cockpit header + SDLC stepper replace the Dialog
// chrome, and the rail auto-collapses (dev is a wide lens in issue-detail)
// so the editor gets the full width.
//
// The lens owns its own editor session: it resolves the issue's EXISTING
// worktrees and opens code-server on the most recent one — the same thing
// opening the sidebar editor section does. It never creates a worktree or
// kicks off an agent; an issue nothing has run on lands on the empty state
// below, and assigning an agent is what starts a session.
//
// The editor is a fixed-viewport surface, not a scrolling document — the
// container takes the viewport height minus the cockpit chrome (h-12 header
// + h-9 stepper = 5.25rem, plus this container's own py-4 and a little
// slack) so the frame never grows a second scrollbar.

export function DevLensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");

  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));

  if (isLoading || !issue) {
    return (
      <div className="mx-auto w-full max-w-4xl px-8 py-8">
        <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
      </div>
    );
  }

  return (
    <DevLensSession
      // Key by issue so navigating between issues under ?lens=dev can never
      // show a stale session (the hook's launch state is per-mount).
      key={issueId}
      issueId={issueId}
      issueKey={issue.identifier}
      issueTitle={issue.title}
      projectId={issue.project_id}
    />
  );
}

function DevLensSession({
  issueId,
  issueKey,
  issueTitle,
  projectId,
}: {
  issueId: string;
  issueKey: string;
  issueTitle: string;
  projectId: string | null;
}) {
  const { t } = useT("issues");
  const session = useEditorSession(issueId, true);

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 w-full flex-col px-4 py-4">
      {session.state === "none" ? (
        <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
          <p className="text-[12px] text-muted-foreground">{t(($) => $.dev_lens.empty)}</p>
          <p className="mt-0.5 text-[11px] text-muted-foreground/70">
            {t(($) => $.dev_lens.empty_hint)}
          </p>
        </div>
      ) : session.state === "error" ? (
        <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
          <p className="text-[12px] text-destructive">{session.err}</p>
          <button
            type="button"
            onClick={() => void session.launch()}
            className="mt-1 text-[11px] text-muted-foreground underline hover:no-underline"
          >
            {t(($) => $.dev_lens.retry)}
          </button>
        </div>
      ) : session.url ? (
        // `url` (not state === "ready") keeps the workbench mounted through an
        // agent-switch relaunch, mirroring the Dialog path.
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border">
          <EditorWorkbench
            issueId={issueId}
            issueKey={issueKey}
            issueTitle={issueTitle}
            projectId={projectId}
            session={session}
          />
        </div>
      ) : (
        <div className="flex items-center gap-2 py-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          {t(($) => $.dev_lens.launching)}
        </div>
      )}
    </div>
  );
}
