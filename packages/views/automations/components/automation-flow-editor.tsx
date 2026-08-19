"use client";

import { useEffect, useMemo, useState } from "react";
import type { AutomationCatalog, AutomationCondition, AutomationStep } from "@agora/core/automations";
import { useT } from "../../i18n";
import { AutomationFlowCanvas, type FlowCanvasNode } from "./automation-flow-canvas";
import { AutomationNodePanel } from "./automation-node-panel";
import { labelFor, summarizeStep } from "./flow-labels";

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
}

export function AutomationFlowEditor({ value, catalog, onChange, disabled }: AutomationFlowEditorProps) {
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
    const triggerNode: FlowCanvasNode = {
      id: "trigger",
      kind: "trigger",
      kicker: t(($) => $.flow.when),
      title: labelFor(triggerLabels, value.trigger_type),
      subtitle:
        value.conditions.length === 0
          ? t(($) => $.flow.no_conditions_short)
          : t(($) => $.flow.conditions_count, { count: value.conditions.length }),
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
    }));
    return [triggerNode, ...stepNodes];
  }, [t, triggerLabels, stepLabels, fieldLabels, opLabels, kindLabels, statusLabels, targetLabels, destinationLabels, value]);

  const setSteps = (actions: AutomationStep[]) => onChange({ ...value, actions });

  const insertStep = (afterNodeIndex: number) => {
    // afterNodeIndex counts canvas nodes (0 = the trigger), so the step index is
    // the same number: inserting "after the trigger" is step 0.
    const next = [...value.actions];
    next.splice(afterNodeIndex, 0, { type: catalog.steps[1] ?? "set_status", config: {} });
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

  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_340px]">
      <AutomationFlowCanvas
        nodes={nodes}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onOpen={setSelectedId}
        onInsert={disabled ? undefined : insertStep}
        onReorder={disabled ? undefined : reorderStep}
        onRemove={disabled ? undefined : removeStep}
        disabled={disabled}
      />
      <AutomationNodePanel
        nodeId={selectedId}
        triggerType={value.trigger_type}
        triggerConditions={value.conditions}
        step={selectedStep}
        catalog={catalog}
        disabled={disabled}
        onTriggerChange={(trigger) =>
          // Conditions are cleared on a trigger change: they were written against
          // the old trigger's facts and would silently never match.
          onChange({ ...value, trigger_type: trigger, conditions: [] })
        }
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
