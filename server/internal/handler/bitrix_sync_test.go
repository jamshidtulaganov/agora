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
)

// bitrixMockPortal is a fake Bitrix REST portal. It serves tasks.task.get from
// an in-memory table keyed by task id and records any AddTaskComment /
// UpdateTaskStatus calls for assertion.
type bitrixMockPortal struct {
	srv      *httptest.Server
	tasks    map[string]string // taskID -> JSON for result.task
	comments []string
	statuses []string
}

func newBitrixMockPortal(t *testing.T) *bitrixMockPortal {
	t.Helper()
	p := &bitrixMockPortal{tasks: map[string]string{}}
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

// TestBitrixWebhookUpdatesInPlace: a second event (status 5) updates the same
// issue to done without creating a duplicate.
func TestBitrixWebhookUpdatesInPlace(t *testing.T) {
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

	// Flip the task to completed (5 -> done) and re-fire.
	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Evolving task","status":"5","tags":["ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKUPDATE", taskID); w.Code != http.StatusOK {
		t.Fatalf("second webhook status = %d", w.Code)
	}

	_, status2, _, _, count2 := issueByBitrixTaskID(t, taskID)
	if count2 != 1 {
		t.Fatalf("after update: count = %d, want 1 (no duplicate)", count2)
	}
	if status2 != "done" {
		t.Errorf("after update: status = %q, want done", status2)
	}
}

// TestBitrixWebhookIgnoresUntaggedTask: a task without the "ai" tag creates no
// issue.
func TestBitrixWebhookIgnoresUntaggedTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

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
		t.Fatalf("issue count = %d, want 0 for untagged task", count)
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
