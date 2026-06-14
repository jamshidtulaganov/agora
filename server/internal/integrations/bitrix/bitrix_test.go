package bitrix

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
	// For every Multica status, mapping to Bitrix and back lands on the same
	// Multica status (the lossless subset).
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
	d := MapTaskToIssue(&Task{Title: "Hello", Description: "world", Status: "3"})
	if d.Title != "Hello" || d.Description != "world" || d.Status != StatusInProgress {
		t.Fatalf("draft = %+v", d)
	}
	// Empty title falls back to a placeholder including the id.
	d2 := MapTaskToIssue(&Task{ID: "42", Status: "5"})
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
