package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

// PLANS — the tests for one confirmation authorizing several writes.
//
// What they protect, in the order it would hurt to lose it:
//
//  1. A plan MUTATES NOTHING when it is proposed, and what it proposes is
//     bounded: only allowlisted tools, at most MaxPlanItems rows.
//  2. Confirm executes the STORED items, in order, through the same executor a
//     direct call uses — so every per-item workspace and role gate fires at the
//     moment of the click, not at the moment of the proposal.
//  3. The receipt is honest per row: ok / failed / skipped-by-the-user /
//     not_run, stored durably so a reloaded transcript re-renders it.
//  4. Everything the single-operation protocol already guaranteed still holds
//     for a plan: ownership, expiry, single use, reject-cancels-everything.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func assistantPlanItemJSON(tool, args, summary string) string {
	return `{"tool":"` + tool + `","arguments":` + args + `,"summary":"` + summary + `"}`
}

func assistantPlanArgs(title string, items ...string) string {
	return `{"title":"` + title + `","items":[` + strings.Join(items, ",") + `]}`
}

// assistantProposePlanCard runs propose_plan the way the run loop does and
// returns the parked payload.
func assistantProposePlanCard(t *testing.T, userID, sessionID, args string) map[string]any {
	t.Helper()
	return assistantAsk(t, userID, sessionID, assistant.ToolProposePlan, args)
}

// assistantConfirmPlanResponse is the confirm answer for a plan: the contract's
// {status, items} alongside the operation and the receipt message the single-op
// path already returned.
type assistantConfirmPlanResponse struct {
	Operation map[string]any  `json:"operation"`
	Message   json.RawMessage `json:"message"`
	Status    string          `json:"status"`
	Items     []struct {
		Index      int    `json:"index"`
		Outcome    string `json:"outcome"`
		Identifier string `json:"identifier"`
		Error      string `json:"error"`
	} `json:"items"`
}

// postAssistantOperationWithBody is postAssistantOperation with the optional
// confirm body — the only thing the plan endpoint adds to the wire.
func postAssistantOperationWithBody(t *testing.T, userID, operationID, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newAssistantRequest(http.MethodPost, "/api/assistant/operations/"+operationID+"/"+action, userID, body), "id", operationID)
	switch action {
	case "confirm":
		testHandler.ConfirmAssistantOperation(w, req)
	case "reject":
		testHandler.RejectAssistantOperation(w, req)
	default:
		t.Fatalf("unknown action %q", action)
	}
	return w
}

func assistantConfirmPlan(t *testing.T, userID, operationID, body string) assistantConfirmPlanResponse {
	t.Helper()
	w := postAssistantOperationWithBody(t, userID, operationID, "confirm", body)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm plan = %d %s", w.Code, w.Body.String())
	}
	var resp assistantConfirmPlanResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("confirm response: %v (%s)", err, w.Body.String())
	}
	return resp
}

