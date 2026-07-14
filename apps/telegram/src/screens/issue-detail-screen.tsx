import { useMemo, useState } from "react";
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
  MessageCircle,
  ShieldAlert,
  GitMerge,
} from "lucide-react";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { useUpdateIssue } from "@agora/core/issues/mutations";
import { memberListOptions, agentListOptions } from "@agora/core/workspace/queries";
import { chatSessionsOptions } from "@agora/core/chat/queries";
import { useCreateChatSession } from "@agora/core/chat/mutations";
import { useAttachLabel, useCreateLabel, labelKeys } from "@agora/core/labels";
import { ALL_STATUSES, PRIORITY_ORDER } from "@agora/core/issues/config";
import { getApi, ApiError } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { useStagePipeline } from "@agora/views/issues/components/use-stage-pipeline";
import type {
  Issue,
  IssueStatus,
  IssuePriority,
  IssueAssigneeType,
  Comment,
  MemberWithUser,
  Agent,
  AgentTask,
} from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { BottomSheet } from "../components/bottom-sheet";
import { ConfirmDialog } from "../components/confirm-dialog";
import { TabSkeleton } from "../components/skeleton";
import { QueryError } from "../components/query-error";
import { ExpandableText } from "../components/expandable";
import { StatusDot, PriorityBars } from "../components/issue-badges";
import { Avatar } from "../components/avatar";
import { AgentAvatar, AgentTag, type AgentStatusTone } from "../components/agent-avatar";
import { StageRail, StageSegments, STAGE_TEXT } from "../components/stage-rail";
import { LabelChips } from "../components/label-chips";
import { MentionComposer } from "../components/mention-composer";
import { Markdown } from "../components/markdown";
import { AttachmentList } from "../components/attachment-list";
import { useToast } from "../components/toast";
import { cn } from "../lib/cn";
import { haptic, shareToTelegram } from "../telegram/sdk";
import { encodeStartParam } from "../telegram/start-param";
import { useT, useFormatRelative } from "../i18n";

const commentsQueryKey = (issueId: string) => ["tg-issue-comments", issueId];
const issueTasksQueryKey = (issueId: string) => ["tg-issue-tasks", issueId];

type Sheet = "status" | "priority" | "assignee" | null;

// The subset of editable fields the detail screen patches. Matches
// UpdateIssueRequest field types so it spreads cleanly into useUpdateIssue.
type IssuePatch = {
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_type?: IssueAssigneeType | null;
  assignee_id?: string | null;
};

// Status → tinted chip classes following the stage-group palette (QA/info,
// Dev/warning, Review/brand, Done/success). Unknown server values fall back
// to the muted chip (enum drift downgrades, never crashes).
const STATUS_CHIP: Record<string, string> = {
  backlog: "bg-muted text-muted-foreground",
  todo: "bg-muted text-muted-foreground",
  in_progress: "bg-warning/15 text-warning",
  in_review: "bg-info/10 text-info",
  blocked: "bg-destructive/10 text-destructive",
  done: "bg-success/10 text-success",
  cancelled: "bg-muted text-muted-foreground",
};

const PRIORITY_CHIP: Record<string, string> = {
  urgent: "bg-destructive/10 text-destructive",
  high: "bg-warning/15 text-warning",
  medium: "bg-info/10 text-info",
  low: "bg-muted text-muted-foreground",
  none: "bg-muted text-muted-foreground",
};

// Active (non-terminal) agent-task states — amber in the activity timeline.
const ACTIVE_TASK_STATUSES = new Set(["running", "queued", "dispatched", "waiting_local_directory"]);

// Agent trigger summaries carry raw mention tokens ("[@Name](mention://…)");
// the timeline is plain text, so render them as a readable "@Name".
function stripMentionTokens(text: string): string {
  return text.replace(/\[@([^\]]+)\]\(mention:\/\/[^)]*\)/g, "@$1");
}

function activityDotClass(status: AgentTask["status"]): string {
  if (ACTIVE_TASK_STATUSES.has(status)) return "bg-warning";
  switch (status) {
    case "completed":
      return "bg-success";
    case "failed":
      return "bg-destructive";
    case "cancelled":
      return "bg-muted-foreground/40";
    default:
      return "bg-muted-foreground/40";
  }
}

