"use client";

import { useState, useEffect } from "react";
import {
  ChevronRight,
  ExternalLink,
  Expand,
  Loader2,
  HelpCircle,
  Layers,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { EditorChatPanel } from "./editor-chat-panel";
import { LiveAgentChangesFeed } from "./live-agent-changes-feed";
import { AgentWorkingIndicator } from "./agent-working-indicator";
import { EditorReviewBar } from "./editor-review-bar";
import {
  EditorHowItWorks,
  useHowItWorksDismissed,
} from "./editor-how-it-works";
import {
  EditorPreviewPane,
  parseTestOutput,
  type TestRunState,
} from "./editor-preview-pane";
import { EditorBrowserPane } from "./editor-browser-pane";
import { EditorContextPanel } from "./editor-context-panel";
import { EditorVariantsDialog } from "./editor-variants-dialog";
import { EditorAskBar } from "./editor-ask-bar";
import { EditorChangesList } from "./editor-changes-list";
import { EditorRunQA } from "./editor-run-qa";
import { EditorDeployQA } from "./editor-deploy-qa";
import { EditorTestsPanel } from "./editor-tests-panel";
import { useWorkspaceId } from "@agora/core/hooks";

// Right-panel "Code" section: launches a browser VS Code (code-server) on the
// issue's agent worktree and iframes it, so a human can watch + edit the live
// code. Every agent that ran on the issue keeps its own worktree, so the chip
// row at the top lets the human switch between agents to review each one's work.
// Self-host flow: GET /api/issues/{id}/editor → {daemon_url, agents:[{work_dir}]}
// then POST {daemon_url}/editor/launch {workdir} → {url}. Lazy: nothing runs
// until the section is opened.

type LaunchState = "idle" | "loading" | "ready" | "none" | "error";

interface EditorAgent {
  agent_id: string;
  agent_name: string;
  work_dir: string;
  status: string;
}

// Distinct per-agent chip colors, picked by a stable hash of the agent id so
// each agent keeps the same color across renders. Full class strings (no
// interpolation) so Tailwind keeps them.
const AGENT_CHIP_COLORS = [
  {
    on: "border-blue-500/50 bg-blue-500/15 text-blue-700 dark:text-blue-300",
    off: "border-blue-500/30 text-blue-600/80 hover:bg-blue-500/10 dark:text-blue-400/80",
  },
  {
    on: "border-emerald-500/50 bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
    off: "border-emerald-500/30 text-emerald-600/80 hover:bg-emerald-500/10 dark:text-emerald-400/80",
  },
  {
    on: "border-violet-500/50 bg-violet-500/15 text-violet-700 dark:text-violet-300",
    off: "border-violet-500/30 text-violet-600/80 hover:bg-violet-500/10 dark:text-violet-400/80",
  },
  {
    on: "border-amber-500/50 bg-amber-500/15 text-amber-700 dark:text-amber-300",
    off: "border-amber-500/30 text-amber-600/80 hover:bg-amber-500/10 dark:text-amber-400/80",
  },
  {
    on: "border-rose-500/50 bg-rose-500/15 text-rose-700 dark:text-rose-300",
    off: "border-rose-500/30 text-rose-600/80 hover:bg-rose-500/10 dark:text-rose-400/80",
  },
  {
    on: "border-cyan-500/50 bg-cyan-500/15 text-cyan-700 dark:text-cyan-300",
    off: "border-cyan-500/30 text-cyan-600/80 hover:bg-cyan-500/10 dark:text-cyan-400/80",
  },
  {
    on: "border-fuchsia-500/50 bg-fuchsia-500/15 text-fuchsia-700 dark:text-fuchsia-300",
    off: "border-fuchsia-500/30 text-fuchsia-600/80 hover:bg-fuchsia-500/10 dark:text-fuchsia-400/80",
  },
  {
    on: "border-teal-500/50 bg-teal-500/15 text-teal-700 dark:text-teal-300",
    off: "border-teal-500/30 text-teal-600/80 hover:bg-teal-500/10 dark:text-teal-400/80",
  },
];

function agentChipColor(id: string) {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return AGENT_CHIP_COLORS[h % AGENT_CHIP_COLORS.length]!;
}

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
  const [state, setState] = useState<LaunchState>("idle");
  const [url, setUrl] = useState<string | null>(null);
  const [err, setErr] = useState("");
  // Expand into a near-fullscreen modal (real coding needs width). One iframe
  // at a time — the inline preview unmounts while the modal is open.
  const [expanded, setExpanded] = useState(false);
  // Per-agent review: every agent that ran on the issue, and which one's
  // worktree is currently shown. Empty in cloud mode (single proxied editor).
  const [agents, setAgents] = useState<EditorAgent[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [daemon, setDaemon] = useState<{ url: string; userId: string } | null>(
    null,
  );
  // Lifted test-run state — shared between EditorPreviewPane (button + bottom
  // bar) and EditorTestsPanel (right panel). Avoids duplicating daemon calls.
  const [testRunState, setTestRunState] = useState<TestRunState>({
    testState: "idle",
    testOut: "",
    testPassed: null,
    testCmd: "",
    parsedTests: { failed: [], failedCount: 0, passedCount: 0 },
  });
  const handleTestResult = (
    result: Omit<TestRunState, "parsedTests">,
  ) => {
    setTestRunState({
      ...result,
      parsedTests: parseTestOutput(result.testOut),
    });
  };
  const handleTestStart = () => {
    setRightTab("tests");
  };

  // Right panel of the modal: watch the agent's live file edits, or chat to
  // steer it. Full diffs live in code-server's native Source Control panel.
  const [rightTab, setRightTab] = useState<
    "activity" | "chat" | "context" | "tests"
  >("activity");
  // Left pane of the modal: the code editor, or a live preview of the running
  // app (the vibecoder's "see it work, not the diff").
  const [leftPane, setLeftPane] = useState<"code" | "preview" | "browser">(
    "code",
  );
  // First-time "how co-code works" explainer (auto-shows once, re-openable).
  const { dismissed: helpDismissed, dismiss: dismissHelp } =
    useHowItWorksDismissed();
  const [showHelp, setShowHelp] = useState(false);
  const [variantsOpen, setVariantsOpen] = useState(false);
  // Co-code: the modal is the real workspace (the inline panel is too narrow),
  // so auto-expand it once the editor is ready. The user can still close it for
  // this visit (autoExpandedOnce guards against re-popping on every render).
  const [autoExpandedOnce, setAutoExpandedOnce] = useState(false);
  const helpVisible = showHelp || !helpDismissed;
  const closeHelp = () => {
    dismissHelp();
    setShowHelp(false);
  };

  // Launch code-server on a specific worktree (self-host: browser → daemon).
  const launchWorkdir = async (
    workdir: string,
    daemonUrl: string,
    userId: string,
  ) => {
    setState("loading");
    setErr("");
    const lr = await fetch(`${daemonUrl}/editor/launch`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workdir, user_id: userId }),
    });
    if (!lr.ok) {
      throw new Error(
        `daemon launch failed (${lr.status}) — is the daemon running?`,
      );
    }
    const { url: launched } = (await lr.json()) as { url: string };
    setUrl(launched);
    setState("ready");
  };

  const launch = async () => {
    setState("loading");
    setErr("");
    try {
      // The backend resolves the workspace from ?workspace_slug — the app routes
      // under /{workspaceSlug}/… so it's the first path segment.
      const slug =
        typeof window !== "undefined"
          ? window.location.pathname.split("/").filter(Boolean)[0]
          : "";
      const r = await fetch(
        `/api/issues/${issueId}/editor${slug ? `?workspace_slug=${encodeURIComponent(slug)}` : ""}`,
        { credentials: "include" },
      );
      if (r.status === 404) {
        setState("none");
        return;
      }
      if (!r.ok) throw new Error(`editor lookup failed (${r.status})`);
      const data = (await r.json()) as {
        mode?: string;
        editor_url?: string;
        daemon_url?: string;
        user_id?: string;
        agents?: EditorAgent[];
      };

      // Cloud: backend already launched + reverse-proxies — iframe directly.
      if (data.mode === "cloud" && data.editor_url) {
        setUrl(data.editor_url);
        setState("ready");
        return;
      }

      // Self-host: launch the first (most-recently-active) agent's worktree; the
      // chip row lets the human switch to any other agent.
      const list = data.agents ?? [];
      if (list.length === 0 || !data.daemon_url || !data.user_id) {
        setState("none");
        return;
      }
      setAgents(list);
      setDaemon({ url: data.daemon_url, userId: data.user_id });
      setSelectedId(list[0]!.agent_id);
      await launchWorkdir(list[0]!.work_dir, data.daemon_url, data.user_id);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed to open editor");
      setState("error");
    }
  };

  const selectAgent = async (a: EditorAgent) => {
    if (a.agent_id === selectedId || !daemon) return;
    setSelectedId(a.agent_id);
    try {
      await launchWorkdir(a.work_dir, daemon.url, daemon.userId);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed to open editor");
      setState("error");
    }
  };

  const toggle = () => setOpen((o) => !o);

  // Open when the issue switches into in_editor mode, then launch once open+idle.
  useEffect(() => {
    if (defaultOpen) setOpen(true);
  }, [defaultOpen]);
  useEffect(() => {
    if (open && state === "idle") void launch();
    // launch reads the latest closure; only the open/state transition matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, state]);
  // Auto-expand into the full modal for co-code once the editor is ready.
  useEffect(() => {
    if (coCode && state === "ready" && url && !expanded && !autoExpandedOnce) {
      setExpanded(true);
      setAutoExpandedOnce(true);
    }
  }, [coCode, state, url, expanded, autoExpandedOnce]);

  // Agent review chips — which agents worked on this issue; click to load that
  // agent's worktree into the editor. Shown inline + in the modal header.
  const agentTabs =
    agents.length > 0 ? (
      <div className="flex flex-wrap items-center gap-1">
        <span className="mr-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
          Agents
        </span>
        {agents.map((a) => {
          const c = agentChipColor(a.agent_id);
          return (
            <button
              key={a.agent_id}
              type="button"
              onClick={() => void selectAgent(a)}
              title={`Review ${a.agent_name || "agent"}'s worktree`}
              className={cn(
                "flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs transition-colors",
                a.agent_id === selectedId ? c.on : c.off,
              )}
            >
              <ActorAvatar
                actorType="agent"
                actorId={a.agent_id}
                size={14}
                className="shrink-0"
              />
              {a.agent_name || "agent"}
            </button>
          );
        })}
      </div>
    ) : null;

  const selectedAgent =
    agents.find((a) => a.agent_id === selectedId) ?? null;

  // Trust bar: branch isolation + CI status + Accept (→PR) / Discard. Self-host
  // only (talks to the daemon directly), keyed to the agent currently shown.
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

  const toolbar = (
    <div className="flex justify-end gap-3">
      <button
        type="button"
        onClick={() => setExpanded(true)}
        className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
      >
        <Expand className="h-3 w-3" />
        expand
      </button>
      {url && (
        <a
          href={url}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          <ExternalLink className="h-3 w-3" />
          open in tab
        </a>
      )}
    </div>
  );

  // QA / variants / help controls. Rendered in BOTH the inline header and the
  // expanded modal header — otherwise the modal overlay hides the inline row
  // (the editor auto-expands for co-code, so these must live inside the modal).
  const editorActions = (
    <div className="flex items-center gap-3">
      <EditorRunQA issueId={issueId} agent={selectedAgent} />
      <EditorDeployQA issueId={issueId} wsId={wsId} projectId={projectId} />
      <button
        type="button"
        onClick={() => setVariantsOpen(true)}
        className="inline-flex items-center gap-1 text-[10px] text-muted-foreground transition-colors hover:text-foreground"
      >
        <Layers className="h-3 w-3" />
        Run variants
      </button>
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
          <EditorVariantsDialog
            issueId={issueId}
            open={variantsOpen}
            onOpenChange={setVariantsOpen}
          />

          {/* Agent chips persist across launches so switching never unmounts them. */}
          {agentTabs}

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
            <div className="space-y-1">
              {toolbar}
              {!expanded && reviewBar && (
                <div className="overflow-hidden rounded-lg border border-border">
                  {reviewBar}
                </div>
              )}
              {!expanded && (
                <iframe
                  src={url}
                  title="code editor"
                  className="h-[70vh] w-full rounded-lg border border-border bg-background"
                />
              )}
            </div>
          )}

          {/* Modal lives at the section level so an agent switch (brief loading)
              doesn't unmount it. */}
          <Dialog open={expanded} onOpenChange={setExpanded}>
            <DialogContent className="flex h-[92vh] w-[96vw] max-w-[96vw] flex-col gap-0 overflow-hidden p-0 sm:max-w-[96vw]">
              <DialogTitle className="sr-only">Code editor</DialogTitle>
              {(agents.length > 0 || selectedAgent) && (
                <div className="flex items-center justify-between gap-3 border-b border-border px-3 py-2">
                  <div className="min-w-0">{agentTabs}</div>
                  {editorActions}
                </div>
              )}
              <EditorAskBar
                issueId={issueId}
                agent={selectedAgent}
                onSent={() => setRightTab("activity")}
              />
              <div className="flex min-h-0 flex-1">
                <div className="flex min-w-0 flex-1 flex-col">
                  <div className="flex shrink-0 items-center gap-1 border-b border-border px-2 py-1 text-xs">
                    <button
                      type="button"
                      onClick={() => setLeftPane("code")}
                      className={cn(
                        "rounded px-2 py-0.5 font-medium transition-colors",
                        leftPane === "code"
                          ? "bg-accent text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Code
                    </button>
                    <button
                      type="button"
                      onClick={() => setLeftPane("preview")}
                      className={cn(
                        "rounded px-2 py-0.5 font-medium transition-colors",
                        leftPane === "preview"
                          ? "bg-accent text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Preview
                    </button>
                    <button
                      type="button"
                      onClick={() => setLeftPane("browser")}
                      className={cn(
                        "rounded px-2 py-0.5 font-medium transition-colors",
                        leftPane === "browser"
                          ? "bg-accent text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Browser
                    </button>
                  </div>
                  <div className="relative min-h-0 flex-1">
                    {/* Code-server stays mounted (hidden) so switching to Preview
                        and back never reloads VS Code. */}
                    <div
                      className={cn(
                        "absolute inset-0 flex",
                        leftPane === "code" ? "" : "hidden",
                      )}
                    >
                      {expanded && url ? (
                        <iframe
                          src={url}
                          title="code editor (expanded)"
                          className="min-w-0 flex-1 border-0 bg-background"
                        />
                      ) : (
                        <div className="flex flex-1 items-center justify-center text-xs text-muted-foreground">
                          <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                          launching VS Code…
                        </div>
                      )}
                    </div>
                    {leftPane === "preview" && daemon && selectedAgent && (
                      <div className="absolute inset-0 flex">
                        <EditorPreviewPane
                          daemonUrl={daemon.url}
                          workdir={selectedAgent.work_dir}
                          testRunState={testRunState}
                          onTestResult={handleTestResult}
                          onTestStart={handleTestStart}
                        />
                      </div>
                    )}
                    {leftPane === "browser" && daemon && selectedAgent && (
                      <div className="absolute inset-0 flex">
                        <EditorBrowserPane
                          daemonUrl={daemon.url}
                          workdir={selectedAgent.work_dir}
                        />
                      </div>
                    )}
                  </div>
                </div>
                <div className="flex h-full w-[360px] shrink-0 flex-col border-l border-border bg-background">
                  {reviewBar}
                  <div className="flex shrink-0 border-b border-border text-xs">
                    <button
                      type="button"
                      onClick={() => setRightTab("activity")}
                      className={cn(
                        "flex-1 px-3 py-2 font-medium transition-colors",
                        rightTab === "activity"
                          ? "border-b-2 border-primary text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Activity
                    </button>
                    <button
                      type="button"
                      onClick={() => setRightTab("chat")}
                      className={cn(
                        "flex-1 px-3 py-2 font-medium transition-colors",
                        rightTab === "chat"
                          ? "border-b-2 border-primary text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Chat
                    </button>
                    <button
                      type="button"
                      onClick={() => setRightTab("context")}
                      className={cn(
                        "flex-1 px-3 py-2 font-medium transition-colors",
                        rightTab === "context"
                          ? "border-b-2 border-primary text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Context
                    </button>
                    <button
                      type="button"
                      onClick={() => setRightTab("tests")}
                      className={cn(
                        "flex-1 px-3 py-2 font-medium transition-colors",
                        rightTab === "tests"
                          ? "border-b-2 border-primary text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      Tests
                    </button>
                  </div>
                  <div className="shrink-0 [&>div]:px-3 [&>div]:py-2">
                    <AgentWorkingIndicator issueId={issueId} allowStop />
                  </div>
                  <div className="min-h-0 flex-1 overflow-y-auto">
                    {rightTab === "activity" ? (
                      <div className="space-y-2 p-2">
                        {daemon && selectedAgent && (
                          <EditorChangesList
                            daemonUrl={daemon.url}
                            workdir={selectedAgent.work_dir}
                          />
                        )}
                        <LiveAgentChangesFeed issueId={issueId} />
                        <p className="mt-2 px-1 text-[11px] leading-snug text-muted-foreground">
                          Live file edits appear here while an agent runs. For
                          the full line-by-line diff, open Source Control (the
                          branch icon) in the editor on the left.
                        </p>
                      </div>
                    ) : rightTab === "chat" ? (
                      <EditorChatPanel
                        issueId={issueId}
                        agent={selectedAgent}
                      />
                    ) : rightTab === "tests" ? (
                      <EditorTestsPanel
                        issueId={issueId}
                        testRunState={testRunState}
                        onRunTests={() => setLeftPane("preview")}
                      />
                    ) : (
                      <EditorContextPanel issueId={issueId} />
                    )}
                  </div>
                </div>
              </div>
            </DialogContent>
          </Dialog>
        </div>
      )}
    </div>
  );
}
