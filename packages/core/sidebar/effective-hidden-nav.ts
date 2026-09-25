import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "../auth";
import type { MemberRole, Workspace } from "../types";
import { readTeamSidebar } from "../workspace/department-setup";
import { memberListOptions } from "../workspace/queries";

export interface EffectiveHiddenNavInput {
  /** The person's own hidden list (user.hidden_nav). */
  userHidden: string[];
  /** True once the person set their own sidebar (user.hidden_nav_customized). */
  customized: boolean;
  /** Owner or admin of the workspace being viewed. */
  isAdmin: boolean;
  /** The workspace's team sidebar, or null when it has none. */
  teamHidden: string[] | null;
}

/**
 * Whether the team sidebar decides what this person sees. It applies only to
 * members who never set their own sidebar; owners and admins always keep
 * their own (they are the ones who set the team sidebar up, and they need
 * every page to do so).
 */
export function usesTeamSidebar({ customized, isAdmin, teamHidden }: EffectiveHiddenNavInput): boolean {
  return !customized && !isAdmin && teamHidden !== null;
}

/**
 * The nav keys the sidebar should hide for this person:
 *   - they customized, or they are an owner/admin → their own list
 *   - else the workspace has a team sidebar → the team's list
 *   - else → their own list (the default Agora sidebar for most people)
 *
 * Returns one of the input arrays as-is (never a copy), so callers that
 * memoize on the result don't re-run on every render.
 */
export function effectiveHiddenNav(input: EffectiveHiddenNavInput): string[] {
  return usesTeamSidebar(input) && input.teamHidden !== null ? input.teamHidden : input.userHidden;
}

export interface EffectiveHiddenNav {
  hidden: string[];
  /** True while the team sidebar is what this person sees. */
  fromTeam: boolean;
}

/**
 * Stable empty array for users with no customization. Returning a fresh `[]`
 * from the selector on every call would give every subscriber a new reference
 * each render and re-run any memo that depends on it.
 */
const EMPTY_HIDDEN_NAV: string[] = [];

/**
 * The effective hidden-nav list for the signed-in person in `workspace`.
 *
 * Takes the workspace as a parameter (not from context) so it also works in
 * chrome that renders before a workspace resolves — with no workspace it is
 * simply the person's own list. The result object is memoized: its identity
 * only changes when the list or its source does.
 */
export function useEffectiveHiddenNav(
  workspace: Pick<Workspace, "id" | "settings"> | null | undefined,
): EffectiveHiddenNav {
  const userHidden = useAuthStore((s) => s.user?.hidden_nav ?? EMPTY_HIDDEN_NAV);
  const customized = useAuthStore((s) => s.user?.hidden_nav_customized === true);
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const wsId = workspace?.id ?? "";
  const { data: role = null } = useQuery({
    ...memberListOptions(wsId),
    enabled: wsId !== "" && userId !== null,
    select: (members): MemberRole | null =>
      members.find((m) => m.user_id === userId)?.role ?? null,
  });
  const settings = workspace?.settings;
  const teamHidden = useMemo(() => readTeamSidebar(settings), [settings]);

  const input: EffectiveHiddenNavInput = {
    userHidden,
    customized,
    isAdmin: role === "owner" || role === "admin",
    teamHidden,
  };
  const hidden = effectiveHiddenNav(input);
  const fromTeam = usesTeamSidebar(input);
  return useMemo(() => ({ hidden, fromTeam }), [hidden, fromTeam]);
}
