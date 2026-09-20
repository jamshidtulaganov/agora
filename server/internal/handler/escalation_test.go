package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/taskfailure"
)

// Escalation lifecycle, end to end against a real database
// (docs/orchestration-upgrade-plan.md §B1). Every assertion here is a
// property the design depends on, not an implementation detail:
//
//   - one OPEN escalation per issue (a second raise refines, never stacks)
//   - raising parks the run in waiting_human — not failed, not completed
//   - answering resumes with the session pointer intact
//   - only a human may answer
//   - the workspace fence holds on every read and write

// escalationTestIssue seeds an issue plus a running task for the workspace's
// test agent, and returns (issueID, agentID, taskID).
func escalationTestIssue(t *testing.T) (string, string, string) {
	t.Helper()
	ctx := t.Context()

	agentID := createHandlerTestAgent(t, "escalation-agent-"+escalationRandSuffix(), nil)

	var issueID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, title, creator_type, creator_id, number, status,
		                    assignee_type, assignee_id)
		 VALUES ($1::uuid, 'escalation issue', 'member', $2::uuid,
		         (3000000 + floor(random()*1000000))::int, 'in_progress', 'agent', $3::uuid)
		 RETURNING id::text`,
		testWorkspaceID, testUserID, agentID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		testPool.Exec(cctx, `DELETE FROM task_escalation WHERE issue_id = $1::uuid`, issueID)
		testPool.Exec(cctx, `DELETE FROM agent_task_queue WHERE issue_id = $1::uuid`, issueID)
		testPool.Exec(cctx, `DELETE FROM issue WHERE id = $1::uuid`, issueID)
	})

	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)
	// The daemon pins the resume pointer mid-flight; the parked row is what
	// the resume reads it back from.
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET session_id = 'sess-escalation' WHERE id = $1::uuid`, taskID); err != nil {
		t.Fatalf("pin session: %v", err)
	}
	return issueID, agentID, taskID
}

func escalationRandSuffix() string {
	var s string
	testPool.QueryRow(context.Background(), `SELECT substr(gen_random_uuid()::text, 1, 8)`).Scan(&s)
	return s
}

// agentEscalateRequest builds a raise request carrying the agent identity
// headers resolveActor requires (both X-Agent-ID and a consistent X-Task-ID).
func agentEscalateRequest(t *testing.T, issueID, agentID, taskID string, body any) *http.Request {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/escalations", body)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	return withURLParam(req, "id", issueID)
}

func decodeEscalation(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode escalation: %v (body=%s)", err, rr.Body.String())
	}
	return out
}

