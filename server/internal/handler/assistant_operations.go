package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// CONFIRMATION BINDING — the authorization half of the assistant's write path.
//
// The rule this file exists to enforce (docs/agora-assistant-final-plan.md §3):
// a model-supplied `confirm: true` is NOT a human confirmation. It is a token
// the same model that wants the deletion also writes, so it proves nothing
// about a person having read what is about to happen. The only thing that does
// is an out-of-band gesture the model cannot produce: an authenticated HTTP
// request from the session's owner, arriving from a button in the transcript.
//
// The shape:
//
//	model calls delete_issue
//	  → the tool resolves its target under the usual read gate
//	  → assistantAwaitConfirmation persists a PENDING OPERATION with a summary
//	    built from that resolved data, and answers needs_confirmation
//	  → NOTHING has mutated; the run continues and the model relays the ask
//	  → the user presses Confirm
//	  → POST /operations/{id}/confirm re-checks ownership, expiry, single-use,
//	    then replays the STORED tool call through the same executor — which
//	    re-resolves the target, re-runs membership and the router's role
//	    middleware, and compares the target against the one that was confirmed
//	  → a role="tool" receipt message lands on the transcript and the normal
//	    assistant:message event fires
//
// Two records, two jobs — they are deliberately not one table:
//
//   - assistant_pending_operation (migration 198) is the PRE-AUTHORIZATION.
//     Written before anything happens, it holds intent, the exact arguments a
//     confirmation would authorize, the resolved target, and the state of the
//     human decision (pending / confirmed / rejected / expired). It is
//     single-use: the CAS in ResolveAssistantPendingOperation is what makes a
//     double-click execute once.
//   - assistant_operation (migration 197) is the EXECUTION RECEIPT. The run
//     loop writes one per mutating tool call with the outcome
//     (succeeded / failed / uncertain). A confirmed operation produces rows in
//     BOTH, and that is correct rather than duplicated: the first call is
//     recorded there as an attempt that was parked (its stored result is the
//     needs_confirmation payload), and the confirmed execution is recorded as a
//     second row, keyed to the run that asked, under tool_call_id "op_<id>".
//
// Nothing here trusts the model with anything: the tool name and arguments come
// from the stored row, never from the confirm request body.

// assistantPendingOperationTTL mirrors the DEFAULT on
// assistant_pending_operation.expires_at. A confirmation the user comes back to
// half an hour later is one they no longer remember the wording of, and the
// world it described has moved on.
const assistantPendingOperationTTL = 30 * time.Minute

// assistantReceiptTag prefixes the synthetic tool_call_id of a receipt message.
//
// A receipt is a role="tool" transcript row with no model tool_use behind it —
// the model asked once, hours ago, and a human answered with a button. The
// prefix guarantees the id can never collide with a provider-generated call id,
// and repairToolHistory (assistant/history.go) drops tool rows whose requesting
// turn is absent, so an unmatched receipt can never be sent to a provider as a
// dangling tool_result.
const assistantReceiptTag = "op_"

// ---------------------------------------------------------------------------
// The wire contract
// ---------------------------------------------------------------------------

// assistantOperationTarget is what the confirmation is ABOUT. It is pinned in
// the pending row and re-derived at execution time; a mismatch invalidates the
// confirmation rather than redirecting it at whatever now answers to the name.
type assistantOperationTarget struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
}

// assistantOperationPayload is the `operation` object of the needs_confirmation
// tool result, and the body of the confirm/reject/read endpoints.
type assistantOperationPayload struct {
	ID            string                   `json:"id"`
	ToolName      string                   `json:"tool_name"`
	Summary       string                   `json:"summary"`
	WorkspaceSlug string                   `json:"workspace_slug"`
	Target        assistantOperationTarget `json:"target"`
	Status        string                   `json:"status,omitempty"`
	CreatedAt     string                   `json:"created_at,omitempty"`
	ExpiresAt     string                   `json:"expires_at,omitempty"`
}

