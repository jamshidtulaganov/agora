"use client";

import { useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Bug, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import type { QAEvidence, IssuePriority } from "@agora/core/types";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetFooter,
} from "@agora/ui/components/ui/sheet";
import { Input } from "@agora/ui/components/ui/input";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@agora/ui/components/ui/select";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { StructuredResult } from "../../issues/components/editor-tests-panel";

const SEVERITY: { value: string; priority: IssuePriority }[] = [
  { value: "blocker", priority: "urgent" },
  { value: "major", priority: "high" },
  { value: "minor", priority: "medium" },
];

// One click turns a FAIL verdict's frozen evidence into a tracked bug: a child
// issue (parent_issue_id back-link) pre-filled with the failing checks, labelled
// `bug`, so the repro never rots into copy-paste. The bug is an ordinary issue,
// so it gets its OWN auto-QA gate — a repro can't be hand-waved closed.
export function FileBugSheet({
  open,
  onOpenChange,
  sourceId,
  sourceTitle,
  identifier,
  projectId,
  evidence,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  sourceId: string;
  sourceTitle: string;
  identifier: string;
  projectId: string | null | undefined;
  evidence: QAEvidence | null | undefined;
}) {
  const { t } = useT("issues");
  const wp = useWorkspacePaths();
  const nav = useNavigation();

  const newFailures = useMemo(
    () => (evidence?.result?.commands ?? []).filter((c) => c.kind === "new_failure"),
    [evidence],
  );
  const seedTitle = useMemo(() => {
    const lead = newFailures[0]?.cmd || evidence?.summary || "";
    const base = lead ? `Bug: ${lead}` : "Bug";
    return `${base} — ${sourceTitle}`.slice(0, 160);
  }, [newFailures, evidence, sourceTitle]);

  const [title, setTitle] = useState(seedTitle);
  const [severity, setSeverity] = useState("major");

  const description = useMemo(() => {
    const lines = [`Filed from [${identifier}] — QA verdict: **${evidence?.verdict ?? "fail"}**.`];
    if (evidence?.summary) lines.push("", evidence.summary);
    if (newFailures.length > 0) {
      lines.push("", "New failures:");
      for (const c of newFailures) lines.push(`- \`${c.cmd}\` (baseline ${c.baseline_exit ?? "—"} → branch ${c.branch_exit})`);
    }
    if (evidence?.branch_sha || evidence?.baseline_ref) {
      lines.push("", `_branch ${evidence.branch_sha || "—"} vs baseline ${evidence.baseline_ref || "merge-base"}_`);
    }
    return lines.join("\n");
  }, [identifier, evidence, newFailures]);

  const file = useMutation({
    mutationFn: async () => {
      const priority = SEVERITY.find((s) => s.value === severity)?.priority ?? "high";
      const bug = await api.createIssue({
        title: title.trim() || seedTitle,
        description,
        priority,
        project_id: projectId ?? undefined,
        parent_issue_id: sourceId,
      });
      // Ensure the `bug` label exists in this workspace, then attach it.
      const labels = await api.listLabels();
      const existing = labels.labels.find((l) => l.name === "bug");
      const labelId = existing?.id ?? (await api.createLabel({ name: "bug", color: "#ef4444" })).id;
      await api.attachLabel(bug.id, labelId);
      return bug;
    },
    onSuccess: (bug) => {
      onOpenChange(false);
      toast.success(t(($) => $.qa_bug.filed_toast), {
        action: { label: t(($) => $.qa_bug.open_bug), onClick: () => nav.push(wp.issueDetail(bug.id)) },
      });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex flex-col gap-0 sm:max-w-md">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2 text-base font-medium">
            <Bug className="size-4 text-destructive" />
            {t(($) => $.qa_bug.file_bug)}
          </SheetTitle>
          <SheetDescription className="text-[12px] text-muted-foreground">
            {t(($) => $.qa_bug.from_evidence)} <span className="font-mono">{identifier}</span>
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-1 flex-col gap-5 overflow-y-auto px-4 py-4">
          <div>
            <div className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.qa_bug.title_label)}
            </div>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} className="h-8 text-[13px]" />
          </div>

          <div>
            <div className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.qa_bug.severity_label)}
            </div>
            <Select value={severity} onValueChange={(v) => setSeverity(v ?? "major")}>
              <SelectTrigger className="h-8 text-[13px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SEVERITY.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {s.value === "blocker"
                      ? t(($) => $.qa_bug.sev_blocker)
                      : s.value === "major"
                        ? t(($) => $.qa_bug.sev_major)
                        : t(($) => $.qa_bug.sev_minor)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
                {t(($) => $.qa_bug.evidence_label)}
              </span>
              <Badge variant="secondary" className="text-[10px] font-normal">
                {t(($) => $.qa_bug.frozen)}
              </Badge>
            </div>
            {evidence?.result && newFailures.length > 0 ? (
              <div className="rounded-lg border">
                <StructuredResult result={{ ...evidence.result, commands: newFailures }} />
              </div>
            ) : (
              <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center text-[12px] text-muted-foreground">
                {t(($) => $.qa_bug.no_evidence)}
              </div>
            )}
          </div>
        </div>

        <SheetFooter className="flex-row items-center gap-2 border-t px-4 py-3">
          <Button type="button" variant="outline" size="sm" className="h-8 text-[12px]" onClick={() => onOpenChange(false)}>
            {t(($) => $.qa_bug.cancel)}
          </Button>
          <Button
            type="button"
            size="sm"
            className="ml-auto h-8 gap-1.5 text-[12px]"
            disabled={file.isPending}
            onClick={() => file.mutate()}
          >
            {file.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Bug className="size-3.5" />}
            {t(($) => $.qa_bug.file_bug)}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
