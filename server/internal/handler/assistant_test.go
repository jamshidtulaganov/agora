package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func newAssistantTestUser(t *testing.T, email string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Assistant Test "+email, email,
	).Scan(&userID); err != nil {
		t.Fatalf("insert test user %s: %v", email, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })
	return userID
}

func newAssistantTestWorkspace(t *testing.T, slug, prefix string) string {
	t.Helper()
	ctx := context.Background()
	var wsID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, '', $3) RETURNING id`,
		"Assistant "+slug, slug, prefix,
	).Scan(&wsID); err != nil {
		t.Fatalf("insert test workspace %s: %v", slug, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID) })
	return wsID
}

func addAssistantTestMember(t *testing.T, workspaceID, userID, role string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)`,
		workspaceID, userID, role,
	); err != nil {
		t.Fatalf("insert member: %v", err)
	}
}

// newAssistantTestIssue creates an issue with an explicit creator and an
// optional direct member assignee (pass "" for unassigned).
func newAssistantTestIssue(t *testing.T, workspaceID, title, creatorID, assigneeID string) string {
	t.Helper()
	ctx := context.Background()
	var assigneeType, assignee any
	if assigneeID != "" {
		assigneeType, assignee = "member", assigneeID
	}
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id,
		                   assignee_type, assignee_id, number)
		VALUES ($1, $2, 'todo', 'medium', 'member', $3, $4, $5,
		        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
		RETURNING id
	`, workspaceID, title, creatorID, assigneeType, assignee).Scan(&issueID); err != nil {
		t.Fatalf("insert issue %q: %v", title, err)
	}
	return issueID
}

func newAssistantTestSession(t *testing.T, userID string) string {
	t.Helper()
	var sessionID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO assistant_session (user_id) VALUES ($1) RETURNING id`, userID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("insert assistant session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM assistant_session WHERE id = $1`, sessionID)
	})
	return sessionID
}

func newAssistantRequest(method, path, userID, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", userID)
	return req
}

// executeAssistantTool runs one tool through the real executor, outside any
// conversation. Every workspace-scoped tool ignores the session, so the empty
// id here is honest: it is what a tool that does not need one receives.
// Session-scoped tools (the artifact pair) use executeAssistantSessionTool.
func executeAssistantTool(t *testing.T, userID, tool, args string) (map[string]any, error) {
	t.Helper()
	return executeAssistantSessionTool(t, userID, "", tool, args)
}

// executeAssistantSessionTool runs one tool as the run loop does, inside a
// conversation the caller owns.
func executeAssistantSessionTool(t *testing.T, userID, sessionID, tool, args string) (map[string]any, error) {
	t.Helper()
	raw, err := testHandler.Execute(context.Background(), userID, sessionID, tool, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		t.Fatalf("tool result is not a JSON object: %v (%s)", uerr, raw)
	}
	return out, nil
}

func assistantIssueTitles(t *testing.T, result map[string]any) []string {
	t.Helper()
	rows, ok := result["issues"].([]any)
	if !ok {
		t.Fatalf("result has no issues array: %v", result)
	}
	titles := make([]string, 0, len(rows))
	for _, row := range rows {
		m, _ := row.(map[string]any)
		title, _ := m["title"].(string)
		titles = append(titles, title)
	}
	return titles
}

// ---------------------------------------------------------------------------
// Executor — permissions
// ---------------------------------------------------------------------------

// The invariant the whole feature rests on: a workspace the caller is not a
// member of returns an error to the model and zero rows — never a partial list.
func TestAssistantExecutorRefusesNonMemberWorkspace(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-outsider@agora.dev")
	insider := newAssistantTestUser(t, "assistant-insider@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-closed", "ACL")
	addAssistantTestMember(t, ws, insider, "owner")
	newAssistantTestIssue(t, ws, "secret roadmap work", insider, insider)

	for _, tc := range []struct{ tool, args string }{
		{assistant.ToolListMyIssues, `{"workspace_id":"` + ws + `"}`},
		{assistant.ToolSearchIssues, `{"workspace_id":"` + ws + `","query":"secret"}`},
	} {
		result, err := executeAssistantTool(t, outsider, tc.tool, tc.args)
		if err == nil {
			t.Fatalf("%s: expected a refusal, got %v", tc.tool, result)
		}
		if !strings.Contains(err.Error(), "not a member") {
			t.Fatalf("%s: refusal should name the reason, got %q", tc.tool, err)
		}
	}

	// The same tools must still work for the member, or the test above proves
	// nothing about the gate.
	result, err := executeAssistantTool(t, insider, assistant.ToolSearchIssues,
		`{"workspace_id":"`+ws+`","query":"secret"}`)
	if err != nil {
		t.Fatalf("member search failed: %v", err)
	}
	if got := assistantIssueTitles(t, result); len(got) != 1 || got[0] != "secret roadmap work" {
		t.Fatalf("member should see the issue, got %v", got)
	}
}

