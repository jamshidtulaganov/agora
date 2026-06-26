import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronLeft,
  Check,
  Bot,
  User as UserIcon,
  CircleSlash,
  Send,
  Share2,
  Sparkles,
  Loader2,
} from "lucide-react";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { useUpdateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import { ALL_STATUSES, PRIORITY_ORDER } from "@agora/core/issues/config";
import { getApi, ApiError } from "@agora/core/api";
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
import { Avatar } from "../components/avatar";
import { LabelChips } from "../components/label-chips";
import { MentionComposer } from "../components/mention-composer";
import { Markdown } from "../components/markdown";
import { haptic, shareToTelegram } from "../telegram/sdk";
import { encodeStartParam } from "../telegram/start-param";
import { useT, useFormatRelative } from "../i18n";

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
  const t = useT();
  const [sheet, setSheet] = useState<Sheet>(null);

  const { data: comments = [] } = useQuery({
    queryKey: commentsQueryKey(issueId),
    queryFn: () => getApi().listComments(issueId),
    // WS keeps the main app's timeline fresh; poll here as a webview fallback so
    // agent replies and teammates' comments show up.
    refetchInterval: 5000,
  });
  // Bot username for building the share deep link (t.me/<bot>?startapp=…).
  const { data: appConfig } = useQuery({
    queryKey: ["app-config"],
    queryFn: () => getApi().getConfig(),
    staleTime: Infinity,
  });
  const [comment, setComment] = useState("");
  const [posting, setPosting] = useState(false);
  const [commentError, setCommentError] = useState<string | null>(null);

  // AI comment-thread summary (free Agora base model). Lazy — only on tap.
  const [summary, setSummary] = useState<string | null>(null);
  const [summarizing, setSummarizing] = useState(false);
  const [summaryError, setSummaryError] = useState<string | null>(null);

  const summarize = async () => {
    if (summarizing) return;
    haptic("light");
    setSummarizing(true);
    setSummaryError(null);
    try {
      const { summary: text } = await getApi().summarizeComments(issueId);
      setSummary(text || t("detail.summaryEmpty"));
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        setSummaryError(t("detail.summaryUnavailable"));
      } else {
        setSummaryError(t("detail.summaryFailed"));
      }
    } finally {
      setSummarizing(false);
    }
  };

  const postComment = async () => {
    const content = comment.trim();
    if (!content || posting) return;
    haptic("light");
    setPosting(true);
    setCommentError(null);
    setComment("");
    try {
      // @mentions in the content trigger the mentioned agent's task server-side.
      await getApi().createComment(issueId, content);
      qc.invalidateQueries({ queryKey: commentsQueryKey(issueId) });
    } catch (err) {
      setComment(content); // restore the draft so the user can retry
      if (err instanceof ApiError && err.status === 404) {
        setCommentError(t("detail.notInWorkspace"));
      } else if (err instanceof ApiError) {
        setCommentError(`${t("detail.postFailed")} (${err.status})`);
      } else {
        setCommentError(t("detail.postFailed"));
      }
    } finally {
      setPosting(false);
    }
  };

  if (isLoading) return <CenterMessage spinner title={t("common.loading")} />;
  if (!issue) {
    return (
      <CenterMessage
        title={t("detail.notFound")}
        subtitle={t("detail.deleted")}
        actionLabel={t("common.back")}
        onAction={back}
      />
    );
  }

  const apply = (data: IssuePatch) => {
    haptic("light");
    update.mutate({ id: issue.id, ...data });
    setSheet(null);
  };

  const shareIssue = () => {
    haptic("light");
    const bot = appConfig?.telegram_bot_username;
    const link = bot
      ? `https://t.me/${bot}?startapp=${encodeStartParam(issue.id)}`
      : window.location.origin;
    shareToTelegram(link, `${issue.identifier}: ${issue.title}`);
  };

  const assigneeLabel = resolveAssigneeLabel(issue, members, agents, t);

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
        <div className="flex-1" />
        <button
          type="button"
          onClick={shareIssue}
          aria-label={t("detail.shareAria")}
          className="px-2 py-1 text-muted-foreground transition-colors active:text-foreground"
        >
          <Share2 className="size-[18px]" />
        </button>
      </header>

      <div className="flex-1 overflow-y-auto">
        <div className="px-4 py-4">
          <h1 className="text-xl font-semibold leading-snug text-foreground">
            {issue.title}
          </h1>
        </div>

        <div className="divide-y divide-border border-y border-border">
          <FieldRow label={t("create.status")} onClick={() => setSheet("status")}>
            <span className="inline-flex items-center gap-1.5">
              <StatusDot status={issue.status} />
              {t(`status.${issue.status}`)}
            </span>
          </FieldRow>
          <FieldRow label={t("create.priority")} onClick={() => setSheet("priority")}>
            <span className="inline-flex items-center gap-1.5">
              <PriorityBars priority={issue.priority} />
              {t(`priority.${issue.priority}`)}
            </span>
          </FieldRow>
          <FieldRow label={t("create.assignee")} onClick={() => setSheet("assignee")}>
            {assigneeLabel}
          </FieldRow>
        </div>

        {issue.labels && issue.labels.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 px-4 py-3">
            <LabelChips labels={issue.labels} />
          </div>
        )}

        {issue.description && (
          <div className="px-4 py-4">
            <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t("detail.description")}
            </div>
            <Markdown content={issue.description} />
          </div>
        )}

        <div className="border-t border-border px-4 py-3">
          <div className="mb-2 flex items-center justify-between gap-2">
            <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t("detail.comments")}
            </span>
            {comments.length > 0 && (
              <button
                type="button"
                onClick={summarize}
                disabled={summarizing}
                className="flex items-center gap-1.5 rounded-lg bg-brand/10 px-2.5 py-1 text-xs font-semibold text-brand transition-colors active:bg-brand/20 disabled:opacity-60"
              >
                {summarizing ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Sparkles className="size-3.5" />
                )}
                {summarizing ? t("detail.summarizing") : t("detail.summarize")}
              </button>
            )}
          </div>

          {summaryError && (
            <div className="mb-2 text-xs text-destructive">{summaryError}</div>
          )}
          {summary !== null && !summaryError && (
            <div className="mb-3 rounded-xl border border-brand/20 bg-brand/5 px-3 py-2.5">
              <div className="mb-1 flex items-center gap-1.5 text-xs font-semibold text-brand">
                <Sparkles className="size-3.5" />
                {t("detail.summaryTitle")}
              </div>
              <Markdown content={summary} className="text-[14px]" />
            </div>
          )}

          {comments.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("detail.noComments")}</p>
          ) : (
            <ul className="space-y-3">
              {/* Newest first — most recent activity reads at the top. The API
                  returns comments oldest→newest, so reverse a shallow copy. */}
              {[...comments].reverse().map((c) => (
                <CommentItem key={c.id} comment={c} members={members} agents={agents} />
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="shrink-0 border-t border-border bg-card pb-[max(env(safe-area-inset-bottom),0.5rem)]">
        {commentError && (
          <div className="px-4 pt-1.5 text-xs text-destructive">{commentError}</div>
        )}
        <div className="flex items-end gap-2 px-3 py-2.5">
          <MentionComposer
            value={comment}
            onChange={(v) => {
              setComment(v);
              if (commentError) setCommentError(null);
            }}
            onSubmit={postComment}
            members={members}
            agents={agents}
            placeholder={t("detail.commentPlaceholder")}
            disabled={posting}
          />
          <button
            type="button"
            onClick={postComment}
            disabled={!comment.trim() || posting}
            aria-label="Send comment"
            className="flex size-10 shrink-0 items-center justify-center rounded-full bg-brand text-brand-foreground disabled:opacity-40"
          >
            <Send className="size-[18px]" />
          </button>
        </div>
      </div>

      {/* Status picker */}
      <BottomSheet open={sheet === "status"} onClose={() => setSheet(null)} title={t("create.status")}>
        <ul className="pb-2">
          {ALL_STATUSES.map((s: IssueStatus) => (
            <OptionRow
              key={s}
              selected={s === issue.status}
              onClick={() => apply({ status: s })}
            >
              <StatusDot status={s} />
              {t(`status.${s}`)}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>

      {/* Priority picker */}
      <BottomSheet open={sheet === "priority"} onClose={() => setSheet(null)} title={t("create.priority")}>
        <ul className="pb-2">
          {PRIORITY_ORDER.map((p: IssuePriority) => (
            <OptionRow
              key={p}
              selected={p === issue.priority}
              onClick={() => apply({ priority: p })}
            >
              <PriorityBars priority={p} />
              {t(`priority.${p}`)}
            </OptionRow>
          ))}
        </ul>
      </BottomSheet>

      {/* Assignee picker */}
      <BottomSheet open={sheet === "assignee"} onClose={() => setSheet(null)} title={t("create.assignee")}>
        <ul className="pb-2">
          <OptionRow
            selected={!issue.assignee_id}
            onClick={() => apply({ assignee_type: null, assignee_id: null })}
          >
            <CircleSlash className="size-4 text-muted-foreground" />
            {t("common.unassigned")}
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
              <Bot className="size-4 text-brand" />
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
      className="flex w-full items-center justify-between gap-3 bg-card px-4 py-3.5 text-left transition-colors active:bg-accent"
    >
      <span className="text-[15px] text-muted-foreground">{label}</span>
      <span className="flex items-center gap-1.5 text-[15px] font-medium text-foreground">
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
        className="flex w-full items-center gap-2.5 px-4 py-3.5 text-left text-[15px] transition-colors active:bg-accent"
      >
        <span className="flex flex-1 items-center gap-2.5">{children}</span>
        {selected && <Check className="size-4 text-brand" />}
      </button>
    </li>
  );
}

function resolveAssigneeLabel(
  issue: Issue,
  members: { user_id: string; name: string }[],
  agents: { id: string; name: string }[],
  t: (key: string, vars?: Record<string, string | number>) => string,
): React.ReactNode {
  if (!issue.assignee_id || !issue.assignee_type) {
    return <span className="text-muted-foreground">{t("common.unassigned")}</span>;
  }
  if (issue.assignee_type === "member") {
    const m = members.find((x) => x.user_id === issue.assignee_id);
    const name = m?.name ?? t("common.member");
    return (
      <>
        <Avatar name={name} size={20} />
        {name}
      </>
    );
  }
  if (issue.assignee_type === "agent") {
    const a = agents.find((x) => x.id === issue.assignee_id);
    const name = a?.name ?? t("common.agent");
    return (
      <>
        <Avatar name={name} isAgent size={20} />
        {name}
      </>
    );
  }
  return <span className="text-muted-foreground">{t("common.unassigned")}</span>;
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
  const t = useT();
  const fmt = useFormatRelative();
  const isAgent = comment.author_type === "agent";
  const name = isAgent
    ? agents.find((a) => a.id === comment.author_id)?.name ?? t("common.agent")
    : members.find((m) => m.user_id === comment.author_id)?.name ?? t("common.member");
  return (
    <li className="flex gap-2.5">
      <Avatar name={name} isAgent={isAgent} size={26} className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-foreground">{name}</span>
          <span className="text-[11px] text-muted-foreground">
            {fmt(comment.created_at)}
          </span>
        </div>
        <Markdown content={comment.content} className="mt-1" />
      </div>
    </li>
  );
}
