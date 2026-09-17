package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Hard caps on one run. Every one of these exists to bound cost and latency of
// a loop the user is waiting on — see the plan's "loop cost runaway" risk.
const (
	// MaxToolRounds is how many times the model may come back asking for more
	// tools before we stop and make it answer with what it has.
	MaxToolRounds = 8
	// MaxRunDuration is the ceiling on a whole run, LLM calls included.
	MaxRunDuration = 3 * time.Minute
	// ToolCallTimeout bounds one tool execution. A slow query must not eat the
	// run budget that the remaining rounds need.
	ToolCallTimeout = 15 * time.Second
	// HistoryLimit is how many transcript rows feed the model. Older turns are
	// dropped; the session's rolling summary (Phase 2) covers them.
	HistoryLimit = 30
	// MaxSessionTitleLen caps the auto-title derived from the first message.
	MaxSessionTitleLen = 80
)

// Run statuses reported by assistant:run_finished.
const (
	RunStatusOK        = "ok"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

// ErrRunInProgress is returned by StartRun when the session already has a live
// run. One run per session: two concurrent loops would interleave their writes
// into the same transcript and the model would read its own half-finished turn.
var ErrRunInProgress = errors.New("assistant: a run is already active for this session")

// ClientFactory resolves the provider client and model id for a run. It is a
// function (not a value) because both are instance config that an operator can
// change at runtime — resolving per run means a provider flip takes effect on
// the next message, with no restart.
type ClientFactory func() (llm.ToolChat, string, error)

// Service owns the assistant conversation loop.
type Service struct {
	Queries          *db.Queries
	Store            runDB
	TxStarter        runTxStarter
	ContextValidator func(context.Context, string, RunContext) error
	Bus              *events.Bus
	Client           ClientFactory
	Exec             ToolExecutor
	// ModelLabel is the human name the UI shows under a reply ("GPT (gpt-5…)").
	// Injected rather than derived because the label is instance configuration
	// owned by the HTTP layer, and the assistant package must not read env.
	// When unset the prompt falls back to the provider's raw model id.
	ModelLabel func() string

	mu sync.Mutex
	// runs maps run id -> cancel, so POST /runs/{id}/cancel can stop one.
	runs map[string]*runHandle
	// active maps session id -> run id, enforcing one live run per session.
	active map[string]string
}

type runHandle struct {
	sessionID string
	userID    string
	owner     string
	cancel    context.CancelFunc
}

// NewService builds a Service. Exec may be wired after construction (the
// handler both holds the service and implements its executor).
func NewService(queries *db.Queries, bus *events.Bus, client ClientFactory) *Service {
	return &Service{
		Queries: queries,
		Bus:     bus,
		Client:  client,
		runs:    map[string]*runHandle{},
		active:  map[string]string{},
	}
}

// ---------------------------------------------------------------------------
// Run registry
// ---------------------------------------------------------------------------

func (s *Service) finishRun(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.runs[runID]
	if !ok {
		return
	}
	delete(s.runs, runID)
	if s.active[h.sessionID] == runID {
		delete(s.active, h.sessionID)
	}
}

// CancelRun stops a live run. Returns false when the run is unknown, already
// finished, or belongs to someone else — the caller maps that to a 404 so a
// run id cannot be probed across accounts.
func (s *Service) CancelRun(runID, userID string) bool {
	s.mu.Lock()
	h, ok := s.runs[runID]
	if !ok || h.userID != userID {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	h.cancel()
	return true
}

// CancelSession signals a worker hosted by this process. Persistent admission
// and cancellation still use the database; this is only a local wakeup.
func (s *Service) CancelSession(sessionID string) {
	s.mu.Lock()
	runID := s.active[sessionID]
	h := s.runs[runID]
	s.mu.Unlock()
	if h != nil {
		h.cancel()
	}
}

func (s *Service) HasActiveRun(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sessionID] != ""
}

// ---------------------------------------------------------------------------
// The loop
// ---------------------------------------------------------------------------

// Run drives one conversation turn to completion: call the model, persist and
// broadcast what it said, execute any tools it asked for, and call it again —
// until it answers with text, hits MaxToolRounds, or the context expires.
//
// Exported and synchronous so tests can drive a run without racing a
// goroutine; StartRun is the production entry point.
func (s *Service) Run(ctx context.Context, sessionID, runID, userID string) {
	status, errMsg := s.runLoop(ctx, sessionID, runID, userID)

	// A dead context outranks whatever step happened to notice it first. A run
	// the user stopped must report "cancelled", not the incidental failure of
	// whichever query was in flight when the cancel landed.
	if ctxStatus, done := terminalFromContext(ctx); done {
		status, errMsg = ctxStatus, contextErrorMessage(ctx)
	}
	if s.Store != nil {
		var cancelRequested bool
		check, c := context.WithTimeout(context.Background(), 5*time.Second)
		if s.Store.QueryRow(check, `SELECT cancel_requested FROM assistant_run WHERE id=$1`, runID).Scan(&cancelRequested) == nil && cancelRequested {
			status, errMsg = RunStatusCancelled, ""
		}
		c()
	}

	// The finalizers run on a fresh context: when a run ends because it was
	// cancelled or timed out, ctx is already dead and these writes would be
	// dropped exactly when the client most needs the terminal event.
	final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.Store != nil {
		s.mu.Lock()
		owner := ""
		if h := s.runs[runID]; h != nil {
			owner = h.owner
		}
		s.mu.Unlock()
		if owner != "" {
			terminal := "completed"
			if status == RunStatusFailed {
				terminal = "failed"
			}
			if status == RunStatusCancelled {
				terminal = "cancelled"
			}
			updated, err := s.updateRun(final, runID, owner, terminal, errMsg)
			if err != nil {
				slog.Error("assistant: persist terminal status failed", "run_id", runID, "error", err)
				return
			}
			if !updated {
				return
			}
		}
	}

	if sessionUUID, err := util.ParseUUID(sessionID); err == nil {
		if err := s.Queries.TouchAssistantSession(final, sessionUUID); err != nil {
			slog.Warn("assistant: touch session failed", "session_id", sessionID, "error", err)
		}
	}

	s.publish(protocol.EventAssistantRunFinished, protocol.AssistantRunFinishedPayload{
		UserID:    userID,
		SessionID: sessionID,
		RunID:     runID,
		Status:    status,
		Error:     errMsg,
	})
}

// runLoop returns the terminal (status, error) for the run.
func (s *Service) runLoop(ctx context.Context, sessionID, runID, userID string) (string, string) {
	sessionUUID, err := util.ParseUUID(sessionID)
	if err != nil {
		return RunStatusFailed, "invalid session"
	}

	session, err := s.Queries.GetAssistantSession(ctx, sessionUUID)
	if err != nil {
		return RunStatusFailed, "session not found"
	}

	client, model, err := s.Client()
	if err != nil {
		slog.Warn("assistant: no provider configured", "error", err)
		return RunStatusFailed, "the assistant is not configured on this instance"
	}

	focus := session.FocusWorkspaceID
	runTimezone := ""
	runContext := RunContext{}
	if s.Store != nil {
		var workspace *string
		var timezone string
		var project *string
		var attachments []string
		if err := s.Store.QueryRow(ctx, `SELECT context_workspace_id::text, context_timezone,context_project_id::text,context_attachment_ids::text[] FROM assistant_run WHERE id=$1 AND user_id=$2`, runID, userID).Scan(&workspace, &timezone, &project, &attachments); err != nil {
			return RunStatusFailed, "could not load this message's workspace context"
		}
		runContext = RunContext{WorkspaceID: workspace, Timezone: timezone, ProjectID: project, AttachmentIDs: attachments}
		focus = pgtype.UUID{}
		if workspace != nil {
			focus, err = util.ParseUUID(*workspace)
			if err != nil {
				return RunStatusFailed, "invalid workspace context"
			}
		}
		runTimezone = timezone
	}
	uc, err := s.userContext(ctx, userID, focus)
	if err != nil {
		slog.Warn("assistant: user context failed", "user_id", userID, "error", err)
		return RunStatusFailed, "could not load your workspaces"
	}
	if focus.Valid {
		allowed := false
		for _, ws := range uc.Workspaces {
			if ws.ID == util.UUIDToString(focus) {
				allowed = true
				break
			}
		}
		if !allowed {
			return RunStatusFailed, "you no longer have access to this message's workspace"
		}
	}
	if runContext.ProjectID != nil || len(runContext.AttachmentIDs) > 0 {
		if s.ContextValidator == nil {
			return RunStatusFailed, "selected project and file access could not be checked"
		}
		if err := s.ContextValidator(ctx, userID, runContext); err != nil {
			return RunStatusFailed, "you no longer have access to selected project or files"
		}
	}
	uc.Timezone = runTimezone
	if s.ModelLabel != nil {
		uc.ModelLabel = s.ModelLabel()
	}
	if uc.ModelLabel == "" {
		uc.ModelLabel = model
	}
	systemText := buildSystemPrompt(uc, session.Summary)
	if s.Store != nil {
		var snapshotJSON []byte
		if err := s.Store.QueryRow(ctx, `SELECT context_snapshot FROM assistant_run WHERE id=$1 AND user_id=$2`, runID, userID).Scan(&snapshotJSON); err != nil {
			return RunStatusFailed, "could not load selected project and files"
		}
		var snapshot ContextSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return RunStatusFailed, "selected project and file context is unreadable"
		}
		if contextText := snapshot.Prompt(); contextText != "" {
			systemText += "\n\n" + contextText
		}
	}
	system := llm.Message{Role: "system", Content: systemText}
	tools := ToolSpecs()

	// The timezone the client captured when this message was sent rides on the
	// context every tool executes under. "Today" is the caller's day, not the
	// server's — see runcontext.go.
	ctx = WithTimezone(ctx, runTimezone)

	for round := 0; round < MaxToolRounds; round++ {
		if status, done := terminalFromContext(ctx); done {
			return status, contextErrorMessage(ctx)
		}

		history, err := s.history(ctx, sessionUUID)
		if err != nil {
			slog.Warn("assistant: load history failed", "session_id", sessionID, "error", err)
			return RunStatusFailed, "could not load the conversation"
		}

		reply, err := client.CompleteWithTools(ctx, model, append([]llm.Message{system}, history...), tools)
		if err != nil {
			if status, done := terminalFromContext(ctx); done {
				return status, contextErrorMessage(ctx)
			}
			slog.Warn("assistant: completion failed", "session_id", sessionID, "error", err)
			return RunStatusFailed, "the assistant could not reach its model, try again"
		}

		var toolCallsJSON []byte
		if len(reply.ToolCalls) > 0 {
			toolCallsJSON, _ = json.Marshal(reply.ToolCalls)
		}
		// A turn with neither text nor tool calls is a degenerate model reply.
		// Persisting it would teach the next round nothing and loop forever.
		if reply.Content == "" && len(reply.ToolCalls) == 0 {
			return RunStatusFailed, "the assistant returned an empty reply, try again"
		}

		msg, err := s.Queries.CreateAssistantMessage(ctx, db.CreateAssistantMessageParams{
			SessionID: sessionUUID,
			Role:      "assistant",
			Content:   reply.Content,
			ToolCalls: toolCallsJSON,
		})
		if err != nil {
			slog.Warn("assistant: persist reply failed", "session_id", sessionID, "error", err)
			return RunStatusFailed, "could not save the reply"
		}
		s.publish(protocol.EventAssistantMessage, protocol.AssistantMessagePayload{
			UserID:    userID,
			SessionID: sessionID,
			RunID:     runID,
			MessageID: util.UUIDToString(msg.ID),
			Role:      "assistant",
			Content:   reply.Content,
			CreatedAt: util.TimestampToString(msg.CreatedAt),
		})

		if len(reply.ToolCalls) == 0 {
			return RunStatusOK, ""
		}

		for _, call := range reply.ToolCalls {
			if status, done := terminalFromContext(ctx); done {
				return status, contextErrorMessage(ctx)
			}
			outcome := s.runToolCall(ctx, sessionUUID, sessionID, runID, userID, call)
			if outcome == toolCallContinue {
				continue
			}
			// A cancel that landed mid-tool outranks whichever bookkeeping
			// step noticed the run was no longer ours.
			if s.Store != nil {
				var cancelled bool
				if s.Store.QueryRow(ctx, `SELECT cancel_requested FROM assistant_run WHERE id=$1`, runID).Scan(&cancelled) == nil && cancelled {
					return RunStatusCancelled, ""
				}
			}
			if outcome == toolCallUncertain {
				return RunStatusFailed, "a write was sent but its outcome could not be confirmed; check whether it took effect before retrying"
			}
			return RunStatusFailed, "this run could not record what it was doing and was stopped; review any recent change before retrying"
		}
	}

	// Round cap reached with the model still asking for tools. This is a real
	// outcome, not an error: the transcript holds everything gathered so far.
	return RunStatusFailed, "the assistant used too many steps without reaching an answer"
}

