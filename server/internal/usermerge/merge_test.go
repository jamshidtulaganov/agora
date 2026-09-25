package usermerge

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

type fixture struct {
	pool                 *pgxpool.Pool
	keepID, dropID       string
	keepEmail, dropEmail string
	sharedWS, dropOnlyWS string
	issueID              string
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("database not available: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newFixture: the kept account is the silent company account (admin in a
// shared workspace, never signed in); the dropped account is the gmail
// sign-up the person used (owner of the shared workspace, sole member of
// another, creator and assignee of an issue, has pinned the same issue as
// the kept account, has a timezone set).
func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	n := time.Now().UnixNano()
	f := fixture{
		pool:      pool,
		keepEmail: fmt.Sprintf("keep-%d@company.example.com", n),
		dropEmail: fmt.Sprintf("drop-%d@gmail.example.com", n),
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Kept', $1) RETURNING id::text`, f.keepEmail).Scan(&f.keepID))
	must(pool.QueryRow(ctx, `INSERT INTO "user" (name, email, onboarded_at, timezone) VALUES ('Dropped', $1, now(), 'Asia/Tashkent') RETURNING id::text`, f.dropEmail).Scan(&f.dropID))
	must(pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('Merge shared', $1) RETURNING id::text`, fmt.Sprintf("merge-shared-%d", n)).Scan(&f.sharedWS))
	must(pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('Merge drop only', $1) RETURNING id::text`, fmt.Sprintf("merge-drop-%d", n)).Scan(&f.dropOnlyWS))
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = ANY($1::uuid[])`, []string{f.sharedWS, f.dropOnlyWS})
		pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = ANY($1::uuid[])`, []string{f.keepID, f.dropID})
		pool.Exec(context.Background(), `DELETE FROM user_email_alias WHERE email = $1`, f.dropEmail)
	})

	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'admin'), ($1, $3, 'owner'), ($4, $3, 'member')`,
		f.sharedWS, f.keepID, f.dropID, f.dropOnlyWS)
	must(err)
	must(pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, creator_type, creator_id, assignee_type, assignee_id)
		VALUES ($1, 'Fix the export', 'member', $2, 'member', $2) RETURNING id::text`, f.sharedWS, f.dropID).Scan(&f.issueID))
	_, err = pool.Exec(ctx, `INSERT INTO pinned_item (workspace_id, user_id, item_type, item_id) VALUES ($1, $2, 'issue', $4), ($1, $3, 'issue', $4)`,
		f.sharedWS, f.keepID, f.dropID, f.issueID)
	must(err)
	return f
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestMergeFoldsTheDroppedAccountIntoTheKeptOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	report, err := Merge(ctx, f.pool, Options{KeepEmail: f.keepEmail, DropEmail: f.dropEmail, Apply: true})
	if err != nil {
		t.Fatal(err)
	}

	// Shared workspace: the higher role wins, one membership remains.
	var role string
	if err := f.pool.QueryRow(ctx, `SELECT role FROM member WHERE workspace_id = $1 AND user_id = $2`, f.sharedWS, f.keepID).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "owner" {
		t.Fatalf("shared workspace role = %s, want owner", role)
	}
	if len(report.Roles) != 1 || report.Roles[0].FinalRole != "owner" {
		t.Fatalf("role report = %+v", report.Roles)
	}
	// A workspace only the dropped account was in moves over as is.
	if got := count(t, f.pool, `SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2 AND role = 'member'`, f.dropOnlyWS, f.keepID); got != 1 {
		t.Fatalf("drop-only membership moved %d times", got)
	}
	// Polymorphic references (no foreign key) move too.
	if got := count(t, f.pool, `SELECT count(*) FROM issue WHERE id = $1 AND creator_id = $2 AND assignee_id = $2`, f.issueID, f.keepID); got != 1 {
		t.Fatal("issue creator/assignee not moved")
	}
	// A duplicate under a unique key is removed, not moved.
	if got := count(t, f.pool, `SELECT count(*) FROM pinned_item WHERE item_id = $1`, f.issueID); got != 1 {
		t.Fatalf("pinned_item rows = %d, want 1", got)
	}
	// Nothing points at the dropped account any more, and it is gone.
	if got := count(t, f.pool, `SELECT count(*) FROM "user" WHERE id = $1`, f.dropID); got != 0 {
		t.Fatal("dropped account still exists")
	}
	// The kept account gets what it was missing, keeps its own name.
	var name, tz string
	var onboarded bool
	if err := f.pool.QueryRow(ctx, `SELECT name, coalesce(timezone,''), onboarded_at IS NOT NULL FROM "user" WHERE id = $1`, f.keepID).Scan(&name, &tz, &onboarded); err != nil {
		t.Fatal(err)
	}
	if name != "Kept" || tz != "Asia/Tashkent" || !onboarded {
		t.Fatalf("profile = %q %q onboarded=%v", name, tz, onboarded)
	}

	// The dropped address now signs in to the kept account.
	q := db.New(f.pool)
	u, err := q.GetUserByEmail(ctx, f.dropEmail)
	if err != nil {
		t.Fatalf("alias lookup: %v", err)
	}
	if got := uuidString(u.ID); got != f.keepID {
		t.Fatalf("alias resolves to %s, want the kept account %s", got, f.keepID)
	}
	if len(report.Aliases) != 1 || report.Aliases[0] != f.dropEmail {
		t.Fatalf("aliases = %v", report.Aliases)
	}
}

func TestMergeDryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	report, err := Merge(ctx, f.pool, Options{KeepEmail: f.keepEmail, DropEmail: f.dropEmail})
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || len(report.Columns) == 0 {
		t.Fatalf("dry run report = %+v", report)
	}
	if got := count(t, f.pool, `SELECT count(*) FROM "user" WHERE id = $1`, f.dropID); got != 1 {
		t.Fatal("dry run deleted the dropped account")
	}
	if got := count(t, f.pool, `SELECT count(*) FROM user_email_alias WHERE email = $1`, f.dropEmail); got != 0 {
		t.Fatal("dry run added an alias")
	}
	if got := count(t, f.pool, `SELECT count(*) FROM issue WHERE id = $1 AND creator_id = $2`, f.issueID, f.dropID); got != 1 {
		t.Fatal("dry run moved the issue")
	}
}

func TestMergeRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := map[string]Options{
		"same address": {KeepEmail: f.keepEmail, DropEmail: f.keepEmail},
		"missing keep": {KeepEmail: "nobody@example.com", DropEmail: f.dropEmail},
		"empty drop":   {KeepEmail: f.keepEmail},
	}
	for name, opts := range cases {
		if _, err := Merge(ctx, f.pool, opts); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestAddMembersNeverLowersARole(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var slug string
	if err := f.pool.QueryRow(ctx, `SELECT slug FROM workspace WHERE id = $1`, f.sharedWS).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	results, err := AddMembers(ctx, f.pool, AddMembersOptions{
		WorkspaceSlug: slug, Role: "member", Emails: []string{f.keepEmail}, Apply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != "already admin" {
		t.Fatalf("outcome = %q", results[0].Outcome)
	}
	if _, err := AddMembers(ctx, f.pool, AddMembersOptions{WorkspaceSlug: slug, Role: "boss", Emails: []string{f.keepEmail}}); err == nil {
		t.Fatal("unknown role accepted")
	}
}
