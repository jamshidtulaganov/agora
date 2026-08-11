package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/analytics"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/mention"
	obsmetrics "github.com/jamshidtulaganov/agora/server/internal/metrics"
	"github.com/jamshidtulaganov/agora/server/internal/realtime"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
	"github.com/jamshidtulaganov/agora/server/pkg/redact"
	"github.com/jamshidtulaganov/agora/server/pkg/taskfailure"
)

type TaskService struct {
	Queries   *db.Queries
	TxStarter TxStarter
	Hub       *realtime.Hub
	Bus       *events.Bus
	Analytics analytics.Client
	Metrics   *obsmetrics.BusinessMetrics
	Wakeup    TaskWakeupNotifier
	// EmptyClaim caches "this runtime has no queued task" so the daemon
	// poll path can skip a Postgres scan on the steady-state empty case.
	// Optional — a nil cache disables the fast path and every claim
	// goes through the DB. Wired in router.go from the shared Redis
	// client.
	EmptyClaim *EmptyClaimCache

	// OnReviewVerdictLabeled, when non-nil, is invoked after the internal
	// task-completion ingress paths (captureStructuredResult / createAgentComment)
	// capture a NEW review verdict gate label, so the merge re-check fires the
	// same way the HTTP comment ingress fires it (comment.go). Kept as a callback
	// wired by the handler layer to avoid a service→handler import cycle;
	// nil-safe. actorID is the reviewer agent id (as string).
	OnReviewVerdictLabeled func(ctx context.Context, issue db.Issue, verdict, actorID string)
	// OnOrchestrationTaskTerminal advances the persisted orchestration graph.
	// It also observes ordinary issue-task terminals so a step deferred behind
	// that task's per-agent queue slot can be scheduled immediately afterward.
	// The handler layer wires it to avoid a service import cycle.
	OnOrchestrationTaskTerminal func(ctx context.Context, task db.AgentTaskQueue) error
	// OnOrchestrationRunReady retries dispatch for active runs with persisted
	// runnable work after a prior post-commit dispatch failure.
	OnOrchestrationRunReady func(ctx context.Context, runID pgtype.UUID) error
	// OnOrchestrationAnswerCommentsReady repairs the commit boundary between a
	// durable member comment and the orchestration answer it was meant to
	// create. The handler owns comment/mention semantics; the service exposes
	// the periodic sweeper hook without importing the handler package.
	OnOrchestrationAnswerCommentsReady func(ctx context.Context) (int, error)

	analyticsContextMu    sync.Mutex
	analyticsContextCache map[string]analytics.TaskContext
	analyticsContextOrder []string
}

type TaskWakeupNotifier interface {
	NotifyTaskAvailable(runtimeID, taskID string)
}

// triggerSummaryMaxLen caps the snapshot length so the row stays cheap to
// transmit (it ends up in every task list response). 200 is enough for a
// recognisable preview of a one-paragraph comment.
const triggerSummaryMaxLen = 200

// ErrOrchestrationAdvance marks a task whose terminal row is durable but whose
// persisted orchestration transition must be replayed.
var ErrOrchestrationAdvance = errors.New("advance orchestration")

// truncateForSummary returns s shortened to maxRunes, with a trailing
// `…` when truncated. Operates on runes (not bytes) so multibyte characters
// — Chinese / emoji — count as one each. Strips surrounding whitespace
// first so a leading newline doesn't waste budget.
func truncateForSummary(s string, maxRunes int) string {
	// strings.Builder + Grow avoids the O(N²) realloc cycle of `+=` in
	// a loop. Grow uses byte length, which is an upper bound for the
	// rune-equivalent output (replacing \n/\r/\t with space is byte-equal
	// for ASCII whitespace), so we never reallocate.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	rs := []rune(strings.TrimSpace(b.String()))
	if len(rs) <= maxRunes {
		return string(rs)
	}
	return string(rs[:maxRunes]) + "…"
}

const (
	taskAnalyticsContextCacheMax = 4096
	// claimResponseRecoveryWindow must exceed daemon client.Timeout for
	// /tasks/claim (30s) plus /tasks/{id}/start (30s) plus scheduling slack, so
	// an in-flight StartTask cannot be reclaimed and double-dispatched.
	claimResponseRecoveryWindow = 90 * time.Second
)

// buildCommentTriggerSummary fetches the comment content and truncates
// it for storage on the task row. Returns an invalid pgtype.Text when
// the comment is missing (deleted / wrong workspace / etc) so the column
// stays NULL — front-end falls back to a structural label in that case.
func (s *TaskService) buildCommentTriggerSummary(ctx context.Context, commentID pgtype.UUID) pgtype.Text {
	if !commentID.Valid {
		return pgtype.Text{}
	}
	comment, err := s.Queries.GetComment(ctx, commentID)
	if err != nil {
		return pgtype.Text{}
	}
	summary := truncateForSummary(comment.Content, triggerSummaryMaxLen)
	if summary == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: summary, Valid: true}
}

func NewTaskService(q *db.Queries, tx TxStarter, hub *realtime.Hub, bus *events.Bus, wakeups ...TaskWakeupNotifier) *TaskService {
	var wakeup TaskWakeupNotifier
	if len(wakeups) > 0 {
		wakeup = wakeups[0]
	}
	return &TaskService{Queries: q, TxStarter: tx, Hub: hub, Bus: bus, Wakeup: wakeup}
}

var trivialDoneMarkers = []string{
	"done",
	"готово",
	"готова",
	"сделано",
	"完成",
	"完了",
}

func isTrivialDoneOutput(output string) bool {
	normalized := strings.TrimSpace(strings.ToLower(output))
	normalized = strings.Trim(normalized, ".!！。… ")
	for _, marker := range trivialDoneMarkers {
		if normalized == marker {
			return true
		}
	}
	return false
}

func (s *TaskService) captureTaskQueued(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskEnqueued(source, runtimeMode)
	}
}

func (s *TaskService) captureTaskDispatched(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskDispatched(util.UUIDToString(task.ID), source, runtimeMode, taskQueueWaitSeconds(task))
	}
}

func (s *TaskService) AnalyticsContextForTask(ctx context.Context, task db.AgentTaskQueue) analytics.TaskContext {
	return s.taskAnalyticsContext(ctx, task)
}

func (s *TaskService) captureTaskStarted(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, provider := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskStarted(source, runtimeMode, provider)
	}
}

func (s *TaskService) captureTaskCompleted(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
	}
}

// persistRunTrace anchors a terminal agent run in agent_run_trace — the seed of
// the fine-tuning dataset. Best-effort: every failure is logged and swallowed so
// it can never break task completion. The run's prompt/completion are NOT copied
// here; they join back in from task_message / issue / agent / task_usage by
// task_id. Outcome columns stay at their defaults and accrue later via event hooks.
func (s *TaskService) persistRunTrace(ctx context.Context, task db.AgentTaskQueue) {
	// workspace_id isn't on the task row — resolve it through the agent.
	agent, err := s.Queries.GetAgent(ctx, task.AgentID)
	if err != nil {
		slog.Warn("run trace: resolve agent failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	var issueStatus pgtype.Text
	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			issueStatus = pgtype.Text{String: issue.Status, Valid: true}
		}
	}
	if _, err := s.Queries.UpsertAgentRunTrace(ctx, db.UpsertAgentRunTraceParams{
		TaskID:           task.ID,
		WorkspaceID:      agent.WorkspaceID,
		AgentID:          task.AgentID,
		IssueID:          task.IssueID,
		TaskStatus:       task.Status,
		IssueStatusAtRun: issueStatus,
	}); err != nil {
		slog.Warn("run trace: upsert failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}
}

func (s *TaskService) captureTaskFailed(ctx context.Context, task db.AgentTaskQueue) {
	failureReason := taskFailureReason(task)
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
		s.Metrics.RecordTaskFailed(source, runtimeMode, failureReason)
	}
}

func (s *TaskService) captureTaskCancelled(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
	}
	// Revoke any mat_ task tokens minted for this task. Cancellation is
	// a terminal transition, so the running agent process no longer
	// needs to call back; eagerly deleting the token closes the
	// window where a compromised process could keep authenticating
	// against the API until the 24h expiry. Failure is non-fatal — the
	// expiry / FK cascade are the durable guards. MUL-2600.
	if err := s.Queries.DeleteTaskTokensByTask(ctx, task.ID); err != nil {
		slog.Warn("cancel task: failed to revoke task tokens",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}
}

func (s *TaskService) CaptureTaskUsage(ctx context.Context, task db.AgentTaskQueue, provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) {
	if s.Metrics == nil {
		return
	}
	source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
	s.Metrics.RecordLLMUsage(source, runtimeMode, provider, model, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens)
}

func (s *TaskService) CaptureQueuedExpiredTasks(ctx context.Context, tasks []db.AgentTaskQueue) {
	if s.Metrics == nil {
		return
	}
	for _, task := range tasks {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskQueuedExpired(source, runtimeMode)
	}
}

func (s *TaskService) CaptureLeaseExpiredTasks(ctx context.Context, tasks []db.AgentTaskQueue) {
	if s.Metrics == nil {
		return
	}
	for _, task := range tasks {
		source, _, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskLeaseExpired(source)
	}
}

func (s *TaskService) cachedTaskAnalyticsContext(task db.AgentTaskQueue) (analytics.TaskContext, bool) {
	key := taskAnalyticsContextKey(task)
	if key == "" {
		return analytics.TaskContext{}, false
	}
	s.analyticsContextMu.Lock()
	defer s.analyticsContextMu.Unlock()
	if s.analyticsContextCache == nil {
		return analytics.TaskContext{}, false
	}
	tc, ok := s.analyticsContextCache[key]
	return tc, ok
}

func (s *TaskService) storeTaskAnalyticsContext(task db.AgentTaskQueue, tc analytics.TaskContext) {
	if tc.WorkspaceID == "" {
		return
	}
	key := taskAnalyticsContextKey(task)
	if key == "" {
		return
	}
	s.analyticsContextMu.Lock()
	defer s.analyticsContextMu.Unlock()
	if s.analyticsContextCache == nil {
		s.analyticsContextCache = make(map[string]analytics.TaskContext)
	}
	if _, ok := s.analyticsContextCache[key]; !ok {
		s.analyticsContextOrder = append(s.analyticsContextOrder, key)
		if len(s.analyticsContextOrder) > taskAnalyticsContextCacheMax {
			oldest := s.analyticsContextOrder[0]
			s.analyticsContextOrder = s.analyticsContextOrder[1:]
			delete(s.analyticsContextCache, oldest)
		}
	}
	s.analyticsContextCache[key] = tc
}

func taskAnalyticsContextKey(task db.AgentTaskQueue) string {
	taskID := util.UUIDToString(task.ID)
	if taskID == "" {
		return ""
	}
	return strings.Join([]string{
		taskID,
		util.UUIDToString(task.RuntimeID),
		util.UUIDToString(task.IssueID),
		util.UUIDToString(task.ChatSessionID),
		util.UUIDToString(task.AutopilotRunID),
	}, "|")
}

func (s *TaskService) taskMetricsContext(ctx context.Context, task db.AgentTaskQueue) (source, runtimeMode, provider string) {
	tc := s.taskAnalyticsContext(ctx, task)
	source = "other"
	switch {
	case task.ChatSessionID.Valid:
		source = "chat"
	case task.IssueID.Valid:
		if tc.Source == analytics.SourceAutopilot {
			source = "autopilot_issue"
		} else {
			source = "issue"
		}
	case task.AutopilotRunID.Valid:
		source = "autopilot"
	default:
		if _, ok := s.parseQuickCreateContext(task); ok {
			source = "quick_create"
		} else if tc.Source != "" {
			source = tc.Source
		}
	}
	return source, tc.RuntimeMode, tc.Provider
}

func (s *TaskService) taskAnalyticsContext(ctx context.Context, task db.AgentTaskQueue) analytics.TaskContext {
	if tc, ok := s.cachedTaskAnalyticsContext(task); ok {
		return tc
	}
	tc := analytics.TaskContext{
		AgentID: util.UUIDToString(task.AgentID),
		TaskID:  util.UUIDToString(task.ID),
		Source:  analytics.SourceManual,
	}
	if task.IssueID.Valid {
		tc.IssueID = util.UUIDToString(task.IssueID)
	}
	if task.ChatSessionID.Valid {
		tc.ChatSessionID = util.UUIDToString(task.ChatSessionID)
		tc.Source = analytics.SourceChat
	}
	if task.AutopilotRunID.Valid {
		tc.AutopilotRunID = util.UUIDToString(task.AutopilotRunID)
		tc.Source = analytics.SourceAutopilot
	}

	if task.RuntimeID.Valid {
		if rt, err := s.Queries.GetAgentRuntime(ctx, task.RuntimeID); err == nil {
			tc.WorkspaceID = util.UUIDToString(rt.WorkspaceID)
			tc.RuntimeMode = rt.RuntimeMode
			tc.Provider = rt.Provider
		}
	}
	if tc.WorkspaceID == "" || tc.RuntimeMode == "" {
		if agent, err := s.Queries.GetAgent(ctx, task.AgentID); err == nil {
			if tc.WorkspaceID == "" {
				tc.WorkspaceID = util.UUIDToString(agent.WorkspaceID)
			}
			if tc.RuntimeMode == "" {
				tc.RuntimeMode = agent.RuntimeMode
			}
		}
	}

	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			tc.WorkspaceID = util.UUIDToString(issue.WorkspaceID)
			if issue.CreatorType == "member" {
				tc.UserID = util.UUIDToString(issue.CreatorID)
			}
			if issue.OriginType.Valid {
				switch issue.OriginType.String {
				case "autopilot":
					tc.Source = analytics.SourceAutopilot
					if ap, err := s.Queries.GetAutopilot(ctx, issue.OriginID); err == nil {
						if ap.CreatedByType == "member" {
							tc.UserID = util.UUIDToString(ap.CreatedByID)
						}
					}
				case "quick_create":
					tc.Source = analytics.SourceManual
				}
			}
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			tc.WorkspaceID = util.UUIDToString(cs.WorkspaceID)
			tc.UserID = util.UUIDToString(cs.CreatorID)
		}
	}
	if task.AutopilotRunID.Valid {
		if run, err := s.Queries.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
			if ap, err := s.Queries.GetAutopilot(ctx, run.AutopilotID); err == nil {
				tc.WorkspaceID = util.UUIDToString(ap.WorkspaceID)
				if ap.CreatedByType == "member" {
					tc.UserID = util.UUIDToString(ap.CreatedByID)
				}
			}
		}
	}
	if qc, ok := s.parseQuickCreateContext(task); ok {
		tc.WorkspaceID = qc.WorkspaceID
		tc.UserID = qc.RequesterID
		tc.Source = analytics.SourceManual
	}
	s.storeTaskAnalyticsContext(task, tc)
	return tc
}

