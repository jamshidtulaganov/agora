package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// zohoDynStub fakes the accounts + CRM API surface the dynamic engine talks
// to: token mint, org probe (connection save), COQL, and record PUT. Extends
// the zohoConnStub shape with the D2/D3 record endpoints.
type zohoDynStub struct {
	srv *httptest.Server

	mu      sync.Mutex
	records []map[string]any // COQL result (single page, more_records=false)
	puts    []map[string]any // decoded bodies of PUT /crm/v8/CustomModule34
}

func newZohoDynStub(t *testing.T) *zohoDynStub {
	t.Helper()
	s := &zohoDynStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-test", "expires_in": 3600})
	})
	mux.HandleFunc("/crm/v8/org", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"org": []map[string]any{{"id": "42"}}})
	})
	mux.HandleFunc("POST /crm/v8/coql", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.records) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": s.records,
			"info": map[string]any{"more_records": false},
		})
	})
	mux.HandleFunc("PUT /crm/v8/CustomModule34", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.puts = append(s.puts, body)
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"status": "success"}}})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *zohoDynStub) setRecords(recs []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = recs
}

func (s *zohoDynStub) putCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.puts)
}

func (s *zohoDynStub) lastPut() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.puts) == 0 {
		return nil
	}
	return s.puts[len(s.puts)-1]
}

// setupZohoDynWorkspace provisions a throwaway workspace (testUserID as
// owner) with a saved Zoho connection pointed at the stub.
func setupZohoDynWorkspace(t *testing.T, ctx context.Context, slug string) (string, *zohoDynStub) {
	t.Helper()
	stub := newZohoDynStub(t)
	configureZohoConnEnv(t, stub.srv.URL)
	wsID := createMcpTestWorkspace(t, ctx, slug, "owner")
	if w := putZohoConn(t, wsID, map[string]any{
		"dc": "us", "client_id": "a", "client_secret": "b", "refresh_token": "c",
	}); w.Code != http.StatusOK {
		t.Fatalf("save connection: %d %s", w.Code, w.Body.String())
	}
	return wsID, stub
}

func postZohoDynConfig(t *testing.T, wsID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces/"+wsID+"/zoho/sync-configs", body)
	req = withURLParam(req, "id", wsID)
	testHandler.CreateZohoSyncConfig(w, req)
	return w
}

