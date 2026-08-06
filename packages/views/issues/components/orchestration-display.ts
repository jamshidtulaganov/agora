import type {
  AgentTask,
  OrchestrationEvent,
  OrchestrationHandoff,
  OrchestrationRun,
  OrchestrationStage,
  OrchestrationStep,
} from "@agora/core/types";

export type OrchestrationDisplayKind = OrchestrationStage | "integration";

export const ORCHESTRATION_DISPLAY_ORDER: readonly OrchestrationDisplayKind[] = [
  "plan",
  "dev",
  "integration",
  "qa",
  "review",
  "release",
];

export interface OrchestrationDisplayGroup {
  kind: OrchestrationDisplayKind;
  steps: OrchestrationStep[];
}

/** A manual run may advance only from the clean, persisted between-batches pause. */
export function isManualOrchestrationBatchPaused(
  run: Pick<OrchestrationRun, "progression_policy" | "status" | "steps">,
): boolean {
  return run.progression_policy === "manual"
    && run.status === "waiting_approval"
    && run.steps.some((step) => step.status === "pending")
    && run.steps.every((step) => step.status === "completed" || step.status === "skipped" || step.status === "pending");
}

const GENERIC_TITLES: Record<OrchestrationDisplayKind, ReadonlySet<string>> = {
  plan: new Set(["plan", "planning", "plan task", "planning task"]),
  dev: new Set([
    "dev",
    "dev task",
    "development",
    "development task",
    "implementation",
    "implementation task",
  ]),
  integration: new Set(["integrate", "integration", "integration gate", "integration task"]),
  qa: new Set(["qa", "qa task", "quality assurance", "quality assurance task", "verification", "verification task"]),
  review: new Set(["code review", "review", "review task"]),
  release: new Set(["release", "release task"]),
};

function normalizeLabel(value: string): string {
  return value.trim().toLocaleLowerCase("en-US").replace(/[^a-z0-9]+/g, " ").trim();
}

function humanizeIdentifier(value: string): string {
  const label = value.trim().replace(/[_-]+/g, " ").replace(/\s+/g, " ");
  if (label.toLocaleLowerCase("en-US") === "qa") return "QA";
  return label ? `${label.charAt(0).toLocaleUpperCase("en-US")}${label.slice(1)}` : "";
}

/** Integration is persisted as a dev-stage step, but it is its own lifecycle gate. */
export function orchestrationDisplayKind(step: OrchestrationStep): OrchestrationDisplayKind {
  return step.kind === "integration" ? "integration" : step.stage;
}

/**
 * Build the visible lifecycle from the actual persisted graph. Empty stages are
 * deliberately absent, and every parallel branch remains a separate step.
 */
export function orchestrationDisplayGroups(steps: OrchestrationStep[]): OrchestrationDisplayGroup[] {
  const grouped = new Map<OrchestrationDisplayKind, OrchestrationStep[]>();
  const orderedSteps = [...steps].sort((left, right) => left.position - right.position || left.id.localeCompare(right.id));

  for (const step of orderedSteps) {
    const kind = orchestrationDisplayKind(step);
    const group = grouped.get(kind);
    if (group) group.push(step);
    else grouped.set(kind, [step]);
  }

  return ORCHESTRATION_DISPLAY_ORDER.flatMap((kind) => {
    const group = grouped.get(kind);
    return group ? [{ kind, steps: group }] : [];
  });
}

export function orchestrationTitleRepeatsKind(
  title: string,
  kind: OrchestrationDisplayKind,
): boolean {
  return GENERIC_TITLES[kind].has(normalizeLabel(title));
}

/** Prefer a specific persisted capability when a legacy title is only "Development task". */
export function orchestrationStepDisplayTitle(step: OrchestrationStep): string {
  const title = step.title.trim();
  const kind = orchestrationDisplayKind(step);
  if (title && !orchestrationTitleRepeatsKind(title, kind)) return title;

  const capability = humanizeIdentifier(step.capability);
  if (capability && !orchestrationTitleRepeatsKind(capability, kind) && normalizeLabel(capability) !== "coordination") {
    return capability;
  }

  const key = humanizeIdentifier(step.key);
  if (key && !orchestrationTitleRepeatsKind(key, kind) && !/^(step|task)( \d+)?$/i.test(key)) {
    return key;
  }

  return title || capability || key;
}

const OUTPUT_KEYS = ["handoff", "output", "summary", "result", "content", "message"] as const;

export function orchestrationHandoff(value: unknown): OrchestrationHandoff | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
  const candidate = value as Partial<OrchestrationHandoff>;
  if (
    candidate.schema_version !== 1 ||
    typeof candidate.summary !== "string" ||
    (candidate.outcome !== "completed" && candidate.outcome !== "waiting_input" && candidate.outcome !== "blocked")
  ) return undefined;
  return candidate as OrchestrationHandoff;
}

function outputText(value: unknown, depth = 0): string {
  if (typeof value === "string") {
    const text = value.trim();
    if (!text || depth > 1 || (!text.startsWith("{") && !text.startsWith("["))) return text;
    try {
      return outputText(JSON.parse(text) as unknown, depth + 1) || text;
    } catch {
      return text;
    }
  }
  if (typeof value !== "object" || value === null) return "";
  if (Array.isArray(value) && value.length === 0) return "";

  const record = value as Record<string, unknown>;
  if (Object.keys(record).length === 0) return "";
  for (const key of OUTPUT_KEYS) {
    if (!(key in record)) continue;
    const nested = outputText(record[key], depth + 1);
    if (nested) return nested;
  }

  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return "";
  }
}

/**
 * Extract the useful persisted handoff while removing daemon progress protocol
 * lines that are already represented by the live-work UI.
 */
export function orchestrationHandoffText(value: unknown): string {
  return outputText(value)
    .replace(/```todo[\s\S]*?```/gi, "")
    .split("\n")
    .filter((line) => !line.trim().startsWith("PROGRESS:"))
    .join("\n")
    .trim();
}

export interface CompletedOrchestrationHandoff {
  step: OrchestrationStep;
  output: string;
}

/** Select the most recently completed step that has a real persisted output. */
export function latestCompletedOrchestrationHandoff(
  steps: OrchestrationStep[],
  events: OrchestrationEvent[],
  tasks: AgentTask[],
): CompletedOrchestrationHandoff | undefined {
  const taskByID = new Map(tasks.map((task) => [task.id, task]));
  const eventTimeByStepID = new Map<string, number>();
  for (const event of events) {
    if (!event.step_id || (event.kind !== "step_completed" && event.kind !== "step_approved")) continue;
    const timestamp = new Date(event.created_at).getTime();
    if (!Number.isFinite(timestamp)) continue;
    eventTimeByStepID.set(event.step_id, Math.max(timestamp, eventTimeByStepID.get(event.step_id) ?? 0));
  }

  return steps
    .filter((step) => step.status === "completed")
    .map((step) => {
      const task = step.task_id ? taskByID.get(step.task_id) : undefined;
      const output = orchestrationHandoffText(step.output) || orchestrationHandoffText(task?.result);
      const taskCompletedAt = task?.completed_at ? new Date(task.completed_at).getTime() : 0;
      const completedAt = Math.max(
        Number.isFinite(taskCompletedAt) ? taskCompletedAt : 0,
        eventTimeByStepID.get(step.id) ?? 0,
      );
      return { step, output, completedAt };
    })
    .filter((candidate) => candidate.output.length > 0)
    .sort((left, right) => right.completedAt - left.completedAt || right.step.position - left.step.position)
    .map(({ step, output }) => ({ step, output }))[0];
}
