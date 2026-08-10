"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bot, ChevronRight, RotateCcw, UserRound, Workflow } from "lucide-react";
import { toast } from "sonner";
import { projectConfigOptions, projectDetailOptions } from "@agora/core/projects/queries";
import {
  useResetProjectConfig,
  useSetProjectConfig,
  useUpdateProject,
} from "@agora/core/projects/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ProjectOrchestrationDefaults } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Input } from "@agora/ui/components/ui/input";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { ProjectPipelineSection } from "./project-pipeline-section";
import { ProjectQASection } from "./project-qa-section";

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
  const [open, setOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const { data: projectConfig = [], isLoading: configLoading } = useQuery({
    ...projectConfigOptions(wsId, projectId),
    enabled: open,
  });
  const updateProject = useUpdateProject();
  const setProjectConfig = useSetProjectConfig(projectId);
  const resetProjectConfig = useResetProjectConfig(projectId);
  const saved = project?.settings.orchestration ?? BUILT_IN_DEFAULTS;
  const [progression, setProgression] = useState<ProjectOrchestrationDefaults["progression_policy"]>(saved.progression_policy);
  const [maxConcurrency, setMaxConcurrency] = useState(saved.max_concurrency);
  const [reviewPlanFirst, setReviewPlanFirst] = useState(saved.review_plan_first);
  const autoQA = projectConfig.find((entry) => entry.key === "AGORA_AUTO_QA_ENABLED");
  const autoReview = projectConfig.find((entry) => entry.key === "AGORA_AUTO_REVIEW_ENABLED");

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

  const setStageOwner = (key: string, automated: boolean) => {
    setProjectConfig.mutate(
      { key, value: automated ? "true" : "false" },
      {
        onError: (error) =>
          toast.error(
            error instanceof Error && error.message
              ? error.message
              : t(($) => $.execution_defaults.save_failed),
          ),
      },
    );
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
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            {t(($) => $.execution_defaults.description)}
          </p>

          <div className="overflow-hidden rounded-lg border bg-muted/15">
            <StaticStageRow
              label={t(($) => $.execution_defaults.build_stage)}
              description={t(($) => $.execution_defaults.build_stage_hint)}
              value={t(($) => $.execution_defaults.issue_assignee)}
              icon="agent"
            />
            <StageOwnerRow
              label={t(($) => $.execution_defaults.qa_stage)}
              description={t(($) => $.execution_defaults.qa_stage_hint)}
              automated={autoQA?.value === "true" || autoQA?.value === "1"}
              overridden={autoQA?.overridden_by_project === true}
              disabled={configLoading || !autoQA || setProjectConfig.isPending || resetProjectConfig.isPending}
              onChange={(value) => setStageOwner("AGORA_AUTO_QA_ENABLED", value)}
              onReset={() => resetProjectConfig.mutate("AGORA_AUTO_QA_ENABLED")}
            />
            <StageOwnerRow
              label={t(($) => $.execution_defaults.review_stage)}
              description={t(($) => $.execution_defaults.review_stage_hint)}
              automated={autoReview?.value === "true" || autoReview?.value === "1"}
              overridden={autoReview?.overridden_by_project === true}
              disabled={configLoading || !autoReview || setProjectConfig.isPending || resetProjectConfig.isPending}
              onChange={(value) => setStageOwner("AGORA_AUTO_REVIEW_ENABLED", value)}
              onReset={() => resetProjectConfig.mutate("AGORA_AUTO_REVIEW_ENABLED")}
            />
            <StaticStageRow
              label={t(($) => $.execution_defaults.release_stage)}
              description={t(($) => $.execution_defaults.release_stage_hint)}
              value={t(($) => $.execution_defaults.human_approval)}
              icon="human"
            />
          </div>

          <div className="rounded-lg border">
            <button
              type="button"
              className="flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left"
              onClick={() => setAdvancedOpen((value) => !value)}
              aria-expanded={advancedOpen}
            >
              <span>
                <span className="block text-xs font-medium">{t(($) => $.execution_defaults.advanced)}</span>
                <span className="mt-0.5 block text-[10px] leading-relaxed text-muted-foreground">
                  {t(($) => $.execution_defaults.advanced_hint)}
                </span>
              </span>
              <ChevronRight
                className={cn(
                  "size-3.5 shrink-0 text-muted-foreground transition-transform",
                  advancedOpen && "rotate-90",
                )}
              />
            </button>

            {advancedOpen && (
              <div className="space-y-4 border-t px-3 py-3">
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
                        className={cn(
                          "h-7 px-2 text-[11px]",
                          progression === option && ACTIVE_SEGMENT,
                        )}
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
                    <span className="font-medium">
                      {t(($) => $.execution_defaults.parallel_workers)}
                    </span>
                    <span className="mt-0.5 block text-[10px] text-muted-foreground">
                      {t(($) => $.execution_defaults.parallel_workers_hint)}
                    </span>
                  </span>
                  <Input
                    type="number"
                    min={1}
                    max={10}
                    value={maxConcurrency}
                    onChange={(event) =>
                      setMaxConcurrency(
                        Math.min(10, Math.max(1, Number(event.target.value) || 1)),
                      )
                    }
                    className="h-7 w-16 text-right font-mono text-xs tabular-nums"
                  />
                </label>

                <label className="flex cursor-pointer items-start gap-2 text-xs">
                  <Checkbox
                    checked={reviewPlanFirst}
                    onCheckedChange={(checked) => setReviewPlanFirst(checked === true)}
                  />
                  <span>
                    <span className="font-medium">
                      {t(($) => $.execution_defaults.review_plan_first)}
                    </span>
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

                <ProjectQASection projectId={projectId} embedded />

                <ProjectPipelineSection
                projectId={projectId}
                embedded
                excludeKeys={["AGORA_AUTO_QA_ENABLED", "AGORA_AUTO_REVIEW_ENABLED"]}
              />
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function StaticStageRow({
  label,
  description,
  value,
  icon,
}: {
  label: string;
  description: string;
  value: string;
  icon: "agent" | "human";
}) {
  const Icon = icon === "agent" ? Bot : UserRound;
  return (
    <div className="flex items-center gap-3 border-b px-3 py-2.5 last:border-b-0">
      <div className="min-w-0 flex-1">
        <div className="text-xs font-medium">{label}</div>
        <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">{description}</p>
      </div>
      <span className="inline-flex shrink-0 items-center gap-1 rounded-md border bg-background px-2 py-1 text-[10px] font-medium">
        <Icon className="size-3" />
        {value}
      </span>
    </div>
  );
}

function StageOwnerRow({
  label,
  description,
  automated,
  overridden,
  disabled,
  onChange,
  onReset,
}: {
  label: string;
  description: string;
  automated: boolean;
  overridden: boolean;
  disabled: boolean;
  onChange: (automated: boolean) => void;
  onReset: () => void;
}) {
  const { t } = useT("projects");
  return (
    <div className="border-b px-3 py-2.5 last:border-b-0">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="text-xs font-medium">{label}</span>
            <span className="rounded bg-muted px-1 py-0.5 text-[9px] text-muted-foreground">
              {overridden
                ? t(($) => $.execution_defaults.custom)
                : t(($) => $.execution_defaults.workspace_default)}
            </span>
          </div>
          <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">{description}</p>
        </div>
        <div className="flex shrink-0 rounded-md border bg-background p-0.5" role="group" aria-label={label}>
          <button
            type="button"
            disabled={disabled}
            aria-pressed={!automated}
            onClick={() => onChange(false)}
            className={cn(
              "inline-flex h-6 items-center gap-1 rounded px-1.5 text-[10px] font-medium transition-colors disabled:opacity-50",
              !automated ? "bg-foreground text-background" : "text-muted-foreground hover:text-foreground",
            )}
          >
            <UserRound className="size-3" />
            {t(($) => $.execution_defaults.human)}
          </button>
          <button
            type="button"
            disabled={disabled}
            aria-pressed={automated}
            onClick={() => onChange(true)}
            className={cn(
              "inline-flex h-6 items-center gap-1 rounded px-1.5 text-[10px] font-medium transition-colors disabled:opacity-50",
              automated ? "bg-foreground text-background" : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Bot className="size-3" />
            {t(($) => $.execution_defaults.agent)}
          </button>
        </div>
      </div>
      {overridden && (
        <button
          type="button"
          disabled={disabled}
          onClick={onReset}
          className="mt-1 inline-flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground disabled:opacity-50"
        >
          <RotateCcw className="size-2.5" />
          {t(($) => $.execution_defaults.use_workspace_default)}
        </button>
      )}
    </div>
  );
}