func assistantIssueStatus(t *testing.T, workspaceID, identifier string) string {
	t.Helper()
	number := strings.TrimPrefix(identifier, identifier[:strings.Index(identifier, "-")+1])
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE workspace_id = $1 AND number = $2`, workspaceID, number).Scan(&status); err != nil {
		t.Fatalf("read issue %s: %v", identifier, err)
	}
	return status
}

func assistantIssueCount(t *testing.T, workspaceID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	return count
}

// ---------------------------------------------------------------------------
// 1. Proposing parks, and the proposal is bounded
// ---------------------------------------------------------------------------

// The whole point of the primitive: proposing several writes writes nothing,
// and the card carries exactly what the contract says it does.
func TestAssistantProposePlanParksOneOperation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-park@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-park-ws", "PPK")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "still todo", user, user)

	args := assistantPlanArgs("Unstick the review queue",
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+ws+`","ref":"PPK-1","status":"todo"}`,
			"Move PPK-1 still todo back to todo"),
		assistantPlanItemJSON(assistant.ToolCommentIssue,
			`{"workspace_id":"`+ws+`","ref":"PPK-1","body":"back to you"}`,
			"Comment on PPK-1"),
	)
	result := assistantProposePlanCard(t, user, session, args)
	op := assistantOperationOf(t, result)

	if op["tool_name"] != assistant.ToolProposePlan {
		t.Fatalf("operation names %v, want propose_plan", op["tool_name"])
	}
	if op["kind"] != "plan" {
		t.Fatalf("operation kind = %v, want plan — the card cannot tell it is a plan", op["kind"])
	}
	if op["summary"] != "Unstick the review queue" {
		t.Fatalf("summary = %v, want the plan title", op["summary"])
	}
	if op["expires_at"] == nil || op["expires_at"] == "" {
		t.Fatalf("plan carries no expiry: %v", op)
	}
	items, _ := op["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("card carries %d items, want 2: %v", len(items), op)
	}
	for i, raw := range items {
		row, _ := raw.(map[string]any)
		if int(row["index"].(float64)) != i {
			t.Fatalf("item %d has index %v", i, row["index"])
		}
		if row["tool"] == "" || row["summary"] == "" {
			t.Fatalf("item %d is missing its tool or summary: %v", i, row)
		}
		// The arguments are deliberately NOT on the card: the summary is the
		// thing a person can check, and the stored arguments are what runs.
		if _, leaked := row["arguments"]; leaked {
			t.Fatalf("item %d put its raw arguments on the card: %v", i, row)
		}
	}

	// Persisted as ONE pending row, workspace-free (items may span workspaces;
	// each item's own membership check is what gates it).
	var status, tool, storedUser, storedSession string
	var workspaceID *string
	var target []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, tool_name, user_id::text, session_id::text, workspace_id::text, target
		FROM assistant_pending_operation WHERE id = $1
	`, op["id"]).Scan(&status, &tool, &storedUser, &storedSession, &workspaceID, &target); err != nil {
		t.Fatalf("plan did not persist a pending operation: %v", err)
	}
	if status != "pending" || tool != assistant.ToolProposePlan || storedUser != user || storedSession != session {
		t.Fatalf("pending row = %s/%s/%s/%s", status, tool, storedUser, storedSession)
	}
	if workspaceID != nil {
		t.Fatalf("plan pinned a workspace (%v) — items may span workspaces", *workspaceID)
	}
	var storedTarget map[string]any
	if err := json.Unmarshal(target, &storedTarget); err != nil || storedTarget["type"] != "plan" {
		t.Fatalf("plan target = %s", target)
	}

	// And the whole point: nothing moved.
	if got := assistantIssueStatus(t, ws, "PPK-1"); got != "todo" {
		t.Fatalf("issue status = %q before any confirmation", got)
	}
	var comments int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE workspace_id = $1`, ws).Scan(&comments); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if comments != 0 {
		t.Fatalf("the proposal posted %d comments", comments)
	}
}