// zohoDynConfigRequest builds a request carrying BOTH the workspace and
// config URL params (withURLParam only sets one).
func zohoDynConfigRequest(method, wsID, configID string, body any) *http.Request {
	req := newRequest(method, "/api/workspaces/"+wsID+"/zoho/sync-configs/"+configID, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", wsID)
	rctx.URLParams.Add("configId", configID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func loadZohoDynConfig(t *testing.T, ctx context.Context, wsID string) db.ZohoSyncConfig {
	t.Helper()
	cfg, err := testHandler.Queries.GetZohoSyncConfigByModule(ctx, db.GetZohoSyncConfigByModuleParams{
		WorkspaceID:   parseUUID(wsID),
		Channel:       "crm",
		ModuleApiName: "CustomModule34",
	})
	if err != nil {
		t.Fatalf("load sync config: %v", err)
	}
	return cfg
}

func TestZohoDynInboundCreateUpdateAndDedup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID, stub := setupZohoDynWorkspace(t, ctx, "handler-tests-zohodyn-in")

	if w := postZohoDynConfig(t, wsID, map[string]any{
		"module_api_name": "CustomModule34",
		"field_map": map[string]string{
			"title": "Subject", "description": "Description",
			"priority": "Priority", "due_date": "Due_Date", "status": "Status",
		},
		"status_map": map[string]any{
			"in":  map[string]string{"Open": "todo", "Escalated": "in_progress"},
			"out": map[string]string{"done": "Closed"},
		},
	}); w.Code != http.StatusCreated {
		t.Fatalf("create config: %d %s", w.Code, w.Body.String())
	}

	stub.setRecords([]map[string]any{
		{
			"id": "101", "Subject": "Fix card decline", "Description": "Chase run failed",
			"Priority": "High", "Due_Date": "2026-07-10", "Status": "Open",
			"Owner":         map[string]any{"email": "billing@octane.test", "name": "Billing"},
			"Modified_Time": "2026-07-01T10:00:00+00:00",
		},
		{
			// No mapped title and an unmapped status: falls back to
			// "<module> <id>" and default "todo".
			"id": "102", "Status": "Weird", "Modified_Time": "2026-07-01T11:00:00+00:00",
		},
	})

	cfg := loadZohoDynConfig(t, ctx, wsID)
	if err := testHandler.syncZohoDynConfig(ctx, cfg); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	// Record 101: mapped fields + metadata stamps.
	var title, status, priority, due, metadata string
	if err := testPool.QueryRow(ctx, `
SELECT title, status, priority, COALESCE(due_date::text, ''), metadata::text FROM issue
WHERE workspace_id = $1 AND metadata @> '{"zoho_rec_id":"CustomModule34:101"}'`, wsID).
		Scan(&title, &status, &priority, &due, &metadata); err != nil {
		t.Fatalf("load issue 101: %v", err)
	}
	if title != "Fix card decline" || status != "todo" || priority != "high" || due != "2026-07-10" {
		t.Fatalf("issue 101 = title=%q status=%q priority=%q due=%q", title, status, priority, due)
	}
	for _, want := range []string{
		`"zoho_module": "CustomModule34"`,
		`"zoho_record_url": "https://crm.zoho.com/crm/tab/CustomModule34/101"`,
		`"zoho_status_name": "Open"`,
		`"zoho_owner_email": "billing@octane.test"`,
	} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("issue 101 metadata missing %s: %s", want, metadata)
		}
	}

	// Record 102: title fallback + status_map miss -> todo.
	var title2, status2 string
	if err := testPool.QueryRow(ctx, `
SELECT title, status FROM issue
WHERE workspace_id = $1 AND metadata @> '{"zoho_rec_id":"CustomModule34:102"}'`, wsID).
		Scan(&title2, &status2); err != nil {
		t.Fatalf("load issue 102: %v", err)
	}
	if title2 != "CustomModule34 102" || status2 != "todo" {
		t.Fatalf("issue 102 = title=%q status=%q, want fallback title + todo", title2, status2)
	}

	// Auto-created destination project carries the module marker and was
	// persisted back onto the config row.
	var projectID string
	if err := testPool.QueryRow(ctx, `
SELECT id FROM project
WHERE workspace_id = $1 AND description LIKE '%zoho_module:CustomModule34%'`, wsID).
		Scan(&projectID); err != nil {
		t.Fatalf("auto-created project: %v", err)
	}
	cfg = loadZohoDynConfig(t, ctx, wsID)
	if uuidToString(cfg.ProjectID) != projectID {
		t.Fatalf("config project_id = %s, want %s", uuidToString(cfg.ProjectID), projectID)
	}

	// Cursor advanced to the max Modified_Time seen.
	wantCursor := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	if !cfg.Cursor.Valid || !cfg.Cursor.Time.UTC().Equal(wantCursor) {
		t.Fatalf("cursor = %v (valid=%v), want %v", cfg.Cursor.Time, cfg.Cursor.Valid, wantCursor)
	}

	// Second sweep: same record id changed in Zoho -> in-place update, no
	// duplicate issue.
	stub.setRecords([]map[string]any{
		{
			"id": "101", "Subject": "Fix card decline v2", "Status": "Escalated",
			"Modified_Time": "2026-07-01T12:00:00+00:00",
		},
	})
	if err := testHandler.syncZohoDynConfig(ctx, cfg); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	var count int
	if err := testPool.QueryRow(ctx, `
SELECT COUNT(*) FROM issue
WHERE workspace_id = $1 AND metadata @> '{"zoho_module":"CustomModule34"}'`, wsID).
		Scan(&count); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if count != 2 {
		t.Fatalf("issue count after re-sweep = %d, want 2 (no duplicates)", count)
	}
	if err := testPool.QueryRow(ctx, `
SELECT title, status, metadata::text FROM issue
WHERE workspace_id = $1 AND metadata @> '{"zoho_rec_id":"CustomModule34:101"}'`, wsID).
		Scan(&title, &status, &metadata); err != nil {
		t.Fatalf("reload issue 101: %v", err)
	}
	if title != "Fix card decline v2" || status != "in_progress" {
		t.Fatalf("issue 101 after update = title=%q status=%q, want v2 + in_progress", title, status)
	}
	if !strings.Contains(metadata, `"zoho_status_name": "Escalated"`) {
		t.Fatalf("zoho_status_name not refreshed: %s", metadata)
	}
}

