"use client";

import { AlertCircle, Check, Minus } from "lucide-react";
import type { Workspace } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../i18n";
import { matchSidebarPreset } from "./presets";
import type { SetupAgentResult } from "./setup-agents";

type RecapTone = "ok" | "empty" | "failed";

function RecapItem({ tone, children }: { tone: RecapTone; children: React.ReactNode }) {
  const Icon = tone === "ok" ? Check : tone === "failed" ? AlertCircle : Minus;
  return (
    <li className="flex items-start gap-2 text-sm" data-tone={tone}>
      <Icon
        className={cn(
          "mt-0.5 size-4 shrink-0",
          tone === "ok" && "text-success",
          tone === "failed" && "text-destructive",
          tone === "empty" && "text-muted-foreground",
        )}
        aria-hidden
      />
      <span className={cn("min-w-0 break-words", tone === "empty" && "text-muted-foreground")}>{children}</span>
    </li>
  );
}

/** One step's results: the step name beside what it set up. */
function RecapGroup({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="grid gap-2 p-4 sm:grid-cols-[8rem_minmax(0,1fr)] sm:gap-4">
      <h3 className="text-sm text-muted-foreground">{title}</h3>
      <ul className="space-y-1.5">{children}</ul>
    </section>
  );
}

/** The last screen: a short recap of what the team now has, and a way out. */
export function DoneStep({
  workspace,
  documentCount,
  teamHidden,
  results,
  onOpenIssues,
  onOpenKnowledge,
}: {
  workspace: Workspace;
  documentCount: number;
  teamHidden: readonly string[];
  results: readonly SetupAgentResult[];
  onOpenIssues: () => void;
  onOpenKnowledge: () => void;
}) {
  const { t } = useT("department-setup");
  const preset = matchSidebarPreset(teamHidden);
  const unknown = t(($) => $.done.unknown_error);

  const agentItems =
    results.length === 0 ? (
      <RecapItem tone="empty">{t(($) => $.done.no_agents)}</RecapItem>
    ) : (
      results.map((result) => {
        const name = result.name;
        if (result.status === "failed") {
          return (
            <RecapItem key={result.slug} tone="failed">
              {t(($) => $.done.agent_failed, { name, reason: result.error ?? unknown })}
            </RecapItem>
          );
        }
        if (result.schedule === "failed") {
          return (
            <RecapItem key={result.slug} tone="failed">
              {t(($) => $.done.agent_schedule_failed, { name, reason: result.scheduleError ?? unknown })}
            </RecapItem>
          );
        }
        return (
          <RecapItem key={result.slug} tone="ok">
            {result.schedule === "added"
              ? t(($) => $.done.agent_scheduled, { name })
              : t(($) => $.done.agent_added, { name })}
          </RecapItem>
        );
      })
    );

  return (
    <div data-testid="setup-step-done">
      <h2 className="text-xl font-semibold tracking-tight">{t(($) => $.done.title)}</h2>
      <p className="mt-1.5 text-sm text-muted-foreground">{t(($) => $.done.description)}</p>

      <div className="mt-7 divide-y rounded-lg border" data-testid="setup-recap">
        <RecapGroup title={t(($) => $.steps.knowledge)}>
          <RecapItem tone={workspace.context?.trim() ? "ok" : "empty"}>
            {workspace.context?.trim()
              ? t(($) => $.done.instructions_set)
              : t(($) => $.done.instructions_missing)}
          </RecapItem>
          <RecapItem tone={documentCount > 0 ? "ok" : "empty"}>
            {documentCount > 0
              ? t(($) => $.done.files_count, { count: documentCount })
              : t(($) => $.done.files_none)}
          </RecapItem>
        </RecapGroup>
        <RecapGroup title={t(($) => $.steps.sidebar)}>
          <RecapItem tone="ok">
            {preset === "simple"
              ? t(($) => $.done.sidebar_simple)
              : preset === "everything"
                ? t(($) => $.done.sidebar_everything)
                : t(($) => $.done.sidebar_custom)}
          </RecapItem>
        </RecapGroup>
        <RecapGroup title={t(($) => $.steps.agents)}>{agentItems}</RecapGroup>
      </div>

      <div className="mt-6 flex flex-wrap items-center gap-2">
        <Button onClick={onOpenIssues} data-testid="setup-open-issues">
          {t(($) => $.done.open_issues)}
        </Button>
        <Button variant="ghost" onClick={onOpenKnowledge} data-testid="setup-open-knowledge">
          {t(($) => $.done.open_knowledge)}
        </Button>
      </div>
    </div>
  );
}
