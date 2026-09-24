import { create } from "zustand";

/**
 * One-shot signal that starts the in-app product tour — the spotlight walk
 * over the sidebar (My tasks, Issues, Inbox, Assistant) a person gets right
 * after finishing the member setup.
 *
 * Deliberately in-memory, like the welcome store: a tour is an introduction,
 * not a preference. A reload mid-tour simply ends it; it never comes back on
 * its own. Keyed on the workspace the setup opened, so the tour only renders
 * inside that workspace's shell.
 */
interface ProductTourState {
  workspaceId: string | null;
  start: (workspaceId: string) => void;
  stop: () => void;
}

export const useProductTourStore = create<ProductTourState>((set) => ({
  workspaceId: null,
  start: (workspaceId) => set({ workspaceId }),
  stop: () => set({ workspaceId: null }),
}));
