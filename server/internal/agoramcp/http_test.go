package agoramcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const otherWorkspaceID = "00000000-0000-4000-8000-000000000005"

func TestRemoteWorkspaceConnector(t *testing.T) {
	var denied, writes int
	var createdStatus string
	var lastAuthor string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token != "Bearer mul_reader" && token != "Bearer mul_other" {
			http.Error(w, "invalid", 401)
			return
		}
		if r.URL.Path == "/api/workspaces" {
			list := []map[string]string{{"id": workspaceID, "name": "Team", "slug": "team"}}
			if token == "Bearer mul_reader" {
				list = append(list, map[string]string{"id": otherWorkspaceID, "name": "Ops", "slug": "ops"})
			}
			json.NewEncoder(w).Encode(list)
			return
		}
		if got := r.Header.Get("X-Workspace-ID"); got != workspaceID {
			t.Errorf("workspace scope %q on %s", got, r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/issues/" + issueID:
			if token == "Bearer mul_other" {
				denied++
				http.NotFound(w, r)
				return
			}
			io.WriteString(w, "{}")
		case "/api/issues":
			var created map[string]any
			json.NewDecoder(r.Body).Decode(&created)
			createdStatus, _ = created["status"].(string)
			json.NewEncoder(w).Encode(map[string]any{"id": issueID, "title": created["title"], "status": createdStatus})
		case "/api/issues/" + issueID + "/comments":
			writes++
			lastAuthor = token
			io.WriteString(w, `{"content":"hello"}`)
		case "/api/attachments/" + issueID:
			json.NewEncoder(w).Encode(map[string]any{"workspace_id": workspaceID, "issue_id": issueID, "filename": "shot.png", "content_type": "image/png", "size_bytes": 8})
		case "/api/attachments/" + issueID + "/download":
			io.WriteString(w, "\x89PNG\r\n\x1a\n")
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
	for _, name := range []string{"download_attachment", "summarize_comments", "list_comments", "comment_issue", "create_issue", "update_issue"} {
		if status != 200 || !strings.Contains(body, name) {
			t.Fatalf("missing tool %s in %d %s", name, status, body)
		}
	}
	if !strings.Contains(body, "list_workspaces") || !strings.Contains(body, `"workspace"`) {
		t.Fatalf("multi-workspace tools missing: %s", body)
	}
	if _, body := send("mul_reader", "tools/call", `{"name":"list_workspaces","arguments":{}}`); !strings.Contains(body, `\"slug\":\"team\"`) || !strings.Contains(body, `\"slug\":\"ops\"`) {
		t.Fatalf("list_workspaces: %s", body)
	}
	// Two workspaces: the tool must be told which one, and only member ones resolve.
	for _, ws := range []string{``, `,"workspace":"elsewhere"`} {
		params := `{"name":"get_issue","arguments":{"issue_id":"` + issueID + `"` + ws + `}}`
		if _, body := send("mul_reader", "tools/call", params); !strings.Contains(body, `"isError":true`) || !strings.Contains(body, "team (Team)") {
			t.Fatalf("workspace choice %q: %s", ws, body)
		}
	}
	download := `{"name":"download_attachment","arguments":{"attachment_id":"` + issueID + `","workspace":"Team"}}`
	if _, body := send("mul_reader", "tools/call", download); !strings.Contains(body, `"type":"image"`) || !strings.Contains(body, `"mimeType":"image/png"`) || !strings.Contains(body, `"data":"iVBORw0KGgo="`) || strings.Contains(body, "content_base64") {
		t.Fatalf("download: %s", body)
	}
	// mul_other has one workspace, so it is chosen without the argument.
	comment := `{"name":"comment_issue","arguments":{"issue_id":"` + issueID + `","content":"hello"}}`
	for _, params := range []string{`{"name":"download_attachment","arguments":{"attachment_id":"` + issueID + `"}}`, comment, `{"name":"summarize_comments","arguments":{"issue_id":"` + issueID + `"}}`} {
		if _, body := send("mul_other", "tools/call", params); !strings.Contains(body, `"isError":true`) {
			t.Fatalf("inaccessible issue: %s", body)
		}
	}
	if _, body := send("mul_reader", "tools/call", `{"name":"comment_issue","arguments":{"issue_id":"`+issueID+`","content":"hello","workspace":"`+workspaceID+`"}}`); !strings.Contains(body, "hello") {
		t.Fatalf("comment: %s", body)
	}
	// A hosting proxy may connect over loopback while Host is the public name.
	{
		req := httptest.NewRequest("POST", "https://connector.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		for k, v := range map[string]string{"Authorization": "Bearer mul_reader", "Content-Type": "application/json", "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-11-25"} {
			req.Header.Set(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10000}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("loopback-proxied request refused: %d %s", rec.Code, rec.Body)
		}
	}
	// Grants issued before multi-workspace access pin one workspace.
	pinned := func(token, ws string) (int, string) {
		req := httptest.NewRequest("POST", "https://connector.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_workspaces","arguments":{}}}`))
		for k, v := range map[string]string{"X-Workspace-ID": ws, "Authorization": "Bearer " + token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-11-25"} {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if status, body := pinned("mul_reader", workspaceID); status != 200 || !strings.Contains(body, "team") || strings.Contains(body, "ops") {
		t.Fatalf("pinned grant must see only its workspace: %d %s", status, body)
	}
	if status, _ := pinned("mul_other", otherWorkspaceID); status != 403 {
		t.Fatalf("pinned grant to a non-member workspace: %d", status)
	}
	create := `{"name":"create_issue","arguments":{"title":"From Claude","workspace":"team"}}`
	if _, body := send("mul_reader", "tools/call", create); !strings.Contains(body, "From Claude") || createdStatus != "backlog" {
		t.Fatalf("create_issue must land in backlog of the chosen workspace: status=%q %s", createdStatus, body)
	}
	if denied != 3 || writes != 1 || lastAuthor != "Bearer mul_reader" {
		t.Fatalf("denied %d writes %d author %s", denied, writes, lastAuthor)
	}
}
