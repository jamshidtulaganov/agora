import type { Workspace } from "../types";
import { useAuthStore } from "../auth";
import { paths } from "./paths";

/**
 * Priority (onboarded-first):
 *   !hasOnboarded               → /onboarding
 *   hasOnboarded + workspace[0] → /<first.slug>/issues
 *   hasOnboarded + no workspace → /workspaces/new
 *
 * V3 invariant: `onboarded_at != null` is the single source of truth for
 * "may access /<slug>/*". The web workspace layout and the desktop App.tsx
 * overlay decision both gate on this — sending an un-onboarded user
 * straight to /issues would just be redirected back to /onboarding by
 * the layout gate, costing a navigation round-trip. Check onboarded
 * first.
 *
 * In v3 "has workspace but !onboarded" is physically rare (a user can
 * only land in that state by closing the app between Step 2 and Step 3
 * — both questionnaire and runtime picker steps run after workspace
 * creation but before CompleteOnboarding). OnboardingFlow's Step 2
 * already recognizes existing workspaces and offers "Continue with
 * {name}", so the recovery is seamless.
 *
 * Callers that need invitation-aware routing (callback / login) handle
 * the "un-onboarded with pending invites" branch themselves before calling
 * this resolver — this resolver only deals with the post-invite-check
 * destination.
 */
export function resolvePostAuthDestination(
  workspaces: Workspace[],
  hasOnboarded: boolean,
): string {
  if (!hasOnboarded) {
    return paths.onboarding();
  }
  const first = workspaces[0];
  if (first) {
    // Land on "my issues" (assigned-to-me by default), not the all-issues list —
    // a user should see their own work first on login, not the whole workspace.
    return paths.workspace(first.slug).myIssues();
  }
  return paths.newWorkspace();
}

/** Choose the first auth screen for a bearer invitation link. */
export function resolveInvitationAuthDestination(
  invitationId: string,
  accountExists: boolean,
): string {
  const invitePath = paths.invite(invitationId);
  const authPath = accountExists ? paths.login() : paths.signup();
  return `${authPath}?next=${encodeURIComponent(invitePath)}`;
}

/**
 * After an invitation is accepted, prefer the workspace named by the invite
 * over list order. The backend marks the user onboarded in the same transaction
 * as membership creation, so both web and desktop can enter it immediately.
 */
export function resolveAcceptedInvitationDestination(
  workspaces: Workspace[],
  invitedWorkspaceId: string,
): string {
  const joined = workspaces.find((workspace) => workspace.id === invitedWorkspaceId);
  return joined
    ? paths.workspace(joined.slug).issues()
    : resolvePostAuthDestination(workspaces, true);
}

/**
 * Single source of truth: backed by `users.onboarded_at`, which
 * arrives with the user object on every auth response.
 */
export function useHasOnboarded(): boolean {
  return useAuthStore((s) => s.user?.onboarded_at != null);
}
