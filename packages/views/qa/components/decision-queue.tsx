"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CircleHelp, GitMerge, HandHelping, ShieldQuestion, SquarePen } from "lucide-react";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  decisionItemAgeSeconds,
  decisionQueueOptions,
  isKnownDecisionKind,
  summarizeDecisionQueue,
  usableDecisionItems,
} from "@agora/core/issues/decision-queue";
import type { DecisionQueueItem } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink } from "../../navigation";
import { BatchReviewSession } from "./batch-review-session";

// The decision queue — the Release page's Queue lane
// (docs/orchestration-upgrade-plan.md §A2).
//
// ONE ranked list of everything waiting on a human: an agent's question, a
// change waiting for review, a QA verdict nobody has judged, a merge waiting
// on approval. They are item kinds of one object, not four inboxes — split
// apart they reproduce the failure the research names ("agents DDoSing our
// attention") instead of fixing it.
//
// Three rules this file holds to, from §E:
//   - No throughput. The header states EXACT counts and the age of the
//     oldest item. Never a rate, never a per-person comparison.
//   - No alarm. The risk chip is a muted token; `critical` is a FILLED chip,
//     not a red one. An empty queue is calm, not celebratory.
//   - The server ranked this list. We render it in the order it arrived —
//     re-sorting here would silently disagree with the same queue on a phone.

// Kinds that land in the review surface with a decision to make there, and
// can therefore be stepped through as one pass. `review_failed` is
// deliberately NOT here: a failed review is a judgement about what to do
// next, not an approve/request-changes tap, so it opens on its own.
const BATCHABLE_KINDS = new Set(["merge_ready"]);

function kindGlyph(kind: string) {
  switch (kind) {
    case "escalation":
      return HandHelping;
    case "merge_ready":
      return GitMerge;
    case "qa_failed":
      return ShieldQuestion;
    case "review_failed":
      return SquarePen;
    // A kind a newer server learned still gets a row and still opens its
    // issue — it just wears the generic glyph (CLAUDE.md, enum drift).
    default:
      return CircleHelp;
  }
}

/**
 * The risk chip's weight. Deliberately monochrome: this queue is read when
 * someone is already behind, and a wall of red trains people to stop looking.
 * `critical` earns a filled chip; everything below it is progressively
 * quieter; `""` (the project has no risk map) earns no chip at all, because
 * an empty tier is "no opinion", not "safe".
 */
function tierChipClass(tier: string): string {
  switch (tier) {
    case "critical":
      return "bg-foreground text-background";
    case "guarded":
      return "bg-muted-foreground/15 text-foreground";
    case "safe":
      return "border border-border text-muted-foreground";
    // "unclassified" is the project saying "no opinion" out loud, and an
    // unknown tier is still information — both render quietly, neither
    // renders as "safe".
    default:
      return "border border-border text-muted-foreground";
  }
}

