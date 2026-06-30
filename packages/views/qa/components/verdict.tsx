import type { ReactNode } from "react";
import { CheckCircle2, XCircle, ShieldQuestion } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";

// The single source of truth for QA verdict visuals — icon + hero tint. Every QA
// surface (review page, file-bug sheet, bugs lens) imports these so the pass /
// fail / pending vocabulary stays consistent instead of being re-derived per
// component. `pass` uses the emerald success tint the codebase already
// standardizes (editor-tests-panel CMD_KIND_STYLE); fail uses the destructive
// token; pending is neutral muted.

export function verdictIcon(verdict: string, className: string): ReactNode {
  if (verdict === "pass") {
    return <CheckCircle2 className={cn("text-emerald-600 dark:text-emerald-400", className)} />;
  }
  if (verdict === "fail") {
    return <XCircle className={cn("text-destructive", className)} />;
  }
  return <ShieldQuestion className={cn("text-muted-foreground", className)} />;
}

// Hero/card tint (border + bg) for a verdict block.
export function verdictTone(verdict: string): string {
  if (verdict === "fail") return "border-destructive/30 bg-destructive/5";
  if (verdict === "pass") return "border-emerald-600/30 bg-emerald-600/5";
  return "border-border bg-muted/20";
}
