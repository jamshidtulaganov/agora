import { create } from "zustand";
import type { StorageAdapter } from "../types";
import { getCurrentSlug, registerForWorkspaceRehydration } from "../platform/workspace-storage";
import { createLogger } from "../logger";

const logger = createLogger("chat.store");

const AGENT_STORAGE_KEY = "agora:chat:selectedAgentId";
const SESSION_STORAGE_KEY = "agora:chat:activeSessionId";
/** Drafts are stored as one JSON blob per workspace: { [sessionId]: text }. */
const DRAFTS_KEY = "agora:chat:drafts";
/** Placeholder sessionId for a chat that hasn't been created yet. */
export const DRAFT_NEW_SESSION = "__new__";
const CHAT_WIDTH_KEY = "agora:chat:width";
const CHAT_HEIGHT_KEY = "agora:chat:height";
const CHAT_EXPANDED_KEY = "agora:chat:expanded";
/**
 * Open/closed preference, persisted globally (not per-workspace) — most users
 * have one habitual chat-panel preference across workspaces. Missing key =
 * new user (or cleared storage); default to OPEN so the chat is discoverable.
 * Once the user toggles even once, their explicit choice is respected on
 * every subsequent reload.
 */
const OPEN_KEY = "agora:chat:isOpen";

function readDrafts(storage: StorageAdapter, key: string): Record<string, string> {
  const raw = storage.getItem(key);
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw);
    return typeof parsed === "object" && parsed !== null ? parsed : {};
  } catch {
    return {};
  }
}

function writeDrafts(storage: StorageAdapter, key: string, drafts: Record<string, string>) {
  // Prune empty entries so the blob doesn't grow unbounded.
  const pruned: Record<string, string> = {};
  for (const [k, v] of Object.entries(drafts)) {
    if (v) pruned[k] = v;
  }
  if (Object.keys(pruned).length === 0) {
    storage.removeItem(key);
  } else {
    storage.setItem(key, JSON.stringify(pruned));
  }
}

export const CHAT_MIN_W = 360;
export const CHAT_MIN_H = 480;
export const CHAT_DEFAULT_W = 380;
export const CHAT_DEFAULT_H = 600;

/**
 * Kept as a public type because existing consumers (chat-message-list,
 * views/chat types) import it. Items themselves no longer live in the
 * store — they flow through the React Query cache keyed by task id.
 */
export interface ChatTimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
  created_at?: string;
}

export interface ChatState {
  identityId: string | null;
  isOpen: boolean;
  activeSessionId: string | null;
  selectedAgentId: string | null;
  /** Drafts per session: sessionId (or DRAFT_NEW_SESSION) → markdown text. */
  inputDrafts: Record<string, string>;
  /** Raw user-chosen size — no clamp applied. UI layer clamps at render time. */
  chatWidth: number;
  chatHeight: number;
  isExpanded: boolean;
  setIdentity: (identityId: string | null) => void;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  setActiveSession: (id: string | null) => void;
  setSelectedAgentId: (id: string) => void;
  /** sessionId accepts a real session UUID or DRAFT_NEW_SESSION. */
  setInputDraft: (sessionId: string, draft: string) => void;
  clearInputDraft: (sessionId: string) => void;
  /** Persist raw size and auto-exit expanded mode. */
  setChatSize: (width: number, height: number) => void;
  setExpanded: (expanded: boolean) => void;
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

  // Resolve initial isOpen from storage. The three-state read (null /
  // "true" / "false") is what enables the "new user → open" default while
  // still honouring an explicit "I closed it" choice on every reload.
  const storedOpen = storage.getItem(OPEN_KEY);
  const initialIsOpen = storedOpen === null ? true : storedOpen === "true";

