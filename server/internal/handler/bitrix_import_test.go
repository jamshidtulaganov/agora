package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- in-memory Storage stub for attachment imports --------------------------

// memStorage is a tiny in-memory storage.Storage for tests that exercise the
// attachment-import path (storage.Upload + CreateAttachment). It records every
// uploaded object so a test can assert filenames / content types.
type memStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	uploads []memUpload
}

type memUpload struct {
	key         string
	filename    string
	contentType string
	size        int
}

func newMemStorage() *memStorage { return &memStorage{objects: map[string][]byte{}} }

func (m *memStorage) Upload(_ context.Context, key string, data []byte, contentType, filename string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objects[key] = cp
	m.uploads = append(m.uploads, memUpload{key: key, filename: filename, contentType: contentType, size: len(data)})
	return "mem://" + key, nil
}
func (m *memStorage) Delete(_ context.Context, _ string)       {}
func (m *memStorage) DeleteKeys(_ context.Context, _ []string) {}
func (m *memStorage) KeyFromURL(rawURL string) string          { return strings.TrimPrefix(rawURL, "mem://") }
func (m *memStorage) CdnDomain() string                        { return "" }
func (m *memStorage) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(bytes.NewReader(m.objects[key])), nil
}

func (m *memStorage) uploadCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.uploads)
}

// withMemStorage swaps a memStorage onto the test handler for the duration of
// the test, restoring whatever was there before.
func withMemStorage(t *testing.T) *memStorage {
	t.Helper()
	prev := testHandler.Storage
	mem := newMemStorage()
	testHandler.Storage = mem
	t.Cleanup(func() { testHandler.Storage = prev })
	return mem
}

// --- enriched Bitrix mock portal --------------------------------------------

// bitrixRichPortal extends the basic mock with the endpoints the v2 sync uses:
// task.commentitem.getlist, tasks.task.get (UF_TASK_WEBDAV_FILES),
// disk.attachedObject.get, sonet_group.get, tasks.task.list, and a file
// download endpoint. Tasks/comments/files/groups are registered per id.
type bitrixRichPortal struct {
	srv *httptest.Server

	mu       sync.Mutex
	tasks    map[string]string   // taskID -> result.task JSON
	comments map[string][]string // taskID -> []POST_MESSAGE
	files    map[string][]string // taskID -> []fileID
	fileMeta map[string]richFile // fileID -> meta
	groups   map[string]string   // groupID -> name
}

type richFile struct {
	name string
	body []byte
	ct   string
}

