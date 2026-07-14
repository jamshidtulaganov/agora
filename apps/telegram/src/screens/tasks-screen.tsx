import { useCallback, useMemo, useRef, useState, type TouchEvent } from "react";
import { Search, X, SlidersHorizontal, Check, Folder, Plus, User, UserPlus } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { issueListOptions } from "@agora/core/issues/queries";
import { useUpdateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import { projectListOptions } from "@agora/core/projects/queries";
import { BOARD_STATUSES, PRIORITY_ORDER } from "@agora/core/issues/config";
import { deriveStagePipeline } from "@agora/core/issues";
import { useWorkspaceId } from "@agora/core/hooks";
import { useAuthStore } from "@agora/core/auth";
import type { Issue, IssueStatus, IssuePriority, Workspace } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { BottomSheet } from "../components/bottom-sheet";
import { TabSkeleton } from "../components/skeleton";
import { QueryError } from "../components/query-error";
import { StageRail, STAGE_TEXT } from "../components/stage-rail";
import { AgentAvatar } from "../components/agent-avatar";
import { Avatar } from "../components/avatar";
import { PriorityBars } from "../components/issue-badges";
import { haptic } from "../telegram/sdk";
import { useT, useFormatRelative } from "../i18n";
import { cn } from "../lib/cn";

// Redesigned Tasks tab (spec 5a §2.1): 26px title + workspace chip + "+" FAB,
// pill filter row, status-grouped card list. Each card carries the derived
// SDLC stage rail and keeps the swipe interactions from the old IssueRow —
// swipe right → Done, swipe left → Assign to me.

type Filter = "active" | "mine" | "unassigned" | "blocked" | "overdue" | "all";

// "mine" first + default: every Telegram user is auto-joined to the shared SD
// workspaces, so the board holds the whole team's issues. The Mini App is a
// personal companion — it must open on the user's OWN work, not the common
// board. The broader views stay available behind the other filters.
const FILTER_ORDER: Filter[] = [
  "mine",
  "active",
  "unassigned",
  "blocked",
  "overdue",
  "all",
];

// Statuses considered "active" work (open, not parked/closed).
const ACTIVE_STATUSES: IssueStatus[] = ["todo", "in_progress", "in_review", "blocked"];
const CLOSED_STATUSES: IssueStatus[] = ["done", "cancelled"];

// Group-header swatch per status (design: stage-group color language).
const GROUP_SWATCH: Record<IssueStatus, string> = {
  backlog: "bg-muted-foreground/40",
  todo: "bg-muted-foreground/40",
  in_progress: "bg-warning",
  in_review: "bg-info",
  done: "bg-success",
  blocked: "bg-destructive",
  cancelled: "bg-muted-foreground/40",
};

// Tinted priority pills; "none" renders no pill at all.
const PRIORITY_PILL: Record<Exclude<IssuePriority, "none">, string> = {
  urgent: "bg-destructive/10 text-destructive",
  high: "bg-warning/15 text-warning",
  medium: "bg-info/10 text-info",
  low: "bg-muted text-muted-foreground",
};

type CardAssignee = { name: string; isAgent: boolean; avatarUrl?: string | null } | null;

function matchesFilter(issue: Issue, filter: Filter, myId: string, todayISO: string): boolean {
  switch (filter) {
    case "all":
      return true;
    case "active":
      return ACTIVE_STATUSES.includes(issue.status);
    case "mine":
      // The user's own work: assigned to them, OR created by them (Telegram
      // users mostly delegate to agents, so created-by-me is the bulk of it).
      return (
        (issue.assignee_type === "member" && issue.assignee_id === myId) ||
        (issue.creator_type === "member" && issue.creator_id === myId)
      );
    case "unassigned":
      return !issue.assignee_id && !CLOSED_STATUSES.includes(issue.status);
    case "blocked":
      return issue.status === "blocked";
    case "overdue":
      return (
        !!issue.due_date &&
        issue.due_date < todayISO &&
        !CLOSED_STATUSES.includes(issue.status)
      );
    default:
      return true;
  }
}

export function TasksScreen({
  workspaces,
  active,
  onSelect,
}: {
  workspaces: Workspace[];
  active: Workspace;
  onSelect: (slug: string) => void;
}) {
  const wsId = useWorkspaceId();
  // No WS in the Mini App — poll the board every 30s to keep statuses live.
  const { data: issues = [], isLoading, isError, refetch } = useQuery({
    ...issueListOptions(wsId),
    refetchInterval: 30_000,
  });
  const myId = useAuthStore((s) => s.user?.id ?? "");
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const update = useUpdateIssue();
  const { navigate } = useRouter();
  const t = useT();
  const [filter, setFilter] = useState<Filter>("mine");
  const [projectId, setProjectId] = useState<string | null>(null);
  const [priority, setPriority] = useState<IssuePriority | null>(null);
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [filterSheetOpen, setFilterSheetOpen] = useState(false);
  const [wsSheetOpen, setWsSheetOpen] = useState(false);

  const todayISO = new Date().toISOString().slice(0, 10);
  const advancedCount = (projectId ? 1 : 0) + (priority ? 1 : 0);

  // Resolve the polymorphic assignee into avatar props for each card.
  const assigneeOf = useCallback(
    (issue: Issue): CardAssignee => {
      if (!issue.assignee_id || !issue.assignee_type) return null;
      if (issue.assignee_type === "member") {
        const m = members.find((x) => x.user_id === issue.assignee_id);
        return m ? { name: m.name, isAgent: false, avatarUrl: m.avatar_url } : null;
      }
      if (issue.assignee_type === "agent") {
        const a = agents.find((x) => x.id === issue.assignee_id);
        return a ? { name: a.name, isAgent: true, avatarUrl: a.avatar_url } : null;
      }
      return null;
    },
    [members, agents],
  );
  const onDone = useCallback(
    (issue: Issue) => update.mutate({ id: issue.id, status: "done" }),
    [update],
  );
  const onAssignMe = useCallback(
    (issue: Issue) => {
      if (myId) update.mutate({ id: issue.id, assignee_type: "member", assignee_id: myId });
    },
    [update, myId],
  );
  const showAssignMe = useCallback(
    (issue: Issue) =>
      !!myId && !(issue.assignee_type === "member" && issue.assignee_id === myId),
    [myId],
  );

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const visible = issues.filter((i) => {
      if (!matchesFilter(i, filter, myId, todayISO)) return false;
      if (projectId && i.project_id !== projectId) return false;
      if (priority && i.priority !== priority) return false;
      if (!q) return true;
      return (
        i.title.toLowerCase().includes(q) ||
        i.identifier.toLowerCase().includes(q)
      );
    });
    return BOARD_STATUSES.map((status) => ({
      status,
      issues: visible.filter((i) => i.status === status),
    })).filter((g) => g.issues.length > 0);
  }, [issues, filter, projectId, priority, query, myId, todayISO]);

  const total = groups.reduce((n, g) => n + g.issues.length, 0);

  if (isLoading) return <TabSkeleton />;
  if (isError) return <QueryError onRetry={() => void refetch()} />;

  return (
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col gap-2.5">
      {/* Title row: Tasks · workspace chip · "+" FAB */}
      <div className="flex shrink-0 items-center gap-2.5 px-5 pt-2.5">
        <h1 className="min-w-0 flex-1 truncate text-[26px] font-bold tracking-[-0.4px] text-foreground">
          {t("tasks.title")}
        </h1>
        <button
          type="button"
          onClick={() => setWsSheetOpen(true)}
          className="flex min-w-0 items-center gap-[7px] rounded-full border border-border bg-card px-3.5 py-2 text-[13px] font-semibold text-foreground active:border-brand"
        >
          <span className="size-[7px] shrink-0 rounded-[2px] bg-brand" />
          <span className="max-w-[110px] truncate">{active.name}</span>
        </button>
        <button
          type="button"
          onClick={() => navigate({ name: "create" })}
          aria-label={t("shell.new")}
          className="flex size-[38px] shrink-0 items-center justify-center rounded-full bg-brand text-brand-foreground active:brightness-90"
        >
          <Plus className="size-5" />
        </button>
      </div>

      {/* Filter chips / inline search */}
      {searching ? (
        <div className="shrink-0 px-4">
          <div className="flex items-center gap-2 rounded-full border border-border bg-card px-4 py-2">
            <Search className="size-4 shrink-0 text-muted-foreground" />
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("issues.search")}
              className="min-w-0 flex-1 bg-transparent text-[15px] text-foreground outline-none placeholder:text-muted-foreground"
            />
            <button
              type="button"
              onClick={() => {
                setQuery("");
                setSearching(false);
              }}
              className="shrink-0 text-muted-foreground"
              aria-label={t("shell.close")}
            >
              <X className="size-4" />
            </button>
          </div>
        </div>
      ) : (
        <div className="flex shrink-0 items-center gap-2 px-4">
          <div className="no-scrollbar flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto py-0.5">
            {FILTER_ORDER.map((key) => (
              <button
                key={key}
                type="button"
                onClick={() => setFilter(key)}
                className={cn(
                  "shrink-0 rounded-full border px-3.5 py-[7px] text-[13px] font-medium transition-colors",
                  filter === key
                    ? "border-transparent bg-brand text-brand-foreground"
                    : "border-border bg-card text-muted-foreground",
                )}
              >
                {t(`filter.${key}`)}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => setFilterSheetOpen(true)}
            className="relative flex size-9 shrink-0 items-center justify-center rounded-full bg-muted text-foreground/70 active:bg-muted/80"
            aria-label={t("filter.title")}
          >
            <SlidersHorizontal className="size-[18px]" />
            {advancedCount > 0 && (
              <span className="absolute -right-0.5 -top-0.5 flex size-4 items-center justify-center rounded-full bg-brand text-[10px] font-semibold text-brand-foreground">
                {advancedCount}
              </span>
            )}
          </button>
          <button
            type="button"
            onClick={() => setSearching(true)}
            className="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted text-foreground/70 active:bg-muted/80"
            aria-label={t("issues.search")}
          >
            <Search className="size-[18px]" />
          </button>
        </div>
      )}

      {/* Grouped card list */}
      {total === 0 ? (
        <CenterMessage
          title={query.trim() ? t("issues.noMatches") : t("issues.allClear")}
          subtitle={query.trim() ? t("issues.tryDifferent") : t(`empty.${filter}`)}
        />
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="flex flex-col gap-2.5 px-4 pb-6">
            {groups.map((g) => (
              <StatusGroup
                key={g.status}
                status={g.status}
                issues={g.issues}
                onOpen={(id) => navigate({ name: "issue", id })}
                assigneeOf={assigneeOf}
                onDone={onDone}
                onAssignMe={onAssignMe}
                showAssignMe={showAssignMe}
              />
            ))}
          </div>
        </div>
      )}

      {/* Workspace switcher */}
      <BottomSheet
        open={wsSheetOpen}
        onClose={() => setWsSheetOpen(false)}
        title={t("tasks.workspace")}
      >
        <ul className="pb-1">
          {workspaces.map((w) => (
            <li key={w.id}>
              <button
                type="button"
                onClick={() => {
                  setWsSheetOpen(false);
                  if (w.slug !== active.slug) onSelect(w.slug);
                }}
                className="flex w-full items-center gap-2.5 px-4 py-3 text-left text-[15px] text-foreground transition-colors active:bg-accent"
              >
                <span className="size-[7px] shrink-0 rounded-[2px] bg-brand" />
                <span className="min-w-0 flex-1 truncate">{w.name}</span>
                {w.slug === active.slug && <Check className="size-4 shrink-0 text-brand" />}
              </button>
            </li>
          ))}
        </ul>
      </BottomSheet>

      {/* Project / priority filters */}
      <BottomSheet
        open={filterSheetOpen}
        onClose={() => setFilterSheetOpen(false)}
        title={t("filter.title")}
      >
        <div className="px-4 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
          {t("filter.project")}
        </div>
        <ul className="pb-1">
          <FilterOption selected={!projectId} onClick={() => setProjectId(null)}>
            <Folder className="size-[18px] text-muted-foreground" />
            {t("filter.allProjects")}
          </FilterOption>
          {projects.map((p) => (
            <FilterOption
              key={p.id}
              selected={projectId === p.id}
              onClick={() => setProjectId(p.id)}
            >
              <span className="text-base leading-none">{p.icon || "📁"}</span>
              <span className="truncate">{p.title}</span>
            </FilterOption>
          ))}
        </ul>

        <div className="px-4 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
          {t("filter.priority")}
        </div>
        <ul className="pb-1">
          <FilterOption selected={!priority} onClick={() => setPriority(null)}>
            <PriorityBars priority="none" />
            {t("filter.allPriorities")}
          </FilterOption>
          {PRIORITY_ORDER.filter((p: IssuePriority) => p !== "none").map((p: IssuePriority) => (
            <FilterOption
              key={p}
              selected={priority === p}
              onClick={() => setPriority(p)}
            >
              <PriorityBars priority={p} />
              {t(`priority.${p}`)}
            </FilterOption>
          ))}
        </ul>

        {advancedCount > 0 && (
          <div className="px-4 py-3">
            <button
              type="button"
              onClick={() => {
                setProjectId(null);
                setPriority(null);
              }}
              className="w-full rounded-xl bg-muted py-2.5 text-[15px] font-medium text-foreground active:bg-accent"
            >
              {t("filter.reset")}
            </button>
          </div>
        )}
      </BottomSheet>
    </div>
  );
}

