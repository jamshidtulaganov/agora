/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useState, useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import {
  ChevronRight,
  X,
  ExternalLink,
  Expand,
  Loader2,
  HelpCircle,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { EditorReviewBar } from "./editor-review-bar";
import {
  EditorHowItWorks,
  useHowItWorksDismissed,
} from "./editor-how-it-works";
import { EditorRunQA } from "./editor-run-qa";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  EditorWorkbench,
  useEditorSession,
  type EditorWorkbenchPane,
} from "./editor-workbench";

// Right-panel "Code" section: launches a browser VS Code (code-server) on the
// issue's agent worktree and iframes it, so a human can watch + edit the live
// code. Every agent that ran on the issue keeps its own worktree, so the chip
// row at the top lets the human switch between agents to review each one's work.
// Self-host flow: GET /api/issues/{id}/editor → {daemon_url, agents:[{work_dir}]}
// then POST {daemon_url}/editor/launch {workdir} → {url}. Lazy: nothing runs
// until the section is opened.
//
// The editor surface itself (pane switcher, ask bar, right rail) lives in
// EditorWorkbench (editor-workbench.tsx) — this section owns the collapsed
// sidebar preview, the session, and the full-screen Dialog that hosts the
// workbench. The cockpit's Dev lens mounts the same workbench without the
// Dialog (docs/sdlc-stage-cockpit-plan.md, phase F).

interface EditorSectionProps {
  issueId: string;
  /** Auto-open + launch (the issue is in in_editor co-code mode). */
  defaultOpen?: boolean;
  /** Show the co-coding badge (the issue is in in_editor co-code mode). */
  coCode?: boolean;
  /** Issue identifier (e.g. "SDM-107") — used for the Accept→PR title/Closes. */
  issueKey?: string;
  /** Issue title — used for the Accept→PR title. */
  issueTitle?: string;
  /** Issue's parent project — resolves the bound QA box for the deploy action. */
  projectId?: string | null;
}

