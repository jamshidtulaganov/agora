/* eslint-disable i18next/no-literal-string -- co-code editor live agent surface; i18n follow-up */
"use client";

import {
  type CSSProperties,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Code2,
  FileText,
  Folder,
  MessageSquarePlus,
  Radio,
} from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { useCreateComment } from "@agora/core/issues/mutations";
import { useActorName } from "@agora/core/workspace/hooks";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { taskMessagesOptions } from "@agora/core/chat/queries";
import type { AgentTask } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { buildTimeline, redactSecrets } from "../../common/task-transcript";
import { useT } from "../../i18n";
import {
  deriveActivitySteps,
  deriveCurrentActivity,
  deriveFileDocs,
  deriveProgressHeadline,
  deriveTodos,
  FRAGMENT_SEPARATOR,
  type ActivityLine,
  type ActivityStep,
  type LiveFileDoc,
  type TodoItem,
} from "./live-agent-activity";
import { TodoList, useStepText } from "./stage-live-process";

// ─────────────────────────────────────────────────────────────────────────────
// LiveAgentCodeEditor — the "spectator editor" for a running agent. Renders the
// agent's work-in-progress as a real code pane: a file tree of everything the
// run touched, the active file with line numbers, the freshest edit highlighted
// (staggered "live coding" reveal), and the agent's avatar-pill cursor sitting
// at the end of what it just wrote. Same data pipeline as LiveAgentChangesFeed
// (task:message stream → ["task-messages", taskId] cache → deriveFileDocs) — no
// new backend surface; this is purely a richer lens on the stream.
//
// Follow mode: by default the pane follows the newest edit (auto-switching
// files like an over-the-shoulder view). Clicking a file in the tree pins it;
// a "follow" affordance appears when pinned away from the live file.
// ─────────────────────────────────────────────────────────────────────────────

// Hard cap on rendered lines per doc — a huge generated file must not stall the
// pane. The TAIL is kept (agents mostly append; the highlight is near the end),
// with a leading ellipsis row; line numbers stay true to the full doc.
const MAX_DOC_LINES = 400;
// Cap the staggered reveal like the diff feed: first N fresh lines animate, the
// rest land instantly, so a big patch never crawls.
const MAX_REVEAL_LINES = 20;
const REVEAL_STEP_MS = 45;

interface LiveAgentCodeEditorProps {
  issueId: string;
  /** Jump to the full code-server editor (the "Code" pane). */
  onOpenFullEditor?: () => void;
}

