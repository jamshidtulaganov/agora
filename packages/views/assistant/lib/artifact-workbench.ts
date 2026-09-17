// Pure decisions behind the artifact workbench's two automatic behaviours:
// the pane that opens itself while the agent is building, and the "updating"
// hint on its header. Both are derived from the transcript + run cache the
// page already has — no new state, no new fetch — so they live here where
// they can be reasoned about (and tested) without React.

import type { AssistantMessage } from "@agora/core/types";
import { isArtifactToolName, parseArtifactToolResult } from "./artifact";

/**
 * The artifact this run most recently produced, or null.
 *
 * Messages carry no run id, so a run's rows are identified positionally: a run
 * starts at the user message it answers (`run.message_id`) and owns everything
 * after it. A run whose starting message isn't in the cache yet attributes
 * nothing — better to open nothing than to reopen an artifact from an earlier
 * turn the user already dismissed.
 */
export function runArtifactId(
  messages: AssistantMessage[],
  runMessageId: string | null | undefined,
): string | null {
  if (!runMessageId) return null;
  const start = messages.findIndex((message) => message.id === runMessageId);
  if (start < 0) return null;

  for (let index = messages.length - 1; index > start; index -= 1) {
    const message = messages[index]!;
    if (message.role !== "tool" || !isArtifactToolName(message.tool_name)) continue;
    const ref = parseArtifactToolResult(message.tool_result);
    if (ref) return ref.artifactId;
  }
  return null;
}

export interface AutoOpenInput {
  /** Latest run of the open session. */
  runId: string | null;
  /** Artifact that run produced (see `runArtifactId`). */
  artifactId: string | null;
  /** The run whose single auto-open has already been used. */
  lastAutoOpenedRunId: string | null;
  /** What the pane is showing right now. */
  openArtifactId: string | null;
}

export interface AutoOpenDecision {
  /** Artifact to switch the pane to; null leaves the pane exactly as it is. */
  artifactId: string | null;
  /** Run whose auto-open budget this decision spends; null spends nothing. */
  spentRunId: string | null;
}

const NO_AUTO_OPEN: AutoOpenDecision = { artifactId: null, spentRunId: null };

/**
 * Should the pane follow the agent to the artifact it just wrote?
 *
 * Exactly ONCE per run. The first artifact of a turn opens itself so the user
 * watches it being built; after that the pane belongs to the user. A second
 * `create_artifact` in the same turn, or a manual switch to something else
 * mid-run, never yanks the view — the caller records `spentRunId` (including
 * on a manual open) and the budget is gone until the next run.
 */
export function autoOpenDecision(input: AutoOpenInput): AutoOpenDecision {
  const { runId, artifactId, lastAutoOpenedRunId, openArtifactId } = input;
  if (!runId || !artifactId) return NO_AUTO_OPEN;
  if (lastAutoOpenedRunId === runId) return NO_AUTO_OPEN;
  // Already the open artifact: nothing to switch, but the run's budget is
  // spent — the user is watching it, and a later artifact must not steal that.
  if (openArtifactId === artifactId) return { artifactId: null, spentRunId: runId };
  return { artifactId, spentRunId: runId };
}

export interface UpdatingInput {
  isRunning: boolean;
  /** `run.active_tool` — the tool the model is executing right now. */
  activeTool: string | null | undefined;
  openArtifactId: string | null;
  /** Artifact this run has touched so far, if any. */
  runArtifactId: string | null;
}

/**
 * Is the open artifact being rewritten under the user's eyes?
 *
 * `active_tool` names the tool but not its target — the tool_result that
 * carries the artifact id only exists once the call has returned. So the
 * target is inferred: a run that has already touched an artifact is assumed
 * to still be on it, and a run that has touched none is assumed to be writing
 * whatever the user is looking at. Worst case the shimmer shows a beat early
 * on the wrong artifact; it is a hint, never a gate on content.
 */
export function isArtifactUpdating(input: UpdatingInput): boolean {
  const { isRunning, activeTool, openArtifactId, runArtifactId: touched } = input;
  if (!isRunning || !openArtifactId || !isArtifactToolName(activeTool)) return false;
  return touched === null || touched === openArtifactId;
}
