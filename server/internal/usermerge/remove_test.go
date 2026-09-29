package usermerge

import (
	"context"
	"fmt"
	"strings"
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
	if got := results[0]; got.WorkspaceSlug != f.migratedSlug || got.Unassigned != 1 || !got.Removed || got.Role != "member" {
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
	if len(results) != 1 || results[0].Outcome != "kept: owner, pass --allow-owner to remove" {
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

// roster pads a keep list up to KeepEmailsMinimum with addresses that belong
// to nobody, so a test can exercise keep mode without tripping the guard.
func roster(emails ...string) []string {
	out := append([]string{}, emails...)
	for i := len(out); i < KeepEmailsMinimum; i++ {
		out = append(out, fmt.Sprintf("filler-%d@company.example.com", i))
	}
	return out
}

// TestRemoveMembersKeepListRemovesTheAbsent: the roster names who stays;
// everyone else in a migrated workspace goes, including people no explicit
// list would have named.
func TestRemoveMembersKeepListRemovesTheAbsent(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)

	// "Stayed" is on the roster; the fixture's own user is not.
	var stayedEmail string
	pool.QueryRow(ctx, `SELECT email FROM "user" WHERE email LIKE 'stayed-%'`).Scan(&stayedEmail)
	if stayedEmail == "" {
		t.Fatal("fixture user missing")
	}

	// Scoped to this test's own workspace: keep mode otherwise sweeps every
	// migrated workspace in the database, which in a shared test database
	// means whatever another test happens to have left lying around.
	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{
		KeepEmails: roster(stayedEmail), WorkspaceSlug: f.migratedSlug, Apply: true,
	})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	var removed []string
	for _, r := range results {
		if r.Removed {
			removed = append(removed, r.Email)
		}
	}
	if len(removed) != 1 || removed[0] != f.email {
		t.Fatalf("removed = %v, want only %s", removed, f.email)
	}

	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.migratedWS).Scan(&n)
	if n != 0 {
		t.Errorf("absent member survived")
	}
	// The ordinary workspace is out of scope and keeps them.
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.plainWS).Scan(&n)
	if n != 1 {
		t.Errorf("a workspace outside the scope must be left alone")
	}
	var assigneeType *string
	pool.QueryRow(ctx, `SELECT assignee_type FROM issue WHERE id = $1`, f.assignedIssue).Scan(&assigneeType)
	if assigneeType != nil {
		t.Errorf("issue should have been unassigned")
	}
}

