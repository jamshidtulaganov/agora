"use client";

import { ArrowDown, ArrowUp, Filter, GitBranch, Plus, Trash2, Zap } from "lucide-react";
import type {
  AutomationCatalog,
  AutomationCondition,
  AutomationStep,
} from "@agora/core/automations";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { useT } from "../../i18n";
import {
  conditionValueToText,
  labelFor,
  operatorTakesValue,
  stepConfigFields,
  textToConditionValue,
} from "./flow-labels";

// The flow canvas: a trigger node, then one node per step, connected top-to-bottom.
// It is a CONTROLLED component — the page owns the draft and the save — so the
// canvas has no state of its own to drift from what gets written.
//
// The layout is deliberately a vertical stack rather than a free 2D graph: a task
// automation is a sequence, and a sequence is easier to read, keyboard-navigate and
// review than a canvas the user has to arrange. Branching, when it arrives, is a
// filter node with two outputs — the model already allows it.

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

  // Fields offered to the condition rows come from the catalog entry for the CHOSEN
  // trigger, so a rule cannot be written against a fact this event never carries.
  const triggerInfo = catalog.triggers.find((entry) => entry.type === value.trigger_type);
  const fields = triggerInfo?.fields ?? [];

  const setTrigger = (trigger: string) => {
    // Conditions are cleared on a trigger change: they were written against the old
    // trigger's facts and would silently never match under the new one.
    onChange({ ...value, trigger_type: trigger, conditions: [] });
  };

  const setConditions = (conditions: AutomationCondition[]) => onChange({ ...value, conditions });
  const setSteps = (actions: AutomationStep[]) => onChange({ ...value, actions });

  const addStep = (index: number) => {
    const next = [...value.actions];
    next.splice(index, 0, { type: catalog.steps[1] ?? "set_status", config: {} });
    setSteps(next);
  };

  const moveStep = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= value.actions.length) return;
    const next = [...value.actions];
    const [moved] = next.splice(index, 1);
    if (!moved) return;
    next.splice(target, 0, moved);
    setSteps(next);
  };

  return (
    <div className="space-y-0">
      {/* Trigger node */}
      <FlowNode
        icon={<Zap className="size-3.5" aria-hidden />}
        kicker={t(($) => $.flow.when)}
        accent
      >
        <NativeSelect
          aria-label={t(($) => $.flow.trigger_type)}
          value={value.trigger_type}
          disabled={disabled}
          onChange={(event) => setTrigger(event.target.value)}
        >
          {catalog.triggers.map((entry) => (
            <NativeSelectOption key={entry.type} value={entry.type}>
              {labelFor(triggerLabels, entry.type)}
            </NativeSelectOption>
          ))}
          {/* A stored trigger the catalog no longer lists stays selectable so an
              older flow round-trips instead of silently switching to another one. */}
          {value.trigger_type !== "" && !triggerInfo && (
            <NativeSelectOption value={value.trigger_type}>
              {labelFor(triggerLabels, value.trigger_type)}
            </NativeSelectOption>
          )}
        </NativeSelect>

        <ConditionRows
          conditions={value.conditions}
          fields={fields}
          operators={catalog.operators}
          disabled={disabled}
          onChange={setConditions}
          emptyHint={t(($) => $.flow.no_conditions)}
        />
      </FlowNode>

      <FlowConnector onAdd={disabled ? undefined : () => addStep(0)} label={t(($) => $.flow.add_step)} />

      {/* Step nodes */}
      {value.actions.map((step, index) => (
        <div key={index}>
          <FlowNode
            icon={step.type === "filter" ? <Filter className="size-3.5" aria-hidden /> : <GitBranch className="size-3.5" aria-hidden />}
            kicker={index === 0 ? t(($) => $.flow.then) : undefined}
            actions={
              <div className="flex items-center gap-1">
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t(($) => $.flow.move_up)}
                  disabled={disabled || index === 0}
                  onClick={() => moveStep(index, -1)}
                >
                  <ArrowUp aria-hidden />
                </Button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t(($) => $.flow.move_down)}
                  disabled={disabled || index === value.actions.length - 1}
                  onClick={() => moveStep(index, 1)}
                >
                  <ArrowDown aria-hidden />
                </Button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t(($) => $.flow.remove)}
                  disabled={disabled}
                  onClick={() => setSteps(value.actions.filter((_, i) => i !== index))}
                >
                  <Trash2 aria-hidden />
                </Button>
              </div>
            }
          >
            <NativeSelect
              aria-label={t(($) => $.flow.step_type)}
              value={step.type}
              disabled={disabled}
              onChange={(event) => {
                // Switching type drops the old config: its keys belong to the
                // previous step and would be saved as dead weight the engine
                // ignores but a reviewer would puzzle over.
                const next = [...value.actions];
                next[index] = { type: event.target.value, config: {}, conditions: [] };
                setSteps(next);
              }}
            >
              {catalog.steps.map((type) => (
                <NativeSelectOption key={type} value={type}>
                  {labelFor(stepLabels, type)}
                </NativeSelectOption>
              ))}
              {step.type !== "" && !catalog.steps.includes(step.type) && (
                <NativeSelectOption value={step.type}>{labelFor(stepLabels, step.type)}</NativeSelectOption>
              )}
            </NativeSelect>

            {step.type === "filter" ? (
              <>
                <ConditionRows
                  conditions={step.conditions ?? []}
                  fields={fields}
                  operators={catalog.operators}
                  disabled={disabled}
                  onChange={(conditions) => {
                    const next = [...value.actions];
                    next[index] = { ...step, conditions };
                    setSteps(next);
                  }}
                  emptyHint={t(($) => $.flow.filter_hint)}
                />
                <p className="text-xs text-muted-foreground">{t(($) => $.flow.filter_hint)}</p>
              </>
            ) : (
              <StepConfigFields
                step={step}
                catalog={catalog}
                disabled={disabled}
                onChange={(config) => {
                  const next = [...value.actions];
                  next[index] = { ...step, config };
                  setSteps(next);
                }}
              />
            )}
          </FlowNode>
          <FlowConnector
            onAdd={disabled ? undefined : () => addStep(index + 1)}
            label={t(($) => $.flow.add_step)}
            last={index === value.actions.length - 1}
          />
        </div>
      ))}

      <p className="pt-2 text-xs text-muted-foreground">
        {t(($) => $.flow.guard_note, {
          seconds: catalog.min_interval_default,
          max: catalog.max_per_hour_default,
        })}
      </p>
    </div>
  );
}