function FilterOption({
  children,
  selected,
  onClick,
}: {
  children: React.ReactNode;
  selected?: boolean;
  onClick: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={onClick}
        className="flex w-full items-center gap-2.5 px-4 py-3 text-left text-[15px] transition-colors active:bg-accent"
      >
        <span className="flex flex-1 items-center gap-2.5 truncate">{children}</span>
        {selected && <Check className="size-4 shrink-0 text-brand" />}
      </button>
    </li>
  );
}

function StatusGroup({
  status,
  issues,
  onOpen,
  assigneeOf,
  onDone,
  onAssignMe,
  showAssignMe,
}: {
  status: IssueStatus;
  issues: Issue[];
  onOpen: (id: string) => void;
  assigneeOf: (issue: Issue) => CardAssignee;
  onDone: (issue: Issue) => void;
  onAssignMe: (issue: Issue) => void;
  showAssignMe: (issue: Issue) => boolean;
}) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2.5">
      <div className="flex items-center gap-2 px-1.5 pt-0.5">
        <span
          className={cn(
            "size-[9px] shrink-0 rounded-[3px]",
            GROUP_SWATCH[status] ?? "bg-muted-foreground/40",
          )}
        />
        <span className="text-xs font-semibold uppercase tracking-[0.06em] text-muted-foreground">
          {t(`status.${status}`)}
        </span>
        <span className="text-xs text-muted-foreground/70">{issues.length}</span>
      </div>
      {issues.map((issue) => (
        <TaskCard
          key={issue.id}
          issue={issue}
          assignee={assigneeOf(issue)}
          onOpen={() => onOpen(issue.id)}
          onDone={() => onDone(issue)}
          onAssignMe={() => onAssignMe(issue)}
          showAssignMe={showAssignMe(issue)}
        />
      ))}
    </section>
  );
}

