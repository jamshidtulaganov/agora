import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types";
import { createAssistantStore } from "./store";

function memoryStorage(seed: Record<string, string> = {}): StorageAdapter & {
  values: Map<string, string>;
} {
  const values = new Map(Object.entries(seed));
  return {
    values,
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
}

describe("createAssistantStore identity scoping", () => {
  it("does not hydrate an active session before identity resolves", () => {
    const storage = memoryStorage({
      "agora:assistant:activeSessionId:user-a": "session-a",
    });
    const store = createAssistantStore({ storage });

    expect(store.getState().activeSessionId).toBeNull();
  });

  it("hydrates and persists the active session per identity — GLOBAL, not per-workspace", () => {
    const storage = memoryStorage({
      "agora:assistant:activeSessionId:user-a": "session-a",
      "agora:assistant:activeSessionId:user-b": "session-b",
    });
    const store = createAssistantStore({ storage });

    store.getState().setIdentity("user-a");
    expect(store.getState().activeSessionId).toBe("session-a");

    store.getState().setActiveSession("session-a-next");
    expect(storage.values.get("agora:assistant:activeSessionId:user-a")).toBe(
      "session-a-next",
    );

    store.getState().setIdentity("user-b");
    expect(store.getState().activeSessionId).toBe("session-b");
    // Switching identity does not clobber the other user's persisted value.
    expect(storage.values.get("agora:assistant:activeSessionId:user-a")).toBe(
      "session-a-next",
    );
  });

  it("clears the persisted session when set to null", () => {
    const storage = memoryStorage();
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");

    store.getState().setActiveSession("session-a");
    expect(storage.values.get("agora:assistant:activeSessionId:user-a")).toBe("session-a");

    store.getState().setActiveSession(null);
    expect(storage.values.has("agora:assistant:activeSessionId:user-a")).toBe(false);
    expect(store.getState().activeSessionId).toBeNull();
  });
});

describe("createAssistantStore drafts and artifact state", () => {
  it("persists drafts per identity with retry context", () => {
    const storage = memoryStorage();
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    const draft = { content: "hello", request_id: "request-1", context: { workspace_id: "ws-1", timezone: "Asia/Tashkent" } };
    store.getState().setDraft("session-a", draft);
    expect(store.getState().draftsBySession["session-a"]).toEqual(draft);

    store.getState().setIdentity("user-b");
    expect(store.getState().draftsBySession).toEqual({});
    store.getState().setIdentity("user-a");
    expect(store.getState().draftsBySession["session-a"]).toEqual(draft);
    store.getState().setDraft("session-a", null);
    expect(store.getState().draftsBySession).toEqual({});
  });

  it("ignores malformed persisted drafts", () => {
    const store = createAssistantStore({ storage: memoryStorage({ "agora:assistant:drafts:user-a": "bad json" }) });
    store.getState().setIdentity("user-a");
    expect(store.getState().draftsBySession).toEqual({});
  });

  it("drops a draft with malformed persisted context", () => {
    const storage = memoryStorage({
      "agora:assistant:drafts:user-a": JSON.stringify({
        "session-a": { content: "retry", request_id: "request-1", context: { workspace_id: 42 } },
      }),
    });
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    expect(store.getState().draftsBySession).toEqual({});
  });

  it("tracks open artifacts without persisting them", () => {
    const storage = memoryStorage();
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    store.getState().setOpenArtifact("session-a", "artifact-1");
    expect(store.getState().openArtifactId).toEqual({ "session-a": "artifact-1" });
    expect([...storage.values.keys()]).toEqual([]);
    store.getState().setIdentity("user-b");
    expect(store.getState().openArtifactId).toEqual({});
  });
});