// assistantOperationPlan is what a destructive tool hands the seam once it has
// resolved its target and BEFORE it mutates anything.
type assistantOperationPlan struct {
	Tool      string
	Summary   string
	Workspace db.Workspace
	Target    assistantOperationTarget
}

// ---------------------------------------------------------------------------
// Per-execution state
// ---------------------------------------------------------------------------

type assistantExecutionKey struct{}
type assistantConfirmationKey struct{}

// assistantConfirmation is a consumed human authorization, carried into the
// executor by the confirm endpoint. It is created in exactly one place and
// never from anything a model or a request body said.
type assistantConfirmation struct {
	OperationID string
	ToolName    string
	UserID      string
	Target      assistantOperationTarget
}

// assistantExecution is the mutable state of one Execute call. It is a pointer
// in the context because two things deep in the call stack have to report
// upward through code that has no return path for them: the invoke helper
// noticing a dispatched request whose outcome is unknown, and the confirmation
// seam recording that this execution is authorized.
type assistantExecution struct {
	tool        string
	sessionID   string
	destructive bool
	confirmed   *assistantConfirmation
	// authorized flips only after assistantAwaitConfirmation has checked the
	// confirmation against THIS tool, THIS caller and THIS target.
	authorized bool
	// uncertain is non-empty when a mutation was dispatched and its outcome
	// could not be established. It names what to go and look at.
	uncertain string
}

func withAssistantConfirmation(ctx context.Context, c *assistantConfirmation) context.Context {
	return context.WithValue(ctx, assistantConfirmationKey{}, c)
}

func assistantConfirmationFrom(ctx context.Context) *assistantConfirmation {
	c, _ := ctx.Value(assistantConfirmationKey{}).(*assistantConfirmation)
	return c
}

func withAssistantExecution(ctx context.Context, e *assistantExecution) context.Context {
	return context.WithValue(ctx, assistantExecutionKey{}, e)
}

func assistantExecutionFrom(ctx context.Context) *assistantExecution {
	e, _ := ctx.Value(assistantExecutionKey{}).(*assistantExecution)
	return e
}

// ---------------------------------------------------------------------------
// The seam every destructive tool calls
// ---------------------------------------------------------------------------

// assistantAwaitConfirmation is the one gate between a resolved destructive
// intent and the mutation that carries it out.
//
// Contract for callers: invoke it AFTER resolving the target (so the summary
// names the real thing) and BEFORE the first write. Then
//
//	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, plan)
//	if out != nil || err != nil {
//	    return out, err
//	}
//
// A nil/nil answer means a human authorization is bound to exactly this call
// and the tool may proceed. Anything else is the tool's answer: the
// needs_confirmation payload, or a refusal.
//
// Forgetting the call is not a silent hole: assistantInvokeAs refuses to
// dispatch any handler for a destructive tool that has not passed through here
// (see the backstop there), and the meta-test over DestructiveTools fails.
func (h *Handler) assistantAwaitConfirmation(
	ctx context.Context,
	caller assistantCaller,
	args json.RawMessage,
	plan assistantOperationPlan,
) (json.RawMessage, error) {
	exec := assistantExecutionFrom(ctx)
	if exec == nil {
		// Execute always installs one. Reaching this means a tool was called
		// around the executor, which must never be allowed to mutate.
		return nil, errors.New("this action cannot run outside the assistant executor")
	}

	if confirmed := exec.confirmed; confirmed != nil {
		if confirmed.ToolName != plan.Tool || confirmed.UserID != caller.ID {
			return nil, errors.New("that confirmation does not authorize this action")
		}
		if confirmed.Target != plan.Target {
			// §3: "Changed targets or material argument changes invalidate old
			// confirmation." The user read a card naming one thing; executing
			// against another is exactly the failure this whole file exists to
			// make impossible.
			return nil, fmt.Errorf(
				"the %s changed since this was confirmed (it now reads %q, the confirmation was for %q) — ask again",
				plan.Target.Type, assistantTargetLabel(plan.Target), assistantTargetLabel(confirmed.Target))
		}
		exec.authorized = true
		return nil, nil
	}

	sessionUUID, err := util.ParseUUID(exec.sessionID)
	if err != nil {
		// No conversation means no transcript to render a card in, so there is
		// nowhere for the user to authorize this. Refusing is the only honest
		// answer; executing would be the unconfirmed delete this replaces.
		return nil, errors.New(plan.Tool + " needs a conversation to ask for confirmation in")
	}

	arguments := args
	if len(arguments) == 0 || !json.Valid(arguments) {
		arguments = json.RawMessage(`{}`)
	}
	target, err := json.Marshal(plan.Target)
	if err != nil {
		return nil, errors.New("could not describe what would be changed")
	}

	op, err := h.Queries.CreateAssistantPendingOperation(ctx, db.CreateAssistantPendingOperationParams{
		RunID:       h.assistantActiveRun(ctx, sessionUUID),
		SessionID:   sessionUUID,
		UserID:      caller.UUID,
		ToolName:    plan.Tool,
		Arguments:   arguments,
		Summary:     plan.Summary,
		WorkspaceID: plan.Workspace.ID,
		Target:      target,
	})
	if err != nil {
		slog.Warn("assistant: persist pending operation failed", "tool", plan.Tool, "error", err)
		return nil, errors.New("could not prepare that action for confirmation")
	}

	return json.Marshal(map[string]any{
		"status":    assistant.StatusNeedsConfirmation,
		"operation": assistantOperationPayloadFor(op, plan.Workspace.Slug),
	})
}

