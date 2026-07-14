import { describe, expect, it } from "vitest";
import { parseAgentProtocol } from "./agent-protocol";

const QA = `[@QA Tester](mention://agent/${"a".repeat(8)}-1111-4111-8111-111111111111) Run QA for this issue as a DETERMINISTIC gate — report strictly by EXIT CODE, never by opinion. ${"x".repeat(400)}`;

describe("parseAgentProtocol", () => {
  it("classifies a QA gate instruction from an agent author", () => {
    const p = parseAgentProtocol(QA, "agent");
    expect(p).not.toBeNull();
    expect(p!.kind).toBe("run_qa");
    expect(p!.agentName).toBe("QA Tester");
    expect(p!.instruction.startsWith("Run QA")).toBe(true);
  });

  it("classifies the QA-lead delegation as run_qa", () => {
    const c = `[@QA Lead](mention://agent/22222222-2222-4222-8222-222222222222) As the QA LEAD, determine the stack and DELEGATE this QA gate. ${"y".repeat(400)}`;
    expect(parseAgentProtocol(c, "agent")!.kind).toBe("run_qa");
  });

  it("classifies write_tests / write_docs / review / design", () => {
    const mk = (body: string) =>
      `[@Dev](mention://agent/33333333-3333-4333-8333-333333333333) ${body} ${"z".repeat(400)}`;
    expect(parseAgentProtocol(mk("Write tests for this issue. Open a pull request"), "agent")!.kind).toBe("write_tests");
    expect(parseAgentProtocol(mk("Write documentation for this issue."), "agent")!.kind).toBe("write_docs");
    expect(parseAgentProtocol(mk("Review the relevant part of this issue and post your findings"), "agent")!.kind).toBe("review");
    expect(parseAgentProtocol(mk("Produce a design-proposal for this issue."), "agent")!.kind).toBe("design");
  });

  it("falls back to delegate for an unrecognized long instruction", () => {
    const c = `[@Bot](mention://agent/44444444-4444-4444-8444-444444444444) Do the needful with great care and diligence. ${"q".repeat(400)}`;
    expect(parseAgentProtocol(c, "agent")!.kind).toBe("delegate");
  });

  it("ignores short human @mentions (below the length floor)", () => {
    expect(parseAgentProtocol("[@Aria](mention://agent/55555555-5555-4555-8555-555555555555) can you fix the header?", "member")).toBeNull();
    expect(parseAgentProtocol("[@Aria](mention://agent/55555555-5555-4555-8555-555555555555) can you fix the header?", "agent")).toBeNull();
  });

  it("ignores non-agent/system authors and comments without a leading mention", () => {
    expect(parseAgentProtocol(QA, "member")).toBeNull();
    expect(parseAgentProtocol(`Run QA for this issue as a DETERMINISTIC gate ${"x".repeat(400)}`, "agent")).toBeNull();
  });
})

describe("parseAgentProtocol — explicit backend marker", () => {
  const mention = "[@QA Tester](mention://agent/11111111-1111-4111-8111-111111111111)";
  it("uses the marker's exact kind (mapped), overriding the heuristic", () => {
    // Body wording says nothing QA-ish, but the marker pins it to run_qa.
    const c = `<!--agent-protocol:run_qa-->\n${mention} Please proceed with the delegated work now.`;
    const p = parseAgentProtocol(c, "agent");
    expect(p!.kind).toBe("run_qa");
    expect(p!.agentName).toBe("QA Tester");
    expect(p!.instruction.startsWith("Please proceed")).toBe(true);
  });

  it("maps backend vocabulary to display kinds", () => {
    const mk = (k: string) => parseAgentProtocol(`<!--agent-protocol:${k}-->\n${mention} short.`, "agent")!.kind;
    expect(mk("auto_docs")).toBe("write_docs");
    expect(mk("gen_test_cases")).toBe("gen_tests");
    expect(mk("run_test_cases")).toBe("gen_tests");
    expect(mk("compile_tests")).toBe("gen_tests");
    expect(mk("review_part")).toBe("review");
    expect(mk("run_review")).toBe("review");
    expect(mk("design_proposal")).toBe("design");
    expect(mk("draft_code")).toBe("delegate");
    expect(mk("something_new")).toBe("delegate");
  });

  it("honors a marked comment even below the length floor (marker is authoritative)", () => {
    const c = `<!--agent-protocol:run_qa-->\n${mention} go.`;
    expect(parseAgentProtocol(c, "agent")).not.toBeNull();
  });

  it("honors a MARKED comment from a MEMBER author (human-triggered Run review/QA)", () => {
    // A human clicking "Run review" posts the marked prompt attributed to the
    // member — it must still collapse into a headline, not dump the template.
    const c = `<!--agent-protocol:run_review-->\n${mention} Review this issue's pull request as an INDEPENDENT code reviewer. ${"r".repeat(400)}`;
    const p = parseAgentProtocol(c, "member");
    expect(p).not.toBeNull();
    expect(p!.kind).toBe("review");
  });
})
