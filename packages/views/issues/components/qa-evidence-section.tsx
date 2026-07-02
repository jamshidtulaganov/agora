"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, CheckCircle2, XCircle, ShieldQuestion, Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { issueKeys, qaEvidenceOptions } from "@agora/core/issues/queries";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { StructuredResult } from "./editor-tests-panel";
import { QADesignCompare } from "../../qa/components/qa-design-compare";

// Evidence-first QA section. Opening an in-review task shows its frozen run_qa
// verdict — the command table with new-failure attribution — read from ONE
// indexed qa_evidence row (api.getQAEvidence), not by re-parsing the timeline.
// This is the default "QA environment": a deterministic, attributable verdict
// that scales to many concurrent task-opens with zero per-task box. A live
// preview box (the rare interactive case) is a later, on-demand escape hatch.
//
// Gating: rendered by issue-detail for QA-relevant statuses; the component
// hides itself when there's no evidence AND the issue is not in_review, so it
// never shows an empty box on a backlog/in-progress task.

interface QAEvidenceSectionProps {
  issueId: string;
  status: string;
}

export function QAEvidenceSection({ issueId, status }: QAEvidenceSectionProps) {
  const { t } = useT("issues");
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(true);
  const [rerunning, setRerunning] = useState(false);

  const { data: evidence, isLoading } = useQuery(qaEvidenceOptions(issueId));

  // Don't intrude on tasks where QA isn't relevant yet: hide unless there's a
  // captured verdict or the issue is actively in review.
  if (!evidence && status !== "in_review") return null;

  const verdict = evidence?.verdict ?? "";
  const verdictIcon =
    verdict === "pass" ? (
      <CheckCircle2 className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
    ) : verdict === "fail" ? (
      <XCircle className="size-3.5 shrink-0 text-destructive" />
    ) : (
      <ShieldQuestion className="size-3.5 shrink-0 text-muted-foreground" />
    );
  const verdictLabel =
    verdict === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : verdict === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : t(($) => $.qa_evidence.verdict_unknown);

  const rerun = async () => {
    if (rerunning) return;
    setRerunning(true);
    try {
      await api.sliceAction(issueId, { kind: "run_qa" });
      void queryClient.invalidateQueries({ queryKey: issueKeys.tasks(issueId) });
      void queryClient.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      toast.success(t(($) => $.slice_actions.toast_fired));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.slice_actions.toast_failed));
    } finally {
      setRerunning(false);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={cn(
          "mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70",
          open ? "" : "text-muted-foreground hover:text-foreground",
        )}
        onClick={() => setOpen(!open)}
      >
        {t(($) => $.qa_evidence.section)}
        {evidence && (
          <span className="ml-1 inline-flex items-center gap-1 text-[10px] font-normal text-muted-foreground">
            {verdictIcon}
            {verdictLabel}
          </span>
        )}
        <ChevronRight
          className={cn(
            "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
            open ? "rotate-90" : "",
          )}
        />
      </button>

      {open && (
        <div className="space-y-2 pl-2">
          {isLoading ? (
            <div className="flex items-center gap-2 px-1 py-2 text-[11px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
            </div>
          ) : evidence ? (
            <div className="space-y-2">
              <div className="flex items-center gap-2 px-1">
                {verdictIcon}
                <span className="text-xs font-medium text-foreground/90">{verdictLabel}</span>
                {evidence.captured_at && (
                  <span className="ml-auto text-[10px] text-muted-foreground">
                    {t(($) => $.qa_evidence.captured)} {new Date(evidence.captured_at).toLocaleString()}
                  </span>
                )}
              </div>
              {evidence.summary && (
                <p className="px-1 text-[11px] text-muted-foreground">{evidence.summary}</p>
              )}
              {evidence.result && evidence.result.commands.length > 0 && (
                <StructuredResult result={evidence.result} />
              )}
              {evidence.result?.design && (
                <div className="mt-2">
                  <QADesignCompare design={evidence.result.design} />
                </div>
              )}
            </div>
          ) : (
            <div className="rounded-md border border-dashed border-border bg-muted/20 px-3 py-4 text-center">
              <p className="text-[11px] text-muted-foreground">{t(($) => $.qa_evidence.empty)}</p>
              <p className="mt-0.5 text-[10px] text-muted-foreground/70">{t(($) => $.qa_evidence.empty_hint)}</p>
            </div>
          )}

          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 w-full text-[11px]"
            disabled={rerunning}
            onClick={rerun}
          >
            {rerunning ? (
              <>
                <Loader2 className="size-3.5 animate-spin" />
                {t(($) => $.qa_evidence.rerunning)}
              </>
            ) : (
              <>
                <RefreshCw className="size-3.5" />
                {t(($) => $.qa_evidence.rerun)}
              </>
            )}
          </Button>
        </div>
      )}
    </div>
  );
}
