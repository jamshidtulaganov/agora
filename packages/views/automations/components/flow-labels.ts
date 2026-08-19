import type { AutomationCondition, AutomationStep } from "@agora/core/automations";

// Label + summary helpers for the flow canvas. Pure, so the wording is testable
// without rendering, and every lookup DOWNGRADES on an unknown value rather than
// showing nothing: the server owns the trigger/step/operator vocabulary, so a newer
// backend can send a node type this client has never heard of and the canvas must
// still describe it (CLAUDE.md "Enum drift downgrades, not crashes").

/** Turn a machine name into readable text: "tracker.stage_changed" → "tracker
 *  stage changed". Used as the fallback when no translation exists. */
export function humanizeMachineName(value: string): string {
  return value.replace(/[._]/g, " ").trim();
}

/** Look a key up in a translated map, falling back to the humanized raw value. */
export function labelFor(map: Record<string, string> | undefined, key: string): string {
  const trimmed = key.trim();
  if (trimmed === "") return "";
  const hit = map?.[trimmed];
  return hit && hit.trim() !== "" ? hit : humanizeMachineName(trimmed);
}

/** Conditions are stored as a single string or a list; the editor edits them as
 *  comma-separated text. These two keep that conversion in one place. */
export function conditionValueToText(value: AutomationCondition["value"]): string {
  if (Array.isArray(value)) return value.join(", ");
  return value ?? "";
}

export function textToConditionValue(text: string): string | string[] {
  const parts = text.split(",").map((part) => part.trim()).filter((part) => part !== "");
  if (parts.length <= 1) return parts[0] ?? "";
  return parts;
}

/** Operators that take no value, so the editor hides the value box. */
export function operatorTakesValue(op: string): boolean {
  return op.trim() !== "exists";
}

/** The two operators that test label MEMBERSHIP. The engine ignores the field
 *  for these (it reads the issue's label set), so the editor pairs them with the
 *  virtual "labels" field — any other field+op combination the panel could offer
 *  here would save an always-false rule. */
export const LABEL_MEMBERSHIP_OPS = ["has_label", "not_has_label"] as const;

export function isLabelMembershipOp(op: string): boolean {
  return (LABEL_MEMBERSHIP_OPS as readonly string[]).includes(op.trim());
}

/** A one-line summary for the list card: "when a label is attached → set the
 *  status, send a Telegram message". Built from the same labels the canvas shows so
 *  the list and the editor can never disagree. */
export function summarizeFlow(
  triggerLabel: string,
  steps: AutomationStep[],
  stepLabels: Record<string, string> | undefined,
): string {
  const stepText = steps
    .map((step) => labelFor(stepLabels, step.type))
    .filter((text) => text !== "")
    .join(", ");
  if (stepText === "") return triggerLabel;
  return `${triggerLabel} → ${stepText}`;
}

/** Which config fields a step type shows. Kept next to the labels so adding a step
 *  type is one edit in this file plus its translation. An unknown type shows no
 *  fields (rather than every field), so it round-trips untouched on save. */
export function stepConfigFields(type: string): string[] {
  switch (type.trim()) {
    case "dispatch_slice_action":
      return ["kind", "agent", "agent_id"];
    case "set_status":
      return ["status"];
    case "assign":
      return ["target", "agent_id"];
    case "add_label":
      return ["name"];
    case "remove_label":
      return ["name"];
    case "post_comment":
      return ["body"];
    case "send_telegram":
      return ["destination", "text", "chat_id"];
    default:
      return [];
  }
}

/** The one-line subtitle a canvas node shows under its title: the parameter that
 *  actually distinguishes this step from another of the same type ("todo", "the
 *  task's owner", "review:fail is …"). Falls back to "" rather than inventing text,
 *  so an unconfigured node reads as unconfigured. */
export function summarizeStep(
  step: AutomationStep,
  labels: {
    fields?: Record<string, string>;
    ops?: Record<string, string>;
    kinds?: Record<string, string>;
    statuses?: Record<string, string>;
    targets?: Record<string, string>;
    destinations?: Record<string, string>;
  },
): string {
  if (step.type === "filter") {
    const first = step.conditions?.[0];
    if (!first) return "";
    const field = labelFor(labels.fields, first.field);
    const op = labelFor(labels.ops, first.op);
    const value = conditionValueToText(first.value);
    const extra = (step.conditions?.length ?? 0) - 1;
    const head = [field, op, value].filter((part) => part !== "").join(" ");
    return extra > 0 ? `${head} +${extra}` : head;
  }
  const config = step.config ?? {};
  // Ordered by how much each key identifies the step at a glance. Machine values
  // (a slice-action kind, a status, a routing target) render through their
  // translations so the canvas never shows a wire name like "run_review".
  const translated: Array<[string, Record<string, string> | undefined]> = [
    ["kind", labels.kinds],
    ["status", labels.statuses],
    ["target", labels.targets],
    ["destination", labels.destinations],
  ];
  for (const [key, map] of translated) {
    const value = config[key];
    if (value && value.trim() !== "") return labelFor(map, value.trim());
  }
  for (const key of ["name", "body", "text"]) {
    const value = config[key];
    if (value && value.trim() !== "") {
      const trimmed = value.trim();
      return trimmed.length > 42 ? `${trimmed.slice(0, 41)}…` : trimmed;
    }
  }
  return "";
}

/** One selectable value for a condition field with a known domain. */
export interface FieldValueOption {
  value: string;
  label: string;
}

/** Which fields have an enumerable domain, and where it comes from. Free-text
 *  fields (a tracker column name, a title fragment) are absent on purpose — a
 *  picker over an open vocabulary would be a lie. */
export type FieldDomain =
  | "statuses"
  | "projects"
  | "labels"
  | "agents"
  | "assignees"
  | "priorities"
  | "assignee_types"
  | "actor_types";

export function fieldValueDomain(field: string): FieldDomain | undefined {
  switch (field.trim()) {
    case "status":
    case "from_status":
    case "to_status":
      return "statuses";
    case "project_id":
      return "projects";
    case "label":
    case "labels":
      return "labels";
    case "assignee_id":
      // Assignees are polymorphic (member or agent), so the domain offers both.
      return "assignees";
    case "priority":
      return "priorities";
    case "assignee_type":
      return "assignee_types";
    case "actor_type":
      return "actor_types";
    default:
      return undefined;
  }
}

/** Condition values are a string or a list; the pickers edit them as a list.
 *  These two normalize without losing a hand-typed free value. */
export function conditionValueList(value: AutomationCondition["value"]): string[] {
  if (Array.isArray(value)) return value.filter((item) => item.trim() !== "");
  const single = (value ?? "").trim();
  return single === "" ? [] : single.split(",").map((part) => part.trim()).filter((part) => part !== "");
}

export function listToConditionValue(values: string[]): string | string[] {
  const cleaned = values.map((item) => item.trim()).filter((item) => item !== "");
  if (cleaned.length <= 1) return cleaned[0] ?? "";
  return cleaned;
}
