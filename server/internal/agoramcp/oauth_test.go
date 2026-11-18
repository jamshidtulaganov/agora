package agoramcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestMemberOAuth(t *testing.T) {
	const origin = "https://connector.example"
	allowed := map[string]bool{"mul_alice": true, "mul_bob": true}
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		member := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !allowed[member] {
			http.Error(w, "forbidden", 403)
			return
		}
		switch r.URL.Path {
		case "/api/workspaces/" + workspaceID:
			json.NewEncoder(w).Encode(map[string]string{"id": workspaceID})
		case "/api/issues/" + issueID:
			io.WriteString(w, "{}")
		case "/api/issues/" + issueID + "/comments":
			json.NewEncoder(w).Encode(map[string]string{"author": strings.TrimPrefix(member, "mul_")})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	old := http.DefaultTransport
	http.DefaultTransport = api.Client().Transport
	defer func() { http.DefaultTransport = old }()
	newService := func() http.Handler {
		h, err := NewMemberConnector(origin, api.URL, workspaceID, "test", make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	h := newService()
	request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	link := func(member string) string {
		t.Helper()
		verifier := strings.Repeat("z", 48)
		values := url.Values{"response_type": {"code"}, "client_id": {claudeClient}, "redirect_uri": {claudeCallback}, "scope": {scope}, "state": {"opaque-state"}, "resource": {origin + "/mcp"}, "code_challenge_method": {"S256"}, "code_challenge": {digest(verifier)}}
		path := "/authorize?" + values.Encode()
		get := request("GET", path, "", nil)
		match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
		if get.Code != 200 || len(match) != 2 {
			t.Fatalf("consent form: %d", get.Code)
		}
		form := url.Values{"csrf": {match[1]}, "pat": {member}}
		headers := map[string]string{"Origin": origin, "Cookie": get.Result().Cookies()[0].String(), "Content-Type": "application/x-www-form-urlencoded"}
		authorize := request("POST", path, form.Encode(), headers)
		if !allowed[member] {
			if authorize.Code != 403 {
				t.Fatalf("nonmember accepted: %d", authorize.Code)
			}
			return ""
		}
		loc, err := url.Parse(authorize.Header().Get("Location"))
		if err != nil || authorize.Code != 303 || loc.Query().Get("state") != "opaque-state" || loc.Query().Get("code") == "" {
			t.Fatalf("authorization failed: %d", authorize.Code)
		}
		exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {claudeClient}, "redirect_uri": {claudeCallback}, "code": {loc.Query().Get("code")}, "code_verifier": {"invalid"}}
		if r := request("POST", "/token", exchange.Encode(), headers); r.Code != 400 {
			t.Fatal("accepted invalid verifier")
		}
		exchange.Set("code_verifier", verifier)
		response := request("POST", "/token", exchange.Encode(), headers)
		var token struct {
			Token string `json:"access_token"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &token) != nil || token.Token == "" || strings.Contains(response.Body.String(), member) {
			t.Fatal("invalid token exchange")
		}
		if r := request("POST", "/token", exchange.Encode(), headers); r.Code != 400 {
			t.Fatal("code reused")
		}
		return token.Token
	}
	alice, bob := link("mul_alice"), link("mul_bob")
	link("mul_nonmember")
	h = newService() // Existing grants survive a free service restart.
	comment := func(token string) *httptest.ResponseRecorder {
		return request("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"comment_issue","arguments":{"issue_id":"`+issueID+`","content":"hello"}}}`, map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json, text/event-stream", "Content-Type": "application/json", "MCP-Protocol-Version": "2025-11-25"})
	}
	for member, token := range map[string]string{"alice": alice, "bob": bob} {
		res := comment(token)
		if res.Code != 200 || !strings.Contains(res.Body.String(), member) {
			t.Errorf("comment author should be %s: %s", member, res.Body)
		}
	}
	allowed["mul_alice"] = false
	if comment(alice).Code != 403 || comment(bob).Code != 200 || comment("mul_bob").Code != 401 {
		t.Fatal("member access isolation failed")
	}
}
