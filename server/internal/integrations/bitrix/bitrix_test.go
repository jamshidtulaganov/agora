package bitrix

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- flexible decoder -------------------------------------------------------

func TestJsonStrFlexibleEncoding(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"string", `{"v":"123"}`, "123"},
		{"number", `{"v":123}`, "123"},
		{"large int preserved", `{"v":900000000000}`, "900000000000"},
		{"null", `{"v":null}`, ""},
		{"empty string", `{"v":""}`, ""},
		{"bool", `{"v":true}`, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wrap struct {
				V jsonStr `json:"v"`
			}
			if err := json.Unmarshal([]byte(tc.in), &wrap); err != nil {
				t.Fatalf("unmarshal %q: %v", tc.in, err)
			}
			if got := wrap.V.String(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRawTaskMixedEncoding(t *testing.T) {
	// STATUS as number, RESPONSIBLE_ID as string, GROUP_ID as number.
	body := `{
		"ID": 42,
		"TITLE": "Fix login",
		"DESCRIPTION": "desc",
		"STATUS": 3,
		"RESPONSIBLE_ID": "77",
		"GROUP_ID": 5,
		"TAGS": ["ai","urgent"]
	}`
	var rt rawTask
	if err := json.Unmarshal([]byte(body), &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	task := rt.toTask()
	if task.ID != "42" {
		t.Errorf("ID = %q, want 42", task.ID)
	}
	if task.Status != "3" {
		t.Errorf("Status = %q, want 3", task.Status)
	}
	if task.ResponsibleID != "77" {
		t.Errorf("ResponsibleID = %q, want 77", task.ResponsibleID)
	}
	if task.GroupID != "5" {
		t.Errorf("GroupID = %q, want 5", task.GroupID)
	}
	if len(task.Tags) != 2 || task.Tags[0] != "ai" {
		t.Errorf("Tags = %v, want [ai urgent]", task.Tags)
	}
}

// --- tag shapes -------------------------------------------------------------

func TestParseTagsThreeShapes(t *testing.T) {
	contains := func(got []string, want string) bool {
		for _, g := range got {
			if g == want {
				return true
			}
		}
		return false
	}

	t.Run("array of strings", func(t *testing.T) {
		got := parseTags([]byte(`["ai","urgent"]`))
		if len(got) != 2 || !contains(got, "ai") || !contains(got, "urgent") {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("array of objects NAME/TITLE", func(t *testing.T) {
		got := parseTags([]byte(`[{"NAME":"ai"},{"TITLE":"urgent"}]`))
		if len(got) != 2 || !contains(got, "ai") || !contains(got, "urgent") {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("map keyed by id", func(t *testing.T) {
		got := parseTags([]byte(`{"12":{"NAME":"ai"},"13":"urgent"}`))
		if len(got) != 2 || !contains(got, "ai") || !contains(got, "urgent") {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("null / false / empty", func(t *testing.T) {
		for _, in := range []string{`null`, `false`, ``, `{}`, `[]`} {
			if got := parseTags([]byte(in)); len(got) != 0 {
				t.Fatalf("parseTags(%q) = %v, want empty", in, got)
			}
		}
	})
}

// --- GetTask over httptest --------------------------------------------------

func TestGetTask(t *testing.T) {
	var gotPath string
	var gotTaskID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		gotTaskID = r.PostForm.Get("taskId")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":{"task":{
			"id":"55","title":"Sync me","description":"body",
			"status":"3","responsibleId":88,"groupId":"9",
			"tags":["ai"]
		}}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL) // no trailing slash — NewClient should add it
	task, err := c.GetTask(context.Background(), "55")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/tasks.task.get") {
		t.Errorf("path = %q, want .../tasks.task.get", gotPath)
	}
	if gotTaskID != "55" {
		t.Errorf("taskId = %q, want 55", gotTaskID)
	}
	if task.ID != "55" || task.Title != "Sync me" || task.Status != "3" {
		t.Errorf("task = %+v", task)
	}
	if task.ResponsibleID != "88" || task.GroupID != "9" {
		t.Errorf("responsible/group = %q/%q", task.ResponsibleID, task.GroupID)
	}
	if !IsAITask(task) {
		t.Errorf("expected AI task")
	}
}

func TestGetTaskBitrixError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"error":"NOT_FOUND","error_description":"Task not found"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL + "/")
	if _, err := c.GetTask(context.Background(), "999"); err == nil {
		t.Fatal("expected error from Bitrix error envelope")
	}
}

// --- AddTaskComment ---------------------------------------------------------

func TestAddTaskComment(t *testing.T) {
	var gotPath, gotTaskID, gotMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		r.ParseForm()
		gotTaskID = r.PostForm.Get("TASKID")
		gotMsg = r.PostForm.Get("FIELDS[POST_MESSAGE]")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":101}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.AddTaskComment(context.Background(), "7", "hello"); err != nil {
		t.Fatalf("AddTaskComment: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/task.commentitem.add") {
		t.Errorf("path = %q", gotPath)
	}
	if gotTaskID != "7" || gotMsg != "hello" {
		t.Errorf("taskid=%q msg=%q", gotTaskID, gotMsg)
	}
}

// --- UpdateTaskStatus POST shape -------------------------------------------

func TestUpdateTaskStatus(t *testing.T) {
	var gotPath string
	var gotBody updateTaskRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("content-type = %q, want json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("decode body: %v (%s)", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":true}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.UpdateTaskStatus(context.Background(), "7", "5"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/tasks.task.update.json") {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody.TaskID != "7" {
		t.Errorf("taskId = %q, want 7", gotBody.TaskID)
	}
	if gotBody.Fields["STATUS"] != "5" {
		t.Errorf("fields[STATUS] = %q, want 5", gotBody.Fields["STATUS"])
	}
}

// --- IsAITask ---------------------------------------------------------------

func TestIsAITask(t *testing.T) {
	cases := []struct {
		tags []string
		want bool
	}{
		{[]string{"ai"}, true},
		{[]string{"AI"}, true},
		{[]string{" Ai "}, true},
		{[]string{"urgent", "ai"}, true},
		{[]string{"urgent"}, false},
		{nil, false},
	}
	for _, tc := range cases {
		got := IsAITask(&Task{Tags: tc.tags})
		if got != tc.want {
			t.Errorf("IsAITask(%v) = %v, want %v", tc.tags, got, tc.want)
		}
	}
	if IsAITask(nil) {
		t.Error("IsAITask(nil) should be false")
	}
}

// --- status mapping round-trip ---------------------------------------------

func TestMapStatus(t *testing.T) {
	cases := map[string]string{
		"1": StatusTodo,
		"2": StatusTodo,
		"3": StatusInProgress,
		"4": StatusInReview,
		"5": StatusDone,
		"6": StatusBacklog,
		"7": StatusCancelled,
		"":  StatusTodo,
		"x": StatusTodo,
	}
	for in, want := range cases {
		if got := MapStatus(in); got != want {
			t.Errorf("MapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsClosedStatus(t *testing.T) {
	if !IsClosedStatus("5") || !IsClosedStatus("7") {
		t.Fatal("completed/declined must be closed")
	}
	for _, s := range []string{"1", "2", "3", "4", "6", "", "x"} {
		if IsClosedStatus(s) {
			t.Errorf("IsClosedStatus(%q) = true, want false", s)
		}
	}
	if !IsClosedIssueStatus(StatusDone) || !IsClosedIssueStatus(StatusCancelled) {
		t.Fatal("done/cancelled issue statuses must be closed")
	}
	if IsClosedIssueStatus(StatusInProgress) {
		t.Fatal("in_progress must not be closed")
	}
}

func TestBitrixStatusFromIssue(t *testing.T) {
	cases := map[string]string{
		StatusBacklog:    "6",
		StatusTodo:       "2",
		StatusInProgress: "3",
		StatusInReview:   "4",
		StatusDone:       "5",
		StatusCancelled:  "7",
		"weird":          "2",
	}
	for in, want := range cases {
		if got := BitrixStatusFromIssue(in); got != want {
			t.Errorf("BitrixStatusFromIssue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusRoundTrip(t *testing.T) {
	// For every Agora status, mapping to Bitrix and back lands on the same
	// Agora status (the lossless subset).
	for _, s := range []string{StatusBacklog, StatusTodo, StatusInProgress, StatusInReview, StatusDone, StatusCancelled} {
		round := MapStatus(BitrixStatusFromIssue(s))
		if round != s {
			t.Errorf("round-trip %q -> %q -> %q", s, BitrixStatusFromIssue(s), round)
		}
	}
}

// --- ResolveWorkspaceSlug precedence ---------------------------------------

func TestResolveWorkspaceSlugPrecedence(t *testing.T) {
	cfg := RouteConfig{
		DefaultSlug: "sd-main",
		GroupMap:    map[string]string{"123": "sd-cs"},
		TagSlugs:    map[string]bool{"sd-eng": true, "sd-cs": true, "sd-main": true},
	}

	t.Run("tag wins over group", func(t *testing.T) {
		task := &Task{Tags: []string{"ai", "sd-eng"}, GroupID: "123"}
		if got := ResolveWorkspaceSlug(task, cfg); got != "sd-eng" {
			t.Fatalf("got %q, want sd-eng", got)
		}
	})

	t.Run("group when no slug tag", func(t *testing.T) {
		task := &Task{Tags: []string{"ai"}, GroupID: "123"}
		if got := ResolveWorkspaceSlug(task, cfg); got != "sd-cs" {
			t.Fatalf("got %q, want sd-cs", got)
		}
	})

	t.Run("default when nothing matches", func(t *testing.T) {
		task := &Task{Tags: []string{"ai"}, GroupID: "999"}
		if got := ResolveWorkspaceSlug(task, cfg); got != "sd-main" {
			t.Fatalf("got %q, want sd-main", got)
		}
	})

	t.Run("empty when no default and no match", func(t *testing.T) {
		bare := RouteConfig{GroupMap: map[string]string{"1": "x"}}
		task := &Task{Tags: []string{"ai"}, GroupID: "999"}
		if got := ResolveWorkspaceSlug(task, bare); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("tag case-insensitive", func(t *testing.T) {
		task := &Task{Tags: []string{"SD-ENG"}}
		if got := ResolveWorkspaceSlug(task, cfg); got != "sd-eng" {
			t.Fatalf("got %q, want sd-eng", got)
		}
	})
}

// --- ParseWebhookEvent ------------------------------------------------------

func TestParseWebhookEvent(t *testing.T) {
	t.Run("ONTASKUPDATE fields_after", func(t *testing.T) {
		v := url.Values{}
		v.Set("event", "ONTASKUPDATE")
		v.Set("data[FIELDS_AFTER][ID]", "00123")
		ev, id, ok := ParseWebhookEvent(v)
		if !ok || ev != "ONTASKUPDATE" || id != "123" {
			t.Fatalf("ev=%q id=%q ok=%v", ev, id, ok)
		}
	})

	t.Run("ONTASKADD", func(t *testing.T) {
		v := url.Values{}
		v.Set("event", "ONTASKADD")
		v.Set("data[FIELDS_AFTER][ID]", "9")
		ev, id, ok := ParseWebhookEvent(v)
		if !ok || ev != "ONTASKADD" || id != "9" {
			t.Fatalf("ev=%q id=%q ok=%v", ev, id, ok)
		}
	})

	t.Run("unhandled event", func(t *testing.T) {
		v := url.Values{}
		v.Set("event", "ONCRMDEALADD")
		v.Set("data[FIELDS_AFTER][ID]", "9")
		if _, _, ok := ParseWebhookEvent(v); ok {
			t.Fatal("expected ok=false for unhandled event")
		}
	})

	t.Run("missing id", func(t *testing.T) {
		v := url.Values{}
		v.Set("event", "ONTASKUPDATE")
		if _, _, ok := ParseWebhookEvent(v); ok {
			t.Fatal("expected ok=false when id missing")
		}
	})

	t.Run("non-numeric id preserved", func(t *testing.T) {
		v := url.Values{}
		v.Set("event", "ONTASKUPDATE")
		v.Set("data[FIELDS_AFTER][ID]", "T9")
		ev, id, ok := ParseWebhookEvent(v)
		if !ok || ev != "ONTASKUPDATE" || id != "T9" {
			t.Fatalf("ev=%q id=%q ok=%v", ev, id, ok)
		}
	})
}

func TestMapTaskToIssue(t *testing.T) {
	d := MapTaskToIssue(&Task{Title: "Hello", Description: "world", Status: "3"}, "")
	if d.Title != "Hello" || d.Description != "world" || d.Status != StatusInProgress {
		t.Fatalf("draft = %+v", d)
	}
	// Empty title falls back to a placeholder including the id.
	d2 := MapTaskToIssue(&Task{ID: "42", Status: "5"}, "")
	if d2.Title != "Bitrix task 42" || d2.Status != StatusDone {
		t.Fatalf("draft = %+v", d2)
	}
}

func TestNewClientTrailingSlash(t *testing.T) {
	if c := NewClient("https://p.bitrix24.ru/rest/1/tok"); c.baseURL != "https://p.bitrix24.ru/rest/1/tok/" {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
	if c := NewClient("https://p.bitrix24.ru/rest/1/tok/"); c.baseURL != "https://p.bitrix24.ru/rest/1/tok/" {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
	if c := NewClient(""); c.baseURL != "" {
		t.Fatalf("empty baseURL should stay empty, got %q", c.baseURL)
	}
}

// TestMapStage covers the keyword stage→status mapping against the FULL kanban
// vocabulary read off the SD portal (task.stages.get across the sprint groups),
// including its typos ("Ready fo release", "Reayd For Testing") and its
// Russian/English mixing, plus the English board variants (Unready / Doing /
// Need Reviewing / Ready Merging / Completed / Failed). An empty or
// unrecognized stage returns "" so the caller falls back to STATUS.
//
// The collision cases are the reason this table is exhaustive rather than
// representative:
//
//   - "Готов к релизу" must NOT read as done ("готов" is a done keyword) or the
//     mirror gets archived while the release is still pending.
//   - "К выполнению" (To Do) must not be dragged into in_progress by the
//     "выполня" keyword that "Выполняются" needs.
//   - "Blocker(s)" belongs in Agora's own blocked column, which exists.
func TestMapStage(t *testing.T) {
	cases := map[string]string{
		// Group 105 / 91 / 93 — the older boards.
		"Новые":              StatusTodo,
		"Обсуждается":        StatusTodo,
		"Need to discussion": StatusTodo,
		"Перенесем в To Do":  StatusTodo,
		"Отмененные":         StatusCancelled,
		"К выполнению":       StatusTodo,
		"В работе":           StatusInProgress,
		"READY FOR TESTING":  StatusInReview,
		"Тестирование":       StatusInReview,
		"Нужен Merge":        StatusInReview,
		"Готов к релизу":     StatusInReview,
		"Готово":             StatusDone,
		// Sprint groups 149 / 151 / 153 / 155 / 175 / 187.
		"To Do":                     StatusTodo,
		"Draft":                     StatusTodo,
		"Выполняются":               StatusInProgress,
		"Returned":                  StatusInProgress,
		"Dev testing":               StatusInProgress,
		"Dev Testing":               StatusInProgress,
		"Blocker":                   StatusBlocked,
		"Blockers":                  StatusBlocked,
		"Code Review":               StatusInReview,
		"In Code Review":            StatusInReview,
		"Review":                    StatusInReview,
		"Ready for testing":         StatusInReview,
		"Reayd For Testing(Mobile)": StatusInReview,
		"Ready For Testing (Web)":   StatusInReview,
		"Testing (Mobile)":          StatusInReview,
		"Testing web":               StatusInReview,
		"Тестинг":                   StatusInReview,
		"Need Merge":                StatusInReview,
		"Need merge":                StatusInReview,
		"Ready for release":         StatusInReview,
		"Ready fo release":          StatusInReview,
		"Ready For Release":         StatusInReview,
		"Сделаны":                   StatusDone,
		"Done":                      StatusDone,
		// English boards elsewhere on the portal.
		"UNREADY TASKS":  StatusTodo,
		"DOING":          StatusInProgress,
		"NEED REVIEWING": StatusInReview,
		"READY MERGING":  StatusInReview,
		"COMPLETED":      StatusDone,
		"FAILED":         StatusCancelled,
		// Group 173 uses its kanban as a category axis, not a workflow. Nothing
		// sane to map — the caller falls back to STATUS and logs the label.
		"EPIC":  "",
		"STORY": "",
		"Front": "",
		"Баги и медленные стреницы": "",
		"": "",
		"Какой-то непонятный этап": "",
	}
	for stage, want := range cases {
		if got := MapStage(stage); got != want {
			t.Errorf("MapStage(%q) = %q, want %q", stage, got, want)
		}
	}
}

// TestRawTaskGroupAsArray guards the rawGroupRef decoder against Bitrix's two
// shapes for the nested "group": an object on a grouped task, OR an array ([])
// when the task has no workgroup — the form tasks.task.list returns when
// filtered by RESPONSIBLE_ID. The array form must decode to the zero value, not
// error the whole response (the bug that broke "import by user").
func TestRawTaskGroupAsArray(t *testing.T) {
	t.Run("group as empty array does not error", func(t *testing.T) {
		var rt rawTask
		if err := json.Unmarshal([]byte(`{"id":"7","title":"x","group":[]}`), &rt); err != nil {
			t.Fatalf("group-as-array must decode, got error: %v", err)
		}
		task := rt.toTask()
		if task.GroupID != "" {
			t.Errorf("no-group task must have empty GroupID, got %q", task.GroupID)
		}
	})

	t.Run("group as object still parses", func(t *testing.T) {
		var rt rawTask
		if err := json.Unmarshal([]byte(`{"id":"7","title":"x","group":{"id":"9","name":"Sprint 9"}}`), &rt); err != nil {
			t.Fatalf("group-as-object must decode: %v", err)
		}
		task := rt.toTask()
		if task.GroupID != "9" || task.GroupName != "Sprint 9" {
			t.Errorf("group object must parse, got id=%q name=%q", task.GroupID, task.GroupName)
		}
	})
}

// --- ListTasks pagination ---------------------------------------------------

// Bitrix returns 50 tasks per page and signals more via a top-level `next`
// offset. ListTasks must follow `next` across every page so a group with more
// than 50 tasks imports in full — the regression that left SD tasks missing
// was a hard 50-cap with no paging.
func TestListTasksPaginatesAllPages(t *testing.T) {
	writePage := func(w http.ResponseWriter, from, count int, next *int) {
		var b strings.Builder
		b.WriteString(`{"result":{"tasks":[`)
		for i := 0; i < count; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			id := strconv.Itoa(from + i)
			b.WriteString(`{"id":"` + id + `","title":"T` + id + `","GROUP_ID":"153"}`)
		}
		b.WriteString(`]}`)
		if next != nil {
			b.WriteString(`,"next":` + strconv.Itoa(*next))
		}
		b.WriteString(`}`)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, b.String())
	}

	var reqs int
	var sawGroup string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		reqs++
		sawGroup = r.PostForm.Get("filter[GROUP_ID]")
		switch r.PostForm.Get("start") {
		case "", "0":
			n := 50
			writePage(w, 1, 50, &n) // ids 1..50, more pages follow
		case "50":
			writePage(w, 51, 10, nil) // ids 51..60, final page (no next)
		default:
			t.Errorf("unexpected start offset %q", r.PostForm.Get("start"))
			writePage(w, 0, 0, nil)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	tasks, err := c.ListTasks(context.Background(), "153", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 60 {
		t.Errorf("expected all 60 tasks across both pages, got %d (50-cap regression?)", len(tasks))
	}
	if reqs != 2 {
		t.Errorf("expected 2 paged requests, got %d", reqs)
	}
	if sawGroup != "153" {
		t.Errorf("group filter = %q, want 153", sawGroup)
	}
	// Spot-check that a task from beyond the first page made it through.
	var has60 bool
	for _, tk := range tasks {
		if tk.ID == "60" {
			has60 = true
		}
	}
	if !has60 {
		t.Error("task id 60 (page 2) missing — pagination dropped the tail")
	}
}

// --- department parsing + subtree resolution --------------------------------

// UF_DEPARTMENT arrives from Bitrix in several shapes across portals; deptIDs
// must normalise every one to a clean, zero-free []string.
func TestDeptIDsUnmarshal(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`[152,153]`, []string{"152", "153"}},
		{`["152","153"]`, []string{"152", "153"}},
		{`[]`, nil},
		{`[0]`, nil},
		{`""`, nil},
		{`null`, nil},
		{`false`, nil},
		{`152`, []string{"152"}},
		{`"152"`, []string{"152"}},
		{`0`, nil},
	}
	for _, c := range cases {
		var d deptIDs
		if err := json.Unmarshal([]byte(c.in), &d); err != nil {
			t.Errorf("Unmarshal(%s): %v", c.in, err)
			continue
		}
		if strings.Join([]string(d), ",") != strings.Join(c.want, ",") {
			t.Errorf("Unmarshal(%s) = %v, want %v", c.in, []string(d), c.want)
		}
	}
}

// ResolveDepartmentSubtree matches a department by name (case-insensitive) and
// expands to its descendants, so "SD Разработка" also captures its sub-teams —
// and never leaks a sibling department the responsible filter must exclude.
func TestResolveDepartmentSubtree(t *testing.T) {
	depts := []Department{
		{ID: "1", Name: "Company", Parent: "0"},
		{ID: "10", Name: "SD Разработка", Parent: "1"},
		{ID: "11", Name: "Frontend", Parent: "10"}, // child of SD Разработка
		{ID: "12", Name: "Backend", Parent: "10"},  // child of SD Разработка
		{ID: "13", Name: "Web UI", Parent: "11"},   // grandchild
		{ID: "20", Name: "Sales", Parent: "1"},     // sibling — must NOT match
		{ID: "21", Name: "QA", Parent: "20"},       // under Sales — must NOT match
	}
	got := ResolveDepartmentSubtree(depts, []string{"sd разработка"}) // case-insensitive
	want := map[string]bool{"10": true, "11": true, "12": true, "13": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected department %s in the SD Разработка subtree", id)
		}
	}
	if got["20"] || got["21"] {
		t.Error("Sales / QA leaked into the SD Разработка subtree")
	}

	// No matching name → empty set (callers read this as "no filter").
	if ids := ResolveDepartmentSubtree(depts, []string{"Nonexistent"}); len(ids) != 0 {
		t.Errorf("unmatched name must yield empty set, got %v", ids)
	}
	// No names → empty set.
	if ids := ResolveDepartmentSubtree(depts, nil); len(ids) != 0 {
		t.Errorf("no names must yield empty set, got %v", ids)
	}
}

// ListUsers must paginate user.get so the "import by responsible" picker lists
// every active user — a portal with hundreds of users used to hide anyone past
// #50, so a high-id responsible (e.g. 525) never appeared and their tasks
// couldn't be imported.
func TestListUsersPaginatesAllUsers(t *testing.T) {
	writeUserPage := func(w http.ResponseWriter, ids []int, next *int) {
		var b strings.Builder
		b.WriteString(`{"result":[`)
		for i, id := range ids {
			if i > 0 {
				b.WriteString(",")
			}
			s := strconv.Itoa(id)
			b.WriteString(`{"ID":"` + s + `","NAME":"User` + s + `","LAST_NAME":"L` + s + `","EMAIL":"u` + s + `@salesdoc.io","UF_DEPARTMENT":[10]}`)
		}
		b.WriteString(`]`)
		if next != nil {
			b.WriteString(`,"next":` + strconv.Itoa(*next))
		}
		b.WriteString(`}`)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, b.String())
	}

	page1 := make([]int, 0, 50)
	for i := 1; i <= 50; i++ {
		page1 = append(page1, i)
	}
	page2 := []int{501, 510, 525} // 525 == the previously-hidden responsible

	var reqs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		reqs++
		if r.PostForm.Get("start") == "" {
			n := 50
			writeUserPage(w, page1, &n)
		} else {
			writeUserPage(w, page2, nil)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	users, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 53 {
		t.Errorf("expected 53 users across both pages, got %d", len(users))
	}
	if reqs != 2 {
		t.Errorf("expected 2 paged requests, got %d", reqs)
	}
	var found525 bool
	for _, u := range users {
		if u.ID == "525" {
			found525 = true
			if len(u.Department) != 1 || u.Department[0] != "10" {
				t.Errorf("user 525 department = %v, want [10]", u.Department)
			}
		}
	}
	if !found525 {
		t.Error("user 525 (page 2) missing — the picker would still hide them")
	}
}

// TestGetTaskStagesPortalOrder pins that stages come back in the portal's own
// SORT order, not numeric id order. Sprint group 153 is the real shape: the
// original Новые/Выполняются/Сделаны triple (ids 2531/2533/2535) was created
// first, and the review columns added later carry HIGHER ids while sitting
// BETWEEN them on the board. Ordering by id would make "the first in_review
// column" Need Merge instead of Code Review, and an outbound status mirror would
// skip the whole review chain.
func TestGetTaskStagesPortalOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":{
			"2531":{"ID":"2531","TITLE":"Новые","SORT":100},
			"2533":{"ID":"2533","TITLE":"Выполняются","SORT":200},
			"2879":{"ID":"2879","TITLE":"Code Review","SORT":300},
			"2877":{"ID":"2877","TITLE":"Need Merge","SORT":400},
			"2535":{"ID":"2535","TITLE":"Сделаны","SORT":500}
		}}`)
	}))
	defer srv.Close()

	stages, err := NewClient(srv.URL).GetTaskStages(context.Background(), "153")
	if err != nil {
		t.Fatalf("GetTaskStages: %v", err)
	}
	want := []string{"Новые", "Выполняются", "Code Review", "Need Merge", "Сделаны"}
	if len(stages) != len(want) {
		t.Fatalf("got %d stages, want %d", len(stages), len(want))
	}
	for i, title := range want {
		if stages[i].Title != title {
			t.Errorf("stage[%d] = %q, want %q", i, stages[i].Title, title)
		}
	}
	if titles := StageTitles(stages); titles["2879"] != "Code Review" {
		t.Errorf("StageTitles[2879] = %q, want Code Review", titles["2879"])
	}
}

// TestGetTaskStagesNoKanban: a group without a kanban returns empty, not an
// error, so the caller degrades to STATUS-only mapping.
func TestGetTaskStagesNoKanban(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":[]}`)
	}))
	defer srv.Close()

	stages, err := NewClient(srv.URL).GetTaskStages(context.Background(), "999")
	if err != nil {
		t.Fatalf("GetTaskStages: %v", err)
	}
	if len(stages) != 0 {
		t.Fatalf("got %d stages, want 0", len(stages))
	}
}

// TestWithinRecencyWindow covers the rolling import window that replaces a
// hardcoded sprint-group list. The portal creates a sprint group every couple of
// weeks (Спринт 12 / Sprint(12) / Спринт (13) / Спринт (14) — the last created
// the same day this was written), so a fixed list silently stops importing the
// sprint the team just moved to.
func TestWithinRecencyWindow(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	ts := func(d time.Time) string { return d.Format(time.RFC3339) }

	cases := []struct {
		name string
		task *Task
		days int
		want bool
	}{
		{
			name: "created inside the window",
			task: &Task{CreatedAt: ts(now.AddDate(0, 0, -3))},
			days: 30, want: true,
		},
		{
			name: "old but changed today — active work must not age out",
			task: &Task{CreatedAt: ts(now.AddDate(0, -6, 0)), ChangedAt: ts(now.AddDate(0, 0, -1))},
			days: 30, want: true,
		},
		{
			name: "old and untouched",
			task: &Task{CreatedAt: ts(now.AddDate(0, -6, 0)), ChangedAt: ts(now.AddDate(0, -5, 0))},
			days: 30, want: false,
		},
		{
			name: "exactly on the cutoff counts as inside",
			task: &Task{CreatedAt: ts(now.AddDate(0, 0, -30))},
			days: 30, want: true,
		},
		{
			name: "window disabled imports everything",
			task: &Task{CreatedAt: ts(now.AddDate(0, -24, 0))},
			days: 0, want: true,
		},
		{
			name: "negative window is treated as disabled",
			task: &Task{CreatedAt: ts(now.AddDate(0, -24, 0))},
			days: -5, want: true,
		},
		{
			// The dates are only present when the caller SELECTed them. Failing
			// closed here would silently stop importing everything.
			name: "no parseable dates fails OPEN",
			task: &Task{},
			days: 30, want: true,
		},
		{
			name: "unparseable garbage fails OPEN",
			task: &Task{CreatedAt: "not a date", ChangedAt: "also not"},
			days: 30, want: true,
		},
		{
			name: "nil task",
			task: nil,
			days: 30, want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WithinRecencyWindow(c.task, c.days, now); got != c.want {
				t.Errorf("WithinRecencyWindow(%+v, %d) = %v, want %v", c.task, c.days, got, c.want)
			}
		})
	}
}