// TestEscalationRaiseParksTheRun: the raise inserts one open row, parks the
// task in waiting_human (NOT failed — an escalation is not a failure), and
// carries the prompt onto the task's wait_reason so every task surface can
// say what is being waited on.
func TestEscalationRaiseParksTheRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	issueID, agentID, taskID := escalationTestIssue(t)

	rr := httptest.NewRecorder()
	testHandler.RaiseEscalation(rr, agentEscalateRequest(t, issueID, agentID, taskID, map[string]any{
		"need":    "Should the export be CSV or XLSX?",
		"tried":   "Read the issue and the linked PR; neither says.",
		"options": []string{"CSV", "XLSX"},
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("raise: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	esc := decodeEscalation(t, rr)
	if esc["status"] != "open" {
		t.Errorf("want status open, got %v", esc["status"])
	}
	if esc["kind"] != service.EscalationKindQuestion {
		t.Errorf("want kind question (the default), got %v", esc["kind"])
	}
	opts, _ := esc["options"].([]any)
	if len(opts) != 2 {
		t.Errorf("want 2 options round-tripped, got %v", esc["options"])
	}

	if got := taskStatus(t, taskID); got != "waiting_human" {
		t.Fatalf("want the run parked in waiting_human, got %q", got)
	}
	var waitReason string
	testPool.QueryRow(context.Background(),
		`SELECT COALESCE(wait_reason, '') FROM agent_task_queue WHERE id = $1::uuid`, taskID).Scan(&waitReason)
	if waitReason != "Should the export be CSV or XLSX?" {
		t.Errorf("wait_reason should carry the prompt, got %q", waitReason)
	}
}

// TestEscalationSecondRaiseRefinesTheSameRow: the partial unique index makes
// "one open escalation per issue" a database guarantee. A second raise must
// UPDATE — stacking a second question onto the same human is the exact
// failure ("agents DDoSing our attention") the design exists to prevent.
func TestEscalationSecondRaiseRefinesTheSameRow(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	issueID, agentID, taskID := escalationTestIssue(t)

	rr := httptest.NewRecorder()
	testHandler.RaiseEscalation(rr, agentEscalateRequest(t, issueID, agentID, taskID, map[string]any{"need": "first question"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("first raise: %d %s", rr.Code, rr.Body.String())
	}
	firstID := decodeEscalation(t, rr)["id"]

	rr2 := httptest.NewRecorder()
	testHandler.RaiseEscalation(rr2, agentEscalateRequest(t, issueID, agentID, taskID, map[string]any{
		"need": "actually, this sharper question",
		"kind": "blocked",
	}))
	if rr2.Code != http.StatusOK {
		t.Fatalf("second raise: %d %s", rr2.Code, rr2.Body.String())
	}
	second := decodeEscalation(t, rr2)
	if second["id"] != firstID {
		t.Errorf("second raise must refine the SAME row, got a new id %v (was %v)", second["id"], firstID)
	}
	if second["prompt"] != "actually, this sharper question" {
		t.Errorf("prompt should be replaced, got %v", second["prompt"])
	}
	if second["kind"] != "blocked" {
		t.Errorf("kind should be replaced, got %v", second["kind"])
	}

	var open int
	testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM task_escalation WHERE issue_id = $1::uuid AND status = 'open'`, issueID).Scan(&open)
	if open != 1 {
		t.Fatalf("want exactly 1 open escalation on the issue, got %d", open)
	}
}

// TestEscalationRaiseRejectsHumanActor: a human with a question comments.
// Letting one raise an escalation would let them park someone else's run.
func TestEscalationRaiseRejectsHumanActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	issueID, _, _ := escalationTestIssue(t)

	rr := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/escalations",
		map[string]any{"need": "let me in"}), "id", issueID)
	testHandler.RaiseEscalation(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a human raiser, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestEscalationResolveResumesWithTheSession: answering re-enqueues a task
// that (a) carries the parked run's session pointer so the CLI resumes
// instead of re-reading the repo, (b) does NOT spend the retry budget, and
// (c) is pointed at the answer comment, which is how the answer becomes the
// prompt.
func TestEscalationResolveResumesWithTheSession(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	issueID, agentID, taskID := escalationTestIssue(t)

	rr := httptest.NewRecorder()
	testHandler.RaiseEscalation(rr, agentEscalateRequest(t, issueID, agentID, taskID,
		map[string]any{"need": "CSV or XLSX?"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("raise: %d %s", rr.Code, rr.Body.String())
	}
	escID, _ := decodeEscalation(t, rr)["id"].(string)

	rr2 := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/escalations/"+escID+"/resolve",
		map[string]any{"answer": "CSV, and keep the header row."}), "escalationId", escID)
	testHandler.ResolveEscalation(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Fatalf("resolve: want 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	answered := decodeEscalation(t, rr2)
	if answered["status"] != "answered" {
		t.Fatalf("want status answered, got %v", answered["status"])
	}
	if answered["answer"] != "CSV, and keep the header row." {
		t.Errorf("answer not recorded, got %v", answered["answer"])
	}
	resumedID, _ := answered["resumed_task_id"].(string)
	if resumedID == "" {
		t.Fatal("answering must resume the parked run")
	}

	resumed, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(resumedID))
	if err != nil {
		t.Fatalf("load resumed task: %v", err)
	}
	if resumed.Status != "queued" {
		t.Errorf("resumed task should be queued, got %q", resumed.Status)
	}
	if resumed.SessionID.String != "sess-escalation" {
		t.Errorf("resume must carry the parked session pointer, got %q", resumed.SessionID.String)
	}
	if resumed.ForceFreshSession {
		t.Error("resume must NOT force a fresh session — that would discard the agent's context")
	}
	if resumed.Attempt != 1 {
		t.Errorf("an answered question is not a failed attempt; want attempt 1, got %d", resumed.Attempt)
	}
	if !resumed.TriggerCommentID.Valid {
		t.Error("resume must point at the answer comment — that is how the answer becomes the prompt")
	} else {
		comment, err := testHandler.Queries.GetComment(ctx, resumed.TriggerCommentID)
		if err != nil {
			t.Fatalf("load answer comment: %v", err)
		}
		if comment.AuthorType != "member" {
			t.Errorf("the answer comment should be attributed to the human who decided, got %q", comment.AuthorType)
		}
	}

	// A second answer must lose the race rather than resume twice.
	rr3 := httptest.NewRecorder()
	testHandler.ResolveEscalation(rr3, withURLParam(newRequest(http.MethodPost,
		"/api/escalations/"+escID+"/resolve", map[string]any{"answer": "second opinion"}),
		"escalationId", escID))
	if rr3.Code != http.StatusConflict {
		t.Fatalf("want 409 on a second answer, got %d: %s", rr3.Code, rr3.Body.String())
	}
}

// TestEscalationWorkspaceFence: an escalation in another workspace is not
// readable or resolvable through this workspace's header, even with the
// right id. Multi-tenancy is enforced on the escalation row AND on the issue.
func TestEscalationWorkspaceFence(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	issueID, agentID, taskID := escalationTestIssue(t)

	rr := httptest.NewRecorder()
	testHandler.RaiseEscalation(rr, agentEscalateRequest(t, issueID, agentID, taskID,
		map[string]any{"need": "tenant check"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("raise: %d %s", rr.Code, rr.Body.String())
	}
	escID, _ := decodeEscalation(t, rr)["id"].(string)

	var otherWS string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix)
		 VALUES ('Other WS', 'esc-other-'||substr(gen_random_uuid()::text,1,8), '', 'OTH')
		 RETURNING id::text`).Scan(&otherWS); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, otherWS)
	})

	rr2 := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/escalations/"+escID+"/resolve", map[string]any{"answer": "nope"})
	req.Header.Set("X-Workspace-ID", otherWS)
	testHandler.ResolveEscalation(rr2, withURLParam(req, "escalationId", escID))
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace resolve must 404, got %d: %s", rr2.Code, rr2.Body.String())
	}

	// And the escalation is still open.
	esc, err := testHandler.Queries.GetTaskEscalation(ctx, db.GetTaskEscalationParams{
		ID: parseUUID(escID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("reload escalation: %v", err)
	}
	if esc.Status != "open" {
		t.Fatalf("cross-workspace call changed the row: status %q", esc.Status)
	}
}

// TestBudgetExhaustedEscalatesAndDoesNotRetry is the load-bearing pair from
// §B2: a blown budget must NOT take the auto-retry path (retrying spends the
// thing that ran out), and must land as an open kind='budget' escalation so
// the ceiling is visible instead of looking like a broken daemon.
func TestBudgetExhaustedEscalatesAndDoesNotRetry(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	issueID, _, taskID := escalationTestIssue(t)

	// The failure path only runs for a task it can transition out of
	// 'running' — which is exactly the state the seed leaves it in.
	if _, err := testHandler.TaskService.FailTask(ctx, parseUUID(taskID),
		"Budget limit reached: $5.00", "sess-escalation", "", string(taskfailure.ReasonBudgetExhausted)); err != nil {
		t.Fatalf("fail task: %v", err)
	}

	var retries int
	testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1::uuid`, taskID).Scan(&retries)
	if retries != 0 {
		t.Fatalf("a blown budget must not auto-retry; found %d child task(s)", retries)
	}

	esc, err := testHandler.Queries.GetOpenTaskEscalationForIssue(ctx, db.GetOpenTaskEscalationForIssueParams{
		IssueID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("a blown budget must open an escalation: %v", err)
	}
	if esc.Kind != service.EscalationKindBudget {
		t.Errorf("want kind budget, got %q", esc.Kind)
	}
	if esc.Status != "open" {
		t.Errorf("want an open escalation, got %q", esc.Status)
	}
}

// TestEscalationInboxItem: the raise must reach a person. The inbox item is
// the notification half of the state — without it the escalation is a row
// nobody looks at, which is exactly the "comment as convention" failure.
func TestEscalationInboxItem(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	issueID, agentID, taskID := escalationTestIssue(t)

	rr := httptest.NewRecorder()
	testHandler.RaiseEscalation(rr, agentEscalateRequest(t, issueID, agentID, taskID,
		map[string]any{"need": "which environment?"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("raise: %d %s", rr.Code, rr.Body.String())
	}

	var itemType, severity, body string
	if err := testPool.QueryRow(context.Background(),
		`SELECT type, severity, COALESCE(body,'') FROM inbox_item
		 WHERE issue_id = $1::uuid AND type = 'escalation' LIMIT 1`, issueID,
	).Scan(&itemType, &severity, &body); err != nil {
		t.Fatalf("expected an escalation inbox item: %v", err)
	}
	if severity != "action_required" {
		t.Errorf("an escalation is by definition action_required, got %q", severity)
	}
	if body != "which environment?" {
		t.Errorf("inbox body should carry the ask, got %q", body)
	}
}