// assistantTargetLabel is how a target reads in a sentence.
func assistantTargetLabel(t assistantOperationTarget) string {
	switch {
	case t.Identifier != "" && t.Title != "":
		return t.Identifier + " " + t.Title
	case t.Title != "":
		return t.Title
	default:
		return t.Identifier
	}
}

func assistantOperationPayloadFor(op db.AssistantPendingOperation, workspaceSlug string) assistantOperationPayload {
	payload := assistantOperationPayload{
		ID:            uuidToString(op.ID),
		ToolName:      op.ToolName,
		Summary:       op.Summary,
		WorkspaceSlug: workspaceSlug,
		Status:        op.Status,
		CreatedAt:     timestampToString(op.CreatedAt),
		ExpiresAt:     timestampToString(op.ExpiresAt),
	}
	// A target that no longer parses renders as an empty one rather than
	// failing the read: the summary still says what the operation was.
	_ = json.Unmarshal(op.Target, &payload.Target)
	return payload
}

// assistantActiveRun links a pending operation to the run that proposed it, so
// the execution receipt can be filed against that run. Best-effort: a tool
// executed outside a run (a test, a future non-conversational caller) simply
// gets a NULL run_id.
func (h *Handler) assistantActiveRun(ctx context.Context, sessionUUID pgtype.UUID) pgtype.UUID {
	if h.DB == nil {
		return pgtype.UUID{}
	}
	var runID string
	err := h.DB.QueryRow(ctx,
		`SELECT id::text FROM assistant_run WHERE session_id=$1 AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`,
		sessionUUID).Scan(&runID)
	if err != nil {
		return pgtype.UUID{}
	}
	parsed, err := util.ParseUUID(runID)
	if err != nil {
		return pgtype.UUID{}
	}
	return parsed
}

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

// loadAssistantPendingOperation resolves an operation the caller owns.
//
// Someone else's operation is reported as NOT FOUND rather than forbidden, for
// the same reason a session is: these rows are user-private, and acknowledging
// one exists would already leak that a teammate was about to delete something.
func (h *Handler) loadAssistantPendingOperation(w http.ResponseWriter, r *http.Request, userID string) (db.AssistantPendingOperation, bool) {
	opUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "assistant operation id")
	if !ok {
		return db.AssistantPendingOperation{}, false
	}
	op, err := h.Queries.GetAssistantPendingOperation(r.Context(), opUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "assistant operation not found")
		return db.AssistantPendingOperation{}, false
	}
	if uuidToString(op.UserID) != userID {
		writeError(w, http.StatusNotFound, "assistant operation not found")
		return db.AssistantPendingOperation{}, false
	}
	// Read-time expiry. There is no sweeper: an operation nobody looks at
	// again never needs a status, and the CAS in ResolveAssistantPendingOperation
	// refuses an expired row anyway. Recording it here just stops the row from
	// reporting "pending" forever once somebody has asked.
	if op.Status == "pending" && op.ExpiresAt.Valid && !op.ExpiresAt.Time.After(time.Now()) {
		if err := h.Queries.ExpireAssistantPendingOperation(r.Context(), op.ID); err != nil {
			slog.Warn("assistant: expire pending operation failed", "operation_id", uuidToString(op.ID), "error", err)
		}
		op.Status = "expired"
	}
	return op, true
}

