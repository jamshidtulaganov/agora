import { describe, expect, it } from "vitest";

import { parseDesignProposalBlock, extractDesignProposals, DesignProposalSchema } from "./proposal";

// Mirrors server/internal/service/design_proposal_test.go — the block contract
// is shared, so the two parsers must agree on states and field names.
describe("parseDesignProposalBlock", () => {
  it("returns none when there is no block", () => {
    expect(parseDesignProposalBlock("just a comment").state).toBe("none");
  });

  it("parses a valid ok proposal", () => {
    const content =
      "Analysis.\n\n```design-proposal\n" +
      JSON.stringify({
        status: "ok",
        screens: [{ name: "List", figma_node_id: "208:5147", summary: "table", render: "figma-208-5147.png" }],
        components: [{ name: "DataTable", verdict: "reuse", code_ref: "src/DataTable.vue" }],
        sub_issues: [{ title: "List", description: "...", depends_on: [] }],
      }) +
      "\n```";
    const { state, proposal } = parseDesignProposalBlock(content);
    expect(state).toBe("ok");
    expect(proposal?.screens[0]?.render).toBe("figma-208-5147.png");
    expect(proposal?.components[0]?.verdict).toBe("reuse");
  });

  it("defaults an omitted status to ok", () => {
    const { state, proposal } = parseDesignProposalBlock('```design-proposal\n{"screens":[]}\n```');
    expect(state).toBe("ok");
    expect(proposal?.status).toBe("ok");
  });

  it("treats a blocked proposal as blocked", () => {
    const { state, proposal } = parseDesignProposalBlock(
      '```design-proposal\n{"status":"blocked","reason":"figma_forbidden"}\n```',
    );
    expect(state).toBe("blocked");
    expect(proposal?.reason).toBe("figma_forbidden");
  });

  it("downgrades an unknown verdict to new instead of dropping the proposal", () => {
    const { state, proposal } = parseDesignProposalBlock(
      '```design-proposal\n{"components":[{"name":"X","verdict":"wat"}]}\n```',
    );
    expect(state).toBe("ok");
    expect(proposal?.components[0]?.verdict).toBe("new");
  });

  it("returns invalid (not none) for a present-but-malformed block", () => {
    expect(parseDesignProposalBlock("```design-proposal\n{not json\n```").state).toBe("invalid");
  });

  it("defaults missing arrays so the UI never reads undefined", () => {
    const { proposal } = parseDesignProposalBlock('```design-proposal\n{"status":"ok"}\n```');
    expect(proposal?.screens).toEqual([]);
    expect(proposal?.components).toEqual([]);
    expect(proposal?.sub_issues).toEqual([]);
    expect(proposal?.deviations).toEqual([]);
    expect(proposal?.open_questions).toEqual([]);
  });
});

describe("extractDesignProposals", () => {
  const mk = (id: string, author_type: string, content: string, created_at: string) => ({
    id,
    author_type,
    author_id: "agent-1",
    content,
    created_at,
  });

  it("returns every agent proposal oldest→newest and ignores non-agent comments", () => {
    const comments = [
      mk("c1", "member", "```design-proposal\n{}\n```", "2026-01-01T00:00:00Z"), // human — ignored
      mk("c2", "agent", "```design-proposal\n{\"status\":\"ok\"}\n```", "2026-01-02T00:00:00Z"),
      mk("c3", "agent", "no block here", "2026-01-03T00:00:00Z"),
      mk("c4", "agent", "```design-proposal\n{\"status\":\"blocked\",\"reason\":\"figma_quota\"}\n```", "2026-01-04T00:00:00Z"),
    ];
    const out = extractDesignProposals(comments);
    expect(out.map((p) => p.commentId)).toEqual(["c2", "c4"]);
    expect(out[1]?.state).toBe("blocked");
  });

  it("carries the invalid state for a broken block instead of skipping it", () => {
    const out = extractDesignProposals([mk("c1", "agent", "```design-proposal\n{bad\n```", "2026-01-01T00:00:00Z")]);
    expect(out).toHaveLength(1);
    expect(out[0]?.state).toBe("invalid");
    expect(out[0]?.proposal).toBeNull();
  });
});

describe("DesignProposalSchema malformed inputs", () => {
  it("fails closed on wrong-typed fields (safeParse false)", () => {
    // screens must be an array; a string is not coercible.
    expect(DesignProposalSchema.safeParse({ screens: "nope" }).success).toBe(false);
  });

  it("accepts unknown future fields (loose)", () => {
    const r = DesignProposalSchema.safeParse({ status: "ok", some_future_field: 1 });
    expect(r.success).toBe(true);
  });
});
