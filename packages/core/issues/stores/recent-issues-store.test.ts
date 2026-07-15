import { beforeEach, describe, expect, it } from "vitest";
import { useRecentIssuesStore, selectRecentIssues } from "./recent-issues-store";

beforeEach(() => {
  useRecentIssuesStore.setState({ identityId: "user-1", byWorkspace: {} });
});

const bucket = (wsId: string) => `user-1:${wsId}`;

describe("useRecentIssuesStore.recordVisit", () => {
  it("keeps visits namespaced by workspace id", () => {
    const { recordVisit } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    recordVisit("ws-b", "issue-2");

    const state = useRecentIssuesStore.getState().byWorkspace;
    expect(state[bucket("ws-a")]?.map((e) => e.id)).toEqual(["issue-1"]);
    expect(state[bucket("ws-b")]?.map((e) => e.id)).toEqual(["issue-2"]);
  });

  it("moves the most recent visit to the front and dedupes", () => {
    const { recordVisit } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    recordVisit("ws-a", "issue-2");
    recordVisit("ws-a", "issue-1");

    const ids = useRecentIssuesStore
      .getState()
      .byWorkspace[bucket("ws-a")]?.map((e) => e.id);
    expect(ids).toEqual(["issue-1", "issue-2"]);
  });

  it("caps each workspace's bucket at 20 entries", () => {
    const { recordVisit } = useRecentIssuesStore.getState();
    for (let i = 0; i < 25; i++) recordVisit("ws-a", `issue-${i}`);
    expect(useRecentIssuesStore.getState().byWorkspace[bucket("ws-a")]).toHaveLength(20);
  });
});

describe("useRecentIssuesStore.forgetIssue", () => {
  it("removes a single id from the workspace bucket", () => {
    const { recordVisit, forgetIssue } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    recordVisit("ws-a", "issue-2");
    recordVisit("ws-a", "issue-3");

    forgetIssue("ws-a", "issue-2");

    const ids = useRecentIssuesStore
      .getState()
      .byWorkspace[bucket("ws-a")]?.map((e) => e.id);
    expect(ids).toEqual(["issue-3", "issue-1"]);
  });

  it("drops the bucket entirely when the last id is removed", () => {
    const { recordVisit, forgetIssue } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    recordVisit("ws-b", "issue-2");

    forgetIssue("ws-a", "issue-1");

    const state = useRecentIssuesStore.getState().byWorkspace;
    expect(state[bucket("ws-a")]).toBeUndefined();
    expect(state[bucket("ws-b")]?.map((e) => e.id)).toEqual(["issue-2"]);
  });

  it("does not touch other workspaces' buckets", () => {
    const { recordVisit, forgetIssue } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    recordVisit("ws-b", "issue-1");

    forgetIssue("ws-a", "issue-1");

    const state = useRecentIssuesStore.getState().byWorkspace;
    expect(state[bucket("ws-a")]).toBeUndefined();
    expect(state[bucket("ws-b")]?.map((e) => e.id)).toEqual(["issue-1"]);
  });

  it("is a no-op when the id is not in the bucket", () => {
    const { recordVisit, forgetIssue } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    const before = useRecentIssuesStore.getState().byWorkspace;

    forgetIssue("ws-a", "issue-missing");

    expect(useRecentIssuesStore.getState().byWorkspace).toBe(before);
  });

  it("is a no-op when the workspace has no bucket", () => {
    const { forgetIssue } = useRecentIssuesStore.getState();
    const before = useRecentIssuesStore.getState().byWorkspace;

    forgetIssue("ws-missing", "issue-1");

    expect(useRecentIssuesStore.getState().byWorkspace).toBe(before);
  });
});

describe("useRecentIssuesStore.pruneWorkspaces", () => {
  it("drops buckets for workspaces not in the active set", () => {
    const { recordVisit, pruneWorkspaces } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    recordVisit("ws-b", "issue-2");
    recordVisit("ws-c", "issue-3");

    pruneWorkspaces(["ws-a", "ws-c"]);
    const state = useRecentIssuesStore.getState().byWorkspace;
    expect(Object.keys(state).sort()).toEqual([bucket("ws-a"), bucket("ws-c")]);
  });

  it("is a no-op when every bucket is still active", () => {
    const { recordVisit, pruneWorkspaces } = useRecentIssuesStore.getState();
    recordVisit("ws-a", "issue-1");
    const before = useRecentIssuesStore.getState().byWorkspace;
    pruneWorkspaces(["ws-a"]);
    expect(useRecentIssuesStore.getState().byWorkspace).toBe(before);
  });
});

describe("selectRecentIssues", () => {
  it("returns the bucket for the given workspace", () => {
    useRecentIssuesStore.getState().recordVisit("ws-a", "issue-1");
    const items = selectRecentIssues("ws-a")(useRecentIssuesStore.getState());
    expect(items.map((e) => e.id)).toEqual(["issue-1"]);
  });

  it("does not expose another user's recents in the same workspace", () => {
    useRecentIssuesStore.getState().recordVisit("ws-a", "issue-user-1");
    useRecentIssuesStore.getState().setIdentity("user-2");

    expect(selectRecentIssues("ws-a")(useRecentIssuesStore.getState())).toEqual([]);

    useRecentIssuesStore.getState().recordVisit("ws-a", "issue-user-2");
    expect(selectRecentIssues("ws-a")(useRecentIssuesStore.getState()).map((e) => e.id)).toEqual([
      "issue-user-2",
    ]);
  });

  it("returns a stable empty array when wsId is null or unknown", () => {
    const a = selectRecentIssues(null)(useRecentIssuesStore.getState());
    const b = selectRecentIssues(null)(useRecentIssuesStore.getState());
    const c = selectRecentIssues("missing")(useRecentIssuesStore.getState());
    expect(a).toBe(b);
    expect(a).toBe(c);
    expect(a).toEqual([]);
  });
});
