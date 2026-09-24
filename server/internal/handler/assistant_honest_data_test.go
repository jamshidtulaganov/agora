package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Phase-2 "honest data": what a tool result is allowed to claim, what a failed
// write is allowed to be called, and whose calendar "today" belongs to.

// ---------------------------------------------------------------------------
// Durable-run harness
// ---------------------------------------------------------------------------

// runScriptedTurn drives ONE real turn through the production entry point:
// SendAssistantMessage → AcceptRun → lease → the run loop → terminal status.
//
// Going through the HTTP handler rather than calling svc.Run directly is what
// makes the receipt path real: the mutation bookkeeping in runToolCall only
// engages for a run that holds a database lease, so a test that skips the
// admission path also skips everything this file is about.
func runScriptedTurn(t *testing.T, user, sessionID string, script *scriptedToolChat, timezone string) (status, runErr string) {
	t.Helper()
	t.Setenv("ZHIPU_API_KEY", "assistant-honest-data-test-key")
	t.Setenv("AGORA_ASSISTANT_PROVIDER", "zhipu")

	svc := assistant.NewService(testHandler.Queries, events.New(), func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Store = testHandler.DB
	svc.TxStarter = testHandler.TxStarter
	svc.Exec = testHandler

	previous := testHandler.Assistant
	testHandler.Assistant = svc
	t.Cleanup(func() {
		svc.CancelSession(sessionID)
		testHandler.Assistant = previous
	})

	payload := map[string]any{
		"content":    "do the thing",
		"request_id": uuid.NewString(),
		"context":    map[string]any{"workspace_id": nil, "timezone": timezone},
	}
	w := sendRecoveryRequest(t, user, sessionID, payload)
	if w.Code != http.StatusAccepted {
		t.Fatalf("send = %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("send response: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var runErrValue *string
		if err := testPool.QueryRow(context.Background(),
			`SELECT status, error FROM assistant_run WHERE id = $1`, accepted.RunID).Scan(&status, &runErrValue); err != nil {
			t.Fatalf("read run: %v", err)
		}
		if status != "queued" && status != "running" {
			if runErrValue != nil {
				runErr = *runErrValue
			}
			return status, runErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a terminal status", accepted.RunID)
	return "", ""
}

func assistantOperationRows(t *testing.T, sessionID string) map[string]string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT o.tool_name, o.status
		FROM assistant_operation o
		JOIN assistant_run r ON r.id = o.run_id
		WHERE r.session_id = $1
	`, sessionID)
	if err != nil {
		t.Fatalf("read operations: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tool, status string
		if err := rows.Scan(&tool, &status); err != nil {
			t.Fatalf("scan operation: %v", err)
		}
		out[tool] = status
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. A refused write is not an interrupted one
// ---------------------------------------------------------------------------

// THE BUG this test pins (two live scenarios, 2026-09-16: assign-to-self and
// artifact-table-then-update).
//
// runToolCall classified a mutating tool's answer with
//
//	if executeErr != nil || result["error"] != nil || uncertain { status = "uncertain" }
//	if status == "uncertain" { return false }   // → abort the whole run
//
// so ANY deterministic refusal from a write tool — a status the model invented,
// an issue that does not exist, a role the caller lacks — was recorded as a
// possibly-committed write and killed the run with "a write may have succeeded
// but its result could not be saved". Nothing had been written. The model never
// saw the error, so it could not correct itself, and the user was sent to
// inspect an item nothing had touched.
//
// The three things that must be true instead, all asserted here:
//   - the run COMPLETES (a refusal is recoverable, the model gets another round)
//   - the refusal is on the transcript, so the model can read what went wrong
//   - the receipt says 'failed', not 'uncertain' — the word means "we do not
//     know", and here we do
func TestRefusedWriteIsRecordedAsFailedNotUncertain(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-refusal@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-refusal-ws", "RFS")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "still here", user, user)
	session := newAssistantTestSession(t, user)

	script := &scriptedToolChat{replies: []llm.Message{
		// A mundane write with an argument the handler rejects outright.
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolUpdateIssue,
				Arguments: `{"workspace_id":"` + ws + `","ref":"RFS-404","status":"in_progress"}`},
		}},
		// The model reads the error and answers. Reaching this reply at all is
		// the proof that the run was not aborted.
		{Role: "assistant", Content: "I could not find RFS-404 — which issue did you mean?"},
	}}

	status, runErr := runScriptedTurn(t, user, session, script, "UTC")
	if status != "completed" {
		t.Fatalf("run = %s (%q), want completed — a refused write must not abort the run", status, runErr)
	}
	if strings.Contains(runErr, "may have") || strings.Contains(runErr, "could not be confirmed") {
		t.Fatalf("run error = %q, want no uncertainty for a deterministic refusal", runErr)
	}
	if len(script.replies) != 0 {
		t.Fatalf("the run stopped early: %d scripted replies unused", len(script.replies))
	}

	messages := assistantToolMessages(t, session, assistant.ToolUpdateIssue)
	if len(messages) != 1 || !strings.Contains(messages[0], `"error"`) {
		t.Fatalf("the model never saw the refusal: %v", messages)
	}
	if got := assistantOperationRows(t, session)[assistant.ToolUpdateIssue]; got != "failed" {
		t.Fatalf("receipt status = %q, want failed", got)
	}
}

// The successful twin, so "everything is failed now" cannot pass.
func TestSuccessfulWriteIsRecordedAsSucceeded(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-success@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-success-ws", "SUC")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "move me", user, user)
	session := newAssistantTestSession(t, user)

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolUpdateIssue,
				Arguments: `{"workspace_id":"` + ws + `","ref":"SUC-1","status":"in_progress"}`},
		}},
		{Role: "assistant", Content: "SUC-1 is in progress."},
	}}
	if status, runErr := runScriptedTurn(t, user, session, script, "UTC"); status != "completed" {
		t.Fatalf("run = %s (%q)", status, runErr)
	}
	if got := assistantOperationRows(t, session)[assistant.ToolUpdateIssue]; got != "succeeded" {
		t.Fatalf("receipt status = %q, want succeeded", got)
	}
}

// A genuinely unknown outcome still stops the run, and still says so. This is
// the case the "failed" reclassification must NOT swallow.
func TestUncertainWriteStopsTheRunAndSaysSo(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-uncertain@agora.dev")
	session := newAssistantTestSession(t, user)

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolCreateIssue, Arguments: `{"workspace_id":"x","title":"t"}`},
		}},
		{Role: "assistant", Content: "unreachable"},
	}}

	previous := testHandler.Assistant
	t.Setenv("ZHIPU_API_KEY", "assistant-honest-data-test-key")
	t.Setenv("AGORA_ASSISTANT_PROVIDER", "zhipu")
	svc := assistant.NewService(testHandler.Queries, events.New(), func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Store = testHandler.DB
	svc.TxStarter = testHandler.TxStarter
	svc.Exec = assistantExecutorFunc(func(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"uncertain","inspect":"open the issue list and check"}`), nil
	})
	testHandler.Assistant = svc
	t.Cleanup(func() { svc.CancelSession(session); testHandler.Assistant = previous })

	w := sendRecoveryRequest(t, user, session, map[string]any{
		"content": "make it", "request_id": uuid.NewString(),
		"context": map[string]any{"workspace_id": nil, "timezone": "UTC"},
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("send = %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	_ = json.Unmarshal(w.Body.Bytes(), &accepted)

	var status string
	var runErr *string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := testPool.QueryRow(context.Background(),
			`SELECT status, error FROM assistant_run WHERE id = $1`, accepted.RunID).Scan(&status, &runErr); err != nil {
			t.Fatalf("read run: %v", err)
		}
		if status != "queued" && status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "failed" || runErr == nil || !strings.Contains(*runErr, "could not be confirmed") {
		t.Fatalf("uncertain run = %s / %v, want failed with an inspect-before-retrying message", status, runErr)
	}
	if got := assistantOperationRows(t, session)[assistant.ToolCreateIssue]; got != "uncertain" {
		t.Fatalf("receipt status = %q, want uncertain", got)
	}
}

// assistantExecutorFunc adapts a function to assistant.ToolExecutor.
type assistantExecutorFunc func(ctx context.Context, userID, sessionID, name string, args json.RawMessage) (json.RawMessage, error)

func (f assistantExecutorFunc) Execute(ctx context.Context, userID, sessionID, name string, args json.RawMessage) (json.RawMessage, error) {
	return f(ctx, userID, sessionID, name, args)
}

// A receipt must outlive the deadline of the thing it is a receipt for.
//
// The tool runs on a 15 s context derived from the run's; if that context dies
// during the call — a slow handler, a cancel, a lease loss — persisting the
// answer on it drops exactly the record that says what happened. Here the
// executor kills the run's context from inside the tool, which is the worst
// case, and the transcript row and the terminal receipt must both still land.
func TestToolReceiptIsPersistedOffTheDyingRunContext(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-deadctx@agora.dev")
	session := newAssistantTestSession(t, user)
	runID := newAssistantTestRun(t, session, user)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolListWorkspaces, Arguments: "{}"},
		}},
		{Role: "assistant", Content: "unreachable — the context is gone"},
	}}
	svc := assistant.NewService(testHandler.Queries, events.New(), func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Exec = assistantExecutorFunc(func(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error) {
		// The tool answered — and by the time it returns, the run is dead.
		cancel()
		return json.RawMessage(`{"workspaces":[]}`), nil
	})
	svc.Run(ctx, session, runID, user)

	if got := assistantToolMessages(t, session, assistant.ToolListWorkspaces); len(got) != 1 {
		t.Fatalf("tool answer was lost with the context: %v", got)
	}
}

