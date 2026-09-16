import { create } from "zustand";
import type { StorageAdapter } from "../types";
import { getCurrentSlug, registerForWorkspaceRehydration } from "../platform/workspace-storage";
import { createLogger } from "../logger";

const logger = createLogger("chat.store");

const AGENT_STORAGE_KEY = "agora:chat:selectedAgentId";
const SESSION_STORAGE_KEY = "agora:chat:activeSessionId";

/**
 * Client-side pointers into the agent-chat feature: which session the user is
 * looking at, and which agent they last talked to. Both are per identity AND
 * per workspace — a reused browser profile must not surface another user's
 * session, and a session belongs to exactly one workspace.
 *
 * The floating chat popup that used to own this store is gone (the bottom-
 * right bubble now opens the Agora Assistant; see assistant/panel-store.ts).
 * What remains is what other surfaces still read: the editor's slash-command
 * suggestion resolves skills against `selectedAgentId`, and the global WS
 * handler clears `activeSessionId` when the session it points at is deleted.
 */

export interface ChatState {
  identityId: string | null;
  activeSessionId: string | null;
  selectedAgentId: string | null;
  setIdentity: (identityId: string | null) => void;
  setActiveSession: (id: string | null) => void;
  setSelectedAgentId: (id: string) => void;
}

export interface ChatStoreOptions {
  storage: StorageAdapter;
}

export function createChatStore(options: ChatStoreOptions) {
  const { storage } = options;

  const identityWorkspaceKey = (base: string, identityId: string | null) => {
    const slug = getCurrentSlug();
    return slug && identityId ? `${base}:${identityId}:${slug}` : null;
  };

  const store = create<ChatState>((set, get) => ({
    identityId: null,
    // User-owned values stay empty until AuthInitializer establishes the
    // authenticated identity. Workspace-only keys leaked another user's
    // session when a browser profile was reused.
    activeSessionId: null,
    selectedAgentId: null,
    setIdentity: (identityId) => {
      const sessionKey = identityWorkspaceKey(SESSION_STORAGE_KEY, identityId);
      const agentKey = identityWorkspaceKey(AGENT_STORAGE_KEY, identityId);
      set({
        identityId,
        activeSessionId: sessionKey ? storage.getItem(sessionKey) : null,
        selectedAgentId: agentKey ? storage.getItem(agentKey) : null,
      });
    },
    setActiveSession: (id) => {
      logger.info("setActiveSession", { from: get().activeSessionId, to: id });
      const key = identityWorkspaceKey(SESSION_STORAGE_KEY, get().identityId);
      if (key) {
        if (id) storage.setItem(key, id);
        else storage.removeItem(key);
      }
      set({ activeSessionId: id });
    },
    setSelectedAgentId: (id) => {
      logger.info("setSelectedAgentId", { from: get().selectedAgentId, to: id });
      const key = identityWorkspaceKey(AGENT_STORAGE_KEY, get().identityId);
      if (key) storage.setItem(key, id);
      set({ selectedAgentId: id });
    },
  }));

  registerForWorkspaceRehydration(() => {
    const identityId = store.getState().identityId;
    const sessionKey = identityWorkspaceKey(SESSION_STORAGE_KEY, identityId);
    const agentKey = identityWorkspaceKey(AGENT_STORAGE_KEY, identityId);
    const nextSession = sessionKey ? storage.getItem(sessionKey) : null;
    const nextAgent = agentKey ? storage.getItem(agentKey) : null;
    logger.info("workspace rehydration", {
      prevSession: store.getState().activeSessionId,
      nextSession,
      prevAgent: store.getState().selectedAgentId,
      nextAgent,
    });
    store.setState({
      activeSessionId: nextSession,
      selectedAgentId: nextAgent,
    });
  });

  return store;
}
