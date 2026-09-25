import { create } from "zustand";

/**
 * "Not now" on the department-setup prompt, per workspace.
 *
 * Deliberately in memory, like the welcome and tour stores: closing the
 * prompt is not a decision (that's "Set up" or "Use the default setup", which
 * the server records). It stays closed for this session — across pages and,
 * on desktop, across tabs — and comes back after a reload until an owner or
 * admin actually decides.
 */
interface SetupPromptState {
  dismissed: Record<string, true>;
  dismiss: (workspaceId: string) => void;
}

export const useSetupPromptStore = create<SetupPromptState>((set) => ({
  dismissed: {},
  dismiss: (workspaceId) =>
    set((state) =>
      state.dismissed[workspaceId] ? state : { dismissed: { ...state.dismissed, [workspaceId]: true } },
    ),
}));
