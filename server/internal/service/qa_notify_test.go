package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestNotifyQAVerdict covers the typed QA inbox dispatch matrix (Phase 2,
// item 2): a fail notifies (qa_failed, action_required) resolving an
// AGENT-assigned issue to the agent's OWNER; a recovery pass notifies
// (qa_passed, info); a routine non-recovery pass notifies NOBODY.
func TestNotifyQAVerdict(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	// The agent's OWNER — the human who must be notified for an
	// agent-assigned issue.
	var ownerID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('owner',$1) RETURNING id`,
		"qa-notify-owner-"+uuid.NewString()[:8]+"@x.dev").Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	var runtimeID, agentID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'qn-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES ($1,'Dev Agent','cloud','{}'::jsonb,$2,'workspace',3,$3) RETURNING id`,
		util.UUIDToString(wsID), runtimeID, ownerID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	var creatorID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('creator',$1) RETURNING id`,
		"qa-notify-creator-"+uuid.NewString()[:8]+"@x.dev").Scan(&creatorID); err != nil {
		t.Fatalf("seed creator: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,assignee_type,assignee_id,number)
		VALUES ($1,'qa notify issue','in_review','member',$2,'agent',$3,1) RETURNING id`,
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

	// 1. FAIL → qa_failed for the agent's OWNER and the creator.
	svc.NotifyQAVerdict(ctx, issue, "fail", false, "agent", pgtype.UUID{}, "step 2 broke")
	if got := countItems("qa_failed", ownerID); got != 1 {
		t.Errorf("agent-assigned fail: owner qa_failed items = %d, want 1", got)
	}
	if got := countItems("qa_failed", creatorID); got != 1 {
		t.Errorf("fail: creator qa_failed items = %d, want 1", got)
	}
	var severity string
	pool.QueryRow(ctx, `SELECT severity FROM inbox_item WHERE issue_id=$1 AND type='qa_failed' LIMIT 1`, issueID).Scan(&severity)
	if severity != "action_required" {
		t.Errorf("qa_failed severity = %q, want action_required", severity)
	}

	// 2. Routine pass (no recovery) → NOBODY.
	svc.NotifyQAVerdict(ctx, issue, "pass", false, "agent", pgtype.UUID{}, "all green")
	if got := countItems("qa_passed", ownerID) + countItems("qa_passed", creatorID); got != 0 {
		t.Errorf("routine pass: qa_passed items = %d, want 0 (no notification noise)", got)
	}

	// 3. RECOVERY pass → qa_passed.
	svc.NotifyQAVerdict(ctx, issue, "pass", true, "agent", pgtype.UUID{}, "fixed and re-verified")
	if got := countItems("qa_passed", ownerID); got != 1 {
		t.Errorf("recovery pass: owner qa_passed items = %d, want 1", got)
	}

	// 4. A human override by the CREATOR must not notify the creator (actor
	// excluded) but still notifies the owner.
	svc.NotifyQAVerdict(ctx, issue, "fail", false, "member", util.MustParseUUID(creatorID), "overridden")
	if got := countItems("qa_failed", creatorID); got != 1 {
		t.Errorf("actor exclusion: creator qa_failed items = %d, want still 1 (no self-notification)", got)
	}
	if got := countItems("qa_failed", ownerID); got != 2 {
		t.Errorf("override fail: owner qa_failed items = %d, want 2", got)
	}
}
