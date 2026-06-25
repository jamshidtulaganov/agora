import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronLeft,
  Check,
  Bot,
  User as UserIcon,
  CircleSlash,
  Send,
} from "lucide-react";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { useUpdateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import {
  STATUS_CONFIG,
  ALL_STATUSES,
  PRIORITY_CONFIG,
  PRIORITY_ORDER,
} from "@agora/core/issues/config";
import { getApi } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import type {
  Issue,
  IssueStatus,
  IssuePriority,
  IssueAssigneeType,
  Comment,
  MemberWithUser,
  Agent,
} from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { BottomSheet } from "../components/bottom-sheet";
import { StatusDot, PriorityBars } from "../components/issue-badges";
import { formatRelative } from "../lib/time";
import { haptic } from "../telegram/sdk";
import { cn } from "../lib/cn";

const commentsQueryKey = (issueId: string) => ["tg-issue-comments", issueId];

type Sheet = "status" | "priority" | "assignee" | null;

// The subset of editable fields the detail screen patches. Matches
// UpdateIssueRequest field types so it spreads cleanly into useUpdateIssue.
type IssuePatch = {
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_type?: IssueAssigneeType | null;
  assignee_id?: string | null;
};

export function IssueDetailScreen({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const update = useUpdateIssue();
  const { back } = useRouter();
  const qc = useQueryClient();
  const [sheet, setSheet] = useState<Sheet>(null);

  const { data: comments = [] } = useQuery({
    queryKey: commentsQueryKey(issueId),
    queryFn: () => getApi().listComments(issueId),
    // WS keeps the main app's timeline fresh; poll here as a webview fallback so
    // agent replies and teammates' comments show up.
    refetchInterval: 5000,
  });
  const [comment, setComment] = useState("");
  const [posting, setPosting] = useState(false);

  const postComment = async () => {
    const content = comment.trim();
    if (!content || posting) return;
    haptic("light");
    setPosting(true);
    setComment("");
    try {
      // @mentions in the content trigger the mentioned agent's task server-side.
      await getApi().createComment(issueId, content);
      qc.invalidateQueries({ queryKey: commentsQueryKey(issueId) });
    } catch {
      setComment(content); // restore draft on failure
    } finally {
      setPosting(false);
    }
  };

  if (isLoading) return <CenterMessage spinner title="Loading…" />;
  if (!issue) {
    return (
      <CenterMessage
        title="Issue not found"
        subtitle="It may have been deleted."
        actionLabel="Back"
        onAction={back}
      />
    );
  }

  const apply = (data: IssuePatch) => {
    haptic("light");
    update.mutate({ id: issue.id, ...data });
    setSheet(null);
  };

  const assigneeLabel = resolveAssigneeLabel(issue, members, agents);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-1 border-b border-border bg-card px-2 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button
          type="button"
          onClick={back}
          className="flex items-center gap-0.5 px-1 py-1 text-sm text-muted-foreground"
        >
          <ChevronLeft className="size-5" />
        </button>
        <span className="font-mono text-xs text-muted-foreground">
          {issue.identifier}
        </span>
      </header>

      <div className="flex-1 overflow-y-auto">
        <div className="px-4 py-4">
          <h1 className="text-lg font-semibold leading-snug text-foreground">
            {issue.title}
          </h1>
        </div>

        <div className="divide-y divide-border border-y border-border">
          <FieldRow label="Status" onClick={() => setSheet("status")}>
            <span className="inline-flex items-center gap-1.5">
              <StatusDot status={issue.status} />
              {STATUS_CONFIG[issue.status].label}
            </span>
          </FieldRow>
          <FieldRow label="Priority" onClick={() => setSheet("priority")}>
            <span className="inline-flex items-center gap-1.5">
              <PriorityBars priority={issue.priority} />
              {PRIORITY_CONFIG[issue.priority].label}
            </span>
          </FieldRow>
          <FieldRow label="Assignee" onClick={() => setSheet("assignee")}>
            {assigneeLabel}
          </FieldRow>
        </div>

        {issue.description && (
          <div className="px-4 py-4">
            <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Description
            </div>
            <p className="whitespace-pre-wrap break-words text-sm text-foreground">
              {issue.description}
            </p>
          </div>
        )}

        <div className="border-t border-border px-4 py-3">
          <div className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Comments
          </div>
          {comments.length === 0 ? (
            <p className="text-sm text-muted-foreground">No comments yet.</p>
          ) : (
            <ul className="space-y-3">
              {comments.map((c) => (
                <CommentItem key={c.id} comment={c} members={members} agents={agents} />
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="flex shrink-0 items-end gap-2 border-t border-border bg-card px-3 py-2 pb-[max(env(safe-area-inset-bottom),0.5rem)]">
        <textarea
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void postComment();
            }
          }}
          rows={1}
          placeholder="Add a comment… use @agent to trigger"
          className="max-h-32 flex-1 resize-none rounded-2xl bg-muted px-3 py-2 text-sm text-foreground outline-none placeholder:text-muted-foreground"
        />
        <button
          type="button"
          onClick={postComment}
          disabled={!comment.trim() || posting}
          aria-label="Send comment"
          className="flex size-9 shrink-0 items-center justify-center rounded-full bg-[var(--brand,theme(colors.blue.600))] text-white disabled:opacity-40"
        >
          <Send className="size-4" />
        </button>
      </div>

      {/* Status picker */}
      <BottomSheet open={sheet === "status"} onClose={() => setSheet(null)} title="Status">
        <ul className="pb-2">
          {ALL_STATUSES.map((s: IssueStatus) => (
            <OptionRow
              key={s}
              selected={s === issue.status}
              onClick={() => apply({ status: s })}
            >
              <StatusDot status={s} />
              {STATUS_CONFIG[s].label}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>

      {/* Priority picker */}
      <BottomSheet open={sheet === "priority"} onClose={() => setSheet(null)} title="Priority">
        <ul className="pb-2">
          {PRIORITY_ORDER.map((p: IssuePriority) => (
            <OptionRow
              key={p}
              selected={p === issue.priority}
              onClick={() => apply({ priority: p })}
            >
              <PriorityBars priority={p} />
              {PRIORITY_CONFIG[p].label}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>

      {/* Assignee picker */}
      <BottomSheet open={sheet === "assignee"} onClose={() => setSheet(null)} title="Assignee">
        <ul className="pb-2">
          <OptionRow
            selected={!issue.assignee_id}
            onClick={() => apply({ assignee_type: null, assignee_id: null })}
          >
            <CircleSlash className="size-4 text-muted-foreground" />
            Unassigned
          </OptionRow>
          {members.map((m) => (
            <OptionRow
              key={m.user_id}
              selected={issue.assignee_type === "member" && issue.assignee_id === m.user_id}
              onClick={() => apply({ assignee_type: "member", assignee_id: m.user_id })}
            >
              <UserIcon className="size-4 text-muted-foreground" />
              {m.name}
            </OptionRow>
          ))}
          {agents.map((a) => (
            <OptionRow
              key={a.id}
              selected={issue.assignee_type === "agent" && issue.assignee_id === a.id}
              onClick={() => apply({ assignee_type: "agent", assignee_id: a.id })}
            >
              <Bot className="size-4 text-[var(--brand,theme(colors.blue.600))]" />
              {a.name}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>
    </div>
  );
}

function FieldRow({
  label,
  children,
  onClick,
}: {
  label: string;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center justify-between gap-3 bg-card px-4 py-3 text-left transition-colors active:bg-accent"
    >
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="flex items-center gap-1.5 text-sm font-medium text-foreground">
        {children}
      </span>
    </button>
  );
}

function OptionRow({
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
        className="flex w-full items-center gap-2.5 px-4 py-3 text-left text-sm transition-colors active:bg-accent"
      >
        <span className="flex flex-1 items-center gap-2.5">{children}</span>
        {selected && <Check className="size-4 text-[var(--brand,theme(colors.blue.600))]" />}
      </button>
    </li>
  );
}

function resolveAssigneeLabel(
  issue: Issue,
  members: { user_id: string; name: string }[],
  agents: { id: string; name: string }[],
): React.ReactNode {
  if (!issue.assignee_id || !issue.assignee_type) {
    return <span className="text-muted-foreground">Unassigned</span>;
  }
  if (issue.assignee_type === "member") {
    const m = members.find((x) => x.user_id === issue.assignee_id);
    return (
      <>
        <UserIcon className="size-3.5 text-muted-foreground" />
        {m?.name ?? "Member"}
      </>
    );
  }
  if (issue.assignee_type === "agent") {
    const a = agents.find((x) => x.id === issue.assignee_id);
    return (
      <>
        <Bot className="size-3.5 text-[var(--brand,theme(colors.blue.600))]" />
        {a?.name ?? "Agent"}
      </>
    );
  }
  return <span className="text-muted-foreground">Assigned</span>;
}

function CommentItem({
  comment,
  members,
  agents,
}: {
  comment: Comment;
  members: MemberWithUser[];
  agents: Agent[];
}) {
  const isAgent = comment.author_type === "agent";
  const name = isAgent
    ? agents.find((a) => a.id === comment.author_id)?.name ?? "Agent"
    : members.find((m) => m.user_id === comment.author_id)?.name ?? "Member";
  return (
    <li className="flex gap-2.5">
      <span
        className={cn(
          "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full",
          isAgent ? "bg-[var(--brand,theme(colors.blue.600))]/10" : "bg-muted",
        )}
      >
        {isAgent ? (
          <Bot className="size-3.5 text-[var(--brand,theme(colors.blue.600))]" />
        ) : (
          <UserIcon className="size-3.5 text-muted-foreground" />
        )}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-foreground">{name}</span>
          <span className="text-[11px] text-muted-foreground">
            {formatRelative(comment.created_at)}
          </span>
        </div>
        <p className="mt-0.5 whitespace-pre-wrap break-words text-sm text-foreground">
          {comment.content}
        </p>
      </div>
    </li>
  );
}
