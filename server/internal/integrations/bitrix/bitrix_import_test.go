package bitrix

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// --- GetTaskComments --------------------------------------------------------

func TestGetTaskComments(t *testing.T) {
	var gotPath, gotTaskID, gotOrder string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		r.ParseForm()
		gotTaskID = r.PostForm.Get("taskId")
		gotOrder = r.PostForm.Get("ORDER[ID]")
		w.Header().Set("Content-Type", "application/json")
		// Bare-array result (Bitrix shape). Returned OUT OF ORDER (3 before 1) and
		// with mixed string/number ids to prove the client sorts ascending; a
		// file-only row with empty POST_MESSAGE must be dropped.
		io.WriteString(w, `{"result":[
			{"ID":3,"POST_MESSAGE":"second comment","AUTHOR_NAME":"Bob","POST_DATE":"2024-01-02 11:00:00"},
			{"ID":"2","POST_MESSAGE":"  ","AUTHOR_NAME":"System","POST_DATE":"2024-01-02 10:05:00"},
			{"ID":1,"POST_MESSAGE":"first comment","AUTHOR_NAME":"Alice","POST_DATE":"2024-01-02 10:00:00"}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	comments, err := c.GetTaskComments(context.Background(), "55")
	if err != nil {
		t.Fatalf("GetTaskComments: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/task.commentitem.getlist") {
		t.Errorf("path = %q, want .../task.commentitem.getlist", gotPath)
	}
	if gotTaskID != "55" {
		t.Errorf("taskId = %q, want 55", gotTaskID)
	}
	// The fix sends NO ORDER param: the legacy task.commentitem.getlist binds
	// args positionally and url.Values.Encode would sort "ORDER[ID]" ahead of
	// "taskId", making Bitrix reject the ORDER array as a non-integer taskId.
	// Ordering is done client-side instead.
	if gotOrder != "" {
		t.Errorf("ORDER[ID] = %q, want empty (no ORDER param sent)", gotOrder)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2 (empty-message row dropped): %+v", len(comments), comments)
	}
	// Sorted ascending by id despite the server returning 3 before 1.
	if comments[0].Author != "Alice" || comments[0].Text != "first comment" || comments[0].Date != "2024-01-02 10:00:00" {
		t.Errorf("comment[0] = %+v", comments[0])
	}
	if comments[1].Author != "Bob" || comments[1].Text != "second comment" {
		t.Errorf("comment[1] = %+v", comments[1])
	}
}

func TestGetTaskCommentsCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var b strings.Builder
		b.WriteString(`{"result":[`)
		for i := 0; i < maxCommentsPerTask+20; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"ID":` + itoa(i) + `,"POST_MESSAGE":"c","AUTHOR_NAME":"X"}`)
		}
		b.WriteString(`]}`)
		io.WriteString(w, b.String())
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	comments, err := c.GetTaskComments(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetTaskComments: %v", err)
	}
	if len(comments) != maxCommentsPerTask {
		t.Fatalf("got %d comments, want cap %d", len(comments), maxCommentsPerTask)
	}
}

func TestGetTaskCommentsBitrixError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"error":"ACCESS_DENIED","error_description":"no scope"}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.GetTaskComments(context.Background(), "1"); err == nil {
		t.Fatal("expected error from Bitrix error envelope")
	}
}

// --- GetTaskFiles -----------------------------------------------------------

