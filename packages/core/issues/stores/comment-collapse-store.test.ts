import { beforeEach, describe, expect, it } from "vitest";
import { useCommentCollapseStore } from "./comment-collapse-store";

describe("comment collapse store", () => {
  beforeEach(() => {
    useCommentCollapseStore.setState({
      collapsedByIssue: {},
      expandedByIssue: {},
    });
  });

  it("keeps human comments expanded until the user collapses them", () => {
    const store = useCommentCollapseStore.getState();

    expect(store.isCollapsed("issue-1", "comment-1")).toBe(false);
    store.toggle("issue-1", "comment-1");
    expect(useCommentCollapseStore.getState().isCollapsed("issue-1", "comment-1")).toBe(true);
  });

  it("keeps agent updates collapsed until the user expands them", () => {
    const store = useCommentCollapseStore.getState();

    expect(store.isCollapsed("issue-1", "agent-comment-1", true)).toBe(true);
    store.toggle("issue-1", "agent-comment-1", true);
    expect(useCommentCollapseStore.getState().isCollapsed("issue-1", "agent-comment-1", true)).toBe(false);
  });
});
