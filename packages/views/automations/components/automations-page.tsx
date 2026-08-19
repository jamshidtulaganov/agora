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
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { Switch } from "@agora/ui/components/ui/switch";
import { toast } from "sonner";
import { PageHeader } from "../../layout/page-header";
import { useT, useTimeAgo } from "../../i18n";
import { useNavigation } from "../../navigation";
import { labelFor, summarizeFlow } from "./flow-labels";

// The Automations index, styled like every other list page (autopilots-page is the
// reference): PageHeader with the sidebar trigger, full-bleed hoverable rows, and
// the recipe gallery inside the empty/footer area rather than on floating cards.

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
    <div className="flex h-full flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Workflow className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
          {!isLoading && (automations?.length ?? 0) > 0 && (
            <span className="text-xs text-muted-foreground tabular-nums">{automations?.length}</span>
          )}
        </div>
        <Button size="sm" variant="outline" onClick={() => navigation.push(paths.automationDetail("new"))}>
          <Plus className="h-3.5 w-3.5 mr-1" />
          {t(($) => $.page.new)}
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-y-auto">
        {isLoading ? (
          <div className="space-y-1 p-5">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : error ? (
          <div className="flex flex-col items-center px-5 py-16">
            <Workflow className="mb-3 h-10 w-10 text-muted-foreground opacity-30" />
            <p className="text-sm text-muted-foreground">{t(($) => $.page.load_failed)}</p>
          </div>
        ) : (automations?.length ?? 0) === 0 ? (
          <div className="flex flex-col items-center px-5 py-16">
            <Workflow className="mb-3 h-10 w-10 text-muted-foreground opacity-30" />
            <p className="text-sm text-muted-foreground">{t(($) => $.page.empty_title)}</p>
            <p className="mb-6 mt-1 max-w-md text-center text-xs text-muted-foreground">
              {t(($) => $.page.empty_description)}
            </p>
            <RecipeGrid />
          </div>
        ) : (
          <>
            {automations?.map((automation) => (
              <button
                key={automation.id}
                type="button"
                className="flex w-full items-center gap-3 px-5 py-2.5 text-left transition-colors hover:bg-accent/30"
                onClick={() => navigation.push(paths.automationDetail(automation.id))}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium">{automation.name}</span>
                    {automation.recipe_key !== "" && (
                      <Badge variant="secondary" className="gap-1 px-1.5 py-0 text-[10px]">
                        <Sparkles className="size-2.5" aria-hidden />
                        {t(($) => $.page.installed)}
                      </Badge>
                    )}
                  </div>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">
                    {summarizeFlow(labelFor(triggerLabels, automation.trigger_type), automation.actions, stepLabels)}
                  </p>
                </div>
                <RunStamp count={automation.run_count} lastRunAt={automation.last_run_at} />
                {/* The switch is inside the row-button, so it must not navigate. */}
                <span onClick={(event) => event.stopPropagation()}>
                  <Switch
                    aria-label={automation.enabled ? t(($) => $.editor.enabled) : t(($) => $.editor.disabled)}
                    checked={automation.enabled}
                    onCheckedChange={(checked) =>
                      setEnabled.mutate({ id: automation.id, enabled: checked === true })
                    }
                  />
                </span>
              </button>
            ))}
            <div className="border-t px-5 py-6">
              <h2 className="text-sm font-medium">{t(($) => $.page.recipes_title)}</h2>
              <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.page.recipes_subtitle)}</p>
              <div className="mt-3">
                <RecipeGrid />
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );

  // RecipeGrid matches the autopilots template grid: bordered tiles, icon left,
  // hover accent — not elevated cards.
  function RecipeGrid() {
    return (
      <div className="grid w-full max-w-3xl grid-cols-1 gap-3 sm:grid-cols-2">
        {recipes?.map((recipe) => (
          <div key={recipe.key} className="flex items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-accent/40">
            <Sparkles className="mt-0.5 h-5 w-5 shrink-0 text-muted-foreground" aria-hidden />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">{recipe.title}</span>
                {recipe.installed && (
                  <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
                    {t(($) => $.page.installed)}
                  </Badge>
                )}
              </div>
              <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{recipe.description}</p>
              <div className="mt-2 flex items-center justify-between">
                <span className="text-xs text-muted-foreground">
                  {t(($) => $.page.flows_count, { count: recipe.flows.length })}
                </span>
                <Button
                  size="sm"
                  variant={recipe.installed ? "ghost" : "outline"}
                  className="h-7 text-xs"
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
                  {installRecipe.isPending ? (
                    <Loader2 className="size-3 animate-spin motion-reduce:animate-none" aria-hidden />
                  ) : (
                    t(($) => $.page.install)
                  )}
                </Button>
              </div>
            </div>
          </div>
        ))}
      </div>
    );
  }
}

// RunStamp: a never-run flow says so — "0" next to an enabled rule reads as broken,
// "never ran" reads as waiting.
function RunStamp({ count, lastRunAt }: { count: number; lastRunAt: string | null }) {
  const { t } = useT("automations");
  const timeAgo = useTimeAgo();
  return (
    <span className="hidden shrink-0 text-xs text-muted-foreground sm:block">
      {count === 0 || !lastRunAt ? t(($) => $.page.never_ran) : t(($) => $.page.last_run, { when: timeAgo(lastRunAt) })}
    </span>
  );
}
