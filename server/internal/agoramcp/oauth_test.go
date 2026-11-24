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
	const otherWorkspace = "00000000-0000-4000-8000-000000000003"
	const origin = "https://connector.example"
	allowed := map[string]bool{"mul_alice": true, "mul_bob": true}
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		member := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !allowed[member] {
			http.Error(w, "forbidden", 403)
			return
		}
		workspace := workspaceID
		if member == "mul_bob" {
			workspace = otherWorkspace
		}
		if r.URL.Path != "/api/workspaces" && r.Header.Get("X-Workspace-ID") != workspace {
			http.Error(w, "not a member", 403)
			return
		}
		switch r.URL.Path {
		case "/api/workspaces":
			json.NewEncoder(w).Encode([]map[string]string{{"id": workspace, "name": "Team", "slug": "team"}})
		case "/api/workspaces/" + workspace:
			json.NewEncoder(w).Encode(map[string]string{"id": workspace})
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
		h, err := NewMemberConnector(origin, api.URL, "test", make([]byte, 32))
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
	link := func(member, clientID, workspace string) string {
		t.Helper()
		callback := clients[clientID].Callback
		verifier := strings.Repeat("z", 48)
		values := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback}, "scope": {scope}, "state": {"opaque-state"}, "resource": {origin + "/mcp"}, "code_challenge_method": {"S256"}, "code_challenge": {digest(verifier)}}
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
		ticket := regexp.MustCompile(`name="ticket" value="([^"]+)"`).FindStringSubmatch(authorize.Body.String())
		if authorize.Code != 200 || len(ticket) != 2 || !strings.Contains(authorize.Body.String(), workspace) || strings.Contains(authorize.Body.String(), member) {
			t.Fatal("invalid workspace chooser")
		}
		form.Del("pat")
		form.Set("ticket", ticket[1])
		form.Set("workspace", "00000000-0000-4000-8000-000000000004")
		if request("POST", path, form.Encode(), headers).Code != 403 {
			t.Fatal("unavailable workspace accepted")
		}
		form.Set("workspace", workspace)
		authorize = request("POST", path, form.Encode(), headers)
		loc, err := url.Parse(authorize.Header().Get("Location"))
		if err != nil || authorize.Code != 303 || loc.Scheme+"://"+loc.Host+loc.Path != callback || loc.Query().Get("state") != "opaque-state" || loc.Query().Get("code") == "" {
			t.Fatalf("authorization failed: %d", authorize.Code)
		}
		exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "redirect_uri": {callback}, "code": {loc.Query().Get("code")}, "code_verifier": {"invalid"}}
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
	alice, bob := link("mul_alice", claudeClient, workspaceID), link("mul_bob", chatGPTClient, otherWorkspace)
	link("mul_nonmember", claudeClient, workspaceID)
	h = newService() // Existing grants survive a free service restart.
	comment := func(token string) *httptest.ResponseRecorder {
		return request("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"comment_issue","arguments":{"issue_id":"`+issueID+`","content":"hello"}}}`, map[string]string{"X-Workspace-ID": "ignored", "Authorization": "Bearer " + token, "Accept": "application/json, text/event-stream", "Content-Type": "application/json", "MCP-Protocol-Version": "2025-11-25"})
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

func TestDynamicClientRegistration(t *testing.T) {
	const origin = "https://connector.example"
	h, err := NewMemberConnector(origin, "https://api.example", "test", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, origin+path, strings.NewReader(body)))
		return rec
	}
	var meta struct {
		Registration string `json:"registration_endpoint"`
	}
	if json.Unmarshal(call("GET", "/.well-known/oauth-authorization-server", "").Body.Bytes(), &meta) != nil || meta.Registration != origin+"/register" {
		t.Fatalf("metadata missing registration_endpoint: %q", meta.Registration)
	}
	cases := []struct {
		body, client string
		status       int
	}{
		{`{"redirect_uris":["` + claudeCallback + `"],"token_endpoint_auth_method":"none","client_name":"Claude"}`, claudeClient, 201},
		{`{"redirect_uris":["` + chatGPTCallback + `"]}`, chatGPTClient, 201},
		{`{"redirect_uris":["https://evil.example/cb"]}`, "", 400},
		{`{"redirect_uris":["` + claudeCallback + `","https://evil.example/cb"]}`, "", 400},
		{`{"redirect_uris":["` + claudeCallback + `","` + chatGPTCallback + `"]}`, "", 400},
		{`{"redirect_uris":[]}`, "", 400},
		{`{"redirect_uris":["` + claudeCallback + `"],"token_endpoint_auth_method":"client_secret_post"}`, "", 400},
		{`not json`, "", 400},
	}
	for _, c := range cases {
		rec := call("POST", "/register", c.body)
		var reply struct {
			ClientID string `json:"client_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &reply)
		if rec.Code != c.status || reply.ClientID != c.client {
			t.Errorf("register %s: got %d %q, want %d %q", c.body, rec.Code, reply.ClientID, c.status, c.client)
		}
	}
}

func TestConsentFormOrigin(t *testing.T) {
	const origin = "https://connector.example"
	o := &memberOAuth{issuer: origin}
	cases := []struct {
		origin, fetchSite string
		ok                bool
	}{
		{origin, "", true},
		{"null", "same-origin", true}, // Referrer-Policy no-referrer serializes Origin as null
		{"", "same-origin", true},
		{"null", "", false},
		{"null", "cross-site", false},
		{"", "", false},
		{"https://evil.example", "same-origin", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", origin+"/authorize", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", c.fetchSite)
		}
		if got := o.sameOriginPost(r); got != c.ok {
			t.Errorf("Origin=%q Sec-Fetch-Site=%q: got %v, want %v", c.origin, c.fetchSite, got, c.ok)
		}
	}
	h, err := NewMemberConnector(origin, "https://api.example", "test", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", origin+"/healthz", nil))
	if p := rec.Header().Get("Referrer-Policy"); p != "same-origin" {
		t.Fatalf("Referrer-Policy %q makes browsers send Origin: null on the consent form", p)
	}
}
