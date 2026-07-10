"use client";

import { useState, type ReactNode } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ShieldCheck, Plus, Sparkles, Loader2, Archive, CircleSlash, Bot, User, Pencil } from "lucide-react";
import { api } from "@agora/core/api";
import type { TestCase } from "@agora/core/types";
import { useWorkspaceId } from "@agora/core/hooks";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { verdictIcon } from "./verdict";

// The project's STANDING regression suite — the "stoppage" release gate. These
// are the test cases with issue_id NULL (GET /api/projects/:id/test-cases): the
// golden-path cases injected into every run_qa / run_test_cases on the project's
// issues, and re-run whole-branch at sprint end. A failing base case is a
// regression that must block a release. QA manages the suite here — build it
// from the QA manifest, add standing cases by hand, and archive ones that no
// longer hold.

export function QASuiteView({ projectId }: { projectId?: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);

  const queryKey = ["qa-suite", wsId, projectId ?? "none"];
  const { data, isLoading } = useQuery({
    // wsId in the key: the list scopes by the ambient workspace header, so a
    // switch without it would serve the previous workspace's cached suite.
    queryKey,
    queryFn: () => api.listProjectTestCases(projectId as string),
    enabled: !!projectId,
    staleTime: 30_000,
  });
  const invalidate = () => qc.invalidateQueries({ queryKey });

  const build = useMutation({
    mutationFn: () => api.buildProjectBaseSuite(projectId as string),
    onSuccess: () => toast.success(t(($) => $.qa_cockpit.suite_toast_build_queued)),
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.qa_cockpit.suite_toast_build_failed)),
  });

  const archive = useMutation({
    mutationFn: (caseId: string) => api.archiveTestCase(caseId),
    onSuccess: () => {
      toast.success(t(($) => $.qa_cockpit.suite_toast_archived));
      invalidate();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.qa_cockpit.suite_toast_archive_failed)),
  });

  const update = useMutation({
    mutationFn: ({ caseId, data }: { caseId: string; data: import("@agora/core/types").UpdateTestCaseRequest }) =>
      api.updateTestCase(caseId, data),
    onSuccess: () => {
      toast.success(t(($) => $.qa_cockpit.suite_toast_updated));
      invalidate();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.qa_cockpit.suite_toast_update_failed)),
  });

  // Per-project gate: base cases belong to ONE project (issue_id NULL, project
  // scoped), so there is nothing to manage until a project is chosen.
  if (!projectId) {
    return (
      <div className="mx-auto max-w-md px-8 py-16 text-center">
        <ShieldCheck className="mx-auto size-6 text-muted-foreground/60" />
        <p className="mt-2 text-sm text-muted-foreground">{t(($) => $.qa_cockpit.suite_select_project)}</p>
        <p className="mt-1 text-[12px] text-muted-foreground/70">{t(($) => $.qa_cockpit.suite_select_project_hint)}</p>
      </div>
    );
  }

  const cases = data?.test_cases ?? [];
  const negativeCount = cases.filter((c) => c.category === "negative").length;
  const positiveCount = cases.length - negativeCount;
  const failingCount = cases.filter((c) => c.latest_run?.status === "fail").length;

  return (
    // One consistent bounded measure for the whole tab (header + rows share it)
    // instead of a header capped at max-w-2xl while the rows below stretch full
    // width — matches issue-detail's max-w-4xl reading measure.
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 px-8 py-8">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1.5">
          <div className="flex items-center gap-2">
            <ShieldCheck className="size-4 shrink-0 text-emerald-500" />
            <span className="text-sm font-medium">{t(($) => $.qa_cockpit.suite_heading)}</span>
            <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{cases.length}</span>
            {failingCount > 0 && (
              <span className="rounded bg-destructive/10 px-1.5 py-0.5 text-[11px] font-medium text-destructive">
                {t(($) => $.qa_cockpit.suite_failing_count, { count: failingCount })}
              </span>
            )}
          </div>
          <p className="text-sm text-muted-foreground">{t(($) => $.qa_cockpit.suite_description)}</p>
          {cases.length > 0 && (
            <p className="text-[11px] text-muted-foreground">
              {t(($) => $.qa_cockpit.suite_positive_count, { count: positiveCount })}
              {" · "}
              <span className={cn(negativeCount === 0 && "font-medium text-amber-600 dark:text-amber-400")}>
                {t(($) => $.qa_cockpit.suite_negative_count, { count: negativeCount })}
              </span>
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-8 gap-1.5 text-[12px]"
            disabled={build.isPending}
            onClick={() => build.mutate()}
            title={t(($) => $.qa_cockpit.suite_build_title)}
          >
            {build.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Sparkles className="size-3.5" />}
            {t(($) => $.qa_cockpit.suite_build)}
          </Button>
          <Button type="button" size="sm" className="h-8 gap-1.5 text-[12px]" onClick={() => setAdding((v) => !v)}>
            <Plus className="size-3.5" />
            {t(($) => $.qa_cockpit.suite_add_case)}
          </Button>
        </div>
      </div>

      {adding && (
        <AddBaseCaseForm
          projectId={projectId}
          onDone={() => {
            setAdding(false);
            invalidate();
          }}
          onCancel={() => setAdding(false)}
        />
      )}

      {isLoading && !data ? (
        <div className="space-y-2" aria-hidden>
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-3/4" />
        </div>
      ) : cases.length === 0 && !adding ? (
        <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-12 text-center">
          <ShieldCheck className="mx-auto size-6 text-muted-foreground/60" />
          <p className="mt-2 text-sm text-muted-foreground">{t(($) => $.qa_cockpit.suite_empty_title)}</p>
          <p className="mx-auto mt-1 max-w-md text-[12px] text-muted-foreground/70">
            {t(($) => $.qa_cockpit.suite_empty_hint)}
          </p>
        </div>
      ) : (
        <ul className="divide-y rounded-lg border">
          {cases.map((c) => (
            <BaseCaseRow
              key={c.id}
              c={c}
              onArchive={() => archive.mutate(c.id)}
              archiving={archive.isPending && archive.variables === c.id}
              onSave={(data) => update.mutate({ caseId: c.id, data })}
              saving={update.isPending && update.variables?.caseId === c.id}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function CasePill({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("rounded border px-1 py-px text-[11px] uppercase tracking-wide", className)}>{children}</span>
  );
}

function BaseCaseRow({
  c,
  onArchive,
  archiving,
  onSave,
  saving,
}: {
  c: TestCase;
  onArchive: () => void;
  archiving: boolean;
  onSave: (data: import("@agora/core/types").UpdateTestCaseRequest) => void;
  saving: boolean;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState({ title: c.title, steps: c.steps, expected: c.expected });
  const status = c.latest_run?.status;
  const isBlocked = status === "blocked" || status === "skip";
  const hasDetail = !!(c.steps || c.expected || (status === "fail" && c.latest_run?.output));
  const kindLabel = c.kind === "automated" ? t(($) => $.test_cases.kind_automated) : t(($) => $.test_cases.kind_manual);
  const categoryLabel =
    c.category === "negative" ? t(($) => $.test_cases.category_negative) : t(($) => $.test_cases.category_positive);

  return (
    <li className="group/case px-4 py-2.5">
      <div className="flex items-start gap-3">
        <button
          type="button"
          className={cn("min-w-0 flex-1 text-left", !hasDetail && "cursor-default")}
          onClick={() => hasDetail && setOpen((v) => !v)}
        >
          <span className="block truncate text-[13px]">{c.title}</span>
          <span className="mt-0.5 flex flex-wrap items-center gap-1 text-[10px] text-muted-foreground">
            <CasePill>{kindLabel}</CasePill>
            <CasePill className={cn(c.category === "negative" && "border-amber-500/40 text-amber-600 dark:text-amber-400")}>
              {categoryLabel}
            </CasePill>
            {c.script ? <CasePill>{t(($) => $.qa_cockpit.suite_compiled)}</CasePill> : null}
            {c.latest_run && (
              <span className="flex items-center gap-0.5">
                {c.latest_run.run_source === "agent" ? <Bot className="size-2.5" /> : <User className="size-2.5" />}
              </span>
            )}
          </span>
        </button>
        <div className="flex shrink-0 items-center gap-1.5 pt-0.5">
          {status === "pass" || status === "fail" ? (
            <span title={status === "fail" ? c.latest_run?.output || undefined : undefined}>
              {verdictIcon(status, "size-4")}
            </span>
          ) : isBlocked ? (
            <span title={c.latest_run?.output || t(($) => $.test_cases.blocked)}>
              <CircleSlash className="size-4 text-amber-600 dark:text-amber-400" />
            </span>
          ) : (
            <span className="text-[10px] text-muted-foreground">—</span>
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-6 text-muted-foreground opacity-0 transition-opacity focus-within:opacity-100 hover:text-foreground group-hover/case:opacity-100"
            onClick={() => {
              setDraft({ title: c.title, steps: c.steps, expected: c.expected });
              setEditing((v) => !v);
            }}
            title={t(($) => $.qa_cockpit.suite_edit_title)}
          >
            <Pencil className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-6 text-muted-foreground opacity-0 transition-opacity focus-within:opacity-100 hover:text-destructive group-hover/case:opacity-100"
            disabled={archiving}
            onClick={onArchive}
            title={t(($) => $.qa_cockpit.suite_archive_title)}
          >
            {archiving ? <Loader2 className="size-3.5 animate-spin" /> : <Archive className="size-3.5" />}
          </Button>
        </div>
      </div>
      {editing && (
        <div className="mt-2 space-y-2 rounded-md border bg-muted/20 p-2.5">
          <Input
            value={draft.title}
            onChange={(e) => setDraft((d) => ({ ...d, title: e.target.value }))}
            className="h-8 text-[13px]"
            placeholder={t(($) => $.qa_cockpit.suite_case_title_ph)}
          />
          <Textarea
            value={draft.steps}
            onChange={(e) => setDraft((d) => ({ ...d, steps: e.target.value }))}
            rows={2}
            className="text-[12px]"
            placeholder={t(($) => $.qa_cockpit.suite_steps_ph)}
          />
          <Textarea
            value={draft.expected}
            onChange={(e) => setDraft((d) => ({ ...d, expected: e.target.value }))}
            rows={1}
            className="text-[12px]"
            placeholder={t(($) => $.qa_cockpit.suite_expected_ph)}
          />
          <div className="flex items-center justify-end gap-1.5">
            <Button type="button" variant="ghost" size="sm" className="h-7 text-[12px]" onClick={() => setEditing(false)}>
              {t(($) => $.qa_cockpit.suite_cancel)}
            </Button>
            <Button
              type="button"
              size="sm"
              className="h-7 text-[12px]"
              disabled={saving || !draft.title.trim()}
              onClick={() => {
                onSave({ title: draft.title.trim(), steps: draft.steps, expected: draft.expected });
                setEditing(false);
              }}
            >
              {saving ? <Loader2 className="size-3.5 animate-spin" /> : t(($) => $.qa_cockpit.suite_save)}
            </Button>
          </div>
        </div>
      )}
      {open && hasDetail && !editing && (
        <div className="mt-1.5 space-y-1 pl-1 text-[12px] text-muted-foreground">
          {status === "fail" && c.latest_run?.output && (
            <pre className="whitespace-pre-wrap break-words rounded border-l-2 border-destructive/50 bg-destructive/5 px-2 py-1.5 font-mono text-[11px] text-destructive/90">
              {c.latest_run.output}
            </pre>
          )}
          {c.steps && <pre className="whitespace-pre-wrap font-sans">{c.steps}</pre>}
          {c.expected && (
            <p>
              <span className="text-foreground/70">→ </span>
              {c.expected}
            </p>
          )}
        </div>
      )}
    </li>
  );
}

function AddBaseCaseForm({
  projectId,
  onDone,
  onCancel,
}: {
  projectId: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const { t } = useT("issues");
  const [title, setTitle] = useState("");
  const [steps, setSteps] = useState("");
  const [expected, setExpected] = useState("");
  // Base cases default to "automated" — only automated cases are injected into
  // run_qa / run_test_cases (a manual base case is inert), mirroring the server.
  const [kind, setKind] = useState<"manual" | "automated">("automated");
  const [category, setCategory] = useState<"positive" | "negative">("positive");

  const save = useMutation({
    mutationFn: () =>
      api.createProjectTestCase(projectId, { title: title.trim(), steps, expected, kind, category }),
    onSuccess: onDone,
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.qa_cockpit.suite_toast_add_failed)),
  });

  return (
    <div className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <Input
        placeholder={t(($) => $.qa_cockpit.suite_new_case_title_ph)}
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        className="h-8 text-[13px]"
      />
      <Textarea
        placeholder={t(($) => $.qa_cockpit.suite_new_steps_ph)}
        value={steps}
        onChange={(e) => setSteps(e.target.value)}
        rows={2}
        className="text-[12px]"
      />
      <Textarea
        placeholder={t(($) => $.qa_cockpit.suite_expected_ph)}
        value={expected}
        onChange={(e) => setExpected(e.target.value)}
        rows={1}
        className="text-[12px]"
      />
      <div className="flex flex-wrap items-center gap-1.5">
        <div className="flex rounded-md border p-0.5">
          {(["automated", "manual"] as const).map((k) => (
            <Button
              key={k}
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setKind(k)}
              className={cn(
                "h-6 px-2 text-[11px]",
                kind === k ? "bg-muted font-medium text-foreground" : "text-muted-foreground",
              )}
            >
              {k === "automated" ? t(($) => $.test_cases.kind_automated) : t(($) => $.test_cases.kind_manual)}
            </Button>
          ))}
        </div>
        <div className="flex rounded-md border p-0.5">
          {(["positive", "negative"] as const).map((cat) => (
            <Button
              key={cat}
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setCategory(cat)}
              className={cn(
                "h-6 px-2 text-[11px]",
                category === cat ? "bg-muted font-medium text-foreground" : "text-muted-foreground",
              )}
            >
              {cat === "positive" ? t(($) => $.test_cases.category_positive) : t(($) => $.test_cases.category_negative)}
            </Button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <Button type="button" variant="ghost" size="sm" className="h-7 text-[12px]" onClick={onCancel}>
            {t(($) => $.qa_cockpit.suite_cancel)}
          </Button>
          <Button
            type="button"
            size="sm"
            className="h-7 gap-1.5 text-[12px]"
            disabled={!title.trim() || save.isPending}
            onClick={() => save.mutate()}
          >
            {save.isPending ? <Loader2 className="size-3.5 animate-spin" /> : t(($) => $.qa_cockpit.suite_add_case)}
          </Button>
        </div>
      </div>
    </div>
  );
}
