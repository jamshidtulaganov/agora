"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { labelListOptions } from "@agora/core/labels";
import { projectListOptions } from "@agora/core/projects/queries";
import { agentListOptions, memberListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import type {
  AutomationCatalog,
  AutomationCondition,
  AutomationRun,
  AutomationStep,
} from "@agora/core/automations";
import { useT } from "../../i18n";
import { AutomationFlowCanvas, type FlowCanvasNode } from "./automation-flow-canvas";
import { AutomationNodePanel } from "./automation-node-panel";
import { labelFor, summarizeStep, type FieldValueOption } from "./flow-labels";

// The flow editor: an n8n-style canvas for the SHAPE of the automation, plus a
// panel for the selected node's parameters. Controlled — the page owns the draft
// and the save, so the canvas has no state of its own to drift from what is
// written.

export interface AutomationFlowValue {
  trigger_type: string;
  conditions: AutomationCondition[];
  actions: AutomationStep[];
}

interface AutomationFlowEditorProps {
  value: AutomationFlowValue;
  catalog: AutomationCatalog;
  onChange: (next: AutomationFlowValue) => void;
  disabled?: boolean;
  /** The latest run, shown as per-node outcome dots. The caller passes it only
   *  while the draft is UNEDITED — an edited flow no longer matches the run's
   *  step order, and a misaligned badge is worse than none. */
  lastRun?: AutomationRun | null;
  fillHeight?: boolean;
}

export function AutomationFlowEditor({ value, catalog, onChange, disabled, lastRun, fillHeight }: AutomationFlowEditorProps) {
  const { t } = useT("automations");
  const triggerLabels = t(($) => $.trigger, { returnObjects: true }) as Record<string, string>;
  const stepLabels = t(($) => $.step, { returnObjects: true }) as Record<string, string>;
  const fieldLabels = t(($) => $.field, { returnObjects: true }) as Record<string, string>;
  const opLabels = t(($) => $.op, { returnObjects: true }) as Record<string, string>;
  const kindLabels = t(($) => $.kind, { returnObjects: true }) as Record<string, string>;
  const statusLabels = t(($) => $.status, { returnObjects: true }) as Record<string, string>;
  const targetLabels = t(($) => $.target, { returnObjects: true }) as Record<string, string>;
  const destinationLabels = t(($) => $.destination, { returnObjects: true }) as Record<string, string>;

  // The trigger is selected by default: it is the one node every flow has, and
  // opening onto an empty panel would make the editor look broken.
  const [selectedId, setSelectedId] = useState("trigger");

  // Value domains for condition fields with a known vocabulary, resolved from
  // the workspace's own data: a project condition offers projects (title shown,
  // id stored), a label condition offers labels, an assignee condition offers
  // agents. Static domains (statuses, priorities, actor kinds) come from the
  // catalog and the platform's fixed sets.
  const wsId = useWorkspaceId();
  const { data: projects } = useQuery(projectListOptions(wsId));
  const { data: labels } = useQuery(labelListOptions(wsId));
  const { data: agents } = useQuery(agentListOptions(wsId));
  const { data: members } = useQuery(memberListOptions(wsId));
  const { data: integrations } = useQuery({
    queryKey: ["release-integrations", wsId],
    queryFn: () => api.listReleaseIntegrations(wsId),
    enabled: !!wsId,
  });
  const webhookIntegrations = useMemo(
    () => (integrations ?? [])
      .filter((integration) => integration.kind === "webhook" && integration.enabled && integration.has_secret)
      .map((integration) => ({
        value: integration.id,
        label: String(integration.config.name || integration.id),
      })),
    [integrations],
  );
  const valueOptions = useMemo((): Partial<Record<string, FieldValueOption[]>> => {
    const agentOptions = (agents ?? []).map((agent) => ({ value: agent.id, label: agent.name }));
    return {
      statuses: catalog.statuses.map((status) => ({ value: status, label: labelFor(statusLabels, status) })),
      priorities: ["urgent", "high", "medium", "low", "none"].map((priority) => ({
        value: priority,
        label: priority,
      })),
      assignee_types: ["member", "agent", "squad"].map((kind) => ({ value: kind, label: kind })),
      actor_types: ["member", "agent", "system", "automation"].map((kind) => ({ value: kind, label: kind })),
      projects: (projects ?? []).map((project) => ({ value: project.id, label: project.title })),
      labels: (labels ?? []).map((label) => ({ value: label.name, label: label.name })),
      agents: agentOptions,
      // Assignees are polymorphic: an assignee_id condition must be able to name
      // a member as well as an agent.
      assignees: [
        ...agentOptions,
        ...(members ?? []).map((member) => ({ value: member.id, label: member.name })),
      ],
    };
  }, [catalog.statuses, statusLabels, projects, labels, agents, members]);

  // Keep the selection valid as steps come and go — a stale index would render an
  // empty panel for a node that no longer exists.
  useEffect(() => {
    if (selectedId === "trigger" || selectedId === "") return;
    const index = Number(selectedId);
    if (!Number.isInteger(index) || index >= value.actions.length) {
      setSelectedId("trigger");
    }
  }, [selectedId, value.actions.length]);

  const nodes: FlowCanvasNode[] = useMemo(() => {
    // Outcome dots from the latest run: the run's action list is ordered per
    // EXECUTED step, so index i is step i; steps past its end never ran (a
    // filter stopped the flow). The trigger dot is the run's own status.
    const runActions = lastRun?.detail.actions ?? [];
    const outcomeFor = (index: number): { outcome?: FlowCanvasNode["outcome"]; outcomeLabel?: string } => {
      if (!lastRun) return {};
      const entry = runActions[index];
      if (!entry) {
        return lastRun.status === "applied"
          ? { outcome: "not_run", outcomeLabel: t(($) => $.runs.skipped) }
          : {};
      }
      if (entry.type === "filter" && entry.detail !== "passed") {
        return { outcome: "stopped", outcomeLabel: t(($) => $.runs.skipped) };
      }
      return entry.ok
        ? { outcome: "ok", outcomeLabel: t(($) => $.runs.applied) }
        : { outcome: "failed", outcomeLabel: t(($) => $.runs.failed) };
    };
    const triggerOutcome = (): { outcome?: FlowCanvasNode["outcome"]; outcomeLabel?: string } => {
      if (!lastRun) return {};
      if (lastRun.status === "applied") return { outcome: "ok", outcomeLabel: t(($) => $.runs.applied) };
      if (lastRun.status === "failed") return { outcome: "failed", outcomeLabel: t(($) => $.runs.failed) };
      return { outcome: "stopped", outcomeLabel: t(($) => $.runs.skipped) };
    };

    const triggerNode: FlowCanvasNode = {
      id: "trigger",
      kind: "trigger",
      kicker: t(($) => $.flow.when),
      title: labelFor(triggerLabels, value.trigger_type),
      subtitle:
        value.conditions.length === 0
          ? t(($) => $.flow.no_conditions_short)
          : t(($) => $.flow.conditions_count, { count: value.conditions.length }),
      ...triggerOutcome(),
    };
    const stepNodes = value.actions.map((step, index): FlowCanvasNode => ({
      id: String(index),
      kind: step.type === "filter" ? "filter" : "action",
      kicker: step.type === "filter" ? t(($) => $.flow.only_if) : t(($) => $.flow.then),
      title: labelFor(stepLabels, step.type),
      subtitle: summarizeStep(step, {
        fields: fieldLabels,
        ops: opLabels,
        kinds: kindLabels,
        statuses: statusLabels,
        targets: targetLabels,
        destinations: destinationLabels,
      }),
      // A run that never got past the conditions ran no steps: leave step nodes
      // clean instead of painting every one "not run".
      ...(lastRun && lastRun.status !== "skipped" ? outcomeFor(index) : {}),
    }));
    return [triggerNode, ...stepNodes];
  }, [t, triggerLabels, stepLabels, fieldLabels, opLabels, kindLabels, statusLabels, targetLabels, destinationLabels, value, lastRun]);

  const setSteps = (actions: AutomationStep[]) => onChange({ ...value, actions });

  const insertStep = (afterNodeIndex: number) => {
    // afterNodeIndex counts canvas nodes (0 = the trigger), so the step index is
    // the same number: inserting "after the trigger" is step 0.
    // The default is the LIGHTEST step (set_status), not a positional pick from
    // the catalog — steps[1] used to be dispatch_slice_action, so one "+" click
    // defaulted to firing an agent with an empty config that could not save.
    const next = [...value.actions];
    next.splice(afterNodeIndex, 0, { type: "set_status", config: {} });
    setSteps(next);
    setSelectedId(String(afterNodeIndex));
  };

  const reorderStep = (from: number, to: number) => {
    const next = [...value.actions];
    const [moved] = next.splice(from, 1);
    if (!moved) return;
    next.splice(to, 0, moved);
    setSteps(next);
    setSelectedId(String(to));
  };

  const removeStep = (index: number) => {
    setSteps(value.actions.filter((_, i) => i !== index));
    setSelectedId("trigger");
  };

  const selectedStep =
    selectedId !== "trigger" && selectedId !== "" ? value.actions[Number(selectedId)] : undefined;
  const selectedRunResult = useMemo(() => {
    if (!lastRun || selectedId === "") return undefined;
    if (selectedId === "trigger") {
      return {
        ok: lastRun.status === "applied",
        label: lastRun.status === "applied" ? t(($) => $.runs.applied) : lastRun.status === "failed" ? t(($) => $.runs.failed) : t(($) => $.runs.skipped),
        detail: lastRun.detail.reason || lastRun.error,
      };
    }
    const outcome = lastRun.detail.actions?.[Number(selectedId)];
    if (!outcome) return undefined;
    return {
      ok: outcome.ok,
      label: outcome.ok ? t(($) => $.runs.applied) : t(($) => $.runs.failed),
      detail: outcome.detail,
    };
  }, [lastRun, selectedId, t]);

  return (
    <div className={fillHeight ? "grid h-full min-h-0 gap-3 lg:grid-cols-[minmax(0,1fr)_360px]" : "grid gap-3 lg:grid-cols-[minmax(0,1fr)_340px]"}>
      <AutomationFlowCanvas
        nodes={nodes}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onOpen={setSelectedId}
        onInsert={disabled ? undefined : insertStep}
        onReorder={disabled ? undefined : reorderStep}
        onRemove={disabled ? undefined : removeStep}
        disabled={disabled}
        fillHeight={fillHeight}
      />
      <AutomationNodePanel
        nodeId={selectedId}
        triggerType={value.trigger_type}
        triggerConditions={value.conditions}
        step={selectedStep}
        catalog={catalog}
        valueOptions={valueOptions}
        webhookIntegrations={webhookIntegrations}
        disabled={disabled}
        fillHeight={fillHeight}
        runResult={selectedRunResult}
        onTriggerChange={(trigger) => {
          // A trigger change keeps the conditions the new trigger can still
          // evaluate (the common facts — status, project, priority… — exist on
          // every trigger, and label membership reads the issue, not the event)
          // and drops only the trigger-specific ones, which would silently never
          // match. A misclick on the trigger select no longer erases the list.
          const nextFields = catalog.triggers.find((entry) => entry.type === trigger)?.fields ?? [];
          const kept = value.conditions.filter(
            (condition) => nextFields.includes(condition.field) || condition.field === "labels",
          );
          onChange({ ...value, trigger_type: trigger, conditions: kept });
        }}
        onTriggerConditionsChange={(conditions) => onChange({ ...value, conditions })}
        onStepChange={(step) => {
          const index = Number(selectedId);
          if (!Number.isInteger(index)) return;
          const next = [...value.actions];
          next[index] = step;
          setSteps(next);
        }}
        onRemoveStep={() => {
          const index = Number(selectedId);
          if (Number.isInteger(index)) removeStep(index);
        }}
        onClose={() => setSelectedId("")}
      />
    </div>
  );
}
