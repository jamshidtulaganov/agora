"use client";

import { Switch } from "@agora/ui/components/ui/switch";
import { NAV_GROUPS, isNavKeyHideable, type NavKey } from "../../layout/nav-items";
import { useT } from "../../i18n";

/**
 * One switch per sidebar item, grouped like the sidebar itself. Shared by
 * Settings → Preferences (the person's own sidebar) and the department setup
 * (the team's sidebar), so both always list exactly the items that exist.
 * Items that can't be hidden (Settings) show "Always visible" instead.
 */
export function NavVisibilityList({
  hidden,
  onToggle,
  disabled = false,
}: {
  hidden: readonly string[];
  onToggle: (key: NavKey, visible: boolean) => void;
  disabled?: boolean;
}) {
  const { t } = useT("settings");
  // Nav item labels live in the layout namespace alongside the sidebar that
  // renders them, so this list can never drift from the real menu.
  const { t: tLayout } = useT("layout");

  return (
    <div className="space-y-5">
      {NAV_GROUPS.filter((group) => group.items.length > 0).map((group) => (
        <div key={group.id} className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">
            {t(($) => $.preferences.sidebar.groups[group.id])}
          </p>
          <div className="divide-y">
            {group.items.map((item) => {
              const hideable = isNavKeyHideable(item.key);
              const visible = !hidden.includes(item.key);
              const label = tLayout(($) => $.nav[item.labelKey]);
              return (
                <div key={item.key} className="flex items-center justify-between gap-4 py-2.5">
                  <span className="flex min-w-0 items-center gap-2 text-sm">
                    <item.icon className="size-4 shrink-0 text-muted-foreground" />
                    <span className="truncate">{label}</span>
                  </span>
                  {hideable ? (
                    <Switch
                      checked={visible}
                      disabled={disabled}
                      aria-label={label}
                      onCheckedChange={(checked) => onToggle(item.key, checked)}
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
    </div>
  );
}
