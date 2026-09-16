import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AssistantSession } from "../types";

const updateAssistantSession = vi.hoisted(() => vi.fn());
vi.mock("../api", () => ({ api: { updateAssistantSession } }));

import { autoTitleAssistantSession, deriveSessionTitle } from "./auto-title";
import { assistantKeys } from "./queries";

function session(overrides: Partial<AssistantSession> & { id: string }): AssistantSession {
  return {
    title: "",
    focus_workspace_id: null,
    created_at: "2026-09-16T09:00:00Z",
    updated_at: "2026-09-16T09:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  updateAssistantSession.mockReset();
  updateAssistantSession.mockResolvedValue(undefined);
});

describe("deriveSessionTitle", () => {
  it("keeps a short message verbatim", () => {
    expect(deriveSessionTitle("Close the login bug")).toBe("Close the login bug");
  });

  it("collapses newlines and runs of whitespace into a single line", () => {
    expect(deriveSessionTitle("  Plan\n\nmy   week  ")).toBe("Plan my week");
  });

  it("cuts on a word boundary, never mid-word", () => {
    const title = deriveSessionTitle(
      "Create an issue to update the pricing page copy before the launch",
    );
    expect(title).toBe("Create an issue to update the pricing page copy…");
    expect(title.length).toBeLessThanOrEqual(49); // 48 + the ellipsis
    expect(title).not.toContain("  ");
  });

  it("falls back to a hard cut when the first word alone overflows", () => {
    const title = deriveSessionTitle(`https://example.com/${"a".repeat(80)}`);
    expect(title).toHaveLength(49);
    expect(title.endsWith("…")).toBe(true);
  });

  it("honours a custom budget", () => {
    expect(deriveSessionTitle("one two three four five", 10)).toBe("one two…");
  });

  it("returns an empty string for a blank message", () => {
    expect(deriveSessionTitle("   \n  ")).toBe("");
  });
});

describe("autoTitleAssistantSession", () => {
  it("titles an untitled session and patches the cache before the request", async () => {
    const qc = new QueryClient();
    qc.setQueryData(assistantKeys.sessions(), [session({ id: "s1" })]);

    await autoTitleAssistantSession(qc, "s1", "What is on my plate today?");

    expect(updateAssistantSession).toHaveBeenCalledWith("s1", {
      title: "What is on my plate today?",
    });
    const cached = qc.getQueryData<AssistantSession[]>(assistantKeys.sessions());
    expect(cached?.[0]!.title).toBe("What is on my plate today?");
  });

  it("never overwrites a manual rename", async () => {
    const qc = new QueryClient();
    qc.setQueryData(assistantKeys.sessions(), [session({ id: "s1", title: "Renamed by me" })]);

    await autoTitleAssistantSession(qc, "s1", "What is on my plate today?");

    expect(updateAssistantSession).not.toHaveBeenCalled();
    expect(qc.getQueryData<AssistantSession[]>(assistantKeys.sessions())?.[0]!.title).toBe(
      "Renamed by me",
    );
  });

  it("does nothing when the session is not in the cache", async () => {
    const qc = new QueryClient();
    qc.setQueryData(assistantKeys.sessions(), [session({ id: "other" })]);

    await autoTitleAssistantSession(qc, "s1", "hello");

    expect(updateAssistantSession).not.toHaveBeenCalled();
  });

  it("does nothing for a blank message", async () => {
    const qc = new QueryClient();
    qc.setQueryData(assistantKeys.sessions(), [session({ id: "s1" })]);

    await autoTitleAssistantSession(qc, "s1", "   ");

    expect(updateAssistantSession).not.toHaveBeenCalled();
  });

  it("swallows a failed PATCH — the transcript must not break over a title", async () => {
    const qc = new QueryClient();
    qc.setQueryData(assistantKeys.sessions(), [session({ id: "s1" })]);
    updateAssistantSession.mockRejectedValue(new Error("network"));

    await expect(autoTitleAssistantSession(qc, "s1", "hello there")).resolves.toBeUndefined();
  });
});
