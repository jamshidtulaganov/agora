import { useMutation } from "@tanstack/react-query";
import { api } from "../api";
import { useAuthStore } from "../auth";
import type { User } from "../types";

/**
 * Pure toggle over a hidden-key list. `hidden: true` hides the key, `false`
 * restores it; re-applying the same state is a no-op that returns the input
 * array unchanged, so callers can skip a redundant request.
 */
export function toggleHiddenNavKey(
  current: readonly string[],
  key: string,
  hidden: boolean,
): string[] {
  const isHidden = current.includes(key);
  if (isHidden === hidden) return current as string[];
  return hidden ? [...current, key] : current.filter((k) => k !== key);
}

/**
 * Persists the hidden-nav list on the user record (PATCH /api/me).
 *
 * Optimistic: the sidebar updates the moment the user clicks, and rolls back
 * to the previous list if the request fails. The preference lives on the user
 * row rather than in local storage so it follows the person across
 * workspaces, browsers, and the desktop app.
 */
export function useSetHiddenNav() {
  return useMutation({
    mutationFn: (hidden: string[]) => api.updateMe({ hidden_nav: hidden }),
    onMutate: (hidden) => {
      const prev = useAuthStore.getState().user;
      if (prev) {
        // Any write makes the sidebar the person's own, so a workspace's team
        // sidebar stops applying the moment they change something — the
        // server sets the same flag on this PATCH.
        useAuthStore.getState().setUser({ ...prev, hidden_nav: hidden, hidden_nav_customized: true });
      }
      return { prev };
    },
    onError: (_err, _hidden, ctx) => {
      if (ctx?.prev) useAuthStore.getState().setUser(ctx.prev);
    },
    onSuccess: (user: User) => {
      // `api.updateMe` parses through `parseWithFallback`, which yields
      // EMPTY_USER (id: "") when the response shape drifts. Adopting that
      // blank record would read as "logged out" everywhere, so keep the
      // optimistic value instead — the next /api/me refresh reconciles.
      if (user.id) useAuthStore.getState().setUser(user);
    },
  });
}
