import type { ReactNode } from "react";
import { CheckCircle2, XCircle, ShieldQuestion, Loader2 } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";

// The single source of truth for QA verdict visuals — reduced (Phase B) to
// FOUR buckets so every QA surface speaks one plain vocabulary and one color
// per state instead of five colors across seven near-synonyms:
//   pass    → green check   ("Passed")
//   fail    → red   cross   ("Failed" / "Couldn't run")
//   running → blue  spinner ("Testing…")
//   pending → grey  shield  ("Not tested yet")
//
// The backend's richer reconciled enum (service.ReconcileQAState) still flows
// through here unchanged; each extra value is FOLDED onto one of the four
// buckets so the headline stays a single word + single color. The fuller
// distinction is preserved at the call site as a muted secondary line /
// tooltip, never as a competing loud color:
//   pass_with_failing_cases, stale → pass    (still a pass; the caveat is a
//                                             muted note, not amber)
//   blocked                        → fail    ("couldn't run" reads as
//                                             not-passing)
//   never_ran / "" / any unknown   → pending (enum-drift-safe default)
// Nothing crashes on an unrecognized value — it downgrades to pending.

export type VerdictBucket = "pass" | "fail" | "running" | "pending";

// Fold any verdict / reconciled-state string onto one of the four buckets.
// The default arm is the enum-drift guard: an old-server "" or a future
// enum member this build doesn't know renders as pending, never broken.
export function verdictBucket(verdict: string): VerdictBucket {
  switch (verdict) {
    case "pass":
    case "pass_with_failing_cases":
    case "stale":
      return "pass";
    case "fail":
    case "blocked":
      return "fail";
    case "running":
      return "running";
    default:
      return "pending";
  }
}

export function verdictIcon(verdict: string, className: string): ReactNode {
  switch (verdictBucket(verdict)) {
    case "pass":
      return <CheckCircle2 className={cn("text-emerald-600 dark:text-emerald-400", className)} />;
    case "fail":
      return <XCircle className={cn("text-destructive", className)} />;
    case "running":
      return <Loader2 className={cn("text-info animate-spin", className)} />;
    default:
      return <ShieldQuestion className={cn("text-muted-foreground", className)} />;
  }
}

// Hero/card tint (border + bg) for a verdict block — one tint per bucket.
export function verdictTone(verdict: string): string {
  switch (verdictBucket(verdict)) {
    case "pass":
      return "border-emerald-600/30 bg-emerald-600/5";
    case "fail":
      return "border-destructive/30 bg-destructive/5";
    case "running":
      return "border-info/30 bg-info/5";
    default:
      return "border-border bg-muted/20";
  }
}
