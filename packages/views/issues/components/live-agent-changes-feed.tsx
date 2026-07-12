/* eslint-disable i18next/no-literal-string -- co-code editor live agent surface; i18n follow-up */
"use client";

import { type CSSProperties, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, FilePenLine, FilePlus2 } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
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
  deriveFileChanges,
  type ActivityStep,
  type FileChange,
} from "./live-agent-activity";

// ─────────────────────────────────────────────────────────────────────────────
// LiveAgentChangesFeed — a LIVE "git changes" view of an agent's run on this
// issue. While an agent is running, it surfaces ONLY the file MUTATIONS it makes
// — which files it wrote/edited and the code it added/removed (diff style). Tool
// noise (Read / Bash / Grep / Glob / WebFetch / …) is excluded entirely; this is
// the changeset, not a tool-call log. It complements — does not replace — the
// full ExecutionLogSection transcript in the right panel.
//
// DATA FLOW (no new daemon event needed — all of this already streams):
//   daemon emits per-tool-call messages → server persists + publishes
//   `task:message` (carries type:"tool_use", tool, input) → use-realtime-sync
//   writes each row into the ["task-messages", taskId] React-Query cache,
//   deduped by seq. `taskMessagesOptions(taskId)` reads that exact cache and
//   ALSO does a one-shot GET for catch-up on mount. So subscribing here gives
//   us the live write/edit stream with at most the daemon's ~500ms flush of lag
//   — the same pipeline the chat live view already renders. deriveFileChanges
//   then keeps only the write/edit calls.
//
// Renders nothing when no agent is running on the issue (no layout shift in the
// idle case). Dark-mode + reduced-motion safe (the header pulse and the
// "live coding" reveal are both `motion-safe` gated — see DiffBlock). One panel
// per running agent — each owns its own task-messages subscription via a child
// component so the React hook rules stay clean.
// ─────────────────────────────────────────────────────────────────────────────

// Cap the visible change rows so a long run doesn't grow the panel without
// bound; a "+K more" toggle (see AgentChangesPanel) reveals the overflow on
// demand. Newest changes are kept.
const MAX_ROWS = 8;
// Below this many changes, rows auto-expand to their diff so the first edits are
// readable at a glance without a click.
const AUTO_EXPAND_THRESHOLD = 2;
// Per-diff line cap — keeps each block compact; an ellipsis row marks the cut.
const MAX_DIFF_LINES = 14;
// Cap the "live coding" reveal: only the first N added lines stagger in; any
// added lines past this appear instantly so a large patch never animates for
// seconds. Removed lines never animate.
const MAX_REVEAL_LINES = 20;
// Per-line stagger of the reveal, in ms. Kept short so the whole block lands in
// well under a second even at the 20-line cap (20 × 45ms ≈ 0.9s).
const REVEAL_STEP_MS = 45;

interface LiveAgentChangesFeedProps {
  issueId: string;
  /**
   * When set, show ONLY the running task(s) whose `trigger_comment_id` matches
   * — the agent run a specific comment / @mention / slice-action kicked off —
   * so its live work renders right under that comment. When omitted, show the
   * running tasks that have NO triggering comment (assignment-started runs),
   * which surface at the top of the activity feed instead.
   */
  triggerCommentId?: string;
}

export function LiveAgentChangesFeed({
  issueId,
  triggerCommentId,
}: LiveAgentChangesFeedProps) {
  const wsId = useWorkspaceId();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  // Only RUNNING tasks carry a live write/edit stream worth surfacing. Queued /
  // dispatched / waiting tasks have streamed nothing yet — the existing
  // AgentWorkingIndicator already speaks for "queued", so an empty changes panel
  // here would be noise. Terminal tasks belong in the execution log. Scoped to
  // the triggering comment (rendered under it) or, when no comment id is given,
  // the comment-less runs (assignment-started) shown at the top of the feed.
  const runningTasks = useMemo(
    () =>
      snapshot.filter(
        (task) =>
          task.issue_id === issueId &&
          task.status === "running" &&
          (triggerCommentId
            ? task.trigger_comment_id === triggerCommentId
            : !task.trigger_comment_id),
      ),
    [snapshot, issueId, triggerCommentId],
  );

  if (runningTasks.length === 0) return null;

  return (
    <div className="mt-4 flex flex-col gap-2">
      {runningTasks.map((task) => (
        <AgentChangesPanel key={task.id} task={task} />
      ))}
    </div>
  );
}

