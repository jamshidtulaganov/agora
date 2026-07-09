"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import { useAuthStore } from "@agora/core/auth";
import { useActorName } from "@agora/core/workspace/hooks";
import type { TimelineEntry } from "@agora/core/types";
import { Tabs, TabsList, TabsTrigger } from "@agora/ui/components/ui/tabs";
import { useT, useTimeAgo } from "../../i18n";
import { ActorAvatar } from "../../common/actor-avatar";
import { useIssueTimeline } from "../../issues/hooks/use-issue-timeline";
import {
  classifyEntryRoot,
  activityTabCounts,
  type ActivityTab,
} from "../../issues/components/activity-tabs";
import { formatActivity } from "../../issues/components/activity-format";
import { CommentCard } from "../../issues/components/comment-card";
import { ResolvedThreadBar } from "../../issues/components/resolved-thread-bar";
import { CommentInput } from "../../issues/components/comment-input";

// The QA lens's discussion panel (stage-cockpit phase G). QA engineers live in
// the issue conversation — repro notes, agent replies, Bitrix-synced comments —
// and @mentioning an agent in a comment is how a re-check gets dispatched. This
// panel brings that conversation INTO the QA lens instead of forcing a hop back
// to the issue lens.
//
// Deliberately built from issue-detail's PRIMITIVES, not a fork of its markup:
//   - useIssueTimeline → the SAME issueKeys.timeline(issueId) cache entry as
//     the issue lens (zero duplicate fetching) with the same WS handlers, so
//     the feed is live without polling.
//   - CommentCard / ResolvedThreadBar → the same thread rendering, including
//     reply/edit/delete/reactions/resolve.
//   - activity-tabs helpers → the same All/Agents/Bitrix/People split.
//   - CommentInput → the same composer, including mention:// support and the
//     trigger-preview chips (the "@qa-agent re-check this" workflow).
// What issue-detail's block owns and this panel intentionally does NOT reuse:
// Virtuoso virtualization, deep-link highlight scrolling, last-seen tinting,
// subscriber management — main-column concerns that don't fit a side panel.

// One renderable feed block: a comment thread (root + flat replies, the
// CommentCard contract) or a run of consecutive non-comment activity entries.
type PanelGroup =
  | { kind: "thread"; root: TimelineEntry; replies: TimelineEntry[] }
  | { kind: "events"; id: string; entries: TimelineEntry[] };

// Ascending walk over the (already filtered) timeline. Replies attach to their
// thread root via the parent chain — same root-resolution walk as
// classifyEntryRoot, so a filtered tab keeps whole threads together and a
// reply can never orphan (its root classifies identically and thus survives
// the same filter).
function groupPanelTimeline(entries: TimelineEntry[]): PanelGroup[] {
  const byId = new Map(entries.map((e) => [e.id, e]));
  const groups: PanelGroup[] = [];
  const threadByRootId = new Map<string, Extract<PanelGroup, { kind: "thread" }>>();
  for (const e of entries) {
    if (e.type === "comment") {
      let root: TimelineEntry = e;
      const seen = new Set<string>();
      while (root.parent_id && byId.get(root.parent_id) && !seen.has(root.id)) {
        seen.add(root.id);
        root = byId.get(root.parent_id)!;
      }
      const existing = threadByRootId.get(root.id);
      if (existing && root.id !== e.id) {
        existing.replies.push(e);
      } else if (!existing) {
        // Promote this entry to a thread root — either it IS the root, or its
        // root hasn't been seen (out-of-order / broken parent chain), in which
        // case rendering it standalone beats dropping it. Keyed under e.id so
        // a late-arriving real root still creates its own group instead of
        // colliding with the orphan's.
        const group = {
          kind: "thread" as const,
          root: e,
          replies: [] as TimelineEntry[],
        };
        threadByRootId.set(e.id, group);
        groups.push(group);
      }
    } else {
      const last = groups[groups.length - 1];
      if (last && last.kind === "events") last.entries.push(e);
      else groups.push({ kind: "events", id: e.id, entries: [e] });
    }
  }
  return groups;
}