func taskQueueWaitSeconds(task db.AgentTaskQueue) float64 {
	return durationSeconds(task.CreatedAt, task.DispatchedAt)
}

func taskRunSeconds(task db.AgentTaskQueue) float64 {
	return durationSeconds(task.StartedAt, task.CompletedAt)
}

func taskTotalSeconds(task db.AgentTaskQueue) float64 {
	return durationSeconds(task.CreatedAt, task.CompletedAt)
}

func durationSeconds(start, end pgtype.Timestamptz) float64 {
	if !start.Valid || !end.Valid {
		return -1
	}
	seconds := end.Time.Sub(start.Time).Seconds()
	if seconds < 0 {
		return 0
	}
	return seconds
}

func taskFailureReason(task db.AgentTaskQueue) string {
	if task.FailureReason.Valid && task.FailureReason.String != "" {
		return task.FailureReason.String
	}
	return "agent_error"
}

func taskErrorType(reason string) string {
	switch reason {
	case "runtime_offline", "runtime_recovery":
		return "runtime"
	case "timeout", "codex_semantic_inactivity":
		return "timeout"
	case "iteration_limit", "agent_fallback_message":
		return "agent_output"
	case "cancelled", "user_cancelled":
		return "cancelled"
	default:
		return "agent_error"
	}
}

// EnqueueTaskForIssue creates a queued task for an agent-assigned issue.
// No context snapshot is stored — the agent fetches all data it needs at
// runtime via the agora CLI.
func (s *TaskService) EnqueueTaskForIssue(ctx context.Context, issue db.Issue, triggerCommentID ...pgtype.UUID) (db.AgentTaskQueue, error) {
	var commentID pgtype.UUID
	if len(triggerCommentID) > 0 {
		commentID = triggerCommentID[0]
	}
	return s.enqueueIssueTask(ctx, issue, commentID, false, AgentRunModeAuto)
}

// EnqueueTaskForIssueWithRunMode is the comment-composer variant: the selected
// mode is stored on this task only and never mutates the issue or agent.
func (s *TaskService) EnqueueTaskForIssueWithRunMode(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, runMode string) (db.AgentTaskQueue, error) {
	return s.enqueueIssueTask(ctx, issue, triggerCommentID, false, runMode)
}

// enqueueIssueTask is the shared implementation behind EnqueueTaskForIssue
// and the manual rerun path. forceFreshSession=true marks the task so the
// daemon claim handler skips the (agent_id, issue_id) resume lookup — the
// user already judged the prior output bad, a fresh agent session is the
// expected behavior.
func (s *TaskService) enqueueIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, forceFreshSession bool, runMode string) (db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "issue has no assignee")
		return db.AgentTaskQueue{}, fmt.Errorf("issue has no assignee")
	}

	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agent.ID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "agent has no runtime")
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:           issue.AssigneeID,
		RuntimeID:         agent.RuntimeID,
		IssueID:           issue.ID,
		Priority:          priorityToInt(issue.Priority),
		TriggerCommentID:  triggerCommentID,
		TriggerSummary:    s.buildCommentTriggerSummary(ctx, triggerCommentID),
		ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		RunMode:           pgtype.Text{String: runMode, Valid: runMode != ""},
	})
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	// Auto-tier the issue by size on first work so per-task model tiering
	// (applyIssueCostTier in the claim handler) can run a small task on a cheap
	// model without a human tagging it. Runs before the daemon is notified so
	// the tier label is in place when the claim resolves the model. Best-effort:
	// it never blocks the enqueue.
	s.maybeAutoTierIssue(ctx, issue)

	slog.Info("task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issue.ID),
		"agent_id", util.UUIDToString(issue.AssigneeID),
		"force_fresh_session", forceFreshSession,
	)
	// Order matters: broadcast first, notify daemon second. notifyTaskAvailable
	// kicks an in-process channel that the daemon picks up over HTTP and
	// claims; the claim path then emits its own task:dispatch. Doing the
	// queued broadcast afterwards risks the dispatch event reaching clients
	// before the queued one (rare but unsafe-by-construction). Publishing
	// in the desired observe-order makes correctness independent of timing.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// EnqueueTaskForMention creates a queued task for a mentioned agent on an issue.
// Unlike EnqueueTaskForIssue, this takes an explicit agent ID rather than
// deriving it from the issue assignee.
func (s *TaskService) EnqueueTaskForMention(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, false, false, pgtype.Text{}, pgtype.Text{}, pgtype.UUID{}, AgentRunModeAuto)
}

// EnqueueTaskForMentionWithRunMode stores a human-selected mode on this one
// mentioned-agent task. It is intentionally separate from agent configuration.
func (s *TaskService) EnqueueTaskForMentionWithRunMode(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, runMode string) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, false, false, pgtype.Text{}, pgtype.Text{}, pgtype.UUID{}, runMode)
}

// EnqueueTaskForMentionWithModel is EnqueueTaskForMention with a per-task model
// override (agent_task_queue.model_override), honored at claim in
// ClaimTaskByRuntime: it wins over the agent's configured model but loses to
// issue cost-tier labels. Used by knowledge-capture to escalate a large-thread
// distillation from the synthesizer's cheap default model to a bigger one.
func (s *TaskService) EnqueueTaskForMentionWithModel(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, modelOverride pgtype.Text) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, false, false, modelOverride, pgtype.Text{}, pgtype.UUID{}, AgentRunModeAuto)
}

// EnqueueOrchestrationTask dispatches one persisted orchestration step through
// the normal issue-task queue. The step id is stored on the task at INSERT
// time, so even a very fast daemon completion can be reconciled without a
// task-created/step-linked race.
func (s *TaskService) EnqueueOrchestrationTask(ctx context.Context, issue db.Issue, agentID, stepID pgtype.UUID, modelOverride, thinkingLevelOverride pgtype.Text) (db.AgentTaskQueue, error) {
	// Orchestration is not a manual rerun. A waiting_input continuation or an
	// infrastructure retry should keep the provider conversation when the
	// claim/runtime safety checks can prove the lineage is compatible. Manual
	// issue reruns remain the only ordinary path that deliberately forces a
	// fresh session.
	return s.enqueueMentionTask(ctx, issue, agentID, pgtype.UUID{}, false, false, modelOverride, thinkingLevelOverride, stepID, AgentRunModeAuto)
}

// CreateOrchestrationTaskInTx inserts an orchestration task through the
// caller's transaction without publishing or waking a daemon. The handler uses
// this to commit step queueing, task creation, and step↔task linkage as one
// unit, then calls PublishTaskEnqueued only after commit.
func (s *TaskService) CreateOrchestrationTaskInTx(
	ctx context.Context,
	queries *db.Queries,
	issue db.Issue,
	agentID, stepID pgtype.UUID,
	modelOverride, thinkingLevelOverride pgtype.Text,
	forceFreshSession bool,
) (db.AgentTaskQueue, error) {
	txService := &TaskService{Queries: queries}
	return txService.createMentionTask(ctx, issue, agentID, pgtype.UUID{}, false, forceFreshSession, modelOverride, thinkingLevelOverride, stepID, AgentRunModeAuto, false)
}

// EnqueueTaskForSquadLeader is the leader-role variant of EnqueueTaskForMention.
// The resulting task carries is_leader_task=true so that downstream
// self-trigger guards can distinguish a comment posted while the agent was
// acting as the squad's leader (skip) from one posted while it was acting
// as a worker (do not skip). This matters for agents that are simultaneously
// the leader and a worker of the same squad — see migration 090.
func (s *TaskService) EnqueueTaskForSquadLeader(ctx context.Context, issue db.Issue, leaderID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(ctx, issue, leaderID, triggerCommentID, true, false, pgtype.Text{}, pgtype.Text{}, pgtype.UUID{}, AgentRunModeAuto)
}

// EnqueueTaskForSquadLeaderWithRunMode is the squad equivalent of the manual
// comment run control.
func (s *TaskService) EnqueueTaskForSquadLeaderWithRunMode(ctx context.Context, issue db.Issue, leaderID pgtype.UUID, triggerCommentID pgtype.UUID, runMode string) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(ctx, issue, leaderID, triggerCommentID, true, false, pgtype.Text{}, pgtype.Text{}, pgtype.UUID{}, runMode)
}

func (s *TaskService) enqueueMentionTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, isLeader bool, forceFreshSession bool, modelOverride, thinkingLevelOverride pgtype.Text, orchestrationStepID pgtype.UUID, runMode string) (db.AgentTaskQueue, error) {
	return s.createMentionTask(ctx, issue, agentID, triggerCommentID, isLeader, forceFreshSession, modelOverride, thinkingLevelOverride, orchestrationStepID, runMode, true)
}

