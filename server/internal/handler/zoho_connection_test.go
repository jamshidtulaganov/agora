package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// zohoConnStub fakes the Zoho accounts + CRM API for connection tests.
func zohoConnStub(t *testing.T, rejectAuth bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if rejectAuth {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_code"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-test", "expires_in": 3600})
	})
	mux.HandleFunc("/crm/v8/org", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"org": []map[string]any{{"id": "42"}}})
	})
	mux.HandleFunc("/crm/v8/users", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"users": []map[string]any{
			{"id": "77", "full_name": "Conn Probe", "email": "probe@octanefuel.com"},
		}})
	})
	mux.HandleFunc("/crm/v8/settings/modules", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"modules": []map[string]any{
			{"api_name": "CustomModule34", "module_name": "Tickets", "generated_type": "custom", "api_supported": true},
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// configureZohoConnEnv points the handler's CRM client at the stub and
// installs a throwaway sealing key.
func configureZohoConnEnv(t *testing.T, base string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("AGORA_ZOHO_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("ZOHO_DYN_ACCOUNTS_BASE", base)
	t.Setenv("ZOHO_DYN_API_BASE", base)
	// The box is memoized once per process; reset so each test's key takes
	// effect (same package, so the vars are reachable).
	zohoBoxOnce = sync.Once{}
	zohoBoxVal, zohoBoxErr = nil, nil
}

func putZohoConn(t *testing.T, wsID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/workspaces/"+wsID+"/zoho-connection", body)
	req = withURLParam(req, "id", wsID)
	testHandler.PutZohoConnection(w, req)
	return w
}

func TestZohoConnection_PutGetDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoConnStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-conn", "owner")

	body := map[string]any{
		"dc": "us", "client_id": "1000.abc", "client_secret": "secret-material",
		"refresh_token": "1000.refresh.token", "crm_org_id": "42",
	}
	w := putZohoConn(t, wsID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-material") || strings.Contains(w.Body.String(), "refresh.token") {
		t.Fatalf("PUT response leaked secret material: %s", w.Body.String())
	}

	// Secrets are sealed at rest — the stored bytes must not contain plaintext.
	var sealedSecret, sealedRefresh []byte
	var probe string
	if err := testPool.QueryRow(ctx, `
SELECT client_secret_encrypted, refresh_token_encrypted, probe_status
FROM zoho_connection WHERE workspace_id = $1`, wsID).Scan(&sealedSecret, &sealedRefresh, &probe); err != nil {
		t.Fatalf("load row: %v", err)
	}
	if strings.Contains(string(sealedSecret), "secret-material") || strings.Contains(string(sealedRefresh), "refresh.token") {
		t.Fatal("secrets stored in plaintext")
	}
	if probe != "ok" {
		t.Fatalf("probe_status = %q, want ok", probe)
	}

	// Status endpoint: configured, no secrets.
	sw := httptest.NewRecorder()
	sreq := newRequest("GET", "/api/workspaces/"+wsID+"/zoho-connection", nil)
	sreq = withURLParam(sreq, "id", wsID)
	testHandler.GetZohoConnectionStatus(sw, sreq)
	if sw.Code != http.StatusOK || !strings.Contains(sw.Body.String(), `"configured":true`) {
		t.Fatalf("status: %d %s", sw.Code, sw.Body.String())
	}
	if strings.Contains(sw.Body.String(), "secret-material") {
		t.Fatalf("status leaked secret: %s", sw.Body.String())
	}

	// Audit row written, without secrets.
	var details string
	if err := testPool.QueryRow(ctx, `
SELECT details::text FROM activity_log
WHERE workspace_id = $1 AND action = 'zoho_connection_updated'
ORDER BY created_at DESC LIMIT 1`, wsID).Scan(&details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if strings.Contains(details, "secret-material") {
		t.Fatalf("audit leaked secret: %s", details)
	}

	// Delete.
	dw := httptest.NewRecorder()
	dreq := newRequest("DELETE", "/api/workspaces/"+wsID+"/zoho-connection", nil)
	dreq = withURLParam(dreq, "id", wsID)
	testHandler.DeleteZohoConnection(dw, dreq)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d: %s", dw.Code, dw.Body.String())
	}
}

func TestZohoConnection_RejectsInvalidGrant(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoConnStub(t, true)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-conn-bad", "owner")

	w := putZohoConn(t, wsID, map[string]any{
		"dc": "us", "client_id": "x", "client_secret": "y", "refresh_token": "z",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for rejected grant, got %d: %s", w.Code, w.Body.String())
	}
}

func TestZohoConnection_MemberAndAgentForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoConnStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-conn-member", "member")

	if w := putZohoConn(t, wsID, map[string]any{"dc": "us", "client_id": "a", "client_secret": "b", "refresh_token": "c"}); w.Code != http.StatusForbidden {
		t.Fatalf("member PUT: expected 403, got %d", w.Code)
	}

	aw := httptest.NewRecorder()
	areq := newRequest("PUT", "/api/workspaces/"+wsID+"/zoho-connection", map[string]any{})
	areq = withURLParam(areq, "id", wsID)
	areq.Header.Set("X-Actor-Source", "task_token")
	areq.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	testHandler.PutZohoConnection(aw, areq)
	if aw.Code != http.StatusForbidden {
		t.Fatalf("agent PUT: expected 403, got %d", aw.Code)
	}
}

func TestZohoCRMModulesDiscovery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoConnStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-discovery", "owner")

	// No connection yet → 400.
	mw := httptest.NewRecorder()
	mreq := newRequest("GET", "/api/workspaces/"+wsID+"/zoho/crm/modules", nil)
	mreq = withURLParam(mreq, "id", wsID)
	testHandler.ListZohoCRMModules(mw, mreq)
	if mw.Code != http.StatusBadRequest {
		t.Fatalf("no connection: expected 400, got %d: %s", mw.Code, mw.Body.String())
	}

	if w := putZohoConn(t, wsID, map[string]any{"dc": "us", "client_id": "a", "client_secret": "b", "refresh_token": "c"}); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}

	mw2 := httptest.NewRecorder()
	mreq2 := newRequest("GET", "/api/workspaces/"+wsID+"/zoho/crm/modules", nil)
	mreq2 = withURLParam(mreq2, "id", wsID)
	testHandler.ListZohoCRMModules(mw2, mreq2)
	if mw2.Code != http.StatusOK || !strings.Contains(mw2.Body.String(), "CustomModule34") {
		t.Fatalf("modules: %d %s", mw2.Code, mw2.Body.String())
	}
}
