import { z } from "zod";

// The design_proposal slice action posts its analysis as a fenced
// ```design-proposal JSON block on an agent comment. The frontend parses that
// block client-side (the server persists only the label + notification, not a
// structured row in Phase 2), so this schema is the read contract — it MUST
// stay lenient the way the API schemas are: every field defaults, unknown enum
// values downgrade, and a malformed block is surfaced as an explicit error
// state rather than crashing the issue view.

export type DesignProposalStatus = "ok" | "blocked";
export type DesignVerdict = "reuse" | "extend" | "new";

// A block that is present in a comment but fails to parse — the section renders
// an explicit "could not parse, re-run" card instead of hiding it.
export type DesignProposalParseState = "ok" | "blocked" | "invalid";

const FigmaRefSchema = z
  .object({
    url: z.string().default(""),
    file_key: z.string().default(""),
    node_id: z.string().default(""),
  })
  .loose();

const ScreenSchema = z
  .object({
    name: z.string().default(""),
    figma_node_id: z.string().default(""),
    summary: z.string().default(""),
    render: z.string().default(""),
  })
  .loose();

const ComponentSchema = z
  .object({
    name: z.string().default(""),
    // Unknown verdicts downgrade to "new" (the generic "needs building" badge)
    // rather than rejecting the whole proposal.
    verdict: z
      .string()
      .default("new")
      .transform((v): DesignVerdict =>
        v === "reuse" || v === "extend" || v === "new" ? v : "new",
      ),
    code_ref: z.string().nullable().default(null),
    figma_node_id: z.string().nullable().default(null),
    notes: z.string().default(""),
  })
  .loose();

const DeviationSchema = z
  .object({
    aspect: z.string().default("other"),
    figma_value: z.string().default(""),
    project_value: z.string().default(""),
    question: z.string().default(""),
  })
  .loose();

const SubIssueSchema = z
  .object({
    title: z.string().default(""),
    description: z.string().default(""),
    screens: z.array(z.string()).default([]),
    node_ids: z.array(z.string()).default([]),
    depends_on: z.array(z.number()).default([]),
  })
  .loose();

export const DesignProposalSchema = z
  .object({
    status: z
      .string()
      .default("ok")
      .transform((v): DesignProposalStatus => (v === "blocked" ? "blocked" : "ok")),
    reason: z.string().nullable().default(null),
    reason_detail: z.string().default(""),
    figma: z.array(FigmaRefSchema).default([]),
    screens: z.array(ScreenSchema).default([]),
    components: z.array(ComponentSchema).default([]),
    deviations: z.array(DeviationSchema).default([]),
    sub_issues: z.array(SubIssueSchema).default([]),
    open_questions: z.array(z.string()).default([]),
  })
  .loose();

export type DesignProposal = z.infer<typeof DesignProposalSchema>;

const BLOCK_RE = /```design-proposal\s*\n([\s\S]*?)```/;

// One parsed proposal block found on a comment, tagged with its parse state so
// the UI can order revisions (v1..vN) and flag a broken block explicitly.
export interface ParsedDesignProposal {
  commentId: string;
  createdAt: string;
  authorId: string;
  state: DesignProposalParseState;
  proposal: DesignProposal | null; // null only when state === "invalid"
}

// Minimal comment shape this reads — a structural subset of the timeline/comment
// types so callers can pass either without coupling to the full interface.
interface ProposalComment {
  id: string;
  author_type: string;
  author_id: string;
  content: string;
  created_at: string;
}

// parseDesignProposalBlock extracts + validates the block from one comment's
// content. Mirrors the server's ParseDesignProposalBlock four-way outcome.
export function parseDesignProposalBlock(
  content: string,
): { state: DesignProposalParseState | "none"; proposal: DesignProposal | null } {
  const m = BLOCK_RE.exec(content);
  if (!m) return { state: "none", proposal: null };
  let raw: unknown;
  try {
    raw = JSON.parse(m[1]!.trim());
  } catch {
    return { state: "invalid", proposal: null };
  }
  const result = DesignProposalSchema.safeParse(raw);
  if (!result.success) return { state: "invalid", proposal: null };
  return {
    state: result.data.status === "blocked" ? "blocked" : "ok",
    proposal: result.data,
  };
}

// extractDesignProposals returns EVERY design-proposal block on the issue's
// agent comments, ordered oldest→newest (v1..vN). The newest is the active one;
// the UI shows a revision dropdown and renders an error card for a newest block
// that is `invalid`. Human/system comments are ignored — only agents author
// proposals.
export function extractDesignProposals(comments: ProposalComment[]): ParsedDesignProposal[] {
  const out: ParsedDesignProposal[] = [];
  for (const c of comments) {
    if (c.author_type !== "agent") continue;
    const { state, proposal } = parseDesignProposalBlock(c.content ?? "");
    if (state === "none") continue;
    out.push({
      commentId: c.id,
      createdAt: c.created_at,
      authorId: c.author_id,
      state,
      proposal,
    });
  }
  return out;
}
