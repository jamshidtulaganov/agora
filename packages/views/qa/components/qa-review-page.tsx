"use client";

import { useState } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { ArrowLeft, CheckCircle2, XCircle, RefreshCw, ExternalLink, Loader2, Bug } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import { issueDetailOptions, qaEvidenceOptions, issueKeys } from "@agora/core/issues/queries";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { StructuredResult } from "../../issues/components/editor-tests-panel";
import { TestCasesPanel } from "./test-cases-panel";
import { verdictIcon, verdictTone } from "./verdict";
import { FileBugSheet } from "./file-bug-sheet";

// The QA team's own instrument surface — a DEDICATED page (not the dev-oriented
// issue detail). Reached from the QA cockpit: a row opens /qa/<issueId> here.
// It surfaces only what QA needs to triage: the deterministic verdict (command
// table with new-failure attribution), and the triage actions (pass / fail /
// re-run). No Prompts / Editor / Repository dev chrome.

type Verdict = "pass" | "fail" | "pending";

function verdictFromLabels(names: string[]): Verdict {
  if (names.includes("qa:fail")) return "fail";
  if (names.includes("qa:pass")) return "pass";
  return "pending";
}

export function QAReviewPage({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const qc = useQueryClient();
  const { t } = useT("issues");
  const [bugOpen, setBugOpen] = useState(false);

  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: evidence } = useQuery(qaEvidenceOptions(issueId));
  const { data: labelCatalog } = useQuery({
    queryKey: ["labels", wsId],
    queryFn: () => api.listLabels(),
    staleTime: 60_000,
  });

  const labelId = (name: string) => labelCatalog?.labels.find((l) => l.name === name)?.id;
  const issueLabelNames = (issue?.labels ?? []).map((l) => l.name);
  const humanVerdict = verdictFromLabels(issueLabelNames);

  // Triage: set the human verdict by attaching the chosen qa:* label and
  // detaching the opposite. Mirrors the cockpit's label-derived lanes, so the
  // queue re-groups the moment QA decides.
  const setVerdict = useMutation({
    mutationFn: async (next: "pass" | "fail") => {
      const addId = labelId(next === "pass" ? "qa:pass" : "qa:fail");
      const removeId = labelId(next === "pass" ? "qa:fail" : "qa:pass");
      if (!addId) throw new Error(`Label qa:${next} not found in this workspace`);
      if (removeId && issueLabelNames.includes(next === "pass" ? "qa:fail" : "qa:pass")) {
        await api.detachLabel(issueId, removeId);
      }
      await api.attachLabel(issueId, addId);
    },
    onSuccess: (_d, next) => {
      void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
      void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
      toast.success(next === "pass" ? t(($) => $.qa_review.marked_pass) : t(($) => $.qa_review.marked_fail));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });

  const rerun = useMutation({
    mutationFn: () => api.sliceAction(issueId, { kind: "run_qa" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: issueKeys.tasks(issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      toast.success(t(($) => $.slice_actions.toast_fired));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.slice_actions.toast_failed)),
  });

  const verdict = evidence?.verdict ?? "";
  const verdictLabel =
    verdict === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : verdict === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : t(($) => $.qa_evidence.verdict_unknown);

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-5 px-8 py-6">
      <AppLink
        href={wp.qa()}
        className="flex items-center gap-1.5 text-[13px] text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="size-3.5" />
        {t(($) => $.qa_review.back)}
      </AppLink>

      {isLoading || !issue ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <>
          <header className="space-y-1.5">
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs text-muted-foreground">{issue.identifier}</span>
              <AppLink
                href={wp.issueDetail(issueId)}
                className="ml-auto flex items-center gap-1 text-[12px] text-muted-foreground hover:text-foreground"
              >
                {t(($) => $.qa_review.open_full)}
                <ExternalLink className="size-3" />
              </AppLink>
            </div>
            <h1 className="text-lg font-semibold leading-tight">{issue.title}</h1>
          </header>

          {/* Verdict hero */}
          <div className={cn("rounded-xl border px-4 py-3", verdictTone(verdict))}>
            <div className="flex items-center gap-2.5">
              {verdictIcon(verdict, "size-5 shrink-0")}
              <span className="text-base font-medium">{verdictLabel}</span>
              {evidence?.captured_at && (
                <span className="ml-auto text-[11px] text-muted-foreground">
                  {t(($) => $.qa_evidence.captured)} {new Date(evidence.captured_at).toLocaleString()}
                </span>
              )}
            </div>
            {evidence?.summary && (
              <p className="mt-1.5 text-[12px] text-muted-foreground">{evidence.summary}</p>
            )}
          </div>

          {/* Checks */}
          {evidence?.result && evidence.result.commands.length > 0 ? (
            <section>
              <div className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
                {t(($) => $.qa_review.checks)}
              </div>
              <div className="rounded-lg border">
                <StructuredResult result={evidence.result} />
              </div>
            </section>
          ) : (
            <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
              <p className="text-[12px] text-muted-foreground">{t(($) => $.qa_evidence.empty)}</p>
              <p className="mt-0.5 text-[11px] text-muted-foreground/70">{t(($) => $.qa_evidence.empty_hint)}</p>
            </div>
          )}

          {/* Triage bar — the QA team's verdict + actions. */}
          <div className="flex flex-wrap items-center gap-2 border-t pt-4">
            <Button
              type="button"
              size="sm"
              className={cn(
                "h-8 gap-1.5 text-[12px]",
                humanVerdict === "pass" && "border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-300",
              )}
              variant="outline"
              disabled={setVerdict.isPending}
              onClick={() => setVerdict.mutate("pass")}
            >
              <CheckCircle2 className="size-3.5" />
              {t(($) => $.qa_review.pass)}
            </Button>
            <Button
              type="button"
              size="sm"
              className={cn(
                "h-8 gap-1.5 text-[12px]",
                humanVerdict === "fail" && "border-destructive/40 bg-destructive/10 text-destructive",
              )}
              variant="outline"
              disabled={setVerdict.isPending}
              onClick={() => setVerdict.mutate("fail")}
            >
              <XCircle className="size-3.5" />
              {t(($) => $.qa_review.fail)}
            </Button>
            {verdict === "fail" && (
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-8 gap-1.5 text-[12px]"
                onClick={() => setBugOpen(true)}
              >
                <Bug className="size-3.5 text-destructive" />
                {t(($) => $.qa_bug.file_bug)}
              </Button>
            )}
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="ml-auto h-8 gap-1.5 text-[12px]"
              disabled={rerun.isPending}
              onClick={() => rerun.mutate()}
            >
              {rerun.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
              {t(($) => $.qa_evidence.rerun)}
            </Button>
          </div>

          {/* Test cases — the QA team's instruments (author / generate / run). */}
          <TestCasesPanel issueId={issueId} />

          <FileBugSheet
            open={bugOpen}
            onOpenChange={setBugOpen}
            sourceId={issueId}
            sourceTitle={issue.title}
            identifier={issue.identifier}
            projectId={issue.project_id}
            evidence={evidence}
          />
        </>
      )}
    </div>
  );
}