// ---------------------------------------------------------------------------
// 2. Read-time recovery of a confirmed operation whose outcome was lost
// ---------------------------------------------------------------------------

// The crash window: claimed ('confirmed' + executing_at), then the process dies
// before the outcome lands. `status` records the HUMAN DECISION and can never
// say whether the work happened, so a row like this used to be
// indistinguishable from one that never ran.
func TestConfirmedOperationWithNoOutcomeReadsAsUncertain(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-lostoutcome@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-lostoutcome-ws", "LST")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "doomed", user, user)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"LST-1"}`)
	operationID := assistantOperationID(t, asked)

	// Exactly the state a crash between execute and receipt-persist leaves.
	if _, err := testPool.Exec(context.Background(), `
		UPDATE assistant_pending_operation
		SET status = 'confirmed', resolved_at = now(), executing_at = now() - interval '10 minutes', outcome = NULL
		WHERE id = $1
	`, operationID); err != nil {
		t.Fatalf("simulate crash: %v", err)
	}

	// The list the transcript renders from tells the truth...
	if got := assistantOperationStatusFromList(t, user, session, operationID); got != "uncertain" {
		t.Fatalf("listed status = %q, want uncertain", got)
	}
	// ...and a second confirm refuses rather than running it again.
	w := postAssistantOperation(t, user, operationID, "confirm")
	if w.Code != http.StatusConflict {
		t.Fatalf("second confirm = %d %s, want 409", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "outcome was never recorded") {
		t.Fatalf("409 body = %s, want it to say the outcome is unknown", w.Body.String())
	}
	assertAssistantRowCount(t, "issue", assistantIssueIDByTitle(t, ws, "doomed"), 1)
}

// A confirmed operation that DID answer keeps reading as confirmed, however
// long ago it ran — the TTL must not turn every historical row uncertain.
func TestConfirmedOperationWithAnOutcomeStaysConfirmed(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-oldoutcome@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-oldoutcome-ws", "OLD")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "doomed", user, user)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"OLD-1"}`)
	operationID := assistantOperationID(t, asked)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE assistant_pending_operation
		SET status = 'confirmed', resolved_at = now(), executing_at = now() - interval '3 days', outcome = 'succeeded'
		WHERE id = $1
	`, operationID); err != nil {
		t.Fatalf("age the row: %v", err)
	}
	if got := assistantOperationStatusFromList(t, user, session, operationID); got != "confirmed" {
		t.Fatalf("listed status = %q, want confirmed", got)
	}
}

func assistantOperationStatusFromList(t *testing.T, user, sessionID, operationID string) string {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.ListAssistantSessionOperations(w, withURLParam(
		newAssistantRequest(http.MethodGet, "/api/assistant/sessions/"+sessionID+"/operations", user, ""), "id", sessionID))
	if w.Code != http.StatusOK {
		t.Fatalf("list operations = %d %s", w.Code, w.Body.String())
	}
	var ops []assistantOperationPayload
	if err := json.Unmarshal(w.Body.Bytes(), &ops); err != nil {
		t.Fatalf("list operations body: %v (%s)", err, w.Body.String())
	}
	for _, op := range ops {
		if op.ID == operationID {
			return op.Status
		}
	}
	t.Fatalf("operation %s not in the list", operationID)
	return ""
}

func assistantIssueIDByTitle(t *testing.T, workspaceID, title string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM issue WHERE workspace_id = $1 AND title = $2`, workspaceID, title).Scan(&id); err != nil {
		t.Fatalf("find issue %q: %v", title, err)
	}
	return id
}

