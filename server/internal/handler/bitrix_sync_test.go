package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

// bitrixMockPortal is a fake Bitrix REST portal. It serves tasks.task.get from
// an in-memory table keyed by task id and records any AddTaskComment /
// UpdateTaskStatus calls for assertion.
type bitrixMockPortal struct {
	srv      *httptest.Server
	tasks    map[string]string // taskID -> JSON for result.task
	users    map[string]string // userID -> JSON for one user.get result row
	comments []string
	statuses []string
}

func newBitrixMockPortal(t *testing.T) *bitrixMockPortal {
	t.Helper()
	p := &bitrixMockPortal{tasks: map[string]string{}, users: map[string]string{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/tasks.task.get"):
			r.ParseForm()
			id := r.PostForm.Get("taskId")
			body, ok := p.tasks[id]
			if !ok {
				io.WriteString(w, `{"error":"NOT_FOUND","error_description":"no task"}`)
				return
			}
			io.WriteString(w, `{"result":{"task":`+body+`}}`)
		case strings.HasSuffix(r.URL.Path, "/user.get"):
			r.ParseForm()
			body, ok := p.users[r.PostForm.Get("ID")]
			if !ok {
				io.WriteString(w, `{"result":[]}`)
				return
			}
			io.WriteString(w, `{"result":[`+body+`]}`)
		case strings.HasSuffix(r.URL.Path, "/task.commentitem.add"):
			r.ParseForm()
			p.comments = append(p.comments, r.PostForm.Get("FIELDS[POST_MESSAGE]"))
			io.WriteString(w, `{"result":1}`)
		case strings.HasSuffix(r.URL.Path, "/tasks.task.update.json"):
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				Fields map[string]string `json:"fields"`
			}
			json.Unmarshal(raw, &body)
			p.statuses = append(p.statuses, body.Fields["STATUS"])
			io.WriteString(w, `{"result":true}`)
		default:
			io.WriteString(w, `{"result":true}`)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// setTask registers a task body (the JSON that goes inside result.task).
func (p *bitrixMockPortal) setTask(id, body string) { p.tasks[id] = body }

func (p *bitrixMockPortal) setUser(id, body string) { p.users[id] = body }

// postBitrixWebhook fires the inbound webhook handler with a form-encoded
// ONTASK* event for the given task id and returns the recorder.
func postBitrixWebhook(t *testing.T, event, taskID string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("event", event)
	form.Set("data[FIELDS_AFTER][ID]", taskID)
	req := httptest.NewRequest(http.MethodPost, "/bitrix/webhook", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	testHandler.BitrixWebhook(w, req)
	return w
}

// issueByBitrixTaskID looks up the synced issue directly from the DB so tests
// can assert on status / assignee / count without going through the handler.
func issueByBitrixTaskID(t *testing.T, taskID string) (id, status, assigneeType, assigneeID string, count int) {
	t.Helper()
	filter, _ := json.Marshal(map[string]string{bitrixTaskIDMetaKey: taskID})
	rows, err := testPool.Query(context.Background(),
		`SELECT id::text, status, COALESCE(assignee_type,''), COALESCE(assignee_id::text,'')
		   FROM issue
		  WHERE workspace_id = $1::uuid AND metadata @> $2::jsonb
		  ORDER BY created_at ASC`,
		testWorkspaceID, string(filter))
	if err != nil {
		t.Fatalf("query issues: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		count++
		if count == 1 {
			if err := rows.Scan(&id, &status, &assigneeType, &assigneeID); err != nil {
				t.Fatalf("scan issue: %v", err)
			}
		}
	}
	return id, status, assigneeType, assigneeID, count
}

func cleanupBitrixIssues(t *testing.T, taskID string) {
	t.Helper()
	t.Cleanup(func() {
		filter, _ := json.Marshal(map[string]string{bitrixTaskIDMetaKey: taskID})
		testPool.Exec(context.Background(),
			`DELETE FROM issue WHERE workspace_id = $1::uuid AND metadata @> $2::jsonb`,
			testWorkspaceID, string(filter))
	})
}

// configureBitrixEnv points the integration at the mock portal and routes all
// AI tasks to the handler-test workspace as the default.
func configureBitrixEnv(t *testing.T, portalURL string) {
	t.Helper()
	t.Setenv("BITRIX_WEBHOOK_URL", portalURL)
	t.Setenv("BITRIX_SYNC_WORKSPACE_SLUG", handlerTestWorkspaceSlug)
	t.Setenv("BITRIX_GROUP_MAP", "")
	t.Setenv("BITRIX_WORKSPACE_SLUGS", "")
	t.Setenv("BITRIX_INBOUND_SECRET", "")
	t.Setenv("BITRIX_PUSH_STATUS", "")
}

// TestBitrixWebhookCreatesAndAssignsIssue: an ONTASKUPDATE with status 3 on an
// AI-tagged task whose RESPONSIBLE_ID is linked to a workspace member creates
// an in_progress issue assigned to that member.
func TestBitrixWebhookCreatesAndAssignsIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-create-1"
	const bitrixUser = "bx-user-100"
	cleanupBitrixIssues(t, taskID)

	// Link the Bitrix responsible to the fixture owner (a member of the ws).
	if err := testHandler.linkExternalIdentity(context.Background(), providerBitrix, bitrixUser, testUserID); err != nil {
		t.Fatalf("link identity: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
			providerBitrix, bitrixUser)
	})

	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Sync from Bitrix","description":"please do",
		"status":3,"responsibleId":"`+bitrixUser+`","tags":["ai"]
	}`)

	w := postBitrixWebhook(t, "ONTASKUPDATE", taskID)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", w.Code)
	}

	id, status, assigneeType, assigneeID, count := issueByBitrixTaskID(t, taskID)
	if count != 1 {
		t.Fatalf("issue count = %d, want 1", count)
	}
	if id == "" {
		t.Fatal("issue id empty")
	}
	if status != "in_progress" {
		t.Errorf("status = %q, want in_progress", status)
	}
	if assigneeType != "member" {
		t.Errorf("assignee_type = %q, want member", assigneeType)
	}
	if assigneeID != testUserID {
		t.Errorf("assignee_id = %q, want %q", assigneeID, testUserID)
	}
}

// TestBitrixWebhookRemovesClosedInPlace: a second event (status 5) deletes the
// previously synced issue — personal mirrors do not keep Bitrix history.
func TestBitrixWebhookRemovesClosedInPlace(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-update-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Evolving task","status":3,"tags":["ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKADD", taskID); w.Code != http.StatusOK {
		t.Fatalf("first webhook status = %d", w.Code)
	}

	_, status, _, _, count := issueByBitrixTaskID(t, taskID)
	if count != 1 || status != "in_progress" {
		t.Fatalf("after create: count=%d status=%q", count, status)
	}

	// Flip the task to completed (5) and re-fire — mirror issue must disappear.
	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Evolving task","status":"5","tags":["ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKUPDATE", taskID); w.Code != http.StatusOK {
		t.Fatalf("second webhook status = %d", w.Code)
	}

	_, _, _, _, count2 := issueByBitrixTaskID(t, taskID)
	if count2 != 0 {
		t.Fatalf("after closed sync: count = %d, want 0 (removed)", count2)
	}
}

// TestBitrixWebhookSkipsCompletedOnCreate: STATUS=5 must not create an issue.
func TestBitrixWebhookSkipsCompletedOnCreate(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-closed-create-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Already done","status":"5","tags":["ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKADD", taskID); w.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", w.Code)
	}
	_, _, _, _, count := issueByBitrixTaskID(t, taskID)
	if count != 0 {
		t.Fatalf("closed task created %d issue(s), want 0", count)
	}
}

// TestBitrixWebhookSkipsTaskMissingConfiguredTag: with BITRIX_TASK_TAG set, a
// task lacking that tag creates no issue. (v2 behavior: the ai-only gate is gone
// — the tag filter is OPTIONAL and only skips when configured AND absent. The
// "import everything by default" path is covered by
// TestBitrixSyncImportsAllTasksNoTag in bitrix_import_test.go.)
func TestBitrixWebhookSkipsTaskMissingConfiguredTag(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)
	t.Setenv("BITRIX_TASK_TAG", "ai")

	const taskID = "bx-untagged-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Internal task","status":3,"tags":["internal","urgent"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKUPDATE", taskID); w.Code != http.StatusOK {
		t.Fatalf("webhook status = %d", w.Code)
	}

	_, _, _, _, count := issueByBitrixTaskID(t, taskID)
	if count != 0 {
		t.Fatalf("issue count = %d, want 0 for task missing configured tag", count)
	}
}

// TestMirrorIssueStatusToBitrix exercises the outbound core: a Bitrix-linked
// issue posts a courtesy comment, and pushes status when BITRIX_PUSH_STATUS is
// on. A non-linked issue is a no-op.
func TestMirrorIssueStatusToBitrix(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)
	t.Setenv("BITRIX_PUSH_STATUS", "true")

	const taskID = "bx-mirror-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{"id":"`+taskID+`","title":"Mirror me","status":3,"tags":["ai"]}`)
	if w := postBitrixWebhook(t, "ONTASKADD", taskID); w.Code != http.StatusOK {
		t.Fatalf("seed webhook status = %d", w.Code)
	}
	id, _, _, _, count := issueByBitrixTaskID(t, taskID)
	if count != 1 {
		t.Fatalf("seed issue count = %d", count)
	}

	if err := testHandler.mirrorIssueStatusToBitrix(context.Background(), id); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if len(portal.comments) == 0 {
		t.Fatal("expected a courtesy comment to Bitrix")
	}
	// Spec format: "🤖 Agora: issue <PREFIX>-<n> → <Label>". The
	// handler-test workspace prefix is "HAN" (see handler_test.go fixture).
	lastComment := portal.comments[len(portal.comments)-1]
	if !strings.Contains(lastComment, "🤖 Agora: issue ") {
		t.Errorf("comment missing emoji/prefix preamble: %q", lastComment)
	}
	if !strings.Contains(lastComment, "HAN-") {
		t.Errorf("comment missing workspace-prefixed identifier: %q", lastComment)
	}
	if !strings.Contains(lastComment, " → ") {
		t.Errorf("comment missing Unicode arrow: %q", lastComment)
	}
	if strings.Contains(lastComment, "->") || strings.Contains(lastComment, "#") {
		t.Errorf("comment still uses old ASCII format: %q", lastComment)
	}
	if len(portal.statuses) == 0 {
		t.Fatal("expected a status push when BITRIX_PUSH_STATUS=true")
	}

	// A non-Bitrix issue is a no-op (no panic, no comment growth).
	before := len(portal.comments)
	createReq := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Plain issue not from bitrix",
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
	if err := testHandler.mirrorIssueStatusToBitrix(context.Background(), created.ID); err != nil {
		t.Fatalf("mirror non-bitrix issue should be no-op, got: %v", err)
	}
	if len(portal.comments) != before {
		t.Errorf("non-bitrix issue produced a comment; comments grew %d -> %d", before, len(portal.comments))
	}
}