// The allowlist is the reason a plan is allowed to exist. A delete inside a
// plan is refused where the model can still fix it — at propose time, with a
// message naming the tool.
func TestAssistantProposePlanRefusesToolsOutsideTheAllowlist(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-allow@agora.dev")
	other := newAssistantTestUser(t, "assistant-plan-allow-victim@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-allow-ws", "PAL")
	addAssistantTestMember(t, ws, user, "owner")
	addAssistantTestMember(t, ws, other, "member")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "not yours to batch", user, user)

	for _, tc := range []struct{ name, item string }{
		{assistant.ToolDeleteIssue, assistantPlanItemJSON(assistant.ToolDeleteIssue,
			`{"workspace_id":"`+ws+`","ref":"PAL-1"}`, "Delete PAL-1")},
		{assistant.ToolRemoveMember, assistantPlanItemJSON(assistant.ToolRemoveMember,
			`{"workspace_id":"`+ws+`","user_id":"`+other+`"}`, "Remove a teammate")},
		{assistant.ToolCreateAgent, assistantPlanItemJSON(assistant.ToolCreateAgent,
			`{"workspace_id":"`+ws+`","name":"Bot","runtime_id":"`+ws+`"}`, "Create an agent")},
		{"no_such_tool", assistantPlanItemJSON("no_such_tool", `{}`, "Do something invented")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executeAssistantSessionTool(t, user, session, assistant.ToolProposePlan,
				assistantPlanArgs("A plan that should not exist",
					assistantPlanItemJSON(assistant.ToolUpdateIssue,
						`{"workspace_id":"`+ws+`","ref":"PAL-1","status":"todo"}`, "Move PAL-1 to todo"),
					tc.item))
			if err == nil {
				t.Fatalf("propose_plan accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("refusal does not name the offending tool: %v", err)
			}
		})
	}

	// A refused plan leaves nothing behind for the user to confirm later.
	var parked int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM assistant_pending_operation WHERE session_id = $1`, session).Scan(&parked); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if parked != 0 {
		t.Fatalf("%d pending operations survived a refused plan", parked)
	}
}

// The cap is a readability limit: nobody audits forty rows before pressing one
// button. The refusal says what to do instead.
func TestAssistantProposePlanRefusesMoreThanTheCap(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-cap@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-cap-ws", "PCP")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)

	items := make([]string, 0, assistant.MaxPlanItems+1)
	for i := 0; i <= assistant.MaxPlanItems; i++ {
		items = append(items, assistantPlanItemJSON(assistant.ToolCreateIssue,
			`{"workspace_id":"`+ws+`","title":"Issue `+fmt.Sprint(i)+`"}`,
			"Open issue "+fmt.Sprint(i)))
	}
	_, err := executeAssistantSessionTool(t, user, session, assistant.ToolProposePlan,
		assistantPlanArgs("Too much at once", items...))
	if err == nil {
		t.Fatalf("propose_plan accepted %d items", len(items))
	}
	if !strings.Contains(err.Error(), fmt.Sprint(assistant.MaxPlanItems)) {
		t.Fatalf("refusal does not name the cap: %v", err)
	}

	// An empty plan is not a plan either.
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolProposePlan,
		`{"title":"Nothing to do","items":[]}`); err == nil {
		t.Fatal("propose_plan accepted an empty plan")
	}
	// Nor is one whose rows carry no line for the human to read.
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolProposePlan,
		assistantPlanArgs("No summaries",
			assistantPlanItemJSON(assistant.ToolCreateIssue, `{"workspace_id":"`+ws+`","title":"x"}`, ""))); err == nil {
		t.Fatal("propose_plan accepted an item with no summary")
	}
	// Nor is one whose arguments are a string of JSON rather than an object.
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolProposePlan,
		`{"title":"Stringly typed","items":[{"tool":"create_issue","arguments":"{}","summary":"x"}]}`); err == nil {
		t.Fatal("propose_plan accepted a non-object arguments blob")
	}
}

// ---------------------------------------------------------------------------
// 2. Confirm runs the stored items
// ---------------------------------------------------------------------------

// One click, N writes, IN ORDER — proven by items that depend on each other:
// the second row can only succeed if the first has already run.
func TestAssistantConfirmPlanRunsEveryItemInOrder(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-run@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-run-ws", "PRN")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestLabel(t, ws, "bug", "#ef4444")
	runID := newAssistantTestRun(t, session, user)

	args := assistantPlanArgs("Open and triage one issue",
		assistantPlanItemJSON(assistant.ToolCreateIssue,
			`{"workspace_id":"`+ws+`","title":"First from the plan"}`,
			"Open \\\"First from the plan\\\""),
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+ws+`","ref":"PRN-1","status":"in_progress"}`,
			"Move PRN-1 to in progress"),
		assistantPlanItemJSON(assistant.ToolAddIssueLabel,
			`{"workspace_id":"`+ws+`","ref":"PRN-1","label":"bug"}`,
			"Label PRN-1 bug"),
	)
	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session, args))
	if assistantIssueCount(t, ws) != 0 {
		t.Fatal("the proposal created an issue")
	}

	// No body at all: run everything, exactly as every existing client sends.
	resp := assistantConfirmPlan(t, user, operationID, "")
	if resp.Status != "completed" {
		t.Fatalf("plan status = %q, want completed (%v)", resp.Status, resp.Items)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("plan reported %d rows, want 3: %v", len(resp.Items), resp.Items)
	}
	for i, item := range resp.Items {
		if item.Index != i || item.Outcome != "ok" {
			t.Fatalf("row %d = %+v, want ok", i, item)
		}
	}
	if resp.Items[0].Identifier != "PRN-1" {
		t.Fatalf("row 0 identifier = %q, want the issue it created", resp.Items[0].Identifier)
	}

	// The effects, and the ORDER: the label and the status could only land on
	// an issue the first row had already created.
	if got := assistantIssueStatus(t, ws, "PRN-1"); got != "in_progress" {
		t.Fatalf("issue status = %q, want in_progress", got)
	}
	var labels int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM issue_to_label a
		JOIN issue i ON i.id = a.issue_id
		WHERE i.workspace_id = $1
	`, ws).Scan(&labels); err != nil {
		t.Fatalf("count label assignments: %v", err)
	}
	if labels != 1 {
		t.Fatalf("label assignments = %d, want 1", labels)
	}

	// The pending row is spent, and its outcome says the work landed.
	if got := assistantPendingOperationStatus(t, operationID); got != "confirmed" {
		t.Fatalf("operation status = %q, want confirmed", got)
	}
	var outcome string
	if err := testPool.QueryRow(context.Background(),
		`SELECT outcome FROM assistant_pending_operation WHERE id = $1`, operationID).Scan(&outcome); err != nil {
		t.Fatalf("read outcome: %v", err)
	}
	if outcome != "succeeded" {
		t.Fatalf("plan outcome = %q, want succeeded", outcome)
	}

	// The DURABLE receipt carries the per-row outcomes, so a transcript
	// reloaded tomorrow renders the same rows instead of an empty card.
	var status string
	var stored []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, result FROM assistant_operation WHERE run_id = $1 AND tool_call_id = $2`,
		runID, "op_"+operationID).Scan(&status, &stored); err != nil {
		t.Fatalf("no durable execution receipt: %v", err)
	}
	if status != "succeeded" {
		t.Fatalf("execution receipt status = %q", status)
	}
	var receipt assistantConfirmPlanResponse
	if err := json.Unmarshal(stored, &receipt); err != nil {
		t.Fatalf("receipt result is not JSON: %v (%s)", err, stored)
	}
	if receipt.Status != "completed" || len(receipt.Items) != 3 {
		t.Fatalf("receipt does not carry the per-row outcomes: %s", stored)
	}

	// …and the transcript row the user reads is the same object.
	messages := assistantToolMessages(t, session, assistant.ToolProposePlan)
	if len(messages) != 1 || !strings.Contains(messages[0], `"outcome":"ok"`) {
		t.Fatalf("transcript does not carry the plan receipt: %v", messages)
	}
}

