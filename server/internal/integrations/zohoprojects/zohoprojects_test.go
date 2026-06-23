package zohoprojects

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// zohoMock is a fake Zoho Projects REST + OAuth host. It serves the read
// endpoints the importer uses plus the token grant, and records any
// custom_status push so the outbound test can assert on it. Both the API host
// and the accounts (token) host point at this one server.
type zohoMock struct {
	srv           *httptest.Server
	statusUpdates map[string]string // taskID -> custom_status pushed
	lastTaskQuery string            // raw query of the most recent ListTasks GET
}

func newZohoMock(t *testing.T) *zohoMock {
	t.Helper()
	m := &zohoMock{statusUpdates: map[string]string{}}
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
			io.WriteString(w, `{"comments":[{"id":9,"id_string":"9","content":"hello","added_person":"Jam","created_time":"06-01-2026"}]}`)
		case strings.HasSuffix(path, "/tasklists/"):
			io.WriteString(w, `{"tasklists":[{"id":501,"id_string":"501","name":"Sprint 7"}]}`)
		case strings.HasSuffix(path, "/subtasks/"):
			io.WriteString(w, `{"tasks":[{"id":888,"id_string":"888","name":"child","subtasks":false,
				"status":{"name":"Closed","type":"closed"},
				"details":{"owners":[{"zpuid":901,"name":"Sub","email":"sub@x.io"}]}}]}`)
		case strings.Contains(path, "/tasks/") && r.Method == http.MethodPost:
			// Task update: POST .../tasks/<id>/ with a custom_status form param.
			r.ParseForm()
			parts := strings.Split(strings.Trim(path, "/"), "/")
			id := parts[len(parts)-1]
			m.statusUpdates[id] = r.PostForm.Get("custom_status")
			io.WriteString(w, `{"tasks":[]}`)
		case strings.HasSuffix(path, "/tasks/"):
			m.lastTaskQuery = r.URL.RawQuery
			io.WriteString(w, `{"tasks":[{
				"id":777,"id_string":"777","name":"Do thing",
				"status":{"name":"In Progress","type":"open"},
				"details":{"owners":[{"zpuid":900,"name":"Jam","email":"jam@x.io"}]},
				"tasklist":{"id":501,"id_string":"501","name":"Sprint 7"},
				"subtasks":true,"last_updated_time_long":"1717200000000"}]}`)
		case strings.HasSuffix(path, "/projects/"):
			io.WriteString(w, `{"projects":[{"id":111,"id_string":"111","name":"RnD","status":"active"}]}`)
		default:
			io.WriteString(w, `{"result":true}`)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *zohoMock) client() *Client {
	return NewClient(Config{
		ClientID:     "cid",
		ClientSecret: "secret",
		RefreshToken: "refresh",
		PortalID:     "12345",
		AccountsHost: m.srv.URL,
		APIHost:      m.srv.URL,
	})
}

// --- mapping (pure) ---------------------------------------------------------

