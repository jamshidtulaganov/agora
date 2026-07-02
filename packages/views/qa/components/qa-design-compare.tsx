"use client";

import type { QADesignResult } from "@agora/core/types";
import { useT } from "../../i18n";

// Renders the advisory design-verification result of a run_qa verdict: the
// pass/fail/skipped badge, the Figma reference node, and the deterministic
// mismatch table (kind · selector · expected → actual). Reused inside the QA
// review page and the issue-detail QA section. Renders nothing when the issue
// carried no design result.
const VERDICT_STYLE: Record<string, string> = {
  pass: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  fail: "bg-destructive/15 text-destructive",
  skipped: "bg-muted text-muted-foreground",
};

export function QADesignCompare({ design }: { design: QADesignResult | null | undefined }) {
  const { t } = useT("issues");
  if (!design) return null;

  const mismatchKindLabel = (kind: string): string => {
    switch (kind) {
      case "color":
        return t(($) => $.qa_review.mismatch_kind_color);
      case "typography":
        return t(($) => $.qa_review.mismatch_kind_typography);
      case "spacing":
        return t(($) => $.qa_review.mismatch_kind_spacing);
      case "layout":
        return t(($) => $.qa_review.mismatch_kind_layout);
      case "missing_element":
        return t(($) => $.qa_review.mismatch_kind_missing_element);
      default:
        return t(($) => $.qa_review.mismatch_kind_generic);
    }
  };

  const verdictLabel =
    design.verdict === "pass"
      ? t(($) => $.qa_review.design_pass)
      : design.verdict === "fail"
        ? t(($) => $.qa_review.design_fail)
        : t(($) => $.qa_review.design_skipped);

  return (
    <section>
      <div className="mb-1.5 flex items-center gap-2 text-[11px] uppercase tracking-wide text-muted-foreground">
        {t(($) => $.qa_review.design_heading)}
        <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium normal-case ${VERDICT_STYLE[design.verdict] ?? VERDICT_STYLE.skipped}`}>
          {verdictLabel}
        </span>
        {design.reference_node && (
          <span className="font-mono text-[10px] normal-case text-muted-foreground/70">{design.reference_node}</span>
        )}
      </div>

      {/* Figma-compare status only when there's a real compare (reference node
          or mismatches); a lint-only result skips this so it doesn't read as
          "Figma unreachable". */}
      {design.verdict === "skipped" && (design.reference_node || (design.lint ?? []).length === 0) ? (
        <p className="rounded-lg border border-dashed bg-muted/20 px-3 py-2 text-[11px] text-muted-foreground">
          {t(($) => $.qa_review.design_skipped_reason)}
        </p>
      ) : design.verdict !== "skipped" && design.mismatches.length === 0 ? (
        <p className="rounded-lg border bg-muted/10 px-3 py-2 text-[11px] text-muted-foreground">
          {t(($) => $.qa_review.design_no_mismatches)}
        </p>
      ) : design.mismatches.length > 0 ? (
        <ul className="divide-y divide-border rounded-lg border">
          {design.mismatches.map((m, i) => (
            <li key={i} className="flex flex-col gap-0.5 px-3 py-1.5 text-[11px]">
              <div className="flex items-center gap-1.5">
                <span className="rounded bg-amber-500/15 px-1 py-0.5 text-[9px] font-medium uppercase text-amber-600 dark:text-amber-400">
                  {mismatchKindLabel(m.kind)}
                </span>
                {m.selector && <code className="truncate text-muted-foreground">{m.selector}</code>}
              </div>
              <div className="text-muted-foreground">
                <span className="text-emerald-600 dark:text-emerald-400">{m.expected}</span>
                {" → "}
                <span className="text-destructive">{m.actual}</span>
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      {/* Diff-scoped design-system lint — what the change eroded. */}
      {design.lint && design.lint.length > 0 && (
        <div className="mt-2">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.qa_review.design_lint_heading)}
          </div>
          <ul className="divide-y divide-border rounded-lg border">
            {design.lint.map((l, i) => (
              <li key={i} className="flex flex-col gap-0.5 px-3 py-1.5 text-[11px]">
                <div className="flex items-center gap-1.5">
                  <span
                    className={`rounded px-1 py-0.5 text-[9px] font-medium uppercase ${
                      l.severity === "block"
                        ? "bg-destructive/15 text-destructive"
                        : "bg-amber-500/15 text-amber-600 dark:text-amber-400"
                    }`}
                  >
                    {l.severity === "block" ? t(($) => $.qa_review.design_lint_block) : t(($) => $.qa_review.design_lint_warn)}
                  </span>
                  {l.where && <code className="truncate text-muted-foreground">{l.where}</code>}
                </div>
                {l.issue && <span className="text-muted-foreground">{l.issue}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
