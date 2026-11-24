/* eslint-disable i18next/no-literal-string -- connector pages are English-only for now; i18n is a follow-up */
"use client";

import { AlertTriangle, CheckCircle2, Loader2, XCircle } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  assistantOperationOptions,
  useConfirmAssistantOperation,
  useRejectAssistantOperation,
} from "@agora/core/assistant";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Button } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { operationState, parseOperationTarget, targetLabel } from "../assistant/lib/operation";

const OUTCOME: Record<string, { icon: typeof CheckCircle2; title: string; body: string; tone: string }> = {
  confirmed: { icon: CheckCircle2, title: "Done", body: "You can go back to your AI assistant.", tone: "text-brand" },
  rejected: { icon: XCircle, title: "Cancelled", body: "Nothing was changed.", tone: "text-muted-foreground" },
  expired: { icon: XCircle, title: "This request expired", body: "Ask your AI assistant again.", tone: "text-muted-foreground" },
  failed: { icon: AlertTriangle, title: "It did not work", body: "Nothing or only part of it changed. Check in Agora, then ask again.", tone: "text-destructive" },
  uncertain: { icon: AlertTriangle, title: "Result unknown", body: "Check in Agora before trying again.", tone: "text-destructive" },
  unavailable: { icon: XCircle, title: "Request not found", body: "It may belong to another account, or it was removed.", tone: "text-muted-foreground" },
};

/**
 * The out-of-band human click for a destructive action an MCP client (Claude,
 * ChatGPT) asked for. The client cannot confirm it; only this page can.
 */
export function ConfirmActionPage({ operationId }: { operationId: string }) {
  const operation = useQuery({
    ...assistantOperationOptions(operationId),
    enabled: operationId !== "",
    // While the confirmed action runs, follow it to its outcome.
    refetchInterval: (q) => (q.state.data?.status === "confirmed" && !q.state.data?.outcome ? 2000 : false),
  });
  const confirm = useConfirmAssistantOperation("");
  const reject = useRejectAssistantOperation("");
  const op = operation.data;
  const busy = confirm.isPending || reject.isPending;

  let state: string | null = null;
  if (operationId === "" || operation.isError || (operation.isSuccess && !op?.id)) state = "unavailable";
  else if (op?.id) state = operationState(op);
  const target = targetLabel(parseOperationTarget(op?.target));
  const outcome = state && state !== "processing" ? OUTCOME[state] ?? OUTCOME.unavailable : null;

  return (
    // The root <body> is overflow-hidden; full-page views own their scrolling.
    <div className="flex h-svh overflow-y-auto bg-background">
      <div className="m-auto w-full max-w-md px-4 py-10">
        <div className="mb-6 flex justify-center">
          <AgoraIcon size="lg" />
        </div>
        <div className="rounded-xl border border-border bg-card p-6 shadow-sm">
          {operation.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-6 w-2/3" />
              <Skeleton className="h-16 w-full" />
            </div>
          ) : outcome ? (
            <div className="flex flex-col items-center gap-2 text-center">
              <outcome.icon className={`size-8 ${outcome.tone}`} aria-hidden="true" />
              <h1 className="text-lg font-semibold">{outcome.title}</h1>
              <p className="text-sm text-muted-foreground">{outcome.body}</p>
            </div>
          ) : state === "processing" ? (
            <div className="flex flex-col items-center gap-2 text-center">
              <Loader2 className="size-8 animate-spin text-muted-foreground" aria-hidden="true" />
              <h1 className="text-lg font-semibold">Working on it…</h1>
            </div>
          ) : (
            <>
              <h1 className="text-lg font-semibold">Confirm this action</h1>
              <p className="mt-1 text-sm text-muted-foreground">Your AI assistant asked Agora to:</p>
              <div className="mt-4 rounded-lg border border-border bg-muted/30 p-3">
                <p className="text-sm font-medium">{op?.summary}</p>
                {(target || op?.workspace_slug) && (
                  <p className="mt-1 truncate text-xs text-muted-foreground">
                    {[op?.workspace_slug, target].filter(Boolean).join(" · ")}
                  </p>
                )}
              </div>
              <p className="mt-3 flex items-center gap-2 text-xs text-destructive">
                <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
                This can&apos;t be undone.
              </p>
              <div className="mt-6 flex gap-2">
                <Button variant="outline" className="flex-1" disabled={busy} onClick={() => reject.mutate(operationId)}>
                  Cancel
                </Button>
                <Button variant="destructive" className="flex-1" disabled={busy} onClick={() => confirm.mutate(operationId)}>
                  {confirm.isPending && <Loader2 className="animate-spin" />}
                  Confirm
                </Button>
              </div>
              {(confirm.isError || reject.isError) && (
                <p role="alert" className="mt-3 text-sm text-destructive">
                  That did not go through. Reload the page to see the current state.
                </p>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
