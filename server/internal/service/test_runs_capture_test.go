package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestCaptureTestRunsRealBlock is the regression for the base-suite promotion
// gap (task_b0f15704): a run_test_cases agent posted a well-formed
// ```test-runs``` block referencing real base-suite case ids, yet ZERO
// test_run rows were recorded. This feeds the EXACT block shape the prod agent
// emitted through CaptureTestRuns to localize the fault to the capture logic
// vs the delivery path.
func TestCaptureTestRunsRealBlock(t *testing.T) {
	pool := knowledgeTestPool(t) // shared helper: skips cleanly without Postgres
	ctx := context.Background()
	q := db.New(pool)

	wsID := seedKnowledgeWorkspace(t, pool)

	var projectID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1,'trcap','planned','none') RETURNING id`,
		util.UUIDToString(wsID)).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('trcap',$1) RETURNING id`, "trcap-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,project_id,number)
		VALUES ($1,'trcap issue','in_review','member',$2,$3,1) RETURNING id`,
		util.UUIDToString(wsID), userID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue := db.Issue{
		ID:          util.MustParseUUID(issueID),
		WorkspaceID: wsID,
		ProjectID:   util.MustParseUUID(projectID),
		Status:      "in_review",
		Number:      1,
	}

	var runtimeID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'trcap-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	var agentID string
	pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks) VALUES ($1,'QA Tester','cloud','{}'::jsonb,$2,'workspace',3) RETURNING id`, util.UUIDToString(wsID), runtimeID).Scan(&agentID)

	// An issue-scoped automated case (sameIssue path — exactly the base-suite
	// tracking-issue shape).
	var caseID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO test_case (workspace_id,issue_id,project_id,title,steps,expected,kind,source,author_type,author_id,category,script)
		VALUES ($1,$2,$3,'[e2e] dash','open','ok','automated','agent','agent',$4,'positive','node x.mjs') RETURNING id`,
		util.UUIDToString(wsID), issueID, projectID, agentID).Scan(&caseID); err != nil {
		t.Fatalf("seed test_case: %v", err)
	}

	svc := NewTaskService(q, pool, nil, events.New())

	// The EXACT block shape the prod agent emitted: prose + '---' + fenced
	// ```test-runs``` JSON array (real field set, real fence).
	content := fmt.Sprintf(
		"## Результаты запуска\n\nВсе кейсы прогнаны.\n\n---\n\n```test-runs\n[\n  {\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"title contains X\",\"baseline_status\":\"unknown\"}\n]\n```\n",
		caseID)

	svc.CaptureTestRuns(ctx, issue, content, util.MustParseUUID(agentID), pgtype.UUID{})

	var runs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM test_run WHERE test_case_id=$1 AND issue_id=$2`, caseID, issueID).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 1 {
		t.Fatalf("expected 1 test_run recorded from the real block, got %d — capture logic is the fault", runs)
	}
}

// TestCaptureStructuredResultPromotes covers the delivery-path fix: when the
// visible-comment fallback is suppressed (agent already commented mid-run), the
// final output's ```test-runs``` block is still captured, and on an
// already-done tracking issue the cases promote into the standing base suite.
func TestCaptureStructuredResultPromotes(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	var projectID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1,'csr-proj','planned','none') RETURNING id`,
		util.UUIDToString(wsID)).Scan(&projectID); err != nil {
		t.Fatalf("project: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('csr',$1) RETURNING id`, "csr-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	// DONE tracking issue (base-suite shape).
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,project_id,number)
		VALUES ($1,'csr issue','done','member',$2,$3,7) RETURNING id`,
		util.UUIDToString(wsID), userID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("issue: %v", err)
	}
	var runtimeID, agentID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'csr-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks) VALUES ($1,'QA Tester','cloud','{}'::jsonb,$2,'workspace',3) RETURNING id`, util.UUIDToString(wsID), runtimeID).Scan(&agentID)
	var caseID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO test_case (workspace_id,issue_id,project_id,title,steps,expected,kind,source,author_type,author_id,category,script)
		VALUES ($1,$2,$3,'[e2e] golden','open','ok','automated','agent','agent',$4,'positive','node x.mjs') RETURNING id`,
		util.UUIDToString(wsID), issueID, projectID, agentID).Scan(&caseID); err != nil {
		t.Fatalf("case: %v", err)
	}

	svc := NewTaskService(q, pool, nil, events.New())
	content := fmt.Sprintf("done\n\n```test-runs\n[{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok\",\"baseline_status\":\"unknown\"}]\n```", caseID)

	svc.captureStructuredResult(ctx, util.MustParseUUID(issueID), util.MustParseUUID(agentID), pgtype.UUID{}, content)

	var runs, promoted int
	pool.QueryRow(ctx, `SELECT count(*) FROM test_run WHERE test_case_id=$1`, caseID).Scan(&runs)
	pool.QueryRow(ctx, `SELECT count(*) FROM test_case WHERE project_id=$1 AND issue_id IS NULL AND kind='automated' AND script<>''`, projectID).Scan(&promoted)
	if runs != 1 {
		t.Fatalf("expected 1 run recorded from final output, got %d", runs)
	}
	if promoted != 1 {
		t.Fatalf("expected 1 case promoted to standing base suite, got %d", promoted)
	}
}

