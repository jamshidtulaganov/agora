import { useQuery } from "@tanstack/react-query";
import { Check, Archive } from "lucide-react";
import { inboxListOptions } from "@agora/core/inbox/queries";
import { useMarkInboxRead, useArchiveInbox } from "@agora/core/inbox/mutations";
import { useMarkAllInboxRead } from "@agora/core/inbox/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import type { InboxItem } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { haptic } from "../telegram/sdk";
import { useT, useFormatRelative } from "../i18n";
import { cn } from "../lib/cn";

export function InboxScreen() {
  const wsId = useWorkspaceId();
  const { data: items = [], isLoading } = useQuery(inboxListOptions(wsId));
  const markRead = useMarkInboxRead();
  const archive = useArchiveInbox();
  const markAll = useMarkAllInboxRead();
  const { navigate } = useRouter();
  const t = useT();

  if (isLoading) return <CenterMessage spinner title={t("inbox.loading")} />;
  if (items.length === 0) {
    return <CenterMessage title={t("inbox.empty")} subtitle={t("inbox.emptySub")} />;
  }

  const hasUnread = items.some((i) => !i.read);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {hasUnread && (
        <div className="flex justify-end border-b border-border px-3 py-1.5">
          <button
            type="button"
            onClick={() => markAll.mutate()}
            className="flex items-center gap-1 text-xs font-medium text-muted-foreground"
          >
            <Check className="size-3.5" />
            {t("inbox.markAll")}
          </button>
        </div>
      )}
      <ul className="flex-1 divide-y divide-border overflow-y-auto">
        {items.map((item) => (
          <InboxRow
            key={item.id}
            item={item}
            onOpen={() => {
              haptic("light");
              if (!item.read) markRead.mutate(item.id);
              if (item.issue_id) navigate({ name: "issue", id: item.issue_id });
            }}
            onArchive={() => {
              haptic("light");
              archive.mutate(item.id);
            }}
          />
        ))}
      </ul>
    </div>
  );
}

function InboxRow({
  item,
  onOpen,
  onArchive,
}: {
  item: InboxItem;
  onOpen: () => void;
  onArchive: () => void;
}) {
  const t = useT();
  const fmt = useFormatRelative();
  return (
    <li className="flex items-stretch">
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 items-start gap-3 px-4 py-4 text-left transition-colors active:bg-accent"
      >
        <span
          className={cn(
            "mt-2 size-2 shrink-0 rounded-full",
            item.read ? "bg-transparent" : "bg-brand",
          )}
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span
              className={cn(
                "truncate text-[15px] leading-snug",
                item.read ? "font-normal text-foreground" : "font-semibold text-foreground",
              )}
            >
              {item.title}
            </span>
            <span className="shrink-0 text-xs text-muted-foreground">
              {fmt(item.created_at)}
            </span>
          </div>
          {item.body && (
            <div className="mt-1 truncate text-[13px] text-muted-foreground">
              {item.body}
            </div>
          )}
        </div>
      </button>
      <button
        type="button"
        onClick={onArchive}
        aria-label={t("inbox.archive")}
        className="flex shrink-0 items-center px-3.5 text-muted-foreground/60 transition-colors active:text-foreground"
      >
        <Archive className="size-[18px]" />
      </button>
    </li>
  );
}