  const store = create<ChatState>((set, get) => ({
    identityId: null,
    isOpen: initialIsOpen,
    // User-owned values stay empty until AuthInitializer establishes the
    // authenticated identity. Workspace-only keys leaked another user's
    // session and drafts when a browser profile was reused.
    activeSessionId: null,
    selectedAgentId: null,
    inputDrafts: {},
    chatWidth: Number(storage.getItem(CHAT_WIDTH_KEY)) || CHAT_DEFAULT_W,
    chatHeight: Number(storage.getItem(CHAT_HEIGHT_KEY)) || CHAT_DEFAULT_H,
    isExpanded: false,
    setIdentity: (identityId) => {
      const sessionKey = identityWorkspaceKey(SESSION_STORAGE_KEY, identityId);
      const agentKey = identityWorkspaceKey(AGENT_STORAGE_KEY, identityId);
      const draftsKey = identityWorkspaceKey(DRAFTS_KEY, identityId);
      const expandedKey = identityWorkspaceKey(CHAT_EXPANDED_KEY, identityId);
      set({
        identityId,
        activeSessionId: sessionKey ? storage.getItem(sessionKey) : null,
        selectedAgentId: agentKey ? storage.getItem(agentKey) : null,
        inputDrafts: draftsKey ? readDrafts(storage, draftsKey) : {},
        isExpanded: expandedKey ? storage.getItem(expandedKey) === "true" : false,
      });
    },
    setOpen: (open) => {
      logger.debug("setOpen", { from: get().isOpen, to: open });
      storage.setItem(OPEN_KEY, String(open));
      set({ isOpen: open });
    },
    toggle: () => {
      const next = !get().isOpen;
      logger.debug("toggle", { to: next });
      storage.setItem(OPEN_KEY, String(next));
      set({ isOpen: next });
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
    setInputDraft: (sessionId, draft) => {
      // Debug level — onUpdate fires on every keystroke.
      logger.debug("setInputDraft", { sessionId, length: draft.length });
      const next = { ...get().inputDrafts, [sessionId]: draft };
      const key = identityWorkspaceKey(DRAFTS_KEY, get().identityId);
      if (key) writeDrafts(storage, key, next);
      set({ inputDrafts: next });
    },
    clearInputDraft: (sessionId) => {
      const current = get().inputDrafts;
      if (!(sessionId in current)) {
        logger.debug("clearInputDraft skipped (no draft)", { sessionId });
        return;
      }
      logger.info("clearInputDraft", { sessionId });
      const next = { ...current };
      delete next[sessionId];
      const key = identityWorkspaceKey(DRAFTS_KEY, get().identityId);
      if (key) writeDrafts(storage, key, next);
      set({ inputDrafts: next });
    },
    setChatSize: (w, h) => {
      logger.debug("setChatSize", { w, h });
      storage.setItem(CHAT_WIDTH_KEY, String(w));
      storage.setItem(CHAT_HEIGHT_KEY, String(h));
      // Dragging = user chose a manual size → exit expanded mode
      const key = identityWorkspaceKey(CHAT_EXPANDED_KEY, get().identityId);
      if (key) storage.removeItem(key);
      set({ chatWidth: w, chatHeight: h, isExpanded: false });
    },
    setExpanded: (expanded) => {
      logger.info("setExpanded", { to: expanded });
      const key = identityWorkspaceKey(CHAT_EXPANDED_KEY, get().identityId);
      if (key) {
        if (expanded) storage.setItem(key, "true");
        else storage.removeItem(key);
      }
      set({ isExpanded: expanded });
    },
  }));

  registerForWorkspaceRehydration(() => {
    const identityId = store.getState().identityId;
    const sessionKey = identityWorkspaceKey(SESSION_STORAGE_KEY, identityId);
    const agentKey = identityWorkspaceKey(AGENT_STORAGE_KEY, identityId);
    const draftsKey = identityWorkspaceKey(DRAFTS_KEY, identityId);
    const expandedKey = identityWorkspaceKey(CHAT_EXPANDED_KEY, identityId);
    const nextSession = sessionKey ? storage.getItem(sessionKey) : null;
    const nextAgent = agentKey ? storage.getItem(agentKey) : null;
    const nextDrafts = draftsKey ? readDrafts(storage, draftsKey) : {};
    logger.info("workspace rehydration", {
      prevSession: store.getState().activeSessionId,
      nextSession,
      prevAgent: store.getState().selectedAgentId,
      nextAgent,
      draftCount: Object.keys(nextDrafts).length,
    });
    store.setState({
      activeSessionId: nextSession,
      selectedAgentId: nextAgent,
      inputDrafts: nextDrafts,
      isExpanded: expandedKey ? storage.getItem(expandedKey) === "true" : false,
    });
  });

  return store;
}