func newBitrixRichPortal(t *testing.T) *bitrixRichPortal {
	t.Helper()
	p := &bitrixRichPortal{
		tasks:    map[string]string{},
		comments: map[string][]string{},
		files:    map[string][]string{},
		fileMeta: map[string]richFile{},
		groups:   map[string]string{},
	}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *bitrixRichPortal) handle(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	// File download endpoint (DOWNLOAD_URL points here).
	if strings.HasPrefix(r.URL.Path, "/dl/") {
		fid := strings.TrimPrefix(r.URL.Path, "/dl/")
		f, ok := p.fileMeta[fid]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.ct != "" {
			w.Header().Set("Content-Type", f.ct)
		}
		w.Write(f.body)
		return
	}

	r.ParseForm()
	switch {
	case strings.HasSuffix(r.URL.Path, "/tasks.task.get"):
		id := r.PostForm.Get("taskId")
		// Files-select variant (GetTaskFiles): return UF_TASK_WEBDAV_FILES.
		if hasSelect(r.PostForm["select[]"], "UF_TASK_WEBDAV_FILES") {
			ids := p.files[id]
			arr, _ := json.Marshal(ids)
			io.WriteString(w, `{"result":{"task":{"id":"`+id+`","ufTaskWebdavFiles":`+string(arr)+`}}}`)
			return
		}
		body, ok := p.tasks[id]
		if !ok {
			io.WriteString(w, `{"error":"NOT_FOUND","error_description":"no task"}`)
			return
		}
		io.WriteString(w, `{"result":{"task":`+body+`}}`)

	case strings.HasSuffix(r.URL.Path, "/task.commentitem.getlist"):
		id := r.PostForm.Get("taskId")
		var b strings.Builder
		b.WriteString(`{"result":[`)
		for i, msg := range p.comments[id] {
			if i > 0 {
				b.WriteString(",")
			}
			mj, _ := json.Marshal(msg)
			b.WriteString(`{"ID":` + strconv.Itoa(i+1) + `,"POST_MESSAGE":` + string(mj) + `,"AUTHOR_NAME":"Tester","POST_DATE":"2024-01-01 00:00:00"}`)
		}
		b.WriteString(`]}`)
		io.WriteString(w, b.String())

	case strings.HasSuffix(r.URL.Path, "/disk.attachedObject.get"):
		fid := r.PostForm.Get("id")
		f, ok := p.fileMeta[fid]
		if !ok {
			io.WriteString(w, `{"error":"NOT_FOUND","error_description":"no file"}`)
			return
		}
		url := p.srv.URL + "/dl/" + fid
		io.WriteString(w, `{"result":{"ID":`+fid+`,"NAME":`+jsonStr(f.name)+`,"DOWNLOAD_URL":`+jsonStr(url)+`,"SIZE":`+strconv.Itoa(len(f.body))+`}}`)

	case strings.HasSuffix(r.URL.Path, "/sonet_group.get"):
		// FILTER[ID] => single group lookup; otherwise the full list.
		if gid := r.PostForm.Get("FILTER[ID]"); gid != "" {
			name, ok := p.groups[gid]
			if !ok {
				io.WriteString(w, `{"result":[]}`)
				return
			}
			io.WriteString(w, `{"result":[{"ID":`+gid+`,"NAME":`+jsonStr(name)+`}]}`)
			return
		}
		var b strings.Builder
		b.WriteString(`{"result":[`)
		first := true
		for gid, name := range p.groups {
			if !first {
				b.WriteString(",")
			}
			first = false
			b.WriteString(`{"ID":` + gid + `,"NAME":` + jsonStr(name) + `}`)
		}
		b.WriteString(`]}`)
		io.WriteString(w, b.String())

	case strings.HasSuffix(r.URL.Path, "/tasks.task.list"):
		gid := r.PostForm.Get("filter[GROUP_ID]")
		var b strings.Builder
		b.WriteString(`{"result":{"tasks":[`)
		first := true
		for _, body := range p.tasks {
			// crude membership: include the task if its body names this group.
			if gid != "" && !strings.Contains(body, `"groupId":`+gid) && !strings.Contains(body, `"groupId":"`+gid+`"`) {
				continue
			}
			if !first {
				b.WriteString(",")
			}
			first = false
			b.WriteString(body)
		}
		b.WriteString(`]}}`)
		io.WriteString(w, b.String())

	default:
		io.WriteString(w, `{"result":true}`)
	}
}

