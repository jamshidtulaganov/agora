"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, CalendarDays, CheckCircle2, Code2, Loader2, MinusCircle, Search, SlidersHorizontal, Trash2, Workflow, XCircle } from "lucide-react";
import { toast } from "sonner";
import {
  automationCatalogOptions,
  automationDetailOptions,
  automationRunsOptions,
  useCreateAutomation,
  useDeleteAutomation,
  useRerunAutomationRun,
  useSetAutomationEnabled,
  useUpdateAutomation,
  type Automation,
  type AutomationRun,
} from "@agora/core/automations";
import { projectListOptions } from "@agora/core/projects/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@agora/ui/components/ui/alert-dialog";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { Tabs, TabsList, TabsTrigger } from "@agora/ui/components/ui/tabs";
import { Badge } from "@agora/ui/components/ui/badge";
import { Input } from "@agora/ui/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import { Switch } from "@agora/ui/components/ui/switch";
import { PageHeader } from "../../layout/page-header";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink, useNavigation } from "../../navigation";
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
  const setEnabledMutation = useSetAutomationEnabled(wsId);
  const rerunAutomation = useRerunAutomationRun(wsId, automationId);

  // Draft state. Local because a flow is edited as a whole and saved once — a
  // per-keystroke mutation would fire the engine's validation on half-built nodes.
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [projectId, setProjectId] = useState<string>("");
  const [enabled, setEnabled] = useState(false);
  const [flow, setFlow] = useState<AutomationFlowValue>({ trigger_type: "", conditions: [], actions: [] });
  // trigger_config rides along even though the canvas has no node for it: the
  // update endpoint full-replaces the row, so NOT sending it back would silently
  // reset a custom cooldown/rate cap to the defaults on every save. The Code
  // view is where it can be read and edited.
  const [triggerConfig, setTriggerConfig] = useState<Record<string, unknown>>({});
  const [dirty, setDirty] = useState(false);
  // Pending confirmation: deleting takes the run history with it; leaving with
  // unsaved edits takes the draft. Both are asked via AlertDialog — a native
  // window.confirm would block the embedded-browser QA loop.
  const [confirming, setConfirming] = useState<"delete" | "discard" | null>(null);
  // Canvas is the default; Code shows the SAME flow as editable JSON, for the
  // people who assemble rules faster in text (and for pasting a flow between
  // workspaces). One draft, two projections — Apply parses back into it.
  const [view, setView] = useState<"canvas" | "code">("canvas");
  const [codeDraft, setCodeDraft] = useState("");
  const [codeError, setCodeError] = useState("");
  // The id whose row seeded the draft. Seeding happens ONCE per automation: the
  // detail query is invalidated by every automation:run WS event, and reseeding
  // on each refetch would clobber a dirty draft mid-edit.
  const [seededId, setSeededId] = useState("");
  // Run-view selection drives the outcome dots on the canvas, like Zapier's run
  // inspector. It does not mutate the flow and falls back to the newest run.
  const [selectedRunId, setSelectedRunId] = useState("");
  const selectedRun = useMemo(
    () => runs?.find((run) => run.id === selectedRunId) ?? runs?.[0] ?? null,
    [runs, selectedRunId],
  );

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
    if (!existing || existing.id === "" || existing.id === seededId) return;
    setName(existing.name);
    setDescription(existing.description);
    setProjectId(existing.project_id ?? "");
    setEnabled(existing.enabled);
    setFlow({
      trigger_type: existing.trigger_type,
      conditions: existing.conditions,
      actions: existing.actions,
    });
    setTriggerConfig(existing.trigger_config);
    setDirty(false);
    setSeededId(existing.id);
  }, [isNew, existing, catalog, seededId]);

  const updateFlow = (next: AutomationFlowValue) => {
    setFlow(next);
    setDirty(true);
  };

  // The Code projection includes trigger_config: the canvas has no node for the
  // loop-guard overrides (min_interval_seconds, max_per_hour), so this is the
  // one place they can be read and edited.
  const codeProjection = () => JSON.stringify({ ...flow, trigger_config: triggerConfig }, null, 2);

  const openCodeView = () => {
    setCodeDraft(codeProjection());
    setCodeError("");
    setView("code");
  };

  // Apply parses the JSON back into the draft. Validation here is SHAPE only —
  // the server's validator (unknown trigger/step/operator, bad status) remains
  // the authority at save time and its message names the offending step.
  const applyCode = (): boolean => {
    try {
      const parsed = JSON.parse(codeDraft) as Partial<AutomationFlowValue> & {
        trigger_config?: Record<string, unknown>;
      };
      if (typeof parsed.trigger_type !== "string" || parsed.trigger_type === "") {
        setCodeError(t(($) => $.code.needs_trigger));
        return false;
      }
      if (!Array.isArray(parsed.actions)) {
        setCodeError(t(($) => $.code.needs_actions));
        return false;
      }
      updateFlow({
        trigger_type: parsed.trigger_type,
        conditions: Array.isArray(parsed.conditions) ? parsed.conditions : [],
        actions: parsed.actions,
      });
      if (parsed.trigger_config && typeof parsed.trigger_config === "object" && !Array.isArray(parsed.trigger_config)) {
        setTriggerConfig(parsed.trigger_config);
      }
      setCodeError("");
      setView("canvas");
      return true;
    } catch {
      setCodeError(t(($) => $.code.invalid_json));
      return false;
    }
  };

  // Switching back to the canvas applies pending JSON edits instead of silently
  // discarding them; invalid JSON keeps the Code view open with its error.
  const leaveCodeView = () => {
    if (codeDraft === codeProjection()) {
      setView("canvas");
      return;
    }
    applyCode();
  };

  const save = () => {
    const payload = {
      name: name.trim() === "" ? t(($) => $.editor.name_placeholder) : name.trim(),
      description: description.trim(),
      enabled,
      project_id: projectId === "" ? null : projectId,
      trigger_type: flow.trigger_type,
      trigger_config: triggerConfig,
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

  const rerunSelected = () => {
    if (!selectedRun || selectedRun.status !== "failed") return;
    rerunAutomation.mutate(selectedRun.id, {
      onSuccess: (run) => {
        setSelectedRunId(run.id);
        toast.success(t(($) => $.runs.rerun_succeeded));
      },
      onError: (error) => {
        toast.error(error instanceof Error && error.message !== "" ? error.message : t(($) => $.runs.rerun_failed));
      },
    });
  };

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
          onClick={() => {
            // A dirty draft is worth one question — Delete already asks, and
            // losing a half-built flow is the more common accident.
            if (dirty) setConfirming("discard");
            else navigation.push(paths.automations());
          }}
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
          {flow.actions.length === 0 ? (
            // Say WHY Save is disabled — a silently dead button on a fresh page
            // reads as broken.
            <span className="hidden text-xs text-muted-foreground lg:block">{t(($) => $.editor.add_step_hint)}</span>
          ) : (
            dirty && <span className="hidden text-xs text-muted-foreground lg:block">{t(($) => $.editor.unsaved)}</span>
          )}
          {/* On an existing flow this switch behaves exactly like the one on the
              list: it saves immediately. Making it part of the draft gave the
              same control two behaviors, and a flipped-off rule kept running
              when the user left without pressing Save. On a NEW flow there is
              no row yet, so it stays draft state for the create. */}
          <Switch
            aria-label={enabled ? t(($) => $.editor.enabled) : t(($) => $.editor.disabled)}
            checked={enabled}
            onCheckedChange={(checked) => {
              setEnabled(checked === true);
              if (isNew) {
                setDirty(true);
                return;
              }
              setEnabledMutation.mutate(
                { id: automationId, enabled: checked === true },
                { onError: () => setEnabled((current) => !current) },
              );
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
              onClick={() => setConfirming("delete")}
            >
              <Trash2 aria-hidden />
            </Button>
          )}
        </div>
      </PageHeader>

      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b px-5 py-2.5">
          <Input
            className="h-8 max-w-2xl flex-1 border-transparent bg-transparent px-2 text-xs text-muted-foreground shadow-none focus-visible:border-input"
            placeholder={t(($) => $.editor.description_placeholder)}
            value={description}
            onChange={(event) => {
              setDescription(event.target.value);
              setDirty(true);
            }}
          />
          <Tabs
            value={view}
            onValueChange={(next) => {
              if (next === "code") openCodeView();
              else leaveCodeView();
            }}
          >
            <TabsList variant="line">
              <TabsTrigger value="canvas" className="text-xs">
                <Workflow className="mr-1 size-3" aria-hidden />
                {t(($) => $.code.canvas_tab)}
              </TabsTrigger>
              <TabsTrigger value="code" className="text-xs">
                <Code2 className="mr-1 size-3" aria-hidden />
                {t(($) => $.code.code_tab)}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
        {view === "code" ? (
          <div className="min-h-0 flex-1 space-y-2 overflow-y-auto p-5">
            <Textarea
              aria-label={t(($) => $.code.code_tab)}
              className="min-h-[min(48vh,420px)] font-mono text-xs"
              spellCheck={false}
              value={codeDraft}
              onChange={(event) => setCodeDraft(event.target.value)}
            />
            {codeError !== "" && <p className="text-xs text-destructive">{codeError}</p>}
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" onClick={applyCode}>
                {t(($) => $.code.apply)}
              </Button>
              <p className="text-xs text-muted-foreground">{t(($) => $.code.hint)}</p>
            </div>
          </div>
        ) : catalog ? (
          <div className="grid min-h-0 flex-1 gap-3 overflow-y-auto bg-muted/20 p-3 xl:grid-cols-[288px_minmax(0,1fr)] xl:overflow-hidden">
            {!isNew && (
              <AutomationRunList
                runs={runs ?? []}
                selectedRunId={selectedRun?.id ?? ""}
                onSelectRun={setSelectedRunId}
              />
            )}
            <AutomationFlowEditor
              value={flow}
              catalog={catalog}
              onChange={updateFlow}
              disabled={saving}
              lastRun={dirty ? undefined : selectedRun}
              onRerunLastRun={dirty || !enabled ? undefined : rerunSelected}
              rerunningLastRun={rerunAutomation.isPending}
              fillHeight
            />
          </div>
        ) : null}
      </div>

      <AlertDialog open={confirming !== null} onOpenChange={(open) => { if (!open) setConfirming(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirming === "delete" ? t(($) => $.editor.delete) : t(($) => $.editor.discard_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirming === "delete" ? t(($) => $.editor.delete_confirm) : t(($) => $.editor.discard_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.editor.keep_editing)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (confirming === "delete") {
                  deleteAutomation.mutate(automationId, {
                    onSuccess: () => navigation.push(paths.automations()),
                    onError: () => toast.error(t(($) => $.editor.save_failed)),
                  });
                } else {
                  navigation.push(paths.automations());
                }
                setConfirming(null);
              }}
            >
              {confirming === "delete" ? t(($) => $.editor.delete) : t(($) => $.editor.discard)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// AutomationRunList renders the audit trail, skipped rows included — a rule that
// evaluated and declined is the normal case, and its reason is the debugging tool.
function AutomationRunList({
  runs,
  selectedRunId,
  onSelectRun,
}: {
  runs: AutomationRun[];
  selectedRunId: string;
  onSelectRun: (id: string) => void;
}) {
  const { t } = useT("automations");
  const timeAgo = useTimeAgo();
  const runPaths = useWorkspacePaths();
  const stepLabels = t(($) => $.step, { returnObjects: true }) as Record<string, string>;

  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("all");
  const rows = useMemo(() => runs
    .filter((run) => status === "all" || run.status === status)
    .filter((run) => {
      const needle = query.trim().toLowerCase();
      if (needle === "") return true;
      return [run.status, run.trigger_type, run.error, run.detail.reason, run.id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle));
    })
    .slice(0, 50), [runs, query, status]);

  return (
    <section className="flex min-h-[520px] flex-col overflow-hidden rounded-lg border bg-card xl:h-full">
      <header className="space-y-3 border-b p-3">
        <div>
          <h2 className="text-sm font-semibold">{t(($) => $.runs.title)}</h2>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{t(($) => $.runs.inspector_hint)}</p>
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
          <Input
            aria-label={t(($) => $.runs.search)}
            className="h-8 pl-8 text-xs"
            placeholder={t(($) => $.runs.search)}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <div className="grid grid-cols-2 gap-2">
          <label className="relative">
            <SlidersHorizontal className="pointer-events-none absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden />
            <NativeSelect
              aria-label={t(($) => $.runs.status_filter)}
              className="h-8 pl-7 text-xs"
              value={status}
              onChange={(event) => setStatus(event.target.value)}
            >
              <NativeSelectOption value="all">{t(($) => $.runs.all_statuses)}</NativeSelectOption>
              <NativeSelectOption value="applied">{t(($) => $.runs.applied)}</NativeSelectOption>
              <NativeSelectOption value="skipped">{t(($) => $.runs.skipped)}</NativeSelectOption>
              <NativeSelectOption value="failed">{t(($) => $.runs.failed)}</NativeSelectOption>
            </NativeSelect>
          </label>
          <div className="flex h-8 items-center gap-1.5 rounded-md border px-2 text-xs text-muted-foreground">
            <CalendarDays className="size-3" aria-hidden />
            <span className="truncate">{t(($) => $.runs.last_30_days)}</span>
          </div>
        </div>
        <p className="text-[11px] text-muted-foreground">{t(($) => $.runs.results_count, { count: rows.length })}</p>
      </header>
      {rows.length === 0 && <p className="p-3 text-xs text-muted-foreground">{t(($) => $.runs.empty)}</p>}
      <ul className="min-h-0 flex-1 space-y-1.5 overflow-y-auto p-2">
        {rows.map((run) => (
          <li key={run.id}>
            <button
              type="button"
              aria-pressed={run.id === selectedRunId}
              className={`w-full rounded-md border px-2.5 py-2 text-left text-xs transition ${run.id === selectedRunId ? "border-brand bg-brand/5 ring-1 ring-brand/20" : "bg-background hover:border-foreground/20 hover:bg-muted/40"}`}
              onClick={() => onSelectRun(run.id)}
            >
              <div className="flex items-center justify-between gap-2">
                <RunStatusBadge status={run.status} />
                <span className="shrink-0 text-[11px] text-muted-foreground">{timeAgo(run.created_at)}</span>
              </div>
              <p className="mt-1.5 truncate font-medium">{run.trigger_type}</p>
              <p className="mt-0.5 truncate text-[11px] text-muted-foreground">
                {run.detail.reason || run.error || t(($) => $.runs.actions_applied, { count: run.actions_applied })}
              </p>
            </button>
            {run.id === selectedRunId && run.issue_id && (
              <AppLink
                href={runPaths.issueDetail(run.issue_id)}
                className="mx-2 mt-1 inline-block text-[11px] text-muted-foreground underline-offset-2 hover:underline"
              >
                {t(($) => $.runs.open_issue)}
              </AppLink>
            )}
          </li>
        ))}
      </ul>
      {selectedRunId && (
        <footer className="space-y-2 border-t p-2 text-[11px] text-muted-foreground">
          {runs.find((run) => run.id === selectedRunId)?.error && (
            <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 p-2 text-destructive">
              <p className="font-medium">{t(($) => $.runs.error_details)}</p>
              <p className="mt-1 break-words leading-relaxed">{runs.find((run) => run.id === selectedRunId)?.error}</p>
            </div>
          )}
          {(runs.find((run) => run.id === selectedRunId)?.detail.actions ?? []).map((action, index) => (
            <div key={index} className="flex items-start gap-1.5 py-0.5">
              {action.ok ? <CheckCircle2 className="size-3 text-emerald-500" aria-hidden /> : <XCircle className="size-3 text-destructive" aria-hidden />}
              <span className="min-w-0 break-words leading-relaxed">{stepLabels[action.type] ?? action.type}{action.detail ? ` — ${action.detail}` : ""}</span>
            </div>
          ))}
        </footer>
      )}
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
