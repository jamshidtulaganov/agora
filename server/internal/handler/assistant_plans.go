package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// PLANS — one confirmation for several related writes.
//
// The problem this solves is not safety, it is speed. Confirmation binding
// (assistant_operations.go) made the assistant safe: nothing destructive runs
// without an out-of-band human click. The standing "one request, one write"
// rule made it careful. Together they made everyday management work — plan a
// sprint, triage an inbox, move the dozen issues that have been stuck in
// review — SLOWER through the assistant than through the UI, because each of
// the N writes is its own round trip and its own decision.
//
// A plan keeps both properties and removes the slowness:
//
//	model does the reads, then calls propose_plan with an ordered list
//	  → this file validates it against the ALLOWLIST and the catalog
//	  → ONE assistant_pending_operation row is persisted, tool_name
//	    "propose_plan", the items inside `arguments`
//	  → NOTHING has mutated; the transcript renders a checklist card
//	  → the user unchecks what they do not want and presses Confirm ONCE
//	  → the confirm endpoint replays the STORED items, in order, through the
//	    SAME executor a direct tool call uses — so every membership check,
//	    every role gate and every visibility rule fires per item, against the
//	    roles the caller holds at the moment of the click
//	  → per-item outcomes are recorded on the receipt and returned
//
// Three deliberate limits, each of which is the reason a plan is allowed to
// exist at all:
//
//   - The ALLOWLIST (assistant.PlanAllowedTools) is shorter than the catalog.
//     One gesture authorizing a list is a weaker gesture than one authorizing a
//     sentence, because the failure mode of a list is skimming it. So plans
//     carry recoverable everyday work only — never a delete, never an access
//     change, never standing machinery. It is checked at propose AND re-checked
//     at confirm, so an allowlist that shrinks between the two refuses the
//     stale items instead of running them.
//   - The CAP (assistant.MaxPlanItems) is a readability limit before it is a
//     safety one: nobody audits forty rows before pressing a button.
//   - STOP ON FIRST FAILURE. A plan is an ordered sequence a human read as a
//     whole; carrying on past a failed step would apply a plan nobody
//     authorized. Everything after the failure is reported `not_run`, which is
//     a different thing from `failed` and the user is entitled to that
//     distinction.
//
// No new table: a plan IS a pending operation, with the same pre-authorization
// / execution-receipt split migration 198 describes, generalized from one call
// to a list.

// assistantPlanTargetType is the target `type` of a plan row.
//
// A plan has no single target row, so the drift re-check that guards a single
// operation (assistantAwaitConfirmation: "the issue changed since this was
// confirmed") has nothing to compare against. That check is skipped for plans
// by construction rather than by a flag: the confirm path for a plan never
// re-enters the tool that proposed it, so it never reaches the comparison.
// Per-item safety comes from somewhere better — each item runs through the real
// executor, which re-resolves its own target and re-runs the caller's gates.
const assistantPlanTargetType = "plan"

// assistantPlanKind is the `kind` discriminator on the wire. It is present only
// on plan operations; a single operation carries no kind, exactly as before.
const assistantPlanKind = "plan"

// Per-item outcomes. The four values are the fixed wire contract.
const (
	assistantPlanItemOK      = "ok"
	assistantPlanItemFailed  = "failed"
	assistantPlanItemSkipped = "skipped"
	assistantPlanItemNotRun  = "not_run"
)

// Plan-level statuses, the headline the receipt card reads from.
const (
	assistantPlanCompleted = "completed"
	assistantPlanFailed    = "failed"
	assistantPlanUncertain = "uncertain"
)

// assistantPlanTotalTimeout bounds a whole plan.
//
// Each item gets the ordinary per-tool budget (assistant.ToolCallTimeout); this
// is the ceiling on the sequence, so a pathological plan cannot hold a
// connection open indefinitely. It is generous on purpose: a plan that stops
// halfway because of an impatient deadline is the worst outcome available.
const assistantPlanTotalTimeout = 6 * time.Minute

// assistantPlanErrorLimit truncates an item's error before it is stored and
// rendered. A handler's message is written for a human; a thousand-line one is
// not, and the receipt has to stay readable.
const assistantPlanErrorLimit = 500

// assistantPlanBodyLimit bounds the optional confirm body. It carries at most
// MaxPlanItems integers.
const assistantPlanBodyLimit = 4 << 10

// ---------------------------------------------------------------------------
// The wire shapes
// ---------------------------------------------------------------------------