// One panel = one running task. Subscribes to that task's live message cache and
// renders the file mutations it has made so far, newest first.
function AgentChangesPanel({ task }: { task: AgentTask }) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();

  // Same cache the chat live view uses: GET for catch-up on mount + live
  // `task:message` writes via use-realtime-sync. `enabled` is gated on a valid
  // UUID inside the options, so a malformed id is a no-op rather than a 4xx.
  const { data: messages = [] } = useQuery(taskMessagesOptions(task.id));

  // buildTimeline coalesces split text fragments and redacts secrets;
  // deriveFileChanges keeps only the write/edit calls (the git-style feed),
  // deriveActivitySteps the readable step trail shown when nothing was written
  // (reviews/research/ops) so the panel is never just a bare "working…".
  const timeline = useMemo(() => buildTimeline(messages), [messages]);
  const changes = useMemo(() => deriveFileChanges(timeline), [timeline]);
  const steps = useMemo(() => deriveActivitySteps(timeline), [timeline]);

  // Change A — "+K more" is a real toggle. Default collapsed (capped at
  // MAX_ROWS); the user can expand to see every changed file for this task and
  // collapse back. Per-task state lives here, so each running task's feed
  // manages its own expand/collapse independently.
  const [showAll, setShowAll] = useState(false);

  // Change B — detect which changes are NEW since the last render so a running
  // agent's freshest edit auto-reveals (the "live coding" feel) without every
  // existing row re-animating on every poll. `deriveFileChanges` keys each row
  // by its timeline index ("idx-N") and returns newest-first, so the newest
  // change is changes[0] and its key is stable across re-renders. We remember
  // every key we've already shown in a ref; a key absent from that ref is a
  // brand-new change this render. The ref is updated in an effect AFTER paint,
  // so the first render that includes a new key still sees it as "new".
  const seenKeysRef = useRef<Set<string> | null>(null);
  // First mount of an already-populated feed (catch-up GET) is NOT "live" — we
  // don't want a burst of reveals replaying history. Only changes that appear
  // AFTER the first render of this panel count as live arrivals.
  const primedRef = useRef(false);
  const newest = changes[0];
  const newestIsLive =
    primedRef.current &&
    task.status === "running" &&
    newest != null &&
    !(seenKeysRef.current?.has(newest.key) ?? false);

  useEffect(() => {
    // Record everything currently rendered as "seen" so the next poll/render
    // treats only genuinely newer items as live. Runs after paint, so the
    // render that introduced a new key already animated it.
    const seen = seenKeysRef.current ?? new Set<string>();
    for (const c of changes) seen.add(c.key);
    seenKeysRef.current = seen;
    primedRef.current = true;
  }, [changes]);

  const agentName = getActorName("agent", task.agent_id);
  const visible = showAll ? changes : changes.slice(0, MAX_ROWS);
  const hiddenCount = changes.length - Math.min(changes.length, MAX_ROWS);
  // Rows expand their diff by default only on a short changeset, so a long run
  // stays compact (collapsed) and a fresh one is immediately readable.
  const expandByDefault = changes.length <= AUTO_EXPAND_THRESHOLD;

  return (
    <div
      className="rounded-md border bg-muted/30 text-xs"
      aria-live="polite"
    >
      {/* Header: agent + change count, or a neutral "working…" while the agent
          reads/thinks before its first edit so the panel is never blank. */}
      <div className="flex items-center gap-2 px-2.5 py-1.5">
        <ActorAvatar actorType="agent" actorId={task.agent_id} size={16} />
        <span
          aria-hidden
          className="size-1.5 shrink-0 rounded-full bg-info motion-safe:animate-pulse"
        />
        <span className="min-w-0 flex-1 truncate text-muted-foreground">
          <span className="font-medium text-foreground/80">{agentName}</span>
          <span className="mx-1 text-muted-foreground/50">·</span>
          {changes.length === 0 ? (
            <span className="text-info">{t(($) => $.live_activity.working)}</span>
          ) : (
            <span>{t(($) => $.live_activity.files, { count: changes.length })}</span>
          )}
        </span>
      </div>

      {visible.length > 0 && (
        <ul className="flex flex-col border-t">
          {visible.map((change) => (
            <ChangeRow
              key={change.key}
              change={change}
              defaultExpanded={expandByDefault}
              // The newest row, when it's a fresh live arrival on a running
              // task, opens itself so its added lines reveal — even on a long
              // (collapsed-by-default) changeset.
              autoLive={newestIsLive && change.key === newest?.key}
            />
          ))}
        </ul>
      )}

      {/* Fallback for runs that wrote no files (reviews / research / ops): a
          readable step trail so the panel shows real progress instead of a bare
          "working…". Capped at MAX_ROWS, newest first. Shown only when there are
          zero file changes — the git-style feed above stays the view for coding. */}
      {changes.length === 0 && steps.length > 0 && (
        <ul className="flex flex-col border-t">
          {steps.slice(0, MAX_ROWS).map((step) => (
            <StepRow key={step.key} step={step} />
          ))}
        </ul>
      )}

      {/* Change A — toggle the capped/expanded view. Keyboard-accessible
          button (it IS a <button>, so Enter/Space activate it); `aria-expanded`
          reflects state for assistive tech. */}
      {hiddenCount > 0 && (
        <button
          type="button"
          onClick={() => setShowAll((v) => !v)}
          aria-expanded={showAll}
          className="flex w-full items-center gap-1 border-t px-2.5 py-1 text-left text-[11px] text-muted-foreground/70 transition-colors hover:bg-muted/50 hover:text-muted-foreground"
        >
          <ChevronRight
            aria-hidden
            className={cn(
              "size-3 shrink-0 transition-transform motion-reduce:transition-none",
              showAll && "rotate-90",
            )}
          />
          <span>
            {showAll
              ? t(($) => $.live_activity.show_less)
              : t(($) => $.live_activity.more, { count: hiddenCount })}
          </span>
        </button>
      )}
    </div>
  );
}

