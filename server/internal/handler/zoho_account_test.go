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

// startFakeZoho serves the in-memory Zoho and points every Zoho host, the
// OAuth client and the sealing key at it for this test.
func startFakeZoho(t *testing.T) *zohofake.Server {
	t.Helper()
	fake := zohofake.New("")
	srv := httptest.NewServer(fake)
	fake.BaseURL = srv.URL
	t.Cleanup(srv.Close)

	configureZohoConnEnv(t, srv.URL)
	t.Setenv("ZOHO_DYN_DESK_BASE", srv.URL)
	t.Setenv("AGORA_ZOHO_CLIENT_ID", zohofake.ClientID)
	t.Setenv("AGORA_ZOHO_CLIENT_SECRET", zohofake.ClientSecret)
	t.Setenv("AGORA_ZOHO_SCOPES", "")

	orig := testHandler.cfg.PublicURL
	testHandler.cfg.PublicURL = "https://api.example.test"
	t.Cleanup(func() { testHandler.cfg.PublicURL = orig })

	zohoClientCache.Range(func(k, _ any) bool { zohoClientCache.Delete(k); return true })
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

func zohoMeRequest(method, path, userID string) *http.Request {
	req := newRequest(method, path, map[string]any{})
	req.Header.Set("X-User-ID", userID)
	return req
}

// startZohoConnect runs POST /api/me/zoho/connect and returns the consent URL.
func startZohoConnect(t *testing.T, userID string) *url.URL {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.ConnectMyZohoAccount(w, zohoMeRequest("POST", "/api/me/zoho/connect", userID))
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
	testHandler.GetMyZohoAccount(w, zohoMeRequest("GET", "/api/me/zoho", userID))
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

func TestZohoAccount_UnsetClientMeansUnavailable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	startFakeZoho(t)
	t.Setenv("AGORA_ZOHO_CLIENT_ID", "")
	userID := newZohoTestUser(t, "unavailable")
	if got := getMyZoho(t, userID); got.Available {
		t.Fatalf("expected unavailable, got %+v", got)
	}
	w := httptest.NewRecorder()
	testHandler.ConnectMyZohoAccount(w, zohoMeRequest("POST", "/api/me/zoho/connect", userID))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("connect without client: %d", w.Code)
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
		req := zohoMeRequest(tc.method, "/api/me/zoho", testUserID)
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
	testHandler.DisconnectMyZohoAccount(w, zohoMeRequest("DELETE", "/api/me/zoho", userID))
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
