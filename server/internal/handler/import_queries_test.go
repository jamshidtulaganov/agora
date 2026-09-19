package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Data-layer tests for the importer foundation (migrations 205-207 and
// pkg/db/queries/imports.sql). They exercise the three properties the rest of
// the importer is built on and cannot re-derive for itself:
//
//   - the source token is sealed at rest and is not reachable through any
//     query an endpoint would use for a response;
//   - an import run is a ROW, with a lifecycle two callers cannot both claim;
//   - a re-import upserts. Issues match on the external_ref blob, comments on
//     the partial unique index, and original timestamps survive both.

const importTestSealKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes, test-only

func importTestToken() string { return "lin_api_TESTONLY_" + strings.Repeat("z", 8) }

// importTestWorkspace creates a workspace of this test's own. The importer
// bumps the issue counter and writes issues, comments and dependencies; doing
// that in the shared handler fixture would perturb every other test that reads
// it. Same reasoning as TestIncrementIssueCounterHealsLag.
func importTestWorkspace(t *testing.T) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	slug := "import-test-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	if len(slug) > 60 {
		slug = slug[:60]
	}

	testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	var wsID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Import Test', $1, '', 'IMP')
		RETURNING id
	`, slug).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE slug = $1`, slug)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, wsID, testUserID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return parseUUID(wsID)
}

