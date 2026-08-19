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