// A workspace id that is not a UUID is the model's mistake, and must come back
// as a correctable tool error rather than a panic.
func TestAssistantExecutorRejectsMalformedWorkspaceID(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-badws@agora.dev")
	if _, err := executeAssistantTool(t, user, assistant.ToolSearchIssues,
		`{"workspace_id":"the-acme-one","query":"x"}`); err == nil {
		t.Fatal("expected an argument error")
	}
}

func TestAssistantExecutorRejectsUnknownTool(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-unknowntool@agora.dev")
	// A name outside the catalog, and deliberately a plausible one: the
	// catalog covers the product, so the hallucinations worth guarding against
	// now sound like features rather than nonsense.
	_, err := testHandler.Execute(context.Background(), user, "", "set_agent_env", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("the catalog must be a hard allowlist, got %v", err)
	}
}

// The non-owner visibility gate is enforced on the assistant's search exactly
// as it is on the HTTP search: a member sees only issues that are theirs.
func TestAssistantSearchHonorsNonOwnerVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-gate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-gate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-gate", "GAT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")

	newAssistantTestIssue(t, ws, "gatecheck owner only", owner, owner)
	newAssistantTestIssue(t, ws, "gatecheck member task", member, member)

	ownerResult, err := executeAssistantTool(t, owner, assistant.ToolSearchIssues,
		`{"workspace_id":"`+ws+`","query":"gatecheck"}`)
	if err != nil {
		t.Fatalf("owner search: %v", err)
	}
	if got := assistantIssueTitles(t, ownerResult); len(got) != 2 {
		t.Fatalf("owner should see both issues, got %v", got)
	}

	memberResult, err := executeAssistantTool(t, member, assistant.ToolSearchIssues,
		`{"workspace_id":"`+ws+`","query":"gatecheck"}`)
	if err != nil {
		t.Fatalf("member search: %v", err)
	}
	got := assistantIssueTitles(t, memberResult)
	if len(got) != 1 || got[0] != "gatecheck member task" {
		t.Fatalf("member must see only their own issue, got %v", got)
	}
}

// Unscoped list_my_issues fans out over MEMBERSHIPS, not over every workspace
// that happens to hold a row pointing at this user.
func TestAssistantListMyIssuesUnscopedOnlyFansOutToMemberships(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-fanout@agora.dev")
	other := newAssistantTestUser(t, "assistant-fanout-other@agora.dev")

	mine := newAssistantTestWorkspace(t, "assistant-fanout-mine", "FMI")
	addAssistantTestMember(t, mine, user, "member")
	newAssistantTestIssue(t, mine, "fanout mine", user, user)

	// An issue in a workspace the user has no membership in, assigned to them
	// anyway (stale invite, removed member, bad import). It must NOT surface.
	foreign := newAssistantTestWorkspace(t, "assistant-fanout-foreign", "FFO")
	addAssistantTestMember(t, foreign, other, "owner")
	newAssistantTestIssue(t, foreign, "fanout foreign", other, user)

	result, err := executeAssistantTool(t, user, assistant.ToolListMyIssues, `{}`)
	if err != nil {
		t.Fatalf("list_my_issues: %v", err)
	}
	got := assistantIssueTitles(t, result)
	if len(got) != 1 || got[0] != "fanout mine" {
		t.Fatalf("expected only the membership workspace's issue, got %v", got)
	}

	// Results carry a workspace-scoped link so the UI can render a chip.
	rows := result["issues"].([]any)
	row := rows[0].(map[string]any)
	if row["url_path"] != "/assistant-fanout-mine/issues/FMI-1" {
		t.Fatalf("url_path = %v", row["url_path"])
	}
	if row["identifier"] != "FMI-1" {
		t.Fatalf("identifier = %v", row["identifier"])
	}
}