// newImportConnection seals a token and stores it, returning the row id.
func newImportConnection(t *testing.T, ws pgtype.UUID, label string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()

	sealed, err := imports.SealSecret(importTestToken())
	if err != nil {
		t.Fatalf("SealSecret: %v", err)
	}
	row, err := testHandler.Queries.CreateImportConnection(ctx, db.CreateImportConnectionParams{
		WorkspaceID:     ws,
		Source:          "linear",
		Label:           label,
		SecretEncrypted: sealed,
		CreatedBy:       parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateImportConnection: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM import_connection WHERE id = $1`, row.ID)
	})
	return row.ID
}

func TestImportConnectionNeverReturnsTheToken(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv(imports.SecretKeyEnv, importTestSealKey)
	ctx := context.Background()
	ws := importTestWorkspace(t)

	id := newImportConnection(t, ws, "never-returns")

	// Whatever a handler marshals from the read queries, the plaintext token is
	// not in it — the row types do not carry the column.
	got, err := testHandler.Queries.GetImportConnection(ctx, db.GetImportConnectionParams{
		ID:          id,
		WorkspaceID: ws,
	})
	if err != nil {
		t.Fatalf("GetImportConnection: %v", err)
	}
	list, err := testHandler.Queries.ListImportConnections(ctx, ws)
	if err != nil {
		t.Fatalf("ListImportConnections: %v", err)
	}
	blob, err := json.Marshal(struct {
		One  db.GetImportConnectionRow     `json:"one"`
		Many []db.ListImportConnectionsRow `json:"many"`
	}{got, list})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), importTestToken()) {
		t.Fatalf("a read query exposed the plaintext token: %s", blob)
	}
	if strings.Contains(string(blob), "secret") {
		t.Fatalf("a read query exposed a secret-bearing field: %s", blob)
	}

	// The sealed column is reachable only through the query named for it, and
	// what comes back is ciphertext that opens to the original token.
	full, err := testHandler.Queries.GetImportConnectionSecret(ctx, db.GetImportConnectionSecretParams{
		ID:          id,
		WorkspaceID: ws,
	})
	if err != nil {
		t.Fatalf("GetImportConnectionSecret: %v", err)
	}
	if strings.Contains(string(full.SecretEncrypted), importTestToken()) {
		t.Fatal("secret_encrypted holds the token in the clear")
	}
	opened, err := imports.OpenSecret(full.SecretEncrypted)
	if err != nil {
		t.Fatalf("OpenSecret: %v", err)
	}
	if opened != importTestToken() {
		t.Fatalf("round trip through the DB = %q, want the original token", opened)
	}
}

func TestImportConnectionIsWorkspaceScopedAndRotatable(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv(imports.SecretKeyEnv, importTestSealKey)
	ctx := context.Background()
	ws := importTestWorkspace(t)

	id := newImportConnection(t, ws, "rotate")

	// A probe verdict sticks...
	if _, err := testHandler.Queries.UpdateImportConnectionProbe(ctx, db.UpdateImportConnectionProbeParams{
		ID:          id,
		WorkspaceID: ws,
		ProbeStatus: "invalid",
	}); err != nil {
		t.Fatalf("UpdateImportConnectionProbe: %v", err)
	}

	// ...until the token is rotated, which must clear it: a stale 'invalid'
	// must not outlive the credential that earned it.
	rotated, err := imports.SealSecret("lin_api_ROTATED")
	if err != nil {
		t.Fatalf("SealSecret: %v", err)
	}
	row, err := testHandler.Queries.CreateImportConnection(ctx, db.CreateImportConnectionParams{
		WorkspaceID:     ws,
		Source:          "linear",
		Label:           "rotate",
		SecretEncrypted: rotated,
		CreatedBy:       parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if uuidToString(row.ID) != uuidToString(id) {
		t.Fatalf("rotation created a second row (%s vs %s); (workspace, source, label) must be one connection",
			uuidToString(row.ID), uuidToString(id))
	}
	if row.ProbeStatus != "" || row.ProbedAt.Valid {
		t.Errorf("rotation kept a stale probe verdict: status=%q probed=%v", row.ProbeStatus, row.ProbedAt.Valid)
	}

	// A read from another workspace is a miss, not a leak.
	otherWS := parseUUID("00000000-0000-0000-0000-0000000000ff")
	if _, err := testHandler.Queries.GetImportConnection(ctx, db.GetImportConnectionParams{
		ID: id, WorkspaceID: otherWS,
	}); err == nil {
		t.Error("GetImportConnection returned a row for the wrong workspace")
	}
	affected, err := testHandler.Queries.DeleteImportConnection(ctx, db.DeleteImportConnectionParams{
		ID: id, WorkspaceID: otherWS,
	})
	if err != nil {
		t.Fatalf("DeleteImportConnection: %v", err)
	}
	if affected != 0 {
		t.Errorf("DeleteImportConnection removed %d rows from the wrong workspace", affected)
	}
}

// The job is a row, and its lifecycle is the thing the Bitrix process-global
// could not give: a claim two callers cannot both win, and state that outlives
// the process.
func TestImportJobLifecycle(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv(imports.SecretKeyEnv, importTestSealKey)
	ctx := context.Background()
	ws := importTestWorkspace(t)

	connID := newImportConnection(t, ws, "job-lifecycle")
	job, err := testHandler.Queries.CreateImportJob(ctx, db.CreateImportJobParams{
		WorkspaceID:  ws,
		ConnectionID: connID,
		Source:       "linear",
		Status:       "pending",
		Scope:        []byte(`{"containers":["ENG"]}`),
		CreatedBy:    parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM import_job WHERE id = $1`, job.ID)
	})
	if string(job.Totals) != "{}" || string(job.Failures) != "[]" {
		t.Errorf("new job totals=%s failures=%s, want {} and []", job.Totals, job.Failures)
	}

	// A second import in this workspace is refused by pointing at this one.
	active, err := testHandler.Queries.GetActiveImportJob(ctx, ws)
	if err != nil {
		t.Fatalf("GetActiveImportJob: %v", err)
	}
	if uuidToString(active.ID) != uuidToString(job.ID) {
		t.Fatalf("GetActiveImportJob = %s, want the pending job %s", uuidToString(active.ID), uuidToString(job.ID))
	}

	planned, err := testHandler.Queries.SetImportJobPlan(ctx, db.SetImportJobPlanParams{
		ID:          job.ID,
		WorkspaceID: ws,
		Status:      "awaiting_confirm",
		Plan:        []byte(`{"issues":240,"unreachable":{"teams":1}}`),
		Mapping:     []byte(`{"status":{"Waiting on customer":"blocked"}}`),
	})
	if err != nil {
		t.Fatalf("SetImportJobPlan: %v", err)
	}
	if planned.Status != "awaiting_confirm" || len(planned.Plan) == 0 || len(planned.Mapping) == 0 {
		t.Fatalf("plan not parked: status=%s plan=%s mapping=%s", planned.Status, planned.Plan, planned.Mapping)
	}

	started, err := testHandler.Queries.StartImportJob(ctx, db.StartImportJobParams{ID: job.ID, WorkspaceID: ws})
	if err != nil {
		t.Fatalf("StartImportJob: %v", err)
	}
	if started.Status != "running" || !started.StartedAt.Valid {
		t.Fatalf("started job = %s started_at valid=%v", started.Status, started.StartedAt.Valid)
	}

	// The status guard is what makes the claim safe: a retry, or a second
	// server racing the same confirm, gets no row rather than a second run.
	if _, err := testHandler.Queries.StartImportJob(ctx, db.StartImportJobParams{ID: job.ID, WorkspaceID: ws}); err == nil {
		t.Error("StartImportJob claimed an already-running job twice")
	}

	if err := testHandler.Queries.UpdateImportJobProgress(ctx, db.UpdateImportJobProgressParams{
		ID: job.ID, WorkspaceID: ws, Totals: []byte(`{"issues":{"created":12}}`),
	}); err != nil {
		t.Fatalf("UpdateImportJobProgress: %v", err)
	}

	done, err := testHandler.Queries.FinishImportJob(ctx, db.FinishImportJobParams{
		ID:          job.ID,
		WorkspaceID: ws,
		Status:      "done",
		Totals:      []byte(`{"issues":{"created":240,"updated":0}}`),
		Failures:    []byte(`[{"kind":"attachment","identifier":"ENG-12","reason":"over per-file cap"}]`),
	})
	if err != nil {
		t.Fatalf("FinishImportJob: %v", err)
	}
	if done.Status != "done" || !done.FinishedAt.Valid {
		t.Fatalf("finished job = %s finished_at valid=%v", done.Status, done.FinishedAt.Valid)
	}

	// Finished means the workspace is free for the next import.
	if _, err := testHandler.Queries.GetActiveImportJob(ctx, ws); err == nil {
		t.Error("GetActiveImportJob still reports an active job after it finished")
	}
}

