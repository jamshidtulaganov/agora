import { describe, expect, it } from "vitest";
import type { AssistantMessage } from "@agora/core/types";
import {
  collectKnowledgeCites,
  knowledgeCiteLabel,
  knowledgeCiteMarkdown,
  knowledgeCitePlainText,
  replaceKnowledgeCites,
  turnKnowledgeSources,
  type KnowledgeCite,
} from "./knowledge-cites";

function tool(name: string, result: unknown, id = name): AssistantMessage {
  return {
    id,
    session_id: "s1",
    role: "tool",
    content: "",
    tool_name: name,
    tool_result: result,
    created_at: "2026-09-25T10:00:00Z",
  };
}

const hit = {
  cite: "kb:1a2b3c4d",
  chunk_id: "1a2b3c4d-0000-0000-0000-000000000000",
  doc_id: "doc-1",
  doc_title: "Collections SOP",
  heading_path: "Collections SOP › Write-offs",
  location: "p. 4",
  section: 3,
  text: "Write-offs over $500 need approval.",
};

describe("collectKnowledgeCites", () => {
  it("reads cites from search and read results, wherever they are nested", () => {
    const cites = collectKnowledgeCites([
      tool("search_knowledge", { results: [hit] }),
      tool("read_knowledge", {
        document: { doc_id: "doc-2", title: "Rates" },
        sections: [{ cite: "kb:5e6f7a8b", doc_id: "doc-2", doc_title: "Rates", location: "Sheet \"Rates\", rows 2–41", section: 0 }],
      }),
    ]);
    expect([...cites.keys()]).toEqual(["kb:1a2b3c4d", "kb:5e6f7a8b"]);
    expect(cites.get("kb:1a2b3c4d")).toEqual({
      cite: "kb:1a2b3c4d",
      docId: "doc-1",
      docTitle: "Collections SOP",
      headingPath: "Collections SOP › Write-offs",
      location: "p. 4",
      section: 3,
    });
  });

  it("ignores other tools, malformed items and invalid cites", () => {
    const cites = collectKnowledgeCites([
      tool("search_issues", { results: [hit] }),
      tool("search_knowledge", { results: [{ ...hit, doc_id: null }, { ...hit, cite: "kb:xyz" }, "junk", null] }),
      tool("search_knowledge", { error: "search failed" }),
      tool("read_knowledge", null),
    ]);
    expect(cites.size).toBe(0);
  });

  it("keeps a section-less cite linkable to the document", () => {
    const cites = collectKnowledgeCites([
      tool("search_knowledge", { results: [{ ...hit, section: "3" }] }),
    ]);
    expect(cites.get("kb:1a2b3c4d")?.section).toBeNull();
  });
});

describe("replaceKnowledgeCites", () => {
  const cites = new Map<string, KnowledgeCite>([
    [
      "kb:1a2b3c4d",
      { cite: "kb:1a2b3c4d", docId: "doc-1", docTitle: "Collections SOP", headingPath: "", location: "p. 4", section: 3 },
    ],
    [
      "kb:5e6f7a8b",
      { cite: "kb:5e6f7a8b", docId: "doc-2", docTitle: "Rates", headingPath: "", location: "", section: null },
    ],
  ]);
  const render = (cite: KnowledgeCite) => `<${cite.cite}>`;

  it("replaces matched cites and drops the rest without leaving a stray space", () => {
    expect(
      replaceKnowledgeCites("Needs approval [kb:1a2b3c4d]. Maybe [kb:deadbeef].", cites, render),
    ).toBe("Needs approval <kb:1a2b3c4d>. Maybe.");
  });

  it("reads double brackets, upper-case hex and grouped cites", () => {
    expect(replaceKnowledgeCites("A [[kb:1A2B3C4D]] B", cites, render)).toBe("A <kb:1a2b3c4d> B");
    expect(replaceKnowledgeCites("A [kb:1a2b3c4d, kb:00000000; kb:5e6f7a8b]", cites, render)).toBe(
      "A <kb:1a2b3c4d> <kb:5e6f7a8b>",
    );
  });

  it("drops every token when the conversation has no knowledge results", () => {
    expect(replaceKnowledgeCites("Pay in 30 days [kb:1a2b3c4d].", new Map(), render)).toBe(
      "Pay in 30 days.",
    );
  });

  it("leaves content without tokens and look-alikes untouched", () => {
    expect(replaceKnowledgeCites("See [the SOP](https://x.test)", cites, render)).toBe(
      "See [the SOP](https://x.test)",
    );
    expect(replaceKnowledgeCites("[kb:123] and kb:1a2b3c4d", cites, render)).toBe(
      "[kb:123] and kb:1a2b3c4d",
    );
  });
});