// One row = one readable activity step (a non-mutating tool call) for a run that
// touched no files. The verb is localized via the existing `live_activity.verb.*`
// keys; the target (file / command summary / query) renders verbatim. Purely
// presentational — no expand, no diff, no fetch.
function StepRow({ step }: { step: ActivityStep }) {
  const { t } = useT("issues");
  let verb = "";
  switch (step.verbKey) {
    case "reading": verb = t(($) => $.live_activity.verb.reading); break;
    case "editing": verb = t(($) => $.live_activity.verb.editing); break;
    case "writing": verb = t(($) => $.live_activity.verb.writing); break;
    case "searching": verb = t(($) => $.live_activity.verb.searching); break;
    case "running": verb = t(($) => $.live_activity.verb.running); break;
    case "fetching": verb = t(($) => $.live_activity.verb.fetching); break;
    case "browsing": verb = t(($) => $.live_activity.verb.browsing); break;
    case "thinking": verb = t(($) => $.live_activity.verb.thinking); break;
    case "working": verb = t(($) => $.live_activity.verb.working); break;
    default: verb = step.rawVerb ?? "";
  }
  // A classified command reads as its human intent; raw summary stays on hover.
  let human = "";
  switch (step.cmdClass) {
    case "install": human = t(($) => $.live_activity.cmd.install); break;
    case "test": human = t(($) => $.live_activity.cmd.test); break;
    case "lint": human = t(($) => $.live_activity.cmd.lint); break;
    case "build": human = t(($) => $.live_activity.cmd.build); break;
    case "review": human = t(($) => $.live_activity.cmd.review); break;
    case "branch": human = t(($) => $.live_activity.cmd.branch); break;
    case "inspect": human = t(($) => $.live_activity.cmd.inspect); break;
    default: break;
  }
  const text = human || (step.target ? `${verb} ${step.target}` : verb);
  return (
    <li className="flex items-center gap-2 border-b px-2.5 py-1.5 last:border-b-0">
      <span
        aria-hidden
        className="size-1 shrink-0 rounded-full bg-muted-foreground/40"
      />
      <span
        className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground"
        title={text}
      >
        {text}
      </span>
    </li>
  );
}

