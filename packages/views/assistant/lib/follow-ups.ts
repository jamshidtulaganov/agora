// Follow-up suggestions under a finished reply.
//
// Deterministic and free: the chips are derived from the tool the run
// actually called, never from a second model call. The bar is deliberately
// high — chips appear only when EXACTLY ONE recognized tool ran in the final
// turn, so a multi-step run (where "Assign it to someone" would be ambiguous
// about which "it") shows nothing at all. Silence beats a wrong guess.

export type FollowUpId =
  | "assign_issue"
  | "set_due_date"
  | "add_issue_context"
  | "add_project_issues"
  | "compare_last_week"
  | "usage_by_agent"
  | "create_from_results"
  | "summarize_results"
  | "what_is_urgent"
  | "what_needs_me";

/** Tool name → the follow-ups that make sense right after it. */
const FOLLOW_UPS: Readonly<Record<string, readonly FollowUpId[]>> = {
  // Mutating
  create_issue: ["assign_issue", "set_due_date", "add_issue_context"],
  create_project: ["add_project_issues"],
  // Analytics
  usage_summary: ["compare_last_week", "usage_by_agent"],
  activity_digest: ["what_needs_me"],
  // Read
  search_issues: ["create_from_results", "summarize_results"],
  list_my_issues: ["what_is_urgent"],
};

export const MAX_FOLLOW_UPS = 3;

interface TranscriptRow {
  role: string;
  tool_name?: string | null;
}

/**
 * Tool names called after the last user turn — i.e. what THIS reply did.
 * Anything before that belongs to an earlier exchange.
 */
export function toolNamesInFinalTurn(messages: readonly TranscriptRow[]): string[] {
  let lastUserIndex = -1;
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    if (messages[i]!.role === "user") {
      lastUserIndex = i;
      break;
    }
  }
  const names: string[] = [];
  for (const message of messages.slice(lastUserIndex + 1)) {
    if (message.role === "tool" && message.tool_name) names.push(message.tool_name);
  }
  return names;
}

/** Up to MAX_FOLLOW_UPS suggestions, or none when the turn was ambiguous. */
export function followUpsForTools(toolNames: readonly string[]): FollowUpId[] {
  const recognized = toolNames.filter((name) => name in FOLLOW_UPS);
  if (recognized.length !== 1) return [];
  return [...(FOLLOW_UPS[recognized[0]!] ?? [])].slice(0, MAX_FOLLOW_UPS);
}

/** Convenience: the chips for a finished transcript. */
export function followUpsForTranscript(messages: readonly TranscriptRow[]): FollowUpId[] {
  return followUpsForTools(toolNamesInFinalTurn(messages));
}