func TestGetTaskFiles(t *testing.T) {
	var sawTaskGet, sawAttachedGet bool
	var attachedIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/tasks.task.get"):
			sawTaskGet = true
			// File id list as numbers (Bitrix camelCase key).
			io.WriteString(w, `{"result":{"task":{"id":"55","ufTaskWebdavFiles":[101,102]}}}`)
		case strings.HasSuffix(r.URL.Path, "/disk.attachedObject.get"):
			sawAttachedGet = true
			id := r.PostForm.Get("id")
			attachedIDs = append(attachedIDs, id)
			switch id {
			case "101":
				io.WriteString(w, `{"result":{"ID":101,"NAME":"bug.png","DOWNLOAD_URL":"/rest/download?token=abc&id=101","SIZE":"2048"}}`)
			case "102":
				// Already-absolute URL must be preserved.
				io.WriteString(w, `{"result":{"ID":102,"NAME":"recording.mp4","DOWNLOAD_URL":"https://files.example.com/recording.mp4","SIZE":4096}}`)
			default:
				io.WriteString(w, `{"error":"NOT_FOUND","error_description":"x"}`)
			}
		default:
			io.WriteString(w, `{"result":true}`)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	files, err := c.GetTaskFiles(context.Background(), "55")
	if err != nil {
		t.Fatalf("GetTaskFiles: %v", err)
	}
	if !sawTaskGet || !sawAttachedGet {
		t.Fatalf("expected both endpoints hit: taskGet=%v attachedGet=%v", sawTaskGet, sawAttachedGet)
	}
	if !reflect.DeepEqual(attachedIDs, []string{"101", "102"}) {
		t.Errorf("attached ids = %v, want [101 102]", attachedIDs)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}
	// Host-relative DOWNLOAD_URL must be made absolute against the portal host.
	if !strings.HasPrefix(files[0].URL, srv.URL) {
		t.Errorf("file[0].URL = %q, want absolute under %q", files[0].URL, srv.URL)
	}
	if files[0].Name != "bug.png" || files[0].Size != 2048 {
		t.Errorf("file[0] = %+v", files[0])
	}
	if files[1].URL != "https://files.example.com/recording.mp4" {
		t.Errorf("file[1].URL = %q, want preserved absolute", files[1].URL)
	}
	if files[1].Size != 4096 {
		t.Errorf("file[1].Size = %d, want 4096", files[1].Size)
	}
}

func TestGetTaskFilesNoFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":{"task":{"id":"55","ufTaskWebdavFiles":[]}}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	files, err := c.GetTaskFiles(context.Background(), "55")
	if err != nil {
		t.Fatalf("GetTaskFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}

func TestGetTaskFilesSkipsUnresolvable(t *testing.T) {
	// One file id resolves, one errors — the errored one is skipped, not fatal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/tasks.task.get"):
			io.WriteString(w, `{"result":{"task":{"UF_TASK_WEBDAV_FILES":["7","8"]}}}`)
		case strings.HasSuffix(r.URL.Path, "/disk.attachedObject.get"):
			if r.PostForm.Get("id") == "7" {
				io.WriteString(w, `{"result":{"ID":7,"NAME":"ok.png","DOWNLOAD_URL":"https://x/ok.png","SIZE":1}}`)
			} else {
				io.WriteString(w, `{"error":"NOT_FOUND","error_description":"gone"}`)
			}
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	files, err := c.GetTaskFiles(context.Background(), "55")
	if err != nil {
		t.Fatalf("GetTaskFiles: %v", err)
	}
	if len(files) != 1 || files[0].Name != "ok.png" {
		t.Fatalf("got %+v, want only ok.png", files)
	}
}

// --- DownloadFile -----------------------------------------------------------

func TestDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()
	c := NewClient("https://portal.example/rest/1/tok/")
	data, ctype, err := c.DownloadFile(context.Background(), srv.URL+"/file.png")
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if string(data) != "PNGDATA" {
		t.Errorf("data = %q", data)
	}
	if ctype != "image/png" {
		t.Errorf("ctype = %q, want image/png", ctype)
	}
}

func TestDownloadFileHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient("")
	if _, _, err := c.DownloadFile(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on non-2xx download")
	}
	if _, _, err := c.DownloadFile(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty url")
	}
}

// --- GetGroup ---------------------------------------------------------------

func TestGetGroup(t *testing.T) {
	var gotFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotFilter = r.PostForm.Get("FILTER[ID]")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":[{"ID":5,"NAME":"Sprint 12"}]}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	g, err := c.GetGroup(context.Background(), "5")
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if gotFilter != "5" {
		t.Errorf("FILTER[ID] = %q, want 5", gotFilter)
	}
	if g.ID != "5" || g.Name != "Sprint 12" {
		t.Errorf("group = %+v", g)
	}
}

func TestGetGroupNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":[]}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	g, err := c.GetGroup(context.Background(), "999")
	if err != nil {
		t.Fatalf("GetGroup not-found should be (empty,nil): %v", err)
	}
	if g.ID != "" || g.Name != "" {
		t.Errorf("group = %+v, want empty", g)
	}
}

// --- parseFileIDs -----------------------------------------------------------

func TestParseFileIDs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"array of numbers", `[101,102]`, []string{"101", "102"}},
		{"array of strings", `["7","8"]`, []string{"7", "8"}},
		{"null", `null`, nil},
		{"false", `false`, nil},
		{"empty array", `[]`, nil},
		{"drops zero", `[0,5]`, []string{"5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFileIDs([]byte(tc.in))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseFileIDs(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// itoa is a tiny helper so the cap test doesn't pull in strconv at call sites.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