export function QAActivityPanel({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const user = useAuthStore((s) => s.user);

  const {
    timeline,
    loading,
    submitComment,
    submitReply,
    editComment,
    deleteComment,
    toggleResolveComment,
    toggleReaction,
  } = useIssueTimeline(issueId, user?.id);

  const [tab, setTab] = useState<ActivityTab>("all");
  const tabCounts = useMemo(() => activityTabCounts(timeline), [timeline]);
  const filtered = useMemo(() => {
    if (tab === "all") return timeline;
    const byId = new Map(timeline.map((e) => [e.id, e]));
    return timeline.filter((e) => classifyEntryRoot(e, byId) === tab);
  }, [timeline, tab]);
  const groups = useMemo(() => groupPanelTimeline(filtered), [filtered]);

  // Per-session UI state: which resolved threads are expanded out of their
  // fold bar, and which activity runs are expanded out of their count line.
  const [expandedResolved, setExpandedResolved] = useState<ReadonlySet<string>>(
    () => new Set<string>(),
  );
  const toggleResolvedExpand = useCallback((rootId: string, expand: boolean) => {
    setExpandedResolved((prev) => {
      const next = new Set(prev);
      if (expand) next.add(rootId);
      else next.delete(rootId);
      return next;
    });
  }, []);
  const [expandedEvents, setExpandedEvents] = useState<ReadonlySet<string>>(
    () => new Set<string>(),
  );

  const handleResolveToggle = useCallback(
    (commentId: string, resolved: boolean) => {
      // Resolving folds the thread back to its bar; unresolving from the bar
      // isn't reachable (the bar only expands), so clearing is safe either way.
      toggleResolvedExpand(commentId, false);
      void toggleResolveComment(commentId, resolved);
    },
    [toggleResolveComment, toggleResolvedExpand],
  );

  // Keep the newest entry visible: the feed is chronological (oldest → newest,
  // composer beneath — chat-shaped), so pin the scroll to the bottom on load
  // and whenever an entry arrives (own submit or WS broadcast).
  const feedRef = useRef<HTMLDivElement>(null);
  const entryCount = timeline.length;
  useEffect(() => {
    const el = feedRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [entryCount, tab, loading]);

  // Same heuristic as issue-detail: tabs appear only once the feed actually
  // mixes origins — a plain human thread isn't cluttered with empty tabs.
  const showTabs =
    tabCounts.all > 0 &&
    [tabCounts.agents, tabCounts.bitrix, tabCounts.people].filter((n) => n > 0)
      .length > 1;

  return (
    <div className="flex flex-col gap-2">
      {showTabs && (
        <Tabs value={tab} onValueChange={(v) => setTab(v as ActivityTab)}>
          <TabsList variant="line">
            <TabsTrigger value="all" className="text-xs">
              {t(($) => $.activity_tabs.all)} · {tabCounts.all}
            </TabsTrigger>
            {tabCounts.agents > 0 && (
              <TabsTrigger value="agents" className="text-xs">
                {t(($) => $.activity_tabs.agents)} · {tabCounts.agents}
              </TabsTrigger>
            )}
            {tabCounts.bitrix > 0 && (
              <TabsTrigger value="bitrix" className="text-xs">
                {t(($) => $.activity_tabs.bitrix)} · {tabCounts.bitrix}
              </TabsTrigger>
            )}
            {tabCounts.people > 0 && (
              <TabsTrigger value="people" className="text-xs">
                {t(($) => $.activity_tabs.people)} · {tabCounts.people}
              </TabsTrigger>
            )}
          </TabsList>
        </Tabs>
      )}

      <div
        ref={feedRef}
        className="flex max-h-[340px] flex-col gap-2 overflow-y-auto pr-0.5"
      >
        {loading && groups.length === 0 ? (
          <p className="px-1 py-2 text-[11px] text-muted-foreground">
            {t(($) => $.timeline.loading)}
          </p>
        ) : groups.length === 0 ? (
          <p className="rounded-lg border border-dashed bg-muted/20 px-3 py-4 text-center text-[11px] text-muted-foreground">
            {t(($) => $.qa_activity.empty)}
          </p>
        ) : (
          groups.map((group) => {
            if (group.kind === "thread") {
              const resolved = !!group.root.resolved_at;
              const expanded = expandedResolved.has(group.root.id);
              if (resolved && !expanded) {
                return (
                  <ResolvedThreadBar
                    key={group.root.id}
                    entry={group.root}
                    replies={group.replies}
                    onExpand={() => toggleResolvedExpand(group.root.id, true)}
                  />
                );
              }
              return (
                <CommentCard
                  key={group.root.id}
                  issueId={issueId}
                  entry={group.root}
                  replies={group.replies}
                  currentUserId={user?.id}
                  onReply={submitReply}
                  onEdit={editComment}
                  onDelete={deleteComment}
                  onToggleReaction={toggleReaction}
                  onResolveToggle={handleResolveToggle}
                  onCollapseResolved={
                    resolved
                      ? () => toggleResolvedExpand(group.root.id, false)
                      : undefined
                  }
                  expandedResolvedIds={expandedResolved}
                  onResolvedExpandChange={toggleResolvedExpand}
                />
              );
            }
            // Activity run — compact one-liners. Multi-entry runs fold behind
            // a count line (status/assignee churn between comments would
            // otherwise drown the discussion in a 380px column).
            const eventsExpanded =
              group.entries.length === 1 || expandedEvents.has(group.id);
            if (!eventsExpanded) {
              return (
                <button
                  key={group.id}
                  type="button"
                  onClick={() =>
                    setExpandedEvents((prev) => new Set(prev).add(group.id))
                  }
                  className="flex items-center gap-1.5 px-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                >
                  <ChevronRight className="h-3 w-3 shrink-0" />
                  <span>
                    {t(($) => $.activity.activity_count, {
                      count: group.entries.length,
                    })}
                  </span>
                </button>
              );
            }
            return (
              <div key={group.id} className="flex flex-col gap-1.5 px-1">
                {group.entries.map((entry) => (
                  <div
                    key={entry.id}
                    className="flex items-center gap-1.5 text-[11px] text-muted-foreground"
                  >
                    <ActorAvatar
                      actorType={entry.actor_type}
                      actorId={entry.actor_id}
                      size={14}
                    />
                    <span className="shrink-0 font-medium">
                      {getActorName(entry.actor_type, entry.actor_id)}
                    </span>
                    <span className="min-w-0 flex-1 truncate">
                      {formatActivity(entry, t, getActorName)}
                    </span>
                    <span className="shrink-0">{timeAgo(entry.created_at)}</span>
                  </div>
                ))}
              </div>
            );
          })
        )}
      </div>

      {/* The SAME composer as the issue lens: ContentEditor with mention://
          completion, slash commands, attachments, draft persistence (shared
          `new:<issueId>` key — one conversation, one draft), and the
          trigger-preview chips showing which agents this comment will
          dispatch. key={issueId}: the lens can switch issues without a
          remount, and the editor only reads its draft at mount. */}
      <CommentInput key={issueId} issueId={issueId} onSubmit={submitComment} />
    </div>
  );
}
