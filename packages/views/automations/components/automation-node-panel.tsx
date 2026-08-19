"use client";

import { Plus, Trash2, X } from "lucide-react";
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

// The node parameter panel — n8n's node-details view, adapted: the canvas shows the
// SHAPE of the flow, this panel edits ONE node. Splitting them is what removed the
// long scrolling form: the page no longer grows with the number of steps.

interface AutomationNodePanelProps {
  /** "trigger" or the step index as a string; "" when nothing is selected. */
  nodeId: string;
  triggerType: string;
  triggerConditions: AutomationCondition[];
  step?: AutomationStep;
  catalog: AutomationCatalog;
  disabled?: boolean;
  onTriggerChange: (trigger: string) => void;
  onTriggerConditionsChange: (conditions: AutomationCondition[]) => void;
  onStepChange: (step: AutomationStep) => void;
  onRemoveStep: () => void;
  onClose: () => void;
}

export function AutomationNodePanel({
  nodeId,
  triggerType,
  triggerConditions,
  step,
  catalog,
  disabled,
  onTriggerChange,
  onTriggerConditionsChange,
  onStepChange,
  onRemoveStep,
  onClose,
}: AutomationNodePanelProps) {
  const { t } = useT("automations");
  const triggerLabels = t(($) => $.trigger, { returnObjects: true }) as Record<string, string>;
  const stepLabels = t(($) => $.step, { returnObjects: true }) as Record<string, string>;

  const triggerInfo = catalog.triggers.find((entry) => entry.type === triggerType);
  const fields = triggerInfo?.fields ?? [];
  const isTrigger = nodeId === "trigger";

  if (nodeId === "") {
    return (
      <aside className="rounded-lg border bg-card p-4 text-sm text-muted-foreground">
        {t(($) => $.flow.select_node)}
      </aside>
    );
  }

  return (
    <aside className="flex max-h-[min(58vh,520px)] flex-col overflow-hidden rounded-lg border bg-card">
      <header className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <h3 className="truncate text-sm font-medium">
          {isTrigger
            ? labelFor(triggerLabels, triggerType)
            : labelFor(stepLabels, step?.type ?? "")}
        </h3>
        <div className="flex items-center gap-1">
          {!isTrigger && (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t(($) => $.flow.remove)}
              disabled={disabled}
              onClick={onRemoveStep}
            >
              <Trash2 aria-hidden />
            </Button>
          )}
          <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.flow.close_panel)} onClick={onClose}>
            <X aria-hidden />
          </Button>
        </div>
      </header>

      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        {isTrigger ? (
          <>
            <LabeledRow label={t(($) => $.flow.trigger_type)}>
              <NativeSelect
                aria-label={t(($) => $.flow.trigger_type)}
                value={triggerType}
                disabled={disabled}
                onChange={(event) => onTriggerChange(event.target.value)}
              >
                {catalog.triggers.map((entry) => (
                  <NativeSelectOption key={entry.type} value={entry.type}>
                    {labelFor(triggerLabels, entry.type)}
                  </NativeSelectOption>
                ))}
                {/* A stored trigger the catalog no longer lists stays selectable so
                    an older flow round-trips instead of silently switching. */}
                {triggerType !== "" && !triggerInfo && (
                  <NativeSelectOption value={triggerType}>{labelFor(triggerLabels, triggerType)}</NativeSelectOption>
                )}
              </NativeSelect>
            </LabeledRow>
            <ConditionRows
              conditions={triggerConditions}
              fields={fields}
              operators={catalog.operators}
              disabled={disabled}
              onChange={onTriggerConditionsChange}
              emptyHint={t(($) => $.flow.no_conditions)}
            />
          </>
        ) : step ? (
          <>
            <LabeledRow label={t(($) => $.flow.step_type)}>
              <NativeSelect
                aria-label={t(($) => $.flow.step_type)}
                value={step.type}
                disabled={disabled}
                onChange={(event) =>
                  // Switching type drops the old config: its keys belong to the
                  // previous step and would be saved as dead weight.
                  onStepChange({ type: event.target.value, config: {}, conditions: [] })
                }
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
            </LabeledRow>

            {step.type === "filter" ? (
              <>
                <ConditionRows
                  conditions={step.conditions ?? []}
                  fields={fields}
                  operators={catalog.operators}
                  disabled={disabled}
                  onChange={(conditions) => onStepChange({ ...step, conditions })}
                  emptyHint={t(($) => $.flow.filter_hint)}
                />
                <p className="text-xs text-muted-foreground">{t(($) => $.flow.filter_hint)}</p>
              </>
            ) : (
              <StepConfigFields
                step={step}
                catalog={catalog}
                disabled={disabled}
                onChange={(config) => onStepChange({ ...step, config })}
              />
            )}
          </>
        ) : null}
      </div>

      <footer className="border-t px-3 py-2">
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.flow.guard_note, {
            seconds: catalog.min_interval_default,
            max: catalog.max_per_hour_default,
          })}
        </p>
      </footer>
    </aside>
  );
}

