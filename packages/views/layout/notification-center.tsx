"use client";

import { useMemo } from "react";
import type { InboxItem } from "@agora/core/types";
import { deduplicateInboxItems } from "@agora/core/inbox/queries";
import { useMarkAllInboxRead } from "@agora/core/inbox/mutations";
import { useWorkspacePaths } from "@agora/core/paths";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { Bell, CheckCheck, Inbox } from "lucide-react";
import { toast } from "sonner";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import { getInboxDisplayTitle } from "../inbox/components/inbox-display";

const MAX_VISIBLE_ITEMS = 6;

interface NotificationCenterProps {
  items: InboxItem[];
}

/**
 * Compact in-app notification center shared by web and desktop.
 *
 * Realtime delivery, browser/Electron banners and OS badges remain owned by
 * core/platform. This component is the always-visible entry point for the same
 * inbox data: unread count, recent items, mark-all-read, and deep links.
 */
export function NotificationCenter({ items }: NotificationCenterProps) {
  const { t } = useT("inbox");
  const { push } = useNavigation();
  const paths = useWorkspacePaths();
  const markAllRead = useMarkAllInboxRead();

  const notifications = useMemo(
    () => deduplicateInboxItems(items),
    [items],
  );
  const visibleItems = notifications.slice(0, MAX_VISIBLE_ITEMS);
  const unreadCount = useMemo(
    () => notifications.filter((item) => !item.read).length,
    [notifications],
  );

  const openInbox = (item?: InboxItem) => {
    const base = paths.inbox();
    if (!item) {
      push(base);
      return;
    }
    const key = item.issue_id ?? item.id;
    push(`${base}?issue=${encodeURIComponent(key)}`);
  };

  const handleMarkAllRead = () => {
    markAllRead.mutate(undefined, {
      onError: (error) =>
        toast.error(
          error instanceof Error && error.message
            ? error.message
            : t(($) => $.errors.mark_all_read_failed),
        ),
    });
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            type="button"
            aria-label={t(($) => $.page.title)}
            title={t(($) => $.page.title)}
            className="relative flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <Bell className="size-4" />
            {unreadCount > 0 && (
              <span className="absolute -right-1 -top-1 flex min-w-4 items-center justify-center rounded-full bg-brand px-1 text-[10px] font-semibold leading-4 text-white ring-2 ring-sidebar">
                {unreadCount > 99 ? "99+" : unreadCount}
              </span>
            )}
          </button>
        }
      />
      <DropdownMenuContent align="end" side="bottom" className="w-80 p-1.5">
        <div className="flex items-center justify-between gap-3 px-2 py-1.5">
          <p className="text-sm font-semibold">{t(($) => $.page.title)}</p>
          {unreadCount > 0 && (
            <button
              type="button"
              onClick={handleMarkAllRead}
              disabled={markAllRead.isPending}
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground disabled:opacity-50"
            >
              <CheckCheck className="size-3.5" />
              {t(($) => $.menu.mark_all_read)}
            </button>
          )}
        </div>
        <DropdownMenuSeparator />
        {visibleItems.length === 0 ? (
          <p className="px-2 py-8 text-center text-sm text-muted-foreground">
            {t(($) => $.list.empty)}
          </p>
        ) : (
          visibleItems.map((item) => (
            <DropdownMenuItem
              key={item.id}
              onClick={() => openInbox(item)}
              className="items-start gap-2 px-2 py-2"
            >
              <span
                aria-hidden="true"
                className={`mt-1.5 size-1.5 shrink-0 rounded-full ${
                  item.read ? "bg-transparent" : "bg-brand"
                }`}
              />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">
                  {getInboxDisplayTitle(item)}
                </span>
                {item.body && (
                  <span className="mt-0.5 line-clamp-2 block text-xs text-muted-foreground">
                    {item.body}
                  </span>
                )}
              </span>
            </DropdownMenuItem>
          ))
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onClick={() => openInbox()}
          className="justify-center py-1.5"
        >
          <Inbox className="size-4" />
          {t(($) => $.page.title)}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
