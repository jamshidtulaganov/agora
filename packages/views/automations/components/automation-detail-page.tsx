"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, CheckCircle2, Loader2, MinusCircle, Trash2, Workflow, XCircle } from "lucide-react";
import { toast } from "sonner";
import {
  automationCatalogOptions,
  automationDetailOptions,
  automationRunsOptions,
  useCreateAutomation,
  useDeleteAutomation,
  useUpdateAutomation,
  type Automation,
  type AutomationRun,
} from "@agora/core/automations";
import { projectListOptions } from "@agora/core/projects/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { Badge } from "@agora/ui/components/ui/badge";
import { Input } from "@agora/ui/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import { Switch } from "@agora/ui/components/ui/switch";
import { PageHeader } from "../../layout/page-header";
import { useT, useTimeAgo } from "../../i18n";
import { useNavigation } from "../../navigation";
import { AutomationFlowEditor, type AutomationFlowValue } from "./automation-flow-editor";

// One flow: its header (name, scope, on/off), its canvas, and its recent runs. The
// runs live on the same screen as the canvas on purpose — "why did this not fire"
// is answered by reading the skipped reason next to the rule that produced it.

const NEW_AUTOMATION_ID = "new";

interface AutomationDetailPageProps {
  automationId: string;
}

export function AutomationDetailPage({ automationId }: AutomationDetailPageProps) {
  const { t } = useT("automations");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();

  const isNew = automationId === NEW_AUTOMATION_ID;
  const { data: catalog } = useQuery(automationCatalogOptions(wsId));
  const { data: existing, isLoading } = useQuery(
    automationDetailOptions(wsId, isNew ? "" : automationId, { enabled: !isNew }),
  );
  const { data: projects } = useQuery(projectListOptions(wsId));
  const { data: runs } = useQuery(automationRunsOptions(wsId, isNew ? "" : automationId, { enabled: !isNew }));

  const createAutomation = useCreateAutomation(wsId);
  const updateAutomation = useUpdateAutomation(wsId);
  const deleteAutomation = useDeleteAutomation(wsId);

  // Draft state. Local because a flow is edited as a whole and saved once — a
  // per-keystroke mutation would fire the engine's validation on half-built nodes.
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [projectId, setProjectId] = useState<string>("");
  const [enabled, setEnabled] = useState(false);
  const [flow, setFlow] = useState<AutomationFlowValue>({ trigger_type: "", conditions: [], actions: [] });
  const [dirty, setDirty] = useState(false);

  // Seed the draft once the server row (or the catalog, for a new flow) arrives.
  useEffect(() => {
    if (isNew) {
      setFlow((current) =>
        current.trigger_type === "" && catalog?.triggers[0]
          ? { trigger_type: catalog.triggers[0].type, conditions: [], actions: [] }
          : current,
      );
      return;
    }
    if (!existing || existing.id === "") return;
    setName(existing.name);
    setDescription(existing.description);
    setProjectId(existing.project_id ?? "");
    setEnabled(existing.enabled);
    setFlow({
      trigger_type: existing.trigger_type,
      conditions: existing.conditions,
      actions: existing.actions,
    });
    setDirty(false);
  }, [isNew, existing, catalog]);

  const updateFlow = (next: AutomationFlowValue) => {
    setFlow(next);
    setDirty(true);
  };

  const save = () => {
    const payload = {
      name: name.trim() === "" ? t(($) => $.editor.name_placeholder) : name.trim(),
      description: description.trim(),
      enabled,
      project_id: projectId === "" ? null : projectId,
      trigger_type: flow.trigger_type,
      conditions: flow.conditions,
      actions: flow.actions,
    };
    const onError = (error: unknown) => {
      // The server validates the flow (unknown step, bad status, project outside the
      // workspace) — surface ITS message, which names the offending step.
      toast.error(error instanceof Error && error.message !== "" ? error.message : t(($) => $.editor.save_failed));
    };
    if (isNew) {
      createAutomation.mutate(payload, {
        onSuccess: (created: Automation) => {
          setDirty(false);
          toast.success(t(($) => $.editor.saved));
          navigation.replace(paths.automationDetail(created.id));
        },
        onError,
      });
      return;
    }
    updateAutomation.mutate(
      { id: automationId, data: payload },
      {
        onSuccess: () => {
          setDirty(false);
          toast.success(t(($) => $.editor.saved));
        },
        onError,
      },
    );
  };

  const saving = createAutomation.isPending || updateAutomation.isPending;

  if (!isNew && isLoading) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader className="px-5">
          <Workflow className="h-4 w-4 text-muted-foreground" />
        </PageHeader>
        <div className="flex flex-1 items-center justify-center" aria-busy="true">
          <Loader2 className="size-4 animate-spin text-muted-foreground motion-reduce:animate-none" aria-hidden />
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      {/* Header carries everything that used to make the page scroll: name, scope,
          the enable switch, save and delete. The canvas below is a fixed frame. */}
      <PageHeader className="gap-3 px-5">
        <button
          type="button"
          className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          onClick={() => navigation.push(paths.automations())}
        >
          <ArrowLeft className="size-3" aria-hidden />
          {t(($) => $.editor.back)}
        </button>
        <Input
          className="h-8 w-full max-w-xs border-transparent bg-transparent px-2 text-sm font-medium shadow-none focus-visible:border-input"
          placeholder={t(($) => $.editor.name_placeholder)}
          value={name}
          onChange={(event) => {
            setName(event.target.value);
            setDirty(true);
          }}
        />
        <NativeSelect
          aria-label={t(($) => $.editor.scope_label)}
          className="h-8 w-auto max-w-44 text-xs"
          value={projectId}
          onChange={(event) => {
            setProjectId(event.target.value);
            setDirty(true);
          }}
        >
          <NativeSelectOption value="">{t(($) => $.page.all_projects)}</NativeSelectOption>
          {projects?.map((project) => (
            <NativeSelectOption key={project.id} value={project.id}>
              {project.title}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <div className="ml-auto flex shrink-0 items-center gap-2">
          {dirty && <span className="hidden text-xs text-muted-foreground lg:block">{t(($) => $.editor.unsaved)}</span>}
          <Switch
            aria-label={enabled ? t(($) => $.editor.enabled) : t(($) => $.editor.disabled)}
            checked={enabled}
            onCheckedChange={(checked) => {
              setEnabled(checked === true);
              setDirty(true);
            }}
          />
          <Button size="sm" onClick={save} disabled={saving || flow.actions.length === 0}>
            {saving ? t(($) => $.editor.saving) : t(($) => $.editor.save)}
          </Button>
          {!isNew && (
            <Button
              size="icon-sm"
              variant="ghost"
              className="text-destructive"
              aria-label={t(($) => $.editor.delete)}
              onClick={() => {
                // A delete takes the run history with it, so it is confirmed first.
                if (!window.confirm(t(($) => $.editor.delete_confirm))) return;
                deleteAutomation.mutate(automationId, {
                  onSuccess: () => navigation.push(paths.automations()),
                  onError: () => toast.error(t(($) => $.editor.save_failed)),
                });
              }}
            >
              <Trash2 aria-hidden />
            </Button>
          )}
        </div>
      </PageHeader>

      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-5">
        <Input
          className="h-8 max-w-2xl border-transparent bg-transparent px-2 text-xs text-muted-foreground shadow-none focus-visible:border-input"
          placeholder={t(($) => $.editor.description_placeholder)}
          value={description}
          onChange={(event) => {
            setDescription(event.target.value);
            setDirty(true);
          }}
        />
        {catalog && (
          <AutomationFlowEditor value={flow} catalog={catalog} onChange={updateFlow} disabled={saving} />
        )}
        {!isNew && <AutomationRunList runs={runs ?? []} />}
      </div>
    </div>
  );
}

// AutomationRunList renders the audit trail, skipped rows included — a rule that
// evaluated and declined is the normal case, and its reason is the debugging tool.
function AutomationRunList({ runs }: { runs: AutomationRun[] }) {
  const { t } = useT("automations");
  const timeAgo = useTimeAgo();
  const stepLabels = t(($) => $.step, { returnObjects: true }) as Record<string, string>;

  const rows = useMemo(() => runs.slice(0, 20), [runs]);

  return (
    <section className="space-y-2 border-t pt-4">
      <h2 className="text-sm font-semibold">{t(($) => $.runs.title)}</h2>
      {rows.length === 0 && <p className="text-xs text-muted-foreground">{t(($) => $.runs.empty)}</p>}
      <ul className="space-y-1.5">
        {rows.map((run) => (
          <li key={run.id} className="rounded-md border bg-card px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <RunStatusBadge status={run.status} />
              <span className="text-muted-foreground">{timeAgo(run.created_at)}</span>
              {run.status === "applied" && (
                <span className="text-muted-foreground">
                  {t(($) => $.runs.actions_applied, { count: run.actions_applied })}
                </span>
              )}
            </div>
            {run.detail.reason && <p className="mt-1 text-muted-foreground">{run.detail.reason}</p>}
            {run.error !== "" && <p className="mt-1 text-destructive">{run.error}</p>}
            {(run.detail.actions?.length ?? 0) > 0 && (
              <ul className="mt-1 space-y-0.5 text-muted-foreground">
                {run.detail.actions?.map((action, index) => (
                  <li key={index} className="flex items-center gap-1.5">
                    {action.ok ? (
                      <CheckCircle2 className="size-3 text-emerald-500" aria-hidden />
                    ) : (
                      <XCircle className="size-3 text-destructive" aria-hidden />
                    )}
                    <span>{stepLabels[action.type] ?? action.type}</span>
                    {action.detail && <span className="truncate">— {action.detail}</span>}
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

// A server-driven status string: anything unrecognised renders neutrally rather
// than crashing the list (CLAUDE.md "Enum drift downgrades, not crashes").
function RunStatusBadge({ status }: { status: string }) {
  const { t } = useT("automations");
  switch (status) {
    case "applied":
      return (
        <Badge variant="secondary" className="gap-1">
          <CheckCircle2 className="size-3 text-emerald-500" aria-hidden />
          {t(($) => $.runs.applied)}
        </Badge>
      );
    case "skipped":
      return (
        <Badge variant="outline" className="gap-1">
          <MinusCircle className="size-3" aria-hidden />
          {t(($) => $.runs.skipped)}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" className="gap-1">
          <XCircle className="size-3" aria-hidden />
          {t(($) => $.runs.failed)}
        </Badge>
      );
    default:
      return <Badge variant="outline">{status}</Badge>;
  }
}