export function LiveAgentCodeEditor({
  issueId,
  onOpenFullEditor,
}: LiveAgentCodeEditorProps) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { getActorName } = useActorName();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const runningTasks = useMemo(
    () =>
      snapshot.filter(
        (task) => task.issue_id === issueId && task.status === "running",
      ),
    [snapshot, issueId],
  );

  // One editor at a time; chips switch between concurrent runs. Falls back to
  // the first running task whenever the picked one finishes.
  const [pickedTaskId, setPickedTaskId] = useState<string | null>(null);
  const task =
    runningTasks.find((tk) => tk.id === pickedTaskId) ?? runningTasks[0] ?? null;

  if (!task) {
    return (
      <div className="flex h-full w-full flex-col items-center justify-center gap-3 px-6 text-center">
        <span className="flex size-11 items-center justify-center rounded-full bg-muted">
          <Radio className="size-5 text-muted-foreground/60" />
        </span>
        <p className="text-xs font-medium text-foreground">
          {t(($) => $.live_editor.idle_title)}
        </p>
        <p className="max-w-[300px] text-[11px] leading-relaxed text-muted-foreground">
          {t(($) => $.live_editor.idle_hint)}
        </p>
      </div>
    );
  }

  return (
    <div className="flex h-full w-full min-w-0 flex-col">
      {runningTasks.length > 1 && (
        <div className="flex shrink-0 flex-wrap items-center gap-1 border-b border-border px-2 py-1.5">
          {runningTasks.map((tk) => (
            <button
              key={tk.id}
              type="button"
              onClick={() => setPickedTaskId(tk.id)}
              className={cn(
                "flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs transition-colors",
                tk.id === task.id
                  ? "border-primary/50 bg-primary/10 text-foreground"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              <ActorAvatar actorType="agent" actorId={tk.agent_id} size={14} />
              {getActorName("agent", tk.agent_id)}
            </button>
          ))}
        </div>
      )}
      {/* Keyed by task so switching runs remounts the pane (fresh follow/reveal
          state and its own task-messages subscription). */}
      <LiveEditorForTask
        key={task.id}
        issueId={issueId}
        task={task}
        onOpenFullEditor={onOpenFullEditor}
      />
    </div>
  );
}

// The actual editor pane for one running task.
function LiveEditorForTask({
  issueId,
  task,
  onOpenFullEditor,
}: {
  issueId: string;
  task: AgentTask;
  onOpenFullEditor?: () => void;
}) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const agentName = getActorName("agent", task.agent_id);

  // GitHub-PR-style line comments: a 💬 on each gutter line posts a normal
  // issue comment quoting file:line + the line itself, so the thread lives in
  // the issue Activity where both humans and agents already read.
  const createComment = useCreateComment(issueId);
  const submitLineComment = async (
    doc: LiveFileDoc,
    line: RenderLine,
    text: string,
  ): Promise<boolean> => {
    const quoted = line.text.trim().slice(0, 120);
    const loc = `**${doc.shortPath}${line.no != null ? `:${line.no}` : ""}**`;
    const content = `${loc}${quoted ? `\n> \`${quoted}\`` : ""}\n\n${text}`;
    try {
      await createComment.mutateAsync({ content });
      return true;
    } catch {
      return false;
    }
  };

  const { data: messages = [] } = useQuery(taskMessagesOptions(task.id));
  const timeline = useMemo(() => buildTimeline(messages), [messages]);
  const docs = useMemo(() => deriveFileDocs(timeline), [timeline]);
  const activity = useMemo(() => deriveCurrentActivity(timeline), [timeline]);
  // Step trail for runs that write no files (QA / review / ops): commands run,
  // files read, pages driven — so the Live pane streams SOMETHING meaningful
  // instead of sitting on "warming up" for the whole run. exec_command and
  // friends are unwrapped/humanized inside deriveActivitySteps (no raw shell).
  const steps = useMemo(() => deriveActivitySteps(timeline), [timeline]);
  // The agent's own plan — leads the waiting pane so the human sees what it's
  // doing and what's next, in the agent's words, not a tool-call trail.
  const todos = useMemo(() => deriveTodos(timeline), [timeline]);
  // The agent's own PROGRESS headline — the primary "what's happening now".
  const headline = useMemo(() => deriveProgressHeadline(timeline), [timeline]);

  // Follow-the-agent by default; clicking a file pins it.
  const [pinnedPath, setPinnedPath] = useState<string | null>(null);
  const liveDoc = docs[0] ?? null; // newest-changed first
  const activeDoc =
    (pinnedPath ? docs.find((d) => d.path === pinnedPath) : null) ?? liveDoc;
  const following = !pinnedPath || pinnedPath === liveDoc?.path;

  // Live-reveal bookkeeping (same primed/seen pattern as the changes feed): a
  // doc-change key unseen after the first paint is a fresh live edit → animate.
  const seenRef = useRef<Set<string> | null>(null);
  const primedRef = useRef(false);
  const liveKey = liveDoc ? `${liveDoc.path}#${liveDoc.lastIdx}` : null;
  const liveArrival =
    primedRef.current &&
    liveKey != null &&
    !(seenRef.current?.has(liveKey) ?? false);
  useEffect(() => {
    const seen = seenRef.current ?? new Set<string>();
    for (const d of docs) seen.add(`${d.path}#${d.lastIdx}`);
    seenRef.current = seen;
    primedRef.current = true;
  }, [docs]);

  // Keep the cursor in view while following.
  const cursorRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (following && liveKey) {
      // Optional call: jsdom (tests) has no scrollIntoView.
      cursorRef.current?.scrollIntoView?.({ block: "nearest" });
    }
  }, [following, liveKey]);

  return (
    <div className="flex min-h-0 min-w-0 flex-1">
      {/* File tree */}
      <div className="flex w-44 shrink-0 flex-col overflow-y-auto border-r border-border py-1.5">
        {docs.length === 0 ? (
          <p className="px-3 py-1 text-[11px] text-muted-foreground/60">
            {t(($) => $.live_editor.waiting)}
          </p>
        ) : (
          groupDocs(docs).map((group) => (
            <div key={group.dir} className="mb-1">
              <div className="flex items-center gap-1.5 px-2.5 py-1 text-[11px] text-muted-foreground">
                <Folder aria-hidden className="size-3 shrink-0" />
                <span className="truncate">{group.dir}</span>
              </div>
              {group.docs.map((d) => (
                <button
                  key={d.path}
                  type="button"
                  onClick={() => setPinnedPath(d.path)}
                  title={d.path}
                  className={cn(
                    "flex w-full items-center gap-1.5 py-1 pl-6 pr-2 text-left text-[11px] transition-colors",
                    d.path === activeDoc?.path
                      ? "bg-accent text-foreground"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  <FileText
                    aria-hidden
                    className="size-3 shrink-0 text-muted-foreground/60"
                  />
                  <span className="min-w-0 flex-1 truncate font-mono">
                    {fileName(d.path)}
                  </span>
                  {d.path === liveDoc?.path && (
                    <span
                      aria-hidden
                      className="size-1.5 shrink-0 rounded-full bg-info motion-safe:animate-pulse"
                    />
                  )}
                </button>
              ))}
            </div>
          ))
        )}
      </div>

      {/* Code pane */}
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-1.5">
          {activeDoc ? (
            <>
              <span
                className="min-w-0 truncate font-mono text-xs text-foreground/90"
                title={activeDoc.path}
              >
                {fileName(activeDoc.path)}
              </span>
              <AgentPill agentId={task.agent_id} name={agentName} />
            </>
          ) : (
            <span className="text-xs text-muted-foreground">
              {t(($) => $.live_editor.waiting)}
            </span>
          )}
          <span className="flex-1" />
          {!following && (
            <button
              type="button"
              onClick={() => setPinnedPath(null)}
              className="text-[10px] font-medium text-info transition-colors hover:underline"
            >
              {t(($) => $.live_editor.follow)}
            </button>
          )}
          <span className="flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wide text-info">
            <span
              aria-hidden
              className="size-1.5 rounded-full bg-info motion-safe:animate-pulse"
            />
            {t(($) => $.live_editor.live)}
          </span>
        </div>

        <div className="min-h-0 flex-1 overflow-auto" aria-live="polite">
          {activeDoc ? (
            <CodeDoc
              doc={activeDoc}
              // Animate only the active doc's fresh live edit.
              reveal={liveArrival && activeDoc.path === liveDoc?.path}
              agentId={task.agent_id}
              agentName={agentName}
              showCursor={activeDoc.path === liveDoc?.path}
              cursorRef={cursorRef}
              onSubmitComment={(line, text) =>
                submitLineComment(activeDoc, line, text)
              }
            />
          ) : (
            <WaitingPane
              task={task}
              activity={activity}
              steps={steps}
              todos={todos}
              headline={headline}
            />
          )}
        </div>

        {/* Status bar */}
        <div className="flex shrink-0 items-center gap-2 border-t border-border px-3 py-1.5 text-[11px] text-muted-foreground">
          <ActorAvatar actorType="agent" actorId={task.agent_id} size={14} />
          <span className="min-w-0 flex-1 truncate">
            <span className="font-medium text-foreground/80">{agentName}</span>{" "}
            {activity ? <ActivityText activity={activity} /> : null}
          </span>
          {docs.length > 0 && (
            <span className="shrink-0">
              {t(($) => $.live_activity.files, { count: docs.length })}
            </span>
          )}
          {onOpenFullEditor && (
            <button
              type="button"
              onClick={onOpenFullEditor}
              className="inline-flex shrink-0 items-center gap-1 transition-colors hover:text-foreground"
            >
              <Code2 className="size-3" />
              {t(($) => $.live_editor.open_editor)}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// Localized "is editing …/file.ts" fragment for the status bar. Same verb keys
// the activity strip uses, so the two stay in sync.
function ActivityText({ activity }: { activity: ActivityLine }) {
  const { t } = useT("issues");
  let verb = "";
  switch (activity.verbKey) {
    case "reading": verb = t(($) => $.live_activity.verb.reading); break;
    case "editing": verb = t(($) => $.live_activity.verb.editing); break;
    case "writing": verb = t(($) => $.live_activity.verb.writing); break;
    case "searching": verb = t(($) => $.live_activity.verb.searching); break;
    case "running": verb = t(($) => $.live_activity.verb.running); break;
    case "fetching": verb = t(($) => $.live_activity.verb.fetching); break;
    case "browsing": verb = t(($) => $.live_activity.verb.browsing); break;
    case "thinking": verb = t(($) => $.live_activity.verb.thinking); break;
    case "working": verb = t(($) => $.live_activity.verb.working); break;
    default: verb = activity.rawVerb ?? "";
  }
  return <>{activity.target ? `${verb} ${activity.target}` : verb}</>;
}

// Cap the step trail in the waiting pane — a long QA run can emit hundreds of
// commands; the newest slice is what tells the human "it's alive and here's
// what it's doing".
const MAX_WAITING_STEPS = 30;

// Pre-first-edit state: the run is alive but nothing was written yet. Runs
// that never write files (QA / review / ops) live here for their whole
// duration, so instead of a static "warming up" the pane renders the step
// trail as a vertical timeline — oldest at the top, the live newest step
// pulsing at the bottom, auto-followed — so the run reads as a sequence of
// human phrases ("installing dependencies", "running the tests"), not raw
// shell.
function WaitingPane({
  task,
  activity,
  steps,
  todos,
  headline,
}: {
  task: AgentTask;
  activity: ActivityLine | null;
  steps: ActivityStep[];
  todos: TodoItem[];
  headline: string | null;
}) {
  const { t } = useT("issues");
  // deriveActivitySteps returns newest-first; the timeline reads top→down in
  // execution order, so take the newest slice and flip it chronological.
  const trail = steps.slice(0, MAX_WAITING_STEPS).reverse();

  // Follow the live end of the timeline as new steps stream in.
  const endRef = useRef<HTMLLIElement | null>(null);
  const lastKey = trail[trail.length - 1]?.key;
  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [lastKey]);

  return (
    <div className="flex h-full flex-col items-center gap-3 overflow-y-auto px-6 py-8">
      <ActorAvatar actorType="agent" actorId={task.agent_id} size={28} />
      {/* The agent's own PROGRESS headline leads; fall back to the derived
          current activity, then a neutral "warming up". */}
      <p className={cn("text-xs", headline ? "font-medium text-foreground" : "text-muted-foreground")}>
        {headline ? (
          headline
        ) : trail.length === 0 && activity ? (
          <ActivityText activity={activity} />
        ) : (
          t(($) => $.live_editor.waiting)
        )}
      </p>
      {/* The agent's own to-do plan leads — what's done, now, and next. */}
      {todos.length > 0 && (
        <div className="w-full max-w-[560px]">
          <TodoList todos={todos} />
        </div>
      )}
      {/* Sequential timeline: connector rail + a dot per step. */}
      {trail.length > 0 && (
        <ul className="mt-1 w-full max-w-[560px] text-left">
          {trail.map((step, i) => {
            const isLast = i === trail.length - 1;
            return (
              <li
                key={step.key}
                ref={isLast ? endRef : undefined}
                className="relative pb-3 pl-6 last:pb-0"
              >
                {!isLast && (
                  <span
                    aria-hidden
                    className="absolute left-[5.5px] top-3.5 h-full w-px bg-border"
                  />
                )}
                <span
                  aria-hidden
                  className={cn(
                    "absolute left-0 top-[3px] size-3 rounded-full border-2 border-background",
                    isLast
                      ? "bg-info motion-safe:animate-pulse"
                      : "bg-muted-foreground/40",
                  )}
                />
                <span
                  className={cn(
                    "block truncate text-xs",
                    isLast
                      ? "font-medium text-foreground/90"
                      : "text-muted-foreground/80",
                  )}
                  title={step.target}
                >
                  <StepText step={step} />
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

// Localized step line — delegates to the shared useStepText so the Watch pane,
// the changes feed, and the stepper all read identically (and pick up the
// commit/publish/pr milestone phrasing without drifting a second switch here).
function StepText({ step }: { step: ActivityStep }) {
  return <>{useStepText(step)}</>;
}

// The blue avatar pill that plays the agent's "cursor" (mockup: "Aria ▍").
function AgentPill({
  agentId,
  name,
  cursor = false,
}: {
  agentId: string;
  name: string;
  cursor?: boolean;
}) {
  return (
    <span className="inline-flex shrink-0 items-center gap-1 rounded bg-primary px-1.5 py-0.5 text-[10px] font-medium leading-none text-primary-foreground">
      <ActorAvatar actorType="agent" actorId={agentId} size={12} />
      {name}
      {cursor && (
        <span
          aria-hidden
          className="hidden motion-safe:inline-block motion-safe:[animation:lec_1s_step-end_infinite]"
        >
          ▍
        </span>
      )}
    </span>
  );
}

interface RenderLine {
  /** 1-based line number; null for separator/ellipsis rows. */
  no: number | null;
  text: string;
  hl: boolean;
  sep: boolean;
}

// One reconstructed file rendered as an editor buffer: gutter numbers, fresh
// lines highlighted (optionally with the staggered reveal), the agent pill
// cursor after the last fresh line. Secrets redact per line, like the feed.
function CodeDoc({
  doc,
  reveal,
  agentId,
  agentName,
  showCursor,
  cursorRef,
  onSubmitComment,
}: {
  doc: LiveFileDoc;
  reveal: boolean;
  agentId: string;
  agentName: string;
  showCursor: boolean;
  cursorRef: React.RefObject<HTMLDivElement | null>;
  /** Posts a line comment; resolves true on success (form then closes). */
  onSubmitComment?: (line: RenderLine, text: string) => Promise<boolean>;
}) {
  const { t } = useT("issues");
  const lines = useMemo(() => buildRenderLines(doc), [doc]);

  // One inline comment form at a time, anchored under a line. Reset when the
  // active file changes — a draft written against another file's line would
  // quote the wrong code.
  const [commentAt, setCommentAt] = useState<number | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  useEffect(() => {
    setCommentAt(null);
    setDraft("");
  }, [doc.path]);

  const submit = () => {
    const line = commentAt != null ? lines[commentAt] : undefined;
    if (!line || !onSubmitComment || !draft.trim()) return;
    setSending(true);
    void onSubmitComment(line, draft.trim()).then((ok) => {
      setSending(false);
      if (ok) {
        setCommentAt(null);
        setDraft("");
      }
    });
  };

  // Cursor sits after the last highlighted row (or the buffer end).
  let cursorAfter = lines.length - 1;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i]!.hl) {
      cursorAfter = i;
      break;
    }
  }

  let revealSeen = 0;
  return (
    <pre className="min-w-full px-0 py-1.5 font-mono text-[11px] leading-relaxed">
      <style>{
        "@keyframes lcr{from{opacity:0;transform:translateY(1px)}to{opacity:1;transform:none}}" +
        "@keyframes lec{0%,49%{opacity:1}50%,100%{opacity:0}}"
      }</style>
      {lines.map((line, i) => {
        const reveals = reveal && line.hl && revealSeen < MAX_REVEAL_LINES;
        const delayMs = reveals ? revealSeen * REVEAL_STEP_MS : 0;
        if (reveals) revealSeen++;
        return (
          <div key={i}>
            <div
              className={cn(
                "group flex",
                line.hl && "bg-emerald-500/10 dark:bg-emerald-400/10",
                reveals &&
                  "motion-safe:[animation:lcr_.22s_ease-out_both] motion-safe:[animation-delay:var(--lcr-delay)]",
              )}
              style={
                reveals
                  ? ({ "--lcr-delay": `${delayMs}ms` } as CSSProperties)
                  : undefined
              }
            >
              <span className="relative w-10 shrink-0 select-none pr-3 text-right tabular-nums text-muted-foreground/40">
                <span
                  aria-hidden
                  className={cn(
                    line.no != null &&
                      onSubmitComment &&
                      "group-hover:opacity-0",
                  )}
                >
                  {line.no ?? ""}
                </span>
                {/* Gutter 💬 — swaps in for the number on row hover. */}
                {line.no != null && onSubmitComment && (
                  <button
                    type="button"
                    onClick={() =>
                      setCommentAt((cur) => (cur === i ? null : i))
                    }
                    title={t(($) => $.live_editor.comment_line)}
                    aria-label={t(($) => $.live_editor.comment_line)}
                    className="absolute inset-y-0 right-2 hidden items-center text-info hover:text-foreground group-hover:inline-flex"
                  >
                    <MessageSquarePlus className="size-3" />
                  </button>
                )}
              </span>
              <span
                className={cn(
                  "whitespace-pre",
                  line.sep && "text-muted-foreground/50",
                  line.hl && "text-emerald-700 dark:text-emerald-300",
                )}
              >
                {line.text}
              </span>
            </div>
            {/* Inline PR-style comment form, anchored under the line. */}
            {commentAt === i && (
              <div className="flex items-start gap-2 py-1.5 pl-10 pr-3">
                <textarea
                  autoFocus
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder={t(($) => $.live_editor.comment_placeholder)}
                  className="min-h-[52px] w-full max-w-[520px] flex-1 resize-y rounded-md border border-border bg-background px-2 py-1.5 font-sans text-xs text-foreground outline-none placeholder:text-muted-foreground/60 focus:ring-1 focus:ring-ring"
                />
                <div className="flex shrink-0 flex-col gap-1">
                  <button
                    type="button"
                    onClick={submit}
                    disabled={!draft.trim() || sending}
                    className="rounded-md bg-primary px-2 py-1 font-sans text-[11px] font-medium text-primary-foreground transition-opacity disabled:opacity-50"
                  >
                    {t(($) => $.live_editor.send)}
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setCommentAt(null);
                      setDraft("");
                    }}
                    className="rounded-md border border-border px-2 py-1 font-sans text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                  >
                    {t(($) => $.live_editor.cancel)}
                  </button>
                </div>
              </div>
            )}
            {showCursor && i === cursorAfter && (
              <div ref={cursorRef} className="flex py-0.5">
                <span aria-hidden className="w-10 shrink-0 pr-3" />
                <AgentPill agentId={agentId} name={agentName} cursor />
              </div>
            )}
          </div>
        );
      })}
    </pre>
  );
}

// Split the doc into gutter-numbered rows, mark highlight/separator rows, keep
// only the tail past MAX_DOC_LINES (numbers stay true), redact each line.
function buildRenderLines(doc: LiveFileDoc): RenderLine[] {
  const raw = doc.text.split("\n");
  const hl = new Set<number>();
  for (const r of doc.ranges) {
    for (let k = r.from; k < r.from + r.count; k++) hl.add(k);
  }

  const start = Math.max(0, raw.length - MAX_DOC_LINES);
  const out: RenderLine[] = [];
  if (start > 0) out.push({ no: null, text: "…", hl: false, sep: true });
  let no = 0;
  for (let i = 0; i < raw.length; i++) {
    const sep = raw[i] === FRAGMENT_SEPARATOR;
    if (!sep) no++;
    if (i < start) continue;
    out.push({
      no: sep ? null : no,
      text: sep ? FRAGMENT_SEPARATOR : redactSecrets(raw[i]!),
      hl: hl.has(i),
      sep,
    });
  }
  return out;
}

function fileName(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? p;
}

// Group docs by their immediate parent directory for the tree. Groups keep the
// newest-changed order (docs arrive newest-first).
function groupDocs(
  docs: LiveFileDoc[],
): Array<{ dir: string; docs: LiveFileDoc[] }> {
  const groups = new Map<string, LiveFileDoc[]>();
  for (const d of docs) {
    const parts = d.path.split("/").filter(Boolean);
    const dir = parts.length > 1 ? parts[parts.length - 2]! : "/";
    const list = groups.get(dir);
    if (list) list.push(d);
    else groups.set(dir, [d]);
  }
  return Array.from(groups.entries()).map(([dir, list]) => ({
    dir,
    docs: list,
  }));
}
