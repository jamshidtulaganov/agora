"use client";

import { memo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Clock } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { isKnownStaleReason, staleIssuesOptions } from "@agora/core/issues/staleness";
import type { StaleIssue } from "@agora/core/types";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@agora/ui/components/ui/tooltip";
import { useT, useTimeAgo } from "../../i18n";

/**
 * Staleness surfaces — Tier 2 of docs/living-truth-plan.md.
 *
 * "This issue looks wrong" is INFERENCE, so it never writes and never
 * shouts: a muted clock on the row/card, one muted sentence on the detail
 * page. No badge chrome, no destructive color, nothing when the issue is
 * fine — a nudge that costs nothing when it is absent.
 *
 * PLUMBING NOTE (deliberate deviation from "prop-drill a Map from the page"):
 * the indicator resolves its own row from the workspace-wide staleness query
 * instead of taking a `Map<issue_id, StaleIssue>` prop. Reasons:
 *   - `IssueAgentActivityIndicator` — the adjacent per-issue cue in the same
 *     two slots (list row + board card) — already works exactly this way, so
 *     this follows the existing pattern instead of adding a parallel one.
 *   - the prop path would have to thread through issues-page, my-issues-page,
 *     project-detail, actor-issues-panel and then list-view, board-view,
 *     board-column, swimlane-view and their drag overlays — nine files of
 *     signature churn, each also a `memo` boundary.
 *   - TanStack Query IS the shared cache here: every row reads one deduped
 *     request keyed on `wsId`, and `toStaleIssueMap` is memoized per response
 *     so the join costs one Map per fetch, not one per row.
 * Both components stay pure-presentational under the hood (`StalenessTooltip`
 * / `StalenessNote` take a `StaleIssue`), so the connected wrappers are the
 * only part that touches the query.
 */

type ReasonKey = "idle" | "review_done" | "reopened_work" | "blocked_quiet" | "generic";

/**
 * Enum drift downgrades, not crashes (CLAUDE.md "API Response
 * Compatibility"): a reason this build has no copy for — a rule a newer
 * server learned, or an empty string from a partially-parsed row — renders
 * the generic line rather than disappearing.
 */
function reasonKeyOf(stale: StaleIssue): ReasonKey {
  return isKnownStaleReason(stale.reason) ? stale.reason : "generic";
}

/** `since` is server-authored; a missing or unparseable one costs the age line, not the nudge. */
function usableSince(since: string): boolean {
  return !!since && !Number.isNaN(new Date(since).getTime());
}

/**
 * Resolve one issue's staleness row from the workspace staleness query.
 * Returns `undefined` while loading, on error, and for a fresh issue — the
 * three cases the UI treats identically (render nothing).
 */
export function useIssueStaleness(issueId: string): StaleIssue | undefined {
  const wsId = useWorkspaceId();
  const { data } = useQuery(staleIssuesOptions(wsId));
  return data?.get(issueId);
}

/**
 * The quiet glyph for list rows and board cards: a muted clock whose
 * accessible name (and tooltip) carries the localized reason plus how long
 * the issue has looked this way.
 */
export const StalenessTooltip = memo(function StalenessTooltip({
  stale,
}: {
  stale: StaleIssue;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const key = reasonKeyOf(stale);
  const reason = t(($) => $.staleness.short[key]);
  const age = usableSince(stale.since)
    ? t(($) => $.staleness.age, { time: timeAgo(stale.since) })
    : null;
  const label = age ? `${reason} · ${age}` : reason;

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            role="img"
            aria-label={label}
            className="inline-flex shrink-0 items-center text-muted-foreground"
          />
        }
      >
        <Clock className="size-3" aria-hidden="true" />
      </TooltipTrigger>
      <TooltipContent className="flex-col items-start gap-0.5">
        <span>{reason}</span>
        {age && <span className="text-muted-foreground">{age}</span>}
      </TooltipContent>
    </Tooltip>
  );
});

/**
 * Connected row/card indicator. Renders nothing at all — no slot, no
 * placeholder — for a fresh issue, an errored query, or a backend that
 * doesn't serve the route yet.
 */
export const StalenessIndicator = memo(function StalenessIndicator({
  issueId,
}: {
  issueId: string;
}) {
  const stale = useIssueStaleness(issueId);
  if (!stale) return null;
  return <StalenessTooltip stale={stale} />;
});

/**
 * The issue-detail form: one muted sentence, longer than the tooltip, that
 * names the reason and the age inline ("…its pull requests were merged or
 * closed 3d ago"). Same string family as the tooltip, `long` variant.
 */
export const StalenessNote = memo(function StalenessNote({
  stale,
}: {
  stale: StaleIssue;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const key = reasonKeyOf(stale);
  // Every `long` string interpolates {{time}}; without a usable `since`
  // there is no honest time to put there, so fall back to the timeless
  // tooltip wording rather than printing "Invalid Date".
  const sentence = usableSince(stale.since)
    ? t(($) => $.staleness.long[key], { time: timeAgo(stale.since) })
    : t(($) => $.staleness.short[key]);

  return (
    <p className="col-span-2 flex items-start gap-1.5 px-2 py-1 text-xs text-muted-foreground">
      <Clock className="mt-0.5 size-3 shrink-0" aria-hidden="true" />
      <span>{sentence}</span>
    </p>
  );
});

/** Connected detail-page sentence. Renders nothing when the issue is fine. */
export const IssueStalenessNote = memo(function IssueStalenessNote({
  issueId,
}: {
  issueId: string;
}) {
  const stale = useIssueStaleness(issueId);
  if (!stale) return null;
  return <StalenessNote stale={stale} />;
});