// Deleting the credential must not delete the receipt of what it imported.
func TestImportJobSurvivesConnectionDelete(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv(imports.SecretKeyEnv, importTestSealKey)
	ctx := context.Background()
	ws := importTestWorkspace(t)

	connID := newImportConnection(t, ws, "receipt-outlives-token")
	job, err := testHandler.Queries.CreateImportJob(ctx, db.CreateImportJobParams{
		WorkspaceID:  ws,
		ConnectionID: connID,
		Source:       "linear",
		Status:       "done",
		Scope:        []byte(`{}`),
		CreatedBy:    parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM import_job WHERE id = $1`, job.ID)
	})

	if _, err := testHandler.Queries.DeleteImportConnection(ctx, db.DeleteImportConnectionParams{
		ID: connID, WorkspaceID: ws,
	}); err != nil {
		t.Fatalf("DeleteImportConnection: %v", err)
	}
	after, err := testHandler.Queries.GetImportJob(ctx, db.GetImportJobParams{ID: job.ID, WorkspaceID: ws})
	if err != nil {
		t.Fatalf("GetImportJob after connection delete: %v", err)
	}
	if after.ConnectionID.Valid {
		t.Error("connection_id should be NULL after the credential was deleted")
	}
}

// createImportedIssue writes one issue the way the applier will: explicit
// source timestamps and an external_ref blob.
func createImportedIssue(t *testing.T, ws pgtype.UUID, externalID, identifier, importID string, createdAt time.Time) db.Issue {
	t.Helper()
	ctx := context.Background()

	number, err := testHandler.Queries.IncrementIssueCounter(ctx, ws)
	if err != nil {
		t.Fatalf("IncrementIssueCounter: %v", err)
	}
	ref, err := json.Marshal(map[string]any{
		"external_ref": map[string]string{
			"source":      "linear",
			"id":          externalID,
			"identifier":  identifier,
			"url":         "https://linear.app/acme/issue/" + identifier,
			"author":      "Dana Wu",
			"imported_at": createdAt.UTC().Format(time.RFC3339),
			"import_id":   importID,
		},
	})
	if err != nil {
		t.Fatalf("marshal external_ref: %v", err)
	}
	issue, err := testHandler.Queries.CreateIssueImported(ctx, db.CreateIssueImportedParams{
		WorkspaceID: ws,
		Title:       identifier + " imported",
		Status:      "todo",
		Priority:    "none",
		CreatorType: "member",
		CreatorID:   parseUUID(testUserID),
		Number:      number,
		Metadata:    ref,
		CreatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateIssueImported: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})
	return issue
}

// A two-year backlog that all says "created today" is a paste, not a
// migration: CreateIssueImported must write the source's own timestamps, and
// the external_ref blob must be findable so the next run is an update.
func TestImportedIssueKeepsSourceTimestampsAndIsFindable(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := importTestWorkspace(t)

	const externalID = "a1b2c3d4-imported-issue"
	const importID = "11111111-2222-3333-4444-555555555555"
	createdAt := time.Date(2024, 3, 7, 9, 30, 0, 0, time.UTC)

	issue := createImportedIssue(t, ws, externalID, "ENG-142", importID, createdAt)
	if got := issue.CreatedAt.Time.UTC(); !got.Equal(createdAt) {
		t.Errorf("created_at = %s, want the source's %s", got, createdAt)
	}

	found, err := testHandler.Queries.FindIssueByExternalRef(ctx, db.FindIssueByExternalRefParams{
		WorkspaceID: ws,
		Source:      "linear",
		ExternalID:  externalID,
	})
	if err != nil {
		t.Fatalf("FindIssueByExternalRef: %v", err)
	}
	if uuidToString(found.ID) != uuidToString(issue.ID) {
		t.Fatalf("FindIssueByExternalRef = %s, want %s", uuidToString(found.ID), uuidToString(issue.ID))
	}

	// A different source with the same id is a different issue, not a match.
	if _, err := testHandler.Queries.FindIssueByExternalRef(ctx, db.FindIssueByExternalRefParams{
		WorkspaceID: ws, Source: "jira", ExternalID: externalID,
	}); err == nil {
		t.Error("FindIssueByExternalRef matched across sources")
	}

	// The create/update split the dry run reports comes from one query, and the
	// receipt count comes from the rows rather than an in-memory counter.
	linked, err := testHandler.Queries.ListIssuesByExternalSource(ctx, db.ListIssuesByExternalSourceParams{
		WorkspaceID: ws, Source: "linear",
	})
	if err != nil {
		t.Fatalf("ListIssuesByExternalSource: %v", err)
	}
	var seen bool
	for _, row := range linked {
		if row.ExternalID.(string) == externalID {
			seen = true
		}
	}
	if !seen {
		t.Errorf("ListIssuesByExternalSource did not list the imported issue (%d rows)", len(linked))
	}

	count, err := testHandler.Queries.CountIssuesByImportJob(ctx, db.CountIssuesByImportJobParams{
		WorkspaceID: ws, ImportID: importID,
	})
	if err != nil {
		t.Fatalf("CountIssuesByImportJob: %v", err)
	}
	if count != 1 {
		t.Errorf("CountIssuesByImportJob = %d, want 1", count)
	}
}

// Re-import is an upsert, always: the same comment twice is one row with the
// newer body, which is what makes "created: 0, updated: N" true.
func TestImportedCommentUpsertsInsteadOfDuplicating(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := importTestWorkspace(t)

	issue := createImportedIssue(t, ws, "issue-for-comments", "ENG-143", "import-comments", time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	authored := time.Date(2024, 5, 2, 8, 15, 0, 0, time.UTC)

	params := db.UpsertCommentImportedParams{
		IssueID:        issue.ID,
		WorkspaceID:    ws,
		AuthorType:     "member",
		AuthorID:       parseUUID(testUserID),
		Content:        "original body",
		Type:           "comment",
		ExternalSource: pgtype.Text{String: "linear", Valid: true},
		ExternalID:     pgtype.Text{String: "comment-99", Valid: true},
		CreatedAt:      pgtype.Timestamptz{Time: authored, Valid: true},
		UpdatedAt:      pgtype.Timestamptz{Time: authored, Valid: true},
	}
	first, err := testHandler.Queries.UpsertCommentImported(ctx, params)
	if err != nil {
		t.Fatalf("UpsertCommentImported (first): %v", err)
	}
	if got := first.CreatedAt.Time.UTC(); !got.Equal(authored) {
		t.Errorf("comment created_at = %s, want the source's %s", got, authored)
	}

	params.Content = "edited in Linear after the first run"
	params.UpdatedAt = pgtype.Timestamptz{Time: authored.Add(48 * time.Hour), Valid: true}
	second, err := testHandler.Queries.UpsertCommentImported(ctx, params)
	if err != nil {
		t.Fatalf("UpsertCommentImported (re-run): %v", err)
	}
	if uuidToString(second.ID) != uuidToString(first.ID) {
		t.Fatalf("re-import created a second comment row (%s then %s)", uuidToString(first.ID), uuidToString(second.ID))
	}
	if second.Content != "edited in Linear after the first run" {
		t.Errorf("re-import did not update the body: %q", second.Content)
	}
	if got := second.CreatedAt.Time.UTC(); !got.Equal(authored) {
		t.Errorf("re-import moved created_at to %s; the original timestamp must stand", got)
	}

	var rows int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM comment WHERE issue_id = $1 AND external_source = 'linear' AND external_id = 'comment-99'`,
		issue.ID).Scan(&rows); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if rows != 1 {
		t.Fatalf("re-import left %d rows, want exactly 1", rows)
	}

	// The same external id under a different issue is a different comment —
	// the unique index is scoped to the issue, not global.
	other := createImportedIssue(t, ws, "issue-for-comments-2", "ENG-144", "import-comments", time.Date(2024, 5, 3, 12, 0, 0, 0, time.UTC))
	params.IssueID = other.ID
	if _, err := testHandler.Queries.UpsertCommentImported(ctx, params); err != nil {
		t.Fatalf("same external id on another issue must be allowed: %v", err)
	}

	// In-Agora comments stay outside the index entirely: NULL linkage never
	// collides with NULL linkage.
	plain := params
	plain.IssueID = issue.ID
	plain.ExternalSource = pgtype.Text{}
	plain.ExternalID = pgtype.Text{}
	for i := 0; i < 2; i++ {
		if _, err := testHandler.Queries.UpsertCommentImported(ctx, plain); err != nil {
			t.Fatalf("unlinked comment %d: %v", i, err)
		}
	}
}

// Half a linkage reads as "imported" while being un-dedupable; the CHECK makes
// it unwritable.
func TestCommentExternalRefMustBeComplete(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := importTestWorkspace(t)

	issue := createImportedIssue(t, ws, "issue-for-half-ref", "ENG-145", "import-half", time.Now().UTC())

	_, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, external_source)
		VALUES ($1, $2, 'member', $3, 'half a linkage', 'comment', 'linear')
	`, issue.ID, ws, parseUUID(testUserID))
	if err == nil {
		t.Fatal("a comment with external_source and no external_id was accepted")
	}
	if !strings.Contains(err.Error(), "comment_external_ref_complete") {
		t.Fatalf("rejected for the wrong reason: %v", err)
	}
}

// Relation vocabulary: 'duplicate' is a real relation every tracker has, so it
// is admitted; everything else a source invents ('similar', Jira's
// installation-defined link types) downgrades to 'related' in the adapter
// rather than widening this CHECK again.
func TestIssueDependencyAdmitsDuplicateOnly(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := importTestWorkspace(t)

	a := createImportedIssue(t, ws, "rel-a", "ENG-146", "import-rel", time.Now().UTC())
	b := createImportedIssue(t, ws, "rel-b", "ENG-147", "import-rel", time.Now().UTC())

	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type) VALUES ($1, $2, 'duplicate')`,
		a.ID, b.ID); err != nil {
		t.Fatalf("'duplicate' must be a valid relation type: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_dependency WHERE issue_id = $1`, a.ID)
	})

	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type) VALUES ($1, $2, 'similar')`,
		a.ID, b.ID); err == nil {
		t.Fatal("'similar' was accepted; sources' extra relation types must downgrade to 'related'")
	}
}