// TestRemoveMembersKeepListHonoursAliases: an account merged under a second
// address is kept when the roster names either address. Without this, every
// merged account would be removed the first time the tool runs.
func TestRemoveMembersKeepListHonoursAliases(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)

	alias := "former-" + f.email
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_email_alias (user_id, email) VALUES ($1, $2)`, f.userID, alias); err != nil {
		t.Fatalf("add alias: %v", err)
	}

	// The roster knows only the alias, never the account's own address.
	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{
		KeepEmails: roster(alias), WorkspaceSlug: f.migratedSlug, Apply: true,
	})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	for _, r := range results {
		if r.Email == f.email && r.Removed {
			t.Fatalf("an account whose alias is on the roster must be kept: %+v", r)
		}
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.migratedWS).Scan(&n)
	if n != 1 {
		t.Errorf("membership was removed despite the alias being on the roster")
	}
}

// TestRemoveMembersKeepListRefusesShortRoster: a truncated roster would empty
// every workspace, and nothing else in the call would look wrong.
func TestRemoveMembersKeepListRefusesShortRoster(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, err := RemoveMembers(ctx, pool, RemoveMembersOptions{
		KeepEmails: []string{"someone@company.example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

// TestRemoveMembersKeepListRejectsBothModes: removing the named and removing
// the unnamed are opposite instructions.
func TestRemoveMembersKeepListRejectsBothModes(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, err := RemoveMembers(ctx, pool, RemoveMembersOptions{
		Emails: []string{"a@b.test"}, KeepEmails: roster("c@d.test"),
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("err = %v, want a rejection", err)
	}
}

// TestRemoveMembersLeavesATrail: nulling the assignee is the only record that
// the person held the issue, and raw SQL bypasses the application's activity
// listener. Both the metadata stamp and the timeline row must survive, or a
// removal silently erases who owned 112 issues.
func TestRemoveMembersLeavesATrail(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)

	if _, err := RemoveMembers(ctx, pool, RemoveMembersOptions{
		Emails: []string{f.email}, WorkspaceSlug: f.migratedSlug, Apply: true,
	}); err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}

	var from, reason string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(metadata->>'unassigned_from',''), COALESCE(metadata->>'unassigned_reason','')
		   FROM issue WHERE id = $1`, f.assignedIssue).Scan(&from, &reason); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if from != f.email {
		t.Errorf("metadata.unassigned_from = %q, want %q", from, f.email)
	}
	if reason == "" {
		t.Errorf("metadata.unassigned_reason is empty")
	}

	var action, fromType, fromID, fromEmail, actorType string
	if err := pool.QueryRow(ctx, `
		SELECT action, COALESCE(details->>'from_type',''), COALESCE(details->>'from_id',''),
		       COALESCE(details->>'from_email',''), COALESCE(actor_type,'')
		  FROM activity_log WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1`,
		f.assignedIssue).Scan(&action, &fromType, &fromID, &fromEmail, &actorType); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if action != "assignee_changed" || fromType != "member" || fromID != f.userID ||
		fromEmail != f.email || actorType != "system" {
		t.Errorf("activity row = %s actor=%s from=%s/%s/%s", action, actorType, fromType, fromID, fromEmail)
	}

	// Somebody else's issue gets neither stamp nor timeline row.
	var otherFrom string
	pool.QueryRow(ctx, `SELECT COALESCE(metadata->>'unassigned_from','') FROM issue WHERE id = $1`,
		f.untouchedIssue).Scan(&otherFrom)
	if otherFrom != "" {
		t.Errorf("another person's issue was stamped: %q", otherFrom)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM activity_log WHERE issue_id = $1`, f.untouchedIssue).Scan(&n)
	if n != 0 {
		t.Errorf("another person's issue got %d activity row(s)", n)
	}
}

// TestRemoveMembersAllowOwnerNeedsAnother: --allow-owner removes an owner only
// where another owner remains. A workspace with no owner has nobody who can
// invite, change roles or delete it, and the product offers no way back.
func TestRemoveMembersAllowOwnerNeedsAnother(t *testing.T) {
	f, ctx := newRemoveFixture(t)
	pool := testPool(t)
	if _, err := pool.Exec(ctx, `UPDATE member SET role = 'owner' WHERE user_id = $1 AND workspace_id = $2`,
		f.userID, f.migratedWS); err != nil {
		t.Fatal(err)
	}

	// Sole owner: refused even with the flag.
	pool.Exec(ctx, `DELETE FROM member WHERE workspace_id = $1 AND user_id <> $2`, f.migratedWS, f.userID)
	results, err := RemoveMembers(ctx, pool, RemoveMembersOptions{
		Emails: []string{f.email}, WorkspaceSlug: f.migratedSlug, AllowOwner: true, Apply: true,
	})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != "kept: the only owner, give someone else owner first" {
		t.Fatalf("sole owner should be kept: %+v", results)
	}

	// A second owner exists: now it goes.
	var otherID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ('Co Owner', $1) RETURNING id::text`,
		"co-"+f.email).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, otherID) })
	if _, err := pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, f.migratedWS, otherID); err != nil {
		t.Fatal(err)
	}
	results, err = RemoveMembers(ctx, pool, RemoveMembersOptions{
		Emails: []string{f.email}, WorkspaceSlug: f.migratedSlug, AllowOwner: true, Apply: true,
	})
	if err != nil {
		t.Fatalf("RemoveMembers: %v", err)
	}
	if len(results) != 1 || !results[0].Removed {
		t.Fatalf("owner should be removed when another remains: %+v", results)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE workspace_id = $1 AND role = 'owner'`,
		f.migratedWS).Scan(&left)
	if left != 1 {
		t.Errorf("owners left = %d, want 1", left)
	}
}
