import type { LucideIcon } from "lucide-react";
import { Loader2, ShieldAlert, ShieldCheck } from "lucide-react";

// Shared classification of a sprint regression gate's server status — one
// place that maps the status string to done/failed/running plus the icon and
// tint every surface renders. Used by the Ship view's RegressionGate chip and
// the health strip's glyph so a new backend status value (e.g. "cancelled")
// degrades identically in both instead of silently drifting apart.
// Unknown statuses fall through to "running" (spinner) — a neutral downgrade,
// never a crash (enum drift rule).
export function regressionStatusMeta(status: string): {
  done: boolean;
  failed: boolean;
  running: boolean;
  Icon: LucideIcon;
  className: string;
} {
  const done = status === "completed" || status === "succeeded";
  const failed = status === "failed" || status === "error";
  return {
    done,
    failed,
    running: !done && !failed,
    Icon: failed ? ShieldAlert : done ? ShieldCheck : Loader2,
    className: failed ? "text-destructive" : done ? "text-emerald-500" : "text-muted-foreground",
  };
}
