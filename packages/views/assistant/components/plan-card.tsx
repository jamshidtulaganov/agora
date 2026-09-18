"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Check, Circle, ListChecks, Minus, XCircle } from "lucide-react";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { cn } from "@agora/ui/lib/utils";
import { ApiError } from "@agora/core/api";
import {
  useAssistantOperation,
  useConfirmAssistantOperation,
  useRejectAssistantOperation,
} from "@agora/core/assistant";
import { useT } from "../../i18n";
import type {
  ConfirmationRequest,
  OperationOutcome,
  PlanItem,
  PlanItemOutcome,
  PlanItemResult,
} from "../lib/operation";
import { operationState, parsePlanItemResults, targetLabel } from "../lib/operation";
import { humanizeToolName } from "../lib/tool-summary";
import { ConfirmFooter } from "./confirm-card";

interface PlanCardProps {
  /** Session the operation belongs to — drives the transcript invalidation. */
  sessionId: string;
  /** The parked plan: title in `summary`, rows in `items`. */
  request: ConfirmationRequest;
  /** Overall outcome already visible in the transcript. Null keeps it pending. */
  outcome: OperationOutcome | null;
  /**
   * Per-row outcomes read off the execution receipt the server persisted in
   * the transcript (lib/operation.ts, `planItemResultsAfter`). Empty while the
   * plan is still pending — and after a reload it is how the receipt comes
   * back, exactly the channel the single-operation card uses for its own
   * final state.
   */
  itemResults: PlanItemResult[];
}

/**
 * A plan: one operation, many rows (docs/assistant-domain-plan.md §3a). The
 * user reviews the list, unchecks whatever shouldn't run, and presses ONE
 * Confirm — the whole point of the primitive is that N writes cost one human
 * decision instead of N.
 *
 * It is the ConfirmCard's sibling, not a new dialect: same operation
 * lifecycle (pending → confirmed / rejected / expired), same mutations, same
 * footer copy. What it adds is the checklist and, once executed, a per-row
 * receipt.
 *
 * Chosen by `operation.kind === "plan"` WITH decodable rows. A plan whose
 * items didn't decode falls back to the single-operation card in
 * message-list.tsx — a checklist with no rows is worse than no checklist.
 */
export function PlanCard({ sessionId, request, outcome, itemResults }: PlanCardProps) {
  const { t } = useT("assistant");
  // The only two pieces of local state: which rows the user unchecked, and
  // the "we clicked, waiting for the persisted outcome" flag the ConfirmCard
  // keeps too (also set by a 409, where the server state changed under us).
  const [skipped, setSkipped] = useState<ReadonlySet<number>>(() => new Set<number>());
  const [localOutcome, setLocalOutcome] = useState<OperationOutcome | null>(null);

  const operation = useAssistantOperation(request.operationId);
  const confirmOperation = useConfirmAssistantOperation(sessionId);
  const rejectOperation = useRejectAssistantOperation(sessionId);

  const authoritative = operation.data?.id === request.operationId
    ? operationState(operation.data)
    : null;
  const settled = authoritative ?? outcome ?? localOutcome ??
    (operation.isPending ? "processing" : operation.isError || !operation.data?.id ? "unavailable" : null);
  const isBusy = confirmOperation.isPending || rejectOperation.isPending || operation.isFetching;

  // Receipt first (it survives a reload and a confirm clicked on another
  // device), then the operation read for a server that re-serves the outcomes
  // there. Proposal rows carry no `outcome`, so a still-pending plan decodes
  // to an empty list and the checklist stays live.
  const results = itemResults.length > 0
    ? itemResults
    : parsePlanItemResults(operation.data?.items);
  const resultByIndex = new Map(results.map((result) => [result.index, result]));
  const isExecuted = results.length > 0;
  const stoppedEarly = results.some((result) => result.outcome === "not_run");
  const isPending = settled === null && !isExecuted;

  const skippedItems = request.items
    .filter((item) => skipped.has(item.index))
    .map((item) => item.index);
  const selected = request.items.length - skippedItems.length;

  const handleError = (err: unknown) => {
    if (err instanceof ApiError && err.status === 409) {
      setLocalOutcome("processing");
      toast.error(t(($) => $.confirm.toast_changed));
      return;
    }
    setLocalOutcome("processing");
    toast.error(t(($) => $.toast.send_failed));
  };

  const handleConfirm = () => {
    // Nothing unchecked ⇒ send the bare operation id, which is byte-for-byte
    // the request a single-operation confirm has always made.
    confirmOperation.mutate(
      skippedItems.length > 0
        ? { operationId: request.operationId, skippedItems }
        : request.operationId,
      { onSuccess: () => setLocalOutcome("processing"), onError: handleError },
    );
  };

  const handleReject = () => {
    rejectOperation.mutate(request.operationId, {
      onSuccess: () => setLocalOutcome("processing"),
      onError: handleError,
    });
  };

  const handleCheck = async () => {
    const result = await operation.refetch();
    if (!result.isError && result.data?.id === request.operationId && result.data.status === "pending") {
      setLocalOutcome(null);
    }
  };

  const toggle = (index: number) => {
    setSkipped((current) => {
      const next = new Set(current);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  };

  const title = request.summary || t(($) => $.plan.fallback_title);
  const target = targetLabel(request.target);

  return (
    <div className="ml-8 flex w-full max-w-md flex-col gap-2 rounded-lg border border-border bg-card px-3 py-2.5 text-sm">
      <div className="flex min-w-0 items-start gap-2">
        <ListChecks className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="break-words font-medium">{title}</p>
          {(request.workspaceSlug || target) && (
            <p className="mt-0.5 break-words text-xs text-muted-foreground">
              {[request.workspaceSlug, target].filter(Boolean).join(" · ")}
            </p>
          )}
        </div>
      </div>

      {/* One note for the whole plan, never one per row: a stop is a single
          event, and repeating it 12 times buries the row that actually
          failed. */}
      {stoppedEarly && (
        <p className="text-xs text-muted-foreground">{t(($) => $.plan.stopped_note)}</p>
      )}

      {/* 25 rows is the server-side cap; the list scrolls inside the card so a
          long plan never pushes the buttons off the transcript. */}
      <ul className="-mx-1 max-h-56 overflow-y-auto">
        {request.items.map((item) => (
          <li key={item.index}>
            <PlanRow
              item={item}
              isPending={isPending}
              isBusy={isBusy}
              isChecked={!skipped.has(item.index)}
              result={resultByIndex.get(item.index)}
              onToggle={() => toggle(item.index)}
            />
          </li>
        ))}
      </ul>

      {isPending && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.plan.selected_count, { selected, total: request.items.length })}
        </p>
      )}

      <ConfirmFooter
        state={settled}
        isBusy={isBusy}
        confirmDisabled={selected === 0}
        onConfirm={handleConfirm}
        onReject={handleReject}
        onCheck={handleCheck}
      />
    </div>
  );
}

