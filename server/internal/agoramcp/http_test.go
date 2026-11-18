package agoramcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteWorkspaceConnector(t *testing.T) {
	var denied, writes int
	var lastAuthor string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token != "Bearer mul_reader" && token != "Bearer mul_other" {
			http.Error(w, "invalid", 401)
			return
		}
		if r.Header.Get("X-Workspace-ID") != workspaceID {
			t.Error("missing workspace scope")
		}
		switch r.URL.Path {
		case "/api/workspaces/" + workspaceID:
			io.WriteString(w, "{}")
		case "/api/issues/" + issueID:
			if token == "Bearer mul_other" {
				denied++
				http.NotFound(w, r)
				return
			}
			io.WriteString(w, "{}")
		case "/api/issues/" + issueID + "/comments":
			writes++
			lastAuthor = token
			io.WriteString(w, `{"content":"hello"}`)
		case "/api/attachments/" + issueID:
			json.NewEncoder(w).Encode(map[string]any{"workspace_id": workspaceID, "issue_id": issueID, "filename": "report.txt", "size_bytes": 2})
		case "/api/attachments/" + issueID + "/download":
			io.WriteString(w, "ok")
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	// Trust only the test API's TLS certificate for this synchronous test.
	old := http.DefaultTransport
	http.DefaultTransport = api.Client().Transport
	defer func() { http.DefaultTransport = old }()
	handler, err := newRemoteHandler(api.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	send := func(token, method, params string) (int, string) {
		req := httptest.NewRequest("POST", "https://connector.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`))
		req.Header.Set("X-Workspace-ID", workspaceID)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	for _, token := range []string{"", "mul_invalid"} {
		if status, _ := send(token, "tools/list", "{}"); status != 401 {
			t.Fatalf("unauthorized status %d", status)
		}
	}
	if status, body := send("mul_reader", "initialize", `{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}`); status != 200 || !strings.Contains(body, "serverInfo") {
		t.Fatalf("initialize %d %s", status, body)
	}
	status, body := send("mul_reader", "tools/list", "{}")
	for _, name := range []string{"download_attachment", "summarize_comments", "list_comments", "comment_issue"} {
		if status != 200 || !strings.Contains(body, name) {
			t.Fatalf("missing tool %s in %d %s", name, status, body)
		}
	}
	if strings.Contains(body, "create_issue") {
		t.Fatal("unexpected remote write tool")
	}
	download := `{"name":"download_attachment","arguments":{"attachment_id":"` + issueID + `"}}`
	if _, body := send("mul_reader", "tools/call", download); !strings.Contains(body, "b2s=") {
		t.Fatalf("download: %s", body)
	}
	comment := `{"name":"comment_issue","arguments":{"issue_id":"` + issueID + `","content":"hello"}}`
	for _, params := range []string{download, comment, `{"name":"summarize_comments","arguments":{"issue_id":"` + issueID + `"}}`} {
		if _, body := send("mul_other", "tools/call", params); !strings.Contains(body, `"isError":true`) {
			t.Fatalf("inaccessible issue: %s", body)
		}
	}
	if _, body := send("mul_reader", "tools/call", comment); !strings.Contains(body, "hello") {
		t.Fatalf("comment: %s", body)
	}
	if denied != 3 || writes != 1 || lastAuthor != "Bearer mul_reader" {
		t.Fatalf("denied %d writes %d author %s", denied, writes, lastAuthor)
	}
}
