package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohofake"
)

// zohoConnectorWS is the workspace whose Zoho connector the current test's
// people connect through (set by startFakeZoho).
var zohoConnectorWS string

// startFakeZoho serves the in-memory Zoho, points every Zoho host and the
// sealing key at it, and sets up a workspace Zoho connector (the fake's
// client, no org-level sync grant) the way an admin would in Settings.
func startFakeZoho(t *testing.T) *zohofake.Server {
	t.Helper()
	fake := zohofake.New("")
	srv := httptest.NewServer(fake)
	fake.BaseURL = srv.URL
	t.Cleanup(srv.Close)

	configureZohoConnEnv(t, srv.URL)
	t.Setenv("ZOHO_DYN_DESK_BASE", srv.URL)

	orig := testHandler.cfg.PublicURL
	testHandler.cfg.PublicURL = "https://api.example.test"
	t.Cleanup(func() { testHandler.cfg.PublicURL = orig })

	zohoClientCache.Range(func(k, _ any) bool { zohoClientCache.Delete(k); return true })

	zohoConnectorWS = createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-connector", "owner")
	if w := putZohoConn(t, zohoConnectorWS, map[string]any{
		"dc": "us", "client_id": zohofake.ClientID, "client_secret": zohofake.ClientSecret,
	}); w.Code != http.StatusOK {
		t.Fatalf("set up connector: %d %s", w.Code, w.Body.String())
	}
	return fake
}

// newZohoTestUser creates a user (and cleans it up), for a second person.
func newZohoTestUser(t *testing.T, label string) string {
	t.Helper()
	var id string
	email := fmt.Sprintf("zoho-%s-%d@agora-example.com", label, time.Now().UnixNano())
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, "Zoho "+label, email,
	).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, id) })
	return id
}

func zohoMeRequest(method, path, userID string, body any) *http.Request {
	req := newRequest(method, path, body)
	req.Header.Set("X-User-ID", userID)
	return req
}

// joinZohoConnectorWS makes userID a member of the connector workspace.
func joinZohoConnectorWS(t *testing.T, userID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`,
		zohoConnectorWS, userID); err != nil {
		t.Fatalf("join connector workspace: %v", err)
	}
}

// startZohoConnect runs POST /api/me/zoho/connect for the connector
// workspace and returns the consent URL.
func startZohoConnect(t *testing.T, userID string) *url.URL {
	t.Helper()
	joinZohoConnectorWS(t, userID)
	w := httptest.NewRecorder()
	testHandler.ConnectMyZohoAccount(w, zohoMeRequest("POST", "/api/me/zoho/connect", userID, map[string]any{"workspace_id": zohoConnectorWS}))
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	u, err := url.Parse(resp.URL)
	if err != nil || resp.URL == "" {
		t.Fatalf("connect url: %q %v", resp.URL, err)
	}
	return u
}

func zohoCallback(t *testing.T, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/integrations/zoho/callback?"+query.Encode(), nil)
	testHandler.ZohoOAuthCallback(w, req)
	return w
}

// connectZohoAs walks the whole Connect Zoho flow for userID, signing in at
// the fake as the given Zoho person.
func connectZohoAs(t *testing.T, fake *zohofake.Server, userID, zohoKey string) {
	t.Helper()
	consent := startZohoConnect(t, userID)
	w := zohoCallback(t, url.Values{
		"code":            {"code-" + zohoKey},
		"state":           {consent.Query().Get("state")},
		"location":        {"us"},
		"accounts-server": {fake.BaseURL},
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Zoho is connected") {
		t.Fatalf("callback as %s: %d %s", zohoKey, w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM zoho_account WHERE user_id = $1`, userID)
		testPool.Exec(context.Background(), `DELETE FROM zoho_call_log WHERE user_id = $1`, userID)
	})
}

