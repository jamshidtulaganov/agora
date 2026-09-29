package agoramcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/cli"
)

const workspaceID = "00000000-0000-4000-8000-000000000001"
const issueID = "00000000-0000-4000-8000-000000000002"

func testServer(t *testing.T, writes bool, handler http.HandlerFunc) *Server {
	t.Helper()
	api := httptest.NewServer(handler)
	t.Cleanup(api.Close)
	s, err := New(cli.NewAPIClient(api.URL, workspaceID, "secret"), "test", writes)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProtocol(t *testing.T) {
	s := testServer(t, false, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected API request") })
	input := strings.Join([]string{
		`not-json`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"tools","method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"unknown"}`,
	}, "\n")
	var output bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&output)
	var responses []map[string]json.RawMessage
	for dec.More() {
		var response map[string]json.RawMessage
		if err := dec.Decode(&response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 6 {
		t.Fatalf("responses = %d: %v", len(responses), responses)
	}
	if string(responses[0]["id"]) != "null" || !bytes.Contains(responses[0]["error"], []byte("-32700")) {
		t.Fatalf("missing parse error: %s", responses[0])
	}
	if !bytes.Contains(responses[1]["error"], []byte("initialize")) {
		t.Fatal("tools available before initialization")
	}
	if !bytes.Contains(responses[2]["result"], []byte(`"protocolVersion":"2025-06-18"`)) {
		t.Fatalf("version not negotiated: %s", responses[2]["result"])
	}
	if bytes.Contains(responses[3]["result"], []byte("create_issue")) || !bytes.Contains(responses[3]["result"], []byte("search_knowledge")) {
		t.Fatalf("incorrect read-only tool list: %s", responses[3]["result"])
	}
	if !bytes.Contains(responses[5]["error"], []byte("-32601")) {
		t.Fatal("missing method error")
	}
}

func TestReadOnlyCannotWrite(t *testing.T) {
	s := testServer(t, false, func(w http.ResponseWriter, r *http.Request) { t.Fatal("read-only server reached API") })
	for _, name := range []string{"create_issue", "update_issue", "comment_issue"} {
		if _, err := s.call(context.Background(), name, json.RawMessage(`{"title":"Test"}`)); err == nil {
			t.Errorf("%s allowed", name)
		}
	}
}

func TestArgumentValidation(t *testing.T) {
	s := testServer(t, true, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input reached API") })
	cases := []struct{ name, args string }{
		{"get_issue", `{}`},
		{"get_issue", `{"issue_id":"../../users"}`},
		{"list_issues", `{"workspace_id":"other"}`},
		{"list_issues", `{"limit":1.5}`},
		{"list_issues", `{"limit":101}`},
		{"list_issues", `{"offset":-1}`},
		{"search_issues", `{"query":" "}`},
		{"create_issue", `{"title":"test","status":"wrong"}`},
		{"create_issue", `{"title":"test","assignee_id":"` + issueID + `"}`},
		{"update_issue", `{"issue_id":"` + issueID + `"}`},
		{"list_projects", `null`},
		{"list_projects", `[]`},
		{"list_projects", `{"include_archived":"true"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			if _, err := s.call(context.Background(), tc.name, json.RawMessage(tc.args)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAPIRequests(t *testing.T) {
	cases := []struct{ name, args, method, path, body string }{
		{"get_workspace", `{}`, "GET", "/api/workspaces/" + workspaceID, ""},
		{"list_issues", `{"limit":10,"offset":20,"project_id":"` + issueID + `"}`, "GET", "/api/issues?limit=10&offset=20&project_id=" + issueID + "&workspace_id=" + workspaceID, ""},
		{"search_issues", `{"query":"a & b","include_closed":true}`, "GET", "/api/issues/search?include_closed=true&limit=20&offset=0&q=a+%26+b&workspace_id=" + workspaceID, ""},
		{"get_issue", `{"issue_id":"` + issueID + `"}`, "GET", "/api/issues/" + issueID, ""},
		{"list_comments", `{"issue_id":"` + issueID + `"}`, "GET", "/api/issues/" + issueID + "/comments?tail=50", ""},
		{"search_knowledge", `{"query":"expected behavior"}`, "GET", "/api/knowledge/search?limit=10&q=expected+behavior", ""},
		{"list_project_knowledge", `{"project_id":"` + issueID + `"}`, "GET", "/api/projects/" + issueID + "/knowledge/items?status=active", ""},
		{"create_issue", `{"title":"Bug","description":"Evidence","project_id":"` + issueID + `"}`, "POST", "/api/issues", `{"title":"Bug","status":"backlog","description":"Evidence","project_id":"` + issueID + `"}`},
		{"create_issue", `{"title":"Task","assignee_type":"member","assignee_id":"` + issueID + `"}`, "POST", "/api/issues", `{"title":"Task","status":"backlog","assignee_type":"member","assignee_id":"` + issueID + `"}`},
		{"create_issue", `{"title":"Task","status":"todo"}`, "POST", "/api/issues", `{"title":"Task","status":"todo"}`},
		{"update_issue", `{"issue_id":"` + issueID + `","status":"done"}`, "PUT", "/api/issues/" + issueID, `{"status":"done"}`},
		{"comment_issue", `{"issue_id":"` + issueID + `","content":"Verified"}`, "POST", "/api/issues/" + issueID + "/comments", `{"content":"Verified"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t, true, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.RequestURI(), tc.method, tc.path)
				}
				if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Workspace-ID") != workspaceID {
					t.Error("auth/workspace headers missing")
				}
				if tc.body != "" {
					body, _ := io.ReadAll(r.Body)
					var got, want any
					_ = json.Unmarshal(body, &got)
					_ = json.Unmarshal([]byte(tc.body), &want)
					g, _ := json.Marshal(got)
					v, _ := json.Marshal(want)
					if !bytes.Equal(g, v) {
						t.Errorf("body = %s, want %s", g, v)
					}
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			if _, err := s.call(context.Background(), tc.name, json.RawMessage(tc.args)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentListRedactsConfigurationAndFailsClosed(t *testing.T) {
	for _, payload := range []string{
		`[{"id":"` + issueID + `","name":"Helper","status":"idle","mcp_config":{"token":"private"},"runtime_config":{"password":"private"},"instructions":"private"}]`,
		`null`, `[{"name":"Missing ID"}]`, `[{"id":true,"name":"Wrong ID"}]`, `{"agents":[]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			s := testServer(t, false, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(payload)) })
			result, err := s.call(context.Background(), "list_agents", json.RawMessage(`{}`))
			if strings.Contains(payload, "mcp_config") {
				if err != nil || bytes.Contains(result, []byte("private")) || bytes.Contains(result, []byte("mcp_config")) {
					t.Fatalf("unsafe result: %s, %v", result, err)
				}
			} else if err == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
}

func TestProtocolToolErrors(t *testing.T) {
	s := testServer(t, false, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"future"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`
	var output bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"protocolVersion":"2025-11-25"`) || !strings.Contains(output.String(), `"isError":true`) || !strings.Contains(output.String(), "401") {
		t.Fatalf("protocol/tool errors: %s", output.String())
	}
}

func TestCancellation(t *testing.T) {
	started := make(chan struct{})
	s := testServer(t, false, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), reader, &output) }()
	_, err := io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_projects"}}
`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	_, err = io.WriteString(writer, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}
`)
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	select {
	case err := <-done:
		if err != nil || !strings.Contains(output.String(), `"isError":true`) {
			t.Fatalf("cancelled result: %s, %v", output.String(), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not interrupt API request")
	}
}

func TestNotificationsNeverExecuteTools(t *testing.T) {
	s := testServer(t, true, func(w http.ResponseWriter, r *http.Request) { t.Error("notification executed a tool") })
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"create_issue","arguments":{"title":"bad"}}}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("notification response: %s", out.String())
	}
}

func TestConfigurationAndAPIErrors(t *testing.T) {
	for _, client := range []*cli.APIClient{
		nil, cli.NewAPIClient("https://example.com", "", "token"),
		cli.NewAPIClient("https://example.com", workspaceID, ""),
		cli.NewAPIClient("ftp://example.com", workspaceID, "token"),
		cli.NewAPIClient("https://user:password@example.com", workspaceID, "token"),
	} {
		if _, err := New(client, "test", false); err == nil {
			t.Error("invalid configuration accepted")
		}
	}
	s := testServer(t, false, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if _, err := s.call(context.Background(), "list_projects", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("permission error = %v", err)
	}
}