const ACTION_W = 76; // px per revealed swipe action

// Card with the old IssueRow's swipe interactions rebuilt for the card design:
// swipe right reveals Done (green, left underlay), swipe left reveals
// Assign-to-me (brand, right underlay). Tap on the card opens the issue; a tap
// while an action is revealed just closes it.
function TaskCard({
  issue,
  assignee,
  onOpen,
  onDone,
  onAssignMe,
  showAssignMe,
}: {
  issue: Issue;
  assignee: CardAssignee;
  onOpen: () => void;
  onDone: () => void;
  onAssignMe: () => void;
  showAssignMe: boolean;
}) {
  const t = useT();
  const formatRelative = useFormatRelative();

  const isDone = issue.status === "done";
  const isClosed = isDone || issue.status === "cancelled";
  const pipeline = useMemo(
    () =>
      deriveStagePipeline({
        status: issue.status,
        labels: issue.labels ?? [],
      }),
    [issue.status, issue.labels],
  );

  // Swipe geometry: right (+) → Done, left (−) → Assign me.
  const maxRight = !isDone ? ACTION_W : 0;
  const maxLeft = showAssignMe ? ACTION_W : 0;

  const [tx, setTx] = useState(0);
  const [dragging, setDragging] = useState(false);
  const start = useRef({ x: 0, base: 0 });
  const moved = useRef(false);

  const onTouchStart = (e: TouchEvent) => {
    if (maxRight === 0 && maxLeft === 0) return;
    start.current = { x: e.touches[0]!.clientX, base: tx };
    moved.current = false;
    setDragging(true);
  };
  const onTouchMove = (e: TouchEvent) => {
    if (maxRight === 0 && maxLeft === 0) return;
    const delta = e.touches[0]!.clientX - start.current.x;
    if (Math.abs(delta) > 6) moved.current = true;
    setTx(Math.max(-maxLeft, Math.min(maxRight, start.current.base + delta)));
  };
  const onTouchEnd = () => {
    if (maxRight === 0 && maxLeft === 0) return;
    setDragging(false);
    setTx(
      maxRight > 0 && tx > maxRight / 2
        ? maxRight
        : maxLeft > 0 && tx < -maxLeft / 2
          ? -maxLeft
          : 0,
    );
  };

  const handleClick = () => {
    if (moved.current) {
      moved.current = false;
      return; // it was a swipe, not a tap
    }
    if (tx !== 0) {
      setTx(0); // first tap closes the revealed action
      return;
    }
    onOpen();
  };

  const runAction = (run: () => void) => {
    haptic("medium");
    run();
    setTx(0);
  };

  const stageLabel = isDone
    ? t("stage.merged")
    : `${t(`stage.${pipeline.current}`)} · ${formatRelative(issue.updated_at)}`;
  const priorityPill =
    issue.priority !== "none" ? PRIORITY_PILL[issue.priority] : undefined;

  return (
    <div className="relative overflow-hidden rounded-xl shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none">
      {/* Swipe underlays — interactive ONLY while revealed. Without the
          disabled/tabIndex gate they sit invisibly under the card and can be
          triggered by keyboard focus or programmatic clicks, silently mutating
          the issue. */}
      {maxRight > 0 && (
        <button
          type="button"
          onClick={() => runAction(onDone)}
          disabled={tx <= 0}
          aria-hidden={tx <= 0}
          tabIndex={tx > 0 ? 0 : -1}
          className={cn(
            "absolute inset-y-0 left-0 flex flex-col items-center justify-center gap-0.5 bg-success text-[11px] font-medium text-white",
            tx <= 0 && "pointer-events-none",
          )}
          style={{ width: ACTION_W }}
        >
          <Check className="size-4" />
          {t("row.done")}
        </button>
      )}
      {maxLeft > 0 && (
        <button
          type="button"
          onClick={() => runAction(onAssignMe)}
          disabled={tx >= 0}
          aria-hidden={tx >= 0}
          tabIndex={tx < 0 ? 0 : -1}
          className={cn(
            "absolute inset-y-0 right-0 flex flex-col items-center justify-center gap-0.5 bg-brand text-[11px] font-medium text-brand-foreground",
            tx >= 0 && "pointer-events-none",
          )}
          style={{ width: ACTION_W }}
        >
          <UserPlus className="size-4" />
          {t("row.assignMe")}
        </button>
      )}

      <button
        type="button"
        onClick={handleClick}
        onTouchStart={onTouchStart}
        onTouchMove={onTouchMove}
        onTouchEnd={onTouchEnd}
        style={{
          transform: `translateX(${tx}px)`,
          transition: dragging ? "none" : "transform 0.18s ease",
        }}
        className={cn(
          "relative flex w-full flex-col gap-2.5 rounded-xl border bg-card px-4 py-[15px] text-left active:border-brand",
          isDone ? "border-success/30" : "border-border",
        )}
      >
        {/* Row 1: identifier + priority pill */}
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-xs text-muted-foreground">{issue.identifier}</span>
          <span className="flex-1" />
          {priorityPill && (
            <span
              className={cn(
                "rounded-full px-[9px] py-[3px] text-[11px] font-semibold",
                priorityPill,
              )}
            >
              {t(`priority.${issue.priority}`)}
            </span>
          )}
        </div>

        {/* Row 2: title */}
        <div
          className={cn(
            "line-clamp-2 text-[15px] font-semibold leading-[1.35]",
            isClosed ? "text-muted-foreground line-through" : "text-foreground",
          )}
        >
          {issue.title}
        </div>

        {/* Row 3: stage rail · stage label · assignee */}
        <div className="flex items-center gap-2.5">
          <StageRail pipeline={pipeline} done={isDone} size="sm" />
          <span
            className={cn(
              "shrink-0 whitespace-nowrap text-[11px] font-semibold",
              isDone ? "text-success" : STAGE_TEXT[pipeline.current],
            )}
          >
            {stageLabel}
          </span>
          {assignee ? (
            assignee.isAgent ? (
              <AgentAvatar size={26} className="shrink-0" />
            ) : (
              <Avatar
                name={assignee.name}
                avatarUrl={assignee.avatarUrl}
                size={26}
                className="shrink-0"
              />
            )
          ) : (
            <span className="flex size-[26px] shrink-0 items-center justify-center rounded-full border border-dashed border-muted-foreground/40">
              <User className="size-3.5 text-muted-foreground/50" />
            </span>
          )}
        </div>
      </button>
    </div>
  );
}
