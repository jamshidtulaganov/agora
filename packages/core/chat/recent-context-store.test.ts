import { beforeEach, describe, expect, it } from "vitest";
import { selectRecentContexts, useRecentContextStore } from "./recent-context-store";

beforeEach(() => {
  useRecentContextStore.setState({ identityId: "user-1", byWorkspace: {} });
});

const bucket = (wsId: string) => `user-1:${wsId}`;

describe("useRecentContextStore.recordVisit", () => {
  it("keeps visits namespaced by workspace id", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-b", { type: "project", id: "project-1" });

    const state = useRecentContextStore.getState().byWorkspace;
    expect(state[bucket("ws-a")]?.map((e) => `${e.type}:${e.id}`)).toEqual(["issue:issue-1"]);
    expect(state[bucket("ws-b")]?.map((e) => `${e.type}:${e.id}`)).toEqual(["project:project-1"]);
  });

  it("moves the most recent visit to the front and dedupes by type and id", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "same-id" });
    recordVisit("ws-a", { type: "project", id: "same-id" });
    recordVisit("ws-a", { type: "issue", id: "same-id" });

    const keys = useRecentContextStore
      .getState()
      .byWorkspace[bucket("ws-a")]?.map((e) => `${e.type}:${e.id}`);
    expect(keys).toEqual(["issue:same-id", "project:same-id"]);
  });

  it("caps each workspace bucket at 20 entries", () => {
    const { recordVisit } = useRecentContextStore.getState();
    for (let i = 0; i < 25; i++) recordVisit("ws-a", { type: "issue", id: `issue-${i}` });
    expect(useRecentContextStore.getState().byWorkspace[bucket("ws-a")]).toHaveLength(20);
  });

  it("stores a local display snapshot for recent entries", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", {
      type: "issue",
      id: "issue-1",
      label: "MUL-1",
      subtitle: "Fix login redirect",
      status: "todo",
      projectStatus: "in_progress",
      icon: "🚀",
    });

    expect(useRecentContextStore.getState().byWorkspace[bucket("ws-a")]?.[0]).toMatchObject({
      type: "issue",
      id: "issue-1",
      label: "MUL-1",
      subtitle: "Fix login redirect",
      status: "todo",
      projectStatus: "in_progress",
      icon: "🚀",
    });
  });
});

describe("useRecentContextStore.forgetContext", () => {
  it("removes a single context from the workspace bucket", () => {
    const { recordVisit, forgetContext } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-a", { type: "project", id: "project-1" });
    recordVisit("ws-a", { type: "issue", id: "issue-2" });

    forgetContext("ws-a", { type: "project", id: "project-1" });

    const keys = useRecentContextStore
      .getState()
      .byWorkspace[bucket("ws-a")]?.map((e) => `${e.type}:${e.id}`);
    expect(keys).toEqual(["issue:issue-2", "issue:issue-1"]);
  });

  it("does not touch other workspaces' buckets", () => {
    const { recordVisit, forgetContext } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-b", { type: "issue", id: "issue-1" });

    forgetContext("ws-a", { type: "issue", id: "issue-1" });

    const state = useRecentContextStore.getState().byWorkspace;
    expect(state[bucket("ws-a")]).toBeUndefined();
    expect(state[bucket("ws-b")]?.map((e) => e.id)).toEqual(["issue-1"]);
  });
});

describe("selectRecentContexts", () => {
  it("isolates contexts by authenticated user", () => {
    useRecentContextStore.getState().recordVisit("ws-a", { type: "issue", id: "issue-user-1" });
    useRecentContextStore.getState().setIdentity("user-2");

    expect(selectRecentContexts("ws-a")(useRecentContextStore.getState())).toEqual([]);
  });

  it("returns a stable empty array when wsId is null or unknown", () => {
    const a = selectRecentContexts(null)(useRecentContextStore.getState());
    const b = selectRecentContexts(null)(useRecentContextStore.getState());
    const c = selectRecentContexts("missing")(useRecentContextStore.getState());
    expect(a).toBe(b);
    expect(a).toBe(c);
    expect(a).toEqual([]);
  });
});
