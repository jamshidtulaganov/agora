/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useState, useEffect, useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { issueKeys } from "@agora/core/issues/queries";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  Activity as ActivityIcon,
  Code2,
  Globe,
  Info,
  Loader2,
  MessageSquare,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Users,
} from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import type { AgentTask } from "@agora/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { TranscriptButton } from "../../common/task-transcript";
import { EditorChatPanel } from "./editor-chat-panel";
import { LiveAgentChangesFeed } from "./live-agent-changes-feed";
import { LiveAgentCodeEditor } from "./live-agent-code-editor";
import { AgentWorkingIndicator } from "./agent-working-indicator";
import { EditorReviewBar } from "./editor-review-bar";
import {
  EditorPreviewPane,
  parseTestOutput,
  type TestRunState,
} from "./editor-preview-pane";
import { EditorBrowserPane } from "./editor-browser-pane";
import { EditorContextPanel } from "./editor-context-panel";
import { EditorAskBar } from "./editor-ask-bar";
import { EditorChangesList } from "./editor-changes-list";
import { EditorRunQA } from "./editor-run-qa";

// The co-code editor WORKBENCH — the editor Dialog's inner surface, extracted
// from editor-section.tsx so it can mount in two hosts (docs/
// sdlc-stage-cockpit-plan.md, phase F):
//
//   1. The issue view's full-screen Dialog (editor-section.tsx) — the daily
//      driver. The section keeps the Dialog chrome (DialogContent sizing,
//      sr-only title, close button via `headerEnd`) and stays in control of
//      the session + left pane (controlled props), so that path behaves
//      exactly as before the extraction.
//   2. The cockpit's Dev lens (dev-lens.tsx) — the same surface as a frame
//      content region: no Dialog, container-relative sizing (the root is
//      `flex-1` inside whatever flex column the host provides).
//
// Layout contract: the host gives the workbench a bounded flex column; the
// workbench fills it (header row + ask bar shrink-0, main region flex-1
// min-h-0). Nothing here assumes a portal, a viewport unit, or Dialog focus
// behavior.

export type LaunchState = "idle" | "loading" | "ready" | "none" | "error";

export interface EditorAgent {
  agent_id: string;
  agent_name: string;
  work_dir: string;
  status: string;
  // Desktop-VS-Code deep link for this worktree (vscode://file/… local, or a
  // Remote-SSH link for a box). Optional — older backends omit it.
  vscode_url?: string;
}

export interface EditorDaemon {
  url: string;
  userId: string;
  env?: Record<string, string>;
}

export type EditorWorkbenchPane = "live" | "code" | "preview" | "browser";

type AgentLiveStatus = "running" | "queued" | "done" | "failed";

// Per-agent live status on the roster chip — the signal that turns the agent
// switcher into an actual "agents window" (which one is working, which is done,
// which failed). running pulses in the agent's own chip color (bg-current);
// the terminal/queued states use fixed semantic colors.
function AgentStatusDot({ status }: { status?: AgentLiveStatus }) {
  if (!status) return null;
  if (status === "running") {
    return (
      <span aria-hidden className="relative inline-flex size-2 shrink-0 items-center justify-center">
        <span className="absolute inline-flex size-2 rounded-full bg-current opacity-40 motion-safe:animate-ping" />
        <span className="relative inline-flex size-1.5 rounded-full bg-current" />
      </span>
    );
  }
  const cls =
    status === "failed"
      ? "bg-destructive"
      : status === "done"
        ? "bg-emerald-500"
        : "bg-amber-500"; // queued
  return <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", cls)} />;
}

// Plain per-session status line for the vertical roster.
function agentStatusLabel(status?: AgentLiveStatus): string {
  switch (status) {
    case "running":
      return "Working…";
    case "queued":
      return "Queued";
    case "done":
      return "Done";
    case "failed":
      return "Failed";
    default:
      return "Idle";
  }
}

