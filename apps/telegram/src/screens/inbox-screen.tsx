import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Archive, Info } from "lucide-react";
import { inboxListOptions } from "@agora/core/inbox/queries";
import {
  useMarkInboxRead,
  useArchiveInbox,
  useMarkAllInboxRead,
} from "@agora/core/inbox/mutations";
import { actorDirectoryOptions, agentListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import type { InboxItem } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { TabSkeleton } from "../components/skeleton";
import { QueryError } from "../components/query-error";
import { AgentAvatar } from "../components/agent-avatar";
import { Avatar } from "../components/avatar";
import { useToast } from "../components/toast";
import { haptic } from "../telegram/sdk";
import { useT, useFormatRelative } from "../i18n";
import { cn } from "../lib/cn";

// Inbox tab (design 5a §2.5 + day-grouping reference §6): day-grouped bordered
// cards with actor avatars, unread dots, result chips and per-row archive.

type DayBucket = "today" | "yesterday" | "earlier";

const BUCKET_ORDER: DayBucket[] = ["today", "yesterday", "earlier"];

function calendarKey(d: Date): number {
  return d.getFullYear() * 10000 + (d.getMonth() + 1) * 100 + d.getDate();
}

function bucketOf(iso: string, now: Date): DayBucket {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "earlier";
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const key = calendarKey(d);
  if (key >= calendarKey(now)) return "today";
  if (key === calendarKey(yesterday)) return "yesterday";
  return "earlier";
}

interface ResolvedActor {
  kind: "agent" | "member" | "system";
  name: string | null;
  avatarUrl: string | null;
}

export function InboxScreen() {
  const wsId = useWorkspaceId();
  const { data: items = [], isLoading, isError, refetch } = useQuery({
    ...inboxListOptions(wsId),
    refetchInterval: 20_000,
  });
  const { data: directory = [] } = useQuery(actorDirectoryOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const markRead = useMarkInboxRead();
  const archive = useArchiveInbox();
  const markAll = useMarkAllInboxRead();
  const { navigate } = useRouter();
  const { toast } = useToast();
  const t = useT();

  const memberById = useMemo(
    () => new Map(directory.map((e) => [e.user_id, e])),
    [directory],
  );
  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);

  const visible = useMemo(() => {
    return items
      .filter((i) => i?.archived !== true)
      .sort(
        (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
      );
  }, [items]);

  const groups = useMemo(() => {
    const now = new Date();
    const byBucket: Record<DayBucket, InboxItem[]> = {
      today: [],
      yesterday: [],
      earlier: [],
    };
    for (const item of visible) byBucket[bucketOf(item.created_at, now)].push(item);
    return BUCKET_ORDER.filter((b) => byBucket[b].length > 0).map((b) => ({
      bucket: b,
      items: byBucket[b],
    }));
  }, [visible]);

  if (isLoading) return <TabSkeleton />;
  if (isError) return <QueryError onRetry={() => void refetch()} />;
  if (visible.length === 0) {
    return <CenterMessage title={t("inbox.empty")} subtitle={t("inbox.emptySub")} />;
  }

  const unreadCount = visible.filter((i) => i.read !== true).length;

  const resolveActor = (item: InboxItem): ResolvedActor => {
    switch (item.actor_type) {
      case "agent": {
        const agent = item.actor_id ? agentById.get(item.actor_id) : undefined;
        return { kind: "agent", name: agent?.name ?? t("common.agent"), avatarUrl: null };
      }
      case "member": {
        const entry = item.actor_id ? memberById.get(item.actor_id) : undefined;
        // Unresolved members degrade to a neutral icon disc (no bold prefix).
        return entry
          ? { kind: "member", name: entry.name, avatarUrl: entry.avatar_url ?? null }
          : { kind: "system", name: null, avatarUrl: null };
      }
      default:
        return { kind: "system", name: null, avatarUrl: null };
    }
  };

  const onMarkAll = () => {
    haptic("light");
    markAll.mutate();
    toast(t("inbox.readToast"), "ok");
  };

  return (
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col overflow-y-auto">
      <div className="flex flex-col gap-2.5 px-4 pb-6 pt-2.5">
        {/* Title row */}
        <div className="flex items-center gap-2.5 px-1 pb-1">
          <h1 className="min-w-0 flex-1 truncate text-[26px] font-bold tracking-[-0.4px]">
            {t("tab.inbox")}
          </h1>
          {unreadCount > 0 ? (
            <button
              type="button"
              onClick={onMarkAll}
              className="-mx-2 -my-2.5 px-2 py-2.5 text-[13px] font-semibold text-brand"
            >
              {t("inbox.markAll")}
            </button>
          ) : (
            <span className="text-[13px] text-muted-foreground/70">
              {t("inbox.allCaughtUp")}
            </span>
          )}
        </div>

        {/* Day groups */}
        {groups.map(({ bucket, items: groupItems }) => {
          const fullyRead = groupItems.every((i) => i.read === true);
          return (
            <div
              key={bucket}
              className={cn(
                "flex flex-col gap-2",
                bucket !== "today" && fullyRead && "opacity-60",
              )}
            >
              <div className="px-1 pt-1 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
                {t(`inbox.${bucket}`)}
              </div>
              <ul className="overflow-hidden rounded-xl border border-border bg-card shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none">
                {groupItems.map((item) => (
                  <InboxRow
                    key={item.id}
                    item={item}
                    actor={resolveActor(item)}
                    onOpen={() => {
                      haptic("light");
                      if (item.read !== true) markRead.mutate(item.id);
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
        })}
      </div>
    </div>
  );
}

// Result chip for agent/task outcome notifications: mono 11px tag, tinted
// destructive for failures, success otherwise. Label = the type's last
// segment ("completed" / "failed"), kept simple per the design brief.
function resultChip(type: InboxItem["type"]): { label: string; className: string } | null {
  switch (type) {
    case "task_failed":
      return { label: "failed", className: "bg-destructive/10 text-destructive" };
    case "task_completed":
    case "agent_completed":
      return { label: "completed", className: "bg-success/10 text-success" };
    default:
      return null;
  }
}

function InboxRow({
  item,
  actor,
  onOpen,
  onArchive,
}: {
  item: InboxItem;
  actor: ResolvedActor;
  onOpen: () => void;
  onArchive: () => void;
}) {
  const t = useT();
  const fmt = useFormatRelative();
  const chip = resultChip(item.type);
  const unread = item.read !== true;

  return (
    <li className="flex items-stretch border-b border-border/50 last:border-b-0">
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 items-start gap-3 px-4 py-3.5 text-left transition-colors active:bg-accent"
      >
        {/* Avatar + unread dot */}
        <span className="relative shrink-0">
          {actor.kind === "agent" ? (
            <AgentAvatar size={36} />
          ) : actor.kind === "member" && actor.name ? (
            <Avatar name={actor.name} avatarUrl={actor.avatarUrl} size={36} />
          ) : (
            <span className="flex size-9 items-center justify-center rounded-full bg-muted text-muted-foreground">
              <Info className="size-[19px]" />
            </span>
          )}
          {unread && (
            <span className="absolute right-[-2px] top-[-2px] size-[9px] rounded-full border-2 border-card bg-brand" />
          )}
        </span>

        <span className="min-w-0 flex-1">
          <span className="line-clamp-2 text-sm leading-[1.45] text-foreground">
            {actor.name && <span className="font-semibold">{actor.name}</span>}
            {actor.name ? " " : null}
            {item.title || item.body || ""}
          </span>
          {item.body != null && item.body !== "" && item.title !== "" && (
            <span className="mt-0.5 block truncate text-[13px] text-muted-foreground">
              {item.body}
            </span>
          )}
          <span className="mt-1 flex items-center gap-[7px]">
            {chip && (
              <span
                className={cn(
                  "rounded-[5px] px-2 py-0.5 font-mono text-[11px] font-bold",
                  chip.className,
                )}
              >
                {chip.label}
              </span>
            )}
            <span className="text-xs text-muted-foreground/70">{fmt(item.created_at)}</span>
          </span>
        </span>
      </button>
      <button
        type="button"
        onClick={onArchive}
        aria-label={t("inbox.archive")}
        className="flex shrink-0 items-center px-3.5 text-muted-foreground/50 transition-colors active:text-foreground"
      >
        <Archive className="size-[18px]" />
      </button>
    </li>
  );
}