describe("cite rendering", () => {
  const cite: KnowledgeCite = {
    cite: "kb:1a2b3c4d",
    docId: "doc-1",
    docTitle: "SOP [draft]",
    headingPath: "",
    location: "p. 4",
    section: 3,
  };

  it("labels a chip with the document and location", () => {
    expect(knowledgeCiteLabel(cite)).toBe("SOP [draft] · p. 4");
    expect(knowledgeCiteLabel({ ...cite, docTitle: "", headingPath: "Write-offs", location: "" })).toBe(
      "Write-offs",
    );
  });

  it("emits a mention link whose text can't break the markdown", () => {
    expect(knowledgeCiteMarkdown(cite)).toBe("[SOP  draft  · p. 4](mention://kb/1a2b3c4d)");
  });

  it("copies as plain text", () => {
    expect(knowledgeCitePlainText(cite)).toBe("(SOP [draft] · p. 4)");
  });
});

function said(role: "user" | "assistant", content: string, id: string): AssistantMessage {
  return { id, session_id: "s1", role, content, created_at: "2026-09-25T10:00:00Z" };
}

describe("turnKnowledgeSources", () => {
  const other = { ...hit, cite: "kb:5e6f7a8b", doc_id: "doc-2", doc_title: "Refund policy", section: 0 };
  const sameDoc = { ...hit, cite: "kb:99990000", section: 4 };
  const earlier = { ...hit, cite: "kb:aaaa1111", doc_id: "doc-old", doc_title: "Old turn" };

  it("lists this turn's documents, one chip each, under a reply that cited nothing", () => {
    const messages = [
      said("user", "old question", "u0"),
      tool("search_knowledge", { results: [earlier] }, "t0"),
      said("assistant", "old answer", "a0"),
      said("user", "who approves a write-off?", "u1"),
      tool("search_knowledge", { results: [hit, sameDoc, other] }, "t1"),
      said("assistant", "The Finance Director approves write-offs over $5,000.", "a1"),
    ];
    const cites = collectKnowledgeCites(messages);
    const sources = turnKnowledgeSources(messages, 5, cites);
    expect(sources.map((s) => s.docTitle)).toEqual(["Collections SOP", "Refund policy"]);
  });

  it("stays empty when the reply cites inline, or isn't the turn's last text", () => {
    const messages = [
      said("user", "q", "u1"),
      tool("search_knowledge", { results: [hit] }, "t1"),
      said("assistant", "Approval is needed [kb:1a2b3c4d].", "a1"),
    ];
    const cites = collectKnowledgeCites(messages);
    expect(turnKnowledgeSources(messages, 2, cites)).toEqual([]);

    const midTurn = [said("user", "q", "u1"), said("assistant", "Let me look.", "a1"), tool("search_knowledge", { results: [hit] }, "t1"), said("assistant", "Done.", "a2")];
    const midCites = collectKnowledgeCites(midTurn);
    expect(turnKnowledgeSources(midTurn, 1, midCites)).toEqual([]);
    expect(turnKnowledgeSources(midTurn, 3, midCites).map((s) => s.docTitle)).toEqual(["Collections SOP"]);
  });

  it("stays empty for a turn that never searched the knowledge base", () => {
    const messages = [said("user", "hi", "u1"), said("assistant", "Hello!", "a1")];
    expect(turnKnowledgeSources(messages, 1, collectKnowledgeCites(messages))).toEqual([]);
  });
});