// A self-host daemon_url is only reachable when the browser shares the daemon's
// host (127.0.0.1). On a hosted page the browser is remote, so POSTing to a
// loopback daemon URL just yields a CORS failure + a stuck spinner. These guards
// let the UI show an honest message instead. (Belt-and-suspenders: the cloud
// backend no longer returns a loopback self-host URL, but an older backend or a
// misconfigured self-host still can — the desktop app outlives any server build.)
function isLoopbackUrl(u: string): boolean {
  try {
    const h = new URL(u).hostname;
    return h === "127.0.0.1" || h === "localhost" || h === "[::1]" || h === "::1";
  } catch {
    return false;
  }
}
function browserIsRemote(): boolean {
  if (typeof window === "undefined") return false;
  const h = window.location.hostname;
  return !(h === "127.0.0.1" || h === "localhost" || h === "[::1]");
}

// Cloud mode only: probe the same-origin editor proxy URL before iframing it.
// The backend returns `editor_url` as soon as the daemon's /editor/launch hands
// back a port — but that port can be dead (code-server died on spawn, or the
// worktree was GC'd between launch and load), in which case the ReverseProxy
// answers 502 or the connection resets. Iframing that renders a raw browser
// net-error (NS_ERROR_NET_ERROR_RESPONSE) the user can't recover from. The proxy
// is served under our OWN origin (Next rewrite → backend ProxyEditor), so this
// fetch is same-origin and cookie-authed exactly like the iframe. Treat only a
// network rejection or a 5xx as unreachable; any 2xx/3xx/4xx (incl. an auth
// redirect) means code-server answered, so let the iframe take over.
export async function probeEditorReachable(u: string): Promise<boolean> {
  try {
    const res = await fetch(u, {
      method: "GET",
      credentials: "include",
      redirect: "manual",
    });
    // redirect:manual surfaces a 3xx as an opaque response (status 0) — that is
    // code-server bouncing to its own path, i.e. reachable.
    if (res.type === "opaqueredirect") return true;
    return res.status < 500;
  } catch {
    return false;
  }
}
const EDITOR_UNREACHABLE_LABEL =
  "This issue's live editor was cleaned up (worktrees are removed automatically about a day after the agent finishes) or runs on a machine this browser can't reach. Re-run an agent on this issue to open it here.";

// Browser-tab empty states (no live worktree / daemon). Plain TS literals so the
// jsx-text-only i18n rule lets them through, matching the editor files'
// raw-string convention (they don't use useT()).
const BROWSER_STARTING_LABEL = "starting browser…";
const BROWSER_UNAVAILABLE_LABEL =
  "Live browser unavailable — no live worktree yet. Assign an agent or wait for the daemon to come online, then reopen this tab.";
const PREVIEW_UNAVAILABLE_LABEL =
  "Preview runs the app's dev server next to the editor — available on self-host runtimes for now. On cloud runtimes, use the Browser tab to watch the live QA Chromium instead.";

export interface EditorSession {
  state: LaunchState;
  url: string | null;
  err: string;
  agents: EditorAgent[];
  selectedId: string | null;
  daemon: EditorDaemon | null;
  selectedAgent: EditorAgent | null;
  launch: () => Promise<void>;
  selectAgent: (agent: EditorAgent) => Promise<void>;
}

/**
 * The editor session: resolve the issue's worktrees (GET /api/issues/{id}/
 * editor) and open code-server on one — cloud mode iframes the backend's
 * reverse-proxied URL, self-host POSTs {daemon_url}/editor/launch. Lazy:
 * nothing runs until `active` is true (the section passes its open state, the
 * Dev lens passes true on mount). It never CREATES a worktree or session —
 * an issue no agent has run on resolves to state "none".
 *
 * Extracted from EditorSection so the section's collapsed sidebar UI and the
 * workbench (Dialog or Dev lens) share ONE session — the section passes its
 * instance down into the Dialog's workbench, so opening the modal never
 * re-launches.
 */
