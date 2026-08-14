package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestValidateDevServerURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"https ok", "https://jamshid.sdteam.uz", "https://jamshid.sdteam.uz", false},
		{"http ok + trims", "  http://sandbox.sdteam.uz/app  ", "http://sandbox.sdteam.uz/app", false},
		{"empty", "   ", "", true},
		{"bad scheme", "ftp://box.example.com", "", true},
		{"no host", "https://", "", true},
		{"credentials rejected", "https://user:pass@box.example.com", "", true},
		{"too long", "https://x.example.com/" + strings.Repeat("a", 500), "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, verr := validateDevServerURL(c.in)
			if c.wantErr {
				if verr == "" {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if verr != "" {
				t.Fatalf("unexpected error: %s", verr)
			}
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestUserDevServerResolution proves the preview ladder's user_dev_server
// tier: an issue routes to its DEVELOPER's declared standing dev server —
// member assignee directly, agent assignee via the agent's owner — and
// resolves "" when no assignee or no declared box (falling through to
// qa_smoke_url, absent here).
func TestUserDevServerResolution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var pid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority)
		 VALUES ($1, 'dev-server-test', 'in_progress', 'none') RETURNING id`,
		testWorkspaceID).Scan(&pid); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, pid) })

	var memberID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM member WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID).Scan(&memberID); err != nil {
		t.Fatalf("load member: %v", err)
	}

	const boxURL = "https://jamshid.sdteam.uz"
	if _, err := testHandler.Queries.UpsertUserDevServer(ctx, db.UpsertUserDevServerParams{
		WorkspaceID: testUUID(testWorkspaceID),
		ProjectID:   testUUID(pid),
		UserID:      testUUID(testUserID),
		BaseUrl:     boxURL,
	}); err != nil {
		t.Fatalf("upsert dev server: %v", err)
	}

	// number is assigned max+1 by default, which collides on back-to-back
	// inserts in one test — spread explicit numbers instead.
	var numberBase int32
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1000 FROM issue WHERE workspace_id = $1`,
		testWorkspaceID).Scan(&numberBase); err != nil {
		t.Fatalf("load issue number base: %v", err)
	}
	newIssue := func(assigneeType, assigneeID string) db.Issue {
		t.Helper()
		numberBase++
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, assignee_type, assignee_id, number)
			VALUES ($1, $2, 'dev-server issue', 'todo', 'member', $3, NULLIF($4, ''), NULLIF($5, '')::uuid, $6)
			RETURNING id`,
			testWorkspaceID, pid, memberID, assigneeType, assigneeID, numberBase).Scan(&id); err != nil {
			t.Fatalf("create issue: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id) })
		issue, err := testHandler.Queries.GetIssue(ctx, testUUID(id))
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		return issue
	}

	// Member assignee → their own box. A member assignee stores the USER id in
	// assignee_id (not the member row id), matching production data.
	memberIssue := newIssue("member", testUserID)
	if got := testHandler.userDevServerURL(ctx, memberIssue); got != boxURL {
		t.Errorf("member assignee: got %q want %q", got, boxURL)
	}
	if got := testHandler.resolveQAPreviewURL(ctx, memberIssue); got != boxURL {
		t.Errorf("resolveQAPreviewURL member assignee: got %q want %q", got, boxURL)
	}

	// Agent assignee → the agent's OWNER's box.
	agentID := createHandlerTestAgent(t, "dev-server-agent", []byte(`[]`))
	agentIssue := newIssue("agent", agentID)
	if got := testHandler.userDevServerURL(ctx, agentIssue); got != boxURL {
		t.Errorf("agent assignee: got %q want %q", got, boxURL)
	}

	// No assignee → no developer → tier resolves "".
	unassigned := newIssue("", "")
	if got := testHandler.userDevServerURL(ctx, unassigned); got != "" {
		t.Errorf("unassigned: got %q want empty", got)
	}

	// Upsert replaces (second declaration wins).
	const boxURL2 = "https://jamshid2.sdteam.uz"
	if _, err := testHandler.Queries.UpsertUserDevServer(ctx, db.UpsertUserDevServerParams{
		WorkspaceID: testUUID(testWorkspaceID),
		ProjectID:   testUUID(pid),
		UserID:      testUUID(testUserID),
		BaseUrl:     boxURL2,
	}); err != nil {
		t.Fatalf("re-upsert dev server: %v", err)
	}
	if got := testHandler.userDevServerURL(ctx, memberIssue); got != boxURL2 {
		t.Errorf("after re-upsert: got %q want %q", got, boxURL2)
	}

	// Delete → tier falls through to "".
	if err := testHandler.Queries.DeleteUserDevServer(ctx, db.DeleteUserDevServerParams{
		ProjectID: testUUID(pid),
		UserID:    testUUID(testUserID),
	}); err != nil {
		t.Fatalf("delete dev server: %v", err)
	}
	if got := testHandler.userDevServerURL(ctx, memberIssue); got != "" {
		t.Errorf("after delete: got %q want empty", got)
	}
}