func (s *TaskService) createMentionTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, isLeader bool, forceFreshSession bool, modelOverride, thinkingLevelOverride pgtype.Text, orchestrationStepID pgtype.UUID, runMode string, publish bool) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		slog.Error("mention task enqueue failed: agent not found", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("mention task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("mention task enqueue failed: agent has no runtime", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:               agentID,
		RuntimeID:             agent.RuntimeID,
		IssueID:               issue.ID,
		Priority:              priorityToInt(issue.Priority),
		TriggerCommentID:      triggerCommentID,
		TriggerSummary:        s.buildCommentTriggerSummary(ctx, triggerCommentID),
		IsLeaderTask:          pgtype.Bool{Bool: isLeader, Valid: isLeader},
		ForceFreshSession:     pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		ModelOverride:         modelOverride,
		ThinkingLevelOverride: thinkingLevelOverride,
		OrchestrationStepID:   orchestrationStepID,
		RunMode:               pgtype.Text{String: runMode, Valid: runMode != ""},
	})
	if err != nil {
		slog.Error("mention task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	// Dev-runtime pinning (daemon-per-dev): a QA-squad mention on an issue
	// whose developer serves the project locally executes on the dev's own
	// daemon. Best-effort; no-op unless labs.qa_dev_runtimes is on.
	task = s.maybePinTaskToDevRuntime(ctx, issue, agent, task, publish)

	if publish {
		slog.Info("mention task enqueued", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "is_leader_task", isLeader)
		// See EnqueueTaskForIssue for ordering rationale.
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.NotifyTaskEnqueued(ctx, task)
	}
	return task, nil
}

// QuickCreateContext is the JSON payload stored on a quick-create task's
// context column. The daemon detects this variant via Type == "quick_create"
// and switches to the quick-create prompt template; the completion path
// uses RequesterID + WorkspaceID to write the inbox notification.
//
// ProjectID is the optional project the user picked in the modal. When
// non-empty the daemon claim handler resolves the project's title +
// resources, and the prompt template instructs the agent to pass
// `--project <uuid>` so the new issue lands in that project.
//
// SquadID is non-empty when the user picked a squad (rather than an agent)
// in the modal. The task is still enqueued against the squad's leader
// agent (Queries.CreateQuickCreateTask is agent-scoped); SquadID is the
// hint the daemon claim handler uses to layer the squad-leader briefing
// onto the agent's Instructions, matching the behavior of issue-bound
// tasks assigned to the squad.
type QuickCreateContext struct {
	Type          string   `json:"type"`
	Prompt        string   `json:"prompt"`
	RequesterID   string   `json:"requester_id"`
	WorkspaceID   string   `json:"workspace_id"`
	ProjectID     string   `json:"project_id,omitempty"`
	SquadID       string   `json:"squad_id,omitempty"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	// ParentIssueID is the optional UUID of the parent issue the new issue
	// should be filed under. Set when the user opens the modal from "Add
	// sub issue" on an existing issue; the daemon claim handler resolves the
	// parent's identifier and the prompt template instructs the agent to
	// pass `--parent <uuid>` so the sub-issue relationship is preserved
	// across the manual→agent mode flip.
	ParentIssueID string `json:"parent_issue_id,omitempty"`
}

// QuickCreateContextType marks a task as a quick-create job.
const QuickCreateContextType = "quick_create"

// EnqueueQuickCreateTask creates a queued task that has no issue / chat /
// autopilot link — the user's natural-language prompt is stored in the
// task's context JSONB and the agent is expected to translate it into a
// `agora issue create` call. Pre-validates that the agent is reachable
// (not archived, has a runtime) so the API can reject up-front rather than
// queue a task no one will ever claim.
//
// projectID is optional (zero-valued pgtype.UUID when the user didn't pick
// one). The handler is responsible for validating it belongs to the same
// workspace before passing it in.
//
// squadID is non-empty (Valid) when the user picked a squad as the actor.
// The handler has already resolved it to the squad's leader agent for
// agentID; the squadID hint is stamped into the task context so the daemon
// claim handler can inject the squad-leader briefing on dispatch.
//
// parentIssueID is optional (zero-valued pgtype.UUID when the user didn't
// open the modal from "Add sub issue"). The handler is responsible for
// validating it belongs to the same workspace before passing it in.
func (s *TaskService) EnqueueQuickCreateTask(ctx context.Context, workspaceID, requesterID pgtype.UUID, agentID, squadID pgtype.UUID, prompt string, projectID, parentIssueID pgtype.UUID, attachmentIDs []pgtype.UUID) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	payload := QuickCreateContext{
		Type:        QuickCreateContextType,
		Prompt:      prompt,
		RequesterID: util.UUIDToString(requesterID),
		WorkspaceID: util.UUIDToString(workspaceID),
	}
	if projectID.Valid {
		payload.ProjectID = util.UUIDToString(projectID)
	}
	if squadID.Valid {
		payload.SquadID = util.UUIDToString(squadID)
	}
	if parentIssueID.Valid {
		payload.ParentIssueID = util.UUIDToString(parentIssueID)
	}
	if len(attachmentIDs) > 0 {
		payload.AttachmentIDs = make([]string, 0, len(attachmentIDs))
		for _, id := range attachmentIDs {
			if id.Valid {
				payload.AttachmentIDs = append(payload.AttachmentIDs, util.UUIDToString(id))
			}
		}
	}
	contextJSON, err := json.Marshal(payload)
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("marshal quick-create context: %w", err)
	}

	task, err := s.Queries.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{
		AgentID:   agentID,
		RuntimeID: agent.RuntimeID,
		Priority:  priorityToInt("high"),
		Context:   contextJSON,
	})
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("create quick-create task: %w", err)
	}

	slog.Info("quick-create task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"agent_id", util.UUIDToString(agentID),
		"squad_id", payload.SquadID,
		"requester_id", util.UUIDToString(requesterID),
		"workspace_id", util.UUIDToString(workspaceID),
		"project_id", payload.ProjectID,
		"parent_issue_id", payload.ParentIssueID,
	)
	// Match every other Enqueue* path: kick the daemon WS so the task
	// gets claimed promptly instead of waiting for the next 30 s poll
	// cycle. Without this the user perceives "quick create never
	// triggered" because the modal closes immediately and the task
	// sits in 'queued' until the next sleepWithContextOrWakeup tick.
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// ErrChatTaskAgentArchived signals that EnqueueChatTask refused to
// queue work because the destination agent has been archived. This
// is a productizable state — surface it to the user as "this agent
// has been archived" rather than retrying.
var ErrChatTaskAgentArchived = errors.New("chat task: agent archived")

// ErrChatTaskAgentNoRuntime signals that EnqueueChatTask refused to
// queue work because the agent has never been associated with a
// runtime (agent.runtime_id IS NULL). This is the "agent has no
// daemon configured" case — productizable as "agent offline".
//
// IMPORTANT: this is NOT the same as "the daemon is currently
// disconnected". When agent.runtime_id IS set, EnqueueChatTask
// enqueues the task and the daemon claims it on next online; that
// path returns a task row, not this error.
var ErrChatTaskAgentNoRuntime = errors.New("chat task: agent has no runtime")

// EnqueueChatTask creates a queued task for a chat session.
// Unlike issue tasks, chat tasks have no issue_id.
//
// Errors split into two layers:
//
//   - Productizable rejections (agent archived, no runtime) return
//     the sentinel errors above. Callers (e.g. the Lark dispatcher)
//     can errors.Is them to decide a user-visible outcome.
//
//   - Infrastructure failures (DB load / insert errors) are wrapped
//     as ordinary errors. The caller should treat them as retryable
//     or page-worthy, NOT as user-facing state.
//
// initiatorUserID is the user who actually sent the triggering message — the
// real requester behind this run. Callers pass it explicitly because
// chat_session.creator_id is not a reliable source: Lark group sessions set the
// creator to the installer, not the sender (see the lark dispatcher). Web chat
// passes the request user; the lark dispatcher passes the inbound sender of the
// latest message in the silence window. Stored on the task so the daemon brief
// can attribute the run to the right person. See MUL-2645.
func (s *TaskService) EnqueueChatTask(ctx context.Context, chatSession db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, chatSession.AgentID)
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, ErrChatTaskAgentArchived
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, ErrChatTaskAgentNoRuntime
	}

	task, err := s.Queries.CreateChatTask(ctx, db.CreateChatTaskParams{
		AgentID:         chatSession.AgentID,
		RuntimeID:       agent.RuntimeID,
		Priority:        2, // medium priority for chat
		ChatSessionID:   chatSession.ID,
		InitiatorUserID: initiatorUserID,
	})
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create chat task: %w", err)
	}

	slog.Info("chat task enqueued", "task_id", util.UUIDToString(task.ID), "chat_session_id", util.UUIDToString(chatSession.ID), "agent_id", util.UUIDToString(chatSession.AgentID))
	// See EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// CancelTasksForIssue cancels every active task on the issue, reconciles each
// affected agent's status, and broadcasts task:cancelled events so frontends
// clear their live cards.
//
// Before #1587 this path was "cancel rows and return" — issue-status flips
// (e.g. user marks the issue `done` or `cancelled` while a task is still
// running) left the agent stuck at status="working" indefinitely, requiring a
// manual `agora agent update <id> --status idle` to unwedge. Matches the
// pattern already used by CancelTask and RerunIssue.
func (s *TaskService) CancelTasksForIssue(ctx context.Context, issueID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByIssue(ctx, issueID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// CancelTasksForAgent cancels every active task belonging to an agent
// (queued + dispatched + running), reconciles the agent's status, and
// broadcasts task:cancelled events. Used by the agent-level "Cancel all
// tasks" action — same shape as CancelTasksForIssue but scoped on agent_id.
//
// Returns the cancelled rows so callers can report counts / log them.
func (s *TaskService) CancelTasksForAgent(ctx context.Context, agentID pgtype.UUID) ([]db.AgentTaskQueue, error) {
	cancelled, err := s.Queries.CancelAgentTasksByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	// Reconcile once after the loop — agent transitions from
	// working→available based on remaining task counts, no need to call
	// per row (the rows we just cancelled all belong to the same agent).
	s.ReconcileAgentStatus(ctx, agentID)
	return cancelled, nil
}

// CancelTasksByTriggerComment cancels active tasks whose trigger is the given
// comment. Called from DeleteComment so an agent does not run with the
// now-deleted content already embedded in its prompt. Must be invoked BEFORE
// the comment row is deleted because the FK ON DELETE SET NULL would
// otherwise nullify trigger_comment_id and we'd lose the ability to find
// the affected tasks.
func (s *TaskService) CancelTasksByTriggerComment(ctx context.Context, commentID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByTriggerComment(ctx, commentID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// BroadcastCancelledTasks reconciles each affected agent's status and emits
// task:cancelled for every row. Callers must invoke this AFTER committing the
// cancellation so subscribers don't observe a "cancelled" event for a row
// that the tx might still roll back.
func (s *TaskService) BroadcastCancelledTasks(ctx context.Context, cancelled []db.AgentTaskQueue) {
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
}

func (s *TaskService) CaptureCancelledTasks(ctx context.Context, cancelled []db.AgentTaskQueue) {
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
	}
}

type CancelledChatMessageResult struct {
	ChatSessionID  string
	MessageID      string
	Content        string
	RestoreToInput bool
}

type CancelTaskResult struct {
	Task                 db.AgentTaskQueue
	CancelledChatMessage *CancelledChatMessageResult
}

// CancelTask cancels a single task by ID. It broadcasts a task:cancelled event
// so frontends can update immediately.
func (s *TaskService) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	result, err := s.CancelTaskWithResult(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return &result.Task, nil
}

// CancelTaskWithResult cancels a single task and returns any chat-specific
// cleanup result needed by user-facing callers.
func (s *TaskService) CancelTaskWithResult(ctx context.Context, taskID pgtype.UUID) (*CancelTaskResult, error) {
	task, err := s.Queries.CancelAgentTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("cancel task: %w", err)
		}
		return &CancelTaskResult{Task: existing}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}

	slog.Info("task cancelled", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskCancelled(ctx, task)
	cancelledChatMessage := s.finalizeCancelledChatMessage(ctx, task)

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast cancellation as a task:failed event so frontends clear the live card
	if task.IssueID.Valid && s.OnOrchestrationTaskTerminal != nil {
		if err := s.OnOrchestrationTaskTerminal(ctx, task); err != nil {
			slog.Error("advance orchestration after task cancellation failed", "task_id", util.UUIDToString(task.ID), "error", err)
		}
	}
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)

	return &CancelTaskResult{
		Task:                 task,
		CancelledChatMessage: cancelledChatMessage,
	}, nil
}

func (s *TaskService) finalizeCancelledChatMessage(ctx context.Context, task db.AgentTaskQueue) *CancelledChatMessageResult {
	if !task.ChatSessionID.Valid {
		return nil
	}
	var cancelled *CancelledChatMessageResult
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		messages, err := qtx.ListTaskMessages(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("list cancelled chat task messages: %w", err)
		}
		if len(messages) == 0 {
			deleted, err := qtx.DeleteUserChatMessageByTask(ctx, task.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("delete empty cancelled chat user message: %w", err)
			}
			cancelled = &CancelledChatMessageResult{
				ChatSessionID:  util.UUIDToString(deleted.ChatSessionID),
				MessageID:      util.UUIDToString(deleted.ID),
				Content:        deleted.Content,
				RestoreToInput: true,
			}
			return nil
		}
		if _, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: task.ChatSessionID,
			Role:          "assistant",
			Content:       "Stopped.",
			TaskID:        task.ID,
			ElapsedMs:     computeChatElapsedMs(task),
		}); err != nil {
			return fmt.Errorf("create cancelled chat message: %w", err)
		}
		return nil
	}); err != nil {
		slog.Error("failed to finalize cancelled chat message",
			"task_id", util.UUIDToString(task.ID),
			"chat_session_id", util.UUIDToString(task.ChatSessionID),
			"error", err,
		)
		return nil
	}
	return cancelled
}

// ClaimTaskForRuntime atomically claims the next runnable task for exactly one
// runtime while respecting each agent's max_concurrent_tasks limit. Runtime
// routing, capacity admission, task selection, and dispatch transition live in
// one SQL statement; there is no agent-global count-then-claim window.
//
// Empty-claim fast path: when EmptyClaim is configured and a recent
// check verified the runtime had no queued tasks, returns immediately
// without touching Postgres. The cache is invalidated synchronously on
// every enqueue (notifyTaskAvailable), so a queued task becomes
// claimable on the next call rather than waiting for the TTL.
func (s *TaskService) ClaimTaskForRuntime(ctx context.Context, runtimeID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	var (
		outcome                                  = "no_task"
		claimMs, pendingCheckMs, statusMs, busMs int64
		claimedFlag                              bool
	)
	defer func() {
		totalMs := time.Since(start).Milliseconds()
		if totalMs < 300 {
			return
		}
		slog.Info("claim_for_runtime slow",
			"runtime_id", util.UUIDToString(runtimeID),
			"outcome", outcome,
			"total_ms", totalMs,
			"claim_ms", claimMs,
			"pending_check_ms", pendingCheckMs,
			"update_status_ms", statusMs,
			"dispatch_ms", busMs,
			"claimed", claimedFlag,
		)
	}()

	runtimeKey := util.UUIDToString(runtimeID)
	// Check this before EmptyClaim: a lost claim response moves the task out of
	// `queued`, so the empty-queued cache cannot represent recoverability.
	stale, err := s.Queries.ReclaimStaleDispatchedTaskForRuntime(ctx, db.ReclaimStaleDispatchedTaskForRuntimeParams{
		RuntimeID:         runtimeID,
		ClaimRecoverySecs: claimResponseRecoveryWindow.Seconds(),
	})
	if err == nil {
		outcome = "reclaimed_dispatched"
		claimedFlag = true
		slog.Info("stale dispatched task reclaimed",
			"task_id", util.UUIDToString(stale.ID),
			"runtime_id", runtimeKey,
			"agent_id", util.UUIDToString(stale.AgentID),
		)
		return &stale, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		outcome = "error_reclaim_dispatched"
		return nil, fmt.Errorf("reclaim stale dispatched task: %w", err)
	}

	if s.EmptyClaim.IsEmpty(ctx, runtimeKey) {
		outcome = "empty_cache_hit"
		return nil, nil
	}

	// Sample the invalidation version BEFORE the SELECT. If a
	// concurrent enqueue Bumps between this read and the post-SELECT
	// MarkEmpty, the next IsEmpty will see the empty key tagged with
	// a stale version and reject it — closing the race that would
	// otherwise stall the just-queued task until the empty key's TTL
	// expired.
	preSelectVersion := s.EmptyClaim.CurrentVersion(ctx, runtimeKey)

	t0 := time.Now()
	task, err := s.Queries.ClaimAgentTaskForRuntime(ctx, runtimeID)
	claimMs = time.Since(t0).Milliseconds()
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			outcome = "error_claim"
			return nil, fmt.Errorf("claim runtime task: %w", err)
		}

		// A no-row result can mean either a truly empty runtime or queued work
		// whose agent is at capacity / row-locked by another poll. Cache only
		// the former: caching a capacity miss would strand queued work after the
		// active task completes because completion does not bump the enqueue
		// invalidation version.
		t0 = time.Now()
		pending, pendingErr := s.Queries.ListPendingTasksByRuntime(ctx, runtimeID)
		pendingCheckMs = time.Since(t0).Milliseconds()
		if pendingErr != nil {
			outcome = "error_check_pending"
			return nil, fmt.Errorf("check pending runtime tasks: %w", pendingErr)
		}
		hasQueued := false
		for _, pendingTask := range pending {
			if pendingTask.Status == "queued" {
				hasQueued = true
				break
			}
		}
		if !hasQueued {
			s.EmptyClaim.MarkEmpty(ctx, runtimeKey, preSelectVersion)
			outcome = "empty_db"
		} else {
			outcome = "no_capacity"
		}
		return nil, nil
	}

	claimedFlag = true
	outcome = "claimed"
	slog.Info("task claimed",
		"task_id", util.UUIDToString(task.ID),
		"runtime_id", runtimeKey,
		"agent_id", util.UUIDToString(task.AgentID),
	)
	s.captureTaskDispatched(ctx, task)

	// Refresh agent status from active tasks. This avoids a stale unconditional
	// working write racing after a just-cancelled claim.
	t0 = time.Now()
	s.ReconcileAgentStatus(ctx, task.AgentID)
	statusMs = time.Since(t0).Milliseconds()

	// Broadcast task:dispatch. ResolveTaskWorkspaceID inside this path can
	// re-query issue/chat_session/autopilot_run, so it can also be a real
	// contributor to claim latency.
	t0 = time.Now()
	s.broadcastTaskDispatch(ctx, task)
	busMs = time.Since(t0).Milliseconds()

	return &task, nil
}

// StartTask transitions a dispatched task to running.
// Issue status is NOT changed here — the agent manages it via the CLI.
func (s *TaskService) StartTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.StartAgentTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("start task: %w", err)
	}

	slog.Info("task started", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskStarted(ctx, task)
	// Tell every connected workspace WS client that this task transitioned
	// (dispatched | waiting_local_directory) → running. Without this, the
	// workspace-wide `agentTaskSnapshot` query only refreshes on the 30s
	// staleTime, so any UI that distinguishes "queued" from "running" (e.g.
	// the issue-card agent activity indicator) lags by up to half a minute
	// on the transition users care about most.
	s.broadcastTaskEvent(ctx, protocol.EventTaskRunning, task)
	return &task, nil
}

// MarkTaskWaitingLocalDirectory parks a dispatched task in the
// waiting_local_directory state while the daemon waits for another in-flight
// task to release the project_resource path lock. reason carries a short
// human-readable hint (typically the contested path) that the UI surfaces
// next to the status. Returns the updated row so the daemon can confirm the
// transition and so the broadcast carries the up-to-date snapshot.
func (s *TaskService) MarkTaskWaitingLocalDirectory(ctx context.Context, taskID pgtype.UUID, reason string) (*db.AgentTaskQueue, error) {
	reason = strings.TrimSpace(reason)
	task, err := s.Queries.MarkAgentTaskWaitingLocalDirectory(ctx, db.MarkAgentTaskWaitingLocalDirectoryParams{
		ID:         taskID,
		WaitReason: pgtype.Text{String: reason, Valid: reason != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("mark task waiting_local_directory: %w", err)
	}

	slog.Info("task waiting_local_directory",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(task.IssueID),
		"reason", reason,
	)
	s.broadcastTaskEvent(ctx, protocol.EventTaskWaitingLocalDirectory, task)
	return &task, nil
}

// CompleteTask marks a task as completed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// For chat tasks, CompleteAgentTask and the chat_session resume-pointer
// update run in a single transaction. This closes a race where the next
// queued chat message could be claimed in the window between the task
// flipping to 'completed' and chat_session.session_id being refreshed,
// causing the new task to resume against a stale (or NULL) session.
func (s *TaskService) CompleteTask(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir string) (*db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.CompleteAgentTask(ctx, db.CompleteAgentTaskParams{
			ID:        taskID,
			Result:    result,
			SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		if t.ChatSessionID.Valid {
			// Pin the chat_session's runtime_id alongside the session_id so the
			// next claim can apply the runtime-guard. Both fields move together:
			// when there's no session_id to record, leave runtime_id untouched
			// (NULL → COALESCE keeps the existing value).
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = t.RuntimeID
			}
			// COALESCE in SQL guarantees empty inputs don't wipe the
			// existing resume pointer; we still surface DB errors.
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		return nil
	}); err != nil {
		// When parallel agents race, a task may already be completed,
		// cancelled, or failed by the time this call runs. The UPDATE
		// … WHERE status = 'running' returns no rows in that case.
		// Treat it as an idempotent success — same pattern as CancelTask.
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("complete task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				if existing.Status == "completed" {
					// Match the normal path for every issue task, not only a task
					// already linked to a step. Ordinary issue tasks are also wake-up
					// edges for DAG work deferred behind the same agent queue slot.
					if existing.IssueID.Valid && s.OnOrchestrationTaskTerminal != nil {
						if callbackErr := s.OnOrchestrationTaskTerminal(ctx, existing); callbackErr != nil {
							return nil, fmt.Errorf("%w: %v", ErrOrchestrationAdvance, callbackErr)
						}
					}
					// The original request may have committed the terminal row and
					// died while advancing orchestration, before this invalidation.
					// Re-emitting is safe and closes that realtime crash window.
					s.broadcastTaskEvent(ctx, protocol.EventTaskCompleted, existing)
				}
				return &existing, nil
			}
			slog.Warn("complete task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("complete task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("complete task: %w", err)
	}

	slog.Info("task completed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskCompleted(ctx, task)
	s.persistRunTrace(ctx, task)

	// Invariant: every completed issue task must have at least one agent
	// comment on the issue, so the user always sees something when a run
	// ends. If the agent posted a comment during execution (result, progress
	// ping, or CLI reply), HasAgentCommentedSince returns true and we skip.
	// Otherwise, synthesize one from the final output. For comment-triggered
	// tasks, TriggerCommentID threads the fallback under the original comment;
	// for assignment-triggered tasks it is NULL and the fallback is top-level.
	// Chat tasks have no IssueID and are handled separately below.
	if task.IssueID.Valid {
		suppressNoActionComment, err := HasSquadLeaderNoActionEvaluationForTask(ctx, s.Queries, task)
		if err != nil {
			slog.Warn("checking squad leader no_action evaluation failed",
				"task_id", util.UUIDToString(task.ID),
				"issue_id", util.UUIDToString(task.IssueID),
				"agent_id", util.UUIDToString(task.AgentID),
				"error", err,
			)
		}
		agentCommented, _ := s.Queries.HasAgentCommentedSince(ctx, db.HasAgentCommentedSinceParams{
			IssueID:  task.IssueID,
			AuthorID: task.AgentID,
			Since:    task.StartedAt,
		})
		if !suppressNoActionComment && !agentCommented {
			var payload protocol.TaskCompletedPayload
			if err := json.Unmarshal(result, &payload); err == nil {
				if payload.Output != "" {
					// Match the CLI's --content / --description behavior: agents that
					// emit literal `\n` 4-char sequences (Python/JSON-style) get them
					// decoded into real newlines before the comment hits the DB. See
					// util.UnescapeBackslashEscapes for the exact contract.
					body := util.UnescapeBackslashEscapes(payload.Output)
					if task.TriggerCommentID.Valid && isTrivialDoneOutput(body) {
						slog.Warn("suppressing trivial comment-trigger fallback output",
							"task_id", util.UUIDToString(task.ID),
							"issue_id", util.UUIDToString(task.IssueID),
							"agent_id", util.UUIDToString(task.AgentID),
						)
					} else {
						// createAgentComment captures the structured blocks (test-runs /
						// test-cases / qa-result) from the posted body.
						s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(body), "comment", task.TriggerCommentID)
					}
				}
			}
		} else if agentCommented {
			// The fallback comment is suppressed because the agent already posted
			// during the run — but the authoritative structured output
			// (```test-runs``` / ```test-cases``` / ```qa-result```) lives in the
			// FINAL result, which interim progress comments don't carry. Without
			// this, a run_test_cases block emitted only in the final output is
			// never captured → test_run rows never recorded → base-suite
			// promotion never fires (task_b0f15704). Capture directly from the
			// final output; no comment is posted (the agent already did).
			var payload protocol.TaskCompletedPayload
			if err := json.Unmarshal(result, &payload); err == nil && strings.TrimSpace(payload.Output) != "" {
				s.captureStructuredResult(ctx, task.IssueID, task.AgentID, task.TriggerCommentID, util.UnescapeBackslashEscapes(payload.Output))
			}
		}
	}

	// Quick-create tasks: locate the issue the agent just created and push
	// an inbox confirmation to the requester. The agent has no issue / chat
	// link, so the regular completion paths above don't apply. We find the
	// new issue by querying for the most recent issue this agent created in
	// the requester's workspace since the task started — more robust than
	// parsing the agent's stdout for an identifier.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		s.notifyQuickCreateCompleted(ctx, task, qc)
	}

	// For chat tasks, save assistant reply and broadcast chat:done. The
	// resume pointer was already persisted inside the transaction above.
	if task.ChatSessionID.Valid {
		var assistantMsg *db.ChatMessage
		var payload protocol.TaskCompletedPayload
		if err := json.Unmarshal(result, &payload); err == nil && payload.Output != "" {
			// Same unescape as the issue-comment path above: literal `\n` from
			// agent stdout becomes a real newline so the chat panel renders
			// paragraph breaks instead of one wall of prose.
			body := util.UnescapeBackslashEscapes(payload.Output)
			row, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
				ChatSessionID: task.ChatSessionID,
				Role:          "assistant",
				Content:       redact.Text(body),
				TaskID:        task.ID,
				ElapsedMs:     computeChatElapsedMs(task),
			})
			if err != nil {
				slog.Error("failed to save assistant chat message", "task_id", util.UUIDToString(task.ID), "error", err)
			} else {
				assistantMsg = &row
				// Event-driven unread: stamp unread_since on the first unread
				// assistant message. No-op if the session already has unread.
				// If the user is actively viewing the session, the frontend's
				// auto-mark-read effect will clear this within a tick.
				if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
					slog.Warn("failed to set unread_since", "chat_session_id", util.UUIDToString(task.ChatSessionID), "error", err)
				}
			}
		}
		s.broadcastChatDone(ctx, task, assistantMsg)
	}

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	if task.IssueID.Valid && s.OnOrchestrationTaskTerminal != nil {
		if err := s.OnOrchestrationTaskTerminal(ctx, task); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrOrchestrationAdvance, err)
		}
	}
	// Broadcast after orchestration advancement so clients invalidating from
	// this event read the new step/run state rather than the just-finished one.
	s.broadcastTaskEvent(ctx, protocol.EventTaskCompleted, task)

	return &task, nil
}

// FailTask marks a task as failed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// sessionID/workDir are optional: when the agent established a real session
// before failing (e.g. crashed mid-conversation, was cancelled, or hit a
// tool error), the daemon should pass them so we can preserve the resume
// pointer on both the task row and the chat_session — otherwise the next
// chat turn would silently start a brand-new session and lose memory.
//
// failureReason is a coarse classifier consumed by the auto-retry path.
// Pass "" when unknown — the server runs the raw error text through
// taskfailure.Classify so the persisted failure_reason still lands in
// the canonical refined taxonomy rather than the legacy "agent_error"
// coarse bucket. Daemon callers that already produced a refined reason
// (via classifyPoisonedError, the timeout / runtime classifier, etc.)
// will have their value preserved untouched.
func (s *TaskService) FailTask(ctx context.Context, taskID pgtype.UUID, errMsg, sessionID, workDir, failureReason string) (*db.AgentTaskQueue, error) {
	// MUL-2946: synthesise a refined reason from the error text whenever the
	// caller didn't supply one. This is the last write-path guard against
	// "agent_error" coarse rows ending up in agent_task_queue.failure_reason
	// — every other path either provides a classified reason directly
	// (sweepers writing 'queued_expired' / 'runtime_offline' / 'timeout'
	// / 'runtime_recovery' via SQL) or runs the daemon's classifyPoisonedError
	// + taskfailure.Classify chain.
	if failureReason == "" {
		failureReason = taskfailure.Classify(errMsg).String()
	}
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.FailAgentTask(ctx, db.FailAgentTaskParams{
			ID:            taskID,
			Error:         pgtype.Text{String: errMsg, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			SessionID:     pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:       pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		// Keep resume-unsafe sessions on the task row for observability, but
		// do not promote them to the chat-level resume pointer.
		if t.ChatSessionID.Valid && !resumeUnsafeFailureReason(failureReason) {
			// Pin the chat_session's runtime_id alongside the session_id so the
			// next claim can apply the runtime-guard. Both fields move together:
			// when there's no session_id to record, leave runtime_id untouched
			// (NULL → COALESCE keeps the existing value).
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = t.RuntimeID
			}
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		return nil
	}); err != nil {
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("fail task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				if existing.Status == "failed" {
					if existing.IssueID.Valid && s.OnOrchestrationTaskTerminal != nil {
						if callbackErr := s.OnOrchestrationTaskTerminal(ctx, existing); callbackErr != nil {
							return nil, fmt.Errorf("%w: %v", ErrOrchestrationAdvance, callbackErr)
						}
					}
					s.broadcastTaskEvent(ctx, protocol.EventTaskFailed, existing)
				}
				return &existing, nil
			}
			slog.Warn("fail task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("fail task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("fail task: %w", err)
	}

	slog.Warn("task failed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID), "error", errMsg, "failure_reason", failureReason)
	s.captureTaskFailed(ctx, task)

	// Auto-retry eligible failures (orphan, timeout, runtime_offline,
	// runtime_recovery). The helper itself enforces attempt < max_attempts
	// and only triggers for issue/chat tasks.
	var retried *db.AgentTaskQueue
	if !task.OrchestrationStepID.Valid {
		retried, _ = s.MaybeRetryFailedTask(ctx, task)
	}
	// Provider usage/rate limit (weekly quota, exhausted 429): the same runtime
	// will keep failing, so instead of a same-runtime retry, route the task onto
	// the agent's configured fallback runtime. Treated like a retry below so the
	// generic failure comment / chat fallback is suppressed (failover posts its
	// own note).
	if retried == nil && !task.OrchestrationStepID.Valid {
		retried, _ = s.maybeFailoverToFallbackRuntime(ctx, task, failureReason)
	}

	// Skip the per-failure system comment when we'll immediately retry —
	// the new task will surface its own status to the user, and we don't
	// want to spam the issue with "task timed out" messages on every
	// daemon hiccup.
	if errMsg != "" && task.IssueID.Valid && retried == nil {
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(errMsg), "system", task.TriggerCommentID)
	}

	// Mirror the issue fallback for chat tasks: write an assistant
	// chat_message tagged with the daemon-reported failure_reason so the
	// conversation history shows what happened. Skip when auto-retry is
	// pending (the new attempt will write its own outcome) — same guard as
	// the issue path above.
	if task.ChatSessionID.Valid && retried == nil {
		if _, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: task.ChatSessionID,
			Role:          "assistant",
			Content:       redact.Text(errMsg),
			TaskID:        pgtype.UUID{Bytes: task.ID.Bytes, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			ElapsedMs:     computeChatElapsedMs(task),
		}); err != nil {
			slog.Error("failed to save failure chat message",
				"task_id", util.UUIDToString(task.ID),
				"chat_session_id", util.UUIDToString(task.ChatSessionID),
				"error", err)
		} else if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
			slog.Warn("failed to set unread_since on failure",
				"chat_session_id", util.UUIDToString(task.ChatSessionID),
				"error", err)
		}
	}

	// Quick-create tasks: push a failure inbox notification to the
	// requester so they can either retry or fall back to the advanced form
	// without losing their original prompt. Skipped when an auto-retry is
	// pending — the new attempt will write its own outcome.
	if retried == nil {
		if qc, ok := s.parseQuickCreateContext(task); ok {
			s.notifyQuickCreateFailed(ctx, task, qc, errMsg)
		}
	}
	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	if task.IssueID.Valid && s.OnOrchestrationTaskTerminal != nil {
		if err := s.OnOrchestrationTaskTerminal(ctx, task); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrOrchestrationAdvance, err)
		}
	}
	s.broadcastTaskEvent(ctx, protocol.EventTaskFailed, task)

	return &task, nil
}

// retryableReasons enumerates failure reasons that the auto-retry path is
// allowed to act on. Agent-side errors (compile failures, model rejections,
// etc.) are intentionally excluded — those are real problems that the user
// should see, not infrastructure flakiness.
var retryableReasons = map[string]bool{
	"runtime_offline":           true,
	"runtime_recovery":          true,
	"timeout":                   true,
	"codex_semantic_inactivity": true,
}

func resumeUnsafeFailureReason(reason string) bool {
	switch reason {
	// Keep in sync with GetLastTaskSession / GetLastChatTaskSession and
	// CreateRetryTask's fresh-session CASE WHEN.
	case "iteration_limit", "agent_fallback_message", "api_invalid_request", "codex_semantic_inactivity":
		return true
	default:
		return false
	}
}

// MaybeRetryFailedTask spawns a fresh queued attempt for a recently-failed
// task when the failure was infrastructure-shaped (daemon crash, runtime
// went offline, dispatch/run timeout) and the task hasn't exhausted its
// max_attempts budget. The child task inherits agent/runtime/issue/chat
// links and, for resume-safe failures, the parent's session_id/work_dir so
// the agent can resume the conversation when the backend supports it. Returns
// the new task, or nil when no retry was created.
//
// Autopilot tasks are NOT auto-retried here; the autopilot scheduler owns
// its own re-run cadence and we don't want to double-fire it.
func (s *TaskService) MaybeRetryFailedTask(ctx context.Context, parent db.AgentTaskQueue) (*db.AgentTaskQueue, error) {
	if parent.Status != "failed" {
		return nil, nil
	}
	// A persisted orchestration step owns its retry budget and must keep the
	// replacement task linked to orchestration_step_id. The generic clone query
	// intentionally does not copy that identity, so using it here would orphan
	// the step and run a second, invisible task beside the DAG.
	if parent.OrchestrationStepID.Valid {
		return nil, nil
	}
	reason := ""
	if parent.FailureReason.Valid {
		reason = parent.FailureReason.String
	}
	if !retryableReasons[reason] {
		return nil, nil
	}
	if parent.Attempt >= parent.MaxAttempts {
		slog.Info("task auto-retry skipped: budget exhausted",
			"task_id", util.UUIDToString(parent.ID),
			"attempt", parent.Attempt,
			"max_attempts", parent.MaxAttempts,
		)
		return nil, nil
	}
	if parent.AutopilotRunID.Valid {
		// Autopilot has its own retry semantics; do not double-trigger.
		return nil, nil
	}
	if !parent.IssueID.Valid && !parent.ChatSessionID.Valid {
		return nil, nil
	}

	// Idempotency guard for the duplicate-branch failure mode. If this task's
	// issue already has an in-flight PR (open or draft), the agent already
	// delivered its work and this "failure" is a post-delivery hang — the CLI
	// session wedged after committing/pushing and then tripped the idle
	// watchdog or got reclaimed by recover-orphans. Auto-retrying would start a
	// fresh session (the local work_dir/session is gone after a daemon restart)
	// that re-clones the repo and invents a SECOND branch for work already up
	// for review — the feat/persian-lang vs feat/persian-relocalize duplication
	// we hit. Only in-flight PRs suppress the retry, so a genuinely new
	// follow-up task on an issue whose prior PR already merged still recovers
	// normally.
	if parent.IssueID.Valid && s.issueHasInFlightPR(ctx, parent.IssueID) {
		slog.Info("task auto-retry skipped: issue already has an in-flight PR",
			"task_id", util.UUIDToString(parent.ID),
			"issue_id", util.UUIDToString(parent.IssueID),
			"reason", reason,
		)
		return nil, nil
	}

	child, err := s.Queries.CreateRetryTask(ctx, parent.ID)
	if err != nil {
		slog.Warn("task auto-retry failed",
			"parent_task_id", util.UUIDToString(parent.ID),
			"reason", reason,
			"error", err,
		)
		return nil, err
	}
	slog.Info("task auto-retry enqueued",
		"parent_task_id", util.UUIDToString(parent.ID),
		"child_task_id", util.UUIDToString(child.ID),
		"reason", reason,
		"attempt", child.Attempt,
		"max_attempts", child.MaxAttempts,
	)
	// Retry creates a fresh queued row, same status transition (∅ → queued)
	// as EnqueueTaskFor*. Broadcast queued first, then notify the daemon —
	// see EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, child)
	s.NotifyTaskEnqueued(ctx, child)
	return &child, nil
}

// maybeFailoverToFallbackRuntime re-dispatches a task onto the agent's
// configured fallback runtime when the PRIMARY runtime hit a provider USAGE/RATE
// LIMIT (a weekly/monthly quota, or an exhausted 429). Without it, one account's
// weekly limit dead-stops the agent — and the whole squad it sits in — until the
// limit resets. Returns the failover child when one was enqueued.
//
// Guards, bounded to a single failover: only provider-limit reasons; the agent
// must have a fallback runtime that is online and is NOT the runtime that just
// failed (so a task already running on the fallback never loops back to it);
// autopilot tasks are skipped (the scheduler owns their retry cadence); and an
// issue with an in-flight PR is left alone (work already delivered).
func (s *TaskService) maybeFailoverToFallbackRuntime(ctx context.Context, parent db.AgentTaskQueue, reason string) (*db.AgentTaskQueue, error) {
	// Fallback routing for a persisted step must be orchestrator-aware. The
	// generic failover clone does not carry orchestration_step_id and therefore
	// cannot safely own this task.
	if parent.OrchestrationStepID.Valid {
		return nil, nil
	}
	if reason != taskfailure.ReasonAgentProviderQuotaLimit.String() &&
		reason != taskfailure.ReasonAgentProviderCapacityOrRateLimit.String() {
		return nil, nil
	}
	if parent.AutopilotRunID.Valid {
		return nil, nil
	}
	if !parent.IssueID.Valid && !parent.ChatSessionID.Valid {
		return nil, nil
	}
	agent, err := s.Queries.GetAgent(ctx, parent.AgentID)
	if err != nil || !agent.FallbackRuntimeID.Valid {
		return nil, nil
	}
	// Already on the fallback (or fallback == primary) → don't loop back to it.
	if parent.RuntimeID.Valid && agent.FallbackRuntimeID.Bytes == parent.RuntimeID.Bytes {
		return nil, nil
	}
	fallback, err := s.Queries.GetAgentRuntime(ctx, agent.FallbackRuntimeID)
	if err != nil || fallback.Status != "online" {
		slog.Info("failover skipped: fallback runtime unavailable",
			"task_id", util.UUIDToString(parent.ID),
			"agent_id", util.UUIDToString(parent.AgentID))
		return nil, nil
	}
	if parent.IssueID.Valid && s.issueHasInFlightPR(ctx, parent.IssueID) {
		return nil, nil
	}

	child, err := s.Queries.CreateFailoverTask(ctx, db.CreateFailoverTaskParams{
		RuntimeID: agent.FallbackRuntimeID,
		ParentID:  parent.ID,
	})
	if err != nil {
		slog.Warn("failover enqueue failed",
			"parent_task_id", util.UUIDToString(parent.ID), "error", err)
		return nil, err
	}

	if parent.IssueID.Valid {
		s.createAgentComment(ctx, parent.IssueID, parent.AgentID,
			"↪️ Primary runtime hit a provider usage/rate limit — failed over to this agent's fallback runtime for this task.",
			"system", parent.TriggerCommentID)
	}
	slog.Info("task failed over to fallback runtime",
		"parent_task_id", util.UUIDToString(parent.ID),
		"child_task_id", util.UUIDToString(child.ID),
		"fallback_runtime_id", util.UUIDToString(agent.FallbackRuntimeID),
		"reason", reason)
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, child)
	s.NotifyTaskEnqueued(ctx, child)
	return &child, nil
}

// issueHasInFlightPR reports whether the issue has at least one open or draft
// pull request. It is the "work already delivered" signal that suppresses
// post-delivery churn from a hung agent session: an auto-retry that would fork
// a duplicate branch (MaybeRetryFailedTask) and a stuck-issue reset that would
// regress the issue to todo while a PR sits awaiting review (HandleFailedTasks).
// Fails open (returns false) on a query error so a transient read can never
// permanently wedge infra recovery — at worst it falls back to the prior
// behaviour for that one tick.
func (s *TaskService) issueHasInFlightPR(ctx context.Context, issueID pgtype.UUID) bool {
	agg, err := s.Queries.GetIssuePullRequestCloseAggregate(ctx, issueID)
	if err != nil {
		slog.Warn("in-flight PR check failed; assuming none",
			"issue_id", util.UUIDToString(issueID),
			"error", err,
		)
		return false
	}
	return agg.OpenCount > 0
}

// RerunIssue creates a fresh queued task for an agent on the issue. Used by
// the manual rerun endpoint.
//
// Target agent resolution:
//   - sourceTaskID Valid: rerun the agent that ran that task (and reuse its
//     leader/worker role). This is what the execution log retry button uses
//     so a per-row retry survives a subsequent assignee change and correctly
//     re-fires the squad worker or mention agent whose row was clicked. The
//     source task's trigger_comment_id is also inherited (when the caller
//     didn't pass one) so a per-row rerun of a comment- or mention-triggered
//     task stays comment-triggered — the daemon's buildCommentPrompt path
//     keys on TriggerCommentID, and losing it would degrade the rerun into
//     a generic issue run that no longer carries the original comment.
//   - sourceTaskID empty: fall back to the issue's current assignee (agent
//     or squad leader). This preserves the CLI / API contract for callers
//     that have an issue ID but no specific task to target.
//
// The new task is flagged force_fresh_session=true so the daemon starts a
// clean agent session instead of resuming the prior (agent_id, issue_id)
// session. A user clicking rerun has just judged the prior output bad —
// resuming the same conversation would replay the same poisoned state.
// Auto-retry of an orphaned mid-flight failure (HandleFailedTasks →
// MaybeRetryFailedTask → CreateRetryTask) does NOT take this path, so
// MUL-1128's mid-flight resume contract is preserved.
//
// Only tasks belonging to the target agent on this issue are cancelled.
// Tasks owned by other agents on the same issue (e.g. a parallel
// @-mention agent) are left alone — rerun must not collateral-cancel
// them.
func (s *TaskService) RerunIssue(ctx context.Context, issueID pgtype.UUID, sourceTaskID pgtype.UUID, triggerCommentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("load issue: %w", err)
	}

	// Determine the target agent for the rerun.
	var (
		agentID  pgtype.UUID
		isLeader bool
		runMode  = AgentRunModeAuto
	)
	if sourceTaskID.Valid {
		sourceTask, err := s.Queries.GetAgentTask(ctx, sourceTaskID)
		if err != nil {
			return nil, fmt.Errorf("load source task: %w", err)
		}
		if !sourceTask.IssueID.Valid || util.UUIDToString(sourceTask.IssueID) != util.UUIDToString(issueID) {
			return nil, fmt.Errorf("source task does not belong to this issue")
		}
		agentID = sourceTask.AgentID
		isLeader = sourceTask.IsLeaderTask
		if sourceTask.RunMode != "" {
			runMode = sourceTask.RunMode
		}
		// Inherit trigger provenance so a per-row rerun of a comment- or
		// mention-triggered task stays a comment-triggered task. Without
		// this the daemon's buildCommentPrompt path is skipped (it keys on
		// TriggerCommentID) and the rerun degrades into a generic issue
		// run that has lost the original comment context. Only override
		// when the caller didn't pass one explicitly.
		if !triggerCommentID.Valid && sourceTask.TriggerCommentID.Valid {
			triggerCommentID = sourceTask.TriggerCommentID
		}
	} else {
		switch {
		case issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid:
			agentID = issue.AssigneeID
		case issue.AssigneeType.String == "squad" && issue.AssigneeID.Valid:
			squad, err := s.Queries.GetSquad(ctx, issue.AssigneeID)
			if err != nil {
				return nil, fmt.Errorf("issue is assigned to a squad but squad not found")
			}
			agentID = squad.LeaderID
			isLeader = true
		default:
			return nil, fmt.Errorf("issue is not assigned to an agent or squad")
		}
	}

	// Cancel only the target agent's active/queued tasks on this issue.
	cancelled, err := s.Queries.CancelAgentTasksByIssueAndAgent(ctx, db.CancelAgentTasksByIssueAndAgentParams{
		IssueID: issueID,
		AgentID: agentID,
	})
	if err != nil {
		slog.Warn("rerun: cancel prior tasks failed",
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(agentID),
			"error", err,
		)
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}

	task, err := s.enqueueRerunTask(ctx, issue, agentID, triggerCommentID, isLeader, runMode)
	if err != nil {
		return nil, err
	}
	slog.Info("issue rerun enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issueID),
		"agent_id", util.UUIDToString(agentID),
		"source_task_id", util.UUIDToString(sourceTaskID),
		"is_leader", isLeader,
		"cancelled_prior", len(cancelled),
	)
	return &task, nil
}

// enqueueRerunTask enqueues a fresh task for the given agent on the issue.
// When the target agent is the issue's single-agent assignee we use the
// assignee-driven path (enqueueIssueTask) so the issue-assignee bookkeeping
// stays in sync; otherwise (squad member, prior assignee that has since been
// reassigned, mention agent) we use the mention path with the same
// force_fresh_session=true contract.
func (s *TaskService) enqueueRerunTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, isLeader bool, runMode string) (db.AgentTaskQueue, error) {
	if issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid &&
		util.UUIDToString(issue.AssigneeID) == util.UUIDToString(agentID) {
		return s.enqueueIssueTask(ctx, issue, triggerCommentID, true, runMode)
	}
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, isLeader, true, pgtype.Text{}, pgtype.Text{}, pgtype.UUID{}, runMode)
}

// HandleFailedTasks runs the post-failure side effects for a batch of
// freshly-failed tasks: optional auto-retry, task:failed event broadcast,
// agent status reconciliation, and (when an issue has no remaining active
// task and isn't being retried) resetting the issue back to todo so the
// daemon can pick it up again.
//
// All callers that surface a task as failed — sweepers, FailTask,
// recover-orphans — funnel through here so the same UI-consistency
// guarantees apply on every code path.
// maybeReTriggerSquadLeaderOnMemberFailure re-engages the squad leader when a
// non-leader (member) task fails on a squad-assigned issue. Without it, a member
// hitting a rate/weekly limit or crashing leaves the issue stalled — the leader
// that owns coordination is never told, so it can never route around the
// failure. We post a system note describing the failure (so the leader routes to
// a different member instead of re-dispatching the one that just failed) and
// enqueue a fresh leader task. Returns true when a leader task was enqueued.
//
// Loop guards: skips when the failed task is the leader's own, the issue is not
// squad-assigned, the failed agent IS the leader, the leader already has a
// pending task on the issue, or the leader is not runnable.
func (s *TaskService) maybeReTriggerSquadLeaderOnMemberFailure(ctx context.Context, task db.AgentTaskQueue, issue db.Issue, failureReason string) bool {
	if task.IsLeaderTask {
		return false
	}
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "squad" || !issue.AssigneeID.Valid {
		return false
	}
	// Issue already closed — re-engaging the leader would just post noise onto a
	// done/cancelled issue and spin up a pointless run.
	if issue.Status == "done" || issue.Status == "cancelled" {
		return false
	}
	squad, err := s.Queries.GetSquad(ctx, issue.AssigneeID)
	if err != nil {
		return false
	}
	if task.AgentID.Valid && task.AgentID.Bytes == squad.LeaderID.Bytes {
		return false
	}
	if pending, err := s.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
		IssueID: issue.ID,
		AgentID: squad.LeaderID,
	}); err != nil || pending {
		return false
	}
	leader, err := s.Queries.GetAgent(ctx, squad.LeaderID)
	if err != nil || leader.ArchivedAt.Valid || !leader.RuntimeID.Valid {
		return false
	}

	memberName := "A squad member"
	if failed, err := s.Queries.GetAgent(ctx, task.AgentID); err == nil && failed.Name != "" {
		memberName = failed.Name
	}
	body := fmt.Sprintf(
		"⚠️ %s could not finish its task (%s). Squad lead: re-route this — pick a different member or adjust the plan, and do NOT re-dispatch the same agent if this is a hard rate/usage limit.",
		memberName, failureReason,
	)
	// triggerCommentID links the leader's re-trigger to the failure note below,
	// so the leader run carries the "a member just failed" context instead of
	// re-reading the issue cold (mirrors the child-done re-trigger path).
	var triggerCommentID pgtype.UUID
	if comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     body,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	}); err == nil {
		triggerCommentID = comment.ID
		s.Bus.Publish(events.Event{
			Type:        protocol.EventCommentCreated,
			WorkspaceID: util.UUIDToString(issue.WorkspaceID),
			ActorType:   "system",
			Payload: map[string]any{
				"comment": map[string]any{
					"id":          util.UUIDToString(comment.ID),
					"issue_id":    util.UUIDToString(comment.IssueID),
					"author_type": comment.AuthorType,
					"author_id":   util.UUIDToString(comment.AuthorID),
					"content":     comment.Content,
					"type":        comment.Type,
					"parent_id":   util.UUIDToPtr(comment.ParentID),
					"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
				},
				"issue_title":  issue.Title,
				"issue_status": issue.Status,
			},
		})
	}

	if _, err := s.EnqueueTaskForSquadLeader(ctx, issue, squad.LeaderID, triggerCommentID); err != nil {
		slog.Warn("squad member failure: re-trigger leader failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return false
	}
	slog.Info("squad member failure: re-triggered leader",
		"issue_id", util.UUIDToString(issue.ID),
		"leader_id", util.UUIDToString(squad.LeaderID),
		"failed_agent_id", util.UUIDToString(task.AgentID),
		"reason", failureReason)
	return true
}

func (s *TaskService) HandleFailedTasks(ctx context.Context, tasks []db.AgentTaskQueue) int {
	if len(tasks) == 0 {
		return 0
	}

	affectedAgents := make(map[string]pgtype.UUID)
	processedIssues := make(map[string]bool)
	retriedIssues := make(map[string]bool)
	retried := 0

	for _, t := range tasks {
		orchestrationOwned := t.OrchestrationStepID.Valid
		if orchestrationOwned {
			// Stale-task and orphan recovery update task rows directly, bypassing
			// FailTask. Re-enter the same terminal callback used by the normal
			// daemon failure path so the persisted step retries in place or fails
			// its run instead of remaining queued/running forever.
			if s.OnOrchestrationTaskTerminal != nil {
				if err := s.OnOrchestrationTaskTerminal(ctx, t); err != nil {
					slog.Error("advance orchestration during stale-task recovery failed", "task_id", util.UUIDToString(t.ID), "error", err)
				}
			}
		} else {
			// Auto-retry first so the issue stays in_progress rather than
			// flapping todo → in_progress within a tick.
			if child, _ := s.MaybeRetryFailedTask(ctx, t); child != nil {
				retried++
				if t.IssueID.Valid {
					retriedIssues[util.UUIDToString(t.IssueID)] = true
				}
			}
		}

		failureReason := "agent_error"
		if t.FailureReason.Valid && t.FailureReason.String != "" {
			failureReason = t.FailureReason.String
		}
		s.captureTaskFailed(ctx, t)

		workspaceID := ""
		if t.IssueID.Valid {
			if issue, err := s.Queries.GetIssue(ctx, t.IssueID); err == nil {
				workspaceID = util.UUIDToString(issue.WorkspaceID)
				if !orchestrationOwned {
					// The run owns issue state and all follow-up dispatch. Generic
					// squad re-trigger and todo rollback would create work outside
					// the DAG or regress an issue while a step retry is already ready.
					issueKey := util.UUIDToString(t.IssueID)
					if !retriedIssues[issueKey] && s.maybeReTriggerSquadLeaderOnMemberFailure(ctx, t, issue, failureReason) {
						retriedIssues[issueKey] = true
					}
					if issue.Status == "in_progress" && !processedIssues[issueKey] && !retriedIssues[issueKey] {
						processedIssues[issueKey] = true
						hasActive, checkErr := s.Queries.HasActiveTaskForIssue(ctx, t.IssueID)
						if checkErr != nil {
							slog.Warn("handle failed tasks: active check failed", "issue_id", issueKey, "error", checkErr)
						} else if !hasActive {
							if s.issueHasInFlightPR(ctx, t.IssueID) {
								slog.Info("handle failed tasks: issue kept in_progress (in-flight PR present)", "issue_id", issueKey)
							} else if _, updateErr := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
								ID: t.IssueID, Status: "todo", WorkspaceID: issue.WorkspaceID,
							}); updateErr != nil {
								slog.Warn("handle failed tasks: reset stuck issue failed", "issue_id", issueKey, "error", updateErr)
							}
						}
					}
				}
			}
		}
		if workspaceID == "" {
			workspaceID = s.ResolveTaskWorkspaceID(ctx, t)
		}

		if workspaceID != "" {
			s.Bus.Publish(events.Event{
				Type:        protocol.EventTaskFailed,
				WorkspaceID: workspaceID,
				ActorType:   "system",
				Payload: map[string]any{
					"task_id":        util.UUIDToString(t.ID),
					"agent_id":       util.UUIDToString(t.AgentID),
					"issue_id":       util.UUIDToString(t.IssueID),
					"status":         "failed",
					"failure_reason": failureReason,
				},
			})
		}

		affectedAgents[util.UUIDToString(t.AgentID)] = t.AgentID
	}

	for _, agentID := range affectedAgents {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	return retried
}

// ReconcileTerminalOrchestrationTasks repairs the transaction boundary between
// a terminal agent_task_queue row and its persisted orchestration step. Normal
// daemon callbacks retry immediately; stale/offline/cancel paths are
// best-effort, so the periodic runtime sweeper uses this durable scan until the
// idempotent terminal callback converges.
func (s *TaskService) ReconcileTerminalOrchestrationTasks(ctx context.Context) int {
	if s.OnOrchestrationTaskTerminal == nil {
		return 0
	}
	tasks, err := s.Queries.ListUnreconciledTerminalOrchestrationTasks(ctx)
	if err != nil {
		slog.Warn("list unreconciled terminal orchestration tasks failed", "error", err)
		return 0
	}
	reconciled := 0
	for _, task := range tasks {
		if err := s.OnOrchestrationTaskTerminal(ctx, task); err != nil {
			slog.Warn("reconcile terminal orchestration task failed",
				"task_id", util.UUIDToString(task.ID),
				"step_id", util.UUIDToString(task.OrchestrationStepID),
				"status", task.Status,
				"error", err,
			)
			continue
		}
		reconciled++
	}
	return reconciled
}

// ReconcileRunnableOrchestrationRuns makes pending dispatch durable across the
// transaction boundary used by answers, approvals, retries, and terminal
// callbacks. The dispatcher is idempotent and owns its own queue guards.
func (s *TaskService) ReconcileRunnableOrchestrationRuns(ctx context.Context) int {
	if s.OnOrchestrationRunReady == nil {
		return 0
	}
	runIDs, err := s.Queries.ListOrchestrationRunsWithRunnablePendingSteps(ctx)
	if err != nil {
		slog.Warn("list runnable orchestration repairs failed", "error", err)
		return 0
	}
	repaired := 0
	for _, runID := range runIDs {
		if err := s.OnOrchestrationRunReady(ctx, runID); err != nil {
			slog.Warn("repair runnable orchestration dispatch failed", "run_id", util.UUIDToString(runID), "error", err)
			continue
		}
		repaired++
	}
	return repaired
}

// ReconcileOrchestrationAnswerComments repairs tagged member answers whose
// comment committed but whose idempotent orchestration response did not. The
// handler callback selects only still-open waiting_input questions, so every
// successful replay is safe to repeat on later sweeper ticks.
func (s *TaskService) ReconcileOrchestrationAnswerComments(ctx context.Context) int {
	if s.OnOrchestrationAnswerCommentsReady == nil {
		return 0
	}
	repaired, err := s.OnOrchestrationAnswerCommentsReady(ctx)
	if err != nil {
		slog.Warn("repair tagged orchestration answer comments failed", "error", err, "repaired", repaired)
	}
	return repaired
}

// runInTx executes fn inside a single DB transaction. If TxStarter is nil
// (e.g. some tests construct TaskService directly), fn runs against the
// regular Queries handle without transactional guarantees.
func (s *TaskService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReportProgress broadcasts a progress update via the event bus.
func (s *TaskService) ReportProgress(ctx context.Context, taskID string, workspaceID string, summary string, step, total int) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskProgress,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		TaskID:      taskID,
		Payload: protocol.TaskProgressPayload{
			TaskID:  taskID,
			Summary: summary,
			Step:    step,
			Total:   total,
		},
	})
}

// ReconcileAgentStatus refreshes agent status from the current active task set.
func (s *TaskService) ReconcileAgentStatus(ctx context.Context, agentID pgtype.UUID) {
	agent, err := s.Queries.RefreshAgentStatusFromTasks(ctx, agentID)
	if err != nil {
		return
	}
	slog.Debug("agent status reconciled", "agent_id", util.UUIDToString(agentID), "status", agent.Status)
	s.publishAgentStatus(agent)
}

func (s *TaskService) updateAgentStatus(ctx context.Context, agentID pgtype.UUID, status string) {
	agent, err := s.Queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
		ID:     agentID,
		Status: status,
	})
	if err != nil {
		return
	}
	s.publishAgentStatus(agent)
}

func (s *TaskService) publishAgentStatus(agent db.Agent) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventAgentStatus,
		WorkspaceID: util.UUIDToString(agent.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"agent": agentToMap(agent)},
	})
}

// LoadAgentSkills loads an agent's skills with their files for task execution.
func (s *TaskService) LoadAgentSkills(ctx context.Context, agentID pgtype.UUID) []AgentSkillData {
	skills, err := s.Queries.ListAgentSkills(ctx, agentID)
	if err != nil || len(skills) == 0 {
		return nil
	}

	result := make([]AgentSkillData, 0, len(skills))
	for _, sk := range skills {
		data := AgentSkillData{
			ID:          util.UUIDToString(sk.ID),
			Name:        sk.Name,
			Description: sk.Description,
			Content:     sk.Content,
		}
		files, _ := s.Queries.ListSkillFiles(ctx, sk.ID)
		for _, f := range files {
			data.Files = append(data.Files, AgentSkillFileData{Path: f.Path, Content: f.Content})
		}
		result = append(result, data)
	}
	return result
}

// AgentSkillData represents a skill for task execution responses.
type AgentSkillData struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Content     string               `json:"content"`
	Files       []AgentSkillFileData `json:"files,omitempty"`
}

// AgentSkillFileData represents a supporting file within a skill.
type AgentSkillFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// computeChatElapsedMs returns the wall-clock duration from task creation
// (user hit send) to terminal state (completed/failed). Stored on the
// assistant chat_message so the UI can render "Replied in 38s" /
// "Failed after 12s". Uses created_at — not started_at — because users
// experience total wait time, including queue + dispatch, not just the
// daemon's actual run time.
func computeChatElapsedMs(task db.AgentTaskQueue) pgtype.Int8 {
	if !task.CompletedAt.Valid || !task.CreatedAt.Valid {
		return pgtype.Int8{}
	}
	ms := task.CompletedAt.Time.Sub(task.CreatedAt.Time).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return pgtype.Int8{Int64: ms, Valid: true}
}

func priorityToInt(p string) int32 {
	switch p {
	case "urgent":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// PublishTaskEnqueued emits the queued event and then wakes the owning runtime.
// Transactional callers invoke it only after their task and source linkage
// commit, so a daemon can never observe a half-created work unit.
func (s *TaskService) PublishTaskEnqueued(ctx context.Context, task db.AgentTaskQueue) {
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
}

// NotifyTaskEnqueued is the cross-package shim for callers outside
// TaskService (e.g. AutopilotService.dispatchRunOnly) that insert a row into
// agent_task_queue directly. It invalidates the empty-claim cache and wakes the
// daemon without emitting a second queued event.
func (s *TaskService) NotifyTaskEnqueued(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskQueued(ctx, task)
	s.notifyTaskAvailable(task)
}

// notifyTaskAvailable runs after a task has been inserted: bumps the
// runtime's invalidation version so any in-flight claim that is about
// to write an "empty" verdict will have it rejected on read, then
// kicks the daemon WS so the daemon claims without waiting for its
// next poll. Order matters — Bump must happen before the wakeup,
// otherwise the wakeup-driven claim could read the still-current
// empty verdict and return null.
func (s *TaskService) notifyTaskAvailable(task db.AgentTaskQueue) {
	if !task.RuntimeID.Valid {
		return
	}
	runtimeKey := util.UUIDToString(task.RuntimeID)
	// Use a background context: the cache bump / wakeup must outlive
	// the request that created the task, otherwise an early client
	// disconnect could leave the empty verdict in place and stall the
	// just-queued task until the TTL expires. The cache itself bounds
	// every Redis call with a short timeout so a wedged Redis cannot
	// block enqueue.
	s.EmptyClaim.Bump(context.Background(), runtimeKey)
	if s.Wakeup == nil {
		return
	}
	s.Wakeup.NotifyTaskAvailable(runtimeKey, util.UUIDToString(task.ID))
}

func (s *TaskService) broadcastTaskDispatch(ctx context.Context, task db.AgentTaskQueue) {
	var payload map[string]any
	if task.Context != nil {
		json.Unmarshal(task.Context, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task_id"] = util.UUIDToString(task.ID)
	payload["runtime_id"] = util.UUIDToString(task.RuntimeID)
	payload["issue_id"] = util.UUIDToString(task.IssueID)
	payload["agent_id"] = util.UUIDToString(task.AgentID)
	// chat_session_id is the routing key the chat window uses to writethrough
	// `chatKeys.pendingTask` to status="running" the moment the daemon claims
	// the task. Without it the pill stays stuck at "Queued" until completion.
	if task.ChatSessionID.Valid {
		payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
	}

	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskDispatch,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	})
}

func (s *TaskService) broadcastTaskEvent(ctx context.Context, eventType string, task db.AgentTaskQueue) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	payload := map[string]any{
		"task_id":  util.UUIDToString(task.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"issue_id": util.UUIDToString(task.IssueID),
		"status":   task.Status,
	}
	if task.ChatSessionID.Valid {
		payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
	}
	s.Bus.Publish(events.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	})
}

// ResolveTaskWorkspaceID determines the workspace ID for a task.
// For issue tasks, it comes from the issue. For chat tasks, from the chat session.
// For autopilot tasks, from the autopilot via its run.
// Returns "" when none of the links resolve — callers treat that as "not found".
func (s *TaskService) ResolveTaskWorkspaceID(ctx context.Context, task db.AgentTaskQueue) string {
	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			return util.UUIDToString(issue.WorkspaceID)
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			return util.UUIDToString(cs.WorkspaceID)
		}
	}
	if task.AutopilotRunID.Valid {
		if run, err := s.Queries.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
			if ap, err := s.Queries.GetAutopilot(ctx, run.AutopilotID); err == nil {
				return util.UUIDToString(ap.WorkspaceID)
			}
		}
	}
	// Quick-create tasks have no issue / chat / autopilot link — workspace
	// lives in the context JSONB. Returning "" here is what blocked
	// requireDaemonTaskAccess (404 on /start, /progress, /complete, /fail
	// for the daemon) and silently dropped task:dispatch / task:completed
	// broadcasts, which is why quick-create tasks appeared stuck queued.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		return qc.WorkspaceID
	}
	return ""
}

func (s *TaskService) broadcastChatDone(ctx context.Context, task db.AgentTaskQueue, msg *db.ChatMessage) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	payload := protocol.ChatDonePayload{
		ChatSessionID: util.UUIDToString(task.ChatSessionID),
		TaskID:        util.UUIDToString(task.ID),
	}
	if msg != nil {
		payload.MessageID = util.UUIDToString(msg.ID)
		payload.Content = msg.Content
		if msg.CreatedAt.Valid {
			payload.CreatedAt = msg.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if msg.ElapsedMs.Valid {
			payload.ElapsedMs = msg.ElapsedMs.Int64
		}
	}
	s.Bus.Publish(events.Event{
		Type:          protocol.EventChatDone,
		WorkspaceID:   workspaceID,
		ActorType:     "system",
		ActorID:       "",
		ChatSessionID: util.UUIDToString(task.ChatSessionID),
		Payload:       payload,
	})
}

func (s *TaskService) broadcastIssueUpdated(issue db.Issue) {
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"issue": issueToMap(issue, prefix)},
	})
}

func (s *TaskService) getIssuePrefix(workspaceID pgtype.UUID) string {
	ws, err := s.Queries.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		return ""
	}
	return ws.IssuePrefix
}

// captureStructuredResult runs the fenced-block captures (test-runs, test-cases,
// compiled scripts, qa-result evidence) on an agent's FINAL task output WITHOUT
// posting a comment — used when the visible-comment fallback is suppressed
// because the agent already commented during the run, so a structured block
// that lives only in the final result still gets persisted. Also fires
// base-suite promotion when the block's runs land on an already-done issue
// (mirrors the comment-handler chain hook, which never runs on this path).
// triggerCommentID threads through to CaptureTestRuns so a scoped single-case
// run still gets its fail-closed enforcement on this path too.
func (s *TaskService) captureStructuredResult(ctx context.Context, issueID, agentID, triggerCommentID pgtype.UUID, content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	s.CaptureQAEvidence(ctx, issue, content, triggerCommentID)
	// A run_review verdict's ```review-result``` block becomes the
	// review:pass/review:fail gate label (Review stage v2). On a NEW attach fire
	// the merge re-check — mirror the HTTP comment ingress (comment.go), so a
	// review:pass that lands via task completion (not an HTTP comment) still
	// advances the merge instead of stalling.
	if verdict, labeled := s.CaptureReviewEvidence(ctx, issue, content, agentID); labeled && s.OnReviewVerdictLabeled != nil {
		s.OnReviewVerdictLabeled(ctx, issue, verdict, util.UUIDToString(agentID))
	}
	s.CaptureDeployEvent(ctx, issue, content)
	s.CaptureTestCases(ctx, issue, content, agentID)
	s.CaptureTestRuns(ctx, issue, content, agentID, triggerCommentID)
	s.CaptureCompiledScripts(ctx, issue, content, agentID)
	// Additively enrich the project's qa_manifest with any routes/flows this
	// task exercised, so the QA nav map grows richer with each done task.
	s.CaptureQAManifest(ctx, issue, content, agentID)
	// Base-suite chaining: run_test_cases runs recorded on an already-done
	// tracking issue promote its cases into the standing project suite. The
	// handler chain-hook fires on a comment POST; this path posts none, so do
	// it here. Idempotent (SQL dedups; the green-latest-run gate still applies).
	if issue.Status == "done" && issue.ProjectID.Valid {
		if ws, werr := s.Queries.GetWorkspace(ctx, issue.WorkspaceID); werr == nil {
			issueKey := fmt.Sprintf("%s-%d", ws.IssuePrefix, issue.Number)
			if _, perr := s.Queries.PromoteIssueTestCasesToProject(ctx, db.PromoteIssueTestCasesToProjectParams{
				IssueID: issue.ID, ProjectID: issue.ProjectID, IssueKey: issueKey,
			}); perr != nil {
				slog.Warn("captureStructuredResult: promotion failed", "issue_id", util.UUIDToString(issue.ID), "error", perr)
			}
		}
	}
}

func (s *TaskService) createAgentComment(ctx context.Context, issueID, agentID pgtype.UUID, content, commentType string, parentID pgtype.UUID) {
	if content == "" {
		return
	}
	// Look up issue to get workspace ID for mention expansion and broadcasting.
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	// Resolve the thread root for thread-level side effects without overwriting
	// parentID. The stored parent_id must remain the exact comment being replied
	// to; recursive thread reads recover the root when needed.
	var rootComment *db.Comment
	if parentID.Valid {
		if root, err := s.Queries.GetThreadRoot(ctx, db.GetThreadRootParams{
			CommentID:   parentID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			rootComment = &root
		}
	}
	// Expand bare issue identifiers (e.g. MUL-117) into mention links.
	// Capture hooks that parse fenced JSON need the PRE-expansion text:
	// expansion rewrites MUL-123 tokens into markdown links even inside
	// indented fences (findSkipRegions is line-anchored), breaking the parse.
	originalContent := content
	content = mention.ExpandIssueIdentifiers(ctx, s.Queries, issue.WorkspaceID, content)
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    agentID,
		Content:     content,
		Type:        commentType,
		ParentID:    parentID,
	})
	if err != nil {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment": map[string]any{
				"id":          util.UUIDToString(comment.ID),
				"issue_id":    util.UUIDToString(comment.IssueID),
				"author_type": comment.AuthorType,
				"author_id":   util.UUIDToString(comment.AuthorID),
				"content":     comment.Content,
				"type":        comment.Type,
				"parent_id":   util.UUIDToPtr(comment.ParentID),
				"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title":  issue.Title,
			"issue_status": issue.Status,
		},
	})
	s.AutoUnresolveThreadOnReply(ctx, rootComment, util.UUIDToString(issue.WorkspaceID), "agent", util.UUIDToString(agentID))

	// Persist a run_qa verdict's structured ```qa-result``` block as durable QA
	// evidence so the issue's QA section reads one indexed row, not the timeline.
	// parentID is this reply's trigger comment — read for triggered_by (Phase 3).
	s.CaptureQAEvidence(ctx, issue, content, parentID)
	// Persist a run_review verdict's ```review-result``` block as the
	// review:pass/review:fail gate label (Review stage v2). On a NEW attach fire
	// the merge re-check — same seam as the HTTP comment ingress (comment.go).
	if verdict, labeled := s.CaptureReviewEvidence(ctx, issue, content, agentID); labeled && s.OnReviewVerdictLabeled != nil {
		s.OnReviewVerdictLabeled(ctx, issue, verdict, util.UUIDToString(agentID))
	}
	// Persist a deploy agent's ```deploy-result``` block as a deploy_event row
	// (the stepper's Deploy signal — deploy-mcp-integration.md §5).
	s.CaptureDeployEvent(ctx, issue, content)
	// Persist a gen_test_cases agent's ```test-cases``` block as test_case rows,
	// and a run_test_cases agent's ```test-runs``` block as test_run rows.
	// parentID is this reply's trigger comment — CaptureTestRuns re-reads it to
	// enforce a scoped single-case run (see scopedTestCaseIDFromTrigger).
	s.CaptureTestCases(ctx, issue, content, agentID)
	s.CaptureTestRuns(ctx, issue, content, agentID, parentID)
	// Persist a compile_tests agent's ```scripts``` block onto the named cases.
	s.CaptureCompiledScripts(ctx, issue, content, agentID)
	// Persist a design_proposal agent's ```design-proposal``` block: attach the
	// design:proposed label + notify the issue's humans.
	s.CaptureDesignProposal(ctx, issue, comment, agentID)
	// Persist a gen_design_context agent's strict block as a pending revision.
	// A human owner/admin must approve it before any runtime can consume it.
	s.CaptureDesignContext(ctx, issue, comment, agentID)
	// Persist a synthesizer's ```knowledge-items``` block as knowledge_item
	// rows and recompile the KB. Pre-expansion content on purpose (see above).
	s.CaptureKnowledgeItems(ctx, issue, originalContent, agentID)
	// Grow the project's qa_manifest with routes/flows the task exercised.
	s.CaptureQAManifest(ctx, issue, originalContent, agentID)
}

// AutoUnresolveThreadOnReply clears resolved_at on the thread root when a
// reply lands in a resolved thread, and broadcasts comment:unresolved. Shared
// between the user-facing Handler.CreateComment path and the agent-facing
// TaskService.createAgentComment path so the resolved-then-replied state can
// never desync (one of the bugs Emacs flagged on PR #2300). Errors are logged
// — the reply itself already committed, the desync is recoverable on next read.
func (s *TaskService) AutoUnresolveThreadOnReply(ctx context.Context, parent *db.Comment, workspaceID, actorType, actorID string) {
	if parent == nil || !parent.ResolvedAt.Valid {
		return
	}
	updated, err := s.Queries.UnresolveComment(ctx, parent.ID)
	if err != nil {
		slog.Warn("auto-unresolve on reply failed", "error", err, "comment_id", util.UUIDToString(parent.ID))
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentUnresolved,
		WorkspaceID: workspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		Payload: map[string]any{
			"comment": map[string]any{
				"id":               util.UUIDToString(updated.ID),
				"issue_id":         util.UUIDToString(updated.IssueID),
				"author_type":      updated.AuthorType,
				"author_id":        util.UUIDToString(updated.AuthorID),
				"content":          updated.Content,
				"type":             updated.Type,
				"parent_id":        util.UUIDToPtr(updated.ParentID),
				"created_at":       util.TimestampToString(updated.CreatedAt),
				"updated_at":       util.TimestampToString(updated.UpdatedAt),
				"resolved_at":      util.TimestampToPtr(updated.ResolvedAt),
				"resolved_by_type": util.TextToPtr(updated.ResolvedByType),
				"resolved_by_id":   util.UUIDToPtr(updated.ResolvedByID),
			},
		},
	})
}

func issueToMap(issue db.Issue, issuePrefix string) map[string]any {
	return map[string]any{
		"id":              util.UUIDToString(issue.ID),
		"workspace_id":    util.UUIDToString(issue.WorkspaceID),
		"number":          issue.Number,
		"identifier":      issuePrefix + "-" + strconv.Itoa(int(issue.Number)),
		"title":           issue.Title,
		"description":     util.TextToPtr(issue.Description),
		"status":          issue.Status,
		"priority":        issue.Priority,
		"assignee_type":   util.TextToPtr(issue.AssigneeType),
		"assignee_id":     util.UUIDToPtr(issue.AssigneeID),
		"creator_type":    issue.CreatorType,
		"creator_id":      util.UUIDToString(issue.CreatorID),
		"parent_issue_id": util.UUIDToPtr(issue.ParentIssueID),
		"position":        issue.Position,
		"start_date":      util.DateToPtr(issue.StartDate),
		"due_date":        util.DateToPtr(issue.DueDate),
		"created_at":      util.TimestampToString(issue.CreatedAt),
		"updated_at":      util.TimestampToString(issue.UpdatedAt),
	}
}

// parseQuickCreateContext returns the quick-create payload if the task's
// context JSONB contains type == "quick_create"; otherwise the bool is
// false so callers can short-circuit. Tasks linked to an issue / chat /
// autopilot are never quick-create even if they happen to carry a
// context blob, so those are filtered up front.
func (s *TaskService) parseQuickCreateContext(task db.AgentTaskQueue) (QuickCreateContext, bool) {
	if task.IssueID.Valid || task.ChatSessionID.Valid || task.AutopilotRunID.Valid {
		return QuickCreateContext{}, false
	}
	if len(task.Context) == 0 {
		return QuickCreateContext{}, false
	}
	var qc QuickCreateContext
	if err := json.Unmarshal(task.Context, &qc); err != nil {
		return QuickCreateContext{}, false
	}
	if qc.Type != QuickCreateContextType {
		return QuickCreateContext{}, false
	}
	return qc, true
}

// notifyQuickCreateCompleted writes a success inbox notification to the
// requester pointing at the issue the agent just created. The issue is
// stamped with origin_type=quick_create + origin_id=<task_id> by the
// daemon-injected AGORA_QUICK_CREATE_TASK_ID env var, so this lookup is
// deterministic — robust against the same agent creating other issues in
// parallel (e.g. assignment task running while max_concurrent_tasks > 1
// permits another quick-create alongside it).
func (s *TaskService) notifyQuickCreateCompleted(ctx context.Context, task db.AgentTaskQueue, qc QuickCreateContext) {
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		slog.Warn("quick-create completion: invalid requester id", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		slog.Warn("quick-create completion: invalid workspace id", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	issue, err := s.Queries.GetIssueByOrigin(ctx, db.GetIssueByOriginParams{
		WorkspaceID: workspaceID,
		OriginType:  pgtype.Text{String: "quick_create", Valid: true},
		OriginID:    task.ID,
	})
	if err != nil {
		// No issue created — agent ran to completion but the CLI call must
		// have failed. Surface as a failure inbox so the user sees something.
		slog.Warn("quick-create completion: no issue found, writing failure inbox",
			"task_id", util.UUIDToString(task.ID),
			"agent_id", util.UUIDToString(task.AgentID),
			"workspace_id", qc.WorkspaceID,
		)
		s.notifyQuickCreateFailed(ctx, task, qc, "agent finished without creating an issue")
		return
	}

	// Link the new issue back to this task so subsequent reads of the task
	// (Activity tab, Recent work, etc.) render it as a normal issue task
	// (kind = "direct") instead of staying on the "Creating issue" active-
	// wording label. Best-effort: a write failure here doesn't block the
	// inbox notification, which is the more important signal to the user.
	if err := s.Queries.LinkTaskToIssue(ctx, db.LinkTaskToIssueParams{
		ID:      task.ID,
		IssueID: issue.ID,
	}); err != nil {
		slog.Warn("quick-create completion: link task→issue failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"error", err,
		)
	}

	// Subscribe the requester so they receive notifications for follow-up
	// comments and updates. The DB row's creator_type/creator_id is the
	// agent (it ran the CLI), but the human who triggered the quick-create
	// is the semantic creator from a UX perspective — without this they
	// only see the one-shot completion inbox and miss everything after.
	// Best-effort: log on failure but don't block the inbox notification.
	if err := s.Queries.AddIssueSubscriber(ctx, db.AddIssueSubscriberParams{
		IssueID:  issue.ID,
		UserType: "member",
		UserID:   requesterID,
		Reason:   "creator",
	}); err != nil {
		slog.Warn("quick-create completion: subscribe requester failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"requester_id", qc.RequesterID,
			"error", err,
		)
	} else {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventSubscriberAdded,
			WorkspaceID: qc.WorkspaceID,
			ActorType:   "agent",
			ActorID:     util.UUIDToString(task.AgentID),
			Payload: map[string]any{
				"issue_id":  util.UUIDToString(issue.ID),
				"user_type": "member",
				"user_id":   qc.RequesterID,
				"reason":    "creator",
			},
		})
	}
	prefix := s.getIssuePrefix(workspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	details, _ := json.Marshal(map[string]any{
		"task_id":         util.UUIDToString(task.ID),
		"agent_id":        util.UUIDToString(task.AgentID),
		"issue_id":        util.UUIDToString(issue.ID),
		"identifier":      identifier,
		"original_prompt": qc.Prompt,
	})
	item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   requesterID,
		Type:          "quick_create_done",
		Severity:      "info",
		IssueID:       issue.ID,
		Title:         issue.Title,
		Body:          pgtype.Text{},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       task.AgentID,
		Details:       details,
	})
	if err != nil {
		slog.Error("quick-create completion: inbox write failed", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.publishQuickCreateInbox(item, qc.WorkspaceID, util.UUIDToString(task.AgentID), issue.Status)
}

// notifyQuickCreateFailed writes a failure inbox notification carrying the
// original prompt + agent ID so the frontend can render an "Edit as
// advanced form" entry that pre-fills the legacy create-issue modal
// without asking the user to retype.
func (s *TaskService) notifyQuickCreateFailed(ctx context.Context, task db.AgentTaskQueue, qc QuickCreateContext, errMsg string) {
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		return
	}
	if errMsg == "" {
		errMsg = "Quick create did not finish successfully"
	}
	details, _ := json.Marshal(map[string]any{
		"task_id":         util.UUIDToString(task.ID),
		"agent_id":        util.UUIDToString(task.AgentID),
		"original_prompt": qc.Prompt,
		"error":           redact.Text(errMsg),
	})
	item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   requesterID,
		Type:          "quick_create_failed",
		Severity:      "action_required",
		IssueID:       pgtype.UUID{},
		Title:         "Quick create failed",
		Body:          pgtype.Text{String: redact.Text(errMsg), Valid: true},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       task.AgentID,
		Details:       details,
	})
	if err != nil {
		slog.Error("quick-create failure: inbox write failed", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.publishQuickCreateInbox(item, qc.WorkspaceID, util.UUIDToString(task.AgentID), "")
}

// publishQuickCreateInbox emits the WS event so the requester's inbox list
// updates immediately. Mirrors the payload shape used by the other inbox
// listeners (notification_listeners.go).
func (s *TaskService) publishQuickCreateInbox(item db.InboxItem, workspaceID, agentID, issueStatus string) {
	resp := map[string]any{
		"id":             util.UUIDToString(item.ID),
		"workspace_id":   util.UUIDToString(item.WorkspaceID),
		"recipient_type": item.RecipientType,
		"recipient_id":   util.UUIDToString(item.RecipientID),
		"type":           item.Type,
		"severity":       item.Severity,
		"issue_id":       util.UUIDToPtr(item.IssueID),
		"title":          item.Title,
		"body":           util.TextToPtr(item.Body),
		"read":           item.Read,
		"archived":       item.Archived,
		"created_at":     util.TimestampToString(item.CreatedAt),
		"actor_type":     util.TextToPtr(item.ActorType),
		"actor_id":       util.UUIDToPtr(item.ActorID),
		"details":        json.RawMessage(item.Details),
		"issue_status":   issueStatus,
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: workspaceID,
		ActorType:   "agent",
		ActorID:     agentID,
		Payload:     map[string]any{"item": resp},
	})
}

// agentToMap builds a simple map for broadcasting agent status updates.
func agentToMap(a db.Agent) map[string]any {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	return map[string]any{
		"id":                   util.UUIDToString(a.ID),
		"workspace_id":         util.UUIDToString(a.WorkspaceID),
		"runtime_id":           util.UUIDToString(a.RuntimeID),
		"name":                 a.Name,
		"description":          a.Description,
		"avatar_url":           util.TextToPtr(a.AvatarUrl),
		"runtime_mode":         a.RuntimeMode,
		"runtime_config":       rc,
		"visibility":           a.Visibility,
		"status":               a.Status,
		"max_concurrent_tasks": a.MaxConcurrentTasks,
		"owner_id":             util.UUIDToPtr(a.OwnerID),
		"skills":               []any{},
		"created_at":           util.TimestampToString(a.CreatedAt),
		"updated_at":           util.TimestampToString(a.UpdatedAt),
		"archived_at":          util.TimestampToPtr(a.ArchivedAt),
		"archived_by":          util.UUIDToPtr(a.ArchivedBy),
	}
}
