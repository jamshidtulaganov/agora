/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useQuery } from "@tanstack/react-query";
import { GitMerge, Check, X, Clock } from "lucide-react";
import { api } from "@agora/core/api";
import { cn } from "@agora/ui/lib/utils";

// Merge-readiness gates — surfaces the EXISTING deterministic gate spine
// (MergeReadiness: ci/qa/security/code-review verdicts from labels) inside the
// editor's review bar, so the human sees what still blocks merge right next to
// Accept→PR. Read-only; polled. Renders nothing until gates exist. Plain names
// only — the internal tier ("trivial/light/full") is never surfaced.

// Backend gate slugs → short plain labels for the tight editor strip.
function gateLabel(name: string): string {
  switch (name) {
    case "ci":
      return "CI";
    case "qa":
      return "QA";
    case "security":
      return "Security";
    case "review":
    case "code-review":
      return "Review";
    default:
      return name;
  }
}

function gateIcon(status: string) {
  if (status === "pass") return <Check className="h-2.5 w-2.5" />;
  if (status === "fail") return <X className="h-2.5 w-2.5" />;
  return <Clock className="h-2.5 w-2.5" />;
}

function gateClass(status: string) {
  if (status === "pass") return "text-emerald-600 dark:text-emerald-400";
  if (status === "fail") return "text-destructive";
  return "text-muted-foreground";
}

export function EditorGates({ issueId }: { issueId: string }) {
  const { data } = useQuery({
    queryKey: ["merge-readiness", issueId],
    queryFn: () => api.mergeReadiness(issueId),
    enabled: !!issueId,
    refetchInterval: 15000,
  });

  if (!data || data.gates.length === 0) return null;

  const blocking = data.gates.filter((g) => g.status !== "pass").length;

  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px]"
      title={data.blocked?.length ? data.blocked.join(" · ") : undefined}
    >
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        <GitMerge className="h-3 w-3 shrink-0" />
        Merge
      </span>
      {data.gates.map((g) => (
        <span
          key={g.name}
          className={cn("inline-flex items-center gap-0.5", gateClass(g.status))}
        >
          {gateLabel(g.name)}
          {gateIcon(g.status)}
        </span>
      ))}
      <span
        className={cn(
          "ml-auto font-medium",
          data.ready
            ? "text-emerald-600 dark:text-emerald-400"
            : "text-amber-600 dark:text-amber-400",
        )}
      >
        {data.ready ? "ready to merge" : `${blocking} blocking`}
      </span>
    </div>
  );
}