// ---------------------------------------------------------------------------
// 3. The coverage envelope
// ---------------------------------------------------------------------------

func assistantScopeOf(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	scope, ok := result["scope"].(map[string]any)
	if !ok {
		t.Fatalf("result carries no scope envelope: %v", result)
	}
	for _, key := range []string{"workspaces_checked", "failed", "truncated", "total"} {
		if _, present := scope[key]; !present {
			t.Fatalf("scope is missing %q: %v", key, scope)
		}
	}
	return scope
}

func assistantScopeStrings(t *testing.T, scope map[string]any, key string) []string {
	t.Helper()
	raw, _ := scope[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// Every list-returning tool carries the envelope. A tool that forgets it is a
// tool whose answer the model will narrate as complete.
func TestEveryListToolCarriesAScopeEnvelope(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-envelope@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-envelope-ws", "ENV")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "envelope issue", user, user)

	cases := map[string]string{
		assistant.ToolListWorkspaces:  `{}`,
		assistant.ToolListMyIssues:    `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListIssues:      `{"workspace_id":"` + ws + `"}`,
		assistant.ToolSearchIssues:    `{"workspace_id":"` + ws + `","query":"envelope"}`,
		assistant.ToolListComments:    `{"workspace_id":"` + ws + `","ref":"ENV-1"}`,
		assistant.ToolListProjects:    `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListSprints:     `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListLabels:      `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListAgents:      `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListSquads:      `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListMembers:     `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListRuntimes:    `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListSkills:      `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListAutopilots:  `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListAutomations: `{"workspace_id":"` + ws + `"}`,
		assistant.ToolInboxSummary:    `{}`,
		assistant.ToolActivityDigest:  `{"workspace_id":"` + ws + `"}`,
		assistant.ToolUsageSummary:    `{"workspace_id":"` + ws + `"}`,
		assistant.ToolQAStatus:        `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListStaleIssues: `{"workspace_id":"` + ws + `"}`,
		// The import connection roster is a list like any other: an empty one
		// must read as "nothing is connected", never as "I could not look".
		assistant.ToolListImportConnections: `{"workspace_id":"` + ws + `"}`,
	}
	for tool, args := range cases {
		t.Run(tool, func(t *testing.T) {
			result, err := executeAssistantTool(t, user, tool, args)
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			assistantScopeOf(t, result)
		})
	}

	// The window-bearing tools must also say which range they measured, and in
	// whose zone.
	for _, tool := range []string{assistant.ToolActivityDigest, assistant.ToolUsageSummary, assistant.ToolQAStatus} {
		t.Run(tool+"/window", func(t *testing.T) {
			result, err := executeAssistantTool(t, user, tool, cases[tool])
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			window, ok := assistantScopeOf(t, result)["window"].(map[string]any)
			if !ok {
				t.Fatalf("%s has no scope.window", tool)
			}
			if window["timezone"] == "" || window["from"] == "" || window["to"] == "" {
				t.Fatalf("%s window = %v", tool, window)
			}
		})
	}
}

// scope.total is a COUNT, not a page length: the same filters, run as an
// aggregate. This is the number the model is told to quote.
func TestListIssuesTotalIsACountNotThePageLength(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-total@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-total-ws", "TOT")
	addAssistantTestMember(t, ws, user, "owner")
	for i := 0; i < 7; i++ {
		newAssistantTestIssue(t, ws, fmt.Sprintf("issue %d", i), user, user)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`","limit":3}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	scope := assistantScopeOf(t, result)
	if total, _ := scope["total"].(float64); total != 7 {
		t.Fatalf("scope.total = %v, want the real total 7", scope["total"])
	}
	if truncated, _ := scope["truncated"].(bool); !truncated {
		t.Fatalf("scope.truncated = %v, want true for 3 of 7", scope["truncated"])
	}
	if issues, _ := result["issues"].([]any); len(issues) != 3 {
		t.Fatalf("returned %d rows, want the capped 3", len(issues))
	}

	// Uncapped: the total is still a count, and nothing is truncated.
	full, err := executeAssistantTool(t, user, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	fullScope := assistantScopeOf(t, full)
	if total, _ := fullScope["total"].(float64); total != 7 {
		t.Fatalf("uncapped total = %v", fullScope["total"])
	}
	if truncated, _ := fullScope["truncated"].(bool); truncated {
		t.Fatalf("uncapped truncated = true")
	}
}

// The count must run the SAME predicates as the list. An archived issue is
// hidden from the rows, so it must be absent from the total too — a count taken
// under looser filters reports "3 of 4" over a complete list of 3.
func TestListIssuesTotalHonoursTheArchiveFilter(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-archivetotal@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-archivetotal-ws", "ARC")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "visible", user, user)
	archived := newAssistantTestIssue(t, ws, "archived", user, user)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET archived_at = now() WHERE id = $1`, archived); err != nil {
		t.Fatalf("archive: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	if total, _ := assistantScopeOf(t, result)["total"].(float64); total != 1 {
		t.Fatalf("total = %v, want 1 — the archived issue is not in the list either", total)
	}

	withArchived, err := executeAssistantTool(t, user, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`","include_archived":true}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	if total, _ := assistantScopeOf(t, withArchived)["total"].(float64); total != 2 {
		t.Fatalf("total with archived = %v, want 2", total)
	}
}

// The count must also honour the non-owner visibility gate, or a plain member
// is told there are issues they cannot see.
func TestListIssuesTotalHonoursTheVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-gatetotal-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-gatetotal-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-gatetotal-ws", "GTL")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")
	newAssistantTestIssue(t, ws, "owner's", owner, owner)
	newAssistantTestIssue(t, ws, "member's", member, member)

	memberResult, err := executeAssistantTool(t, member, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues as member: %v", err)
	}
	rows, _ := memberResult["issues"].([]any)
	total, _ := assistantScopeOf(t, memberResult)["total"].(float64)
	if len(rows) != 1 || total != 1 {
		t.Fatalf("member sees %d rows and is told there are %v", len(rows), total)
	}

	ownerResult, err := executeAssistantTool(t, owner, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues as owner: %v", err)
	}
	if total, _ := assistantScopeOf(t, ownerResult)["total"].(float64); total != 2 {
		t.Fatalf("owner total = %v, want 2", total)
	}
}

// setAssistantTestIssueStatus moves a fixture issue off the default 'todo'.
func setAssistantTestIssueStatus(t *testing.T, issueID, status string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET status = $2 WHERE id = $1`, issueID, status); err != nil {
		t.Fatalf("set status %s: %v", status, err)
	}
}

// newAssistantTestIssues creates n issues in one status, assigned to assignee.
func newAssistantTestIssues(t *testing.T, workspaceID, creatorID, assigneeID, status string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := newAssistantTestIssue(t, workspaceID, fmt.Sprintf("%s %d", status, i), creatorID, assigneeID)
		if status != "todo" {
			setAssistantTestIssueStatus(t, id, status)
		}
	}
}

// assistantMyIssuesCounts reads list_my_issues' top-level counting fields and
// checks the invariants that make them safe to quote: total is scope.total,
// by_status sums to total, and returned is the row count.
func assistantMyIssuesCounts(t *testing.T, result map[string]any) (total float64, byStatus map[string]float64, returned float64, truncated bool) {
	t.Helper()
	for _, key := range []string{"issues", "total", "by_status", "returned", "truncated"} {
		if _, ok := result[key]; !ok {
			t.Fatalf("list_my_issues result is missing %q: %v", key, result)
		}
	}
	total, ok := result["total"].(float64)
	if !ok {
		t.Fatalf("total = %v, want a number", result["total"])
	}
	if scopeTotal, _ := assistantScopeOf(t, result)["total"].(float64); scopeTotal != total {
		t.Fatalf("total %v disagrees with scope.total %v", total, scopeTotal)
	}
	raw, ok := result["by_status"].(map[string]any)
	if !ok {
		t.Fatalf("by_status = %v, want an object", result["by_status"])
	}
	byStatus = map[string]float64{}
	var sum float64
	for status, v := range raw {
		n, _ := v.(float64)
		byStatus[status] = n
		sum += n
	}
	if sum != total {
		t.Fatalf("by_status %v sums to %v, want total %v", byStatus, sum, total)
	}
	rows, _ := result["issues"].([]any)
	returned, _ = result["returned"].(float64)
	if int(returned) != len(rows) {
		t.Fatalf("returned = %v, but %d rows came back", returned, len(rows))
	}
	truncated, _ = result["truncated"].(bool)
	if scopeTruncated, _ := assistantScopeOf(t, result)["truncated"].(bool); scopeTruncated != truncated {
		t.Fatalf("truncated %v disagrees with scope.truncated %v", truncated, scopeTruncated)
	}
	return total, byStatus, returned, truncated
}

func assertAssistantByStatus(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("by_status = %v, want %v", got, want)
	}
	for status, n := range want {
		if v, ok := got[status]; !ok || v != n {
			t.Fatalf("by_status[%s] = %v (present %v), want %v — full split %v", status, v, ok, n, got)
		}
	}
}

// "How many tasks do I have" was answered by counting list_my_issues' capped
// page. The result now carries total and by_status, counted by the server over
// ALL of the caller's matching issues — so a user with more issues than the cap
// gets exact numbers, and a truncated page says it is one.
func TestListMyIssuesCountsAreExactPastThePageCap(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-mycounts@agora.dev")
	teammate := newAssistantTestUser(t, "assistant-mycounts-mate@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-mycounts-ws", "MYC")
	addAssistantTestMember(t, ws, user, "owner")
	addAssistantTestMember(t, ws, teammate, "member")

	// 25 issues of the caller's — more than the 20-row cap.
	newAssistantTestIssues(t, ws, user, user, "done", 12)
	newAssistantTestIssues(t, ws, user, user, "todo", 8)
	newAssistantTestIssues(t, ws, user, user, "in_progress", 3)
	newAssistantTestIssues(t, ws, user, user, "blocked", 2)
	// Not the caller's: assigned to a teammate, or unassigned. The caller is
	// the owner and can SEE these, but they are not "my" issues.
	newAssistantTestIssues(t, ws, user, teammate, "todo", 4)
	newAssistantTestIssues(t, ws, user, "", "in_review", 2)
	// The caller's, but archived — hidden from the rows, so absent from the
	// counts too.
	archived := newAssistantTestIssue(t, ws, "archived mine", user, user)
	setAssistantTestIssueStatus(t, archived, "done")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET archived_at = now() WHERE id = $1`, archived); err != nil {
		t.Fatalf("archive: %v", err)
	}

	wantAll := map[string]float64{
		"backlog": 0, "todo": 8, "in_progress": 3, "in_review": 0,
		"done": 12, "blocked": 2, "cancelled": 0,
	}

	t.Run("default page", func(t *testing.T) {
		result, err := executeAssistantTool(t, user, assistant.ToolListMyIssues, `{"workspace_id":"`+ws+`"}`)
		if err != nil {
			t.Fatalf("list_my_issues: %v", err)
		}
		total, byStatus, returned, truncated := assistantMyIssuesCounts(t, result)
		if total != 25 {
			t.Fatalf("total = %v, want the exact 25", total)
		}
		if returned != 20 || !truncated {
			t.Fatalf("returned %v truncated %v, want the capped 20 and truncated", returned, truncated)
		}
		assertAssistantByStatus(t, byStatus, wantAll)
		if note, _ := result["note"].(string); !strings.Contains(note, "20 of the user's 25") {
			t.Fatalf("note = %q, want it to say the page is 20 of 25", note)
		}
	})

	t.Run("small limit does not shrink the counts", func(t *testing.T) {
		result, err := executeAssistantTool(t, user, assistant.ToolListMyIssues,
			`{"workspace_id":"`+ws+`","limit":5}`)
		if err != nil {
			t.Fatalf("list_my_issues: %v", err)
		}
		total, byStatus, returned, truncated := assistantMyIssuesCounts(t, result)
		if total != 25 || returned != 5 || !truncated {
			t.Fatalf("total %v returned %v truncated %v, want 25 / 5 / true", total, returned, truncated)
		}
		assertAssistantByStatus(t, byStatus, wantAll)
	})

	t.Run("status filter names only that status", func(t *testing.T) {
		result, err := executeAssistantTool(t, user, assistant.ToolListMyIssues,
			`{"workspace_id":"`+ws+`","status":"done"}`)
		if err != nil {
			t.Fatalf("list_my_issues: %v", err)
		}
		total, byStatus, returned, truncated := assistantMyIssuesCounts(t, result)
		if total != 12 || returned != 12 || truncated {
			t.Fatalf("total %v returned %v truncated %v, want 12 / 12 / false", total, returned, truncated)
		}
		// Seeding the other statuses with 0 would claim "0 todo" about issues
		// the filter never looked at.
		assertAssistantByStatus(t, byStatus, map[string]float64{"done": 12})
		if _, hasNote := result["note"]; hasNote {
			t.Fatalf("an untruncated list carries a truncation note: %v", result["note"])
		}
	})

	t.Run("unknown status is refused, not counted as zero", func(t *testing.T) {
		if _, err := executeAssistantTool(t, user, assistant.ToolListMyIssues,
			`{"workspace_id":"`+ws+`","status":"open"}`); err == nil {
			t.Fatalf("status \"open\" was accepted; it would read as an exact 0")
		}
	})

	t.Run("unscoped fan-out sums every membership", func(t *testing.T) {
		second := newAssistantTestWorkspace(t, "assistant-mycounts-second-ws", "MYD")
		addAssistantTestMember(t, second, user, "member")
		newAssistantTestIssues(t, second, user, user, "done", 2)
		newAssistantTestIssues(t, second, user, user, "cancelled", 1)

		result, err := executeAssistantTool(t, user, assistant.ToolListMyIssues, `{}`)
		if err != nil {
			t.Fatalf("list_my_issues: %v", err)
		}
		total, byStatus, returned, truncated := assistantMyIssuesCounts(t, result)
		if total != 28 {
			t.Fatalf("total = %v, want 25 + 3 = 28", total)
		}
		// 20 (capped) from the first workspace + all 3 from the second.
		if returned != 23 || !truncated {
			t.Fatalf("returned %v truncated %v, want 23 and truncated", returned, truncated)
		}
		assertAssistantByStatus(t, byStatus, map[string]float64{
			"backlog": 0, "todo": 8, "in_progress": 3, "in_review": 0,
			"done": 14, "blocked": 2, "cancelled": 1,
		})
	})
}