// assistantOperationWorkspaceSlug resolves the slug for the card. An empty
// string is a fine answer — the summary is what the user reads.
func (h *Handler) assistantOperationWorkspaceSlug(ctx context.Context, op db.AssistantPendingOperation) string {
	if !op.WorkspaceID.Valid {
		return ""
	}
	ws, err := h.Queries.GetWorkspace(ctx, op.WorkspaceID)
	if err != nil {
		return ""
	}
	return ws.Slug
}

// GetAssistantOperation reports the current state of one pending operation, so
// a transcript reloaded hours later renders a live card as live and a spent one
// as spent.
func (h *Handler) GetAssistantOperation(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	op, ok := h.loadAssistantPendingOperation(w, r, userID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, assistantOperationPayloadFor(op, h.assistantOperationWorkspaceSlug(r.Context(), op)))
}

// ListAssistantSessionOperations lists the confirmation cards of one session.
func (h *Handler) ListAssistantSessionOperations(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}
	ops, err := h.Queries.ListAssistantPendingOperations(r.Context(), db.ListAssistantPendingOperationsParams{
		SessionID: session.ID,
		Limit:     assistantSessionListLimit,
	})
	if err != nil {
		slog.Warn("assistant: list pending operations failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list assistant operations")
		return
	}
	resp := make([]assistantOperationPayload, 0, len(ops))
	for _, op := range ops {
		if op.Status == "pending" && op.ExpiresAt.Valid && !op.ExpiresAt.Time.After(time.Now()) {
			op.Status = "expired"
		}
		resp = append(resp, assistantOperationPayloadFor(op, h.assistantOperationWorkspaceSlug(r.Context(), op)))
	}
	writeJSON(w, http.StatusOK, resp)
}

// AssistantOperationResponse is what confirm answers with: the operation in its
// new state plus the receipt message that landed on the transcript.
type AssistantOperationResponse struct {
	Operation assistantOperationPayload `json:"operation"`
	Message   AssistantMessageResponse  `json:"message"`
}

