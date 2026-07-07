package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

	svc.CaptureTestRuns(ctx, issue, content, util.MustParseUUID(agentID))

	var runs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM test_run WHERE test_case_id=$1 AND issue_id=$2`, caseID, issueID).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 1 {
		t.Fatalf("expected 1 test_run recorded from the real block, got %d — capture logic is the fault", runs)
	}
}