// FlowNode is the node chrome: an icon rail on the left, content on the right, and
// an optional action cluster. The rail is what makes the stack read as a connected
// flow rather than a list of forms.
function FlowNode({
  icon,
  kicker,
  accent,
  actions,
  children,
}: {
  icon: React.ReactNode;
  kicker?: string;
  accent?: boolean;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border bg-card p-3">
      <div className="flex items-start gap-3">
        <span
          className={
            accent
              ? "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-brand/10 text-brand"
              : "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground"
          }
        >
          {icon}
        </span>
        <div className="min-w-0 flex-1 space-y-2">
          {kicker && <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{kicker}</p>}
          {children}
        </div>
        {actions}
      </div>
    </div>
  );
}

// The connector line between two nodes, carrying the "add a step here" affordance.
function FlowConnector({ onAdd, label, last }: { onAdd?: () => void; label: string; last?: boolean }) {
  return (
    <div className="flex items-center gap-2 py-1 pl-6">
      <span className={last ? "h-4 w-px bg-border" : "h-6 w-px bg-border"} aria-hidden />
      {onAdd && (
        <Button size="sm" variant="ghost" className="h-6 gap-1 px-2 text-xs text-muted-foreground" onClick={onAdd}>
          <Plus className="size-3" aria-hidden />
          {label}
        </Button>
      )}
    </div>
  );
}

