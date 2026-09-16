"use client";

import { useState } from "react";
import { toast } from "sonner";
import { AlertTriangle, Check, ShieldQuestion, X } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { ApiError } from "@agora/core/api";
import {
  useConfirmAssistantOperation,
  useRejectAssistantOperation,
} from "@agora/core/assistant";
import { useT } from "../../i18n";
import type { ConfirmationRequest, OperationOutcome } from "../lib/operation";
import { targetLabel } from "../lib/operation";

interface ConfirmCardProps {
  /** Session the operation belongs to — drives the transcript invalidation. */
  sessionId: string;
  request: ConfirmationRequest;
  /**
   * Outcome already visible in the transcript (a receipt or a "cancelled" row
   * that references this operation). Authoritative when present: it survives a
   * reload, and it is how a confirmation clicked on another device shows up
   * here. Null keeps the card pending.
   */
  outcome: OperationOutcome | null;
}

/**
 * The out-of-band human click that binds a confirmation to one exact
 * operation (docs/agora-assistant-final-plan.md §3 "Bind confirmation to the
 * intended action"). A model-generated `confirm: true` is not evidence of a
 * human confirmation — this card is.
 *
 * Renders in place of the ToolChip for a `needs_confirmation` tool row. It
 * always shows the resolved scope (workspace + target) before the buttons,
 * because the session's focus — not the page the user happens to be on — is
 * what the operation will touch.
 *
 * While a runtime without the confirm/reject endpoints is deployed both
 * buttons 404: the card toasts and stays pending. It never throws into the
 * transcript.
 */
export function ConfirmCard({ sessionId, request, outcome }: ConfirmCardProps) {
  const { t } = useT("assistant");
  // Set by a 409 (the operation changed or aged out server-side) and as
  // immediate feedback for our own click, until the receipt lands over WS.
  const [localOutcome, setLocalOutcome] = useState<OperationOutcome | null>(null);
  const confirmOperation = useConfirmAssistantOperation(sessionId);
  const rejectOperation = useRejectAssistantOperation(sessionId);

  const settled = outcome ?? localOutcome;
  const isBusy = confirmOperation.isPending || rejectOperation.isPending;

  const handleError = (err: unknown) => {
    // 409 is the contract's "you confirmed something that no longer matches" —
    // the only failure that changes the card, because retrying cannot help.
    if (err instanceof ApiError && err.status === 409) {
      setLocalOutcome("expired");
      toast.error(t(($) => $.confirm.toast_changed));
      return;
    }
    toast.error(t(($) => $.toast.send_failed));
  };

  const handleConfirm = () => {
    confirmOperation.mutate(request.operationId, {
      onSuccess: () => setLocalOutcome("confirmed"),
      onError: handleError,
    });
  };

  const handleReject = () => {
    rejectOperation.mutate(request.operationId, {
      onSuccess: () => setLocalOutcome("rejected"),
      onError: handleError,
    });
  };

  const summary = request.summary || t(($) => $.confirm.fallback_summary);
  const target = targetLabel(request.target);

  return (
    <div className="ml-8 flex w-full max-w-md flex-col gap-2 rounded-lg border border-border bg-card px-3 py-2.5 text-sm">
      <div className="flex min-w-0 items-start gap-2">
        <ShieldQuestion className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="break-words font-medium">{summary}</p>
          {(request.workspaceSlug || target) && (
            <p className="mt-0.5 break-words text-xs text-muted-foreground">
              {[request.workspaceSlug, target].filter(Boolean).join(" · ")}
            </p>
          )}
        </div>
      </div>
      <ConfirmFooter
        state={settled}
        isBusy={isBusy}
        onConfirm={handleConfirm}
        onReject={handleReject}
      />
    </div>
  );
}

function ConfirmFooter({
  state,
  isBusy,
  onConfirm,
  onReject,
}: {
  state: OperationOutcome | null;
  isBusy: boolean;
  onConfirm: () => void;
  onReject: () => void;
}) {
  const { t } = useT("assistant");

  if (state === "confirmed") {
    return (
      <p role="status" className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Check className="size-3.5 shrink-0 text-success" />
        {t(($) => $.confirm.confirmed)}
      </p>
    );
  }

  if (state === "rejected") {
    return (
      <p role="status" className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <X className="size-3.5 shrink-0" />
        {t(($) => $.confirm.rejected)}
      </p>
    );
  }

  if (state === "expired") {
    return (
      <p role="status" className="flex items-start gap-1.5 text-xs text-warning">
        <AlertTriangle className="mt-px size-3.5 shrink-0" />
        {t(($) => $.confirm.expired)}
      </p>
    );
  }

  return (
    <div className="flex items-center gap-2">
      <Button size="sm" onClick={onConfirm} disabled={isBusy}>
        {t(($) => $.confirm.confirm)}
      </Button>
      <Button size="sm" variant="ghost" onClick={onReject} disabled={isBusy}>
        {t(($) => $.confirm.cancel)}
      </Button>
    </div>
  );
}