// The counts see exactly what the rows see. A plain member is not counted into
// issues that are not theirs, a workspace they are not a member of contributes
// nothing to the fan-out even when an issue there names them, and a named
// workspace they cannot read is refused outright — never answered with counts.
func TestListMyIssuesCountsOnlyWhatTheCallerCanSee(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-mycounts-gate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-mycounts-gate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-mycounts-gate-ws", "MCG")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")

	newAssistantTestIssues(t, ws, owner, member, "todo", 2)
	newAssistantTestIssues(t, ws, owner, member, "done", 1)
	// The owner's own issues, and one the member created but handed to the
	// owner: visible to the member in places, but not assigned to them.
	newAssistantTestIssues(t, ws, owner, owner, "todo", 5)
	newAssistantTestIssues(t, ws, member, owner, "in_progress", 1)

	// A workspace the member does not belong to, holding an issue assigned to
	// them anyway (stale invite, removed member, bad import).
	foreign := newAssistantTestWorkspace(t, "assistant-mycounts-gate-foreign", "MCF")
	addAssistantTestMember(t, foreign, owner, "owner")
	newAssistantTestIssues(t, foreign, owner, member, "todo", 3)

	for name, args := range map[string]string{
		"scoped":   `{"workspace_id":"` + ws + `"}`,
		"unscoped": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := executeAssistantTool(t, member, assistant.ToolListMyIssues, args)
			if err != nil {
				t.Fatalf("list_my_issues: %v", err)
			}
			total, byStatus, returned, truncated := assistantMyIssuesCounts(t, result)
			if total != 3 || returned != 3 || truncated {
				t.Fatalf("member total %v returned %v truncated %v, want 3 / 3 / false", total, returned, truncated)
			}
			assertAssistantByStatus(t, byStatus, map[string]float64{
				"backlog": 0, "todo": 2, "in_progress": 0, "in_review": 0,
				"done": 1, "blocked": 0, "cancelled": 0,
			})
		})
	}

	if result, err := executeAssistantTool(t, member, assistant.ToolListMyIssues,
		`{"workspace_id":"`+foreign+`"}`); err == nil {
		t.Fatalf("a non-member workspace answered with %v", result)
	}
}