export function DecisionQueue({ projectId }: { projectId?: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const wp = useWorkspacePaths();
  const { data, isLoading, isError } = useQuery(decisionQueueOptions(wsId, projectId));

  const items = useMemo(() => usableDecisionItems(data), [data]);
  const summary = useMemo(() => summarizeDecisionQueue(data), [data]);

  // The batch pass: a local queue of issue ids plus a cursor. Deliberately
  // no store, no URL state, no machinery — the pass is a sitting, and a
  // sitting does not need to survive a reload.
  const [selected, setSelected] = useState<string[]>([]);
  const [passIds, setPassIds] = useState<string[] | null>(null);

  // Drop selections whose row left the queue (someone else answered it) so
  // the pass can never open an issue that is no longer waiting.
  useEffect(() => {
    const live = new Set(items.map((i) => i.issue_id));
    setSelected((prev) => {
      const next = prev.filter((id) => live.has(id));
      return next.length === prev.length ? prev : next;
    });
  }, [items]);

  const toggle = (issueId: string) =>
    setSelected((prev) =>
      prev.includes(issueId) ? prev.filter((id) => id !== issueId) : [...prev, issueId],
    );

  // Relative age from whichever spelling the server sent, rendered by the
  // same formatter the inbox uses.
  const ageLabel = (item: DecisionQueueItem) =>
    timeAgo(new Date(Date.now() - decisionItemAgeSeconds(item) * 1000).toISOString());

  const kindLabel = (kind: string) => {
    // Narrowed first: a kind this build has never seen gets the generic
    // label rather than falling through a switch that might grow a branch
    // for it later (CLAUDE.md, enum drift downgrades).
    if (!isKnownDecisionKind(kind)) return t(($) => $.decision_queue.kind_generic);
    switch (kind) {
      case "escalation":
        return t(($) => $.decision_queue.kind_escalation);
      case "merge_ready":
        return t(($) => $.decision_queue.kind_merge_ready);
      case "qa_failed":
        return t(($) => $.decision_queue.kind_qa_failed);
      case "review_failed":
        return t(($) => $.decision_queue.kind_review_failed);
      default:
        return t(($) => $.decision_queue.kind_generic);
    }
  };

  // What this person is being asked for, in one line.
  //
  // The SERVER's `needed` wins whenever it sent one: it knows which gate is
  // blocking and can name it specifically ("2 blockers from code review").
  // The localized copy is the floor for the rows it did not describe —
  // looked up by `needed_code` first (a stable identifier a future server
  // can add copy for) and by `kind` second.
  const localNeeds = (code: string) => {
    switch (code) {
      case "escalation":
        return t(($) => $.decision_queue.needs_escalation);
      case "merge_ready":
        return t(($) => $.decision_queue.needs_merge_ready);
      case "qa_failed":
        return t(($) => $.decision_queue.needs_qa_failed);
      case "review_failed":
        return t(($) => $.decision_queue.needs_review_failed);
      default:
        return "";
    }
  };
  const needsLine = (item: DecisionQueueItem) =>
    item.needed ||
    // An escalation row carries the agent's literal question; showing it
    // beats showing our generic line about it.
    item.escalation?.prompt ||
    localNeeds(item.needed_code) ||
    localNeeds(item.kind) ||
    t(($) => $.decision_queue.needs_generic);

  // Where the row's one tap goes: the lens that already exists for the kind.
  // An escalation opens the issue itself, where the escalation card renders;
  // an unknown kind does the same, which is always a useful destination.
  const hrefFor = (item: DecisionQueueItem) => {
    switch (item.kind) {
      case "merge_ready":
      case "review_failed":
        return `${wp.issueDetail(item.issue_id)}?lens=work`;
      case "qa_failed":
        return `${wp.issueDetail(item.issue_id)}?lens=qa`;
      case "escalation":
      default:
        return wp.issueDetail(item.issue_id);
    }
  };

  if (isError) {
    // A failed fetch must NOT read as an empty queue: "nothing is waiting on
    // you" is a claim, and making it on a network error is how a parked run
    // goes unanswered for a day (CLAUDE.md, API Response Compatibility).
    return (
      <div className="flex w-full flex-col gap-4 px-8 py-8">
        <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-12 text-center text-sm text-muted-foreground">
          {t(($) => $.decision_queue.load_failed)}
        </div>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="flex w-full flex-col gap-2 px-8 py-8" aria-hidden>
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-3/4" />
      </div>
    );
  }

  if (items.length === 0) {
    return (
      <div className="flex w-full flex-col gap-4 px-8 py-8">
        <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-12 text-center text-sm text-muted-foreground">
          {t(($) => $.decision_queue.empty)}
        </div>
      </div>
    );
  }

  return (
    <div className="flex w-full flex-col gap-3 px-8 py-8">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-medium">{t(($) => $.decision_queue.heading)}</h2>
        {/* Exact counts only: how many, and how long the oldest has waited. */}
        <p className="text-[12px] text-muted-foreground">
          {t(($) => $.decision_queue.summary_waiting, { count: summary.waiting })}
          {summary.oldestAgeSeconds > 0
            ? ` · ${t(($) => $.decision_queue.summary_oldest, {
                age: timeAgo(new Date(Date.now() - summary.oldestAgeSeconds * 1000).toISOString()),
              })}`
            : ""}
        </p>
        {selected.length > 0 && (
          <div className="ml-auto flex items-center gap-1.5">
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 text-[11px]"
              onClick={() => setPassIds(selected)}
            >
              {t(($) => $.decision_queue.batch_start, { count: selected.length })}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-7 text-[11px] text-muted-foreground"
              onClick={() => setSelected([])}
            >
              {t(($) => $.decision_queue.batch_clear)}
            </Button>
          </div>
        )}
      </div>

      {/* Server order = the ranking. Never re-sorted here. */}
      <ul className="flex flex-col divide-y rounded-lg border">
        {items.map((item) => {
          const Glyph = kindGlyph(item.kind);
          const batchable = BATCHABLE_KINDS.has(item.kind);
          return (
            <li key={`${item.kind}:${item.issue_id}`} className="flex items-center gap-2 px-3 py-2">
              {batchable ? (
                <Checkbox
                  checked={selected.includes(item.issue_id)}
                  onCheckedChange={() => toggle(item.issue_id)}
                  aria-label={t(($) => $.decision_queue.select_row, {
                    identifier: item.identifier || item.issue_id,
                  })}
                />
              ) : (
                // Keeps every row's text on the same left edge, batchable or not.
                <span className="size-4 shrink-0" aria-hidden />
              )}
              <AppLink
                href={hrefFor(item)}
                className="flex min-w-0 flex-1 items-center gap-2.5 rounded-md py-0.5 hover:text-foreground"
              >
                <Glyph
                  className="size-4 shrink-0 text-muted-foreground"
                  aria-label={kindLabel(item.kind)}
                />
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                      {item.identifier}
                    </span>
                    <span className="truncate text-[13px]">{item.title}</span>
                  </span>
                  <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">
                    {needsLine(item)}
                  </span>
                </span>
                {/* Risk tiers are schema-level identifiers, so they stay
                    lowercase English in every locale — same rule as issue
                    status (conventions.mdx §2). */}
                {item.risk_tier && (
                  <span
                    className={cn(
                      "shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium",
                      tierChipClass(item.risk_tier),
                    )}
                  >
                    {item.risk_tier}
                  </span>
                )}
                <span className="w-16 shrink-0 text-right text-[11px] whitespace-nowrap tabular-nums text-muted-foreground">
                  {ageLabel(item)}
                </span>
              </AppLink>
            </li>
          );
        })}
      </ul>

      {passIds && passIds.length > 0 && (
        <BatchReviewSession
          issueIds={passIds}
          onClose={() => {
            setPassIds(null);
            setSelected([]);
          }}
        />
      )}
    </div>
  );
}
