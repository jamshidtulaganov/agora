import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types";
import {
  ASSISTANT_WORKBENCH_PANE_DEFAULT_W,
  createAssistantPanelStore,
} from "./panel-store";

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

describe("assistant panel store — workbench divider", () => {
  it("defaults the pane width and persists a dragged one", () => {
    const values: Record<string, string> = {};
    const storage: StorageAdapter = {
      getItem: (key) => values[key] ?? null,
      setItem: (key, value) => {
        values[key] = value;
      },
      removeItem: (key) => {
        delete values[key];
      },
    };
    const store = createAssistantPanelStore({ storage });

    expect(store.getState().workbenchPaneWidth).toBe(ASSISTANT_WORKBENCH_PANE_DEFAULT_W);

    store.getState().setWorkbenchPaneWidth(640.4);

    expect(store.getState().workbenchPaneWidth).toBe(640);
    expect(values["agora:assistant:workbench:paneWidth"]).toBe("640");
  });

  it("rehydrates the persisted width on the next boot", () => {
    const store = createAssistantPanelStore({
      storage: memoryStorage({ "agora:assistant:workbench:paneWidth": "700" }),
    });

    expect(store.getState().workbenchPaneWidth).toBe(700);
  });

  it("falls back to the default when the stored value is junk", () => {
    const store = createAssistantPanelStore({
      storage: memoryStorage({ "agora:assistant:workbench:paneWidth": "not-a-number" }),
    });

    expect(store.getState().workbenchPaneWidth).toBe(ASSISTANT_WORKBENCH_PANE_DEFAULT_W);
  });

  it("refuses to persist a non-finite width (a pointer event on a detached node)", () => {
    const store = createAssistantPanelStore({ storage: memoryStorage() });

    store.getState().setWorkbenchPaneWidth(Number.NaN);
    store.getState().setWorkbenchPaneWidth(0);

    expect(store.getState().workbenchPaneWidth).toBe(ASSISTANT_WORKBENCH_PANE_DEFAULT_W);
  });

  it("double-click reset clears the stored preference, not just the state", () => {
    const values: Record<string, string> = { "agora:assistant:workbench:paneWidth": "700" };
    const storage: StorageAdapter = {
      getItem: (key) => values[key] ?? null,
      setItem: (key, value) => {
        values[key] = value;
      },
      removeItem: (key) => {
        delete values[key];
      },
    };
    const store = createAssistantPanelStore({ storage });

    store.getState().resetWorkbenchPaneWidth();

    expect(store.getState().workbenchPaneWidth).toBe(ASSISTANT_WORKBENCH_PANE_DEFAULT_W);
    expect(values["agora:assistant:workbench:paneWidth"]).toBeUndefined();
  });
});
