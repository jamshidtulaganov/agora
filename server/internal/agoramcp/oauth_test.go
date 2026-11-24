package agoramcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/connectauth"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

const appOrigin = "https://app.example"

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
		if r.URL.Path != "/api/mcp" {
			t.Errorf("connector called %s; it must only proxy to /api/mcp", r.URL.Path)
		}
		for _, h := range []string{"Cookie", "Origin", "X-Workspace-ID"} {
			if r.Header.Get(h) != "" {
				t.Errorf("connector forwarded %s", h)
			}
		}
		json.NewEncoder(w).Encode(map[string]string{"author": strings.TrimPrefix(member, "mul_"), "client": r.Header.Get(connectorClientHeader)})
	}))
	defer api.Close()
	old := http.DefaultTransport
	http.DefaultTransport = api.Client().Transport
	defer func() { http.DefaultTransport = old }()
	newService := func() http.Handler {
		h, err := NewMemberConnector(origin, api.URL, appOrigin, "test", make([]byte, 32))
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
	box, _ := secretbox.New(make([]byte, 32))
	// start runs Claude's /authorize and returns the sealed consent request
	// Agora would receive, plus the browser's consent cookie.
	start := func(clientID, verifier string) (connectauth.Request, string, url.Values) {
		t.Helper()
		values := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {clients[clientID].Callback}, "scope": {scope}, "state": {"opaque-state"}, "resource": {origin + "/mcp"}, "code_challenge_method": {"S256"}, "code_challenge": {digest(verifier)}}
		res := request("GET", "/authorize?"+values.Encode(), "", nil)
		loc, err := url.Parse(res.Header().Get("Location"))
		if res.Code != 303 || err != nil || loc.Scheme+"://"+loc.Host+loc.Path != appOrigin+"/oauth/connect" {
			t.Fatalf("authorize must send the member to Agora: %d %s", res.Code, res.Header().Get("Location"))
		}
		consent, err := connectauth.OpenRequest(box, loc.Query().Get("request"), time.Now())
		if err != nil || consent.ClientName != clients[clientID].Name || !strings.HasPrefix(consent.Return, origin+"/authorize/callback?") {
			t.Fatalf("consent request: %+v %v", consent, err)
		}
		return consent, res.Result().Cookies()[0].String(), values
	}
	// back simulates Agora returning to the connector after the member allows.
	back := func(consent connectauth.Request, cookie, member string) *httptest.ResponseRecorder {
		reply, err := connectauth.SealReply(box, member, consent.Binding, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return request("GET", strings.TrimPrefix(consent.Return+"&reply="+url.QueryEscape(reply), origin), "", map[string]string{"Cookie": cookie})
	}
	link := func(member, clientID, workspace string) string {
		t.Helper()
		callback := clients[clientID].Callback
		verifier := strings.Repeat("z", 48)
		consent, cookie, _ := start(clientID, verifier)
		if r := back(consent, "", member); r.Code != 403 {
			t.Fatalf("reply accepted without the starting browser's cookie: %d", r.Code)
		}
		if r := back(consent, "__Host-agora-mcp-consent=other", member); r.Code != 403 {
			t.Fatalf("reply accepted for another browser: %d", r.Code)
		}
		authorize := back(consent, cookie, member)
		loc, err := url.Parse(authorize.Header().Get("Location"))
		if err != nil || authorize.Code != 303 || loc.Scheme+"://"+loc.Host+loc.Path != callback || loc.Query().Get("state") != "opaque-state" || loc.Query().Get("code") == "" {
			t.Fatalf("authorization failed: %d %s", authorize.Code, authorize.Body)
		}
		headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
		exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "redirect_uri": {callback}, "code": {loc.Query().Get("code")}, "code_verifier": {"invalid"}}
		if r := request("POST", "/token", exchange.Encode(), headers); r.Code != 400 {
			t.Fatal("accepted invalid verifier")
		}
		exchange.Set("code_verifier", verifier)
		response := request("POST", "/token", exchange.Encode(), headers)
		var token struct {
			Token   string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &token) != nil || token.Token == "" || token.Refresh == "" || strings.Contains(response.Body.String(), member) {
			t.Fatalf("invalid token exchange: %s", response.Body)
		}
		if r := request("POST", "/token", exchange.Encode(), headers); r.Code != 400 {
			t.Fatal("code reused")
		}
		refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {token.Refresh}}
		renewed := request("POST", "/token", refresh.Encode(), headers)
		var again struct {
			Token string `json:"access_token"`
		}
		if renewed.Code != 200 || json.Unmarshal(renewed.Body.Bytes(), &again) != nil || again.Token == "" {
			t.Fatalf("refresh failed: %d %s", renewed.Code, renewed.Body)
		}
		other := chatGPTClient
		if clientID == chatGPTClient {
			other = claudeClient
		}
		refresh.Set("client_id", other)
		if r := request("POST", "/token", refresh.Encode(), headers); r.Code != 400 {
			t.Fatal("refresh token accepted for another client")
		}
		refresh.Set("client_id", clientID)
		refresh.Set("refresh_token", token.Token)
		if r := request("POST", "/token", refresh.Encode(), headers); r.Code != 400 {
			t.Fatal("access token accepted as a refresh token")
		}
		return again.Token
	}
	// Denying on Agora returns access_denied to the app.
	{
		consent, cookie, _ := start(claudeClient, strings.Repeat("y", 48))
		r := request("GET", strings.TrimPrefix(consent.Return+"&error=access_denied", origin), "", map[string]string{"Cookie": cookie})
		loc, _ := url.Parse(r.Header().Get("Location"))
		if r.Code != 303 || loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" || loc.Query().Get("state") != "opaque-state" {
			t.Fatalf("deny: %d %s", r.Code, r.Header().Get("Location"))
		}
	}
	alice, bob := link("mul_alice", claudeClient, workspaceID), link("mul_bob", chatGPTClient, otherWorkspace)
	h = newService() // Existing grants survive a free service restart.
	call := func(token string, extra map[string]string) *httptest.ResponseRecorder {
		headers := map[string]string{"X-Workspace-ID": "ignored", "Cookie": "agora_auth=stolen", "Authorization": "Bearer " + token, "Content-Type": "application/json"}
		for k, v := range extra {
			headers[k] = v
		}
		return request("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, headers)
	}
	for member, want := range map[string][2]string{"alice": {alice, "Claude"}, "bob": {bob, "ChatGPT"}} {
		res := call(want[0], nil)
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"author":"`+member+`"`) || !strings.Contains(res.Body.String(), `"client":"`+want[1]+`"`) {
			t.Errorf("%s: upstream must see the member's own token and app: %d %s", member, res.Code, res.Body)
		}
	}
	// Claude's and ChatGPT's servers send their own Origin; it is dropped, not refused.
	for _, from := range []string{"https://claude.ai", "https://chatgpt.com"} {
		if res := call(alice, map[string]string{"Origin": from, "Sec-Fetch-Site": "cross-site"}); res.Code != 200 {
			t.Errorf("request with Origin %s refused: %d %s", from, res.Code, res.Body)
		}
	}
	allowed["mul_alice"] = false
	if call(alice, nil).Code != 403 || call(bob, nil).Code != 200 || call("mul_bob", nil).Code != 401 {
		t.Fatal("member access isolation failed")
	}
}

func TestDynamicClientRegistration(t *testing.T) {
	const origin = "https://connector.example"
	h, err := NewMemberConnector(origin, "https://api.example", appOrigin, "test", make([]byte, 32))
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

func TestConnectorHeaders(t *testing.T) {
	const origin = "https://connector.example"
	if _, err := NewMemberConnector(origin, "https://api.example", "http://app.example", "test", make([]byte, 32)); err == nil {
		t.Fatal("non-HTTPS app URL accepted")
	}
	h, err := NewMemberConnector(origin, "https://api.example", appOrigin, "test", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", origin+"/healthz", nil))
	if p := rec.Header().Get("Referrer-Policy"); p != "same-origin" {
		t.Fatalf("Referrer-Policy %q leaks OAuth state to other sites", p)
	}
}
