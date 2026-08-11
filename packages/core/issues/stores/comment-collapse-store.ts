import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

/**
 * Tracks comment disclosure overrides by issue. Human comments are expanded
 * by default and persist collapsed IDs; agent updates are compact by default
 * and persist only the IDs the user explicitly expanded.
 */
interface CommentCollapseStore {
  collapsedByIssue: Record<string, string[]>;
  expandedByIssue: Record<string, string[]>;
  isCollapsed: (issueId: string, commentId: string, defaultCollapsed?: boolean) => boolean;
  toggle: (issueId: string, commentId: string, defaultCollapsed?: boolean) => void;
}

export const useCommentCollapseStore = create<CommentCollapseStore>()(
  persist(
    (set, get) => ({
      collapsedByIssue: {},
      expandedByIssue: {},
      isCollapsed: (issueId, commentId, defaultCollapsed = false) => {
        const state = get();
        const ids = defaultCollapsed
          ? state.expandedByIssue[issueId]
          : state.collapsedByIssue[issueId];
        const contains = ids ? ids.includes(commentId) : false;
        return defaultCollapsed ? !contains : contains;
      },
      toggle: (issueId, commentId, defaultCollapsed = false) =>
        set((s) => {
          const stateKey = defaultCollapsed ? "expandedByIssue" : "collapsedByIssue";
          const currentState = s[stateKey];
          const current = currentState[issueId] ?? [];
          const contains = current.includes(commentId);
          if (contains) {
            const next = current.filter((id) => id !== commentId);
            if (next.length === 0) {
              const { [issueId]: _, ...rest } = currentState;
              return { [stateKey]: rest };
            }
            return { [stateKey]: { ...currentState, [issueId]: next } };
          }
          return { [stateKey]: { ...currentState, [issueId]: [...current, commentId] } };
        }),
    }),
    {
      name: "agora_comment_collapse",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
    },
  ),
);

registerForWorkspaceRehydration(() => useCommentCollapseStore.persist.rehydrate());
