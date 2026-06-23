package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

// zohoHandlerMock is a fake Zoho Projects REST + OAuth host for the handler-level
// import/outbound tests. It serves one project (111) with one task (777) whose
// owner email + status are configurable per-test, plus the token grant and the
// custom-status list, and records any outbound custom_status push.
type zohoHandlerMock struct {
	srv           *httptest.Server
	statusUpdates map[string]string // taskID -> custom_status pushed
	ownerEmail    string
	statusName    string
	statusType    string
}

func newZohoHandlerMock(t *testing.T) *zohoHandlerMock {
	t.Helper()
	m := &zohoHandlerMock{
		statusUpdates: map[string]string{},
		ownerEmail:    handlerTestEmail,
		statusName:    "In Progress",
		statusType:    "open",
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/oauth/v2/token"):
			io.WriteString(w, `{"access_token":"tok-123","expires_in":3600,"token_type":"Zoho-oauthtoken"}`)
		case strings.HasSuffix(path, "/portals/"):
			io.WriteString(w, `{"portals":[{"id":12345,"id_string":"12345","name":"octane"}]}`)
		case strings.HasSuffix(path, "/customstatus/"):
			io.WriteString(w, `{"status":[
				{"id":1,"name":"Open","type":"open"},
				{"id":2,"name":"In Progress","type":"open"},
				{"id":3,"name":"Closed","type":"closed"}]}`)
		case strings.HasSuffix(path, "/comments/"):
			io.WriteString(w, `{"comments":[{"id":9,"id_string":"9","content":"a zoho comment","added_person":"Jam","created_time":"06-01-2026"}]}`)
		case strings.HasSuffix(path, "/tasklists/"):
			io.WriteString(w, `{"tasklists":[{"id":501,"id_string":"501","name":"Sprint 7"}]}`)
		case strings.Contains(path, "/tasks/") && r.Method == http.MethodPost:
			r.ParseForm()
			parts := strings.Split(strings.Trim(path, "/"), "/")
			m.statusUpdates[parts[len(parts)-1]] = r.PostForm.Get("custom_status")
			io.WriteString(w, `{"tasks":[]}`)
		case strings.HasSuffix(path, "/tasks/"):
			io.WriteString(w, fmt.Sprintf(`{"tasks":[{
				"id":777,"id_string":"777","name":"Do thing",
				"status":{"name":%q,"type":%q},
				"details":{"owners":[{"zpuid":900,"name":"Jam","email":%q}]},
				"tasklist":{"id":501,"id_string":"501","name":"Sprint 7"}}]}`,
				m.statusName, m.statusType, m.ownerEmail))
		case strings.HasSuffix(path, "/projects/"):
			io.WriteString(w, `{"projects":[{"id":111,"id_string":"111","name":"RnD","status":"active"}]}`)
		default:
			io.WriteString(w, `{"result":true}`)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// configureZohoEnv points the integration at the mock host and sets the portal.
func configureZohoEnv(t *testing.T, mockURL string) {
	t.Helper()
	t.Setenv("ZOHO_PROJECTS_CLIENT_ID", "cid")
	t.Setenv("ZOHO_PROJECTS_CLIENT_SECRET", "secret")
	t.Setenv("ZOHO_PROJECTS_REFRESH_TOKEN", "refresh")
	t.Setenv("ZOHO_PROJECTS_PORTAL", "12345")
	t.Setenv("ZOHO_PROJECTS_ACCOUNTS_HOST", mockURL)
	t.Setenv("ZOHO_PROJECTS_API_HOST", mockURL)
	t.Setenv("ZOHO_PROJECTS_PUSH_STATUS", "")
}

// issueByZohoTaskID looks up the synced issue directly from the DB so tests can
// assert on status / assignee / project-id metadata / count without the handler.
func issueByZohoTaskID(t *testing.T, taskID string) (id, status, assigneeType, assigneeID, zohoProjectID string, count int) {
	t.Helper()
	filter := fmt.Sprintf(`{"zoho_task_id":%q}`, taskID)
	rows, err := testPool.Query(context.Background(),
		`SELECT id::text, status, COALESCE(assignee_type,''), COALESCE(assignee_id::text,''),
		        COALESCE(metadata->>'zoho_project_id','')
		   FROM issue
		  WHERE workspace_id = $1::uuid AND metadata @> $2::jsonb
		  ORDER BY created_at ASC`,
		testWorkspaceID, filter)
	if err != nil {
		t.Fatalf("query issues: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		count++
		if count == 1 {
			if err := rows.Scan(&id, &status, &assigneeType, &assigneeID, &zohoProjectID); err != nil {
				t.Fatalf("scan issue: %v", err)
			}
		}
	}
	return id, status, assigneeType, assigneeID, zohoProjectID, count
}

// cleanupZohoFixtures removes the issue/sprint/project this test created, keyed
// by the durable Zoho markers, scoped to the handler-test workspace.
func cleanupZohoFixtures(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx,
			`DELETE FROM issue WHERE workspace_id = $1::uuid AND metadata @> '{"zoho_task_id":"777"}'::jsonb`,
			testWorkspaceID)
		testPool.Exec(ctx,
			`DELETE FROM sprint WHERE workspace_id = $1::uuid AND goal LIKE '%zoho_tasklist:%'`,
			testWorkspaceID)
		testPool.Exec(ctx,
			`DELETE FROM project WHERE workspace_id = $1::uuid AND description LIKE '%zoho_project:%'`,
			testWorkspaceID)
	})
}