export function EditorSection({
  issueId,
  defaultOpen = false,
  coCode = false,
  issueKey,
  issueTitle,
  projectId = null,
}: EditorSectionProps) {
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(defaultOpen);
  // The editor session (worktree lookup + code-server launch + agent roster).
  // Owned here — not by the workbench — because the collapsed sidebar UI below
  // and the Dialog share it: the same instance is passed into the workbench,
  // so expanding the modal never re-launches. Launches once the section opens.
  const session = useEditorSession(issueId, open);
  const { state, url, err, daemon, selectedAgent, launch } = session;
  // Expand into a near-fullscreen modal (real coding needs width). One iframe
  // at a time — the inline preview unmounts while the modal is open.
  const [expanded, setExpanded] = useState(false);
  // Left pane of the modal, kept section-owned (controlled into the workbench)
  // so auto-expand-to-Live below and pane persistence across dialog
  // close/reopen behave exactly as before the workbench extraction.
  const [leftPane, setLeftPane] = useState<EditorWorkbenchPane>("live");
  // Is an agent currently running on this issue? Drives the auto-selection of
  // the spectator view when the modal auto-expands.
  const { data: taskSnapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const hasLiveRun = useMemo(
    () =>
      taskSnapshot.some(
        (task) => task.issue_id === issueId && task.status === "running",
      ),
    [taskSnapshot, issueId],
  );
  // First-time "how co-code works" explainer (auto-shows once, re-openable).
  const { dismissed: helpDismissed, dismiss: dismissHelp } =
    useHowItWorksDismissed();
  const [showHelp, setShowHelp] = useState(false);
  // Co-code: the modal is the real workspace (the inline panel is too narrow),
  // so auto-expand it once the editor is ready. The user can still close it for
  // this visit (autoExpandedOnce guards against re-popping on every render).
  const [autoExpandedOnce, setAutoExpandedOnce] = useState(false);
  const helpVisible = showHelp || !helpDismissed;
  const closeHelp = () => {
    dismissHelp();
    setShowHelp(false);
  };

  const toggle = () => setOpen((o) => !o);

  // Open when the issue switches into in_editor mode (the session launches
  // itself once open — see useEditorSession).
  useEffect(() => {
    if (defaultOpen) setOpen(true);
  }, [defaultOpen]);
  // Auto-expand into the full modal for co-code once the editor is ready. When
  // an agent is actively coding, open on the Live spectator view — that's the
  // "watch it work" moment; the user can hop to Code anytime.
  useEffect(() => {
    if (coCode && state === "ready" && url && !expanded && !autoExpandedOnce) {
      setExpanded(true);
      setAutoExpandedOnce(true);
      if (hasLiveRun) setLeftPane("live");
    }
  }, [coCode, state, url, expanded, autoExpandedOnce, hasLiveRun]);

  // Trust bar: branch isolation + CI status + Accept (→PR) / Discard. Self-host
  // only (talks to the daemon directly), keyed to the agent currently shown.
  // The workbench renders its own copy inside the modal's right rail; this one
  // is the inline (collapsed-section) instance.
  const reviewBar =
    daemon && selectedAgent ? (
      <EditorReviewBar
        issueId={issueId}
        issueKey={issueKey}
        issueTitle={issueTitle}
        daemonUrl={daemon.url}
        workdir={selectedAgent.work_dir}
      />
    ) : null;

  // QA / variants / help controls. Rendered in BOTH the inline header and the
  // expanded modal header — otherwise the modal overlay hides the inline row
  // (the editor auto-expands for co-code, so these must live inside the modal).
  const editorActions = (
    <div className="flex items-center gap-3">
      <EditorRunQA issueId={issueId} agent={selectedAgent} />
      <button
        type="button"
        onClick={() => setShowHelp(true)}
        className="inline-flex items-center gap-1 text-[10px] text-muted-foreground transition-colors hover:text-foreground"
      >
        <HelpCircle className="h-3 w-3" />
        How it works
      </button>
    </div>
  );

  return (
    <div>
      <button
        type="button"
        onClick={toggle}
        className={`mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70 ${
          open ? "" : "text-muted-foreground hover:text-foreground"
        }`}
      >
        Code editor
        {coCode && (
          <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-primary">
            Co-code
          </span>
        )}
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${
            open ? "rotate-90" : ""
          }`}
        />
      </button>

      {open && (
        <div className="space-y-1.5 pl-2">
          <div className="flex justify-end">{editorActions}</div>
          {helpVisible && <EditorHowItWorks onClose={closeHelp} />}

          {state === "loading" && (
            <div className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              launching VS Code…
            </div>
          )}

          {state === "none" && (
            <div className="space-y-1.5 rounded-md border border-border bg-muted/30 px-2.5 py-2 text-xs text-muted-foreground">
              <p className="font-medium text-foreground">
                No editor worktree yet
              </p>
              <p className="leading-snug">
                A worktree is created when an agent/runtime runs this issue.
                Connect a repository above and assign an agent — your live editor
                opens here once it starts.
              </p>
            </div>
          )}

          {state === "error" && (
            <div className="py-2 text-xs text-destructive">
              {err}{" "}
              <button
                type="button"
                onClick={() => void launch()}
                className="underline hover:no-underline"
              >
                retry
              </button>
            </div>
          )}

          {state === "ready" && url && (
            <div className="space-y-1.5">
              {!expanded && reviewBar && (
                <div className="overflow-hidden rounded-lg border border-border">
                  {reviewBar}
                </div>
              )}
              {/* The side panel is far too narrow for a usable VS Code — don't
                  squeeze the iframe in here. One affordance: open the real
                  workspace (the near-fullscreen modal). */}
              {!expanded && (
                <button
                  type="button"
                  onClick={() => setExpanded(true)}
                  className="flex w-full items-center justify-center gap-2 rounded-lg border border-dashed border-border bg-muted/30 px-3 py-8 text-xs text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground"
                >
                  <Expand className="size-3.5" />
                  Open co-code editor
                </button>
              )}
              {!expanded && url && (
                <div className="flex justify-end">
                  <a
                    href={url}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="inline-flex items-center gap-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                  >
                    <ExternalLink className="h-3 w-3" />
                    open in tab
                  </a>
                </div>
              )}
            </div>
          )}

          {/* Modal lives at the section level so an agent switch (brief loading)
              doesn't unmount it. The Dialog owns only the chrome (sizing, sr
              title, close button) — the surface inside is EditorWorkbench,
              driven by this section's session + pane state so behavior is
              identical to the pre-extraction modal. */}
          <Dialog open={expanded} onOpenChange={setExpanded}>
            <DialogContent
              showCloseButton={false}
              className="flex h-[92vh] w-[96vw] max-w-[96vw] flex-col gap-0 overflow-hidden p-0 sm:max-w-[96vw]"
            >
              <DialogTitle className="sr-only">Code editor</DialogTitle>
              <EditorWorkbench
                issueId={issueId}
                issueKey={issueKey}
                issueTitle={issueTitle}
                projectId={projectId}
                session={session}
                leftPane={leftPane}
                onLeftPaneChange={setLeftPane}
                actions={editorActions}
                headerEnd={
                  <button
                    type="button"
                    onClick={() => setExpanded(false)}
                    title="Close editor"
                    aria-label="Close editor"
                    className="shrink-0 rounded p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                  >
                    <X className="size-4" />
                  </button>
                }
              />
            </DialogContent>
          </Dialog>
        </div>
      )}
    </div>
  );
}