export function IssueDetailScreen({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const {
    data: issue,
    isLoading,
    isError,
    refetch: refetchIssue,
  } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const update = useUpdateIssue();
  const { back, navigate } = useRouter();
  const qc = useQueryClient();
  const t = useT();
  const fmt = useFormatRelative();
  const { toast } = useToast();
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

  // Agent activity — the agent_task_queue rows for this issue drive both the
  // hero card status dot and the activity timeline. No WS in the webview,
  // so poll.
  const { data: tasks = [] } = useQuery({
    queryKey: issueTasksQueryKey(issueId),
    queryFn: () => getApi().listTasksByIssue(issueId),
    refetchInterval: 15000,
  });

  // Merge gates — same queryKey as useStagePipeline / the web EditorGates so
  // all observers share one cache entry. Poll only while review is moving.
  const { data: mergeReadiness } = useQuery({
    queryKey: ["merge-readiness", issueId],
    queryFn: () => getApi().mergeReadiness(issueId),
    enabled: !!issueId,
    refetchInterval: issue?.status === "in_review" ? 15000 : undefined,
  });

  // Derived SDLC pipeline (Design → Dev → QA → Review) for the hero segments
  // and the cycle-position rail.
  const pipeline = useStagePipeline(wsId, issueId);

  const isAgentAssignee = issue?.assignee_type === "agent" && !!issue.assignee_id;

  // LEAD detection: an agent "leads" when it is a squad leader. Unknown
  // (query not loaded / no squads) → skip the tag.
  const { data: squads } = useQuery({
    queryKey: ["tg-squads", wsId],
    queryFn: () => getApi().listSquads(),
    staleTime: 300_000,
    enabled: !!wsId && isAgentAssignee,
  });

  // Find-or-create chat wiring for the hero card's chat button. The lookup
  // itself happens inside openAgentChat via ensureQueryData — deciding from a
  // possibly-unresolved cache here would create duplicate sessions on cold
  // start (list still loading → "no session" → create).
  const createSession = useCreateChatSession();
  const [openingChat, setOpeningChat] = useState(false);

  // Human merge sign-off = attach the `merge:override` label. There is no
  // dedicated approve endpoint; the label is the recorded human approval that
  // the sprint-PR done-transition honors. It does NOT merge the PR itself —
  // the lead agent/automation acts on it.
  const attachLabel = useAttachLabel(issueId);
  const createLabel = useCreateLabel();
  const [mergeConfirm, setMergeConfirm] = useState(false);
  const [mergeBusy, setMergeBusy] = useState(false);

  const [comment, setComment] = useState("");
  const [posting, setPosting] = useState(false);
  const [commentError, setCommentError] = useState<string | null>(null);

  // AI comment-thread summary (free Agora base model). Lazy — only on tap.
  const [summary, setSummary] = useState<string | null>(null);
  const [summarizing, setSummarizing] = useState(false);
  const [summaryError, setSummaryError] = useState<string | null>(null);

  // Newest-first activity entries (max 6 in the timeline card).
  const sortedTasks = useMemo(
    () =>
      [...tasks].sort((a, b) => (b?.created_at ?? "").localeCompare(a?.created_at ?? "")),
    [tasks],
  );
  const activity = sortedTasks.slice(0, 6);
  const newestTask = sortedTasks[0];

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

  if (isLoading) return <TabSkeleton />;
  // A transient fetch failure (cellular blip, 5xx) must not read as "deleted"
  // — especially on the share/deep-link entry path. Offer a retry instead.
  if (isError) return <QueryError onRetry={() => void refetchIssue()} />;
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

  // metadata is a flat KV map; pr_number is only trustworthy as a number.
  const prRaw = issue.metadata?.pr_number;
  const prNumber = typeof prRaw === "number" ? prRaw : null;

  // Sign-off already recorded → the ball is no longer in the human's court.
  // Hides the paused banner and the CTA so the screen converges after confirm.
  const hasOverride = (issue.labels ?? []).some((l) => l?.name === "merge:override");

  // Amber "paused — waiting for you" gate: review stage + an actual open PR
  // + gates not ready + no recorded sign-off. Done/cancelled issues never
  // show it — the merge already happened (or never will).
  const gateBlocked =
    pipeline.current === "review" &&
    issue.status !== "done" &&
    issue.status !== "cancelled" &&
    prNumber != null &&
    !hasOverride &&
    !!mergeReadiness &&
    mergeReadiness.ready !== true;

  const heroAgent = isAgentAssignee
    ? agents.find((a) => a.id === issue.assignee_id)
    : undefined;
  const heroAgentId = isAgentAssignee ? issue.assignee_id : null;
  const heroAgentName = heroAgent?.name ?? t("common.agent");
  const isLead = !!heroAgentId && !!squads?.some((s) => s?.leader_id === heroAgentId);

  const hasRunningTask = sortedTasks.some((task) => task?.status === "running");
  const heroStatus: AgentStatusTone = hasRunningTask
    ? "running"
    : gateBlocked ||
        newestTask?.status === "failed" ||
        newestTask?.status === "waiting_local_directory"
      ? "paused"
      : "idle";

  const heroSubParts: string[] = [];
  if (newestTask?.started_at) {
    heroSubParts.push(t("hero.onTask", { time: fmt(newestTask.started_at) }));
  }
  heroSubParts.push(
    t("hero.inStage", {
      stage: t(`stage.${pipeline.current}`),
      time: fmt(issue.updated_at),
    }),
  );

  const openAgentChat = async () => {
    if (!heroAgentId || openingChat) return;
    haptic("light");
    setOpeningChat(true);
    try {
      // ensureQueryData fetches the sessions list when it isn't cached yet, so
      // a cold-start tap can't race the list into a duplicate session.
      const sessions = await qc.ensureQueryData(chatSessionsOptions(wsId));
      const existing =
        sessions.find((s) => s?.agent_id === heroAgentId && s.status === "active") ??
        sessions.find((s) => s?.agent_id === heroAgentId);
      const session =
        existing ?? (await createSession.mutateAsync({ agent_id: heroAgentId }));
      navigate({ name: "chat-session", id: session.id });
    } catch {
      /* stay on the screen — the button remains tappable to retry */
    } finally {
      setOpeningChat(false);
    }
  };

  // The human sign-off: find (or create) the `merge:override` label and
  // attach it. The tier gate treats the label as recorded approval.
  const signOffMerge = async () => {
    if (mergeBusy) return;
    haptic("light");
    setMergeBusy(true);
    try {
      const res = await getApi().listLabels();
      let label = res?.labels?.find((l) => l?.name === "merge:override");
      if (!label) {
        label = await createLabel.mutateAsync({ name: "merge:override", color: "#2563eb" });
      }
      if (!label?.id) throw new Error("merge:override label unavailable");
      await attachLabel.mutateAsync(label.id);
      setMergeConfirm(false);
      toast(t("merge.done", { id: issue.identifier }), "ok");
      qc.invalidateQueries({ queryKey: ["merge-readiness", issueId] });
      qc.invalidateQueries({ queryKey: labelKeys.byIssue(wsId, issueId) });
      qc.invalidateQueries({ queryKey: issueDetailOptions(wsId, issueId).queryKey });
    } catch {
      setMergeConfirm(false);
      toast(t("merge.failed"), "info");
    } finally {
      setMergeBusy(false);
    }
  };

  const showMergeCta =
    prNumber != null &&
    !!mergeReadiness &&
    pipeline.current === "review" &&
    issue.status !== "done" &&
    !hasOverride;

  const agentNameById = (id: string) =>
    agents.find((a) => a.id === id)?.name ?? t("common.agent");

  return (
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col">
      {/* Sub-header: back · mono identifier · share */}
      <header className="flex shrink-0 items-center gap-2 px-4 pb-2.5 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button
          type="button"
          onClick={back}
          className="flex min-h-11 items-center gap-[3px] text-[15px] text-brand"
        >
          <ChevronLeft className="size-[17px]" />
          {t("common.back")}
        </button>
        <div className="flex-1" />
        <span className="font-mono text-[13px] text-muted-foreground">
          {issue.identifier}
        </span>
        <button
          type="button"
          onClick={shareIssue}
          aria-label={t("detail.shareAria")}
          className="flex min-h-11 items-center px-2 text-muted-foreground transition-colors active:text-foreground"
        >
          <Share2 className="size-[18px]" />
        </button>
      </header>

      <div className="flex-1 overflow-y-auto">
        <div className="flex flex-col gap-3 px-4 pb-3">
          {/* Chip row: status · priority · PR */}
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              onClick={() => setSheet("status")}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full px-3 py-[6px] text-[12.5px] font-semibold active:brightness-95",
                STATUS_CHIP[issue.status] ?? "bg-muted text-muted-foreground",
              )}
            >
              <StatusDot status={issue.status} />
              {t(`status.${issue.status}`)}
            </button>
            <button
              type="button"
              onClick={() => setSheet("priority")}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full px-3 py-[6px] text-[12.5px] font-semibold active:brightness-95",
                PRIORITY_CHIP[issue.priority] ?? "bg-muted text-muted-foreground",
              )}
            >
              <PriorityBars priority={issue.priority} />
              {t(`priority.${issue.priority}`)}
            </button>
            {prNumber != null && (
              <span className="inline-flex items-center rounded-full bg-muted px-[9px] py-[6px] font-mono text-[11.5px] font-medium text-muted-foreground">
                PR #{prNumber}
              </span>
            )}
          </div>

          {/* Title */}
          <h1 className="text-[21px] font-semibold leading-[1.32] tracking-[-0.25px] text-foreground [text-wrap:pretty]">
            {issue.title}
          </h1>

          {issue.labels && issue.labels.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              <LabelChips labels={issue.labels} />
            </div>
          )}

          {/* Agent hero card — the agent leads; only for agent assignees */}
          {isAgentAssignee && (
            <section className="rounded-2xl border-[1.5px] border-brand/20 bg-card p-4 shadow-[0_4px_20px_rgba(37,99,235,0.08)] dark:shadow-none">
              <div className="flex items-center gap-3">
                <AgentAvatar size={46} status={heroStatus} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-[7px]">
                    <span className="truncate text-base font-semibold tracking-[-0.2px] text-foreground">
                      {heroAgentName}
                    </span>
                    {isLead && <AgentTag label="LEAD" />}
                  </div>
                  <div className="mt-0.5 truncate text-[12.5px] text-muted-foreground">
                    {heroSubParts.join(" · ")}
                  </div>
                </div>
                <button
                  type="button"
                  onClick={openAgentChat}
                  disabled={openingChat}
                  aria-label={t("agents.chatAria", { agent: heroAgentName })}
                  className="flex size-[38px] shrink-0 items-center justify-center rounded-full bg-muted text-foreground/70 active:bg-muted/80 disabled:opacity-60"
                >
                  {openingChat ? (
                    <Loader2 className="size-[19px] animate-spin" />
                  ) : (
                    <MessageCircle className="size-[19px]" />
                  )}
                </button>
              </div>

              {gateBlocked && mergeReadiness && (
                <div className="mt-[13px] flex items-start gap-[9px] rounded-[11px] border border-warning/25 bg-warning/10 p-3">
                  <ShieldAlert className="mt-px size-[17px] shrink-0 text-warning" />
                  <div className="min-w-0">
                    <div className="text-[13px] font-semibold text-warning">
                      {t("hero.paused")}
                    </div>
                    <p className="mt-0.5 text-xs leading-[1.45] text-muted-foreground [text-wrap:pretty]">
                      {t("hero.pausedBody", {
                        tier: mergeReadiness.tier,
                        n: prNumber ?? "—",
                      })}
                    </p>
                  </div>
                </div>
              )}

              <StageSegments pipeline={pipeline} className="mt-[13px]" />
              <div className="mt-[7px] flex justify-between">
                {pipeline.stages.map((s) => (
                  <span
                    key={s.stage}
                    className={cn(
                      "text-[10px] font-semibold uppercase tracking-[0.04em]",
                      s.state === "passed"
                        ? "text-success"
                        : s.stage === pipeline.current
                          ? "text-warning"
                          : "text-muted-foreground",
                    )}
                  >
                    {t(`stage.${s.stage}`)}
                  </span>
                ))}
              </div>
            </section>
          )}

          {/* Cycle position — always shown, human assignees included */}
          <section className="rounded-xl border border-border bg-card p-4 shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none">
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
                {t("detail.cyclePosition")}
              </span>
              <span
                className={cn("text-xs font-semibold", STAGE_TEXT[pipeline.current])}
              >
                {t(`stage.${pipeline.current}`)}
              </span>
            </div>
            <StageRail
              pipeline={pipeline}
              done={issue.status === "done"}
              size="lg"
              className="mt-3.5"
            />
            <div className="mt-2 flex justify-between">
              {pipeline.stages.map((s) => (
                <span
                  key={s.stage}
                  className={cn(
                    "text-[10px] font-semibold uppercase tracking-[0.04em]",
                    s.stage === pipeline.current
                      ? cn("font-bold", STAGE_TEXT[s.stage])
                      : s.state === "skipped"
                        ? "text-muted-foreground/50"
                        : "text-muted-foreground",
                  )}
                >
                  {t(`stage.${s.stage}`)}
                </span>
              ))}
            </div>
          </section>

          {/* Agent activity timeline */}
          <section className="rounded-xl border border-border bg-card px-4 pb-1.5 pt-3.5 shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none">
            <div className="mb-2.5 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
              {t("detail.agentActivity")}
            </div>
            {activity.length === 0 ? (
              <p className="pb-2 text-xs text-muted-foreground">
                {t("detail.activityEmpty")}
              </p>
            ) : (
              <ul>
                {activity.map((task, i) => (
                  <li key={task.id} className="flex gap-[11px]">
                    <div className="flex w-3 flex-none flex-col items-center">
                      <span
                        className={cn(
                          "mt-1 size-[9px] shrink-0 rounded-full",
                          activityDotClass(task.status),
                          i === 0 &&
                            ACTIVE_TASK_STATUSES.has(task.status) &&
                            "ring-[3px] ring-warning/15",
                        )}
                      />
                      {i < activity.length - 1 && (
                        <span className="w-[2px] flex-1 bg-border/60" />
                      )}
                    </div>
                    <div
                      className={cn(
                        "min-w-0 flex-1",
                        i === activity.length - 1 ? "pb-2.5" : "pb-3",
                      )}
                    >
                      <p className="truncate text-[13px] leading-[1.4] text-foreground">
                        <span className="font-medium">{agentNameById(task.agent_id)}</span>
                        <span className="text-muted-foreground">
                          {" — "}
                          {stripMentionTokens(
                            task.trigger_summary || task.kind || task.status,
                          )}
                        </span>
                      </p>
                      <div className="mt-px text-[11.5px] text-muted-foreground/70">
                        {fmt(task.started_at ?? task.created_at)}
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </section>

          {/* Assignee row — keeps the assignee picker reachable (status and
              priority open from the chips above) */}
          <div className="overflow-hidden rounded-xl border border-border shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none">
            <FieldRow label={t("create.assignee")} onClick={() => setSheet("assignee")}>
              {assigneeLabel}
            </FieldRow>
          </div>
        </div>

        {issue.description && (
          <div className="px-4 py-3">
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

      {/* Sticky merge CTA — the one-tap human sign-off, above the composer */}
      {showMergeCta && (
        <div className="shrink-0 px-4 pb-2 pt-1.5">
          <button
            type="button"
            onClick={() => {
              if (mergeBusy) return;
              haptic("light");
              setMergeConfirm(true);
            }}
            disabled={mergeBusy}
            className="flex h-[52px] w-full items-center justify-center gap-2.5 rounded-xl bg-brand text-base font-semibold text-brand-foreground transition-[filter] active:brightness-90 disabled:opacity-70"
          >
            {mergeBusy ? (
              <>
                <Loader2 className="size-4 animate-spin" />
                {t("detail.signingOff")}
              </>
            ) : (
              <>
                <GitMerge className="size-5" />
                {t("detail.mergeCta", { n: prNumber })}
              </>
            )}
          </button>
        </div>
      )}

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

      {/* Merge sign-off confirm */}
      <ConfirmDialog
        open={mergeConfirm}
        icon={<GitMerge className="size-[26px]" />}
        title={t("merge.title", { n: prNumber ?? "—" })}
        body={t("merge.body")}
        confirmLabel={t("merge.confirm")}
        cancelLabel={t("merge.cancel")}
        busy={mergeBusy}
        onConfirm={signOffMerge}
        onCancel={() => setMergeConfirm(false)}
      />

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
        {/* Agent replies (QA verdicts etc.) run long — collapse behind
            "Show more"; human comments are usually short and stay as-is. */}
        {isAgent ? (
          <ExpandableText className="mt-1" fadeClass="from-sidebar dark:from-background">
            <Markdown content={comment.content} />
          </ExpandableText>
        ) : (
          <Markdown content={comment.content} className="mt-1" />
        )}
        <AttachmentList attachments={comment.attachments} />
      </div>
    </li>
  );
}