func TestAssistantListWorkspacesReturnsMembershipsWithRole(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-roster@agora.dev")
	other := newAssistantTestUser(t, "assistant-roster-other@agora.dev")
	owned := newAssistantTestWorkspace(t, "assistant-roster-owned", "ROW")
	joined := newAssistantTestWorkspace(t, "assistant-roster-joined", "ROJ")
	hidden := newAssistantTestWorkspace(t, "assistant-roster-hidden", "ROH")
	addAssistantTestMember(t, owned, user, "owner")
	addAssistantTestMember(t, joined, user, "member")
	addAssistantTestMember(t, hidden, other, "owner")

	result, err := executeAssistantTool(t, user, assistant.ToolListWorkspaces, `{}`)
	if err != nil {
		t.Fatalf("list_workspaces: %v", err)
	}
	rows, _ := result["workspaces"].([]any)
	roles := map[string]string{}
	for _, row := range rows {
		m := row.(map[string]any)
		roles[m["slug"].(string)] = m["role"].(string)
	}
	if roles["assistant-roster-owned"] != "owner" || roles["assistant-roster-joined"] != "member" {
		t.Fatalf("roles = %v", roles)
	}
	if _, leaked := roles["assistant-roster-hidden"]; leaked {
		t.Fatalf("a workspace the user does not belong to leaked: %v", roles)
	}
}

// ---------------------------------------------------------------------------
// Endpoints — ownership
// ---------------------------------------------------------------------------

