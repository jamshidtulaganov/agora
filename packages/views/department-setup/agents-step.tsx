"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { CalendarClock, Check } from "lucide-react";
import { useAuthStore } from "@agora/core/auth";
import { agentTemplateListOptions } from "@agora/core/agents/queries";
import { runtimeListOptions } from "@agora/core/runtimes/queries";
import type { AgentRuntime, AgentTemplateSummary } from "@agora/core/types";
import { agentListOptions } from "@agora/core/workspace/queries";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { WEEKLY_DIGEST_SLUG, pickSetupRuntime, setupTemplates } from "./setup-agents";
import { TemplateIcon } from "./template-icon";

const EMPTY_TEMPLATES: AgentTemplateSummary[] = [];

function agentNameKey(name: string): string {
  return name.trim().toLowerCase();
}

export interface SetupAgentsData {
  templates: AgentTemplateSummary[];
  runtime: AgentRuntime | null;
  /** Slugs of templates whose agent is already in the workspace (by name). */
  existing: ReadonlySet<string>;
  isLoading: boolean;
  isError: boolean;
}

/**
 * Everything the agents step needs, loaded when the wizard opens so the step
 * is ready by the time the person gets there. The runtime list and agent list
 * come back as bare casts from the API client, so both are guarded.
 */
export function useSetupAgentsData(wsId: string): SetupAgentsData {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const templatesQuery = useQuery(agentTemplateListOptions());
  const runtimesQuery = useQuery(runtimeListOptions(wsId));
  const agentsQuery = useQuery(agentListOptions(wsId));

  const templates = useMemo(
    () => setupTemplates(templatesQuery.data ?? EMPTY_TEMPLATES),
    [templatesQuery.data],
  );
  const runtime = useMemo(
    () => pickSetupRuntime(Array.isArray(runtimesQuery.data) ? runtimesQuery.data : [], userId),
    [runtimesQuery.data, userId],
  );
  const existing = useMemo(() => {
    const agents = Array.isArray(agentsQuery.data) ? agentsQuery.data : [];
    const names = new Set(
      agents.filter((agent) => !agent.archived_at).map((agent) => agentNameKey(agent.name ?? "")),
    );
    return new Set(templates.filter((t) => names.has(agentNameKey(t.name))).map((t) => t.slug));
  }, [agentsQuery.data, templates]);

  return {
    templates,
    runtime,
    existing,
    isLoading: templatesQuery.isLoading || runtimesQuery.isLoading,
    isError: templatesQuery.isError,
  };
}

/**
 * Step 3: pick any of the ready-made "Business" agents. With no runtime the
 * person can use there is nothing to create them on, so the step says so and
 * points at Runtimes instead — it stays skippable either way.
 */
export function AgentsStep({
  data,
  selected,
  onToggle,
  runtimesHref,
  disabled,
}: {
  data: SetupAgentsData;
  selected: readonly string[];
  onToggle: (slug: string) => void;
  runtimesHref: string;
  disabled: boolean;
}) {
  const { t } = useT("department-setup");

  if (data.isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-24 w-full" />
        ))}
      </div>
    );
  }

  if (!data.runtime) {
    return (
      <div className="space-y-2 rounded-lg border border-dashed p-4" data-testid="setup-no-runtime">
        <p className="text-sm">{t(($) => $.agents.no_runtime)}</p>
        <p className="text-sm text-muted-foreground">{t(($) => $.agents.no_runtime_later)}</p>
        <AppLink
          href={runtimesHref}
          className="inline-block text-sm font-medium text-primary underline-offset-4 hover:underline"
          data-testid="setup-runtimes-link"
        >
          {t(($) => $.agents.no_runtime_link)}
        </AppLink>
      </div>
    );
  }

  if (data.templates.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {data.isError ? t(($) => $.agents.load_failed) : t(($) => $.agents.none)}
      </p>
    );
  }

  return (
    <div className="space-y-2">
      {data.templates.map((template) => {
        const added = data.existing.has(template.slug);
        const isSelected = !added && selected.includes(template.slug);
        return (
          <button
            key={template.slug}
            type="button"
            aria-pressed={isSelected}
            disabled={disabled || added}
            onClick={() => onToggle(template.slug)}
            data-testid={`setup-template-${template.slug}`}
            data-selected={isSelected}
            className={cn(
              "flex w-full items-start gap-3 rounded-lg border p-4 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:hover:bg-transparent",
              isSelected && "border-foreground/40 bg-muted/40",
              added && "opacity-60",
            )}
          >
            <TemplateIcon icon={template.icon} accent={template.accent} />
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{template.name}</span>
              <span className="mt-1 block text-sm text-muted-foreground">{template.description}</span>
              {added ? (
                <span className="mt-2 block text-xs text-muted-foreground">{t(($) => $.agents.added)}</span>
              ) : template.slug === WEEKLY_DIGEST_SLUG ? (
                <span className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground">
                  <CalendarClock className="size-3.5" aria-hidden />
                  {t(($) => $.agents.weekly_schedule)}
                </span>
              ) : null}
            </span>
            <span
              className={cn(
                "mt-1.5 flex size-5 shrink-0 items-center justify-center rounded-full border transition-colors",
                isSelected && "border-primary bg-primary text-primary-foreground",
              )}
              aria-hidden
            >
              {isSelected && <Check className="size-3" />}
            </span>
          </button>
        );
      })}
    </div>
  );
}