// Unchecking rows is the point of the card. A skipped row runs nothing, says
// so, and does not stop the rows after it.
func TestAssistantConfirmPlanSkipsUncheckedItems(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-skip@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-skip-ws", "PSK")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "leave me alone", user, user)

	args := assistantPlanArgs("Three changes, one unwanted",
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+ws+`","ref":"PSK-1","status":"in_progress"}`, "Move PSK-1 to in progress"),
		assistantPlanItemJSON(assistant.ToolCommentIssue,
			`{"workspace_id":"`+ws+`","ref":"PSK-1","body":"unwanted comment"}`, "Comment on PSK-1"),
		assistantPlanItemJSON(assistant.ToolCreateIssue,
			`{"workspace_id":"`+ws+`","title":"Second from the plan"}`, "Open a follow-up"),
	)
	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session, args))

	resp := assistantConfirmPlan(t, user, operationID, `{"skipped_items":[1]}`)
	if resp.Status != "completed" {
		t.Fatalf("plan status = %q, want completed: %v", resp.Status, resp.Items)
	}
	if resp.Items[0].Outcome != "ok" || resp.Items[1].Outcome != "skipped" || resp.Items[2].Outcome != "ok" {
		t.Fatalf("outcomes = %+v, want ok/skipped/ok", resp.Items)
	}
	var comments int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE workspace_id = $1`, ws).Scan(&comments); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if comments != 0 {
		t.Fatalf("a skipped row still posted %d comments", comments)
	}
	// The rows around it did run.
	if got := assistantIssueStatus(t, ws, "PSK-1"); got != "in_progress" {
		t.Fatalf("issue status = %q, want in_progress", got)
	}
	if assistantIssueCount(t, ws) != 2 {
		t.Fatalf("issues = %d, want the original plus the one the plan opened", assistantIssueCount(t, ws))
	}
}

// A row the client names that the plan does not have means the two are looking
// at different plans — which is exactly when running the rest would be wrong.
// The authorization is NOT spent by the refusal.
func TestAssistantConfirmPlanRefusesOutOfRangeSkips(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-range@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-range-ws", "PRG")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "untouched", user, user)

	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session,
		assistantPlanArgs("One change",
			assistantPlanItemJSON(assistant.ToolUpdateIssue,
				`{"workspace_id":"`+ws+`","ref":"PRG-1","status":"done"}`, "Close PRG-1"))))

	w := postAssistantOperationWithBody(t, user, operationID, "confirm", `{"skipped_items":[4]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range skip = %d %s, want 400", w.Code, w.Body.String())
	}
	if got := assistantIssueStatus(t, ws, "PRG-1"); got != "todo" {
		t.Fatalf("the refused confirm still ran the plan (status %q)", got)
	}
	if got := assistantPendingOperationStatus(t, operationID); got != "pending" {
		t.Fatalf("operation status = %q — a refused body spent the authorization", got)
	}
	// The same body on a SINGLE operation is a 400 too: skipping rows means
	// nothing there, and silently ignoring it would hide a client bug.
	newAssistantTestIssue(t, ws, "doomed", user, user)
	single := assistantOperationID(t, assistantAsk(t, user, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"PRG-2"}`))
	if w := postAssistantOperationWithBody(t, user, single, "confirm", `{"skipped_items":[0]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("skipped_items on a single operation = %d %s, want 400", w.Code, w.Body.String())
	}
}

