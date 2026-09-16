import { create } from "zustand";
import type { StorageAdapter } from "../types";
import { createLogger } from "../logger";
import type { AssistantRunContext } from "../types";

const logger = createLogger("assistant.store");

// Deliberately GLOBAL, not per-workspace: the Agora Assistant is user-scoped
// and spans every workspace the person belongs to (see
// docs/agora-assistant-plan.md §3). Unlike chat/store.ts, there is no
// per-workspace rehydration here — only per-identity, so switching the
// logged-in user on the same device can't leak the previous user's active
// session id.
const SESSION_STORAGE_KEY = "agora:assistant:activeSessionId";
const DRAFT_STORAGE_KEY = "agora:assistant:drafts";

export interface AssistantDraft {
  content: string;
  request_id: string;
  context?: AssistantRunContext;
}

function readDrafts(raw: string | null): Record<string, AssistantDraft> {
  if (!raw) return {};
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object" || Array.isArray(value)) return {};
    return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, AssistantDraft] => {
      const draft = entry[1];
      if (!draft || typeof draft !== "object" || Array.isArray(draft)) return false;
      const candidate = draft as Partial<AssistantDraft>;
      if (typeof candidate.content !== "string" || typeof candidate.request_id !== "string") return false;
      if (candidate.context === undefined) return true;
      const context = candidate.context;
      return !!context && typeof context === "object" && !Array.isArray(context) &&
        (context.workspace_id === null || typeof context.workspace_id === "string") &&
        (context.timezone === undefined || typeof context.timezone === "string");
    }));
  } catch { return {}; }
}

export interface AssistantState {
  identityId: string | null;
  /** Session currently shown in the transcript. Persisted per identity. */
  activeSessionId: string | null;
  draftsBySession: Record<string, AssistantDraft>;
  /**
   * Artifact currently shown in the split pane, keyed by session id.
   * Ephemeral — deliberately NOT persisted (a reopened app should land on
   * the transcript, not on whatever pane was open days ago) and cleared on
   * identity switch, same as the other ephemeral maps here.
   */
  openArtifactId: Record<string, string>;
  setIdentity: (identityId: string | null) => void;
  setActiveSession: (id: string | null) => void;
  setDraft: (sessionId: string, draft: AssistantDraft | null) => void;
  setOpenArtifact: (sessionId: string, artifactId: string | null) => void;
}

export interface AssistantStoreOptions {
  storage: StorageAdapter;
}

export function createAssistantStore(options: AssistantStoreOptions) {
  const { storage } = options;

  const identityKey = (identityId: string | null) =>
    identityId ? `${SESSION_STORAGE_KEY}:${identityId}` : null;

  const store = create<AssistantState>((set, get) => ({
    identityId: null,
    // Stays empty until AuthInitializer establishes the authenticated
    // identity — same guard as chat/store.ts, so a reused browser profile
    // can't render a previous user's active session before identity resolves.
    activeSessionId: null,
    draftsBySession: {},
    openArtifactId: {},
    setIdentity: (identityId) => {
      const key = identityKey(identityId);
      logger.info("setIdentity", { identityId });
      set({
        identityId,
        activeSessionId: key ? storage.getItem(key) : null,
        // Ephemeral state belongs to whatever was running under the
        // previous identity — never carry it across a user switch.
        draftsBySession: identityId ? readDrafts(storage.getItem(`${DRAFT_STORAGE_KEY}:${identityId}`)) : {},
        openArtifactId: {},
      });
    },
    setActiveSession: (id) => {
      logger.info("setActiveSession", { from: get().activeSessionId, to: id });
      const key = identityKey(get().identityId);
      if (key) {
        if (id) storage.setItem(key, id);
        else storage.removeItem(key);
      }
      set({ activeSessionId: id });
    },
    setDraft: (sessionId, draft) => {
      const current = get().draftsBySession;
      const next = { ...current };
      if (draft) next[sessionId] = draft;
      else delete next[sessionId];
      const identityId = get().identityId;
      if (identityId) storage.setItem(`${DRAFT_STORAGE_KEY}:${identityId}`, JSON.stringify(next));
      set({ draftsBySession: next });
    },
    setOpenArtifact: (sessionId, artifactId) => {
      const current = get().openArtifactId;
      if ((current[sessionId] ?? null) === artifactId) return;
      const next = { ...current };
      if (artifactId) next[sessionId] = artifactId;
      else delete next[sessionId];
      set({ openArtifactId: next });
    },
  }));

  return store;
}

export type AssistantStoreInstance = ReturnType<typeof createAssistantStore>;

/** Module-level singleton — set once at app boot via `registerAssistantStore()`. */
let _store: AssistantStoreInstance | null = null;

/**
 * Register the assistant store instance created by the app. Must be called
 * at boot before any component renders (see platform/core-provider.tsx).
 */
export function registerAssistantStore(store: AssistantStoreInstance) {
  _store = store;
}

/**
 * Singleton accessor — a Zustand hook backed by the registered instance.
 * Supports `useAssistantStore(selector)` and `useAssistantStore.getState()`.
 */
export const useAssistantStore: AssistantStoreInstance = new Proxy(
  (() => {}) as unknown as AssistantStoreInstance,
  {
    apply(_target, _thisArg, args) {
      if (!_store) {
        throw new Error(
          "Assistant store not initialised — call registerAssistantStore() first",
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
