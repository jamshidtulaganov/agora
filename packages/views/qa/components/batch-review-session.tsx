"use client";

import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { Button } from "@agora/ui/components/ui/button";
import { useT } from "../../i18n";
import { WorkLensBody } from "../../issues/components/review-lens";

// A batched review pass over rows picked in the decision queue
// (docs/orchestration-upgrade-plan.md §A2, "batched review sessions").
//
// The honest claim for this, from the plan: it does not make anyone decide
// faster. It removes the ~20 seconds of context reconstruction per item and
// the tab-hunting between them, which is what decides WHEN the human sits
// down — a ten-item queue that takes four minutes gets done at the first
// coffee; four scattered notifications get done tomorrow.
//
// Mechanically it is a local list of issue ids and a cursor. Nothing else:
// the surface inside is the review workspace that already exists, so approve
// / request-changes keep using the mutations they already use, and this file
// adds no pipeline, no state machinery and no second way to decide.

export function BatchReviewSession({
  issueIds,
  onClose,
}: {
  issueIds: string[];
  onClose: () => void;
}) {
  const { t } = useT("issues");
  const [index, setIndex] = useState(0);

  // The pass shrinks if the caller hands over a shorter list (a row someone
  // else cleared); clamp rather than render past the end.
  const safeIndex = Math.min(index, Math.max(0, issueIds.length - 1));
  const current = issueIds[safeIndex];
  const atFirst = safeIndex === 0;
  const atLast = safeIndex >= issueIds.length - 1;

  useEffect(() => {
    // j / k — the keyboard pass named in the plan. Approve / request-changes
    // deliberately stay mouse-driven inside the review surface: binding a
    // one-key approve over a merge decision is exactly the kind of speed
    // this plan does not want to buy.
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))) {
        return;
      }
      if (e.key === "j") setIndex((i) => Math.min(i + 1, issueIds.length - 1));
      if (e.key === "k") setIndex((i) => Math.max(i - 1, 0));
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [issueIds.length]);

  if (!current) return null;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[85vh] w-[min(1100px,95vw)] max-w-none flex-col gap-3 sm:max-w-none">
        <DialogHeader className="flex-row items-center gap-3 space-y-0">
          <DialogTitle className="text-sm">{t(($) => $.decision_queue.batch_title)}</DialogTitle>
          <span className="text-[11px] text-muted-foreground">
            {t(($) => $.decision_queue.batch_position, {
              index: safeIndex + 1,
              total: issueIds.length,
            })}
          </span>
        </DialogHeader>

        {/* The review workspace, unchanged — one issue at a time. Keyed on the
            issue id so moving to the next item remounts instead of leaking
            the previous issue's draft state into it. */}
        <div className="min-h-0 flex-1 overflow-auto rounded-md border">
          <WorkLensBody key={current} issueId={current} />
        </div>

        <div className="flex items-center gap-2">
          <span className="text-[11px] text-muted-foreground">
            {t(($) => $.decision_queue.batch_hint)}
          </span>
          <div className="ml-auto flex items-center gap-1.5">
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 text-[11px]"
              disabled={atFirst}
              onClick={() => setIndex(safeIndex - 1)}
            >
              {t(($) => $.decision_queue.batch_prev)}
            </Button>
            {atLast ? (
              <Button type="button" size="sm" className="h-7 text-[11px]" onClick={onClose}>
                {t(($) => $.decision_queue.batch_done)}
              </Button>
            ) : (
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-7 text-[11px]"
                onClick={() => setIndex(safeIndex + 1)}
              >
                {t(($) => $.decision_queue.batch_next)}
              </Button>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
