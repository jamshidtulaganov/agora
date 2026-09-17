import { create } from "zustand";
import type { StorageAdapter } from "../types";
import { createLogger } from "../logger";

const logger = createLogger("assistant.panel");

// Geometry + open/closed preference for the floating Assistant panel.
//
// Deliberately GLOBAL — not keyed by workspace and not keyed by identity.
// The assistant itself is user-scoped and spans every workspace (see
// assistant/store.ts), and these four values are pure window chrome: how big
// the panel is and whether it's showing. Nothing here is user content, so a
// shared browser profile leaks nothing by keeping them un-namespaced.
const OPEN_KEY = "agora:assistant:panel:isOpen";
const WIDTH_KEY = "agora:assistant:panel:width";
const HEIGHT_KEY = "agora:assistant:panel:height";
const EXPANDED_KEY = "agora:assistant:panel:expanded";
// Divider position of the assistant page's artifact workbench. Same reasoning
// as the panel geometry above — window chrome, not user content, so it is
// global rather than per-session: a person who widened the pane once wants it
// wide for the next artifact too.
const WORKBENCH_PANE_KEY = "agora:assistant:workbench:paneWidth";

export const ASSISTANT_PANEL_MIN_W = 360;
export const ASSISTANT_PANEL_MIN_H = 480;
export const ASSISTANT_PANEL_DEFAULT_W = 380;
export const ASSISTANT_PANEL_DEFAULT_H = 600;

/** Below this the artifact pane stops being a viewer and starts being a strip. */
export const ASSISTANT_WORKBENCH_PANE_MIN_W = 320;
/** The transcript never shrinks past a readable message column. */
export const ASSISTANT_WORKBENCH_CHAT_MIN_W = 400;
export const ASSISTANT_WORKBENCH_PANE_DEFAULT_W = 520;

export interface AssistantPanelState {
  isOpen: boolean;
  /**
   * A run finished while the panel was closed — drives the dot on the FAB.
   * Deliberately ephemeral (never persisted): a badge that survives a reload
   * is a badge nobody trusts.
   */
  hasUnseenResult: boolean;
  /** Raw user-chosen size — no clamp applied. The UI clamps at render time. */
  panelWidth: number;
  panelHeight: number;
  isExpanded: boolean;
  /**
   * Width of the artifact pane on the assistant page, in px. Raw user choice —
   * the workbench clamps it against the live container width at render time,
   * so a value stored on a wide monitor can't strand the transcript on a
   * laptop.
   */
  workbenchPaneWidth: number;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  /** Persist raw size and auto-exit expanded mode. */
  setPanelSize: (width: number, height: number) => void;
  setExpanded: (expanded: boolean) => void;
  /** Persist the dragged divider position. */
  setWorkbenchPaneWidth: (width: number) => void;
  /** Double-click on the divider — back to the default split. */
  resetWorkbenchPaneWidth: () => void;
  markUnseenResult: () => void;
  clearUnseenResult: () => void;
}

export interface AssistantPanelStoreOptions {
  storage: StorageAdapter;
}

export function createAssistantPanelStore(options: AssistantPanelStoreOptions) {
  const { storage } = options;

  // Default CLOSED. The assistant already has a permanent home (the sidebar
  // item and the full page); the panel is the quick-access surface, so it
  // opens on intent rather than covering the app on first load. The FAB
  // carries discoverability.
  const initialIsOpen = storage.getItem(OPEN_KEY) === "true";

  return create<AssistantPanelState>((set, get) => ({
    isOpen: initialIsOpen,
    hasUnseenResult: false,
    panelWidth: Number(storage.getItem(WIDTH_KEY)) || ASSISTANT_PANEL_DEFAULT_W,
    panelHeight: Number(storage.getItem(HEIGHT_KEY)) || ASSISTANT_PANEL_DEFAULT_H,
    isExpanded: storage.getItem(EXPANDED_KEY) === "true",
    workbenchPaneWidth:
      Number(storage.getItem(WORKBENCH_PANE_KEY)) || ASSISTANT_WORKBENCH_PANE_DEFAULT_W,
    setOpen: (open) => {
      logger.debug("setOpen", { from: get().isOpen, to: open });
      storage.setItem(OPEN_KEY, String(open));
      // Opening IS seeing it — the result is on screen a frame later.
      set(open ? { isOpen: true, hasUnseenResult: false } : { isOpen: false });
    },
    toggle: () => {
      const next = !get().isOpen;
      logger.debug("toggle", { to: next });
      storage.setItem(OPEN_KEY, String(next));
      set(next ? { isOpen: true, hasUnseenResult: false } : { isOpen: false });
    },
    setPanelSize: (width, height) => {
      storage.setItem(WIDTH_KEY, String(width));
      storage.setItem(HEIGHT_KEY, String(height));
      // Dragging = the user chose a manual size → leave expanded mode.
      storage.removeItem(EXPANDED_KEY);
      set({ panelWidth: width, panelHeight: height, isExpanded: false });
    },
    setExpanded: (expanded) => {
      logger.info("setExpanded", { to: expanded });
      if (expanded) storage.setItem(EXPANDED_KEY, "true");
      else storage.removeItem(EXPANDED_KEY);
      set({ isExpanded: expanded });
    },
    setWorkbenchPaneWidth: (width) => {
      // Guard the storage write, not the state: a NaN from a pointer event on
      // a detached element would otherwise poison the key for every later
      // session (`Number(null)` reads back as 0 → the default, but `"NaN"`
      // reads back as NaN → a pane with no width).
      if (!Number.isFinite(width) || width <= 0) return;
      const next = Math.round(width);
      storage.setItem(WORKBENCH_PANE_KEY, String(next));
      set({ workbenchPaneWidth: next });
    },
    resetWorkbenchPaneWidth: () => {
      storage.removeItem(WORKBENCH_PANE_KEY);
      set({ workbenchPaneWidth: ASSISTANT_WORKBENCH_PANE_DEFAULT_W });
    },
    markUnseenResult: () => {
      if (get().isOpen || get().hasUnseenResult) return;
      set({ hasUnseenResult: true });
    },
    clearUnseenResult: () => {
      if (!get().hasUnseenResult) return;
      set({ hasUnseenResult: false });
    },
  }));
}

export type AssistantPanelStoreInstance = ReturnType<typeof createAssistantPanelStore>;

/** Module-level singleton — set once at app boot via `registerAssistantPanelStore()`. */
let _store: AssistantPanelStoreInstance | null = null;

/**
 * Register the panel store instance created by the app. Must be called at
 * boot before any component renders (see platform/core-provider.tsx).
 */
export function registerAssistantPanelStore(store: AssistantPanelStoreInstance) {
  _store = store;
}

/**
 * Singleton accessor — a Zustand hook backed by the registered instance.
 * Supports `useAssistantPanelStore(selector)` and `.getState()`.
 */
export const useAssistantPanelStore: AssistantPanelStoreInstance = new Proxy(
  (() => {}) as unknown as AssistantPanelStoreInstance,
  {
    apply(_target, _thisArg, args) {
      if (!_store) {
        throw new Error(
          "Assistant panel store not initialised — call registerAssistantPanelStore() first",
        );
      }
      return (_store as unknown as (...a: unknown[]) => unknown)(...args);
    },
    get(_target, prop) {
      if (!_store) return undefined;
      return Reflect.get(_store, prop);
    },
  },
);