// One row = one file mutation. Click toggles the unified diff; on a short
// changeset it starts open. Read-only changesets never trigger fetches, so the
// row is purely presentational.
function ChangeRow({
  change,
  defaultExpanded,
  autoLive,
}: {
  change: FileChange;
  defaultExpanded: boolean;
  // True for the freshest live change on a running task — the row opens itself
  // so its added lines reveal, giving the "watching it type" feel.
  autoLive: boolean;
}) {
  const Icon = change.kind === "write" ? FilePlus2 : FilePenLine;
  const hasDiff = change.added.length > 0 || change.removed.length > 0;
  const [open, setOpen] = useState(defaultExpanded);

  // A fresh live arrival auto-opens once. We bump local state (rather than
  // deriving from the prop every render) so the user can still collapse it
  // afterwards and it won't re-open on the next poll.
  const liveHandledRef = useRef(false);
  useEffect(() => {
    if (autoLive && hasDiff && !liveHandledRef.current) {
      liveHandledRef.current = true;
      setOpen(true);
    }
  }, [autoLive, hasDiff]);

  // Change B — drive a fresh one-shot reveal each time the diff transitions to
  // open. The reveal "nonce" is part of the DiffBlock key, so every open mounts
  // a fresh block whose CSS animations play from the start exactly once.
  const [revealNonce, setRevealNonce] = useState(0);
  const wasOpen = useRef(false);
  useEffect(() => {
    if (open && !wasOpen.current) setRevealNonce((n) => n + 1);
    wasOpen.current = open;
  }, [open]);

  return (
    <li className="border-b last:border-b-0">
      <button
        type="button"
        onClick={() => hasDiff && setOpen((v) => !v)}
        className={cn(
          "flex w-full items-center gap-2 px-2.5 py-1.5 text-left transition-colors",
          hasDiff ? "cursor-pointer hover:bg-muted/50" : "cursor-default",
        )}
        disabled={!hasDiff}
        aria-expanded={hasDiff ? open : undefined}
      >
        <ChevronRight
          aria-hidden
          className={cn(
            "size-3 shrink-0 text-muted-foreground/40 transition-transform",
            hasDiff ? "visible" : "invisible",
            open && "rotate-90",
          )}
        />
        <Icon aria-hidden className="size-3.5 shrink-0 text-muted-foreground/70" />
        <span
          className="min-w-0 flex-1 truncate font-mono text-[11px] text-foreground/80"
          title={change.path}
        >
          {change.shortPath}
        </span>
        <DiffBadge additions={change.additions} deletions={change.deletions} />
      </button>

      {open && hasDiff && (
        <DiffBlock
          key={revealNonce}
          added={change.added}
          removed={change.removed}
        />
      )}
    </li>
  );
}

// "+12 −3" — additions in green, deletions in red, using the same tokens the
// rest of the app uses (emerald for success/added, destructive for removed). A
// zero side is dimmed rather than hidden so the badge width stays steady.
function DiffBadge({
  additions,
  deletions,
}: {
  additions: number;
  deletions: number;
}) {
  return (
    <span className="shrink-0 font-mono text-[10.5px] tabular-nums">
      <span className={additions > 0 ? "text-emerald-500" : "text-muted-foreground/40"}>
        +{additions}
      </span>{" "}
      <span className={deletions > 0 ? "text-destructive" : "text-muted-foreground/40"}>
        −{deletions}
      </span>
    </span>
  );
}