func getMyZoho(t *testing.T, userID string) zohoAccountResponse {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.GetMyZohoAccount(w, zohoMeRequest("GET", "/api/me/zoho?workspace_id="+zohoConnectorWS, userID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var resp zohoAccountResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return resp
}

func TestZohoAccount_ConnectShowsZohoRoleAndProfile(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	userID := newZohoTestUser(t, "connect")
	joinZohoConnectorWS(t, userID)

	if got := getMyZoho(t, userID); !got.Available || got.Connected {
		t.Fatalf("before connect: %+v", got)
	}

	consent := startZohoConnect(t, userID)
	q := consent.Query()
	if consent.Path != "/oauth/v2/auth" || q.Get("client_id") != zohofake.ClientID ||
		q.Get("access_type") != "offline" || q.Get("redirect_uri") != "https://api.example.test/api/integrations/zoho/callback" {
		t.Fatalf("consent url = %s", consent)
	}
	for _, scope := range strings.Split(q.Get("scope"), ",") {
		if !strings.HasSuffix(scope, ".READ") {
			t.Fatalf("scope %q is not read-only (%s)", scope, q.Get("scope"))
		}
	}

	w := zohoCallback(t, url.Values{"code": {"code-shohruh"}, "state": {q.Get("state")}, "location": {"us"}, "accounts-server": {fake.BaseURL}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "shohruh.a@octane-example.com") {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM zoho_account WHERE user_id = $1`, userID) })

	got := getMyZoho(t, userID)
	if !got.Connected || got.Status != "connected" || got.Email != "shohruh.a@octane-example.com" ||
		got.CRMRole != "Collections Agent" || got.CRMProfile != "Standard" ||
		len(got.DeskDepartments) != 1 || got.DeskDepartments[0] != "Collections" {
		t.Fatalf("after connect: %+v", got)
	}

	// The grant is sealed at rest, never stored in the clear.
	var sealed []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT refresh_token_encrypted FROM zoho_account WHERE user_id = $1`, userID).Scan(&sealed); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if strings.Contains(string(sealed), "rt-shohruh") {
		t.Fatal("refresh token stored in plaintext")
	}
}

func TestZohoAccount_StateIsSingleUse(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	userID := newZohoTestUser(t, "state")
	state := startZohoConnect(t, userID).Query().Get("state")
	back := url.Values{"code": {"code-dilnoza"}, "state": {state}, "location": {"us"}, "accounts-server": {fake.BaseURL}}

	if w := zohoCallback(t, back); w.Code != http.StatusOK {
		t.Fatalf("first use: %d %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM zoho_account WHERE user_id = $1`, userID) })
	if w := zohoCallback(t, back); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	back.Set("state", "made-up")
	if w := zohoCallback(t, back); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: %d", w.Code)
	}
	if w := zohoCallback(t, url.Values{"error": {"access_denied"}}); !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("cancel page: %s", w.Body.String())
	}
}

func TestZohoAccount_NoConnectorMeansUnavailable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	startFakeZoho(t)
	userID := newZohoTestUser(t, "unavailable")
	joinZohoConnectorWS(t, userID)
	if _, err := testPool.Exec(context.Background(), `DELETE FROM zoho_connection WHERE workspace_id = $1`, zohoConnectorWS); err != nil {
		t.Fatalf("remove connector: %v", err)
	}
	if got := getMyZoho(t, userID); got.Available {
		t.Fatalf("expected unavailable, got %+v", got)
	}
	w := httptest.NewRecorder()
	testHandler.ConnectMyZohoAccount(w, zohoMeRequest("POST", "/api/me/zoho/connect", userID, map[string]any{"workspace_id": zohoConnectorWS}))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "connector isn't set up") {
		t.Fatalf("connect without connector: %d %s", w.Code, w.Body.String())
	}
}

func TestZohoAccount_ConnectNeedsMembership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	startFakeZoho(t)
	outsider := newZohoTestUser(t, "outsider")
	w := httptest.NewRecorder()
	testHandler.ConnectMyZohoAccount(w, zohoMeRequest("POST", "/api/me/zoho/connect", outsider, map[string]any{"workspace_id": zohoConnectorWS}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("outsider connect: %d %s", w.Code, w.Body.String())
	}
	if got := getMyZoho(t, outsider); got.Available {
		t.Fatalf("outsider sees the connector as available: %+v", got)
	}
}

// Removing the connector strands the grants minted under it: they show as
// "reconnect" and are never used.
func TestZohoAccount_RemovedConnectorNeedsReconnect(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	userID := newZohoTestUser(t, "stranded")
	connectZohoAs(t, fake, userID, "shohruh")
	if _, err := testPool.Exec(context.Background(), `DELETE FROM zoho_connection WHERE workspace_id = $1`, zohoConnectorWS); err != nil {
		t.Fatalf("remove connector: %v", err)
	}
	zohoClientCache.Delete(userID)
	if got := getMyZoho(t, userID); !got.Connected || got.Status != "reconnect" {
		t.Fatalf("after connector removed: %+v", got)
	}
	if _, ok := testHandler.zohoClientsForUser(context.Background(), parseUUID(userID)); ok {
		t.Fatal("a grant from a removed connector was still usable")
	}
}

func TestZohoAccount_AgentsCantManageIt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	startFakeZoho(t)
	for _, tc := range []struct {
		method string
		fn     func(http.ResponseWriter, *http.Request)
	}{
		{"GET", testHandler.GetMyZohoAccount},
		{"POST", testHandler.ConnectMyZohoAccount},
		{"DELETE", testHandler.DisconnectMyZohoAccount},
	} {
		w := httptest.NewRecorder()
		req := zohoMeRequest(tc.method, "/api/me/zoho", testUserID, map[string]any{"workspace_id": zohoConnectorWS})
		req.Header.Set("X-Actor-Source", "task_token")
		tc.fn(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s as agent: %d", tc.method, w.Code)
		}
	}
}

func TestZohoAccount_DisconnectRevokesAtZoho(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	userID := newZohoTestUser(t, "disconnect")
	connectZohoAs(t, fake, userID, "shohruh")

	w := httptest.NewRecorder()
	testHandler.DisconnectMyZohoAccount(w, zohoMeRequest("DELETE", "/api/me/zoho", userID, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	if got := getMyZoho(t, userID); got.Connected {
		t.Fatalf("still connected: %+v", got)
	}
	if !fake.Revoked("rt-shohruh") {
		t.Fatal("grant was not revoked at Zoho")
	}
}
