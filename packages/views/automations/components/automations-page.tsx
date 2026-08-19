"use client";

import { useQuery } from "@tanstack/react-query";
import { Workflow, Plus, Sparkles, Loader2 } from "lucide-react";
import {
  automationListOptions,
  automationRecipesOptions,
  useInstallAutomationRecipe,
  useSetAutomationEnabled,
} from "@agora/core/automations";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { Badge } from "@agora/ui/components/ui/badge";
import { Switch } from "@agora/ui/components/ui/switch";
import { toast } from "sonner";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink, useNavigation } from "../../navigation";
import { labelFor, summarizeFlow } from "./flow-labels";

// The Automations index: the workspace's flows, plus the recipe gallery a new team
// starts from. One page on purpose — a separate "templates" screen would hide the
// only content that exists before anyone has built a flow.

export function AutomationsPage() {
  const { t } = useT("automations");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();

  const { data: automations, isLoading, error } = useQuery(automationListOptions(wsId));
  const { data: recipes } = useQuery(automationRecipesOptions(wsId));
  const setEnabled = useSetAutomationEnabled(wsId);
  const installRecipe = useInstallAutomationRecipe(wsId);

  const triggerLabels = t(($) => $.trigger, { returnObjects: true }) as Record<string, string>;
  const stepLabels = t(($) => $.step, { returnObjects: true }) as Record<string, string>;

  return (
    <div className="mx-auto w-full max-w-4xl space-y-6 p-4 sm:p-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <Workflow className="size-4 text-muted-foreground" aria-hidden />
            {t(($) => $.page.title)}
          </h1>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{t(($) => $.page.subtitle)}</p>
        </div>
        <Button size="sm" onClick={() => navigation.push(paths.automationDetail("new"))}>
          <Plus aria-hidden />
          {t(($) => $.page.new)}
        </Button>
      </header>

      {error && (
        <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
          {t(($) => $.page.load_failed)}
        </div>
      )}

      {isLoading && (
        <div className="flex items-center gap-2 rounded-lg border p-4 text-sm text-muted-foreground" aria-busy="true">
          <Loader2 className="size-4 animate-spin motion-reduce:animate-none" aria-hidden />
        </div>
      )}

      {!isLoading && !error && (automations?.length ?? 0) === 0 && (
        <div className="rounded-lg border border-dashed p-8 text-center">
          <h2 className="text-sm font-semibold">{t(($) => $.page.empty_title)}</h2>
          <p className="mx-auto mt-1 max-w-md text-sm text-muted-foreground">{t(($) => $.page.empty_description)}</p>
        </div>
      )}

      {(automations?.length ?? 0) > 0 && (
        <ul className="space-y-2">
          {automations?.map((automation) => (
            <li key={automation.id} className="rounded-lg border bg-card">
              <div className="flex flex-wrap items-center gap-3 p-3">
                <div className="min-w-0 flex-1">
                  <AppLink
                    href={paths.automationDetail(automation.id)}
                    className="truncate text-sm font-medium hover:underline"
                  >
                    {automation.name}
                  </AppLink>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">
                    {summarizeFlow(labelFor(triggerLabels, automation.trigger_type), automation.actions, stepLabels)}
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  {automation.recipe_key !== "" && (
                    <Badge variant="secondary" className="gap-1">
                      <Sparkles className="size-3" aria-hidden />
                      {t(($) => $.page.installed)}
                    </Badge>
                  )}
                  <RunCountBadge count={automation.run_count} lastRunAt={automation.last_run_at} />
                  <Switch
                    aria-label={automation.enabled ? t(($) => $.editor.enabled) : t(($) => $.editor.disabled)}
                    checked={automation.enabled}
                    onCheckedChange={(checked) =>
                      setEnabled.mutate({ id: automation.id, enabled: checked === true })
                    }
                  />
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}

      <section className="space-y-3 border-t pt-6">
        <div>
          <h2 className="text-sm font-semibold">{t(($) => $.page.recipes_title)}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t(($) => $.page.recipes_subtitle)}</p>
        </div>
        <ul className="grid gap-3 sm:grid-cols-2">
          {recipes?.map((recipe) => (
            <li key={recipe.key} className="flex flex-col rounded-lg border bg-card p-3">
              <div className="flex items-start justify-between gap-2">
                <h3 className="text-sm font-medium">{recipe.title}</h3>
                {recipe.installed && <Badge variant="secondary">{t(($) => $.page.installed)}</Badge>}
              </div>
              <p className="mt-1 flex-1 text-xs leading-relaxed text-muted-foreground">{recipe.description}</p>
              <div className="mt-3 flex items-center justify-between gap-2">
                <span className="text-xs text-muted-foreground">
                  {t(($) => $.page.flows_count, { count: recipe.flows.length })}
                </span>
                <Button
                  size="sm"
                  variant={recipe.installed ? "ghost" : "outline"}
                  disabled={installRecipe.isPending}
                  onClick={() =>
                    installRecipe.mutate(
                      { key: recipe.key },
                      {
                        onSuccess: () => toast.success(t(($) => $.page.install_done)),
                        onError: () => toast.error(t(($) => $.page.install_failed)),
                      },
                    )
                  }
                >
                  {installRecipe.isPending ? t(($) => $.page.installing) : t(($) => $.page.install)}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

// RunCountBadge shows how often a flow has fired. A never-run flow says so
// explicitly: "0" next to an enabled rule reads as broken, "never ran" reads as
// waiting.
function RunCountBadge({ count, lastRunAt }: { count: number; lastRunAt: string | null }) {
  const { t } = useT("automations");
  const timeAgo = useTimeAgo();
  if (count === 0 || !lastRunAt) {
    return <span className="text-xs text-muted-foreground">{t(($) => $.page.never_ran)}</span>;
  }
  return (
    <span className="text-xs text-muted-foreground">
      {t(($) => $.page.last_run, { when: timeAgo(lastRunAt) })}
    </span>
  );
}