// TestBitrixTaskIDFromMetadata covers the numeric-id rendering: large/round
// JSON numbers (decoded as float64) must render as plain integers, not
// scientific notation ("9e+11"). Regression for the float64 %v mangling.
func TestBitrixTaskIDFromMetadata(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"large integral float", `{"bitrix_task_id": 900000000000}`, "900000000000"},
		{"round million", `{"bitrix_task_id": 1000000}`, "1000000"},
		{"small int", `{"bitrix_task_id": 42}`, "42"},
		{"string value", `{"bitrix_task_id": "T123"}`, "T123"},
		{"absent key", `{"other": 1}`, ""},
		{"empty metadata", ``, ""},
		{"non-integral float", `{"bitrix_task_id": 12.5}`, "12.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bitrixTaskIDFromMetadata([]byte(tc.raw))
			if got != tc.want {
				t.Errorf("bitrixTaskIDFromMetadata(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestBitrixShouldMirror documents the outbound status_changed gate: proceed
// only when status_changed is present AND true. Absent and present-and-false
// both skip. Regression for the "missing key proceeds" bug.
func TestBitrixShouldMirror(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    bool
	}{
		{"present true", map[string]any{"status_changed": true}, true},
		{"present false", map[string]any{"status_changed": false}, false},
		{"absent key", map[string]any{"issue": map[string]any{"id": "x"}}, false},
		{"non-bool value", map[string]any{"status_changed": "true"}, false},
		{"non-map payload", "not a map", false},
		{"nil payload", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitrixShouldMirror(tc.payload); got != tc.want {
				t.Errorf("bitrixShouldMirror(%#v) = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

// TestBitrixWebhookReassignsOnUpdate: an ONTASKUPDATE that changes
// RESPONSIBLE_ID re-resolves the assignee on the existing issue (RAW, no
// outbound echo) without creating a duplicate. Regression for "inbound update
// doesn't re-sync assignee".
func TestBitrixWebhookReassignsOnUpdate(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-reassign-1"
	const bitrixUser = "bx-user-reassign"
	cleanupBitrixIssues(t, taskID)

	// Link the Bitrix responsible to the fixture owner (a member of the ws).
	if err := testHandler.linkExternalIdentity(context.Background(), providerBitrix, bitrixUser, testUserID); err != nil {
		t.Fatalf("link identity: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
			providerBitrix, bitrixUser)
	})

	// First sync: unassigned (responsible not linked to any member).
	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Reassign me","status":3,"responsibleId":"unlinked-99","tags":["ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKADD", taskID); w.Code != http.StatusOK {
		t.Fatalf("first webhook status = %d", w.Code)
	}
	_, _, assigneeType, _, count := issueByBitrixTaskID(t, taskID)
	if count != 1 {
		t.Fatalf("after create: count = %d, want 1", count)
	}
	if assigneeType != "" {
		t.Fatalf("after create: assignee_type = %q, want unassigned", assigneeType)
	}

	// Update: reassign RESPONSIBLE_ID to the linked member and re-fire.
	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Reassign me","status":3,"responsibleId":"`+bitrixUser+`","tags":["ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKUPDATE", taskID); w.Code != http.StatusOK {
		t.Fatalf("second webhook status = %d", w.Code)
	}

	_, _, assigneeType2, assigneeID2, count2 := issueByBitrixTaskID(t, taskID)
	if count2 != 1 {
		t.Fatalf("after reassign: count = %d, want 1 (no duplicate)", count2)
	}
	if assigneeType2 != "member" {
		t.Errorf("after reassign: assignee_type = %q, want member", assigneeType2)
	}
	if assigneeID2 != testUserID {
		t.Errorf("after reassign: assignee_id = %q, want %q", assigneeID2, testUserID)
	}
}

func TestBitrixWebhookStoresResponsibleAndCreatorMetadata(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-people-metadata-1"
	cleanupBitrixIssues(t, taskID)
	portal.setUser("525", `{"ID":"525","NAME":"Jamshid","LAST_NAME":"Tulaganov","EMAIL":"j.tulaganov@salesdoc.io"}`)
	portal.setUser("777", `{"ID":"777","NAME":"Shaxzod","LAST_NAME":"Tadjiyev","EMAIL":"s.tajiyev@salesdoc.io"}`)
	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Track both people","status":3,
		"responsibleId":"525","createdBy":"777","tags":["ai"]
	}`)

	if w := postBitrixWebhook(t, "ONTASKADD", taskID); w.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, body = %s", w.Code, w.Body.String())
	}
	filter, _ := json.Marshal(map[string]string{bitrixTaskIDMetaKey: taskID})
	var raw []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT metadata FROM issue
		WHERE workspace_id = $1::uuid AND metadata @> $2::jsonb
	`, testWorkspaceID, string(filter)).Scan(&raw); err != nil {
		t.Fatalf("query issue metadata: %v", err)
	}
	metadata := parseIssueMetadata(raw)
	for key, want := range map[string]string{
		"bitrix_responsible_email": "j.tulaganov@salesdoc.io",
		"bitrix_responsible_name":  "Jamshid Tulaganov",
		"bitrix_created_by_email":  "s.tajiyev@salesdoc.io",
		"bitrix_created_by_name":   "Shaxzod Tadjiyev",
	} {
		if got, _ := metadata[key].(string); got != want {
			t.Errorf("metadata[%q] = %q, want %q", key, got, want)
		}
	}
}

// TestBitrixSyncSerializedNoDuplicate documents the advisory-lock dedup: two
// sequential syncs of the same task (simulating ONTASKADD + ONTASKUPDATE)
// produce exactly one issue. The per-task pg_advisory_lock serializes the
// find/create/stamp sequence so the second sync sees the stamped metadata and
// takes the update path instead of double-creating.
//
// NOTE: a true concurrency race is non-deterministic against a shared test DB
// (and racing sibling agents); this asserts the sequential invariant the lock
// guarantees and exercises the lock acquire/release path.
func TestBitrixSyncSerializedNoDuplicate(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-serialize-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{"id":"`+taskID+`","title":"Serialize me","status":3,"tags":["ai"]}`)

	cfg := bitrixRouteConfig()
	ctx := context.Background()
	// Two back-to-back syncs of the same task. The advisory lock + dedup must
	// collapse these onto a single issue.
	if err := testHandler.syncBitrixTask(ctx, taskID, cfg); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := testHandler.syncBitrixTask(ctx, taskID, cfg); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	_, _, _, _, count := issueByBitrixTaskID(t, taskID)
	if count != 1 {
		t.Fatalf("issue count = %d, want exactly 1 (advisory lock should dedup)", count)
	}
}

// TestBitrixAdvisoryLockRoundTrip documents that the transaction-scoped advisory
// lock the sync uses is freed when its lock-tx commits: a second tx can acquire
// the same key with a non-blocking pg_try_advisory_xact_lock only AFTER the
// first tx released it. This proves the lock/release pattern in syncBitrixTask
// doesn't leak the lock across pooled connections.
func TestBitrixAdvisoryLockRoundTrip(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	const key = "test-ws:bitrix:lock-roundtrip"

	// Tx 1 takes the xact lock.
	tx1, err := testHandler.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	if _, err := tx1.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		tx1.Rollback(ctx)
		t.Fatalf("tx1 lock: %v", err)
	}

	// A concurrent tx must NOT be able to grab the same key while tx1 holds it.
	tx2, err := testHandler.TxStarter.Begin(ctx)
	if err != nil {
		tx1.Rollback(ctx)
		t.Fatalf("begin tx2: %v", err)
	}
	var got bool
	if err := tx2.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, key).Scan(&got); err != nil {
		tx1.Rollback(ctx)
		tx2.Rollback(ctx)
		t.Fatalf("tx2 try-lock: %v", err)
	}
	if got {
		tx1.Rollback(ctx)
		tx2.Rollback(ctx)
		t.Fatal("tx2 acquired the lock while tx1 held it; lock is not exclusive")
	}
	tx2.Rollback(ctx)

	// Release tx1's lock by committing, then a fresh tx must be able to grab it.
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}
	tx3, err := testHandler.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	defer tx3.Rollback(ctx)
	var got3 bool
	if err := tx3.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, key).Scan(&got3); err != nil {
		t.Fatalf("tx3 try-lock: %v", err)
	}
	if !got3 {
		t.Fatal("tx3 could not acquire the lock after tx1 released it; lock leaked")
	}
}

// TestBitrixStageIDForIssueStatus covers the outbound stage picker against the
// real shape of an SD sprint kanban, where FOUR columns (Code Review, Ready for
// testing, Тестинг, Need Merge) all mean in_review and the review columns carry
// higher ids than Сделаны because they were added to the board later.
//
// The no-move cases are the important ones: a first-match-wins picker moved a
// task sitting in Тестинг back to Code Review on every unrelated status event,
// writing bogus stage churn into the portal.
func TestBitrixStageIDForIssueStatus(t *testing.T) {
	// Portal (SORT) order, which is deliberately NOT id order.
	stages := []bitrix.Stage{
		{ID: "2531", Title: "Новые", Sort: 100},
		{ID: "2533", Title: "Выполняются", Sort: 200},
		{ID: "2883", Title: "Returned", Sort: 250},
		{ID: "2879", Title: "Code Review", Sort: 300},
		{ID: "2881", Title: "Ready for testing", Sort: 400},
		{ID: "2901", Title: "Тестинг", Sort: 500},
		{ID: "2877", Title: "Need Merge", Sort: 600},
		{ID: "2903", Title: "Ready for release", Sort: 700},
		{ID: "2535", Title: "Сделаны", Sort: 800},
	}

	cases := []struct {
		name     string
		status   string
		current  string
		stageMap map[string]string
		want     string
	}{
		{"already in a column meaning in_review stays put", "in_review", "2901", nil, ""},
		{"need merge also means in_review, no churn", "in_review", "2877", nil, ""},
		{"entering review lands on the EARLIEST review column", "in_review", "2533", nil, "2879"},
		{"done moves to Сделаны", "done", "2533", nil, "2535"},
		{"in_progress from Новые", "in_progress", "2531", nil, "2533"},
		// "Returned" means TODO (re-queued), not in_progress: a task the reviewer
		// sent back is un-owned until someone restarts it. So a task Agora moved
		// to in_progress while parked in Returned DOES move to the dev column.
		{"returned means todo, so in_progress moves to the dev column", "in_progress", "2883", nil, "2533"},
		{"returned already means todo — no churn", "todo", "2883", nil, ""},
		{"no blocked column on this board — no move", "blocked", "2531", nil, ""},
		{"unknown status has no column", "backlog", "2531", nil, ""},
		{
			name:     "workspace override retargets a column",
			status:   "in_progress",
			current:  "2901",
			stageMap: map[string]string{"тестинг": "in_progress"},
			want:     "", // the override makes the CURRENT column mean in_progress
		},
		{
			name:     "workspace override frees the task to move for review",
			status:   "in_review",
			current:  "2901",
			stageMap: map[string]string{"тестинг": "in_progress"},
			want:     "2879",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitrixStageIDForIssueStatus(tc.status, stages, tc.current, tc.stageMap); got != tc.want {
				t.Errorf("bitrixStageIDForIssueStatus(%q, current=%q) = %q, want %q",
					tc.status, tc.current, got, tc.want)
			}
		})
	}
}

// TestResolveBitrixIssueStatusReportsStageDecision pins the second return value:
// callers use it to stamp bitrix_stage_mapped and to log the unmapped label. The
// "Ready for release" and "Готов к релизу" rows are the mismatches this whole
// change exists to fix — they used to fall through to STATUS.
func TestResolveBitrixIssueStatusReportsStageDecision(t *testing.T) {
	cases := []struct {
		stage        string
		bitrixStatus string
		stageMap     map[string]string
		wantStatus   string
		wantDecided  bool
	}{
		{"Code Review", "3", nil, "in_review", true},
		{"Ready for release", "3", nil, "in_review", true},
		{"Готов к релизу", "2", nil, "in_review", true},
		{"Blockers", "3", nil, "blocked", true},
		{"To Do", "3", nil, "todo", true},
		{"К выполнению", "3", nil, "todo", true},
		// Group 173 uses stages as categories: no keyword, so STATUS decides and
		// the caller flags it.
		{"EPIC", "3", nil, "in_progress", false},
		{"EPIC", "2", map[string]string{"epic": "backlog"}, "backlog", true},
		// Off-kanban task: no stage at all.
		{"", "4", nil, "in_review", false},
	}
	for _, tc := range cases {
		gotStatus, gotDecided := resolveBitrixIssueStatus(tc.stage, tc.bitrixStatus, tc.stageMap)
		if gotStatus != tc.wantStatus || gotDecided != tc.wantDecided {
			t.Errorf("resolveBitrixIssueStatus(%q, %q) = (%q, %v), want (%q, %v)",
				tc.stage, tc.bitrixStatus, gotStatus, gotDecided, tc.wantStatus, tc.wantDecided)
		}
	}
}

// TestBitrixTaskIsClosed guards a DESTRUCTIVE gate: a "closed" task is never
// imported, and an already-mirrored one is hard-deleted together with its S3
// attachments. The rows with bitrix_status=5 and an active column are real
// observations from one prod sync cycle, which dropped 22 tasks that way —
// including a Bitrix task whose video the human was reading at that moment
// (issue 4b1dd153, task 36251, STATUS=5, stage "Ready for release").
func TestBitrixTaskIsClosed(t *testing.T) {
	cases := []struct {
		name         string
		mappedStatus string
		stageDecided bool
		bitrixStatus string
		want         bool
	}{
		// The column says the work is finished — closing is correct.
		{"stage Сделаны", "done", true, "5", true},
		{"stage Готово with open STATUS", "done", true, "2", true},
		{"stage Отмененные", "cancelled", true, "3", true},
		// The column says the work is LIVE while STATUS claims completed. These
		// are the wrong drops: the column wins, the mirror survives.
		{"Ready for release with STATUS=5", "in_review", true, "5", false},
		{"Need Merge with STATUS=5", "in_review", true, "5", false},
		{"Новые with STATUS=5", "todo", true, "5", false},
		{"To Do with STATUS=5", "todo", true, "5", false},
		{"Выполняются with STATUS=5", "in_progress", true, "5", false},
		{"Returned with STATUS=5", "in_progress", true, "5", false},
		{"Blockers with STATUS=5", "blocked", true, "5", false},
		{"declined STATUS under a live column", "in_progress", true, "7", false},
		// No column signal: STATUS is all there is, so it decides.
		{"off-kanban completed", "done", false, "5", true},
		{"off-kanban declined", "todo", false, "7", true},
		{"off-kanban open", "todo", false, "2", false},
		{"unmapped column, completed STATUS", "in_review", false, "5", true},
		{"unmapped column, open STATUS", "in_progress", false, "3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitrixTaskIsClosed(tc.mappedStatus, tc.stageDecided, tc.bitrixStatus); got != tc.want {
				t.Errorf("bitrixTaskIsClosed(%q, stageDecided=%v, %q) = %v, want %v",
					tc.mappedStatus, tc.stageDecided, tc.bitrixStatus, got, tc.want)
			}
		})
	}
}

// TestBitrixStageIDForIssueStatusReturnColumn pins the RETURN preference: a task
// leaving a review/testing column for todo lands on the board's "Returned"
// column, not on the earliest todo column. That is what the reviewer sending work
// back actually looks like on the kanban — Новые would read as "never started".
func TestBitrixStageIDForIssueStatusReturnColumn(t *testing.T) {
	stages := []bitrix.Stage{
		{ID: "2531", Title: "Новые", Sort: 100},
		{ID: "2533", Title: "Выполняются", Sort: 200},
		{ID: "2883", Title: "Returned", Sort: 250},
		{ID: "2879", Title: "Code Review", Sort: 300},
		{ID: "2901", Title: "Тестинг", Sort: 500},
		{ID: "2535", Title: "Сделаны", Sort: 800},
	}
	cases := []struct {
		name    string
		status  string
		current string
		want    string
	}{
		{"review:fail from Code Review lands on Returned", "todo", "2879", "2883"},
		{"QA fail from Testing lands on Returned too", "todo", "2901", "2883"},
		{"re-queue from the dev column keeps the earliest todo column", "todo", "2533", "2531"},
		{"already parked in Returned — no churn", "todo", "2883", ""},
		{"in_review still lands on the earliest review column", "in_review", "2533", "2879"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitrixStageIDForIssueStatus(tc.status, stages, tc.current, nil); got != tc.want {
				t.Errorf("bitrixStageIDForIssueStatus(%q, current=%q) = %q, want %q",
					tc.status, tc.current, got, tc.want)
			}
		})
	}
}

// TestBitrixStageIDForIssueStatusNoReturnColumn: a board WITHOUT a return column
// must keep working — the preference degrades to the earliest todo column instead
// of returning "" and stranding the task in the review column.
func TestBitrixStageIDForIssueStatusNoReturnColumn(t *testing.T) {
	stages := []bitrix.Stage{
		{ID: "1", Title: "To Do", Sort: 100},
		{ID: "2", Title: "Doing", Sort: 200},
		{ID: "3", Title: "Code Review", Sort: 300},
	}
	if got := bitrixStageIDForIssueStatus("todo", stages, "3", nil); got != "1" {
		t.Errorf("without a Returned column, todo from review = %q, want %q", got, "1")
	}
}
