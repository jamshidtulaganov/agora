import { describe, expect, it } from "vitest";

import { parseDesignAuditBlock, latestDesignAudit, DesignAuditSchema } from "./audit";

describe("parseDesignAuditBlock", () => {
  it("returns null with no block", () => {
    expect(parseDesignAuditBlock("plain comment")).toBeNull();
  });

  it("parses a full audit", () => {
    const content =
      "Summary here.\n\n```design-audit\n" +
      JSON.stringify({
        summary: "3 off-token colors",
        off_token: [{ kind: "color", value: "#3b82f6", occurrences: 14, suggested_token: "primary", sample_refs: ["src/x.vue:12"] }],
        duplicates: [{ pattern: "table", occurrences: 9, suggested_component: "SdGrid", sample_refs: [] }],
        unmanaged_components: [{ name: "OldModal", code_ref: "src/OldModal.vue" }],
        proposed_tokens: [{ name: "primary", value: "#2563EB", replaces: ["#3b82f6", "#3B82F6"] }],
      }) +
      "\n```";
    const audit = parseDesignAuditBlock(content);
    expect(audit?.off_token[0]?.occurrences).toBe(14);
    expect(audit?.off_token[0]?.suggested_token).toBe("primary");
    expect(audit?.proposed_tokens[0]?.replaces).toHaveLength(2);
  });

  it("defaults missing arrays and downgrades unknown kind", () => {
    const audit = parseDesignAuditBlock('```design-audit\n{"off_token":[{"kind":"weird","value":"x"}]}\n```');
    expect(audit?.off_token[0]?.kind).toBe("color");
    expect(audit?.duplicates).toEqual([]);
    expect(audit?.proposed_tokens).toEqual([]);
  });

  it("returns null on malformed json", () => {
    expect(parseDesignAuditBlock("```design-audit\n{bad\n```")).toBeNull();
  });
});

describe("latestDesignAudit", () => {
  it("picks the newest agent audit, ignoring humans", () => {
    const comments = [
      { author_type: "member", content: "```design-audit\n{}\n```", created_at: "2026-01-01T00:00:00Z" },
      { author_type: "agent", content: "```design-audit\n{\"summary\":\"old\"}\n```", created_at: "2026-01-02T00:00:00Z" },
      { author_type: "agent", content: "no block", created_at: "2026-01-03T00:00:00Z" },
      { author_type: "agent", content: "```design-audit\n{\"summary\":\"new\"}\n```", created_at: "2026-01-04T00:00:00Z" },
    ];
    expect(latestDesignAudit(comments)?.summary).toBe("new");
  });

  it("returns null when no agent audit exists", () => {
    expect(latestDesignAudit([{ author_type: "member", content: "```design-audit\n{}\n```", created_at: "x" }])).toBeNull();
  });
});

describe("DesignAuditSchema malformed", () => {
  it("fails closed on wrong-typed off_token", () => {
    expect(DesignAuditSchema.safeParse({ off_token: "nope" }).success).toBe(false);
  });
  it("accepts unknown future fields (loose)", () => {
    expect(DesignAuditSchema.safeParse({ summary: "x", future: 1 }).success).toBe(true);
  });
});
