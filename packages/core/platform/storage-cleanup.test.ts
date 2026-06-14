import { describe, it, expect, vi } from "vitest";
import { clearWorkspaceStorage } from "./storage-cleanup";

describe("clearWorkspaceStorage", () => {
  it("removes all workspace-scoped keys for given wsId", () => {
    const adapter = {
      getItem: vi.fn(),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    };

    clearWorkspaceStorage(adapter, "ws_123");

    expect(adapter.removeItem).toHaveBeenCalledWith("tandem_issue_draft:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem_issues_view:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem_issues_scope:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem_my_issues_view:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem:chat:selectedAgentId:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem:chat:activeSessionId:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem:chat:drafts:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem:chat:expanded:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("tandem_navigation:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledTimes(9);
  });
});
