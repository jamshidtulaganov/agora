package handler

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// zohoIdentityFixture is one issue created by an agent in the test
// workspace, a second member, and a task queue row factory.
type zohoIdentityFixture struct {
	agentID  string
	issueID  string
	memberID string // a member other than testUserID
}

func newZohoIdentityFixture(t *testing.T, number int) zohoIdentityFixture {
	t.Helper()
	ctx := context.Background()
	fx := zohoIdentityFixture{agentID: createHandlerTestAgent(t, fmt.Sprintf("zoho-id-agent-%d", number), nil)}

	email := fmt.Sprintf("zoho-identity-%d-%d@agora-example.com", number, time.Now().UnixNano())
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ('Zoho Identity Member', $1) RETURNING id`, email,
	).Scan(&fx.memberID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, fx.memberID) })
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, testWorkspaceID, fx.memberID,
	); err != nil {
		t.Fatalf("add member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'zoho identity issue', 'todo', 'medium', $2, 'agent', $3, 0)
		RETURNING id
	`, testWorkspaceID, fx.agentID, 93000+number).Scan(&fx.issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, fx.issueID) })
	return fx
}

func (fx zohoIdentityFixture) task(t *testing.T, cols string, vals ...any) string {
	t.Helper()
	args := append([]any{fx.agentID, handlerTestRuntimeID(t)}, vals...)
	placeholders := ""
	for i := range vals {
		placeholders += fmt.Sprintf(", $%d", i+3)
	}
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority`+cols+`)
		VALUES ($1, $2, 'queued', 2`+placeholders+`)
		RETURNING id
	`, args...).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

func assertZohoIdentity(t *testing.T, taskID, wantUser, wantReason string) {
	t.Helper()
	user, reason, ok := testHandler.zohoActingUserForTask(context.Background(), parseUUID(taskID))
	if wantUser == "" {
		if ok {
			t.Fatalf("expected no identity, got %s (%s)", uuidToString(user), reason)
		}
		return
	}
	if !ok || uuidToString(user) != wantUser || reason != wantReason {
		t.Fatalf("identity = %s (%q, ok=%v), want %s (%q)", uuidToString(user), reason, ok, wantUser, wantReason)
	}
}

func TestZohoActingUser_AssignerOfAgentCreatedIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newZohoIdentityFixture(t, 1)
	taskID := fx.task(t, ", issue_id", fx.issueID)

	// Agent-created issue with no human assignment: nobody to act for.
	assertZohoIdentity(t, taskID, "", "")

	// A member hands it to the agent: the task now works for them — the
	// last human assigner wins over an earlier one.
	for i, actor := range []string{testUserID, fx.memberID} {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
			VALUES ($1, $2, 'member', $3, 'assignee_changed', $4, now() + $5 * interval '1 second')
		`, testWorkspaceID, fx.issueID, actor, fmt.Sprintf(`{"to_type":"agent","to_id":%q}`, fx.agentID), i); err != nil {
			t.Fatalf("log assignment: %v", err)
		}
	}
	assertZohoIdentity(t, taskID, fx.memberID, "assigned the issue")
}

func TestZohoActingUser_MentionAuthorWinsOverAssigner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newZohoIdentityFixture(t, 2)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details)
		VALUES ($1, $2, 'member', $3, 'assignee_changed', $4)
	`, testWorkspaceID, fx.issueID, testUserID, fmt.Sprintf(`{"to_type":"agent","to_id":%q}`, fx.agentID)); err != nil {
		t.Fatalf("log assignment: %v", err)
	}
	var commentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, '@agent please check the deal') RETURNING id
	`, testWorkspaceID, fx.issueID, fx.memberID).Scan(&commentID); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	taskID := fx.task(t, ", issue_id, trigger_comment_id", fx.issueID, commentID)
	assertZohoIdentity(t, taskID, fx.memberID, "mentioned the agent")
}

func TestZohoActingUser_ChatInitiatorAndRetryInherit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newZohoIdentityFixture(t, 3)
	parent := fx.task(t, ", initiator_user_id", fx.memberID)
	assertZohoIdentity(t, parent, fx.memberID, "started this conversation")

	// A retry clone carries only parent_task_id; it works for the same person.
	retry := fx.task(t, ", parent_task_id", parent)
	assertZohoIdentity(t, retry, fx.memberID, "started this conversation")
}

func TestZohoActingUser_SubIssueWalksToParent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newZohoIdentityFixture(t, 4)
	// The parent was created by a member; the agent made a sub-issue of it.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET creator_type = 'member', creator_id = $2 WHERE id = $1`, fx.issueID, fx.memberID,
	); err != nil {
		t.Fatalf("set parent creator: %v", err)
	}
	var childID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, parent_issue_id)
		VALUES ($1, 'zoho identity sub-issue', 'todo', 'medium', $2, 'agent', 93104, 0, $3)
		RETURNING id
	`, testWorkspaceID, fx.agentID, fx.issueID).Scan(&childID); err != nil {
		t.Fatalf("create sub-issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, childID) })

	taskID := fx.task(t, ", issue_id", childID)
	assertZohoIdentity(t, taskID, fx.memberID, "created the issue")
}

func TestQuickCreateRequester(t *testing.T) {
	id := "0199a0b2-7c3d-7e4f-8a9b-0c1d2e3f4a5b"
	if got, ok := quickCreateRequester([]byte(`{"type":"quick_create","requester_id":"` + id + `"}`)); !ok || uuidToString(got) != id {
		t.Fatalf("quick create requester = %v %v", uuidToString(got), ok)
	}
	for _, raw := range []string{``, `{}`, `{"type":"chat","requester_id":"` + id + `"}`, `{"type":"quick_create","requester_id":"nope"}`, `not json`} {
		if _, ok := quickCreateRequester([]byte(raw)); ok {
			t.Fatalf("expected no requester for %q", raw)
		}
	}
}
