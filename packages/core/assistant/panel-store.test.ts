import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types";
import { createAssistantPanelStore } from "./panel-store";

function memoryStorage(seed: Record<string, string> = {}): StorageAdapter {
  const values = new Map(Object.entries(seed));
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
}

describe("assistant panel store — unseen-result dot", () => {
  it("starts clean and never hydrates from storage (ephemeral by design)", () => {
    const store = createAssistantPanelStore({
      storage: memoryStorage({ "agora:assistant:panel:isOpen": "false" }),
    });

    expect(store.getState().hasUnseenResult).toBe(false);
  });

  it("marks a result unseen while the panel is closed", () => {
    const store = createAssistantPanelStore({ storage: memoryStorage() });

    store.getState().markUnseenResult();

    expect(store.getState().hasUnseenResult).toBe(true);
  });

  it("ignores a result that arrives while the panel is already open", () => {
    const store = createAssistantPanelStore({
      storage: memoryStorage({ "agora:assistant:panel:isOpen": "true" }),
    });

    store.getState().markUnseenResult();

    expect(store.getState().hasUnseenResult).toBe(false);
  });

  it("clears the dot when the panel opens — by setOpen and by toggle", () => {
    const store = createAssistantPanelStore({ storage: memoryStorage() });

    store.getState().markUnseenResult();
    store.getState().setOpen(true);
    expect(store.getState().hasUnseenResult).toBe(false);

    store.getState().setOpen(false);
    store.getState().markUnseenResult();
    expect(store.getState().hasUnseenResult).toBe(true);

    store.getState().toggle();
    expect(store.getState().isOpen).toBe(true);
    expect(store.getState().hasUnseenResult).toBe(false);
  });

  it("clears explicitly (the assistant page consumes the signal too)", () => {
    const store = createAssistantPanelStore({ storage: memoryStorage() });

    store.getState().markUnseenResult();
    store.getState().clearUnseenResult();

    expect(store.getState().hasUnseenResult).toBe(false);
  });
});