// A plan is an ordered sequence a human read as a whole. Carrying on past a
// failed step would apply a plan nobody authorized, so it stops — and the rows
// after it are reported not_run, which is a different statement from failed.
func TestAssistantConfirmPlanStopsAtTheFirstFailure(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-stop@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-stop-ws", "PST")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "the only real one", user, user)

	args := assistantPlanArgs("One good row, one impossible one",
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+ws+`","ref":"PST-1","status":"in_progress"}`, "Move PST-1 to in progress"),
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+ws+`","ref":"PST-404","status":"done"}`, "Close an issue that does not exist"),
		assistantPlanItemJSON(assistant.ToolCreateIssue,
			`{"workspace_id":"`+ws+`","title":"Never opened"}`, "Open a follow-up"),
	)
	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session, args))

	resp := assistantConfirmPlan(t, user, operationID, "")
	if resp.Status != "failed" {
		t.Fatalf("plan status = %q, want failed: %+v", resp.Status, resp.Items)
	}
	if resp.Items[0].Outcome != "ok" {
		t.Fatalf("row 0 = %+v, want the row before the failure to have run", resp.Items[0])
	}
	if resp.Items[1].Outcome != "failed" || resp.Items[1].Error == "" {
		t.Fatalf("row 1 = %+v, want failed with the executor's own message", resp.Items[1])
	}
	if resp.Items[2].Outcome != "not_run" {
		t.Fatalf("row 2 = %+v, want not_run", resp.Items[2])
	}

	// The row before the failure stands; the row after it never happened.
	if got := assistantIssueStatus(t, ws, "PST-1"); got != "in_progress" {
		t.Fatalf("issue status = %q — the successful row was rolled back", got)
	}
	if assistantIssueCount(t, ws) != 1 {
		t.Fatalf("issues = %d — a row after the failure ran", assistantIssueCount(t, ws))
	}

	// A partly-applied plan is recorded as failed, not as succeeded.
	var outcome string
	if err := testPool.QueryRow(context.Background(),
		`SELECT outcome FROM assistant_pending_operation WHERE id = $1`, operationID).Scan(&outcome); err != nil {
		t.Fatalf("read outcome: %v", err)
	}
	if outcome != "failed" {
		t.Fatalf("plan outcome = %q, want failed", outcome)
	}
	// …and the confirmation is spent either way: a partial plan is not retried
	// by clicking again, it is asked for again.
	if w := postAssistantOperationWithBody(t, user, operationID, "confirm", ""); w.Code != http.StatusConflict {
		t.Fatalf("second confirm = %d, want 409", w.Code)
	}
}

