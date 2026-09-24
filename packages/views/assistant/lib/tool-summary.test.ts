import { describe, expect, it } from "vitest";
import { describeTool, resultCount, summarizeToolResult } from "./tool-summary";

describe("describeTool", () => {
  it("splits a known tool into a verb and an object", () => {
    expect(describeTool("list_my_issues")).toEqual({ verb: "list", object: "my_issues" });
    expect(describeTool("create_issue")).toEqual({ verb: "create", object: "issue" });
    expect(describeTool("attach_skill_to_agent")).toEqual({ verb: "attach", object: "skill_to_agent" });
    expect(describeTool("update_member_role")).toEqual({ verb: "update", object: "member_role" });
  });

  it("reads the longest verb first", () => {
    expect(describeTool("dry_run_import")).toEqual({ verb: "dry_run", object: "import" });
  });

  it("treats the verb-less status tools as checks", () => {
    expect(describeTool("usage_summary")).toEqual({ verb: "check", object: "usage" });
    expect(describeTool("qa_status")).toEqual({ verb: "check", object: "qa_status" });
  });

  it("names the person's Zoho reads in words", () => {
    expect(describeTool("zoho_crm_search")).toEqual({ verb: "search", object: "zoho_crm" });
    expect(describeTool("zoho_desk_list_tickets")).toEqual({ verb: "list", object: "zoho_tickets" });
    expect(describeTool("zoho_desk_get_ticket")).toEqual({ verb: "get", object: "zoho_ticket" });
    expect(describeTool("zoho_whoami")).toEqual({ verb: "check", object: "zoho_account" });
  });

  it("returns null for a tool this build does not know, so the row falls back to the raw name", () => {
    expect(describeTool("list_widgets")).toBeNull();
    expect(describeTool("teleport_issue")).toBeNull();
    expect(describeTool("")).toBeNull();
  });
});

describe("resultCount", () => {
  it("prefers an exact server total over the length of a capped page", () => {
    expect(resultCount({ total: 32, issues: new Array(20).fill({}) })).toBe(32);
  });

  it("falls back to count, then to the first list in the result", () => {
    expect(resultCount({ count: 4 })).toBe(4);
    expect(resultCount({ by_status: { done: 1 }, issues: [{}, {}] })).toBe(2);
  });

  it("is null for a result that is not list-shaped, and for a total the server could not compute", () => {
    expect(resultCount({ title: "Fix login" })).toBeNull();
    expect(resultCount("done")).toBeNull();
    expect(resultCount(null)).toBeNull();
    expect(resultCount({ total: null, issues: [{}] })).toBe(1);
  });
});

describe("summarizeToolResult", () => {
  it("keeps a title-shaped summary and a count for lists", () => {
    expect(summarizeToolResult({ title: "Fix login" })).toBe("Fix login");
    expect(summarizeToolResult({ total: 7, issues: [] })).toBe("7");
  });
});