// TestZohoImportCreatesIssue: a full import of the mock project creates one
// in_progress issue, assigned to the workspace owner (email match), linked to a
// sprint (the "Sprint 7" task list), with the task's comment mirrored, the
// originating Zoho project id stamped, and the per-project cursor persisted.
func TestZohoImportCreatesIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newZohoHandlerMock(t)
	configureZohoEnv(t, mock.srv.URL)
	cleanupZohoFixtures(t)

	wsUUID, err := util.ParseUUID(testWorkspaceID)
	if err != nil {
		t.Fatalf("parse ws: %v", err)
	}
	st := testHandler.newZohoSyncState()
	if err := testHandler.syncZohoProject(context.Background(), wsUUID, "111", st); err != nil {
		t.Fatalf("syncZohoProject: %v", err)
	}

	id, status, assigneeType, assigneeID, zohoProjectID, count := issueByZohoTaskID(t, "777")
	if count != 1 {
		t.Fatalf("issue count = %d, want 1", count)
	}
	if id == "" {
		t.Fatal("issue id empty")
	}
	if status != "in_progress" {
		t.Errorf("status = %q, want in_progress", status)
	}
	if assigneeType != "member" || assigneeID != testUserID {
		t.Errorf("assignee = (%q,%q), want (member,%q)", assigneeType, assigneeID, testUserID)
	}
	if zohoProjectID != "111" {
		t.Errorf("zoho_project_id metadata = %q, want 111", zohoProjectID)
	}

	// Sprint link: the issue must be in a sprint whose goal carries the tasklist marker.
	var sprintCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_to_sprint isp
		   JOIN sprint s ON s.id = isp.sprint_id
		  WHERE isp.issue_id = $1::uuid AND s.goal LIKE '%zoho_tasklist:501%'`,
		id).Scan(&sprintCount); err != nil {
		t.Fatalf("sprint link query: %v", err)
	}
	if sprintCount != 1 {
		t.Errorf("sprint link count = %d, want 1", sprintCount)
	}

	// Comment mirrored.
	var commentCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1::uuid AND content LIKE '%a zoho comment%'`,
		id).Scan(&commentCount); err != nil {
		t.Fatalf("comment query: %v", err)
	}
	if commentCount != 1 {
		t.Errorf("comment count = %d, want 1", commentCount)
	}

	// Cursor persisted on the Agora project settings.
	var cursor string
	if err := testPool.QueryRow(context.Background(),
		`SELECT COALESCE(settings->>'zoho_synced_at','')
		   FROM project
		  WHERE workspace_id = $1::uuid AND description LIKE '%zoho_project:111%'`,
		testWorkspaceID).Scan(&cursor); err != nil {
		t.Fatalf("cursor query: %v", err)
	}
	if strings.TrimSpace(cursor) == "" {
		t.Error("expected settings.zoho_synced_at cursor to be persisted")
	}
}

