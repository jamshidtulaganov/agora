"use client";

import { NAV_GROUPS } from "../layout/nav-items";
import { useT } from "../i18n";

/**
 * A small copy of the sidebar as a member will see it, built from the same
 * nav groups, icons and labels as the real one. It follows the draft while
 * the admin flips switches, so the choice is visible before it is saved.
 */
export function TeamSidebarPreview({
  workspaceName,
  hidden,
}: {
  workspaceName: string;
  hidden: readonly string[];
}) {
  const { t } = useT("department-setup");
  const { t: tLayout } = useT("layout");
  const { t: tSettings } = useT("settings");
  const groups = NAV_GROUPS.map((group) => ({
    ...group,
    items: group.items.filter((item) => !hidden.includes(item.key)),
  })).filter((group) => group.items.length > 0);
  const initial = workspaceName.trim().charAt(0).toUpperCase() || "A";

  return (
    <figure className="space-y-2" data-testid="setup-sidebar-preview">
      <figcaption className="text-xs text-muted-foreground">{t(($) => $.summary.preview_label)}</figcaption>
      <div className="rounded-lg border border-sidebar-border bg-sidebar p-2 text-sidebar-foreground">
        <div className="flex items-center gap-2 px-1.5 pb-1.5">
          <span
            className="flex size-5 shrink-0 items-center justify-center rounded border bg-background text-[10px] font-medium"
            aria-hidden
          >
            {initial}
          </span>
          <span className="truncate text-xs font-medium">{workspaceName}</span>
        </div>
        {groups.map((group) => (
          <div key={group.id} className="pt-1.5">
            {group.id !== "personal" && (
              <p className="px-1.5 pb-0.5 text-[11px] text-muted-foreground">
                {tSettings(($) => $.preferences.sidebar.groups[group.id])}
              </p>
            )}
            <ul>
              {group.items.map((item) => (
                <li key={item.key} className="flex items-center gap-2 px-1.5 py-1 text-xs">
                  <item.icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="truncate">{tLayout(($) => $.nav[item.labelKey])}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </figure>
  );
}
