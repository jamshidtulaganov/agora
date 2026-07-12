package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestNotifyReviewVerdict covers the review inbox matrix: fail →
// review_failed (action_required); pass with the other gates green (qa:pass)
// → merge_ready (action_required, "awaiting your approval"); pass with gates
// not green → review_passed (info); the acting human is excluded.
func TestNotifyReviewVerdict(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	var ownerID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('owner',$1) RETURNING id`,
		"rev-notify-owner-"+uuid.NewString()[:8]+"@x.dev").Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, ownerID) })
	var runtimeID, agentID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'rn-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES ($1,'Author Agent','cloud','{}'::jsonb,$2,'workspace',3,$3) RETURNING id`,
		util.UUIDToString(wsID), runtimeID, ownerID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	var creatorID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('creator',$1) RETURNING id`,
		"rev-notify-creator-"+uuid.NewString()[:8]+"@x.dev").Scan(&creatorID); err != nil {
		t.Fatalf("seed creator: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, creatorID) })
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,assignee_type,assignee_id,number)
		VALUES ($1,'review notify issue','in_review','member',$2,'agent',$3,1) RETURNING id`,
		util.UUIDToString(wsID), creatorID, agentID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	svc := NewTaskService(q, pool, nil, events.New())

	countItems := func(itemType, recipientID string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type=$2 AND recipient_id=$3`,
			issueID, itemType, recipientID).Scan(&n)
		return n
	}
	severityOf := func(itemType string) string {
		var s string
		pool.QueryRow(ctx, `SELECT severity FROM inbox_item WHERE issue_id=$1 AND type=$2 LIMIT 1`, issueID, itemType).Scan(&s)
		return s
	}

	// 1. FAIL → review_failed / action_required for the agent's OWNER + creator.
	svc.NotifyReviewVerdict(ctx, issue, "fail", "agent", pgtype.UUID{}, "1 blocker")
	if got := countItems("review_failed", ownerID); got != 1 {
		t.Errorf("fail: owner review_failed items = %d, want 1", got)
	}
	if got := countItems("review_failed", creatorID); got != 1 {
		t.Errorf("fail: creator review_failed items = %d, want 1", got)
	}
	if s := severityOf("review_failed"); s != "action_required" {
		t.Errorf("review_failed severity = %q, want action_required", s)
	}

	// 2. PASS with gates NOT green (no qa:pass label) → review_passed / info.
	svc.NotifyReviewVerdict(ctx, issue, "pass", "agent", pgtype.UUID{}, "clean")
	if got := countItems("review_passed", ownerID); got != 1 {
		t.Errorf("pass w/o green gates: owner review_passed items = %d, want 1", got)
	}
	if got := countItems("merge_ready", ownerID); got != 0 {
		t.Errorf("pass w/o green gates: merge_ready items = %d, want 0", got)
	}
	if s := severityOf("review_passed"); s != "info" {
		t.Errorf("review_passed severity = %q, want info", s)
	}

	// 3. PASS with qa:pass present → merge_ready / action_required.
	label, err := q.CreateLabel(ctx, db.CreateLabelParams{WorkspaceID: wsID, Name: "qa:pass", Color: "#22c55e"})
	if err != nil {
		t.Fatalf("create qa:pass label: %v", err)
	}
	if err := q.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: label.ID, WorkspaceID: wsID,
	}); err != nil {
		t.Fatalf("attach qa:pass: %v", err)
	}
	svc.NotifyReviewVerdict(ctx, issue, "pass", "agent", pgtype.UUID{}, "all green")
	if got := countItems("merge_ready", ownerID); got != 1 {
		t.Errorf("pass with green gates: owner merge_ready items = %d, want 1", got)
	}
	if s := severityOf("merge_ready"); s != "action_required" {
		t.Errorf("merge_ready severity = %q, want action_required", s)
	}

	// 4. Actor exclusion: a member-actored verdict never notifies the actor.
	svc.NotifyReviewVerdict(ctx, issue, "fail", "member", util.MustParseUUID(creatorID), "human re-review")
	if got := countItems("review_failed", creatorID); got != 1 {
		t.Errorf("actor exclusion: creator review_failed items = %d, want still 1", got)
	}
	if got := countItems("review_failed", ownerID); got != 2 {
		t.Errorf("member fail: owner review_failed items = %d, want 2", got)
	}
}