// toolCallOutcome is what one tool call did to the run.
//
// It is three-valued rather than a bool because the three cases have genuinely
// different consequences for the user, and collapsing them is what produced
// the "a write may have succeeded" message on ordinary validation failures:
// a tool that refuses is NOT an interrupted write.
type toolCallOutcome int

const (
	// toolCallContinue: the answer (success OR a plain refusal) is on the
	// transcript and the model can read it. The loop goes on.
	toolCallContinue toolCallOutcome = iota
	// toolCallUncertain: a mutation was dispatched and its outcome is unknown.
	// The run stops and the user is told to inspect; nothing is retried.
	toolCallUncertain
	// toolCallAborted: the run's own bookkeeping failed (lease lost, receipt
	// unwritable). Nothing can be trusted to have been recorded, so stop.
	toolCallAborted
)

// assistant_operation statuses (migration 197). 'failed' is the one this file
// used to never write, which is exactly how an ordinary refusal came out as an
// interrupted write.
const (
	operationSucceeded = "succeeded"
	operationFailed    = "failed"
	operationUncertain = "uncertain"
)

// receiptPersistTimeout bounds the fresh contexts used to write receipts and
// transcript rows AFTER a tool has run. They are deliberately short: this work
// happens off the run's context, so nothing here can block on a dead pool.
const receiptPersistTimeout = 5 * time.Second