// ConditionRows edits one list of clauses, laid out the way n8n's IF node does
// it: each condition is a card of three STACKED, full-width, labelled controls
// (field / condition / value) — never side-by-side selects that truncate "is none
// of" into "is none o…" — with an AND chip between cards, because every clause
// must hold. OR is expressed by writing a second automation, which keeps both
// this panel and the audit trail readable.
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

  // The comma hint only matters for list-shaped operators, and only under the
  // value it applies to — a standing footer read as noise on every condition.
  const listOp = (op: string) => ["in", "not_in", "contains"].includes(op.trim());

  return (
    <div className="space-y-0">
      {conditions.length === 0 && <p className="text-xs text-muted-foreground">{emptyHint}</p>}
      {conditions.map((condition, index) => (
        <div key={index}>
          {index > 0 && (
            <div className="flex justify-center py-1" aria-hidden>
              <span className="rounded-full border bg-muted px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {t(($) => $.flow.and)}
              </span>
            </div>
          )}
          <div className="relative space-y-2 rounded-md border bg-background p-2.5 pr-8">
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t(($) => $.flow.remove)}
              disabled={disabled}
              className="absolute right-1 top-1 size-6 text-muted-foreground"
              onClick={() => onChange(conditions.filter((_, i) => i !== index))}
            >
              <X className="size-3" aria-hidden />
            </Button>

            <label className="block space-y-1">
              <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                {t(($) => $.flow.field)}
              </span>
              <NativeSelect
                aria-label={t(($) => $.flow.field)}
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
            </label>

            <label className="block space-y-1">
              <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                {t(($) => $.flow.operator)}
              </span>
              <NativeSelect
                aria-label={t(($) => $.flow.operator)}
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
            </label>

            {operatorTakesValue(condition.op) && (
              <label className="block space-y-1">
                <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                  {t(($) => $.flow.value)}
                </span>
                <Input
                  aria-label={t(($) => $.flow.value)}
                  className="h-9"
                  placeholder={t(($) => $.flow.value_placeholder)}
                  value={conditionValueToText(condition.value)}
                  disabled={disabled}
                  onChange={(event) => update(index, { value: textToConditionValue(event.target.value) })}
                />
                {listOp(condition.op) && (
                  <span className="block text-[11px] leading-snug text-muted-foreground">
                    {t(($) => $.flow.value_list_hint)}
                  </span>
                )}
              </label>
            )}
          </div>
        </div>
      ))}
      <Button
        size="sm"
        variant="outline"
        className="mt-2 h-7 w-full text-xs"
        disabled={disabled}
        onClick={() => onChange([...conditions, { field: fields[0] ?? "status", op: operators[0] ?? "eq", value: "" }])}
      >
        <Plus className="size-3" aria-hidden />
        {t(($) => $.flow.add_condition)}
      </Button>
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
  const kindLabels = t(($) => $.kind, { returnObjects: true }) as Record<string, string>;
  const statusLabels = t(($) => $.status, { returnObjects: true }) as Record<string, string>;
  const config = step.config ?? {};
  const fields = stepConfigFields(step.type);
  const set = (key: string, value: string) => onChange({ ...config, [key]: value });

  if (fields.length === 0) return null;

  return (
    <div className="space-y-2">
      {fields.includes("kind") && (
        <LabeledRow label={t(($) => $.config.kind)}>
          <NativeSelect value={config.kind ?? ""} disabled={disabled} onChange={(event) => set("kind", event.target.value)}>
            <NativeSelectOption value="">—</NativeSelectOption>
            {catalog.slice_action_kinds.map((kind) => (
              <NativeSelectOption key={kind} value={kind}>
                {labelFor(kindLabels, kind)}
              </NativeSelectOption>
            ))}
            {config.kind && !catalog.slice_action_kinds.includes(config.kind) && (
              <NativeSelectOption value={config.kind}>{labelFor(kindLabels, config.kind)}</NativeSelectOption>
            )}
          </NativeSelect>
        </LabeledRow>
      )}

      {fields.includes("agent") && (
        <LabeledRow label={t(($) => $.config.agent)}>
          <NativeSelect value={config.agent ?? ""} disabled={disabled} onChange={(event) => set("agent", event.target.value)}>
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
          <NativeSelect value={config.status ?? ""} disabled={disabled} onChange={(event) => set("status", event.target.value)}>
            <NativeSelectOption value="">—</NativeSelectOption>
            {catalog.statuses.map((status) => (
              <NativeSelectOption key={status} value={status}>
                {labelFor(statusLabels, status)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </LabeledRow>
      )}

      {fields.includes("target") && (
        <LabeledRow label={t(($) => $.config.target)}>
          <NativeSelect value={config.target ?? ""} disabled={disabled} onChange={(event) => set("target", event.target.value)}>
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
          <Input className="h-9" value={config.name ?? ""} disabled={disabled} onChange={(event) => set("name", event.target.value)} />
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
            rows={4}
            value={config.body ?? config.text ?? ""}
            disabled={disabled}
            placeholder={fields.includes("body") ? t(($) => $.config.body) : t(($) => $.config.text)}
            onChange={(event) => set(fields.includes("body") ? "body" : "text", event.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t(($) => $.flow.template_hint)}</p>
        </div>
      )}

      {/* The room is resolved from the agent's own bound group, so an explicit chat
          id is an override — offered only for a group send. */}
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

      {/* agent_id only matters once a specific agent was chosen. */}
      {fields.includes("agent_id") && (config.target === "agent" || config.agent === "agent") && (
        <LabeledRow label={t(($) => $.config.agent_id)}>
          <Input className="h-9" value={config.agent_id ?? ""} disabled={disabled} onChange={(event) => set("agent_id", event.target.value)} />
        </LabeledRow>
      )}
    </div>
  );
}

function LabeledRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1 text-xs text-muted-foreground">
      <span>{label}</span>
      {children}
    </label>
  );
}
