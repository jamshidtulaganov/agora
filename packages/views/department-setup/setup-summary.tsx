"use client";

import { Check } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../i18n";
import { matchSidebarPreset } from "./presets";
import { TeamSidebarPreview } from "./team-sidebar-preview";

export type SetupStepId = "knowledge" | "sidebar" | "agents";

/**
 * The rail beside the wizard: the three steps in order, each with what the
 * team gets from it so far. It doubles as the step list — earlier steps can
 * be reopened from here — and on the sidebar step it shows the live preview.
 */
export function SetupSummary({
  steps,
  current,
  onOpenStep,
  hasInstructions,
  documentCount,
  workspaceName,
  hidden,
  agentNames,
}: {
  steps: readonly SetupStepId[];
  current: number;
  onOpenStep: (step: SetupStepId) => void;
  hasInstructions: boolean;
  documentCount: number;
  workspaceName: string;
  hidden: readonly string[];
  agentNames: readonly string[];
}) {
  const { t } = useT("department-setup");
  const preset = matchSidebarPreset(hidden);

  const lines = (step: SetupStepId): string[] => {
    if (step === "knowledge") {
      return [
        hasInstructions ? t(($) => $.done.instructions_set) : t(($) => $.done.instructions_missing),
        documentCount > 0 ? t(($) => $.done.files_count, { count: documentCount }) : t(($) => $.done.files_none),
      ];
    }
    if (step === "sidebar") {
      return [
        preset === "simple"
          ? t(($) => $.done.sidebar_simple)
          : preset === "everything"
            ? t(($) => $.done.sidebar_everything)
            : t(($) => $.done.sidebar_custom),
      ];
    }
    return agentNames.length > 0 ? [...agentNames] : [t(($) => $.summary.no_agents)];
  };

  return (
    <section aria-labelledby="setup-summary-title" data-testid="setup-summary">
      <h2 id="setup-summary-title" className="text-sm font-medium">
        {t(($) => $.summary.title)}
      </h2>
      <ol className="mt-4" aria-label={t(($) => $.page.steps_label)}>
        {steps.map((step, i) => {
          const done = i < current;
          const active = i === current;
          const title = t(($) => $.steps[step]);
          return (
            <li key={step} aria-current={active ? "step" : undefined} className="relative flex gap-3 pb-5 last:pb-0">
              {i < steps.length - 1 && (
                <span className="absolute left-2.5 top-6 bottom-1 w-px bg-border" aria-hidden />
              )}
              <span
                className={cn(
                  "relative flex size-5 shrink-0 items-center justify-center rounded-full border bg-background text-[11px] tabular-nums text-muted-foreground",
                  done && "border-foreground bg-foreground text-background",
                  active && "border-foreground text-foreground",
                )}
                aria-hidden
              >
                {done ? <Check className="size-3" /> : i + 1}
              </span>
              <div className="min-w-0 flex-1 space-y-1">
                {done ? (
                  <button
                    type="button"
                    onClick={() => onOpenStep(step)}
                    className="rounded-sm text-sm font-medium underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    {title}
                  </button>
                ) : (
                  <p className={cn("text-sm", active ? "font-medium" : "text-muted-foreground")}>{title}</p>
                )}
                {lines(step).map((line) => (
                  <p key={line} className="text-xs text-muted-foreground">
                    {line}
                  </p>
                ))}
                {active && step === "sidebar" && (
                  <div className="pt-2">
                    <TeamSidebarPreview workspaceName={workspaceName} hidden={hidden} />
                  </div>
                )}
              </div>
            </li>
          );
        })}
      </ol>
    </section>
  );
}
