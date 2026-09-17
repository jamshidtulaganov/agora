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
    const draft = { content: "hello", request_id: "request-1", context: { workspace_id: "ws-1", timezone: "Asia/Tashkent", project_id: "project-1", attachment_ids: ["file-1"] } };
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

  it.each([
    { project_id: 5 },
    { attachment_ids: ["file-1", 7] },
    { attachment_ids: "file-1" },
    { project_id: "project-1", workspace_id: null },
  ])("drops a draft with malformed project or file context", (bad) => {
    const storage = memoryStorage({
      "agora:assistant:drafts:user-a": JSON.stringify({
        "session-a": { content: "retry", request_id: "request-1", context: { workspace_id: "ws-1", ...bad } },
      }),
    });
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    expect(store.getState().draftsBySession).toEqual({});
  });

  it("persists composer selection and clears project/files on workspace change", () => {
    const storage = memoryStorage();
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    const selected = { workspace_id: "ws-1", project_id: "project-1", attachments: [{ id: "file-1", filename: "report.md", size_bytes: 42 }] };
    store.getState().setComposerContext("session-a", selected);
    store.getState().setIdentity("user-b");
    expect(store.getState().composerContextBySession).toEqual({});
    store.getState().setIdentity("user-a");
    expect(store.getState().composerContextBySession["session-a"]).toEqual(selected);

    store.getState().setComposerContext("session-a", { workspace_id: "ws-2", project_id: "wrong-project", attachments: selected.attachments });
    expect(store.getState().composerContextBySession["session-a"]).toEqual({ workspace_id: "ws-2", project_id: null, member: null, attachments: [] });
    expect(store.getState().draftsBySession).toEqual({});
  });

  it("keeps a pinned workspace and an attached member across a reload", () => {
    const storage = memoryStorage();
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    const selected = {
      workspace_id: "ws-2",
      workspace_pinned: true,
      member: { user_id: "user-7", name: "Dana" },
    };
    store.getState().setComposerContext("session-a", selected);
    store.getState().setIdentity("user-b");
    store.getState().setIdentity("user-a");
    expect(store.getState().composerContextBySession["session-a"]).toEqual(selected);
  });

  // A member belongs to the workspace it was picked in, so a persisted blob
  // that lost the workspace — or the member's id — is dropped rather than
  // replayed into whichever workspace the composer opens on next.
  it.each([
    { workspace_id: "ws-1", member: { user_id: "", name: "Dana" } },
    { workspace_id: "ws-1", member: { user_id: "user-7" } },
    { workspace_id: null, member: { user_id: "user-7", name: "Dana" } },
    { workspace_id: null, workspace_pinned: true },
    { workspace_id: "ws-1", workspace_pinned: "yes" },
  ])("drops a malformed persisted composer scope (%j)", (bad) => {
    const storage = memoryStorage({
      "agora:assistant:composerContext:user-a": JSON.stringify({ "session-a": bad }),
    });
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    expect(store.getState().composerContextBySession).toEqual({});
  });

  it("drops malformed persisted composer attachments", () => {
    const storage = memoryStorage({
      "agora:assistant:composerContext:user-a": JSON.stringify({ "session-a": { workspace_id: "ws-1", attachments: [{ id: 7, filename: "bad", size_bytes: 1 }] } }),
    });
    const store = createAssistantStore({ storage });
    store.getState().setIdentity("user-a");
    expect(store.getState().composerContextBySession).toEqual({});
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
