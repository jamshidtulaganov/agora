package usermerge

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type removeFixture struct {
	userID, email  string
	migratedWS     string
	migratedSlug   string
	plainWS        string
	plainSlug      string
	assignedIssue  string
	untouchedIssue string
}

// newRemoveFixture: one person who is a member of a Zoho-migrated workspace
// (assigned an issue there) and of an ordinary workspace, plus a second issue
// assigned to somebody else that must not be touched.
func newRemoveFixture(t *testing.T) (*removeFixture, context.Context) {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	n := time.Now().UnixNano()
	f := &removeFixture{
		email:        fmt.Sprintf("left-%d@company.example.com", n),
		migratedSlug: fmt.Sprintf("zoho-mig-%d", n),
		plainSlug:    fmt.Sprintf("plain-%d", n),
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var otherID string
	must(pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Left', $1) RETURNING id::text`, f.email).Scan(&f.userID))
	must(pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Stayed', $1) RETURNING id::text`,
		fmt.Sprintf("stayed-%d@company.example.com", n)).Scan(&otherID))
	must(pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, settings) VALUES ('Zoho migrated', $1, '{"zoho_project_id":"911"}'::jsonb) RETURNING id::text`,
		f.migratedSlug).Scan(&f.migratedWS))
	must(pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('Plain', $1) RETURNING id::text`,
		f.plainSlug).Scan(&f.plainWS))
	for _, ws := range []string{f.migratedWS, f.plainWS} {
		_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, ws, f.userID)
		must(err)
	}
	must(pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, number, title, status, creator_type, creator_id, assignee_type, assignee_id)
		VALUES ($1, 1, 'Theirs', 'todo', 'member', $2, 'member', $2) RETURNING id::text`,
		f.migratedWS, f.userID).Scan(&f.assignedIssue))
	must(pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, number, title, status, creator_type, creator_id, assignee_type, assignee_id)
		VALUES ($1, 2, 'Someone else''s', 'todo', 'member', $2, 'member', $2) RETURNING id::text`,
		f.migratedWS, otherID).Scan(&f.untouchedIssue))

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM workspace WHERE id = ANY($1::uuid[])`, []string{f.migratedWS, f.plainWS})
		pool.Exec(ctx, `DELETE FROM "user" WHERE id = ANY($1::uuid[])`, []string{f.userID, otherID})
	})
	return f, ctx
}

// TestRemoveMembersDefaultsToMigratedWorkspaces: the cleanup touches only the
// workspaces the Zoho migration created, and unassigns before removing.
func TestRemoveMembersDefaultsToMigratedWorkspaces(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)

	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{Emails: []string{f.email}, Apply: true})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want only the migrated workspace", results)
	}
	if got := results[0]; got.WorkspaceSlug != f.migratedSlug || got.Unassigned != 1 || got.Outcome != "removed (member)" {
		t.Errorf("result = %+v", got)
	}

	var members int
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.migratedWS).Scan(&members)
	if members != 0 {
		t.Errorf("membership survived the removal")
	}
	// The ordinary workspace is out of scope and keeps them.
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.plainWS).Scan(&members)
	if members != 1 {
		t.Errorf("a non-migrated workspace must be left alone")
	}

	var assigneeType *string
	pool.QueryRow(ctx, `SELECT assignee_type FROM issue WHERE id = $1`, f.assignedIssue).Scan(&assigneeType)
	if assigneeType != nil {
		t.Errorf("issue assignee_type = %v, want NULL", *assigneeType)
	}
	// Somebody else's issue is untouched.
	pool.QueryRow(ctx, `SELECT assignee_type FROM issue WHERE id = $1`, f.untouchedIssue).Scan(&assigneeType)
	if assigneeType == nil || *assigneeType != "member" {
		t.Errorf("another person's issue lost its assignee")
	}
}

// TestRemoveMembersDryRunWritesNothing: the default run reports exactly what
// --apply would do and then rolls it back.
func TestRemoveMembersDryRunWritesNothing(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)

	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{Emails: []string{f.email}})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if len(results) != 1 || results[0].Unassigned != 1 {
		t.Fatalf("dry run should report the same work: %+v", results)
	}
	var members int
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1`, f.userID).Scan(&members)
	if members != 2 {
		t.Errorf("dry run changed %d membership(s)", 2-members)
	}
	var assigneeType *string
	pool.QueryRow(ctx, `SELECT assignee_type FROM issue WHERE id = $1`, f.assignedIssue).Scan(&assigneeType)
	if assigneeType == nil {
		t.Errorf("dry run unassigned an issue")
	}
}

// TestRemoveMembersKeepsOwners: a workspace with no owner cannot be
// administered, so the tool refuses rather than leaving one behind.
func TestRemoveMembersKeepsOwners(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)
	if _, err := pool.Exec(ctx, `UPDATE member SET role = 'owner' WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.migratedWS); err != nil {
		t.Fatal(err)
	}

	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{Emails: []string{f.email}, Apply: true})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != "kept: owner, remove by hand if intended" {
		t.Fatalf("result = %+v", results)
	}
	var members int
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.migratedWS).Scan(&members)
	if members != 1 {
		t.Errorf("owner was removed")
	}
}

// TestRemoveMembersUnknownEmail: an address with no account is reported, not
// an error that aborts the rest of the list.
func TestRemoveMembersUnknownEmail(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{Emails: []string{"nobody@nowhere.example.com"}})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != "no account" {
		t.Errorf("result = %+v", results)
	}
}
