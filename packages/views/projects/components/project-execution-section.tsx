"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Workflow } from "lucide-react";
import { toast } from "sonner";
import { projectDetailOptions } from "@agora/core/projects/queries";
import { useUpdateProject } from "@agora/core/projects/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ProjectOrchestrationDefaults } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Input } from "@agora/ui/components/ui/input";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

const BUILT_IN_DEFAULTS: ProjectOrchestrationDefaults = {
  execution_strategy: "automatic",
  progression_policy: "automatic",
  max_concurrency: 3,
  review_plan_first: false,
};

const ACTIVE_SEGMENT =
  "border-brand bg-brand text-brand-foreground hover:bg-brand/90 hover:text-brand-foreground dark:border-brand dark:bg-brand";

export function ProjectExecutionSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const updateProject = useUpdateProject();
  const [open, setOpen] = useState(false);
  const saved = project?.settings.orchestration ?? BUILT_IN_DEFAULTS;
  const [progression, setProgression] = useState<ProjectOrchestrationDefaults["progression_policy"]>(saved.progression_policy);
  const [maxConcurrency, setMaxConcurrency] = useState(saved.max_concurrency);
  const [reviewPlanFirst, setReviewPlanFirst] = useState(saved.review_plan_first);

  useEffect(() => {
    setProgression(saved.progression_policy);
    setMaxConcurrency(saved.max_concurrency);
    setReviewPlanFirst(saved.review_plan_first);
  }, [saved.progression_policy, saved.max_concurrency, saved.review_plan_first]);

  const save = async () => {
    if (!project) return;
    const orchestration: ProjectOrchestrationDefaults = {
      // Kept for wire compatibility. Run topology is derived from the issue
      // assignee (agent=solo, squad=squad, member/unassigned=human).
      execution_strategy: "automatic",
      progression_policy: progression,
      max_concurrency: Math.min(10, Math.max(1, maxConcurrency)),
      review_plan_first: reviewPlanFirst,
    };
    try {
      await updateProject.mutateAsync({
        id: project.id,
        settings: { ...project.settings, orchestration },
      });
      toast.success(t(($) => $.execution_defaults.saved));
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.execution_defaults.save_failed),
      );
    }
  };

  return (
    <div>
      <button
        type="button"
        className={cn(
          "mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70",
          !open && "text-muted-foreground hover:text-foreground",
        )}
        onClick={() => setOpen((value) => !value)}
      >
        <Workflow className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.execution_defaults.title)}
        <ChevronRight
          className={cn(
            "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
      </button>
      {open && (
        <div className="space-y-4 pl-2">
          <p className="text-[10px] leading-relaxed text-muted-foreground">
            {t(($) => $.execution_defaults.description)}
          </p>

          <fieldset>
            <legend className="text-[10px] font-medium text-muted-foreground">
              {t(($) => $.execution_defaults.progression)}
            </legend>
            <div className="mt-1.5 flex flex-wrap gap-1">
              {(["automatic", "gated", "manual"] as const).map((option) => (
                <Button
                  key={option}
                  type="button"
                  size="sm"
                  variant="outline"
                  className={cn("h-7 px-2 text-[11px]", progression === option && ACTIVE_SEGMENT)}
                  onClick={() => setProgression(option)}
                  aria-pressed={progression === option}
                >
                  {t(($) => $.execution_defaults[`progression_${option}`])}
                </Button>
              ))}
            </div>
          </fieldset>

          <label className="flex items-center justify-between gap-3 text-xs">
            <span>
              <span className="font-medium">{t(($) => $.execution_defaults.parallel_workers)}</span>
              <span className="mt-0.5 block text-[10px] text-muted-foreground">
                {t(($) => $.execution_defaults.parallel_workers_hint)}
              </span>
            </span>
            <Input
              type="number"
              min={1}
              max={10}
              value={maxConcurrency}
              onChange={(event) => setMaxConcurrency(Math.min(10, Math.max(1, Number(event.target.value) || 1)))}
              className="h-7 w-16 text-right font-mono text-xs tabular-nums"
            />
          </label>

          <label className="flex cursor-pointer items-start gap-2 text-xs">
            <Checkbox
              checked={reviewPlanFirst}
              onCheckedChange={(checked) => setReviewPlanFirst(checked === true)}
            />
            <span>
              <span className="font-medium">{t(($) => $.execution_defaults.review_plan_first)}</span>
              <span className="mt-0.5 block text-[10px] text-muted-foreground">
                {t(($) => $.execution_defaults.review_plan_first_hint)}
              </span>
            </span>
          </label>

          <Button
            size="sm"
            className="h-7"
            disabled={updateProject.isPending}
            onClick={() => void save()}
          >
            {t(($) => $.execution_defaults.save)}
          </Button>
        </div>
      )}
    </div>
  );
}
