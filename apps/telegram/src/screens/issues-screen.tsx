import { useCallback, useMemo, useState } from "react";
import { Search, X, SlidersHorizontal, Check, Folder } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { issueListOptions } from "@agora/core/issues/queries";
import { useUpdateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import { projectListOptions } from "@agora/core/projects/queries";
import { STATUS_CONFIG, BOARD_STATUSES, PRIORITY_ORDER } from "@agora/core/issues/config";
import { useWorkspaceId } from "@agora/core/hooks";
import { useAuthStore } from "@agora/core/auth";
import type { Issue, IssueStatus, IssuePriority } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { IssueRow, type RowAssignee } from "../components/issue-row";
import { CenterMessage } from "../components/center-message";
import { BottomSheet } from "../components/bottom-sheet";
import { PriorityBars } from "../components/issue-badges";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

type Filter = "active" | "mine" | "unassigned" | "blocked" | "overdue" | "all";

// "mine" first + default: every Telegram user is auto-joined to the shared SD
// workspaces, so the board holds the whole team's issues. The Mini App is a
// personal companion — it must open on the user's OWN work, not the common
// board. The broader views stay available behind the other tabs.
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
  }
}

export function IssuesScreen() {
  const wsId = useWorkspaceId();
  const { data: issues = [], isLoading } = useQuery(issueListOptions(wsId));
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
  const [sheetOpen, setSheetOpen] = useState(false);

  const todayISO = new Date().toISOString().slice(0, 10);
  const advancedCount = (projectId ? 1 : 0) + (priority ? 1 : 0);

  // Resolve the assignee avatar + quick-action handlers passed to each row.
  const assigneeOf = useCallback(
    (issue: Issue): RowAssignee => {
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

  if (isLoading) return <CenterMessage spinner title={t("issues.loading")} />;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {searching ? (
        <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2.5">
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
            aria-label="Close search"
          >
            <X className="size-4" />
          </button>
        </div>
      ) : (
        <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2.5">
          <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
            {FILTER_ORDER.map((key) => (
              <button
                key={key}
                type="button"
                onClick={() => setFilter(key)}
                className={cn(
                  "shrink-0 rounded-lg px-3.5 py-1.5 text-[13px] font-medium transition-colors",
                  filter === key
                    ? "bg-brand text-brand-foreground"
                    : "bg-muted text-muted-foreground",
                )}
              >
                {t(`filter.${key}`)}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => setSheetOpen(true)}
            className="relative shrink-0 text-muted-foreground"
            aria-label={t("filter.title")}
          >
            <SlidersHorizontal className="size-[18px]" />
            {advancedCount > 0 && (
              <span className="absolute -right-1.5 -top-1.5 flex size-4 items-center justify-center rounded-full bg-brand text-[10px] font-semibold text-brand-foreground">
                {advancedCount}
              </span>
            )}
          </button>
          <button
            type="button"
            onClick={() => setSearching(true)}
            className="shrink-0 text-muted-foreground"
            aria-label="Search"
          >
            <Search className="size-[18px]" />
          </button>
        </div>
      )}

      {total === 0 ? (
        <CenterMessage
          title={query.trim() ? t("issues.noMatches") : t("issues.allClear")}
          subtitle={query.trim() ? t("issues.tryDifferent") : t(`empty.${filter}`)}
        />
      ) : (
        <div className="flex-1 overflow-y-auto">
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
      )}

      <BottomSheet open={sheetOpen} onClose={() => setSheetOpen(false)} title={t("filter.title")}>
        <div className="px-4 pb-1 pt-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
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

        <div className="px-4 pb-1 pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
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
  assigneeOf: (issue: Issue) => RowAssignee;
  onDone: (issue: Issue) => void;
  onAssignMe: (issue: Issue) => void;
  showAssignMe: (issue: Issue) => boolean;
}) {
  const cfg = STATUS_CONFIG[status];
  const t = useT();
  return (
    <section>
      <div className="sticky top-0 z-10 flex items-center gap-2 bg-muted/80 px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground backdrop-blur">
        <span className={cn("size-2 rounded-full", cfg.dividerColor)} />
        {t(`status.${status}`)}
        <span className="font-normal text-muted-foreground/70">{issues.length}</span>
      </div>
      <ul className="divide-y divide-border">
        {issues.map((issue) => (
          <li key={issue.id}>
            <IssueRow
              issue={issue}
              assignee={assigneeOf(issue)}
              onClick={() => onOpen(issue.id)}
              onDone={() => onDone(issue)}
              onAssignMe={() => onAssignMe(issue)}
              showAssignMe={showAssignMe(issue)}
            />
          </li>
        ))}
      </ul>
    </section>
  );
}
