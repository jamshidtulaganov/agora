"use client";

import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

// Test-case metadata controls + chips (phase 2: priority / modality), shared
// by the issue Test-cases panel's AddCaseForm and the Suite tab's add/edit
// forms so the two panels don't grow parallel segmented controls. Same visual
// pattern as the existing kind/category toggles in those forms.

export const TEST_CASE_PRIORITIES = ["p1", "p2", "p3"] as const;
export type TestCasePriority = (typeof TEST_CASE_PRIORITIES)[number];

export const TEST_CASE_MODALITIES = ["ui", "api", "unit", "manual"] as const;
export type TestCaseModality = (typeof TEST_CASE_MODALITIES)[number] | "";

// priorityRank orders cases for the panel's secondary sort: p1 first, then
// p2, then p3. Unknown/drifted values rank with p2 (the server-side default).
export function priorityRank(priority: string): number {
  switch (priority) {
    case "p1":
      return 0;
    case "p3":
      return 2;
    default:
      return 1;
  }
}

export function usePriorityLabel(): (p: string) => string {
  const { t } = useT("issues");
  return (p: string) => {
    switch (p) {
      case "p1":
        return t(($) => $.test_cases.priority_p1);
      case "p3":
        return t(($) => $.test_cases.priority_p3);
      default:
        return t(($) => $.test_cases.priority_p2);
    }
  };
}

export function useModalityLabel(): (m: string) => string {
  const { t } = useT("issues");
  return (m: string) => {
    switch (m) {
      case "ui":
        return t(($) => $.test_cases.modality_ui);
      case "api":
        return t(($) => $.test_cases.modality_api);
      case "unit":
        return t(($) => $.test_cases.modality_unit);
      case "manual":
        return t(($) => $.test_cases.modality_manual);
      default:
        return "";
    }
  };
}

// The p1 chip gets destructive-tinted emphasis — a p1 case failing is the
// first thing a reviewer must see. p2/p3 stay muted.
export function priorityChipClass(priority: string): string {
  return priority === "p1" ? "font-medium text-destructive" : "";
}

export function PriorityToggle({
  value,
  onChange,
}: {
  value: TestCasePriority;
  onChange: (p: TestCasePriority) => void;
}) {
  const priorityLabel = usePriorityLabel();
  return (
    <div className="flex rounded-md border p-0.5">
      {TEST_CASE_PRIORITIES.map((p) => (
        <Button
          key={p}
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onChange(p)}
          className={cn(
            "h-6 px-2 text-[11px]",
            value === p ? "bg-muted font-medium text-foreground" : "text-muted-foreground",
            value === p && p === "p1" && "text-destructive",
          )}
        >
          {priorityLabel(p)}
        </Button>
      ))}
    </div>
  );
}

// Modality is optional: clicking the active segment deselects back to ""
// (legacy/unspecified) — a case isn't forced to declare one.
export function ModalityToggle({
  value,
  onChange,
}: {
  value: TestCaseModality;
  onChange: (m: TestCaseModality) => void;
}) {
  const modalityLabel = useModalityLabel();
  return (
    <div className="flex rounded-md border p-0.5">
      {TEST_CASE_MODALITIES.map((m) => (
        <Button
          key={m}
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onChange(value === m ? "" : m)}
          className={cn(
            "h-6 px-2 text-[11px]",
            value === m ? "bg-muted font-medium text-foreground" : "text-muted-foreground",
          )}
        >
          {modalityLabel(m)}
        </Button>
      ))}
    </div>
  );
}
