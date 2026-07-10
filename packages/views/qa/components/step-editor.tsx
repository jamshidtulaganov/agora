"use client";

import { Plus, X } from "lucide-react";
import { parseSteps, serializeSteps, type ParsedStep } from "@agora/core/qa/steps";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { useT } from "../../i18n";

// Structured step editor backing the test_case.steps text column's canonical
// serialized format (packages/core/qa/steps.ts) — rows of [action | expects |
// remove], auto-numbered by array position. Shared by AddCaseForm
// (test-cases-panel.tsx) and the Suite tab's add/edit forms (qa-suite-view.tsx)
// so the two panels don't grow parallel step-authoring UIs.
//
// No drag-reorder on purpose (plan scope: "drag-free, keep it simple") — steps
// are typically authored in order and reordering is rare enough that
// remove-and-re-add is an acceptable cost for v1.

export { parseSteps, serializeSteps };
export type { ParsedStep };

export function StepEditor({
  steps,
  onChange,
}: {
  steps: ParsedStep[];
  onChange: (steps: ParsedStep[]) => void;
}) {
  const { t } = useT("issues");
  const update = (i: number, patch: Partial<ParsedStep>) =>
    onChange(steps.map((s, idx) => (idx === i ? { ...s, ...patch } : s)));
  const remove = (i: number) => onChange(steps.filter((_, idx) => idx !== i));
  const add = () => onChange([...steps, { action: "", expects: "" }]);

  return (
    <div className="space-y-1.5">
      {steps.map((s, i) => (
        <div key={i} className="flex items-start gap-1">
          <span className="mt-1.5 w-4 shrink-0 text-right text-[11px] text-muted-foreground" aria-hidden>
            {i + 1}.
          </span>
          <div className="min-w-0 flex-1 space-y-1">
            <Input
              value={s.action}
              onChange={(e) => update(i, { action: e.target.value })}
              placeholder={t(($) => $.test_cases.step_action_ph)}
              aria-label={t(($) => $.test_cases.step_action_ph)}
              className="h-7 text-[12px]"
            />
            <Input
              value={s.expects ?? ""}
              onChange={(e) => update(i, { expects: e.target.value })}
              placeholder={t(($) => $.test_cases.step_expects_ph)}
              aria-label={t(($) => $.test_cases.step_expects_ph)}
              className="h-7 text-[12px]"
            />
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="mt-0.5 size-7 shrink-0 text-muted-foreground hover:text-destructive"
            onClick={() => remove(i)}
            title={t(($) => $.test_cases.remove_step)}
          >
            <X className="size-3.5" />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" className="h-7 gap-1 text-[11px]" onClick={add}>
        <Plus className="size-3.5" />
        {t(($) => $.test_cases.add_step)}
      </Button>
    </div>
  );
}

// Read-only numbered rendering of a case's parsed steps — falls back to
// plain action-only lines when the text doesn't carry `expects` markers
// (legacy free-text blobs). Renders nothing for an empty/blank `steps` value.
export function StepList({ text }: { text: string }) {
  const steps = parseSteps(text);
  if (steps.length === 0) return null;
  return (
    <ol className="list-none space-y-0.5">
      {steps.map((s, i) => (
        <li key={i} className="flex gap-1">
          <span className="shrink-0 text-foreground/70" aria-hidden>
            {i + 1}.
          </span>
          <span>
            {s.action}
            {s.expects && (
              <>
                <span className="text-foreground/70"> → expects: </span>
                {s.expects}
              </>
            )}
          </span>
        </li>
      ))}
    </ol>
  );
}
