import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search, X } from "lucide-react";
import { issueListOptions } from "@agora/core/issues/queries";
import { useUpdateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import { STATUS_CONFIG, BOARD_STATUSES } from "@agora/core/issues/config";
import { useWorkspaceId } from "@agora/core/hooks";
import { useAuthStore } from "@agora/core/auth";
import type { Issue, IssueStatus } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { IssueRow, type RowAssignee } from "../components/issue-row";
import { CenterMessage } from "../components/center-message";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

type Filter = "active" | "mine" | "unassigned" | "blocked" | "overdue" | "all";

const FILTER_ORDER: Filter[] = [
  "active",
  "mine",
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
      return issue.assignee_type === "member" && issue.assignee_id === myId;
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
  const update = useUpdateIssue();
  const { navigate } = useRouter();
  const t = useT();
  const [filter, setFilter] = useState<Filter>("active");
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);

  const todayISO = new Date().toISOString().slice(0, 10);

  // Resolve the assignee avatar + quick-action handlers passed to each row.
  const assigneeOf = useCallback(
    (issue: Issue): RowAssignee => {
      if (!issue.assignee_id || !issue.assignee_type) return null;
      if (issue.assignee_type === "member") {
        const m = members.find((x) => x.user_id === issue.assignee_id);
        return m ? { name: m.name, isAgent: false } : null;
      }
      if (issue.assignee_type === "agent") {
        const a = agents.find((x) => x.id === issue.assignee_id);
        return a ? { name: a.name, isAgent: true } : null;
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
  }, [issues, filter, query, myId, todayISO]);

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
                  "shrink-0 rounded-full px-3.5 py-1.5 text-[13px] font-medium transition-colors",
                  filter === key
                    ? "bg-foreground text-background"
                    : "bg-muted text-muted-foreground",
                )}
              >
                {t(`filter.${key}`)}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => setSearching(true)}
            className="shrink-0 text-muted-foreground"
            aria-label="Search"
          >
            <Search className="size-4" />
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
    </div>
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
      <div className="sticky top-0 z-10 flex items-center gap-2 bg-muted/80 px-4 py-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground backdrop-blur">
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