// TestCaptureTestRunsScopedSingleCaseFailsClosed is the regression for the
// unenforced-scope audit finding: the Test-cases panel's per-row "Run" button
// dispatches run_test_cases scoped to ONE case via a prose "RUN ONLY the
// single test case id=<uuid>" clause (test-cases-panel.tsx), but nothing
// server-side ever enforced it — CaptureTestRuns wrote every entry an agent
// emitted, so a scoped run that also (accidentally or not) reported OTHER
// cases silently overwrote their state too. This seeds two cases, dispatches
// a run scoped to case A (a trigger comment carrying the scope marker), and
// feeds a test-runs block reporting BOTH A and B — only A may be recorded.
func TestCaptureTestRunsScopedSingleCaseFailsClosed(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	var projectID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1,'scope-proj','planned','none') RETURNING id`,
		util.UUIDToString(wsID)).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('scope',$1) RETURNING id`, "scope-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,project_id,number)
		VALUES ($1,'scope issue','in_review','member',$2,$3,1) RETURNING id`,
		util.UUIDToString(wsID), userID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue := db.Issue{
		ID:          util.MustParseUUID(issueID),
		WorkspaceID: wsID,
		ProjectID:   util.MustParseUUID(projectID),
		Status:      "in_review",
		Number:      1,
	}
	var runtimeID, agentID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'scope-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks) VALUES ($1,'QA Tester','cloud','{}'::jsonb,$2,'workspace',3) RETURNING id`, util.UUIDToString(wsID), runtimeID).Scan(&agentID)

	var caseAID, caseBID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO test_case (workspace_id,issue_id,project_id,title,steps,expected,kind,source,author_type,author_id,category)
		VALUES ($1,$2,$3,'[e2e] case A','open','ok','automated','agent','agent',$4,'positive') RETURNING id`,
		util.UUIDToString(wsID), issueID, projectID, agentID).Scan(&caseAID); err != nil {
		t.Fatalf("seed case A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO test_case (workspace_id,issue_id,project_id,title,steps,expected,kind,source,author_type,author_id,category)
		VALUES ($1,$2,$3,'[e2e] case B','open','ok','automated','agent','agent',$4,'positive') RETURNING id`,
		util.UUIDToString(wsID), issueID, projectID, agentID).Scan(&caseBID); err != nil {
		t.Fatalf("seed case B: %v", err)
	}

	svc := NewTaskService(q, pool, nil, events.New())

	// The trigger comment carries EXACTLY the scope clause the Test-cases
	// panel's runOne mutation sends (test-cases-panel.tsx), wrapped the way
	// buildSliceInstruction appends it ("... Focus on: <scope>").
	trigger, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: wsID,
		AuthorType:  "member",
		AuthorID:    util.MustParseUUID(userID),
		Content: fmt.Sprintf(
			"[@QA Tester](mention://agent/%s) Run the automated cases. Focus on: RUN ONLY the single test case id=%s (\"case A\") — execute just this one, skip all other cases.",
			agentID, caseAID),
		Type: "comment",
	})
	if err != nil {
		t.Fatalf("seed trigger comment: %v", err)
	}

	// The agent's reply reports BOTH cases — B is outside the scope it was
	// dispatched with and must be dropped, not written.
	content := fmt.Sprintf(
		"```test-runs\n[{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok\"},{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok\"}]\n```",
		caseAID, caseBID)

	svc.CaptureTestRuns(ctx, issue, content, util.MustParseUUID(agentID), trigger.ID)

	var runsA, runsB int
	pool.QueryRow(ctx, `SELECT count(*) FROM test_run WHERE test_case_id=$1`, caseAID).Scan(&runsA)
	pool.QueryRow(ctx, `SELECT count(*) FROM test_run WHERE test_case_id=$1`, caseBID).Scan(&runsB)
	if runsA != 1 {
		t.Fatalf("expected the scoped case A to record 1 run, got %d", runsA)
	}
	if runsB != 0 {
		t.Fatalf("expected the OUT-OF-SCOPE case B to record 0 runs (fail-closed), got %d", runsB)
	}
}

