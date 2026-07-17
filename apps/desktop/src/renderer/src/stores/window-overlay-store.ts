import { create } from "zustand";

/**
 * Window-level transition overlay: pre-workspace flows that are NOT pages
 * inside a tab. Triggered by navigation-adapter interception, zero-workspace
 * auto-redirect, or deep link; rendered above the tab system as a full-window
 * takeover.
 *
 * These flows used to be routes (`/workspaces/new`, `/invite/:id`) but on
 * desktop the URL is invisible to users — routes are an implementation detail
 * of the tab system. Representing transitions as routes meant tabs tried to
 * persist them, TabBar rendered on top, and invite deep-linking had no clean
 * dispatch target. Modeling them as application state removes all three.
 */
export type WindowOverlay =
  | { type: "new-workspace" }
  | { type: "invite"; invitationId: string }
  | { type: "invitations" }
  | { type: "onboarding" };

interface WindowOverlayStore {
  overlay: WindowOverlay | null;
  /**
   * Invite deep link (agora://invite/<id>) that arrived while no user was
   * logged in. InvitePage's queries need a session, so the overlay can't
   * open yet — the id parks here and App opens the invite overlay right
   * after login. Cleared on logout so user B never inherits user A's link.
   */
  pendingInvitationId: string | null;
  open: (overlay: WindowOverlay) => void;
  close: () => void;
  setPendingInvitation: (invitationId: string) => void;
  clearPendingInvitation: () => void;
}

export const useWindowOverlayStore = create<WindowOverlayStore>((set) => ({
  overlay: null,
  pendingInvitationId: null,
  open: (overlay) => set({ overlay }),
  close: () => set({ overlay: null }),
  setPendingInvitation: (invitationId) =>
    set({ pendingInvitationId: invitationId }),
  clearPendingInvitation: () => set({ pendingInvitationId: null }),
}));