func TestMapStatus(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Open", StatusTodo},
		{"To Do", StatusTodo},
		{"New", StatusTodo},
		{"In Progress", StatusInProgress},
		{"WIP", StatusInProgress},
		{"In Review", StatusInReview},
		{"QA", StatusInReview},
		{"Closed", StatusDone},
		{"Completed", StatusDone},
		{"Deferred", StatusBacklog},
		{"On Hold", StatusBacklog},
		{"Cancelled", StatusCancelled},
		{"Rejected", StatusCancelled},
		{"", StatusTodo},               // empty -> default
		{"Something Weird", StatusTodo}, // unknown -> default
	}
	for _, c := range cases {
		if got := MapStatus(c.name); got != c.want {
			t.Errorf("MapStatus(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMapStatusWithTypeFallback(t *testing.T) {
	// Unrecognized name falls back to the Zoho status "type".
	if got := MapStatusWithType("Custom Bucket", "closed"); got != StatusDone {
		t.Errorf("type=closed fallback = %q, want done", got)
	}
	if got := MapStatusWithType("Custom Bucket", "open"); got != StatusTodo {
		t.Errorf("type=open fallback = %q, want todo", got)
	}
}

func TestZohoStatusNameFromIssue(t *testing.T) {
	cases := map[string]string{
		StatusBacklog:    "Deferred",
		StatusTodo:       "Open",
		StatusInProgress: "In Progress",
		StatusInReview:   "In Review",
		StatusDone:       "Closed",
		StatusCancelled:  "Cancelled",
		"bogus":          "Open",
	}
	for in, want := range cases {
		if got := ZohoStatusNameFromIssue(in); got != want {
			t.Errorf("ZohoStatusNameFromIssue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveCustomStatusID(t *testing.T) {
	statuses := []CustomStatus{
		{ID: "1", Name: "Open", Type: "open"},
		{ID: "2", Name: "In Progress", Type: "open"},
		{ID: "3", Name: "Closed", Type: "closed"},
	}
	cases := []struct {
		target string
		wantID string
		wantOK bool
	}{
		{StatusTodo, "1", true},
		{StatusInProgress, "2", true},
		{StatusDone, "3", true},
		{StatusBacklog, "", false},   // no backlog-named status
		{StatusCancelled, "", false}, // no cancel-named status
		{"", "", false},
	}
	for _, c := range cases {
		id, ok := ResolveCustomStatusID(statuses, c.target)
		if ok != c.wantOK || id != c.wantID {
			t.Errorf("ResolveCustomStatusID(%q) = (%q,%v), want (%q,%v)", c.target, id, ok, c.wantID, c.wantOK)
		}
	}
}

func TestResolveCustomStatusIDTypeFallback(t *testing.T) {
	// A portal whose only terminal status has an unrecognized NAME still resolves
	// "done" via the closed type.
	statuses := []CustomStatus{{ID: "9", Name: "Shipped It", Type: "closed"}}
	id, ok := ResolveCustomStatusID(statuses, StatusDone)
	if !ok || id != "9" {
		t.Errorf("done type-fallback = (%q,%v), want (9,true)", id, ok)
	}
}

func TestTasklistIsSprint(t *testing.T) {
	yes := []string{"Sprint 7", "sprint", "Спринт 12", "  SPRINT  "}
	no := []string{"", "Backlog", "Tasklist 1", "Bugs"}
	for _, n := range yes {
		if !TasklistIsSprint(n) {
			t.Errorf("TasklistIsSprint(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if TasklistIsSprint(n) {
			t.Errorf("TasklistIsSprint(%q) = true, want false", n)
		}
	}
}

func TestTaskIsSprint(t *testing.T) {
	// Real Octane parent-task names that denote a sprint container.
	yes := []string{"Sales Mytrion [Sprint 3]", "Browser Automation [SPRINT]",
		"ZepToMail implementation sprint", "Analyze Debtor List [ Collection sprint]", "Спринт 2"}
	no := []string{"Fix the Weekly Progress", "WEX Lead Converter", "", "Bug fixing"}
	for _, n := range yes {
		if !TaskIsSprint(n) {
			t.Errorf("TaskIsSprint(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if TaskIsSprint(n) {
			t.Errorf("TaskIsSprint(%q) = true, want false", n)
		}
	}
}

// --- client (httptest) ------------------------------------------------------

func TestClientListProjectsAndTasks(t *testing.T) {
	m := newZohoMock(t)
	c := m.client()
	ctx := context.Background()

	projects, err := c.ListProjects(ctx, "12345")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "111" || projects[0].Name != "RnD" {
		t.Fatalf("ListProjects = %+v", projects)
	}

	tasks, err := c.ListTasks(ctx, "12345", "111", nil, "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("ListTasks count = %d, want 1", len(tasks))
	}
	tk := tasks[0]
	if tk.ID != "777" || tk.Status != "In Progress" || tk.Owner.Email != "jam@x.io" {
		t.Fatalf("task = %+v", tk)
	}
	if tk.TasklistID != "501" || !TasklistIsSprint(tk.TasklistName) {
		t.Fatalf("task tasklist = %q/%q", tk.TasklistID, tk.TasklistName)
	}
	if tk.LastUpdatedUnix != 1717200000000 {
		t.Errorf("LastUpdatedUnix = %d, want 1717200000000", tk.LastUpdatedUnix)
	}
	if !tk.HasSubtasks {
		t.Error("HasSubtasks = false, want true (task carries subtasks:true)")
	}
}

func TestClientListSubtasks(t *testing.T) {
	m := newZohoMock(t)
	c := m.client()
	subs, err := c.ListSubtasks(context.Background(), "12345", "111", "777")
	if err != nil {
		t.Fatalf("ListSubtasks: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("subtasks = %d, want 1", len(subs))
	}
	if subs[0].ID != "888" || subs[0].Owner.Email != "sub@x.io" || subs[0].HasSubtasks {
		t.Errorf("subtask = %+v", subs[0])
	}
	if MapStatusWithType(subs[0].Status, subs[0].StatusType) != StatusDone {
		t.Errorf("subtask status mapping = %q, want done", subs[0].Status)
	}
}

func TestClientListTasksModifiedSince(t *testing.T) {
	m := newZohoMock(t)
	c := m.client()
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.ListTasks(context.Background(), "12345", "111", &since, ""); err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	// The modified-since filter must be passed to Zoho as MM-DD-YYYY.
	if !strings.Contains(m.lastTaskQuery, "last_modified_time=06-01-2026") {
		t.Errorf("ListTasks query = %q, want last_modified_time=06-01-2026", m.lastTaskQuery)
	}
}

func TestClientListTasksOwnerFilter(t *testing.T) {
	m := newZohoMock(t)
	c := m.client()
	if _, err := c.ListTasks(context.Background(), "12345", "111", nil, "910595107"); err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	// The owner filter must be passed to Zoho as the "owner" query param.
	if !strings.Contains(m.lastTaskQuery, "owner=910595107") {
		t.Errorf("ListTasks query = %q, want owner=910595107", m.lastTaskQuery)
	}
}

func TestClientListTaskCustomStatuses(t *testing.T) {
	m := newZohoMock(t)
	c := m.client()
	statuses, err := c.ListTaskCustomStatuses(context.Background(), "12345", "111")
	if err != nil {
		t.Fatalf("ListTaskCustomStatuses: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("custom statuses = %d, want 3", len(statuses))
	}
	if statuses[0].Name != "Open" || statuses[2].Type != "closed" {
		t.Fatalf("custom statuses = %+v", statuses)
	}
}

func TestClientUpdateTaskStatusPOSTShape(t *testing.T) {
	m := newZohoMock(t)
	c := m.client()
	if err := c.UpdateTaskStatus(context.Background(), "12345", "111", "777", "3"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if got := m.statusUpdates["777"]; got != "3" {
		t.Errorf("pushed custom_status = %q, want 3", got)
	}
}

func TestIsThrottle(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errString("zohoprojects: http 400: URL_ROLLING_THROTTLES_LIMIT_EXCEEDED"), true},
		{errString("zohoprojects: http 429: too many requests"), true},
		{errString("zohoprojects: http 500"), false},
		{errString("zohoprojects: decode tasks: unexpected end of JSON input"), false},
	}
	for _, c := range cases {
		if got := IsThrottle(c.err); got != c.want {
			t.Errorf("IsThrottle(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestClientListTasksEmpty204(t *testing.T) {
	// A zero-task project returns 204 (empty body); ListTasks must return an empty
	// slice, not a decode error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/oauth/v2/token") {
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewClient(Config{ClientID: "c", ClientSecret: "s", RefreshToken: "r",
		AccountsHost: srv.URL, APIHost: srv.URL})
	tasks, err := c.ListTasks(context.Background(), "12345", "111", nil, "")
	if err != nil {
		t.Fatalf("ListTasks on 204 should not error, got: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("tasks = %d, want 0", len(tasks))
	}
}

func TestClientGetTaskCommentsEmpty204(t *testing.T) {
	// Zoho returns 204 No Content (empty body) for a task with no comments. The
	// client must treat that as zero comments, not a decode error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/oauth/v2/token") {
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
			return
		}
		w.WriteHeader(http.StatusNoContent) // 204, empty body
	}))
	defer srv.Close()
	c := NewClient(Config{ClientID: "c", ClientSecret: "s", RefreshToken: "r",
		AccountsHost: srv.URL, APIHost: srv.URL})
	comments, err := c.GetTaskComments(context.Background(), "12345", "111", "777")
	if err != nil {
		t.Fatalf("GetTaskComments on 204 should not error, got: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("comments = %d, want 0", len(comments))
	}
}

func TestClientUpdateTaskStatusValidatesArgs(t *testing.T) {
	c := newZohoMock(t).client()
	ctx := context.Background()
	if err := c.UpdateTaskStatus(ctx, "12345", "111", "777", ""); err == nil {
		t.Error("empty custom status id should error")
	}
	if err := c.UpdateTaskStatus(ctx, "12345", "", "777", "3"); err == nil {
		t.Error("empty project id should error")
	}
}