export function useEditorSession(issueId: string, active: boolean): EditorSession {
  const [state, setState] = useState<LaunchState>("idle");
  const [url, setUrl] = useState<string | null>(null);
  const [err, setErr] = useState("");
  // Per-agent review: every agent that ran on the issue, and which one's
  // worktree is currently shown. Empty in cloud mode (single proxied editor).
  const [agents, setAgents] = useState<EditorAgent[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [daemon, setDaemon] = useState<EditorDaemon | null>(null);

  // Launch code-server on a specific worktree (self-host: browser → daemon).
  const launchWorkdir = async (
    workdir: string,
    daemonUrl: string,
    userId: string,
    env?: Record<string, string>,
  ) => {
    setState("loading");
    setErr("");
    const lr = await fetch(`${daemonUrl}/editor/launch`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // env = the user's editor account tokens (Settings → editor
      // integration), forwarded verbatim; the daemon allowlists the keys.
      body: JSON.stringify({ workdir, user_id: userId, ...(env ? { env } : {}) }),
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
      if (!r.ok) {
        // Surface the backend's own message (e.g. a GC'd worktree → 410
        // worktree_gone) instead of a bare status code, so the user sees why +
        // what to do. Defensive parse: the body may be non-JSON on some errors.
        let msg = `editor lookup failed (${r.status})`;
        try {
          const body = (await r.json()) as { error?: string };
          if (typeof body?.error === "string" && body.error) msg = body.error;
        } catch {
          /* non-JSON body — keep the status-code message */
        }
        throw new Error(msg);
      }
      const data = (await r.json()) as {
        mode?: string;
        editor_url?: string;
        daemon_url?: string;
        user_id?: string;
        agents?: EditorAgent[];
        editor_env?: Record<string, string>;
      };

      // Cloud: backend already launched + reverse-proxies — iframe directly.
      // Still surface the agent roster so the chips + per-worktree "Open in
      // VS Code" links render (the browser editor shows the default worktree;
      // per-agent browser switching in cloud is the remaining follow-up).
      if (data.mode === "cloud" && data.editor_url) {
        const list = data.agents ?? [];
        setAgents(list);
        // Cloud carries a same-origin proxied base for the daemon pane surface
        // (preview / test / live browser). Wiring it into the same `daemon`
        // state the self-host path uses lights up the Preview and Browser tabs
        // in cloud with zero pane changes. selectedId must be set too — the
        // panes render on `daemon && selectedAgent`.
        if (
          typeof data.daemon_url === "string" &&
          data.daemon_url.startsWith("/") &&
          typeof data.user_id === "string" &&
          data.user_id
        ) {
          setDaemon({ url: data.daemon_url, userId: data.user_id, env: data.editor_env });
          setSelectedId(list[0]?.agent_id ?? null);
        }
        // Don't iframe a URL whose code-server is actually dead — probe first so
        // an unreachable proxy shows the actionable empty state (with a retry)
        // instead of a raw browser net-error. See probeEditorReachable.
        const reachable = await probeEditorReachable(data.editor_url);
        if (!reachable) {
          setErr(EDITOR_UNREACHABLE_LABEL);
          setState("error");
          return;
        }
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
      // A remote browser can't reach a loopback daemon URL — don't spew a CORS
      // failure + hang on the spinner; say so plainly.
      if (isLoopbackUrl(data.daemon_url) && browserIsRemote()) {
        setErr(EDITOR_UNREACHABLE_LABEL);
        setState("error");
        return;
      }
      setAgents(list);
      setDaemon({ url: data.daemon_url, userId: data.user_id, env: data.editor_env });
      setSelectedId(list[0]!.agent_id);
      await launchWorkdir(list[0]!.work_dir, data.daemon_url, data.user_id, data.editor_env);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed to open editor");
      setState("error");
    }
  };

  const selectAgent = async (a: EditorAgent) => {
    if (a.agent_id === selectedId || !daemon) return;
    setSelectedId(a.agent_id);
    try {
      await launchWorkdir(a.work_dir, daemon.url, daemon.userId, daemon.env);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed to open editor");
      setState("error");
    }
  };

  // Launch once the host activates the session (section open / lens mount).
  useEffect(() => {
    if (active && state === "idle") void launch();
    // launch reads the latest closure; only the active/state transition matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, state]);

  const selectedAgent = agents.find((a) => a.agent_id === selectedId) ?? null;

  return { state, url, err, agents, selectedId, daemon, selectedAgent, launch, selectAgent };
}

export interface EditorWorkbenchProps {
  issueId: string;
  /** Issue identifier (e.g. "SDM-107") — used for the Accept→PR title/Closes. */
  issueKey?: string;
  /** Issue title — used for the Accept→PR title. */
  issueTitle?: string;
  /** Issue's parent project — preview-pane command defaults + deploy action. */
  projectId?: string | null;
  /** The editor session driving this workbench (see useEditorSession). */
  session: EditorSession;
  /**
   * Controlled left pane. The Dialog host (editor-section.tsx) owns the pane
   * so auto-expand-to-Live and pane persistence across dialog close/reopen
   * behave exactly as before the extraction. Omit both (the Dev lens does)
   * and the workbench owns the pane itself, starting on Code.
   */
  leftPane?: EditorWorkbenchPane;
  onLeftPaneChange?: (pane: EditorWorkbenchPane) => void;
  /**
   * Custom header actions row. The Dialog host passes the section's
   * `editorActions` (QA / deploy / help) so the modal header is unchanged;
   * when omitted the workbench renders its own QA + deploy actions.
   */
  actions?: ReactNode;
  /** Trailing header slot — the Dialog host mounts its close button here. */
  headerEnd?: ReactNode;
}

export function EditorWorkbench({
  issueId,
  issueKey,
  issueTitle,
  projectId = null,
  session,
  leftPane: leftPaneProp,
  onLeftPaneChange,
  actions,
  headerEnd,
}: EditorWorkbenchProps) {
  const wsId = useWorkspaceId();
  const { state, url, agents, selectedId, daemon, selectedAgent, selectAgent } = session;

  // Project settings feed the preview pane's command defaults: qa_smoke_cmd
  // prefills the dev command, qa_test_cmd overrides /editor/test. Typed pane
  // input > project setting > daemon auto-detect.
  const { data: project } = useQuery({
    ...projectDetailOptions(wsId, projectId ?? ""),
    enabled: !!projectId,
  });
  const projectSettings = project?.settings;

  // Lifted test-run state — shared between EditorPreviewPane (button + bottom
  // bar); parseTestOutput summarizes the raw runner output for that bar.
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

  // Right panel: watch the agent's live file edits, or chat to steer it. Full
  // diffs live in code-server's native Source Control panel. (No Tests tab —
  // merge gates render in the review bar via EditorGates, and QA verdicts
  // live in the issue's QA evidence section.)
  const [rightTab, setRightTab] = useState<"activity" | "chat" | "context">(
    "activity",
  );
  // Collapse the right panel to a slim icon rail — the editor gets the full
  // width. Ephemeral UI state, deliberately not persisted.
  const [rightCollapsed, setRightCollapsed] = useState(false);

  // Left pane: the live spectator editor (watch the agent code), the real
  // code editor, or a live preview of the running app (the vibecoder's "see
  // it work, not the diff"). Controlled by the Dialog host, self-owned in the
  // Dev lens. Defaults to Watch — a vibe coder lands on "watch the agent /
  // nothing running yet", not the raw IDE (Code is a click away for devs).
  const [ownLeftPane, setOwnLeftPane] = useState<EditorWorkbenchPane>("live");
  const leftPane = leftPaneProp ?? ownLeftPane;
  const setLeftPane = onLeftPaneChange ?? setOwnLeftPane;

  // Is an agent currently running on this issue? Drives the Live tab's pulse
  // and the collapsed rail's activity dot.
  const { data: taskSnapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const hasLiveRun = useMemo(
    () =>
      taskSnapshot.some(
        (task) => task.issue_id === issueId && task.status === "running",
      ),
    [taskSnapshot, issueId],
  );

  // Per-agent live status for THIS issue, so each roster chip can show whether
  // that agent is working / queued / done / failed — the thing that makes the
  // switcher read like a Cursor/VS-Code agents window. A live state (running >
  // queued) always wins over a terminal one.
  const agentStatusById = useMemo(() => {
    const byAgent = new Map<string, AgentLiveStatus>();
    const rank: Record<AgentLiveStatus, number> = { running: 3, queued: 2, done: 1, failed: 1 };
    for (const task of taskSnapshot) {
      if (task.issue_id !== issueId) continue;
      let bucket: AgentLiveStatus;
      switch (task.status) {
        case "running":
          bucket = "running";
          break;
        case "queued":
        case "dispatched":
        case "waiting_local_directory":
          bucket = "queued";
          break;
        case "failed":
        case "cancelled":
          bucket = "failed";
          break;
        case "completed":
          bucket = "done";
          break;
        default:
          continue;
      }
      const prev = byAgent.get(task.agent_id);
      if (!prev || rank[bucket] > rank[prev]) byAgent.set(task.agent_id, bucket);
    }
    return byAgent;
  }, [taskSnapshot, issueId]);

  const workingCount = useMemo(
    () => [...agentStatusById.values()].filter((s) => s === "running").length,
    [agentStatusById],
  );

  // The full per-issue task history (same cache the execution log uses, so it
  // dedupes) — retains OLD terminal runs the live snapshot drops, so the roster
  // can open a finished agent's transcript however long ago it ran. Fetched
  // only once an agent is selected.
  const { data: issueTasks = [] } = useQuery({
    queryKey: issueKeys.tasks(issueId),
    queryFn: () => api.listTasksByIssue(issueId),
    enabled: !!selectedId,
  });

  // Each agent's most recent task on this issue — so every row in the vertical
  // session list can open its own transcript, not just the selected one.
  const latestTaskByAgent = useMemo(() => {
    const stamp = (t: AgentTask) =>
      new Date(t.started_at ?? t.dispatched_at ?? t.created_at).getTime();
    const byAgent = new Map<string, AgentTask>();
    for (const task of issueTasks) {
      const prev = byAgent.get(task.agent_id);
      if (!prev || stamp(task) > stamp(prev)) byAgent.set(task.agent_id, task);
    }
    return byAgent;
  }, [issueTasks]);

  // Compact header entry point — count + working pulse, click expands the rail
  // to the full session list. Replaces the old horizontal chip strip; the list
  // itself is vertical in the right rail (agentSessionList below).
  const agentSummary =
    agents.length > 0 ? (
      <button
        type="button"
        onClick={() => setRightCollapsed(false)}
        title="Agents"
        className="flex items-center gap-1.5 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        <Users className="size-3.5 shrink-0" />
        <span>Agents · {agents.length}</span>
        {workingCount > 0 && (
          <span className="inline-flex items-center gap-1 text-info">
            <span className="size-1.5 rounded-full bg-info motion-safe:animate-pulse" />
            {workingCount}
          </span>
        )}
      </button>
    ) : null;

  // The dev space's agents window — a VERTICAL session list, one row per agent
  // that has worked this issue: avatar + live status + plain status line, with
  // per-row Transcript + VS Code actions. Selecting a row loads that agent's
  // worktree into the editor (and focuses Steer/Stop on it). Sits at the top of
  // the right rail so switching sessions never leaves the editor. The row is a
  // container (not a button) so the transcript/vscode controls don't nest inside
  // an interactive element.
  const agentSessionList =
    agents.length > 0 ? (
      <div className="flex shrink-0 flex-col border-b border-border">
        <div className="flex items-center justify-between px-3 pb-1 pt-2">
          <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
            Agents · {agents.length}
          </span>
          {workingCount > 0 && (
            <span className="inline-flex items-center gap-1 text-[10px] text-info">
              <span className="size-1.5 rounded-full bg-info motion-safe:animate-pulse" />
              {workingCount} working
            </span>
          )}
        </div>
        <div className="max-h-40 overflow-y-auto pb-1">
          {agents.map((a) => {
            const status = agentStatusById.get(a.agent_id);
            const task = latestTaskByAgent.get(a.agent_id);
            const selected = a.agent_id === selectedId;
            const running = status === "running";
            return (
              <div
                key={a.agent_id}
                className={cn(
                  "flex items-center gap-2 border-l-2 pl-2 pr-2 transition-colors",
                  selected
                    ? "border-l-primary bg-accent"
                    : "border-l-transparent hover:bg-accent/50",
                )}
              >
                <button
                  type="button"
                  onClick={() => void selectAgent(a)}
                  title={`Load ${a.agent_name || "agent"}'s worktree`}
                  className="flex min-w-0 flex-1 items-center gap-2 py-1.5 text-left"
                >
                  <ActorAvatar
                    actorType="agent"
                    actorId={a.agent_id}
                    size={18}
                    className="shrink-0"
                  />
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate text-xs font-medium">
                      {a.agent_name || "agent"}
                    </span>
                    <span
                      className={cn(
                        "flex items-center gap-1 text-[10px]",
                        running ? "text-info" : "text-muted-foreground",
                      )}
                    >
                      <AgentStatusDot status={status} />
                      {agentStatusLabel(status)}
                    </span>
                  </span>
                </button>
                {task && (
                  <TranscriptButton
                    task={task}
                    agentName={a.agent_name || "agent"}
                    title={`View ${a.agent_name || "agent"}'s run transcript`}
                  />
                )}
                {a.vscode_url && (
                  <a
                    href={a.vscode_url}
                    title={`Open ${a.agent_name || "agent"}'s worktree in VS Code`}
                    aria-label="Open in VS Code"
                    className="shrink-0 rounded p-1 text-muted-foreground opacity-70 transition-colors hover:bg-accent/50 hover:text-foreground hover:opacity-100"
                  >
                    <Code2 className="size-3.5" />
                  </a>
                )}
              </div>
            );
          })}
        </div>
      </div>
    ) : null;

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

  // QA / deploy actions — the Dialog host passes the section's own row (with
  // its help affordance) so the modal header is byte-identical; the Dev lens
  // gets this default.
  const actionsNode = actions ?? (
    <div className="flex items-center gap-3">
      <EditorRunQA issueId={issueId} agent={selectedAgent} />
    </div>
  );

  return (
    <div className="flex min-h-0 w-full flex-1 flex-col overflow-hidden">
      {/* Single header row: agent chips (scrollable) · actions · headerEnd
          (the Dialog host's close button). Always rendered so the host stays
          controllable even before agents load. */}
      <div className="flex shrink-0 items-center gap-3 border-b border-border py-2 pl-3 pr-2">
        <div className="min-w-0 flex-1 overflow-x-auto">{agentSummary}</div>
        {actionsNode}
        {headerEnd}
      </div>
      <EditorAskBar
        issueId={issueId}
        agent={selectedAgent}
        onSent={() => setRightTab("activity")}
      />
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex shrink-0 items-center gap-1 border-b border-border px-2 py-1 text-xs">
            {/* App-first order for the vibe-coder default: Watch the agent →
                Preview the running app → Code (the IDE, for devs) → Browser. */}
            <button
              type="button"
              onClick={() => setLeftPane("live")}
              className={cn(
                "flex items-center gap-1.5 rounded px-2 py-0.5 font-medium transition-colors",
                leftPane === "live"
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              Watch
              {hasLiveRun && (
                <span
                  aria-hidden
                  className="size-1.5 rounded-full bg-info motion-safe:animate-pulse"
                />
              )}
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
            {leftPane === "live" && (
              <div className="absolute inset-0 flex">
                <LiveAgentCodeEditor
                  issueId={issueId}
                  onOpenFullEditor={() => setLeftPane("code")}
                />
              </div>
            )}
            {/* Code-server stays mounted (hidden) so switching to Preview
                and back never reloads VS Code. */}
            <div
              className={cn(
                "absolute inset-0 flex",
                leftPane === "code" ? "" : "hidden",
              )}
            >
              {url ? (
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
            {leftPane === "preview" &&
              (daemon && selectedAgent ? (
                <div className="absolute inset-0 flex">
                  <EditorPreviewPane
                    daemonUrl={daemon.url}
                    workdir={selectedAgent.work_dir}
                    defaultDevCommand={projectSettings?.qa_smoke_cmd}
                    defaultTestCommand={projectSettings?.qa_test_cmd}
                    testRunState={testRunState}
                    onTestResult={handleTestResult}
                  />
                </div>
              ) : (
                // Cloud mode: the preview pane drives the daemon's
                // /editor/preview API from the BROWSER, which only works
                // when the daemon is reachable directly (self-host).
                // Without this the tab rendered a silently blank pane.
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-6 text-center">
                  <span className="flex size-11 items-center justify-center rounded-full bg-muted">
                    <Play className="size-5 text-muted-foreground/60" />
                  </span>
                  <p className="max-w-[300px] text-[11px] leading-relaxed text-muted-foreground">
                    {PREVIEW_UNAVAILABLE_LABEL}
                  </p>
                </div>
              ))}
            {leftPane === "browser" &&
              (daemon && selectedAgent ? (
                <div className="absolute inset-0 flex">
                  <EditorBrowserPane
                    daemonUrl={daemon.url}
                    workdir={selectedAgent.work_dir}
                  />
                </div>
              ) : (
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-6 text-center">
                  {state === "loading" ? (
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <Loader2 className="h-4 w-4 animate-spin" />
                      {BROWSER_STARTING_LABEL}
                    </div>
                  ) : (
                    <>
                      <span className="flex size-11 items-center justify-center rounded-full bg-muted">
                        <Globe className="size-5 text-muted-foreground/60" />
                      </span>
                      <p className="max-w-[260px] text-[11px] leading-relaxed text-muted-foreground">
                        {BROWSER_UNAVAILABLE_LABEL}
                      </p>
                    </>
                  )}
                </div>
              ))}
          </div>
        </div>
        {rightCollapsed && (
          <div className="flex h-full w-10 shrink-0 flex-col items-center gap-1 border-l border-border bg-background py-2">
            <button
              type="button"
              onClick={() => setRightCollapsed(false)}
              title="Expand panel"
              aria-label="Expand panel"
              className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <PanelRightOpen className="size-4" />
            </button>
            <div className="my-1 h-px w-5 bg-border" />
            {(
              [
                ["activity", ActivityIcon, "Activity"],
                ["chat", MessageSquare, "Chat"],
                ["context", Info, "Context"],
              ] as const
            ).map(([key, Icon, label]) => (
              <button
                key={key}
                type="button"
                title={label}
                aria-label={label}
                onClick={() => {
                  setRightTab(key);
                  setRightCollapsed(false);
                }}
                className={cn(
                  "relative rounded p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  rightTab === key && "text-foreground",
                )}
              >
                <Icon className="size-4" />
                {/* Live pulse on the Activity icon so a collapsed rail
                    still signals a running agent. */}
                {key === "activity" && hasLiveRun && (
                  <span
                    aria-hidden
                    className="absolute right-0.5 top-0.5 size-1.5 rounded-full bg-info motion-safe:animate-pulse"
                  />
                )}
              </button>
            ))}
          </div>
        )}
        {!rightCollapsed && (
        <div className="flex h-full w-[360px] shrink-0 flex-col border-l border-border bg-background">
          {reviewBar}
          {agentSessionList}
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
              onClick={() => setRightCollapsed(true)}
              title="Collapse panel"
              aria-label="Collapse panel"
              className="shrink-0 px-2 text-muted-foreground transition-colors hover:text-foreground"
            >
              <PanelRightClose className="size-3.5" />
            </button>
          </div>
          <div className="shrink-0 [&>div]:px-3 [&>div]:py-2">
            <AgentWorkingIndicator
              issueId={issueId}
              allowStop
              focusAgentId={selectedId ?? undefined}
            />
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
            ) : (
              <EditorContextPanel issueId={issueId} />
            )}
          </div>
        </div>
        )}
      </div>
    </div>
  );
}