// The unscoped fan-out names every workspace it looked in. An answer that
// silently omits one is wrong even when every row in it is right.
func TestUnscopedFanOutNamesTheWorkspacesItChecked(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-fanout@agora.dev")
	first := newAssistantTestWorkspace(t, "assistant-fanout-a-ws", "FNA")
	second := newAssistantTestWorkspace(t, "assistant-fanout-b-ws", "FNB")
	addAssistantTestMember(t, first, user, "owner")
	addAssistantTestMember(t, second, user, "owner")
	newAssistantTestIssue(t, first, "mine there", user, user)

	for _, tool := range []string{assistant.ToolListMyIssues, assistant.ToolInboxSummary, assistant.ToolActivityDigest} {
		t.Run(tool, func(t *testing.T) {
			result, err := executeAssistantTool(t, user, tool, `{}`)
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			scope := assistantScopeOf(t, result)
			checked := assistantScopeStrings(t, scope, "workspaces_checked")
			if len(checked) < 2 {
				t.Fatalf("%s checked %v, want both workspaces", tool, checked)
			}
			var seenA, seenB bool
			for _, slug := range checked {
				seenA = seenA || slug == "assistant-fanout-a-ws"
				seenB = seenB || slug == "assistant-fanout-b-ws"
			}
			if !seenA || !seenB {
				t.Fatalf("%s checked %v", tool, checked)
			}
			if failed := assistantScopeStrings(t, scope, "failed"); len(failed) != 0 {
				t.Fatalf("%s reported failures on a healthy fixture: %v", tool, failed)
			}
		})
	}
}

