package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// zohoBindingStub extends the connection stub with the authorization_code
// grant + the CurrentUser identity probe.
func zohoBindingStub(t *testing.T, rejectCode bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if rejectCode {
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_code"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-user", "refresh_token": "user-refresh-token",
				"scope": "ZohoCRM.modules.ALL", "api_domain": "https://www.zohoapis.com",
			})
		default: // refresh_token grant — workspace conn probe + user client mints
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-any", "expires_in": 3600})
		}
	})
	mux.HandleFunc("/crm/v8/org", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"org": []map[string]any{{"id": "42"}}})
	})
	mux.HandleFunc("/crm/v8/users", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"users": []map[string]any{
			{"id": "77", "full_name": "Test Person", "email": "person@octanefuel.com"},
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func seedZohoConnection(t *testing.T, wsID string) {
	t.Helper()
	if w := putZohoConn(t, wsID, map[string]any{
		"dc": "us", "client_id": "1000.ws", "client_secret": "ws-secret", "refresh_token": "ws-refresh",
	}); w.Code != http.StatusOK {
		t.Fatalf("seed connection: %d %s", w.Code, w.Body.String())
	}
}

func putZohoBinding(t *testing.T, wsID, code string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/workspaces/"+wsID+"/zoho-user-binding", map[string]any{"grant_code": code})
	req = withURLParam(req, "id", wsID)
	testHandler.PutZohoUserBinding(w, req)
	return w
}

func TestZohoUserBinding_PutGetDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	// Plain member — binding is self-service, no admin role needed.
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-binding", "member")
	// The workspace connection itself needs an admin; seed it directly.
	if _, err := testPool.Exec(ctx, `UPDATE member SET role = 'owner' WHERE workspace_id = $1 AND user_id = $2`, wsID, testUserID); err != nil {
		t.Fatalf("promote for seed: %v", err)
	}
	seedZohoConnection(t, wsID)
	if _, err := testPool.Exec(ctx, `UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`, wsID, testUserID); err != nil {
		t.Fatalf("demote back: %v", err)
	}

	w := putZohoBinding(t, wsID, "1000.grantcode.abc")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT binding: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "user-refresh-token") {
		t.Fatalf("binding response leaked refresh token: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "person@octanefuel.com") {
		t.Fatalf("expected identity hint in response: %s", w.Body.String())
	}

	// Sealed at rest.
	var sealed []byte
	if err := testPool.QueryRow(ctx, `
SELECT refresh_token_encrypted FROM zoho_user_binding
WHERE workspace_id = $1 AND user_id = $2`, wsID, testUserID).Scan(&sealed); err != nil {
		t.Fatalf("load binding row: %v", err)
	}
	if strings.Contains(string(sealed), "user-refresh-token") {
		t.Fatal("refresh token stored in plaintext")
	}

	// Status round-trip.
	sw := httptest.NewRecorder()
	sreq := newRequest("GET", "/api/workspaces/"+wsID+"/zoho-user-binding", nil)
	sreq = withURLParam(sreq, "id", wsID)
	testHandler.GetZohoUserBindingStatus(sw, sreq)
	if sw.Code != http.StatusOK || !strings.Contains(sw.Body.String(), `"bound":true`) {
		t.Fatalf("status: %d %s", sw.Code, sw.Body.String())
	}

	// The acting-user client resolves.
	wsUUID := parseUUID(wsID)
	userUUID := parseUUID(testUserID)
	if _, ok := testHandler.zohoCRMClientForUser(ctx, wsUUID, userUUID); !ok {
		t.Fatal("zohoCRMClientForUser: expected ok for bound user")
	}

	// Delete → 204, then status unbound and client resolution fails.
	dw := httptest.NewRecorder()
	dreq := newRequest("DELETE", "/api/workspaces/"+wsID+"/zoho-user-binding", nil)
	dreq = withURLParam(dreq, "id", wsID)
	testHandler.DeleteZohoUserBinding(dw, dreq)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d", dw.Code)
	}
	if _, ok := testHandler.zohoCRMClientForUser(ctx, wsUUID, userUUID); ok {
		t.Fatal("zohoCRMClientForUser: expected failure after unbind")
	}
}

func TestZohoUserBinding_RejectsBadGrantCode(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, true)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-binding-bad", "owner")
	seedZohoConnection(t, wsID)

	if w := putZohoBinding(t, wsID, "expired-code"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for rejected code, got %d: %s", w.Code, w.Body.String())
	}
}

func TestZohoUserBinding_RequiresConnectionAndRejectsAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-binding-noconn", "owner")

	// No workspace connection yet → 400.
	if w := putZohoBinding(t, wsID, "1000.code"); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without workspace connection, got %d: %s", w.Code, w.Body.String())
	}

	// Agent actor → 403 even with an owner host.
	aw := httptest.NewRecorder()
	areq := newRequest("PUT", "/api/workspaces/"+wsID+"/zoho-user-binding", map[string]any{"grant_code": "x"})
	areq = withURLParam(areq, "id", wsID)
	areq.Header.Set("X-Actor-Source", "task_token")
	areq.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	testHandler.PutZohoUserBinding(aw, areq)
	if aw.Code != http.StatusForbidden {
		t.Fatalf("agent PUT: expected 403, got %d", aw.Code)
	}
}