// TestZohoImportUpdatesInPlace: a second sync of the same task updates the
// existing issue (status flip to done) without creating a duplicate.
func TestZohoImportUpdatesInPlace(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newZohoHandlerMock(t)
	configureZohoEnv(t, mock.srv.URL)
	cleanupZohoFixtures(t)

	wsUUID, _ := util.ParseUUID(testWorkspaceID)
	ctx := context.Background()

	st := testHandler.newZohoSyncState()
	if err := testHandler.syncZohoProject(ctx, wsUUID, "111", st); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	_, status, _, _, _, count := issueByZohoTaskID(t, "777")
	if count != 1 || status != "in_progress" {
		t.Fatalf("after create: count=%d status=%q", count, status)
	}

	// Flip the task to a closed status and re-sync.
	mock.statusName = "Closed"
	mock.statusType = "closed"
	st2 := testHandler.newZohoSyncState()
	if err := testHandler.syncZohoProject(ctx, wsUUID, "111", st2); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	_, status2, _, _, _, count2 := issueByZohoTaskID(t, "777")
	if count2 != 1 {
		t.Fatalf("after update: count = %d, want 1 (no duplicate)", count2)
	}
	if status2 != "done" {
		t.Errorf("after update: status = %q, want done", status2)
	}
}

// TestMirrorIssueStatusToZoho exercises the outbound core: a Zoho-linked issue
// whose Agora status is "done" pushes the project's "Closed" custom-status id to
// the Zoho task. A non-Zoho issue is a no-op. Gated on ZOHO_PROJECTS_PUSH_STATUS.
func TestMirrorIssueStatusToZoho(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newZohoHandlerMock(t)
	configureZohoEnv(t, mock.srv.URL)
	t.Setenv("ZOHO_PROJECTS_PUSH_STATUS", "true")
	cleanupZohoFixtures(t)

	wsUUID, _ := util.ParseUUID(testWorkspaceID)
	ctx := context.Background()

	// Seed a Zoho-linked issue via the import path.
	st := testHandler.newZohoSyncState()
	if err := testHandler.syncZohoProject(ctx, wsUUID, "111", st); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	id, _, _, _, _, count := issueByZohoTaskID(t, "777")
	if count != 1 {
		t.Fatalf("seed issue count = %d", count)
	}

	// Move the issue to done (raw, mirrors a genuine Agora-side change) then mirror.
	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET status = 'done' WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("set done: %v", err)
	}
	if err := testHandler.mirrorIssueStatusToZoho(ctx, id); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if got := mock.statusUpdates["777"]; got != "3" {
		t.Errorf("pushed custom_status = %q, want 3 (Closed)", got)
	}

	// A non-Zoho issue is a no-op (no panic, no extra push).
	before := len(mock.statusUpdates)
	createReq := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Plain issue not from zoho",
	})
	cw := httptest.NewRecorder()
	testHandler.CreateIssue(cw, createReq)
	var created IssueResponse
	if err := json.Unmarshal(cw.Body.Bytes(), &created); err != nil {
		t.Fatalf("create plain issue: %v (%s)", err, cw.Body.String())
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, created.ID)
	})
	if err := testHandler.mirrorIssueStatusToZoho(ctx, created.ID); err != nil {
		t.Fatalf("mirror non-zoho issue should be no-op, got: %v", err)
	}
	if len(mock.statusUpdates) != before {
		t.Errorf("non-zoho issue produced a push; map grew %d -> %d", before, len(mock.statusUpdates))
	}
}