func TestAssistantSessionOwnershipIsolation(t *testing.T) {
	alice := newAssistantTestUser(t, "assistant-alice@agora.dev")
	bob := newAssistantTestUser(t, "assistant-bob@agora.dev")
	sessionID := newAssistantTestSession(t, alice)

	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		method  string
		body    string
	}{
		{"get", testHandler.GetAssistantSession, "GET", ""},
		{"patch", testHandler.PatchAssistantSession, "PATCH", `{"title":"stolen"}`},
		{"delete", testHandler.DeleteAssistantSession, "DELETE", ""},
		{"messages", testHandler.ListAssistantMessages, "GET", ""},
		{"send", testHandler.SendAssistantMessage, "POST", `{"content":"hi"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := withURLParam(newAssistantRequest(tc.method, "/api/assistant/sessions/"+sessionID, bob, tc.body), "id", sessionID)
			tc.handler(w, req)
			// Not 403: acknowledging the session exists would already leak it.
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// The owner still sees it — proving the 404s above are about ownership.
	w := httptest.NewRecorder()
	testHandler.GetAssistantSession(w, withURLParam(newAssistantRequest("GET", "/x", alice, ""), "id", sessionID))
	if w.Code != http.StatusOK {
		t.Fatalf("owner read failed: %d %s", w.Code, w.Body.String())
	}
}

func TestCreateAssistantSessionRejectsForeignFocusWorkspace(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-focus@agora.dev")
	other := newAssistantTestUser(t, "assistant-focus-other@agora.dev")
	foreign := newAssistantTestWorkspace(t, "assistant-focus-foreign", "FCF")
	addAssistantTestMember(t, foreign, other, "owner")

	w := httptest.NewRecorder()
	testHandler.CreateAssistantSession(w, newAssistantRequest("POST", "/api/assistant/sessions", user,
		`{"focus_workspace_id":"`+foreign+`"}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAssistantSessionListIsScopedToTheCaller(t *testing.T) {
	alice := newAssistantTestUser(t, "assistant-list-alice@agora.dev")
	bob := newAssistantTestUser(t, "assistant-list-bob@agora.dev")
	aliceSession := newAssistantTestSession(t, alice)
	newAssistantTestSession(t, bob)

	w := httptest.NewRecorder()
	testHandler.ListAssistantSessions(w, newAssistantRequest("GET", "/api/assistant/sessions", alice, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var sessions []AssistantSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != aliceSession {
		t.Fatalf("expected only alice's session, got %+v", sessions)
	}
}

// ---------------------------------------------------------------------------
// Endpoints — send
// ---------------------------------------------------------------------------

// blockingToolChat holds a run open until the test releases it, so the
// "one run per session" behavior can be asserted deterministically. Releasing
// it fails the call rather than replying: teardown closes the channel, and a
// reply landing then would be written into rows cleanup has already deleted.
type blockingToolChat struct{ release chan struct{} }

func (b *blockingToolChat) CompleteWithTools(ctx context.Context, model string, msgs []llm.Message, tools []llm.Tool) (llm.Message, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return llm.Message{}, errors.New("blocking test client released")
}

// withBlockingAssistant swaps in a service whose model call blocks. Tests in
// this package are sequential, and the previous service is restored on cleanup.
func withBlockingAssistant(t *testing.T) {
	t.Helper()
	t.Setenv("ZHIPU_API_KEY", "assistant-test-key")

	release := make(chan struct{})
	client := &blockingToolChat{release: release}
	svc := assistant.NewService(testHandler.Queries, testHandler.Bus, func() (llm.ToolChat, string, error) {
		return client, "test-model", nil
	})
	svc.Store = testPool
	svc.TxStarter = testPool
	svc.Exec = testHandler

	prev := testHandler.Assistant
	testHandler.Assistant = svc
	t.Cleanup(func() {
		close(release)
		testHandler.Assistant = prev
	})
}

func TestSendAssistantMessagePersistsAndAccepts(t *testing.T) {
	withBlockingAssistant(t)
	user := newAssistantTestUser(t, "assistant-send@agora.dev")
	sessionID := newAssistantTestSession(t, user)

	w := httptest.NewRecorder()
	testHandler.SendAssistantMessage(w, withURLParam(
		newAssistantRequest("POST", "/x", user, `{"content":"what is on my plate?"}`), "id", sessionID))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MessageID == "" || resp.RunID == "" || resp.CreatedAt == "" {
		t.Fatalf("incomplete 202 body: %+v", resp)
	}

	var role, content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT role, content FROM assistant_message WHERE id = $1`, resp.MessageID,
	).Scan(&role, &content); err != nil {
		t.Fatalf("user message was not persisted: %v", err)
	}
	if role != "user" || content != "what is on my plate?" {
		t.Fatalf("stored %s/%q", role, content)
	}

	// The first message names the session so the switcher is never a wall of
	// "Untitled".
	var title string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title FROM assistant_session WHERE id = $1`, sessionID).Scan(&title); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if title != "what is on my plate?" {
		t.Fatalf("auto-title = %q", title)
	}
}

func TestSendAssistantMessageConflictsWhileRunning(t *testing.T) {
	withBlockingAssistant(t)
	user := newAssistantTestUser(t, "assistant-conflict@agora.dev")
	sessionID := newAssistantTestSession(t, user)

	first := httptest.NewRecorder()
	testHandler.SendAssistantMessage(first, withURLParam(
		newAssistantRequest("POST", "/x", user, `{"content":"first"}`), "id", sessionID))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first send: expected 202, got %d: %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	testHandler.SendAssistantMessage(second, withURLParam(
		newAssistantRequest("POST", "/x", user, `{"content":"second"}`), "id", sessionID))
	if second.Code != http.StatusConflict {
		t.Fatalf("second send: expected 409, got %d: %s", second.Code, second.Body.String())
	}

	// The refused message must not be half-written into the transcript.
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM assistant_message WHERE session_id = $1 AND content = 'second'`, sessionID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a refused send persisted %d message(s)", count)
	}
}

func TestSendAssistantMessageValidatesContent(t *testing.T) {
	withBlockingAssistant(t)
	user := newAssistantTestUser(t, "assistant-validate@agora.dev")
	sessionID := newAssistantTestSession(t, user)

	for _, tc := range []struct{ name, body string }{
		{"empty", `{"content":"   "}`},
		{"missing", `{}`},
		{"too long", `{"content":"` + strings.Repeat("a", assistantMessageMaxLen+1) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler.SendAssistantMessage(w, withURLParam(
				newAssistantRequest("POST", "/x", user, tc.body), "id", sessionID))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSendAssistantMessageUnavailableWithoutKey(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "")
	user := newAssistantTestUser(t, "assistant-nokey-send@agora.dev")
	sessionID := newAssistantTestSession(t, user)

	w := httptest.NewRecorder()
	testHandler.SendAssistantMessage(w, withURLParam(
		newAssistantRequest("POST", "/x", user, `{"content":"hi"}`), "id", sessionID))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Availability
// ---------------------------------------------------------------------------

func TestAssistantAvailabilityReflectsMissingKey(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-avail@agora.dev")

	t.Setenv("ZHIPU_API_KEY", "")
	w := httptest.NewRecorder()
	testHandler.GetAssistantAvailability(w, newAssistantRequest("GET", "/api/assistant/availability", user, ""))
	var off AssistantAvailabilityResponse
	json.Unmarshal(w.Body.Bytes(), &off)
	if off.Enabled {
		t.Fatalf("no provider key must report disabled: %+v", off)
	}
	if off.ModelLabel != "" {
		t.Fatalf("a disabled assistant must not advertise a model: %+v", off)
	}

	t.Setenv("ZHIPU_API_KEY", "assistant-test-key")
	w = httptest.NewRecorder()
	testHandler.GetAssistantAvailability(w, newAssistantRequest("GET", "/api/assistant/availability", user, ""))
	var on AssistantAvailabilityResponse
	json.Unmarshal(w.Body.Bytes(), &on)
	if !on.Enabled {
		t.Fatalf("a keyed instance must report enabled: %+v", on)
	}
	if !strings.Contains(on.ModelLabel, llm.FreeModel) || !strings.Contains(on.ModelLabel, "Agora") {
		t.Fatalf("model label = %q", on.ModelLabel)
	}
}

// The assistant model defaults are per provider: flipping only the provider
// must not send the free Zhipu model id to Anthropic.
func TestAssistantModelFallsBackPerProvider(t *testing.T) {
	t.Setenv("AGORA_ASSISTANT_MODEL", "")
	if got := assistantModel("zhipu"); got != llm.FreeModel {
		t.Fatalf("zhipu default = %q", got)
	}
	if got := assistantModel("anthropic"); got != llm.DefaultAnthropicModel {
		t.Fatalf("anthropic default = %q", got)
	}
	t.Setenv("AGORA_ASSISTANT_MODEL", llm.FreeModel)
	if got := assistantModel("anthropic"); got != llm.DefaultAnthropicModel {
		t.Fatalf("anthropic must not inherit the zhipu model id, got %q", got)
	}
	t.Setenv("AGORA_ASSISTANT_MODEL", "claude-custom")
	if got := assistantModel("anthropic"); got != "claude-custom" {
		t.Fatalf("explicit model ignored: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Run loop, end to end
// ---------------------------------------------------------------------------

// scriptedToolChat replays a fixed list of model replies and records what it
// was asked, so the test can assert the loop fed tool results back.
type scriptedToolChat struct {
	mu      sync.Mutex
	replies []llm.Message
	seen    [][]llm.Message
}

func (s *scriptedToolChat) CompleteWithTools(ctx context.Context, model string, msgs []llm.Message, tools []llm.Tool) (llm.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, append([]llm.Message(nil), msgs...))
	if len(s.replies) == 0 {
		return llm.Message{}, errors.New("script exhausted")
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return reply, nil
}

func TestAssistantRunLoopEndToEnd(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-loop@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-loop-ws", "LOP")
	addAssistantTestMember(t, ws, user, "owner")
	sessionID := newAssistantTestSession(t, user)

	// The user's turn is written by the HTTP handler in production; here the
	// test writes it so Run has something to answer.
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO assistant_message (session_id, role, content) VALUES ($1, 'user', 'which workspaces am I in?')`,
		sessionID); err != nil {
		t.Fatalf("seed user message: %v", err)
	}

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: assistant.ToolListWorkspaces, Arguments: "{}"},
		}},
		{Role: "assistant", Content: "You are in Assistant assistant-loop-ws."},
	}}

	bus := events.New()
	var mu sync.Mutex
	var seen []events.Event
	bus.SubscribeAll(func(e events.Event) {
		mu.Lock()
		seen = append(seen, e)
		mu.Unlock()
	})

	svc := assistant.NewService(testHandler.Queries, bus, func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Exec = testHandler
	svc.Run(context.Background(), sessionID, "run-loop-1", user)

	// --- transcript ------------------------------------------------------
	rows, err := testPool.Query(context.Background(),
		`SELECT role, content, tool_name, tool_calls IS NOT NULL, tool_result IS NOT NULL
		   FROM assistant_message WHERE session_id = $1 ORDER BY created_at ASC, id ASC`, sessionID)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	defer rows.Close()

	type row struct {
		role, content string
		toolName      *string
		hasCalls      bool
		hasResult     bool
	}
	var transcript []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.role, &r.content, &r.toolName, &r.hasCalls, &r.hasResult); err != nil {
			t.Fatalf("scan: %v", err)
		}
		transcript = append(transcript, r)
	}
	if len(transcript) != 4 {
		t.Fatalf("expected user/assistant/tool/assistant, got %d rows: %+v", len(transcript), transcript)
	}
	if transcript[0].role != "user" {
		t.Fatalf("row 0: %+v", transcript[0])
	}
	if transcript[1].role != "assistant" || !transcript[1].hasCalls {
		t.Fatalf("row 1 should be the tool-call turn: %+v", transcript[1])
	}
	if transcript[2].role != "tool" || !transcript[2].hasResult ||
		transcript[2].toolName == nil || *transcript[2].toolName != assistant.ToolListWorkspaces {
		t.Fatalf("row 2 should be the tool answer: %+v", transcript[2])
	}
	if !strings.Contains(transcript[2].content, "assistant-loop-ws") {
		t.Fatalf("the tool answer should carry real data, got %q", transcript[2].content)
	}
	if transcript[3].role != "assistant" || transcript[3].content != "You are in Assistant assistant-loop-ws." {
		t.Fatalf("row 3 should be the final text: %+v", transcript[3])
	}

	// --- the loop fed the tool result back -------------------------------
	script.mu.Lock()
	calls := script.seen
	script.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(calls))
	}
	if calls[0][0].Role != "system" || !strings.Contains(calls[0][0].Content, "Agora Assistant") {
		t.Fatalf("first call is missing the system prompt: %+v", calls[0][0])
	}
	last := calls[1][len(calls[1])-1]
	if last.Role != "tool" || last.ToolCallID != "call_1" || !strings.Contains(last.Content, "assistant-loop-ws") {
		t.Fatalf("second call did not carry the tool result: %+v", last)
	}

	// --- events ----------------------------------------------------------
	mu.Lock()
	observed := seen
	mu.Unlock()

	counts := map[string]int{}
	var finished protocol.AssistantRunFinishedPayload
	for _, e := range observed {
		counts[e.Type]++
		if p, ok := e.Payload.(protocol.AssistantRunFinishedPayload); ok {
			finished = p
		}
		if p, ok := e.Payload.(protocol.AssistantRecipient); ok && p.RecipientUserID() != user {
			t.Fatalf("event %s addressed to the wrong user: %v", e.Type, p.RecipientUserID())
		}
	}
	if counts[protocol.EventAssistantMessage] != 3 {
		t.Fatalf("expected 3 assistant:message events (assistant, tool, assistant), got %d", counts[protocol.EventAssistantMessage])
	}
	if counts[protocol.EventAssistantToolActivity] != 1 {
		t.Fatalf("expected 1 tool-activity event, got %d", counts[protocol.EventAssistantToolActivity])
	}
	if counts[protocol.EventAssistantRunFinished] != 1 {
		t.Fatalf("expected exactly 1 run-finished event, got %d", counts[protocol.EventAssistantRunFinished])
	}
	if finished.Status != assistant.RunStatusOK || finished.RunID != "run-loop-1" || finished.SessionID != sessionID {
		t.Fatalf("run finished payload = %+v", finished)
	}
}

// A failing tool is data for the model, not the end of the run: the loop must
// hand back an {"error": ...} result and keep going.
func TestAssistantRunLoopSurvivesToolErrors(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-toolerr@agora.dev")
	sessionID := newAssistantTestSession(t, user)
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO assistant_message (session_id, role, content) VALUES ($1, 'user', 'find something')`,
		sessionID); err != nil {
		t.Fatalf("seed user message: %v", err)
	}

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "not_a_real_tool", Arguments: "{}"},
		}},
		{Role: "assistant", Content: "I could not do that."},
	}}

	bus := events.New()
	svc := assistant.NewService(testHandler.Queries, bus, func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Exec = testHandler
	svc.Run(context.Background(), sessionID, "run-toolerr-1", user)

	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM assistant_message WHERE session_id = $1 AND role = 'tool'`, sessionID,
	).Scan(&content); err != nil {
		t.Fatalf("no tool row was persisted: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatalf("tool error is not JSON: %v (%s)", err, content)
	}
	if !strings.Contains(payload["error"], "unknown tool") {
		t.Fatalf("tool error payload = %v", payload)
	}

	var final string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM assistant_message WHERE session_id = $1 AND role = 'assistant' AND content <> ''
		  ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&final); err != nil {
		t.Fatalf("the run should have continued to a text answer: %v", err)
	}
	if final != "I could not do that." {
		t.Fatalf("final answer = %q", final)
	}
}

// A run whose context is already cancelled reports cancelled, not failed —
// the user pressing stop is not an error.
func TestAssistantRunReportsCancellation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-cancel@agora.dev")
	sessionID := newAssistantTestSession(t, user)

	bus := events.New()
	var status string
	bus.Subscribe(protocol.EventAssistantRunFinished, func(e events.Event) {
		if p, ok := e.Payload.(protocol.AssistantRunFinishedPayload); ok {
			status = p.Status
		}
	})
	svc := assistant.NewService(testHandler.Queries, bus, func() (llm.ToolChat, string, error) {
		return &scriptedToolChat{}, "test-model", nil
	})
	svc.Exec = testHandler

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.Run(ctx, sessionID, "run-cancel-1", user)

	if status != assistant.RunStatusCancelled {
		t.Fatalf("expected a cancelled run, got %q", status)
	}
}

func TestCreateAssistantSessionRejectsMalformedBody(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-badbody@agora.dev")
	w := httptest.NewRecorder()
	testHandler.CreateAssistantSession(w, newAssistantRequest("POST", "/api/assistant/sessions", user, `{"title":`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// An empty body is the common case ("just give me a session").
func TestCreateAssistantSessionAcceptsEmptyBody(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-emptybody@agora.dev")
	w := httptest.NewRecorder()
	testHandler.CreateAssistantSession(w, newAssistantRequest("POST", "/api/assistant/sessions", user, ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp AssistantSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM assistant_session WHERE id = $1`, resp.ID)
	})
	if resp.ID == "" || resp.FocusWorkspaceID != nil {
		t.Fatalf("unexpected session: %+v", resp)
	}
}