// unread_count is how many unread items EXIST. It used to be the length of the
// capped page, which made any busy inbox report exactly the cap forever.
func TestInboxUnreadCountIsNotThePageLength(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-inboxtotal@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-inboxtotal-ws", "IBX")
	addAssistantTestMember(t, ws, user, "owner")
	unread := assistant.MaxInboxRows + 5
	for i := 0; i < unread; i++ {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, title, read)
			VALUES ($1, 'member', $2, 'mention', $3, false)
		`, ws, user, fmt.Sprintf("notice %d", i)); err != nil {
			t.Fatalf("insert inbox item: %v", err)
		}
	}

	result, err := executeAssistantTool(t, user, assistant.ToolInboxSummary, `{}`)
	if err != nil {
		t.Fatalf("inbox_summary: %v", err)
	}
	if got, _ := result["unread_count"].(float64); int(got) != unread {
		t.Fatalf("unread_count = %v, want %d", result["unread_count"], unread)
	}
	if got, _ := result["returned_count"].(float64); int(got) != assistant.MaxInboxRows {
		t.Fatalf("returned_count = %v, want the cap %d", result["returned_count"], assistant.MaxInboxRows)
	}
	scope := assistantScopeOf(t, result)
	if truncated, _ := scope["truncated"].(bool); !truncated {
		t.Fatalf("scope.truncated = false over a capped inbox")
	}
	if total, _ := scope["total"].(float64); int(total) != unread {
		t.Fatalf("scope.total = %v, want %d", scope["total"], unread)
	}
}

// ---------------------------------------------------------------------------
// 4. Whose day is "today"
// ---------------------------------------------------------------------------

// "Today" starts at the CALLER's midnight.
//
// The window is asserted against the boundary itself rather than against UTC,
// because whether a UTC window happens to agree depends on what time it is
// when the suite runs — and a test that only fails for four hours a day is not
// a test. Two rows, one second apart, straddling the caller's local midnight:
// the later one is today, the earlier one is not, whatever UTC thinks.
func TestActivityDigestTodayStartsAtTheCallersMidnight(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-tz@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-tz-ws", "TZN")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "late night fix", user, user)

	dubai, err := time.LoadLocation("Asia/Dubai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	local := time.Now().In(dubai)
	startOfLocalDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, dubai)

	seed := func(at time.Time, from string) {
		t.Helper()
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
			VALUES ($1, $2, 'member', $3, 'status_changed', $4::jsonb, $5)
		`, ws, issueID, user, `{"from":"`+from+`","to":"in_progress"}`, at); err != nil {
			t.Fatalf("seed activity: %v", err)
		}
	}
	seed(startOfLocalDay, "todo")                      // the first second of the caller's today
	seed(startOfLocalDay.Add(-time.Second), "backlog") // the last second of yesterday

	digest := func(tz string) map[string]any {
		t.Helper()
		ctx := assistant.WithTimezone(context.Background(), tz)
		raw, derr := testHandler.Execute(ctx, user, "", assistant.ToolActivityDigest,
			json.RawMessage(`{"workspace_id":"`+ws+`","since_days":1}`))
		if derr != nil {
			t.Fatalf("activity_digest (%s): %v", tz, derr)
		}
		var out map[string]any
		if uerr := json.Unmarshal(raw, &out); uerr != nil {
			t.Fatalf("digest is not JSON: %v", uerr)
		}
		return out
	}

	result := digest("Asia/Dubai")
	rows, _ := result["activity"].([]any)
	if len(rows) != 1 {
		t.Fatalf("Asia/Dubai today = %d rows, want exactly the one filed after local midnight", len(rows))
	}

	window, _ := assistantScopeOf(t, result)["window"].(map[string]any)
	if window["timezone"] != "Asia/Dubai" {
		t.Fatalf("echoed window = %v, want Asia/Dubai", window)
	}
	from, perr := time.Parse(time.RFC3339, fmt.Sprint(window["from"]))
	if perr != nil {
		t.Fatalf("window.from = %v: %v", window["from"], perr)
	}
	if !from.Equal(startOfLocalDay) {
		t.Fatalf("window.from = %s, want the caller's midnight %s", from, startOfLocalDay)
	}

	// A caller in a different zone gets a different boundary from the same
	// data — proof the window is computed per caller and not once per server.
	other, _ := assistantScopeOf(t, digest("America/New_York"))["window"].(map[string]any)
	if other["from"] == window["from"] {
		t.Fatalf("America/New_York got the same window as Asia/Dubai: %v", other)
	}
}