// classifyToolOutcome maps a tool's answer onto an assistant_operation status.
//
// The distinction is the whole point of the receipt table:
//
//   - uncertain — the request reached the server and the answer was lost. This
//     is the ONLY case that must not be retried, and the only one that stops a
//     run. It is signalled by the result's status field (never by an "error"
//     key: an error invites a retry).
//   - failed    — the tool refused, deterministically and with no effect: bad
//     arguments, a missing target, a role the caller does not have. The model
//     gets the message and corrects itself; the run continues.
//   - succeeded — everything else, including a destructive call that parked a
//     needs_confirmation card (the CALL succeeded — it asked).
func classifyToolOutcome(result json.RawMessage, executeErr error) string {
	var decoded map[string]json.RawMessage
	if json.Unmarshal(result, &decoded) == nil {
		var status string
		if raw, ok := decoded["status"]; ok && json.Unmarshal(raw, &status) == nil && status == StatusUncertain {
			return operationUncertain
		}
		if decoded["error"] != nil {
			return operationFailed
		}
	}
	if executeErr != nil {
		return operationFailed
	}
	return operationSucceeded
}

// runToolCall executes one tool and persists its answer as a role="tool" turn.
//
// It never returns an error: a failing tool becomes an {"error": ...} result
// the model reads and can recover from, which is strictly better than killing
// a run the user is waiting on.
//
// Everything AFTER the execution — the receipt, the transcript row, the
// active_tool reset — runs on a FRESH context. The run's context (and the
// 15 s tool context derived from it) may well have expired during the call
// that just finished, and dropping the receipt exactly then is how a completed
// write becomes an unexplained "uncertain" outcome. A receipt must outlive the
// deadline of the thing it is a receipt for.
func (s *Service) runToolCall(ctx context.Context, sessionUUID pgtype.UUID, sessionID, runID, userID string, call llm.ToolCall) toolCallOutcome {
	mutating := IsMutating(call.Name) && s.Store != nil
	receiptID := strings.TrimSpace(call.ID)
	if s.Store != nil {
		s.mu.Lock()
		owner := ""
		if h := s.runs[runID]; h != nil {
			owner = h.owner
		}
		s.mu.Unlock()
		tag, err := s.Store.Exec(ctx, `UPDATE assistant_run SET active_tool=$3,updated_at=now(),version=version+1 WHERE id=$1 AND lease_owner=$2 AND status='running' AND cancel_requested=false AND lease_expires_at>now()`, runID, owner, call.Name)
		if err != nil || tag.RowsAffected() != 1 {
			return toolCallAborted
		}
	}
	if mutating {
		args := json.RawMessage(strings.TrimSpace(call.Arguments))
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		id, err := s.insertOperation(ctx, runID, receiptID, call.Name, args)
		if err != nil {
			slog.Warn("assistant: persist operation intent failed", "run_id", runID, "tool", call.Name, "error", err)
			return toolCallAborted
		}
		receiptID = id
	}
	if ctx.Err() != nil {
		return toolCallAborted
	}
	if s.Store != nil {
		var allowed bool
		if err := s.Store.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assistant_run WHERE id=$1 AND status='running' AND cancel_requested=false AND lease_expires_at>now())`, runID).Scan(&allowed); err != nil || !allowed {
			return toolCallAborted
		}
	}
	s.publish(protocol.EventAssistantToolActivity, protocol.AssistantToolActivityPayload{
		UserID:     userID,
		SessionID:  sessionID,
		RunID:      runID,
		ToolCallID: call.ID,
		ToolName:   call.Name,
	})

	result, executeErr := s.execute(ctx, userID, sessionID, call)

	final, cancelFinal := context.WithTimeout(context.Background(), receiptPersistTimeout)
	defer cancelFinal()

	opStatus := operationSucceeded
	if mutating {
		// assistant_operation (migration 197) is the run's EXECUTION RECEIPT,
		// and it is not the same record as assistant_pending_operation
		// (migration 198), which is the pre-authorization a destructive call
		// parks. The two coexist on purpose:
		//
		//   - A destructive call that returns needs_confirmation is recorded
		//     here as 'succeeded', because the TOOL CALL did succeed — it
		//     asked. The stored `result` is the needs_confirmation payload, so
		//     a reader can always tell a parked attempt from a mutation.
		//   - When the user later presses Confirm, the confirm endpoint writes
		//     a SECOND row against this same run (tool_call_id "op_<id>") with
		//     the outcome of the actual execution. Overwriting this one instead
		//     would erase the fact that the model asked before a human answered.
		opStatus = classifyToolOutcome(result, executeErr)
		tag, err := s.Store.Exec(final, `UPDATE assistant_operation SET status=$4,result=$3,updated_at=now() WHERE run_id=$1 AND tool_call_id=$2 AND status='pending'`, runID, receiptID, result, opStatus)
		if err != nil || tag.RowsAffected() != 1 {
			slog.Error("assistant: persist tool receipt failed",
				"run_id", runID, "tool", call.Name, "status", opStatus, "error", err)
			return toolCallAborted
		}
	}

	msg, err := s.Queries.CreateAssistantMessage(final, db.CreateAssistantMessageParams{
		SessionID:  sessionUUID,
		Role:       "tool",
		Content:    string(result),
		ToolCallID: util.StrToText(call.ID),
		ToolName:   util.StrToText(call.Name),
		ToolResult: result,
	})
	if err != nil {
		slog.Warn("assistant: persist tool result failed", "session_id", sessionID, "tool", call.Name, "error", err)
		return toolCallAborted
	}
	if s.Store != nil {
		_, _ = s.Store.Exec(final, `UPDATE assistant_run SET active_tool=NULL,updated_at=now(),version=version+1 WHERE id=$1 AND status='running'`, runID)
	}
	s.publish(protocol.EventAssistantMessage, protocol.AssistantMessagePayload{
		UserID:    userID,
		SessionID: sessionID,
		RunID:     runID,
		MessageID: util.UUIDToString(msg.ID),
		Role:      "tool",
		ToolName:  call.Name,
		CreatedAt: util.TimestampToString(msg.CreatedAt),
	})
	// The answer is on the transcript either way; only an unknown outcome stops
	// the run. A refusal is something the model reads and recovers from.
	if opStatus == operationUncertain {
		return toolCallUncertain
	}
	return toolCallContinue
}

// insertOperation writes the pre-execution intent row and returns the
// tool_call_id it landed under.
//
// The id it is handed is whatever the provider called the tool call, and that
// is not guaranteed to be unique — some providers reuse "call_0" every round,
// and some send none at all. The table's UNIQUE(run_id, tool_call_id) turned
// that into a failed INSERT, which aborted the run and told the user a write
// might have half-landed when in fact nothing had run yet. So a collision is
// resolved by suffixing rather than by failing.
func (s *Service) insertOperation(ctx context.Context, runID, toolCallID, toolName string, args json.RawMessage) (string, error) {
	if toolCallID == "" {
		toolCallID = "call"
	}
	candidate := toolCallID
	for attempt := 0; attempt < 8; attempt++ {
		tag, err := s.Store.Exec(ctx,
			`INSERT INTO assistant_operation(run_id,tool_call_id,tool_name,arguments,status) VALUES($1,$2,$3,$4,'pending') ON CONFLICT (run_id,tool_call_id) DO NOTHING`,
			runID, candidate, toolName, args)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() == 1 {
			return candidate, nil
		}
		candidate = toolCallID + "#" + strconv.Itoa(attempt+2)
	}
	return "", errors.New("assistant: could not record a receipt for this tool call")
}

func (s *Service) execute(ctx context.Context, userID, sessionID string, call llm.ToolCall) (json.RawMessage, error) {
	if s.Exec == nil {
		return toolError("this tool is unavailable"), errors.New("tool unavailable")
	}
	args := json.RawMessage(strings.TrimSpace(call.Arguments))
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	toolCtx, cancel := context.WithTimeout(ctx, ToolCallTimeout)
	defer cancel()

	result, err := s.Exec.Execute(toolCtx, userID, sessionID, call.Name, args)
	if err != nil {
		return toolError(err.Error()), err
	}
	if len(result) == 0 {
		return json.RawMessage("{}"), nil
	}
	return result, nil
}

func toolError(msg string) json.RawMessage {
	out, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		return json.RawMessage(`{"error":"tool failed"}`)
	}
	return out
}

// ---------------------------------------------------------------------------
// History + context
// ---------------------------------------------------------------------------

// history loads the tail of the transcript in model order.
func (s *Service) history(ctx context.Context, sessionUUID pgtype.UUID) ([]llm.Message, error) {
	rows, err := s.Queries.ListRecentAssistantMessages(ctx, db.ListRecentAssistantMessagesParams{
		SessionID: sessionUUID,
		Limit:     HistoryLimit,
	})
	if err != nil {
		return nil, err
	}

	// The query is newest-first (that is how a LIMIT tail is taken); the model
	// needs oldest-first.
	msgs := make([]llm.Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		msgs = append(msgs, rowToMessage(rows[i]))
	}
	return repairToolHistory(msgs), nil
}

func rowToMessage(row db.AssistantMessage) llm.Message {
	m := llm.Message{Role: row.Role, Content: row.Content}
	if row.ToolCallID.Valid {
		m.ToolCallID = row.ToolCallID.String
	}
	if len(row.ToolCalls) > 0 {
		// A tool_calls blob that no longer parses is corrupt history, not a
		// reason to fail the run: drop the calls and keep the text.
		if err := json.Unmarshal(row.ToolCalls, &m.ToolCalls); err != nil {
			slog.Warn("assistant: unreadable tool_calls blob", "message_id", util.UUIDToString(row.ID), "error", err)
			m.ToolCalls = nil
		}
	}
	return m
}

// trimDanglingToolMessages drops tool answers at the head of the window whose
// requesting assistant turn was truncated away. Anthropic rejects a tool_result
// with no preceding tool_use outright, and every provider is confused by one.
func trimDanglingToolMessages(msgs []llm.Message) []llm.Message {
	cut := 0
	for cut < len(msgs) && msgs[cut].Role == "tool" {
		cut++
	}
	return msgs[cut:]
}

// userContext loads who is asking and what they have access to.
func (s *Service) userContext(ctx context.Context, userID string, focus pgtype.UUID) (UserContext, error) {
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return UserContext{}, err
	}

	uc := UserContext{}
	if focus.Valid {
		uc.FocusWorkspaceID = util.UUIDToString(focus)
	}

	user, err := s.Queries.GetUser(ctx, userUUID)
	if err != nil {
		return UserContext{}, err
	}
	uc.Name = user.Name
	if user.Language.Valid {
		uc.Language = user.Language.String
	}

	workspaces, err := s.Queries.ListWorkspaces(ctx, userUUID)
	if err != nil {
		return UserContext{}, err
	}
	for _, ws := range workspaces {
		role := ""
		member, merr := s.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      userUUID,
			WorkspaceID: ws.ID,
		})
		if merr == nil {
			role = member.Role
		}
		uc.Workspaces = append(uc.Workspaces, WorkspaceRef{
			ID:   util.UUIDToString(ws.ID),
			Slug: ws.Slug,
			Name: ws.Name,
			Role: role,
		})
	}
	return uc, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Service) publish(eventType string, payload any) {
	if s.Bus == nil {
		return
	}
	s.Bus.Publish(events.Event{
		Type:      eventType,
		ActorType: "system",
		Payload:   payload,
	})
}

// terminalFromContext maps a dead context onto a run status. A deliberate
// cancel is not a failure; a blown deadline is.
func terminalFromContext(ctx context.Context) (string, bool) {
	switch ctx.Err() {
	case nil:
		return "", false
	case context.Canceled:
		return RunStatusCancelled, true
	default:
		return RunStatusFailed, true
	}
}

func contextErrorMessage(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "the assistant took too long and was stopped"
	}
	return ""
}

// DeriveSessionTitle turns the first user message into a session title. Plain
// truncation on a word boundary — naming a session with the model would spend
// a completion on something the user can rename in one click.
func DeriveSessionTitle(content string) string {
	title := strings.Join(strings.Fields(content), " ")
	if len(title) <= MaxSessionTitleLen {
		return title
	}
	trimmed := title[:MaxSessionTitleLen]
	if idx := strings.LastIndex(trimmed, " "); idx > MaxSessionTitleLen/2 {
		trimmed = trimmed[:idx]
	}
	return strings.TrimSpace(trimmed) + "…"
}
