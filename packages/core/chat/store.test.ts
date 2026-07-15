import { beforeEach, describe, expect, it } from "vitest";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import type { StorageAdapter } from "../types";
import { createChatStore } from "./store";

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

beforeEach(() => {
  setCurrentWorkspace(null, null);
});

describe("createChatStore identity scoping", () => {
  it("does not hydrate legacy workspace-only session state", () => {
    setCurrentWorkspace("acme", "ws-1");
    const storage = memoryStorage({
      "agora:chat:activeSessionId:acme": "legacy-session",
    });
    const store = createChatStore({ storage });

    store.getState().setIdentity("user-a");

    expect(store.getState().activeSessionId).toBeNull();
  });

  it("hydrates and persists sessions separately for each user and workspace", () => {
    setCurrentWorkspace("acme", "ws-1");
    const storage = memoryStorage({
      "agora:chat:activeSessionId:user-a:acme": "session-a",
      "agora:chat:activeSessionId:user-b:acme": "session-b",
    });
    const store = createChatStore({ storage });

    store.getState().setIdentity("user-a");
    expect(store.getState().activeSessionId).toBe("session-a");

    store.getState().setActiveSession("session-a-next");
    expect(storage.values.get("agora:chat:activeSessionId:user-a:acme")).toBe(
      "session-a-next",
    );

    store.getState().setIdentity("user-b");
    expect(store.getState().activeSessionId).toBe("session-b");
    expect(storage.values.get("agora:chat:activeSessionId:user-a:acme")).toBe(
      "session-a-next",
    );
  });
});
