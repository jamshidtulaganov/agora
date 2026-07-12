import type { ReactNode } from "react";
import { CheckCircle2, XCircle, ShieldQuestion, CircleSlash, RefreshCw, Loader2 } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";

// The single source of truth for QA verdict visuals — icon + hero tint. Every QA
// surface (review page, file-bug sheet, bugs lens) imports these so the pass /
// fail / pending vocabulary stays consistent instead of being re-derived per
// component. `pass` uses the emerald success tint the codebase already
// standardizes (qa-result CMD_KIND_STYLE); fail uses the destructive token;
// pending is neutral muted.
//
// Phase 2 (reconciled QA state — service.ReconcileQAState) added four more
// recognized values so the chip can render the server's fuller enum instead
// of collapsing everything non-pass/fail into one generic "pending":
// "pass_with_failing_cases" (a qa:pass label sitting on a known-failing case
// — amber, NOT a clean pass), "blocked", "stale", and "running". Colors
// mirror qa-lane.tsx's STATE_BADGE so a state reads the same tone on every
// QA surface. Any other string (including "", "never_ran", or an unrecognized
// future value) falls through to the original muted/ShieldQuestion pending
// look — enum drift downgrades, it never crashes or looks broken.

export function verdictIcon(verdict: string, className: string): ReactNode {
  if (verdict === "pass") {
    return <CheckCircle2 className={cn("text-emerald-600 dark:text-emerald-400", className)} />;
  }
  if (verdict === "pass_with_failing_cases") {
    return <CheckCircle2 className={cn("text-amber-600 dark:text-amber-400", className)} />;
  }
  if (verdict === "fail") {
    return <XCircle className={cn("text-destructive", className)} />;
  }
  if (verdict === "blocked") {
    return <CircleSlash className={cn("text-amber-600 dark:text-amber-400", className)} />;
  }
  if (verdict === "stale") {
    return <RefreshCw className={cn("text-amber-600 dark:text-amber-400", className)} />;
  }
  if (verdict === "running") {
    return <Loader2 className={cn("text-info animate-spin", className)} />;
  }
  return <ShieldQuestion className={cn("text-muted-foreground", className)} />;
}

// Hero/card tint (border + bg) for a verdict block.
export function verdictTone(verdict: string): string {
  if (verdict === "fail") return "border-destructive/30 bg-destructive/5";
  if (verdict === "pass") return "border-emerald-600/30 bg-emerald-600/5";
  if (verdict === "pass_with_failing_cases" || verdict === "blocked" || verdict === "stale") {
    return "border-amber-500/30 bg-amber-500/5";
  }
  if (verdict === "running") return "border-info/30 bg-info/5";
  return "border-border bg-muted/20";
}