/**
 * One plan row. Pending: a checkbox the user can uncheck. Executed: the same
 * line with the checkbox replaced by an outcome glyph. Everything else
 * (rejected, expired, a row the server reported no outcome for) keeps the
 * line with a neutral bullet — quiet, and never a verdict it can't back.
 */
function PlanRow({
  item,
  isPending,
  isBusy,
  isChecked,
  result,
  onToggle,
}: {
  item: PlanItem;
  isPending: boolean;
  isBusy: boolean;
  isChecked: boolean;
  result: PlanItemResult | undefined;
  onToggle: () => void;
}) {
  const { t } = useT("assistant");
  const summary = item.summary || humanizeToolName(item.tool);
  const tool = item.tool ? humanizeToolName(item.tool) : t(($) => $.plan.tool_fallback);
  // A label only while there is a control to label; afterwards the row is
  // plain text and a <label> would be a lie to assistive tech.
  const Wrapper = isPending ? "label" : "div";

  return (
    <Wrapper
      className={cn(
        "flex min-w-0 items-start gap-2 rounded-md px-1 py-1",
        isPending && "cursor-pointer hover:bg-accent/40",
      )}
    >
      {isPending ? (
        // No aria-label: the wrapping <label> is the row, so Base UI points
        // the control at it and the whole line (summary + tool) becomes the
        // accessible name — and clicking anywhere on the row toggles.
        <Checkbox
          className="mt-0.5 shrink-0"
          checked={isChecked}
          disabled={isBusy}
          onCheckedChange={onToggle}
        />
      ) : (
        <OutcomeGlyph outcome={result?.outcome} />
      )}
      <span className="min-w-0 flex-1">
        <span
          className={cn(
            "block break-words",
            result?.outcome === "skipped" && "text-muted-foreground",
          )}
        >
          {summary}
          {result?.identifier && (
            <span className="ml-1 text-muted-foreground">{result.identifier}</span>
          )}
        </span>
        {result?.outcome === "failed" && result.error && (
          <span className="mt-0.5 block truncate text-xs text-destructive">{result.error}</span>
        )}
      </span>
      <span className="mt-px shrink-0 text-[11px] text-muted-foreground">{tool}</span>
    </Wrapper>
  );
}

/**
 * The per-row receipt mark. `not_run` and anything this build doesn't
 * recognise share one neutral dot — enum drift downgrades the rendering, it
 * never breaks the list — and only an outcome the server actually reported
 * gets a spoken label, so a rejected or still-settling row stays a bullet
 * rather than claiming a verdict.
 */
function OutcomeGlyph({ outcome }: { outcome: PlanItemOutcome | undefined }) {
  const { t } = useT("assistant");

  const mark = outcome === "ok" ? (
    <Check className="size-3.5 text-muted-foreground" />
  ) : outcome === "failed" ? (
    <XCircle className="size-3.5 text-destructive" />
  ) : outcome === "skipped" ? (
    <Minus className="size-3.5 text-muted-foreground" />
  ) : (
    <Circle className="size-3 text-muted-foreground/60" />
  );

  const label =
    outcome === "ok" ? t(($) => $.plan.outcome.ok)
    : outcome === "failed" ? t(($) => $.plan.outcome.failed)
    : outcome === "skipped" ? t(($) => $.plan.outcome.skipped)
    : outcome === "not_run" ? t(($) => $.plan.outcome.not_run)
    : "";

  return (
    <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center">
      {mark}
      {label && <span className="sr-only">{label}</span>}
    </span>
  );
}