// assistantPlanItemPayload is one row of the CARD: what the human reads before
// authorizing. The arguments are deliberately not on the wire — the summary is
// the thing a person can check, and the stored arguments are what actually
// runs.
type assistantPlanItemPayload struct {
	Index   int    `json:"index"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

// assistantPlanItemResult is one row of the RECEIPT: what happened.
type assistantPlanItemResult struct {
	Index      int    `json:"index"`
	Outcome    string `json:"outcome"`
	Identifier string `json:"identifier,omitempty"`
	Error      string `json:"error,omitempty"`
}

// assistantPlanResult is the confirm response for a plan, and the result JSON
// stored on the execution receipt so a reloaded transcript re-renders the same
// rows instead of an empty card.
type assistantPlanResult struct {
	Status string                    `json:"status"`
	Items  []assistantPlanItemResult `json:"items"`
}

// assistantConfirmRequest is the OPTIONAL confirm body. An absent or empty body
// means "run everything", which is byte-for-byte today's behaviour for every
// single operation and every plan confirmed without unchecking a row.
type assistantConfirmRequest struct {
	SkippedItems []int `json:"skipped_items"`
}

// ---------------------------------------------------------------------------
// propose_plan
// ---------------------------------------------------------------------------

// assistantProposePlan is the tool. It validates and parks; it never executes.
//
// It reuses assistantAwaitConfirmation — the same seam every destructive tool
// goes through — rather than writing its own pending row, so a plan inherits
// expiry, single-use, ownership and the needs_confirmation payload shape for
// free. The zero db.Workspace is what leaves workspace_id NULL: a plan's items
// may span workspaces, and each item's own membership check is what gates them.
func (h *Handler) assistantProposePlan(ctx context.Context, caller assistantCaller, args json.RawMessage) (json.RawMessage, error) {
	plan, err := assistant.ParsePlan(args)
	if err != nil {
		// Straight back to the model as a tool error: a rejected plan is one it
		// can rewrite, and the message names the item and the reason.
		return nil, err
	}

	// Persist the VALIDATED plan, not the raw blob: trimmed title, trimmed
	// tools and summaries, so the card and the execution read the same thing.
	stored, err := json.Marshal(plan)
	if err != nil {
		return nil, errors.New("could not record that plan")
	}

	return h.assistantAwaitConfirmation(ctx, caller, stored, assistantOperationPlan{
		Tool:    assistant.ToolProposePlan,
		Summary: plan.Title,
		Target: assistantOperationTarget{
			Type:  assistantPlanTargetType,
			Title: plan.Title,
		},
	})
}

// assistantPlanItemsOf decodes the stored items of a plan row.
//
// It is used by the card payload and by the execution, and both tolerate a row
// that no longer parses: the card degrades to a plan with no rows (the summary
// still says what it was), and the execution refuses rather than guessing.
func assistantPlanItemsOf(op db.AssistantPendingOperation) ([]assistant.PlanItem, error) {
	if op.ToolName != assistant.ToolProposePlan {
		return nil, errors.New("this operation is not a plan")
	}
	var plan assistant.Plan
	if err := json.Unmarshal(op.Arguments, &plan); err != nil {
		return nil, errors.New("this plan could not be read back")
	}
	if len(plan.Items) == 0 {
		return nil, errors.New("this plan has no items")
	}
	return plan.Items, nil
}

// assistantPlanCardItems is the items array of the needs_confirmation payload.
func assistantPlanCardItems(op db.AssistantPendingOperation) []assistantPlanItemPayload {
	items, err := assistantPlanItemsOf(op)
	if err != nil {
		return []assistantPlanItemPayload{}
	}
	out := make([]assistantPlanItemPayload, 0, len(items))
	for i, item := range items {
		out = append(out, assistantPlanItemPayload{Index: i, Tool: item.Tool, Summary: item.Summary})
	}
	return out
}

// ---------------------------------------------------------------------------
// Confirm
// ---------------------------------------------------------------------------

// assistantConfirmBody reads the optional confirm body.
//
// Absent, empty, or an empty JSON object all mean the same thing — run
// everything — because that is what every existing client sends and the
// existing behaviour must not change under them. Anything else that is not JSON
// is a 400: a body the server cannot read must never be silently treated as
// "run the whole plan".
func assistantConfirmBody(w http.ResponseWriter, r *http.Request) (assistantConfirmRequest, bool) {
	var req assistantConfirmRequest
	if r.Body == nil {
		return req, true
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, assistantPlanBodyLimit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return req, false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return req, true
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, true
}

// assistantPlanSkips validates the unchecked rows against the stored plan and
// turns them into a set.
//
// Out-of-range indexes are refused rather than ignored: the client is telling
// the server which rows the human unchecked, and a mismatch means the two are
// looking at different plans — which is exactly when running the rest would be
// wrong.
func assistantPlanSkips(w http.ResponseWriter, requested []int, count int) (map[int]bool, bool) {
	skips := make(map[int]bool, len(requested))
	for _, index := range requested {
		if index < 0 || index >= count {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("skipped_items refers to row %d, but this plan has %d", index, count))
			return nil, false
		}
		skips[index] = true
	}
	return skips, true
}

// assistantExecutePlan runs the authorized items in order, as the operating
// user, and returns the receipt.
//
// Every item goes through h.Execute — the SAME entry point a direct tool call
// uses — with its own execution state, so an item's membership check, role
// gate, visibility restriction and uncertain-outcome detection all behave
// exactly as they would if the model had called that tool on its own. An item
// targeting a workspace the user left fails THAT item; it is not an error of
// the endpoint and it does not touch the items before it.
func (h *Handler) assistantExecutePlan(ctx context.Context, userID, sessionID string, items []assistant.PlanItem, skips map[int]bool) assistantPlanResult {
	result := assistantPlanResult{Status: assistantPlanCompleted, Items: make([]assistantPlanItemResult, 0, len(items))}
	stopped := false

	for i, item := range items {
		switch {
		case skips[i]:
			result.Items = append(result.Items, assistantPlanItemResult{Index: i, Outcome: assistantPlanItemSkipped})
			continue
		case stopped:
			// Everything after a failure is NOT RUN, which is a different
			// statement from "failed" and the one the user needs to decide what
			// to do next.
			result.Items = append(result.Items, assistantPlanItemResult{Index: i, Outcome: assistantPlanItemNotRun})
			continue
		}

		// The allowlist is re-checked HERE, not only at propose time: the row
		// may have been sitting for half an hour, and a tool removed from the
		// allowlist in between must not execute on the strength of a
		// confirmation taken when it was still allowed.
		if !assistant.PlanAllows(item.Tool) || !assistant.IsCatalogTool(item.Tool) {
			result.Items = append(result.Items, assistantPlanItemResult{
				Index:   i,
				Outcome: assistantPlanItemFailed,
				Error:   item.Tool + " can no longer run inside a plan",
			})
			result.Status = assistantPlanFailed
			stopped = true
			continue
		}

		itemCtx, cancel := context.WithTimeout(ctx, assistant.ToolCallTimeout)
		itemCtx = withAssistantExecution(itemCtx, &assistantExecution{})
		out, err := h.Execute(itemCtx, userID, sessionID, item.Tool, item.Arguments)
		cancel()

		switch {
		case err != nil:
			result.Items = append(result.Items, assistantPlanItemResult{
				Index:   i,
				Outcome: assistantPlanItemFailed,
				Error:   assistantPlanTruncate(err.Error()),
			})
			result.Status = assistantPlanFailed
			stopped = true
		case assistantResultStatus(out) == assistant.StatusUncertain:
			// Dispatched, answer lost. The item enum has no "uncertain", so the
			// row reads failed and carries the inspect text — but the PLAN says
			// uncertain, which is what stops the whole thing being reported as
			// a clean failure that invites a retry.
			result.Items = append(result.Items, assistantPlanItemResult{
				Index:   i,
				Outcome: assistantPlanItemFailed,
				Error:   assistantPlanTruncate(assistantPlanInspectText(out)),
			})
			result.Status = assistantPlanUncertain
			stopped = true
		default:
			result.Items = append(result.Items, assistantPlanItemResult{
				Index:      i,
				Outcome:    assistantPlanItemOK,
				Identifier: assistantPlanItemIdentifier(out),
			})
		}
	}
	return result
}

// assistantPlanOutcome maps a plan's status onto the three-way outcome the
// pending row and the durable receipt record.
func assistantPlanOutcome(status string) string {
	switch status {
	case assistantPlanUncertain:
		return "uncertain"
	case assistantPlanFailed:
		return "failed"
	default:
		return "succeeded"
	}
}

// assistantPlanItemIdentifier digs the identifier out of an item's answer,
// best-effort: the receipt the executor already attached names the target, and
// the tool's own fields are the fallback. An empty string is a fine answer —
// nothing here invents one.
func assistantPlanItemIdentifier(result json.RawMessage) string {
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil || out == nil {
		return ""
	}
	if receipt, ok := out["receipt"].(map[string]any); ok {
		if target, ok := receipt["target"].(map[string]any); ok {
			if id := assistantFirstString(target, "identifier"); id != "" {
				return id
			}
			if title := assistantFirstString(target, "title"); title != "" {
				return title
			}
		}
	}
	return assistantFirstString(out, "issue_identifier", "identifier", "slug", "name", "title")
}

// assistantPlanInspectText is the "go and look at this" line of an uncertain
// result.
func assistantPlanInspectText(result json.RawMessage) string {
	var out struct {
		Inspect string `json:"inspect"`
	}
	if err := json.Unmarshal(result, &out); err == nil && strings.TrimSpace(out.Inspect) != "" {
		return out.Inspect
	}
	return "this step was sent but its outcome is unknown — check whether it took effect before asking again"
}

func assistantPlanTruncate(s string) string {
	if len(s) <= assistantPlanErrorLimit {
		return s
	}
	return s[:assistantPlanErrorLimit]
}

// assistantPlanResultJSON is the receipt as the transcript and the model see
// it. A marshal failure degrades to a minimal honest shape rather than an empty
// message.
func assistantPlanResultJSON(result assistantPlanResult) json.RawMessage {
	out, err := json.Marshal(result)
	if err != nil {
		slog.Warn("assistant: encode plan result failed", "error", err)
		return json.RawMessage(`{"status":"uncertain","items":[]}`)
	}
	return out
}

// confirmAssistantPlan is the plan branch of ConfirmAssistantOperation.
//
// It follows the single-operation path step for step — claim, execute, record
// the outcome, write the receipt, fire the same assistant:message event — and
// differs in exactly two places, both forced by a plan being a LIST:
//
//   - The response is 200 with per-row outcomes even when a row failed. A
//     single operation answers a failure with 409 and one message because there
//     is nothing else to say; a partly-applied plan has to report what DID run,
//     and burying that in an error string would lose it.
//   - The execution runs on a context detached from the request. A plan is a
//     sequence of already-authorized writes; abandoning it half-way because the
//     reader's socket closed is the one outcome worse than finishing it.
func (h *Handler) confirmAssistantPlan(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
	op db.AssistantPendingOperation,
	body assistantConfirmRequest,
) {
	items, err := assistantPlanItemsOf(op)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	skips, ok := assistantPlanSkips(w, body.SkippedItems, len(items))
	if !ok {
		return
	}

	// STEP 1 — claim, exactly as a single operation does: single-use, and
	// executing_at stamped before anything is dispatched.
	claimed, err := h.claimAssistantOperation(r.Context(), op.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "this action is no longer waiting for confirmation")
			return
		}
		slog.Error("assistant: claim confirmed plan failed", "operation_id", uuidToString(op.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "could not prepare this plan for execution")
		return
	}

	// STEP 2 — execute the authorized rows, in order, each through the same
	// executor a direct call uses.
	execCtx, cancelExec := context.WithTimeout(context.WithoutCancel(r.Context()), assistantPlanTotalTimeout)
	defer cancelExec()
	result := h.assistantExecutePlan(execCtx, userID, uuidToString(claimed.SessionID), items, skips)
	toolResult := assistantPlanResultJSON(result)

	// STEP 3 — the durable records first (they are what a later GET reads),
	// then the transcript message.
	persist, cancelPersist := assistantReceiptContext(r.Context())
	defer cancelPersist()

	outcome := assistantPlanOutcome(result.Status)
	h.recordAssistantExecutionReceipt(persist, claimed, toolResult, nil, outcome)
	if _, oerr := h.Queries.RecordAssistantPendingOperationOutcome(persist, db.RecordAssistantPendingOperationOutcomeParams{
		ID:      claimed.ID,
		Outcome: strToText(outcome),
	}); oerr != nil {
		slog.Error("assistant: persist plan outcome failed", "operation_id", uuidToString(claimed.ID), "error", oerr)
	} else {
		claimed.Outcome = strToText(outcome)
	}

	message, ok := h.recordAssistantOperationMessage(persist, claimed, toolResult)
	if !ok {
		writeError(w, http.StatusInternalServerError, "the plan ran but its receipt could not be saved")
		return
	}

	writeJSON(w, http.StatusOK, AssistantOperationResponse{
		Operation: assistantOperationPayloadFor(claimed, h.assistantOperationWorkspaceSlug(persist, claimed)),
		Message:   message,
		Status:    result.Status,
		Items:     result.Items,
	})
}
