package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	Queries   *db.Queries
	Store     runDB
	TxStarter runTxStarter
	Bus       *events.Bus
	Client    ClientFactory
	Exec      ToolExecutor

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
	if s.Store != nil {
		var workspace *string
		var timezone string
		if err := s.Store.QueryRow(ctx, `SELECT context_workspace_id::text, context_timezone FROM assistant_run WHERE id=$1 AND user_id=$2`, runID, userID).Scan(&workspace, &timezone); err != nil {
			return RunStatusFailed, "could not load this message's workspace context"
		}
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
	uc.Timezone = runTimezone
	system := llm.Message{Role: "system", Content: buildSystemPrompt(uc, session.Summary)}
	tools := ToolSpecs()

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
			if !s.runToolCall(ctx, sessionUUID, sessionID, runID, userID, call) {
				if s.Store != nil {
					var cancelled bool
					if s.Store.QueryRow(ctx, `SELECT cancel_requested FROM assistant_run WHERE id=$1`, runID).Scan(&cancelled) == nil && cancelled {
						return RunStatusCancelled, ""
					}
				}
				return RunStatusFailed, "a write may have succeeded but its result could not be saved; review the affected item before retrying"
			}
		}
	}

	// Round cap reached with the model still asking for tools. This is a real
	// outcome, not an error: the transcript holds everything gathered so far.
	return RunStatusFailed, "the assistant used too many steps without reaching an answer"
}

// runToolCall executes one tool and persists its answer as a role="tool" turn.
// It never returns an error: a failing tool becomes an {"error": ...} result
// the model reads and can recover from, which is strictly better than killing
// a run the user is waiting on.
func (s *Service) runToolCall(ctx context.Context, sessionUUID pgtype.UUID, sessionID, runID, userID string, call llm.ToolCall) bool {
	mutating := IsMutating(call.Name) && s.Store != nil
	if s.Store != nil {
		s.mu.Lock()
		owner := ""
		if h := s.runs[runID]; h != nil {
			owner = h.owner
		}
		s.mu.Unlock()
		tag, err := s.Store.Exec(ctx, `UPDATE assistant_run SET active_tool=$3,updated_at=now(),version=version+1 WHERE id=$1 AND lease_owner=$2 AND status='running' AND cancel_requested=false AND lease_expires_at>now()`, runID, owner, call.Name)
		if err != nil || tag.RowsAffected() != 1 {
			return false
		}
	}
	if mutating {
		args := json.RawMessage(strings.TrimSpace(call.Arguments))
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		_, err := s.Store.Exec(ctx, `INSERT INTO assistant_operation(run_id,tool_call_id,tool_name,arguments,status) VALUES($1,$2,$3,$4,'pending')`, runID, call.ID, call.Name, args)
		if err != nil {
			return false
		}
	}
	if ctx.Err() != nil {
		return false
	}
	if s.Store != nil {
		var allowed bool
		if err := s.Store.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assistant_run WHERE id=$1 AND status='running' AND cancel_requested=false AND lease_expires_at>now())`, runID).Scan(&allowed); err != nil || !allowed {
			return false
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
	if mutating {
		final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
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
		opStatus := "succeeded"
		var toolResult map[string]json.RawMessage
		uncertainResult := false
		if json.Unmarshal(result, &toolResult) == nil {
			// An uncertain outcome carries no "error" key by design (an error
			// invites a retry, and a retry is the one thing a possibly-committed
			// write must not get), so it has to be recognised by status.
			var status string
			if raw, ok := toolResult["status"]; ok && json.Unmarshal(raw, &status) == nil {
				uncertainResult = status == StatusUncertain
			}
		}
		if executeErr != nil || toolResult["error"] != nil || uncertainResult {
			opStatus = "uncertain"
		}
		tag, err := s.Store.Exec(final, `UPDATE assistant_operation SET status=$4,result=$3,updated_at=now() WHERE run_id=$1 AND tool_call_id=$2 AND status='pending'`, runID, call.ID, result, opStatus)
		if err != nil || tag.RowsAffected() != 1 {
			return false
		}
		if opStatus == "uncertain" {
			return false
		}
	}

	msg, err := s.Queries.CreateAssistantMessage(ctx, db.CreateAssistantMessageParams{
		SessionID:  sessionUUID,
		Role:       "tool",
		Content:    string(result),
		ToolCallID: util.StrToText(call.ID),
		ToolName:   util.StrToText(call.Name),
		ToolResult: result,
	})
	if err != nil {
		slog.Warn("assistant: persist tool result failed", "session_id", sessionID, "tool", call.Name, "error", err)
		return false
	}
	if s.Store != nil {
		_, _ = s.Store.Exec(ctx, `UPDATE assistant_run SET active_tool=NULL,updated_at=now(),version=version+1 WHERE id=$1 AND status='running'`, runID)
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
	return true
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