// Every gate a direct call runs, a plan item runs — at the moment of the CLICK.
// A workspace the user has left since proposing fails that ITEM; it is not an
// error of the endpoint and it does not touch the rows before it.
func TestAssistantPlanItemStillHitsWorkspaceGates(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-gate@agora.dev")
	keeper := newAssistantTestUser(t, "assistant-plan-gate-keeper@agora.dev")
	mine := newAssistantTestWorkspace(t, "assistant-plan-gate-mine", "PGM")
	theirs := newAssistantTestWorkspace(t, "assistant-plan-gate-theirs", "PGT")
	addAssistantTestMember(t, mine, user, "owner")
	addAssistantTestMember(t, theirs, user, "owner")
	addAssistantTestMember(t, theirs, keeper, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, mine, "mine to change", user, user)
	newAssistantTestIssue(t, theirs, "theirs to keep", keeper, keeper)

	args := assistantPlanArgs("Across two workspaces",
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+mine+`","ref":"PGM-1","status":"in_progress"}`, "Move PGM-1 to in progress"),
		assistantPlanItemJSON(assistant.ToolUpdateIssue,
			`{"workspace_id":"`+theirs+`","ref":"PGT-1","status":"done"}`, "Close PGT-1"),
		assistantPlanItemJSON(assistant.ToolCreateIssue,
			`{"workspace_id":"`+mine+`","title":"Never opened"}`, "Open a follow-up"),
	)
	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session, args))

	// …and now the user is out of the second workspace.
	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, theirs, user); err != nil {
		t.Fatalf("remove membership: %v", err)
	}

	resp := assistantConfirmPlan(t, user, operationID, "")
	if resp.Status != "failed" {
		t.Fatalf("plan status = %q, want failed: %+v", resp.Status, resp.Items)
	}
	if resp.Items[0].Outcome != "ok" {
		t.Fatalf("row 0 = %+v, want the in-workspace row to have run", resp.Items[0])
	}
	if resp.Items[1].Outcome != "failed" || !strings.Contains(resp.Items[1].Error, "not a member") {
		t.Fatalf("row 1 = %+v, want the membership refusal", resp.Items[1])
	}
	if resp.Items[2].Outcome != "not_run" {
		t.Fatalf("row 2 = %+v, want not_run", resp.Items[2])
	}
	if got := assistantIssueStatus(t, theirs, "PGT-1"); got != "todo" {
		t.Fatalf("the other workspace's issue moved to %q", got)
	}
}

// ---------------------------------------------------------------------------
// 3. Everything the single-operation protocol guaranteed still holds
// ---------------------------------------------------------------------------

// Somebody else's plan is NOT FOUND, exactly as somebody else's delete is.
func TestAssistantConfirmPlanByAnotherUserIsNotFound(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-plan-other@agora.dev")
	other := newAssistantTestUser(t, "assistant-plan-other-two@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-other-ws", "POT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, other, "owner")
	session := newAssistantTestSession(t, owner)
	newAssistantTestIssue(t, ws, "not yours to confirm", owner, owner)

	operationID := assistantOperationID(t, assistantProposePlanCard(t, owner, session,
		assistantPlanArgs("Owner's plan",
			assistantPlanItemJSON(assistant.ToolUpdateIssue,
				`{"workspace_id":"`+ws+`","ref":"POT-1","status":"done"}`, "Close POT-1"))))

	for _, action := range []string{"confirm", "reject"} {
		if w := postAssistantOperationWithBody(t, other, operationID, action, ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s by another user = %d %s, want 404", action, w.Code, w.Body.String())
		}
	}
	if got := assistantIssueStatus(t, ws, "POT-1"); got != "todo" {
		t.Fatalf("issue status = %q — somebody else's plan ran", got)
	}
	if got := assistantPendingOperationStatus(t, operationID); got != "pending" {
		t.Fatalf("operation status = %q, want it untouched", got)
	}
}

// Expiry is the same 30 minutes, evaluated when somebody looks.
func TestAssistantExpiredPlanIsRefused(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-expired@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-expired-ws", "PEX")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "outlived the card", user, user)

	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session,
		assistantPlanArgs("A plan nobody came back to",
			assistantPlanItemJSON(assistant.ToolUpdateIssue,
				`{"workspace_id":"`+ws+`","ref":"PEX-1","status":"done"}`, "Close PEX-1"))))
	if _, err := testPool.Exec(context.Background(),
		`UPDATE assistant_pending_operation SET expires_at = now() - interval '1 minute' WHERE id = $1`,
		operationID); err != nil {
		t.Fatalf("age the plan: %v", err)
	}

	w := postAssistantOperationWithBody(t, user, operationID, "confirm", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("confirm on an expired plan = %d %s, want 409", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("refusal does not say it expired: %s", w.Body.String())
	}
	if got := assistantIssueStatus(t, ws, "PEX-1"); got != "todo" {
		t.Fatalf("an expired plan still ran (status %q)", got)
	}
	if got := assistantPendingOperationStatus(t, operationID); got != "expired" {
		t.Fatalf("operation status = %q, want expired", got)
	}
}

