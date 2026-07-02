import { z } from "zod";

// The design_audit slice action posts a fenced ```design-audit JSON block with
// the project's design-system health: hardcoded values that should be tokens,
// duplicated markup that should be shared components, components the manifest
// is blind to, and a proposed token set. Parsed client-side and rendered as a
// read-only report — lenient the same way every other design block is.

const OffTokenSchema = z
  .object({
    kind: z
      .string()
      .default("color")
      .transform((v) => (v === "spacing" || v === "typography" || v === "color" ? v : "color")),
    value: z.string().default(""),
    occurrences: z.number().default(0),
    suggested_token: z.string().default(""),
    sample_refs: z.array(z.string()).default([]),
  })
  .loose();

const DuplicateSchema = z
  .object({
    pattern: z.string().default(""),
    occurrences: z.number().default(0),
    suggested_component: z.string().default(""),
    sample_refs: z.array(z.string()).default([]),
  })
  .loose();

const UnmanagedSchema = z
  .object({
    name: z.string().default(""),
    code_ref: z.string().default(""),
    note: z.string().default(""),
  })
  .loose();

const ProposedTokenSchema = z
  .object({
    name: z.string().default(""),
    value: z.string().default(""),
    replaces: z.array(z.string()).default([]),
  })
  .loose();

export const DesignAuditSchema = z
  .object({
    summary: z.string().default(""),
    off_token: z.array(OffTokenSchema).default([]),
    duplicates: z.array(DuplicateSchema).default([]),
    unmanaged_components: z.array(UnmanagedSchema).default([]),
    proposed_tokens: z.array(ProposedTokenSchema).default([]),
  })
  .loose();

export type DesignAudit = z.infer<typeof DesignAuditSchema>;

const BLOCK_RE = /```design-audit\s*\n([\s\S]*?)```/;

interface AuditComment {
  author_type: string;
  content: string;
  created_at: string;
}

// parseDesignAuditBlock extracts + validates the block from one comment. Returns
// null when absent or malformed (the section then renders nothing).
export function parseDesignAuditBlock(content: string): DesignAudit | null {
  const m = BLOCK_RE.exec(content);
  if (!m) return null;
  let raw: unknown;
  try {
    raw = JSON.parse(m[1]!.trim());
  } catch {
    return null;
  }
  const result = DesignAuditSchema.safeParse(raw);
  return result.success ? result.data : null;
}

// latestDesignAudit returns the newest agent comment's audit block, or null.
export function latestDesignAudit(comments: AuditComment[]): DesignAudit | null {
  for (let i = comments.length - 1; i >= 0; i--) {
    const c = comments[i]!;
    if (c.author_type !== "agent") continue;
    const audit = parseDesignAuditBlock(c.content ?? "");
    if (audit) return audit;
  }
  return null;
}