// Renaming to empty must actually clear the stored title (COALESCE would
// otherwise read a NULL as "leave it alone" and silently no-op).
func TestPatchAssistantSessionClearsTitle(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-retitle@agora.dev")
	sessionID := newAssistantTestSession(t, user)

	w := httptest.NewRecorder()
	testHandler.PatchAssistantSession(w, withURLParam(
		newAssistantRequest("PATCH", "/x", user, `{"title":"Sprint questions"}`), "id", sessionID))
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.PatchAssistantSession(w, withURLParam(
		newAssistantRequest("PATCH", "/x", user, `{"title":"  "}`), "id", sessionID))
	if w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}

	var title string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title FROM assistant_session WHERE id = $1`, sessionID).Scan(&title); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if title != "" {
		t.Fatalf("title was not cleared, got %q", title)
	}
}

// A PATCH that mentions only the focus workspace must not wipe the title.
func TestPatchAssistantSessionPreservesUnmentionedFields(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-patchkeep@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-patchkeep-ws", "PKW")
	addAssistantTestMember(t, ws, user, "owner")
	sessionID := newAssistantTestSession(t, user)

	w := httptest.NewRecorder()
	testHandler.PatchAssistantSession(w, withURLParam(
		newAssistantRequest("PATCH", "/x", user, `{"title":"Keep me"}`), "id", sessionID))
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.PatchAssistantSession(w, withURLParam(
		newAssistantRequest("PATCH", "/x", user, `{"focus_workspace_id":"`+ws+`"}`), "id", sessionID))
	if w.Code != http.StatusOK {
		t.Fatalf("refocus: %d %s", w.Code, w.Body.String())
	}
	var resp AssistantSessionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Title != "Keep me" {
		t.Fatalf("title was clobbered: %+v", resp)
	}
	if resp.FocusWorkspaceID == nil || *resp.FocusWorkspaceID != ws {
		t.Fatalf("focus not set: %+v", resp)
	}
}

// focus_workspace_id is the three-way field: absent leaves it alone, JSON null
// clears it, a workspace id pins it.
//
// The middle case is the one that was broken and the one the UI needs: removing
// the assistant's context chip sends {"focus_workspace_id": null}, and under
// plain COALESCE semantics that read as "leave it alone" — so the chip vanished
// optimistically and came back on the next refetch. Decoding into *string was
// the other half of the same bug: null and absent both arrive as nil.
func TestPatchAssistantSessionFocusIsThreeWay(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-focus3@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-focus3-ws", "FC3")
	addAssistantTestMember(t, ws, user, "owner")
	sessionID := newAssistantTestSession(t, user)

	patch := func(body string) AssistantSessionResponse {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.PatchAssistantSession(w, withURLParam(
			newAssistantRequest("PATCH", "/x", user, body), "id", sessionID))
		if w.Code != http.StatusOK {
			t.Fatalf("patch %s: %d %s", body, w.Code, w.Body.String())
		}
		var resp AssistantSessionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("patch response: %v", err)
		}
		return resp
	}
	storedFocus := func() *string {
		t.Helper()
		var focus *string
		if err := testPool.QueryRow(context.Background(),
			`SELECT focus_workspace_id::text FROM assistant_session WHERE id = $1`, sessionID).Scan(&focus); err != nil {
			t.Fatalf("read session: %v", err)
		}
		return focus
	}

	// 1. A workspace id SETS it — membership-checked on the way in.
	if resp := patch(`{"focus_workspace_id":"` + ws + `"}`); resp.FocusWorkspaceID == nil || *resp.FocusWorkspaceID != ws {
		t.Fatalf("focus not set: %+v", resp)
	}
	if got := storedFocus(); got == nil || *got != ws {
		t.Fatalf("stored focus = %v, want %s", got, ws)
	}

	// 2. A PATCH that does not mention the field LEAVES IT ALONE.
	if resp := patch(`{"title":"Renamed"}`); resp.FocusWorkspaceID == nil || *resp.FocusWorkspaceID != ws {
		t.Fatalf("an unrelated patch moved the focus: %+v", resp)
	}

	// 3. Explicit null CLEARS it — the context chip's remove button.
	if resp := patch(`{"focus_workspace_id":null}`); resp.FocusWorkspaceID != nil {
		t.Fatalf("focus was not cleared: %+v", resp)
	}
	if got := storedFocus(); got != nil {
		t.Fatalf("stored focus = %q, want NULL", *got)
	}
	// …and the title survived it, so clearing is not a wipe.
	if resp := patch(`{}`); resp.Title != "Renamed" {
		t.Fatalf("clearing the focus clobbered the title: %+v", resp)
	}

	// An empty string means the same as null: some clients clear an input
	// rather than delete the key.
	patch(`{"focus_workspace_id":"` + ws + `"}`)
	if resp := patch(`{"focus_workspace_id":""}`); resp.FocusWorkspaceID != nil {
		t.Fatalf("an empty string did not clear the focus: %+v", resp)
	}

	// A workspace the caller is not in is still refused, cleared or not.
	stranger := newAssistantTestWorkspace(t, "assistant-focus3-other-ws", "FC4")
	w := httptest.NewRecorder()
	testHandler.PatchAssistantSession(w, withURLParam(
		newAssistantRequest("PATCH", "/x", user, `{"focus_workspace_id":"`+stranger+`"}`), "id", sessionID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("focus on a workspace the user is not in = %d %s", w.Code, w.Body.String())
	}
}

func TestCancelAssistantRunIsOwnershipChecked(t *testing.T) {
	withBlockingAssistant(t)
	owner := newAssistantTestUser(t, "assistant-cancelrun@agora.dev")
	stranger := newAssistantTestUser(t, "assistant-cancelrun-other@agora.dev")
	sessionID := newAssistantTestSession(t, owner)

	send := httptest.NewRecorder()
	testHandler.SendAssistantMessage(send, withURLParam(
		newAssistantRequest("POST", "/x", owner, `{"content":"long question"}`), "id", sessionID))
	if send.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", send.Code, send.Body.String())
	}
	var accepted SendAssistantMessageResponse
	json.Unmarshal(send.Body.Bytes(), &accepted)

	w := httptest.NewRecorder()
	testHandler.CancelAssistantRun(w, withURLParam(
		newAssistantRequest("POST", "/x", stranger, ""), "id", accepted.RunID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("a stranger cancelling must 404, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.CancelAssistantRun(w, withURLParam(
		newAssistantRequest("POST", "/x", owner, ""), "id", accepted.RunID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner cancel: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Malformed run id → 400 per the handler UUID convention (run ids are
	// pure-UUID request inputs, validated with parseUUIDOrBadRequest since
	// the durable-run rework); a WELL-FORMED but unknown id → 404.
	w = httptest.NewRecorder()
	testHandler.CancelAssistantRun(w, withURLParam(
		newAssistantRequest("POST", "/x", owner, ""), "id", "no-such-run"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed run id: expected 400, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	testHandler.CancelAssistantRun(w, withURLParam(
		newAssistantRequest("POST", "/x", owner, ""), "id", "00000000-0000-4000-8000-000000000000"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown run: expected 404, got %d", w.Code)
	}
}
