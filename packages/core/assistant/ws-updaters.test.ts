import { describe, it, expect, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { onAssistantMessage, onAssistantToolActivity, onAssistantRunFinished } from "./ws-updaters";
import { assistantKeys } from "./queries";

const base = { user_id: "user-1", session_id: "session-1", run_id: "run-1" };

function queryClient() {
  const qc = new QueryClient();
  const invalidate = vi.spyOn(qc, "invalidateQueries");
  return { qc, invalidate };
}

describe("assistant WS invalidation", () => {
  it("reconciles messages, session and runs on a message", () => {
    const { qc, invalidate } = queryClient();
    onAssistantMessage(qc, { ...base, message_id: "msg-1", role: "assistant", content: "hi", created_at: "now" });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: assistantKeys.messages("session-1") });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: assistantKeys.session("session-1") });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: assistantKeys.runs("session-1") });
  });

  it("reconciles the active tool from the authoritative run", () => {
    const { qc, invalidate } = queryClient();
    onAssistantToolActivity(qc, { ...base, tool_call_id: "call-1", tool_name: "search_issues" });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: assistantKeys.run("run-1") });
  });

  it("reconciles terminal status without writing a local approximation", () => {
    const { qc, invalidate } = queryClient();
    onAssistantRunFinished(qc, { ...base, status: "failed", error: "model unavailable" });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: assistantKeys.messages("session-1") });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: assistantKeys.run("run-1") });
  });
});