// Compact unified diff: removed lines (red, "−" prefix) then added lines
// (green, "+" prefix), capped at MAX_DIFF_LINES with an ellipsis. Secrets are
// redacted line-by-line so a key pasted into new code never renders here.
//
// Change B — "live coding" reveal. Added (green "+") lines stagger in one by
// one with a per-line `animation-delay`, and the last added line carries a
// blinking cursor while the reveal runs, so the block reads as if it's being
// typed. This is a ONE-SHOT per mount: the parent re-mounts DiffBlock (via a
// reveal-nonce key) on each expand, so the animations replay only on a fresh
// open — they don't loop. Removed lines render instantly (no animation); only
// the first MAX_REVEAL_LINES added lines animate (the rest appear instantly) so
// a large patch never crawls. PERFORMANCE: pure CSS — animation-delay stagger
// plus one cursor element, no per-character JS typewriter. REDUCED MOTION: the
// whole effect is `motion-safe` gated, so `prefers-reduced-motion: reduce`
// renders the full diff immediately with no stagger and no cursor.
function DiffBlock({ added, removed }: { added: string; removed: string }) {
  const lines = useMemo(() => buildDiffLines(removed, added), [removed, added]);

  // Index (within `lines`) of the last added line that participates in the
  // reveal — it gets the blinking cursor. -1 when there are no added lines.
  let revealOrdinal = 0;
  let lastRevealIndex = -1;
  for (let i = 0; i < lines.length; i++) {
    if (lines[i]!.sign === "+" && revealOrdinal < MAX_REVEAL_LINES) {
      lastRevealIndex = i;
      revealOrdinal++;
    }
  }

  // Reset the per-line counter for the actual render pass.
  let addedSeen = 0;

  return (
    <pre className="overflow-x-auto bg-muted/40 px-2.5 py-1.5 font-mono text-[10.5px] leading-relaxed">
      {/* Scoped keyframes (same inline-<style> pattern priority-icon.tsx uses
          for its pulse). `lcr` = live-coding-reveal; `lcc` = live-coding-cursor.
          Both are defined once per block; the gating to motion-safe happens via
          the Tailwind variants on the elements below. */}
      <style>{
        "@keyframes lcr{from{opacity:0;transform:translateY(1px)}to{opacity:1;transform:none}}" +
        "@keyframes lcc{0%,49%{opacity:1}50%,100%{opacity:0}}"
      }</style>
      {lines.map((line, i) => {
        if (line.sign === "…") {
          return (
            <div key={i} className="whitespace-pre text-muted-foreground/50">
              …
            </div>
          );
        }

        const isAdded = line.sign === "+";
        // Only the first MAX_REVEAL_LINES added lines reveal; give each a
        // staggered delay. Removed lines and overflow added lines render at
        // full opacity immediately.
        const reveals = isAdded && addedSeen < MAX_REVEAL_LINES;
        const delayMs = reveals ? addedSeen * REVEAL_STEP_MS : 0;
        if (isAdded) addedSeen++;
        const showCursor = i === lastRevealIndex;

        return (
          <div
            key={i}
            className={cn(
              "whitespace-pre",
              isAdded && "text-emerald-600 dark:text-emerald-400",
              line.sign === "-" && "text-destructive",
              // motion-safe: start hidden and fade/slide in via `lcr`. The
              // `forwards` fill holds the end state (visible) after one pass —
              // one-shot, never loops. motion-reduce: nothing here, so the line
              // stays at its natural full opacity (instant).
              reveals &&
                "motion-safe:[animation:lcr_.22s_ease-out_both] motion-safe:[animation-delay:var(--lcr-delay)]",
            )}
            style={
              reveals
                ? ({ "--lcr-delay": `${delayMs}ms` } as CSSProperties)
                : undefined
            }
          >
            {`${line.sign} ${line.text}`}
            {showCursor && (
              // Blinking cursor on the last revealed line. Appears only after
              // that line's reveal delay, then blinks a few cycles. motion-safe
              // gated and aria-hidden so reduced-motion / SR users never see it.
              <span
                aria-hidden
                className="ml-px hidden motion-safe:inline-block motion-safe:[animation:lcc_.5s_step-end_var(--lcc-delay)_3] text-emerald-500/80"
                style={
                  { "--lcc-delay": `${delayMs}ms` } as CSSProperties
                }
              >
                ▍
              </span>
            )}
          </div>
        );
      })}
    </pre>
  );
}

interface DiffLine {
  sign: "+" | "-" | "…";
  text: string;
}

// Split removed/added blocks into prefixed lines, redact each, and cap the
// total. The cap counts real content lines; the ellipsis marker is extra.
function buildDiffLines(removed: string, added: string): DiffLine[] {
  const out: DiffLine[] = [];
  const push = (block: string, sign: "+" | "-") => {
    if (!block) return;
    for (const raw of block.replace(/\n$/, "").split("\n")) {
      out.push({ sign, text: redactSecrets(raw) });
    }
  };
  push(removed, "-");
  push(added, "+");
  if (out.length > MAX_DIFF_LINES) {
    const kept = out.slice(0, MAX_DIFF_LINES);
    kept.push({ sign: "…", text: "" });
    return kept;
  }
  return out;
}