// ConfirmAssistantOperation is the human authorization, and the only path that
// executes a destructive assistant tool.
//
// The claim happens BEFORE the execution, and deliberately: the status column
// records the HUMAN DECISION, not the outcome. Claiming first is what makes a
// double-click, a retried request or two open tabs execute exactly once; the
// outcome of the execution lives in the receipt message and in the durable
// assistant_operation row. A confirmation that was consumed by a failed
// execution is spent — the user asks again and gets a fresh card, which is the
// correct behaviour for an authorization that must never be replayable.
func (h *Handler) ConfirmAssistantOperation(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	op, ok := h.loadAssistantPendingOperation(w, r, userID)
	if !ok {
		return
	}
	if op.Status != "pending" {
		writeError(w, http.StatusConflict, assistantOperationStateMessage(op.Status))
		return
	}

	claimed, err := h.Queries.ResolveAssistantPendingOperation(r.Context(), db.ResolveAssistantPendingOperationParams{
		ID:     op.ID,
		Status: "confirmed",
	})
	if err != nil {
		// No row matched: something else confirmed, rejected or expired it
		// between the read above and this write.
		writeError(w, http.StatusConflict, "this action is no longer waiting for confirmation")
		return
	}

	var target assistantOperationTarget
	_ = json.Unmarshal(claimed.Target, &target)
	ctx := withAssistantConfirmation(r.Context(), &assistantConfirmation{
		OperationID: uuidToString(claimed.ID),
		ToolName:    claimed.ToolName,
		UserID:      userID,
		Target:      target,
	})

	// The SAME executor the run loop uses: membership, the non-owner visibility
	// gate and the router's role middleware all run again, against the roles the
	// caller holds NOW. A member demoted between the ask and the click is
	// refused here, by the product's own refusal.
	result, execErr := h.Execute(ctx, userID, uuidToString(claimed.SessionID), claimed.ToolName, claimed.Arguments)

	slug := h.assistantOperationWorkspaceSlug(r.Context(), claimed)
	toolResult := result
	if execErr != nil {
		toolResult = assistantOperationRefusal(claimed, slug, execErr)
	}
	message, ok := h.recordAssistantOperationMessage(r.Context(), claimed, toolResult)
	if !ok {
		writeError(w, http.StatusInternalServerError, "the action ran but its receipt could not be saved")
		return
	}
	h.recordAssistantExecutionReceipt(r.Context(), claimed, toolResult, execErr)

	if execErr != nil {
		writeError(w, http.StatusConflict, execErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, AssistantOperationResponse{
		Operation: assistantOperationPayloadFor(claimed, slug),
		Message:   message,
	})
}

// RejectAssistantOperation is Cancel. Nothing ran, and the transcript says so
// in the same place the card is, so a conversation reread later cannot be
// mistaken for one where the deletion went ahead.
func (h *Handler) RejectAssistantOperation(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	op, ok := h.loadAssistantPendingOperation(w, r, userID)
	if !ok {
		return
	}
	if op.Status != "pending" {
		writeError(w, http.StatusConflict, assistantOperationStateMessage(op.Status))
		return
	}
	rejected, err := h.Queries.ResolveAssistantPendingOperation(r.Context(), db.ResolveAssistantPendingOperationParams{
		ID:     op.ID,
		Status: "rejected",
	})
	if err != nil {
		writeError(w, http.StatusConflict, "this action is no longer waiting for confirmation")
		return
	}

	slug := h.assistantOperationWorkspaceSlug(r.Context(), rejected)
	cancelled, merr := json.Marshal(map[string]any{
		"status":    "cancelled",
		"cancelled": true,
		"operation": assistantOperationPayloadFor(rejected, slug),
		"note":      "The user cancelled this action. Nothing was changed. Do not call the tool again unless they ask.",
	})
	if merr != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel the action")
		return
	}
	if _, ok := h.recordAssistantOperationMessage(r.Context(), rejected, cancelled); !ok {
		writeError(w, http.StatusInternalServerError, "failed to cancel the action")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func assistantOperationStateMessage(status string) string {
	switch status {
	case "confirmed":
		return "this action was already confirmed"
	case "rejected":
		return "this action was already cancelled"
	case "expired":
		return "this confirmation has expired — ask the assistant again"
	default:
		return "this action is no longer waiting for confirmation"
	}
}

// assistantOperationRefusal is the transcript row for a confirmed operation the
// executor then refused. It carries no receipt: nothing happened.
func assistantOperationRefusal(op db.AssistantPendingOperation, slug string, execErr error) json.RawMessage {
	out, err := json.Marshal(map[string]any{
		"error":     execErr.Error(),
		"confirmed": true,
		"operation": assistantOperationPayloadFor(op, slug),
	})
	if err != nil {
		return json.RawMessage(`{"error":"the confirmed action could not be carried out"}`)
	}
	return out
}

// recordAssistantOperationMessage persists the role="tool" receipt and fires
// the same assistant:message event a tool answer inside a run fires, so every
// open client renders it without knowing this path exists.
func (h *Handler) recordAssistantOperationMessage(ctx context.Context, op db.AssistantPendingOperation, toolResult json.RawMessage) (AssistantMessageResponse, bool) {
	if len(toolResult) == 0 {
		toolResult = json.RawMessage(`{}`)
	}
	message, err := h.Queries.CreateAssistantMessage(ctx, db.CreateAssistantMessageParams{
		SessionID:  op.SessionID,
		Role:       "tool",
		Content:    string(toolResult),
		ToolCallID: strToText(assistantReceiptTag + uuidToString(op.ID)),
		ToolName:   strToText(op.ToolName),
		ToolResult: toolResult,
	})
	if err != nil {
		slog.Error("assistant: persist operation receipt failed",
			"operation_id", uuidToString(op.ID), "tool", op.ToolName, "error", err)
		return AssistantMessageResponse{}, false
	}
	resp := assistantMessageToResponse(message)
	if h.Bus != nil {
		h.Bus.Publish(events.Event{
			Type:      protocol.EventAssistantMessage,
			ActorType: "member",
			ActorID:   uuidToString(op.UserID),
			Payload: protocol.AssistantMessagePayload{
				UserID:    uuidToString(op.UserID),
				SessionID: uuidToString(op.SessionID),
				RunID:     uuidToString(op.RunID),
				MessageID: resp.ID,
				Role:      "tool",
				ToolName:  op.ToolName,
				CreatedAt: resp.CreatedAt,
			},
		})
	}
	return resp, true
}

// recordAssistantExecutionReceipt files the outcome on assistant_operation —
// the DURABLE receipt table from migration 197 — against the run that proposed
// the operation. Best-effort and never fatal: the transcript message above is
// the user-visible record, this row is the recovery one.
//
// It is a SECOND row, not an edit of the one the run loop wrote when the model
// first called the tool. That first row's stored result is the
// needs_confirmation payload — an attempt that was parked — and overwriting it
// would erase the fact that the model asked before a human answered.
func (h *Handler) recordAssistantExecutionReceipt(ctx context.Context, op db.AssistantPendingOperation, result json.RawMessage, execErr error) {
	if h.DB == nil || !op.RunID.Valid {
		return
	}
	status := "succeeded"
	var errText any
	switch {
	case execErr != nil:
		status, errText = "failed", execErr.Error()
	case assistantResultStatus(result) == assistant.StatusUncertain:
		status = "uncertain"
	}
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO assistant_operation (run_id, tool_call_id, tool_name, arguments, status, result, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (run_id, tool_call_id) DO UPDATE
		SET status = EXCLUDED.status, result = EXCLUDED.result, error = EXCLUDED.error, updated_at = now()
	`, op.RunID, assistantReceiptTag+uuidToString(op.ID), op.ToolName, op.Arguments, status, result, errText); err != nil {
		slog.Warn("assistant: persist execution receipt failed",
			"operation_id", uuidToString(op.ID), "error", err)
	}
}

// assistantResultStatus reads the top-level "status" of a tool result, if any.
func assistantResultStatus(result json.RawMessage) string {
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return ""
	}
	return out.Status
}

// ---------------------------------------------------------------------------
// Receipts on ordinary mutations
// ---------------------------------------------------------------------------

// assistantToolTargets names the kind of thing each mutating tool acts on.
//
// It is the only per-tool knowledge the receipt builder has; everything else is
// read out of the answer the tool already returns, so a tool that changes its
// response shape degrades to a thinner receipt rather than a wrong one.
var assistantToolTargets = map[string]string{
	assistant.ToolCreateIssue:          "issue",
	assistant.ToolUpdateIssue:          "issue",
	assistant.ToolCommentIssue:         "issue",
	assistant.ToolArchiveIssue:         "issue",
	assistant.ToolAddIssueLabel:        "issue",
	assistant.ToolRemoveIssueLabel:     "issue",
	assistant.ToolMoveIssueToSprint:    "issue",
	assistant.ToolSubscribeIssue:       "issue",
	assistant.ToolDeleteIssue:          "issue",
	assistant.ToolCreateProject:        "project",
	assistant.ToolUpdateProject:        "project",
	assistant.ToolDeleteProject:        "project",
	assistant.ToolCreateSprint:         "sprint",
	assistant.ToolDeleteSprint:         "sprint",
	assistant.ToolCreateLabel:          "label",
	assistant.ToolDeleteLabel:          "label",
	assistant.ToolUpdateComment:        "comment",
	assistant.ToolResolveComment:       "comment",
	assistant.ToolDeleteComment:        "comment",
	assistant.ToolCreateAgent:          "agent",
	assistant.ToolUpdateAgent:          "agent",
	assistant.ToolAttachSkillToAgent:   "agent",
	assistant.ToolAddSkill:             "skill",
	assistant.ToolInviteMember:         "member",
	assistant.ToolUpdateMemberRole:     "member",
	assistant.ToolRemoveMember:         "member",
	assistant.ToolCreateWorkspace:      "workspace",
	assistant.ToolUpdateWorkspace:      "workspace",
	assistant.ToolLeaveWorkspace:       "workspace",
	assistant.ToolDeleteWorkspace:      "workspace",
	assistant.ToolCreateAutopilot:      "autopilot",
	assistant.ToolUpdateAutopilot:      "autopilot",
	assistant.ToolRunAutopilotNow:      "autopilot",
	assistant.ToolCreateAutomation:     "automation",
	assistant.ToolSetAutomationEnabled: "automation",
	assistant.ToolDeleteAutomation:     "automation",
	assistant.ToolMarkInboxRead:        "inbox",
	assistant.ToolPinItem:              "item",
	assistant.ToolCreateArtifact:       "artifact",
	assistant.ToolUpdateArtifact:       "artifact",
}

// assistantAttachReceipt answers the user's second question — "what did you
// change?" — in the tool result itself, so the UI and the model both read the
// same record instead of re-deriving it from prose.
//
// It runs at the Execute seam rather than in forty tools, and it invents
// nothing: the action is the tool name, the target and links come out of the
// fields the tool already returned, and effects are only the state flips the
// response actually encodes.
func assistantAttachReceipt(tool string, result json.RawMessage) json.RawMessage {
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil || out == nil {
		return result
	}
	// Nothing happened yet (or may not have happened): a receipt would be a lie.
	switch assistantResultStatus(result) {
	case assistant.StatusNeedsConfirmation, assistant.StatusUncertain:
		return result
	}
	if _, failed := out["error"]; failed {
		return result
	}
	if _, already := out["receipt"]; already {
		return result
	}

	targetType := assistantToolTargets[tool]
	target := assistantOperationTarget{Type: targetType}
	links := []string{}

	if nested, ok := out[targetType].(map[string]any); ok {
		target.Identifier = assistantFirstString(nested, "identifier", "slug", "id")
		target.Title = assistantFirstString(nested, "title", "name")
		if link := assistantFirstString(nested, "url_path"); link != "" {
			links = append(links, link)
		}
	} else if name := assistantFirstString(out, targetType); name != "" {
		// Tools that name their target inline: {"deleted":true,"project":"X"}.
		target.Title = name
	}
	if target.Identifier == "" {
		target.Identifier = assistantFirstString(out,
			"issue_identifier", "identifier", targetType+"_id",
			"comment_id", "item_id", "user_id", "artifact_id", "invitation_id",
			"workspace_slug", "id")
	}
	if target.Title == "" {
		target.Title = assistantFirstString(out, "title", "name", "email", "workspace_name", "item")
	}
	if link := assistantFirstString(out, "url_path"); link != "" {
		links = append(links, link)
	}

	out["receipt"] = map[string]any{
		"action":  tool,
		"target":  target,
		"links":   links,
		"effects": assistantReceiptEffects(tool, out),
	}
	decorated, err := json.Marshal(out)
	if err != nil {
		return result
	}
	return decorated
}

// assistantReceiptEffects lists the downstream consequences the RESPONSE
// already encodes. Anything the response does not say is not listed — a receipt
// that guesses is worse than a short one.
func assistantReceiptEffects(tool string, out map[string]any) []string {
	effects := []string{}
	add := func(s string) { effects = append(effects, s) }
	flag := func(key string) (bool, bool) {
		v, ok := out[key].(bool)
		return v, ok
	}

	switch tool {
	case assistant.ToolArchiveIssue:
		if archived, ok := flag("archived"); ok {
			if archived {
				add("moved to the archive")
			} else {
				add("restored from the archive")
			}
		}
	case assistant.ToolResolveComment:
		if resolved, ok := flag("resolved"); ok {
			if resolved {
				add("thread marked resolved")
			} else {
				add("thread reopened")
			}
		}
	case assistant.ToolSubscribeIssue:
		if subscribed, ok := flag("subscribed"); ok {
			if subscribed {
				add("you now get updates on this issue")
			} else {
				add("you no longer get updates on this issue")
			}
		}
	case assistant.ToolPinItem:
		if pinned, ok := flag("pinned"); ok {
			if pinned {
				add("pinned to the sidebar")
			} else {
				add("unpinned from the sidebar")
			}
		}
	case assistant.ToolAddIssueLabel, assistant.ToolRemoveIssueLabel:
		if label := assistantFirstString(out, "label"); label != "" {
			if _, added := out["label_added"]; added {
				add("label " + label + " added")
			} else {
				add("label " + label + " removed")
			}
		}
	case assistant.ToolMoveIssueToSprint:
		if _, removed := out["removed_from_sprint"]; removed {
			add("taken out of its sprint")
		} else if sprint := assistantFirstString(out, "sprint"); sprint != "" {
			add("now in sprint " + sprint)
		}
	case assistant.ToolUpdateMemberRole:
		if previous := assistantFirstString(out, "previous_role"); previous != "" {
			add("role changed from " + previous + " to " + assistantFirstString(out, "role"))
		}
	case assistant.ToolInviteMember:
		if email := assistantFirstString(out, "email"); email != "" {
			add("invitation email sent to " + email)
		}
	case assistant.ToolSetAutomationEnabled:
		if enabled, ok := flag("enabled"); ok {
			if enabled {
				add("the rule now fires on its own")
			} else {
				add("the rule no longer fires")
			}
		}
	case assistant.ToolCreateAutomation:
		add("the rule now fires on its own")
	case assistant.ToolMarkInboxRead:
		if count, ok := out["items_marked"].(float64); ok {
			add(fmt.Sprintf("%d inbox items marked read", int(count)))
		}
	case assistant.ToolAttachSkillToAgent:
		if skill := assistantFirstString(out, "skill"); skill != "" {
			add("skill " + skill + " is now available to the agent")
		}
	}
	return effects
}

// assistantFirstString returns the first of `keys` whose value is a non-empty
// string.
func assistantFirstString(out map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := out[key].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Uncertain outcomes
// ---------------------------------------------------------------------------

// assistantUncertainResult is what a dispatched-but-unresolved mutation answers
// with. Deliberately NOT an error: an error reads to the model as "that did not
// work", and the correction to "that did not work" is a retry — which is
// exactly the one thing that must not happen to a write that may already have
// landed (the plan's "do not automatically replay it without reconciliation").
func assistantUncertainResult(inspect string) json.RawMessage {
	out, err := json.Marshal(map[string]any{
		"status":  assistant.StatusUncertain,
		"inspect": inspect,
	})
	if err != nil {
		return json.RawMessage(`{"status":"uncertain","inspect":"check whether the change landed before trying again"}`)
	}
	return out
}

// assistantMarkUncertain records that a request was dispatched and its outcome
// is unknown. Called from assistantInvokeAs, which is the only place in the
// assistant write path where a request crosses into a real handler.
func assistantMarkUncertain(ctx context.Context, tool, reason string) {
	exec := assistantExecutionFrom(ctx)
	if exec == nil || exec.uncertain != "" {
		return
	}
	if tool == "" {
		tool = "the action"
	}
	exec.uncertain = tool + " was sent to the server but was interrupted before its result came back (" +
		reason + "). It MAY have taken effect. Open the item in Agora and check whether the change is there " +
		"before trying it again."
}