// ConditionRows edits one list of clauses. All clauses must hold; OR is expressed by
// writing a second automation, which keeps this UI (and the audit trail) readable.
function ConditionRows({
  conditions,
  fields,
  operators,
  disabled,
  onChange,
  emptyHint,
}: {
  conditions: AutomationCondition[];
  fields: string[];
  operators: string[];
  disabled?: boolean;
  onChange: (next: AutomationCondition[]) => void;
  emptyHint: string;
}) {
  const { t } = useT("automations");
  const fieldLabels = t(($) => $.field, { returnObjects: true }) as Record<string, string>;
  const opLabels = t(($) => $.op, { returnObjects: true }) as Record<string, string>;

  const update = (index: number, patch: Partial<AutomationCondition>) => {
    const next = [...conditions];
    const current = next[index];
    if (!current) return;
    next[index] = { ...current, ...patch };
    onChange(next);
  };

  return (
    <div className="space-y-2">
      {conditions.length === 0 && <p className="text-xs text-muted-foreground">{emptyHint}</p>}
      {conditions.map((condition, index) => (
        <div key={index} className="flex flex-wrap items-center gap-2">
          <NativeSelect
            aria-label={t(($) => $.flow.field)}
            className="w-auto min-w-32"
            value={condition.field}
            disabled={disabled}
            onChange={(event) => update(index, { field: event.target.value })}
          >
            {fields.map((field) => (
              <NativeSelectOption key={field} value={field}>
                {labelFor(fieldLabels, field)}
              </NativeSelectOption>
            ))}
            <NativeSelectOption value="labels">{labelFor(fieldLabels, "labels")}</NativeSelectOption>
            {condition.field !== "" && !fields.includes(condition.field) && condition.field !== "labels" && (
              <NativeSelectOption value={condition.field}>{labelFor(fieldLabels, condition.field)}</NativeSelectOption>
            )}
          </NativeSelect>

          <NativeSelect
            aria-label={t(($) => $.flow.operator)}
            className="w-auto min-w-28"
            value={condition.op}
            disabled={disabled}
            onChange={(event) => update(index, { op: event.target.value })}
          >
            {operators.map((op) => (
              <NativeSelectOption key={op} value={op}>
                {labelFor(opLabels, op)}
              </NativeSelectOption>
            ))}
            {condition.op !== "" && !operators.includes(condition.op) && (
              <NativeSelectOption value={condition.op}>{labelFor(opLabels, condition.op)}</NativeSelectOption>
            )}
          </NativeSelect>

          {operatorTakesValue(condition.op) && (
            <Input
              aria-label={t(($) => $.flow.value)}
              className="h-9 w-auto min-w-40 flex-1"
              placeholder={t(($) => $.flow.value_placeholder)}
              value={conditionValueToText(condition.value)}
              disabled={disabled}
              onChange={(event) => update(index, { value: textToConditionValue(event.target.value) })}
            />
          )}

          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={t(($) => $.flow.remove)}
            disabled={disabled}
            onClick={() => onChange(conditions.filter((_, i) => i !== index))}
          >
            <Trash2 aria-hidden />
          </Button>
        </div>
      ))}
      <Button
        size="sm"
        variant="outline"
        className="h-7 text-xs"
        disabled={disabled}
        onClick={() => onChange([...conditions, { field: fields[0] ?? "status", op: operators[0] ?? "eq", value: "" }])}
      >
        <Plus className="size-3" aria-hidden />
        {t(($) => $.flow.add_condition)}
      </Button>
      {conditions.length > 0 && <p className="text-xs text-muted-foreground">{t(($) => $.flow.value_list_hint)}</p>}
    </div>
  );
}

