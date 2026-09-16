"use client";

import { toast } from "sonner";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Switch } from "@agora/ui/components/ui/switch";
import { Button } from "@agora/ui/components/ui/button";
import {
  useHiddenNav,
  useSetHiddenNav,
  toggleHiddenNavKey,
} from "@agora/core/sidebar";
import { NAV_GROUPS, isNavKeyHideable, type NavKey } from "../../layout/nav-items";
import { useT } from "../../i18n";

/**
 * Sidebar customization: one switch per nav item.
 *
 * Deliberately a flat show/hide list rather than a drag-to-reorder editor —
 * the need this answers is "I never use Runtimes, stop showing it to me",
 * and every item stays one toggle away from coming back.
 */
export function SidebarSection() {
  const { t } = useT("settings");
  // Nav item labels live in the layout namespace alongside the sidebar that
  // renders them, so this list can never drift from the real menu.
  const { t: tLayout } = useT("layout");
  const hiddenNav = useHiddenNav();
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
    const next = toggleHiddenNavKey(hiddenNav, key, !visible);
    if (next === hiddenNav) return;
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
            {t(($) => $.preferences.sidebar.hint)}
          </p>
        </div>
        {hiddenNav.length > 0 && (
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
        <CardContent className="space-y-5">
          {NAV_GROUPS.filter((group) => group.items.length > 0).map((group) => (
            <div key={group.id} className="space-y-1">
              <p className="text-xs font-medium text-muted-foreground">
                {t(($) => $.preferences.sidebar.groups[group.id])}
              </p>
              <div className="divide-y">
                {group.items.map((item) => {
                  const hideable = isNavKeyHideable(item.key);
                  const visible = !hiddenNav.includes(item.key);
                  return (
                    <div
                      key={item.key}
                      className="flex items-center justify-between gap-4 py-2.5"
                    >
                      <span className="flex min-w-0 items-center gap-2 text-sm">
                        <item.icon className="size-4 shrink-0 text-muted-foreground" />
                        <span className="truncate">
                          {tLayout(($) => $.nav[item.labelKey])}
                        </span>
                      </span>
                      {hideable ? (
                        <Switch
                          checked={visible}
                          aria-label={tLayout(($) => $.nav[item.labelKey])}
                          onCheckedChange={(checked) =>
                            handleToggle(item.key, checked)
                          }
                        />
                      ) : (
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {t(($) => $.preferences.sidebar.always_visible)}
                        </span>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </section>
  );
}