func hasSelect(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

// jsonStr quotes a string as a JSON literal (test helper).
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (p *bitrixRichPortal) setTask(id, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tasks[id] = body
}
func (p *bitrixRichPortal) setComments(id string, msgs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.comments[id] = msgs
}
func (p *bitrixRichPortal) setFile(taskID, fileID, name string, body []byte, ct string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[taskID] = append(p.files[taskID], fileID)
	p.fileMeta[fileID] = richFile{name: name, body: body, ct: ct}
}
func (p *bitrixRichPortal) setGroup(id, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.groups[id] = name
}

// configureBitrixEnvRich points the integration at the rich portal, routing all
// tasks to the handler-test workspace as the default, with NO tag filter (import
// everything).
func configureBitrixEnvRich(t *testing.T, portalURL string) {
	t.Helper()
	t.Setenv("BITRIX_WEBHOOK_URL", portalURL)
	t.Setenv("BITRIX_SYNC_WORKSPACE_SLUG", handlerTestWorkspaceSlug)
	t.Setenv("BITRIX_GROUP_MAP", "")
	t.Setenv("BITRIX_WORKSPACE_SLUGS", "")
	t.Setenv("BITRIX_INBOUND_SECRET", "")
	t.Setenv("BITRIX_PUSH_STATUS", "")
	t.Setenv("BITRIX_TASK_TAG", "")
	// The operator bulk-import endpoint (POST /api/bitrix/import, exercised by
	// TestImportBitrixTasks*) is OFF by default — members self-serve via
	// /api/bitrix/import/mine. These tests drive that gated operator path, so opt
	// into it explicitly; without this the handler 403s before any sync runs.
	t.Setenv("AGORA_BITRIX_BULK_IMPORT", "1")
}

// --- helpers to read synced issue id + related rows -------------------------

func issueIDByBitrixTaskID(t *testing.T, taskID string) (string, bool) {
	t.Helper()
	id, _, _, _, count := issueByBitrixTaskID(t, taskID)
	return id, count == 1
}

func commentCountForIssue(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1::uuid`, issueID).Scan(&n); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return n
}

func projectIDForIssue(t *testing.T, issueID string) (string, bool) {
	t.Helper()
	var pid string
	err := testPool.QueryRow(context.Background(),
		`SELECT COALESCE(project_id::text,'') FROM issue WHERE id = $1::uuid`, issueID).Scan(&pid)
	if err != nil {
		t.Fatalf("read project_id: %v", err)
	}
	return pid, pid != ""
}

func cleanupProject(t *testing.T, projectID string) {
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
}

// --- TestBitrixSyncImportsCommentsAndProject --------------------------------

// A first sync of a task that has a GROUP_ID and comments creates the issue,
// files it under a Bitrix-derived project, and mirrors the comments. A second
// sync does NOT duplicate comments or the project.
func TestBitrixSyncImportsCommentsAndProject(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)

	const taskID = "bx2-comments-1"
	const groupID = "777"
	cleanupBitrixIssues(t, taskID)

	portal.setGroup(groupID, "Sprint Alpha")
	portal.setTask(taskID, `{"id":"`+taskID+`","title":"Task with comments","status":3,"groupId":"`+groupID+`","tags":["ai"]}`)
	portal.setComments(taskID, "first comment from a human", "second human comment")

	cfg := bitrixRouteConfig()
	if err := testHandler.syncBitrixTask(context.Background(), taskID, cfg); err != nil {
		t.Fatalf("sync: %v", err)
	}

	issueID, ok := issueIDByBitrixTaskID(t, taskID)
	if !ok {
		t.Fatal("expected exactly one synced issue")
	}

	// Comments mirrored.
	if n := commentCountForIssue(t, issueID); n != 2 {
		t.Fatalf("comment count = %d, want 2", n)
	}

	// Issue filed under a project carrying the bitrix_group marker.
	pid, hasProject := projectIDForIssue(t, issueID)
	if !hasProject {
		t.Fatal("issue has no project_id; group→project filing failed")
	}
	cleanupProject(t, pid)

	var title, desc string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title, COALESCE(description,'') FROM project WHERE id = $1::uuid`, pid).Scan(&title, &desc); err != nil {
		t.Fatalf("read project: %v", err)
	}
	if title != "Sprint Alpha" {
		t.Errorf("project title = %q, want Sprint Alpha", title)
	}
	if !strings.Contains(desc, bitrixProjectMarkerPrefix+groupID) {
		t.Errorf("project description missing marker: %q", desc)
	}

	// Verify a comment carries the provenance header.
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM comment WHERE issue_id = $1::uuid ORDER BY created_at ASC LIMIT 1`, issueID).Scan(&content); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if !strings.Contains(content, "**Bitrix — Tester") || !strings.Contains(content, "first comment from a human") {
		t.Errorf("comment content missing header/body: %q", content)
	}

	// Re-sync: status unchanged, comments + project must NOT duplicate.
	if err := testHandler.syncBitrixTask(context.Background(), taskID, cfg); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if n := commentCountForIssue(t, issueID); n != 2 {
		t.Fatalf("after re-sync comment count = %d, want 2 (no dupes)", n)
	}
	if _, count := func() (string, int) {
		id, _, _, _, c := issueByBitrixTaskID(t, taskID)
		return id, c
	}(); count != 1 {
		t.Fatalf("after re-sync issue count = %d, want 1", count)
	}
	// Project dedup: still exactly one project carrying this group marker.
	var projCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM project WHERE workspace_id = $1::uuid AND description LIKE '%' || $2 || '%'`,
		testWorkspaceID, bitrixProjectMarkerPrefix+groupID).Scan(&projCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projCount != 1 {
		t.Fatalf("project count = %d, want 1 (dedup on re-sync)", projCount)
	}
}