// TestCaptureTestRunsScopedSetFailsClosed covers the SET form of the scope
// marker — "RUN ONLY these test cases ids=a,b" — emitted by the Test-cases
// panel's "Re-run failed (N)" button (Phase 2, item 4). Three cases; the
// dispatch scopes to A+B; the agent reports A, B AND C — C is outside the
// set and must be dropped.
func TestCaptureTestRunsScopedSetFailsClosed(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	var projectID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1,'scope-set-proj','planned','none') RETURNING id`,
		util.UUIDToString(wsID)).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('scopeset',$1) RETURNING id`, "scopeset-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,project_id,number)
		VALUES ($1,'scope set issue','in_review','member',$2,$3,1) RETURNING id`,
		util.UUIDToString(wsID), userID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue := db.Issue{
		ID:          util.MustParseUUID(issueID),
		WorkspaceID: wsID,
		ProjectID:   util.MustParseUUID(projectID),
		Status:      "in_review",
		Number:      1,
	}
	var runtimeID, agentID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'scopeset-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks) VALUES ($1,'QA Tester','cloud','{}'::jsonb,$2,'workspace',3) RETURNING id`, util.UUIDToString(wsID), runtimeID).Scan(&agentID)

	seedCase := func(title string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO test_case (workspace_id,issue_id,project_id,title,steps,expected,kind,source,author_type,author_id,category)
			VALUES ($1,$2,$3,$4,'open','ok','automated','agent','agent',$5,'positive') RETURNING id`,
			util.UUIDToString(wsID), issueID, projectID, title, agentID).Scan(&id); err != nil {
			t.Fatalf("seed case %s: %v", title, err)
		}
		return id
	}
	caseA, caseB, caseC := seedCase("[e2e] A"), seedCase("[e2e] B"), seedCase("[e2e] C")

	svc := NewTaskService(q, pool, nil, events.New())

	trigger, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: wsID, AuthorType: "member", AuthorID: util.MustParseUUID(userID),
		Content: fmt.Sprintf(
			"[@QA Tester](mention://agent/%s) Run the automated cases. Focus on: RUN ONLY these test cases ids=%s,%s — execute just these currently-failing cases, skip all other cases.",
			agentID, caseA, caseB),
		Type: "comment",
	})
	if err != nil {
		t.Fatalf("seed trigger comment: %v", err)
	}

	content := fmt.Sprintf(
		"```test-runs\n[{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok\"},{\"test_case_id\":\"%s\",\"status\":\"fail\",\"output\":\"still broken\"},{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok\"}]\n```",
		caseA, caseB, caseC)
	svc.CaptureTestRuns(ctx, issue, content, util.MustParseUUID(agentID), trigger.ID)

	countRuns := func(caseID string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM test_run WHERE test_case_id=$1`, caseID).Scan(&n)
		return n
	}
	if got := countRuns(caseA); got != 1 {
		t.Errorf("case A (in set): runs = %d, want 1", got)
	}
	if got := countRuns(caseB); got != 1 {
		t.Errorf("case B (in set): runs = %d, want 1", got)
	}
	if got := countRuns(caseC); got != 0 {
		t.Errorf("case C (OUT of set): runs = %d, want 0 (fail-closed)", got)
	}
}