// StepConfigFields renders the inputs for one step type. Which fields appear comes
// from stepConfigFields, so an unknown step type shows none and its stored config
// round-trips untouched.
function StepConfigFields({
  step,
  catalog,
  disabled,
  onChange,
}: {
  step: AutomationStep;
  catalog: AutomationCatalog;
  disabled?: boolean;
  onChange: (config: Record<string, string>) => void;
}) {
  const { t } = useT("automations");
  const targetLabels = t(($) => $.target, { returnObjects: true }) as Record<string, string>;
  const destinationLabels = t(($) => $.destination, { returnObjects: true }) as Record<string, string>;
  const config = step.config ?? {};
  const fields = stepConfigFields(step.type);
  const set = (key: string, value: string) => onChange({ ...config, [key]: value });

  if (fields.length === 0) return null;

  return (
    <div className="space-y-2">
      {fields.includes("kind") && (
        <LabeledRow label={t(($) => $.config.kind)}>
          <NativeSelect
            value={config.kind ?? ""}
            disabled={disabled}
            onChange={(event) => set("kind", event.target.value)}
          >
            <NativeSelectOption value="">—</NativeSelectOption>
            {catalog.slice_action_kinds.map((kind) => (
              <NativeSelectOption key={kind} value={kind}>
                {kind}
              </NativeSelectOption>
            ))}
            {config.kind && !catalog.slice_action_kinds.includes(config.kind) && (
              <NativeSelectOption value={config.kind}>{config.kind}</NativeSelectOption>
            )}
          </NativeSelect>
        </LabeledRow>
      )}

      {fields.includes("agent") && (
        <LabeledRow label={t(($) => $.config.agent)}>
          <NativeSelect
            value={config.agent ?? ""}
            disabled={disabled}
            onChange={(event) => set("agent", event.target.value)}
          >
            {catalog.agent_selectors.map((selector) => (
              <NativeSelectOption key={selector || "default"} value={selector}>
                {selector === "" ? t(($) => $.config.agent_default) : labelFor(targetLabels, selector)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </LabeledRow>
      )}

      {fields.includes("status") && (
        <LabeledRow label={t(($) => $.config.status)}>
          <NativeSelect
            value={config.status ?? ""}
            disabled={disabled}
            onChange={(event) => set("status", event.target.value)}
          >
            <NativeSelectOption value="">—</NativeSelectOption>
            {catalog.statuses.map((status) => (
              <NativeSelectOption key={status} value={status}>
                {status}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </LabeledRow>
      )}

      {fields.includes("target") && (
        <LabeledRow label={t(($) => $.config.target)}>
          <NativeSelect
            value={config.target ?? ""}
            disabled={disabled}
            onChange={(event) => set("target", event.target.value)}
          >
            <NativeSelectOption value="">—</NativeSelectOption>
            {catalog.assign_targets.map((target) => (
              <NativeSelectOption key={target} value={target}>
                {labelFor(targetLabels, target)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </LabeledRow>
      )}

      {fields.includes("name") && (
        <LabeledRow label={t(($) => $.config.label_name)}>
          <Input
            className="h-9"
            value={config.name ?? ""}
            disabled={disabled}
            onChange={(event) => set("name", event.target.value)}
          />
        </LabeledRow>
      )}

      {fields.includes("destination") && (
        <LabeledRow label={t(($) => $.config.destination)}>
          <NativeSelect
            value={config.destination ?? ""}
            disabled={disabled}
            onChange={(event) => set("destination", event.target.value)}
          >
            <NativeSelectOption value="">—</NativeSelectOption>
            {catalog.telegram_targets.map((target) => (
              <NativeSelectOption key={target} value={target}>
                {labelFor(destinationLabels, target)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </LabeledRow>
      )}

      {(fields.includes("body") || fields.includes("text")) && (
        <div className="space-y-1">
          <Textarea
            rows={3}
            value={config.body ?? config.text ?? ""}
            disabled={disabled}
            placeholder={fields.includes("body") ? t(($) => $.config.body) : t(($) => $.config.text)}
            onChange={(event) => set(fields.includes("body") ? "body" : "text", event.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t(($) => $.flow.template_hint)}</p>
        </div>
      )}

      {/* The room is resolved from the agent's own bound group, so an explicit chat
          id is an override — offered only for a group send, and only as a way to
          post somewhere other than that group. */}
      {fields.includes("chat_id") && config.destination === "group" && (
        <LabeledRow label={t(($) => $.config.chat_id)}>
          <Input
            className="h-9"
            placeholder={t(($) => $.config.chat_id_placeholder)}
            value={config.chat_id ?? ""}
            disabled={disabled}
            onChange={(event) => set("chat_id", event.target.value)}
          />
        </LabeledRow>
      )}

      {/* agent_id only matters when a specific agent was chosen, so it stays hidden
          until then instead of asking for a uuid nobody has. */}
      {fields.includes("agent_id") && (config.target === "agent" || config.agent === "agent") && (
        <LabeledRow label={t(($) => $.config.agent_id)}>
          <Input
            className="h-9"
            value={config.agent_id ?? ""}
            disabled={disabled}
            onChange={(event) => set("agent_id", event.target.value)}
          />
        </LabeledRow>
      )}
    </div>
  );
}

function LabeledRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span className="min-w-20">{label}</span>
      <span className="min-w-40 flex-1">{children}</span>
    </label>
  );
}
