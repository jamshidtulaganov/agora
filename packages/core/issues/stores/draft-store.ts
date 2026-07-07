import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import type { IssueStatus, IssuePriority, IssueAssigneeType, Attachment } from "../../types";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

interface IssueDraft {
  title: string;
  description: string;
  status: IssueStatus;
  priority: IssuePriority;
  assigneeType?: IssueAssigneeType;
  assigneeId?: string;
  startDate: string | null;
  dueDate: string | null;
  attachments: Attachment[];
}

const EMPTY_DRAFT: IssueDraft = {
  title: "",
  description: "",
  status: "todo",
  priority: "none",
  assigneeType: undefined,
  assigneeId: undefined,
  startDate: null,
  dueDate: null,
  attachments: [],
};

interface IssueDraftStore {
  draft: IssueDraft;
  // Last assignee picked at submit time. Persisted across drafts so the
  // create-issue modal can prefill the picker with the user's most recent
  // choice instead of always opening with no assignee.
  lastAssigneeType?: IssueAssigneeType;
  lastAssigneeId?: string;
  setDraft: (patch: Partial<IssueDraft>) => void;
  clearDraft: () => void;
  setLastAssignee: (type?: IssueAssigneeType, id?: string) => void;
  hasDraft: () => boolean;
}

export const useIssueDraftStore = create<IssueDraftStore>()(
  persist(
    (set, get) => ({
      draft: { ...EMPTY_DRAFT },
      lastAssigneeType: undefined,
      lastAssigneeId: undefined,
      setDraft: (patch) =>
        set((s) => ({ draft: { ...s.draft, ...patch } })),
      clearDraft: () =>
        set((s) => ({
          draft: {
            ...EMPTY_DRAFT,
            assigneeType: s.lastAssigneeType,
            assigneeId: s.lastAssigneeId,
          },
        })),
      setLastAssignee: (type, id) =>
        set({ lastAssigneeType: type, lastAssigneeId: id }),
      hasDraft: () => {
        const { draft } = get();
        return !!(draft.title || draft.description);
      },
    }),
    {
      name: "agora_issue_draft",
      // v1: one-time forced clear of draft.description. An earlier
      // agent→manual leak persisted an action prompt (e.g. a learn-conventions
      // directive) as the draft body, so "Create manually" kept opening with a
      // stale "Extract the coding conventions…" description. The runtime fix
      // (quick-create switchToManual + equality self-heal) stops recurrence,
      // but a description already saved before it — and not byte-equal to the
      // prompt store — survived. Bumping the version wipes description once for
      // everyone on load, no manual localStorage clear needed. Cost: an
      // in-progress description draft is dropped once (title/status/etc. kept).
      version: 1,
      migrate: (persisted, fromVersion) => {
        const s = (persisted ?? {}) as Partial<IssueDraftStore>;
        if (fromVersion < 1 && s.draft) {
          s.draft = { ...s.draft, description: "" };
        }
        return s;
      },
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      // Drafts persisted by older builds predate fields added later (e.g.
      // `attachments`). Backfill EMPTY_DRAFT defaults on rehydrate so every
      // read site can rely on the declared IssueDraft shape instead of
      // re-defending with `?? fallback`.
      merge: (persistedState, currentState) => {
        const persisted = (persistedState ?? {}) as Partial<IssueDraftStore>;
        return {
          ...currentState,
          ...persisted,
          draft: { ...EMPTY_DRAFT, ...persisted.draft },
        };
      },
    },
  ),
);

registerForWorkspaceRehydration(() => useIssueDraftStore.persist.rehydrate());