// The timezone reaches the executor from the RUN, not from a tool argument:
// the client captures it per message (assistant_run.context_timezone) and the
// loop puts it on the context every tool executes under.
func TestRunContextTimezoneReachesTheTools(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-tzrun@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-tzrun-ws", "TZR")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolActivityDigest, Arguments: `{"workspace_id":"` + ws + `","since_days":1}`},
		}},
		{Role: "assistant", Content: "Nothing today."},
	}}
	if status, runErr := runScriptedTurn(t, user, session, script, "Asia/Dubai"); status != "completed" {
		t.Fatalf("run = %s (%q)", status, runErr)
	}

	messages := assistantToolMessages(t, session, assistant.ToolActivityDigest)
	if len(messages) != 1 {
		t.Fatalf("digest messages = %v", messages)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(messages[0]), &result); err != nil {
		t.Fatalf("digest row is not JSON: %v", err)
	}
	window, _ := assistantScopeOf(t, result)["window"].(map[string]any)
	if window["timezone"] != "Asia/Dubai" {
		t.Fatalf("window timezone = %v, want the run's Asia/Dubai", window["timezone"])
	}
}

// ---------------------------------------------------------------------------
// 5. Model self-knowledge
// ---------------------------------------------------------------------------

// "Which model are you" must agree with the footer. The label is instance
// configuration, so it is injected rather than guessed.
func TestAssistantServiceKnowsItsModelLabel(t *testing.T) {
	if testHandler.Assistant == nil || testHandler.Assistant.ModelLabel == nil {
		t.Fatal("the assistant service has no model label — \"which model are you\" would be answered from training data")
	}
	if got, want := testHandler.Assistant.ModelLabel(), assistantModelLabel(); got != want {
		t.Fatalf("service label = %q, UI label = %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// 6. The activity record the digest reads
// ---------------------------------------------------------------------------

// activity_digest reads activity_log, and the status_changed rows in it are
// written by registerActivityListeners (cmd/server) off the issue:updated
// event. The writer lives in another package, so nothing in a handler test
// compiles against it — which means the CONTRACT between them is exactly the
// thing no test covered: the listener's own tests publish a hand-built payload,
// so UpdateIssue could stop emitting these fields and every suite would stay
// green while the digest quietly went blank.
//
// This is that contract, asserted on the producer side: the three payload keys
// the status_changed writer reads, emitted by a real update, through the same
// executor path the assistant writes on.
func TestUpdateIssueEmitsTheStatusChangePayloadTheActivityWriterReads(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-activity@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-activity-ws", "ACT")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "moves", user, user)

	events := recordBusEvents(t, protocol.EventIssueUpdated)
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"ACT-1","status":"in_progress"}`); err != nil {
		t.Fatalf("update_issue: %v", err)
	}

	seen := events()
	if len(seen) == 0 {
		t.Fatal("no issue:updated event — the activity writer subscribes to this")
	}
	payload, ok := seen[len(seen)-1].Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload is %T, want map[string]any", seen[len(seen)-1].Payload)
	}
	if changed, _ := payload["status_changed"].(bool); !changed {
		t.Fatalf("status_changed = %v, want true", payload["status_changed"])
	}
	if prev, _ := payload["prev_status"].(string); prev != "todo" {
		t.Fatalf("prev_status = %v, want todo", payload["prev_status"])
	}
	issue, ok := payload["issue"].(IssueResponse)
	if !ok {
		t.Fatalf("issue payload is %T, want handler.IssueResponse — the listener type-asserts on it", payload["issue"])
	}
	if issue.Status != "in_progress" {
		t.Fatalf("issue.Status = %q, want in_progress", issue.Status)
	}

	// And the row itself lands, in the shape the digest and the issue timeline
	// both read (details.from / details.to).
	var action, details string
	if err := testPool.QueryRow(context.Background(), `
		SELECT action, details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'status_changed'
		ORDER BY created_at DESC LIMIT 1
	`, ws).Scan(&action, &details); err != nil {
		t.Logf("no activity_log row in this process: %v", err)
		t.Log("expected — registerActivityListeners is wired in cmd/server, not in the handler package; " +
			"the payload contract above is what this test guards")
		return
	}
	if !strings.Contains(details, `"from": "todo"`) && !strings.Contains(details, `"from":"todo"`) {
		t.Fatalf("activity details = %s", details)
	}
}
