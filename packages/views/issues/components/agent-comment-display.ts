const MACHINE_BLOCK =
  /```(todo|agora-handoff|knowledge-items|qa-manifest|qa-result|test-cases)\b\s*\n?([\s\S]*?)```/gi;

const VERIFICATION_HEAD_CONFLICT =
  /^complete task failed:[\s\S]*\b409\b[\s\S]*verification worktree changed or no longer matches the integrated HEAD/i;

function handoffSummary(raw: string): string {
  try {
    const value = JSON.parse(raw) as { summary?: unknown };
    return typeof value.summary === "string" ? value.summary.trim() : "";
  } catch {
    return "";
  }
}

/** Machine completion errors are already represented by the adjacent task_failed activity. */
export function isRedundantAgentCompletionFailure(
  content: string,
  actorType: string,
): boolean {
  return (actorType === "agent" || actorType === "system")
    && VERIFICATION_HEAD_CONFLICT.test(content.trim());
}

/**
 * Keep agent protocol in storage while presenting only its human-readable
 * prose in the issue conversation. Structured blocks have dedicated product
 * surfaces (plan, QA, review, knowledge) and should not become raw JSON cards.
 */
export function humanReadableAgentComment(
  content: string,
  actorType: string,
): string {
  if (actorType !== "agent" && actorType !== "system") return content;

  let fallbackSummary = "";
  const cleaned = content
    .replace(MACHINE_BLOCK, (_match, kind: string, raw: string) => {
      if (kind.toLowerCase() === "agora-handoff") {
        fallbackSummary ||= handoffSummary(raw);
      }
      return "";
    })
    .replace(/\n{3,}/g, "\n\n")
    .trim();

  return cleaned || fallbackSummary;
}