// --- TestBitrixSyncImportsAttachments ---------------------------------------

// A task with an image attachment downloads + stores it as an issue attachment.
func TestBitrixSyncImportsAttachments(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)
	mem := withMemStorage(t)

	const taskID = "bx2-attach-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{"id":"`+taskID+`","title":"Task with file","status":3,"tags":["ai"]}`)
	// A small PNG-ish blob (content-type drives the attachment row).
	portal.setFile(taskID, "9001", "screenshot.png", []byte("\x89PNG\r\n\x1a\nfakefakefake"), "image/png")

	cfg := bitrixRouteConfig()
	if err := testHandler.syncBitrixTask(context.Background(), taskID, cfg); err != nil {
		t.Fatalf("sync: %v", err)
	}
	issueID, ok := issueIDByBitrixTaskID(t, taskID)
	if !ok {
		t.Fatal("expected one synced issue")
	}

	if mem.uploadCount() != 1 {
		t.Fatalf("storage upload count = %d, want 1", mem.uploadCount())
	}
	var attCount int
	var fname, ctype string
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM attachment WHERE issue_id = $1::uuid`, issueID).Scan(&attCount); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if attCount != 1 {
		t.Fatalf("attachment count = %d, want 1", attCount)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT filename, content_type FROM attachment WHERE issue_id = $1::uuid LIMIT 1`, issueID).Scan(&fname, &ctype); err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if fname != "screenshot.png" || ctype != "image/png" {
		t.Errorf("attachment = %q / %q", fname, ctype)
	}

	// Re-sync must not duplicate the attachment.
	if err := testHandler.syncBitrixTask(context.Background(), taskID, cfg); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM attachment WHERE issue_id = $1::uuid`, issueID).Scan(&attCount); err != nil {
		t.Fatalf("count attachments after re-sync: %v", err)
	}
	if attCount != 1 {
		t.Fatalf("after re-sync attachment count = %d, want 1 (no dupes)", attCount)
	}
}

// --- TestBitrixSyncImportsAllTasksNoTag -------------------------------------

// With BITRIX_TASK_TAG empty (default), a task WITHOUT the "ai" tag is still
// imported — the v2 behavior (drop the ai-only gate).
func TestBitrixSyncImportsAllTasksNoTag(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL) // BITRIX_TASK_TAG=""

	const taskID = "bx2-notag-1"
	cleanupBitrixIssues(t, taskID)

	// No "ai" tag, no tags at all.
	portal.setTask(taskID, `{"id":"`+taskID+`","title":"Untagged but imported","status":2}`)

	cfg := bitrixRouteConfig()
	if err := testHandler.syncBitrixTask(context.Background(), taskID, cfg); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, ok := issueIDByBitrixTaskID(t, taskID); !ok {
		t.Fatal("untagged task should be imported when BITRIX_TASK_TAG is empty")
	}
}

// --- TestBitrixSyncTagFilterSkips -------------------------------------------

// With BITRIX_TASK_TAG set, a task lacking that tag is skipped.
func TestBitrixSyncTagFilterSkips(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)
	t.Setenv("BITRIX_TASK_TAG", "ai")

	const keep = "bx2-tag-keep"
	const drop = "bx2-tag-drop"
	cleanupBitrixIssues(t, keep)
	cleanupBitrixIssues(t, drop)

	portal.setTask(keep, `{"id":"`+keep+`","title":"Has ai tag","status":2,"tags":["ai"]}`)
	portal.setTask(drop, `{"id":"`+drop+`","title":"No ai tag","status":2,"tags":["internal"]}`)

	cfg := bitrixRouteConfig()
	if err := testHandler.syncBitrixTask(context.Background(), keep, cfg); err != nil {
		t.Fatalf("sync keep: %v", err)
	}
	if err := testHandler.syncBitrixTask(context.Background(), drop, cfg); err != nil {
		t.Fatalf("sync drop: %v", err)
	}
	if _, ok := issueIDByBitrixTaskID(t, keep); !ok {
		t.Error("tagged task should be imported")
	}
	if _, ok := issueIDByBitrixTaskID(t, drop); ok {
		t.Error("untagged task should be skipped when BITRIX_TASK_TAG=ai")
	}
}

// --- async import helpers ---------------------------------------------------

// waitForBitrixIssue polls for the issue synced from a Bitrix task id. POST
// /api/bitrix/import is asynchronous: it returns 202 after resolving the task-id
// set, then the per-task sync runs in a detached background goroutine that
// streams issues onto the board. Bounded ~3s so a missing issue still fails.
func waitForBitrixIssue(t *testing.T, taskID string) (string, bool) {
	t.Helper()
	for i := 0; i < 60; i++ {
		if id, ok := issueIDByBitrixTaskID(t, taskID); ok {
			return id, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", false
}

// waitForBitrixImportDone blocks until the background goroutine started by the
// most recent ImportBitrixTasks call has synced every task — the progress
// tracker flips Running=false with Synced>=Total. Used to observe post-run state
// (dedup on a re-import) that isn't visible until the async sync finishes. The
// Synced>=Total guard is load-bearing: a prior run's trailing Finish() can race
// Running to false against a fresh Start(), but Synced only reaches Total once
// this run's per-task syncs have all returned. Bounded ~6s.
func waitForBitrixImportDone(t *testing.T) {
	t.Helper()
	for i := 0; i < 120; i++ {
		bitrixImportProgressState.Lock()
		done := !bitrixImportProgressState.Running &&
			bitrixImportProgressState.Synced >= bitrixImportProgressState.Total
		bitrixImportProgressState.Unlock()
		if done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("bitrix import did not finish within deadline")
}

// --- TestImportBitrixTasksEndpoint ------------------------------------------

// POST /api/bitrix/import with explicit task_ids returns 202 Accepted (the sync
// runs in the background) and creates the issues. A second import of the same
// ids takes the update path and must NOT duplicate rows.
func TestImportBitrixTasksEndpoint(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)

	const t1 = "bx2-imp-1"
	const t2 = "bx2-imp-2"
	cleanupBitrixIssues(t, t1)
	cleanupBitrixIssues(t, t2)
	portal.setTask(t1, `{"id":"`+t1+`","title":"Import one","status":2}`)
	portal.setTask(t2, `{"id":"`+t2+`","title":"Import two","status":3}`)

	body, _ := json.Marshal(BitrixImportRequest{TaskIDs: []string{t1, t2}})
	req := newRequest("POST", "/api/bitrix/import", nil)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	testHandler.ImportBitrixTasks(w, req)
	// Async contract: 202 Accepted once the task-id set is resolved; Created stays
	// 0 (the per-task sync streams in over the websocket), Accepted tallies what
	// was enqueued.
	if w.Code != http.StatusAccepted {
		t.Fatalf("import status = %d, want 202, body=%s", w.Code, w.Body.String())
	}
	var resp BitrixImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v (%s)", err, w.Body.String())
	}
	if resp.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2 (errors=%v)", resp.Accepted, resp.Errors)
	}
	// Poll until the background sync has filed both tasks.
	if _, ok := waitForBitrixIssue(t, t1); !ok {
		t.Error("t1 not created")
	}
	if _, ok := waitForBitrixIssue(t, t2); !ok {
		t.Error("t2 not created")
	}

	// Second import: both issues already exist, so the background sync takes the
	// update path. The 202 body carries no update tally, so wait for the run to
	// finish, then assert dedup held — still exactly one issue per task.
	body2, _ := json.Marshal(BitrixImportRequest{TaskIDs: []string{t1, t2}})
	req2 := newRequest("POST", "/api/bitrix/import", nil)
	req2.Body = io.NopCloser(bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	testHandler.ImportBitrixTasks(w2, req2)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("second import status = %d, want 202, body=%s", w2.Code, w2.Body.String())
	}
	waitForBitrixImportDone(t)
	for _, id := range []string{t1, t2} {
		if _, _, _, _, count := issueByBitrixTaskID(t, id); count != 1 {
			t.Fatalf("after re-import, task %s issue count = %d, want 1 (no dupes)", id, count)
		}
	}
}

// TestImportBitrixTasksByGroup: POST /api/bitrix/import with group_ids expands
// each group into its tasks (via tasks.task.list), returns 202, and the
// background sync imports them.
func TestImportBitrixTasksByGroup(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)

	const groupID = "991"
	const a = "bx2-grp-a"
	const b = "bx2-grp-b"
	cleanupBitrixIssues(t, a)
	cleanupBitrixIssues(t, b)
	portal.setGroup(groupID, "Group Import")
	portal.setTask(a, `{"id":"`+a+`","title":"Group task A","status":2,"groupId":"`+groupID+`"}`)
	portal.setTask(b, `{"id":"`+b+`","title":"Group task B","status":3,"groupId":"`+groupID+`"}`)

	body, _ := json.Marshal(BitrixImportRequest{GroupIDs: []string{groupID}})
	req := newRequest("POST", "/api/bitrix/import", nil)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	testHandler.ImportBitrixTasks(w, req)
	// Group expansion happens synchronously, so Accepted reflects the group's task
	// count; the 202 returns before the background sync creates the issues.
	if w.Code != http.StatusAccepted {
		t.Fatalf("import status = %d, want 202, body=%s", w.Code, w.Body.String())
	}
	var resp BitrixImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v (%s)", err, w.Body.String())
	}
	if resp.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2 (errors=%v)", resp.Accepted, resp.Errors)
	}
	// Poll until the background sync has filed both group tasks.
	for _, id := range []string{a, b} {
		if _, ok := waitForBitrixIssue(t, id); !ok {
			t.Errorf("group task %s not imported", id)
		}
	}
	// Both tasks filed under the same group project — clean it up.
	if pid, ok := projectIDForIssueByTask(t, a); ok {
		cleanupProject(t, pid)
	}
}

// --- TestListBitrixTasksEndpoint --------------------------------------------

// GET /api/bitrix/tasks?group_id=... lists a group's tasks with already_synced.
func TestListBitrixTasksEndpoint(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)

	const groupID = "880"
	const synced = "bx2-list-synced"
	const fresh = "bx2-list-fresh"
	cleanupBitrixIssues(t, synced)
	cleanupBitrixIssues(t, fresh)
	portal.setGroup(groupID, "List Group")
	portal.setTask(synced, `{"id":"`+synced+`","title":"Already here","status":2,"groupId":"`+groupID+`"}`)
	portal.setTask(fresh, `{"id":"`+fresh+`","title":"Not yet","status":2,"groupId":"`+groupID+`"}`)

	// Pre-sync one of them so already_synced=true for it.
	cfg := bitrixRouteConfig()
	if err := testHandler.syncBitrixTask(context.Background(), synced, cfg); err != nil {
		t.Fatalf("pre-sync: %v", err)
	}
	if pid, ok := projectIDForIssueByTask(t, synced); ok {
		cleanupProject(t, pid)
	}

	req := newRequest("GET", "/api/bitrix/tasks?group_id="+groupID, nil)
	w := httptest.NewRecorder()
	testHandler.ListBitrixTasks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", w.Code, w.Body.String())
	}
	var tasks []BitrixTaskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	byID := map[string]BitrixTaskResponse{}
	for _, tk := range tasks {
		byID[tk.ID] = tk
	}
	if got, ok := byID[synced]; !ok || !got.AlreadySynced {
		t.Errorf("synced task already_synced = %v (present=%v)", got.AlreadySynced, ok)
	}
	if got, ok := byID[fresh]; !ok || got.AlreadySynced {
		t.Errorf("fresh task already_synced = %v (present=%v), want false", got.AlreadySynced, ok)
	}
	if got := byID[synced]; got.WorkspaceSlug != handlerTestWorkspaceSlug {
		t.Errorf("workspace_slug = %q, want %q", got.WorkspaceSlug, handlerTestWorkspaceSlug)
	}
}

func projectIDForIssueByTask(t *testing.T, taskID string) (string, bool) {
	t.Helper()
	id, ok := issueIDByBitrixTaskID(t, taskID)
	if !ok {
		return "", false
	}
	return projectIDForIssue(t, id)
}

// --- TestBitrixEndpointsDisabled --------------------------------------------

// When BITRIX_WEBHOOK_URL is unset, the import-browser endpoints 503.
func TestBitrixEndpointsDisabled(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	// The endpoints now authorize the caller (requireBitrixOperator) BEFORE the
	// "is Bitrix configured" check, so route to the fixture workspace — which
	// testUserID owns — to get PAST the operator gate. With the webhook URL empty
	// the enabled-check is then what fails, yielding the 503 this test asserts.
	t.Setenv("BITRIX_SYNC_WORKSPACE_SLUG", handlerTestWorkspaceSlug)
	t.Setenv("BITRIX_WEBHOOK_URL", "")

	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{"groups", testHandler.ListBitrixGroups, newRequest("GET", "/api/bitrix/groups", nil)},
		{"tasks", testHandler.ListBitrixTasks, newRequest("GET", "/api/bitrix/tasks?group_id=1", nil)},
	} {
		w := httptest.NewRecorder()
		tc.fn(w, tc.req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", tc.name, w.Code)
		}
	}
}

func TestBitrixAttachmentBlock(t *testing.T) {
	if got := bitrixAttachmentBlock(nil); got != "" {
		t.Errorf("empty embeds = %q, want empty", got)
	}

	block := bitrixAttachmentBlock([]bitrixEmbed{
		{url: "/uploads/a.png", name: "shot [1].png", contentType: "image/png"},
		{url: "/uploads/v.mp4", name: "rec.mp4", contentType: "video/mp4"},
		{url: "/uploads/d.txt", name: "notes.txt", contentType: "text/plain"},
	})

	if !strings.Contains(block, "**Attachments (from Bitrix):**") {
		t.Errorf("missing header:\n%s", block)
	}
	// Image embeds inline; the bracket in the name is sanitized so it can't break
	// the markdown alt text.
	if !strings.Contains(block, "![shot (1).png](/uploads/a.png)") {
		t.Errorf("image embed wrong:\n%s", block)
	}
	// Video + other files render as labelled links, not inline images.
	if !strings.Contains(block, "🎬 [rec.mp4](/uploads/v.mp4)") {
		t.Errorf("video link wrong:\n%s", block)
	}
	if !strings.Contains(block, "📎 [notes.txt](/uploads/d.txt)") {
		t.Errorf("file link wrong:\n%s", block)
	}
	if strings.Contains(block, "![rec.mp4]") {
		t.Errorf("video must not embed as an inline image:\n%s", block)
	}
}