func TestZohoDynOutboundMirror(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID, stub := setupZohoDynWorkspace(t, ctx, "handler-tests-zohodyn-out")

	if w := postZohoDynConfig(t, wsID, map[string]any{
		"module_api_name": "CustomModule34",
		"direction":       "both",
		"field_map":       map[string]string{"status": "Status"},
		"status_map": map[string]any{
			"in":  map[string]string{"Open": "todo"},
			"out": map[string]string{"done": "Closed"},
		},
	}); w.Code != http.StatusCreated {
		t.Fatalf("create config: %d %s", w.Code, w.Body.String())
	}

	ownerUUID := parseUUID(testUserID)
	linked, err := testHandler.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID: parseUUID(wsID), Title: "Linked to CRM", Status: "done", Priority: "none",
		CreatorType: "member", CreatorID: ownerUUID, AllowDuplicate: true,
		Metadata: map[string]any{zohoDynRecIDMetaKey: "CustomModule34:999"},
	}, service.IssueCreateOpts{ActorID: testUserID})
	if err != nil {
		t.Fatalf("create linked issue: %v", err)
	}
	linkedID := util.UUIDToString(linked.Issue.ID)

	// Gate off (default): nothing is pushed.
	if err := testHandler.mirrorIssueStatusToZohoDyn(ctx, linkedID); err != nil {
		t.Fatalf("mirror (gate off): %v", err)
	}
	if stub.putCount() != 0 {
		t.Fatalf("push happened with ZOHO_DYN_PUSH off")
	}

	t.Setenv("ZOHO_DYN_PUSH", "true")

	// Linked issue + direction both + mapped status -> one PUT with the
	// mapped picklist value.
	if err := testHandler.mirrorIssueStatusToZohoDyn(ctx, linkedID); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if stub.putCount() != 1 {
		t.Fatalf("put count = %d, want 1", stub.putCount())
	}
	data, _ := stub.lastPut()["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("push body shape: %+v", stub.lastPut())
	}
	rec, _ := data[0].(map[string]any)
	if rec["id"] != "999" || rec["Status"] != "Closed" {
		t.Fatalf("pushed record = %+v, want id=999 Status=Closed", rec)
	}

	// Non-linked issue is a no-op.
	plain, err := testHandler.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID: parseUUID(wsID), Title: "No CRM link", Status: "done", Priority: "none",
		CreatorType: "member", CreatorID: ownerUUID, AllowDuplicate: true,
	}, service.IssueCreateOpts{ActorID: testUserID})
	if err != nil {
		t.Fatalf("create plain issue: %v", err)
	}
	if err := testHandler.mirrorIssueStatusToZohoDyn(ctx, util.UUIDToString(plain.Issue.ID)); err != nil {
		t.Fatalf("mirror plain: %v", err)
	}
	if stub.putCount() != 1 {
		t.Fatalf("non-linked issue triggered a push")
	}

	// Unmapped status (status_map.out miss) is skipped, never guessed.
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'blocked' WHERE id = $1`, linkedID); err != nil {
		t.Fatalf("set unmapped status: %v", err)
	}
	if err := testHandler.mirrorIssueStatusToZohoDyn(ctx, linkedID); err != nil {
		t.Fatalf("mirror unmapped: %v", err)
	}
	if stub.putCount() != 1 {
		t.Fatalf("unmapped status was pushed")
	}

	// Direction 'in' config never pushes.
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'done' WHERE id = $1`, linkedID); err != nil {
		t.Fatalf("restore status: %v", err)
	}
	cfg := loadZohoDynConfig(t, ctx, wsID)
	if _, err := testHandler.Queries.UpdateZohoSyncConfig(ctx, db.UpdateZohoSyncConfigParams{
		ID:        cfg.ID,
		Direction: pgtype.Text{String: "in", Valid: true},
	}); err != nil {
		t.Fatalf("set direction in: %v", err)
	}
	if err := testHandler.mirrorIssueStatusToZohoDyn(ctx, linkedID); err != nil {
		t.Fatalf("mirror direction=in: %v", err)
	}
	if stub.putCount() != 1 {
		t.Fatalf("direction 'in' config pushed to zoho")
	}
}

func TestZohoDynConfigCRUDAuthAndValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID, _ := setupZohoDynWorkspace(t, ctx, "handler-tests-zohodyn-crud")

	// Module API name must be identifier-shaped (COQL injection guard).
	if w := postZohoDynConfig(t, wsID, map[string]any{"module_api_name": "Bad-Module!"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad module: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	// status_map values must be real Agora statuses.
	if w := postZohoDynConfig(t, wsID, map[string]any{
		"module_api_name": "CustomModule34",
		"status_map":      map[string]any{"in": map[string]string{"Open": "not_a_status"}},
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad status_map: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	// field_map keys must stay inside the Agora whitelist.
	if w := postZohoDynConfig(t, wsID, map[string]any{
		"module_api_name": "CustomModule34",
		"field_map":       map[string]string{"assignee_id": "Owner"},
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad field_map key: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w := postZohoDynConfig(t, wsID, map[string]any{
		"module_api_name": "CustomModule34",
		"field_map":       map[string]string{"title": "Subject"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created zohoSyncConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Same (workspace, channel, module) is unique.
	if w := postZohoDynConfig(t, wsID, map[string]any{"module_api_name": "CustomModule34"}); w.Code != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Create is audited with the module name only.
	var details string
	if err := testPool.QueryRow(ctx, `
SELECT details::text FROM activity_log
WHERE workspace_id = $1 AND action = 'zoho_sync_config_created'
ORDER BY created_at DESC LIMIT 1`, wsID).Scan(&details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if !strings.Contains(details, "CustomModule34") {
		t.Fatalf("audit details missing module: %s", details)
	}

	// List includes the config.
	lw := httptest.NewRecorder()
	lreq := newRequest("GET", "/api/workspaces/"+wsID+"/zoho/sync-configs", nil)
	lreq = withURLParam(lreq, "id", wsID)
	testHandler.ListZohoSyncConfigs(lw, lreq)
	if lw.Code != http.StatusOK || !strings.Contains(lw.Body.String(), created.ID) {
		t.Fatalf("list: %d %s", lw.Code, lw.Body.String())
	}

	// Plain member is forbidden.
	memberWs := createMcpTestWorkspace(t, ctx, "handler-tests-zohodyn-member", "member")
	if w := postZohoDynConfig(t, memberWs, map[string]any{"module_api_name": "Tasks"}); w.Code != http.StatusForbidden {
		t.Fatalf("member create: expected 403, got %d", w.Code)
	}

	// Agent actors are rejected even when the backing user is an owner.
	aw := httptest.NewRecorder()
	areq := newRequest("POST", "/api/workspaces/"+wsID+"/zoho/sync-configs", map[string]any{"module_api_name": "Tasks"})
	areq = withURLParam(areq, "id", wsID)
	areq.Header.Set("X-Actor-Source", "task_token")
	areq.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	testHandler.CreateZohoSyncConfig(aw, areq)
	if aw.Code != http.StatusForbidden {
		t.Fatalf("agent create: expected 403, got %d", aw.Code)
	}

	// Cross-workspace config ids are indistinguishable from missing (404).
	otherWs := createMcpTestWorkspace(t, ctx, "handler-tests-zohodyn-other", "owner")
	uw := httptest.NewRecorder()
	testHandler.UpdateZohoSyncConfig(uw, zohoDynConfigRequest("PUT", otherWs, created.ID, map[string]any{"enabled": false}))
	if uw.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace update: expected 404, got %d: %s", uw.Code, uw.Body.String())
	}
	dw := httptest.NewRecorder()
	testHandler.DeleteZohoSyncConfig(dw, zohoDynConfigRequest("DELETE", otherWs, created.ID, nil))
	if dw.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace delete: expected 404, got %d: %s", dw.Code, dw.Body.String())
	}

	// Partial update in the owning workspace works and round-trips.
	uw2 := httptest.NewRecorder()
	testHandler.UpdateZohoSyncConfig(uw2, zohoDynConfigRequest("PUT", wsID, created.ID, map[string]any{
		"enabled": false, "direction": "out",
	}))
	if uw2.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", uw2.Code, uw2.Body.String())
	}
	var updated zohoSyncConfigResponse
	if err := json.Unmarshal(uw2.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Enabled || updated.Direction != "out" {
		t.Fatalf("update result = enabled=%v direction=%q", updated.Enabled, updated.Direction)
	}
	if string(updated.FieldMap) == "" || !strings.Contains(string(updated.FieldMap), "Subject") {
		t.Fatalf("partial update clobbered field_map: %s", updated.FieldMap)
	}

	// Delete in the owning workspace -> 204, then the config is gone.
	dw2 := httptest.NewRecorder()
	testHandler.DeleteZohoSyncConfig(dw2, zohoDynConfigRequest("DELETE", wsID, created.ID, nil))
	if dw2.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", dw2.Code, dw2.Body.String())
	}
	dw3 := httptest.NewRecorder()
	testHandler.DeleteZohoSyncConfig(dw3, zohoDynConfigRequest("DELETE", wsID, created.ID, nil))
	if dw3.Code != http.StatusNotFound {
		t.Fatalf("second delete: expected 404, got %d", dw3.Code)
	}
}

// TestZohoDynSweepWatermark pins the failure-aware cursor ladder: clean
// sweeps persist the full watermark, a failed record caps the cursor just
// below itself so it is re-queried, and a poison record is abandoned after
// zohoDynMaxFailStreak consecutive capped sweeps.
func TestZohoDynSweepWatermark(t *testing.T) {
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	failed := base.Add(30 * time.Second)
	max := base.Add(2 * time.Minute)

	// Clean sweep: full watermark, streak reset.
	if p, s, a := zohoDynSweepWatermark(max, time.Time{}, 3); !p.Equal(max) || s != 0 || a {
		t.Fatalf("clean sweep: p=%v s=%d a=%v", p, s, a)
	}
	// Failure before watermark: cap just below the failed record, streak grows.
	p, s, a := zohoDynSweepWatermark(max, failed, 0)
	if a || s != 1 || !p.Equal(failed.Add(-time.Millisecond)) {
		t.Fatalf("capped sweep: p=%v s=%d a=%v", p, s, a)
	}
	// Failure after watermark already (failed > maxSeen impossible ordering):
	// maxSeen before earliestFailed → nothing to cap.
	if p, s, a := zohoDynSweepWatermark(failed.Add(-time.Minute), failed, 2); !p.Equal(failed.Add(-time.Minute)) || s != 0 || a {
		t.Fatalf("no-cap sweep: p=%v s=%d a=%v", p, s, a)
	}
	// Streak exhaustion: abandon, advance fully, reset.
	if p, s, a := zohoDynSweepWatermark(max, failed, zohoDynMaxFailStreak-1); !a || s != 0 || !p.Equal(max) {
		t.Fatalf("abandon sweep: p=%v s=%d a=%v", p, s, a)
	}
}
