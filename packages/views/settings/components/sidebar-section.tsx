"use client";

import { toast } from "sonner";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Button } from "@agora/ui/components/ui/button";
import { useCurrentWorkspace } from "@agora/core/paths";
import {
  useEffectiveHiddenNav,
  useSetHiddenNav,
  toggleHiddenNavKey,
} from "@agora/core/sidebar";
import type { NavKey } from "../../layout/nav-items";
import { useT } from "../../i18n";
import { NavVisibilityList } from "./nav-visibility-list";

/**
 * Sidebar customization: one switch per nav item.
 *
 * Deliberately a flat show/hide list rather than a drag-to-reorder editor —
 * the need this answers is "I never use Runtimes, stop showing it to me",
 * and every item stays one toggle away from coming back.
 *
 * The switches show what the person actually sees: for a member who never
 * customized, that's the workspace's team sidebar. Any change is written to
 * their own list starting from it, so they keep the team's choices plus
 * their edit, and from then on the sidebar is theirs.
 */
export function SidebarSection() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const { hidden, fromTeam } = useEffectiveHiddenNav(workspace);
  const setHiddenNav = useSetHiddenNav();

  const persist = (next: string[]) => {
    setHiddenNav.mutate(next, {
      onError: (err) =>
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.preferences.sidebar.sync_failed),
        ),
    });
  };

  const handleToggle = (key: NavKey, visible: boolean) => {
    const next = toggleHiddenNavKey(hidden, key, !visible);
    if (next === hidden) return;
    persist(next);
  };

  return (
    <section className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold">
            {t(($) => $.preferences.sidebar.title)}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {fromTeam
              ? t(($) => $.preferences.sidebar.team_hint)
              : t(($) => $.preferences.sidebar.hint)}
          </p>
        </div>
        {hidden.length > 0 && (
          <Button
            variant="ghost"
            size="sm"
            className="shrink-0"
            onClick={() => persist([])}
          >
            {t(($) => $.preferences.sidebar.show_all)}
          </Button>
        )}
      </div>

      <Card>
        <CardContent>
          <NavVisibilityList hidden={hidden} onToggle={handleToggle} />
        </CardContent>
      </Card>
    </section>
  );
}
