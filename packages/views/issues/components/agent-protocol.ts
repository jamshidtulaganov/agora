// Detects "agent-protocol" comments — the machine instructions the
// orchestrator posts to summon another agent (a slice action: run_qa,
// write_tests, review_part, …). These carry a full, multi-paragraph prompt as
// the comment body so the target agent can execute; a human reading the thread
// sees an unreadable wall of text. The UI uses this to render a short headline
// (+ a diagram for QA) and collapse the raw prompt behind a disclosure.
//
// Pure + dependency-free so it's unit-testable and safe to reuse. Heuristic by
// design (works retroactively on already-posted comments): a protocol comment
// starts with a single agent/squad @mention link and carries a long
// instruction. Short human @mentions ("@Aria can you check this?") are left
// alone.

/** Recognized slice-action intents, for the headline + diagram. */
export type AgentProtocolKind =
  | "run_qa"
  | "write_tests"
  | "write_docs"
  | "review"
  | "gen_tests"
  | "design"
  | "delegate";

export interface AgentProtocol {
  /** Display name from the leading [@Name](mention://…) link. */
  agentName: string;
  kind: AgentProtocolKind;
  /** The full instruction (everything after the leading mention link). */
  instruction: string;
}

// Leading mention link: the orchestrator always prefixes the target with
// [@Name](mention://agent|squad/<uuid>). ParseMentions on the backend scans
// anywhere, but a genuine PROTOCOL comment leads with it.
const LEADING_MENTION =
  /^\s*\[@([^\]]+)\]\(mention:\/\/(?:agent|squad)\/[0-9a-fA-F-]+\)\s*/;

// Explicit backend marker: the slice-action dispatcher prepends
// <!--agent-protocol:<backend-kind>--> so classification is EXACT (not the
// wording heuristic below). An HTML comment → invisible in markdown, inert to
// the agent. Backend kinds (run_qa, gen_test_cases, review_part, …) map to the
// display kinds via BACKEND_KIND.
const PROTOCOL_MARKER = /^\s*<!--\s*agent-protocol:([a-z_]+)\s*-->\s*/;

const BACKEND_KIND: Record<string, AgentProtocolKind> = {
  run_qa: "run_qa",
  run_review: "review",
  review_part: "review",
  auto_docs: "write_docs",
  write_docs: "write_docs",
  write_tests: "write_tests",
  gen_test_cases: "gen_tests",
  run_test_cases: "gen_tests",
  compile_tests: "gen_tests",
  design_proposal: "design",
  gen_design_manifest: "design",
  design_audit: "design",
  draft_code: "delegate",
  deploy: "delegate",
  run_ci: "delegate",
};

// Below this instruction length it's a normal human @mention, not a machine
// prompt — leave it rendered verbatim.
const MIN_INSTRUCTION_LEN = 280;

// First match wins. Ordered so the QA-lead delegation ("As the QA LEAD …")
// still classifies as run_qa, and gen/gather test-cases doesn't shadow run_qa.
const KIND_RULES: Array<{ kind: AgentProtocolKind; re: RegExp }> = [
  { kind: "run_qa", re: /DETERMINISTIC gate|Run QA for this issue|As the QA LEAD|qa:pass|qa:fail/i },
  { kind: "gen_tests", re: /author test cases|generate test cases|write test cases that assert/i },
  { kind: "write_tests", re: /\bWrite tests\b/i },
  { kind: "write_docs", re: /\bWrite documentation\b|auto[_ ]?docs/i },
  { kind: "review", re: /\bReview the relevant part\b|post your findings as a comment/i },
  { kind: "design", re: /design[-_ ]?proposal|design manifest|design audit/i },
];

/**
 * Parse a comment into an {@link AgentProtocol} when it is a machine
 * instruction the orchestrator posted to drive an agent; null otherwise.
 * `authorType` gates it to agent/system authors (a human's long @mention is
 * still a human message).
 */
export function parseAgentProtocol(
  content: string,
  authorType: string,
): AgentProtocol | null {
  // Explicit backend marker wins — exact kind, robust to instruction wording.
  let rest = content;
  let markedKind: AgentProtocolKind | null = null;
  const mk = PROTOCOL_MARKER.exec(content);
  if (mk) {
    markedKind = BACKEND_KIND[mk[1] ?? ""] ?? "delegate";
    rest = content.slice(mk[0].length);
  }

  // The authorType gate applies ONLY to the unmarked heuristic path: a human's
  // genuine long @mention must not be mistaken for a machine prompt. An EXPLICIT
  // marker is unambiguous, so honor it whoever the dispatch was attributed to —
  // a human-triggered "Run QA" / "Run review" posts the marked prompt as a
  // MEMBER comment, and it must still collapse into a headline, not dump the
  // whole template into the thread.
  if (!markedKind && authorType !== "agent" && authorType !== "system") return null;

  const m = LEADING_MENTION.exec(rest);
  if (!m) return null;
  const instruction = rest.slice(m[0].length).trim();
  // A marked comment is authoritative even if short; an unmarked one must clear
  // the length floor so a normal human @mention isn't mistaken for a prompt.
  if (!markedKind && instruction.length < MIN_INSTRUCTION_LEN) return null;

  let kind: AgentProtocolKind = markedKind ?? "delegate";
  if (!markedKind) {
    for (const rule of KIND_RULES) {
      if (rule.re.test(instruction)) {
        kind = rule.kind;
        break;
      }
    }
  }
  return { agentName: (m[1] ?? "").trim(), kind, instruction };
}

/** The QA gate's fixed stages, for the pipeline diagram (i18n keys under
 * `agent_protocol.qa_stage.*`). Rendered as a "what QA will run" stepper. */
export const QA_STAGES = [
  "baseline",
  "checks",
  "smoke",
  "tests",
  "verdict",
] as const;
export type QAStage = (typeof QA_STAGES)[number];