// Reject is Cancel, and for a plan it cancels the WHOLE list — there is no
// per-row reject, because the user authorized (or did not) one thing.
func TestAssistantRejectCancelsTheWholePlan(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-reject@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-reject-ws", "PRJ")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "spared by cancel", user, user)

	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session,
		assistantPlanArgs("Three changes nobody wanted",
			assistantPlanItemJSON(assistant.ToolUpdateIssue,
				`{"workspace_id":"`+ws+`","ref":"PRJ-1","status":"done"}`, "Close PRJ-1"),
			assistantPlanItemJSON(assistant.ToolCommentIssue,
				`{"workspace_id":"`+ws+`","ref":"PRJ-1","body":"nope"}`, "Comment on PRJ-1"),
			assistantPlanItemJSON(assistant.ToolCreateIssue,
				`{"workspace_id":"`+ws+`","title":"Never opened"}`, "Open a follow-up"))))

	w := postAssistantOperationWithBody(t, user, operationID, "reject", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("reject = %d %s", w.Code, w.Body.String())
	}
	if got := assistantPendingOperationStatus(t, operationID); got != "rejected" {
		t.Fatalf("operation status = %q, want rejected", got)
	}
	if got := assistantIssueStatus(t, ws, "PRJ-1"); got != "todo" {
		t.Fatalf("a cancelled plan ran (status %q)", got)
	}
	if assistantIssueCount(t, ws) != 1 {
		t.Fatalf("a cancelled plan created issues")
	}
	messages := assistantToolMessages(t, session, assistant.ToolProposePlan)
	if len(messages) != 1 || !strings.Contains(messages[0], "cancelled") {
		t.Fatalf("transcript does not record the cancellation: %v", messages)
	}
	// A cancelled card is spent.
	if w := postAssistantOperationWithBody(t, user, operationID, "confirm", ""); w.Code != http.StatusConflict {
		t.Fatalf("confirm after reject = %d, want 409", w.Code)
	}
}

// A plan reloaded from the API renders as the same checklist — the rows live
// in the stored arguments, so the card survives a refresh.
func TestAssistantPlanCardSurvivesAReload(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-plan-reload@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-plan-reload-ws", "PRL")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "still waiting", user, user)

	operationID := assistantOperationID(t, assistantProposePlanCard(t, user, session,
		assistantPlanArgs("Two changes",
			assistantPlanItemJSON(assistant.ToolUpdateIssue,
				`{"workspace_id":"`+ws+`","ref":"PRL-1","status":"in_progress"}`, "Move PRL-1 to in progress"),
			assistantPlanItemJSON(assistant.ToolSubscribeIssue,
				`{"workspace_id":"`+ws+`","ref":"PRL-1"}`, "Follow PRL-1"))))

	w := httptest.NewRecorder()
	req := withURLParam(newAssistantRequest(http.MethodGet, "/api/assistant/operations/"+operationID, user, ""), "id", operationID)
	testHandler.GetAssistantOperation(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get operation = %d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("operation payload: %v", err)
	}
	if payload["kind"] != "plan" {
		t.Fatalf("reloaded operation kind = %v", payload["kind"])
	}
	items, _ := payload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("reloaded card carries %d rows, want 2: %v", len(items), payload)
	}
}
