package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Knowledge-item review API tests — the human half of the KB flywheel:
// list / create / approve / edit / retire / delete. The mutating routes are
// RequireHumanActor-gated at the router (see cmd/server/router.go), so the
// agent-403 sub-cases drive the middleware exactly as the router wires it
// (the handler methods themselves do no actor check). All tests run serially
// (no t.Parallel) against the shared handler_test.go fixture.
//
// kbTestProject and kbInsertItem live here because they are the shared seeds
// for every KB handler test (knowledge_synth_test.go, skill_kb_test.go).

// kbTestProject inserts a project in the shared workspace and returns its id.
// status is 'planned' to satisfy the project.status check constraint. The
// title is used verbatim so ProjectKBSkillName derives a deterministic
// "<slug>-kb" (callers that need a specific slug pass a slug-safe title).
func kbTestProject(t *testing.T, title string) string {
	t.Helper()
	ctx := context.Background()
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status)
		VALUES ($1, $2, 'planned')
		RETURNING id
	`, testWorkspaceID, title).Scan(&projectID); err != nil {
		t.Fatalf("insert test project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM knowledge_item WHERE project_id = $1`, projectID)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, projectID)
	})
	return projectID
}

// kbInsertItem inserts one knowledge_item row directly and returns its id.
// It bypasses the ingest/dedupe path so tests can seed an exact starting state.
func kbInsertItem(t *testing.T, projectID, kbName, kind, title, normTitle, status string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO knowledge_item (
			workspace_id, project_id, kb_name, module, kind, title, body,
			norm_title, created_by_type, status
		)
		VALUES ($1, $2, $3, '', $4, $5, '', $6, 'member', $7)
		RETURNING id
	`, testWorkspaceID, projectID, kbName, kind, title, normTitle, status).Scan(&id); err != nil {
		t.Fatalf("insert knowledge item: %v", err)
	}
	return id
}

func kbGetItemStatus(t *testing.T, itemID string) (string, bool) {
	t.Helper()
	var status string
	err := testPool.QueryRow(context.Background(),
		`SELECT status FROM knowledge_item WHERE id = $1`, itemID).Scan(&status)
	if err != nil {
		return "", false
	}
	return status, true
}

func TestListKnowledgeItems(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbName := fmt.Sprintf("kb-list-%d", time.Now().UnixNano())
	projectID := kbTestProject(t, fmt.Sprintf("KB List Project %d", time.Now().UnixNano()))
	activeID := kbInsertItem(t, projectID, kbName, "gotcha", "Active fact", "active fact", "active")
	proposedID := kbInsertItem(t, projectID, kbName, "gotcha", "Proposed fact", "proposed fact", "proposed")
	archivedID := kbInsertItem(t, projectID, kbName, "gotcha", "Archived fact", "archived fact", "archived")

	list := func(query string) []knowledgeItemResponse {
		w := httptest.NewRecorder()
		req := newRequest(http.MethodGet, "/api/projects/"+projectID+"/knowledge/items"+query, nil)
		req = withURLParam(req, "id", projectID)
		testHandler.ListKnowledgeItems(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListKnowledgeItems%s: expected 200, got %d: %s", query, w.Code, w.Body.String())
		}
		var got []knowledgeItemResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		return got
	}

	ids := func(rows []knowledgeItemResponse) map[string]bool {
		m := make(map[string]bool, len(rows))
		for _, r := range rows {
			m[r.ID] = true
		}
		return m
	}

	// No filter → archived excluded, active + proposed present.
	got := ids(list(""))
	if !got[activeID] || !got[proposedID] {
		t.Fatalf("default list must include active and proposed items; got %v", got)
	}
	if got[archivedID] {
		t.Fatalf("default list must exclude archived items; got %v", got)
	}

	// ?status=archived → only the archived one.
	got = ids(list("?status=archived"))
	if !got[archivedID] || got[activeID] || got[proposedID] {
		t.Fatalf("status=archived filter wrong; got %v", got)
	}

	// ?status=proposed → only the proposed one.
	got = ids(list("?status=proposed"))
	if !got[proposedID] || got[activeID] || got[archivedID] {
		t.Fatalf("status=proposed filter wrong; got %v", got)
	}

	// Bogus status → 400.
	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/projects/"+projectID+"/knowledge/items?status=bogus", nil)
	req = withURLParam(req, "id", projectID)
	testHandler.ListKnowledgeItems(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=bogus: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Cross-workspace project id → 404 (GetProject is not workspace-scoped).
	otherWS := kbSeedForeignWorkspace(t)
	foreignProject := kbSeedProjectInWorkspace(t, otherWS)
	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/projects/"+foreignProject+"/knowledge/items", nil)
	req = withURLParam(req, "id", foreignProject)
	testHandler.ListKnowledgeItems(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace project: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateKnowledgeItem(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	projectID := kbTestProject(t, fmt.Sprintf("KB Create Project %d", time.Now().UnixNano()))

	// Member happy path: created immediately active, and the KB recompiles
	// (no skill row exists, so recompile is a harmless no-op — assert the row).
	w := httptest.NewRecorder()
	body := createKnowledgeItemRequest{Kind: "gotcha", Title: "Created fact", Body: "some detail"}
	req := newRequest(http.MethodPost, "/api/projects/"+projectID+"/knowledge/items", body)
	req = withURLParam(req, "id", projectID)
	testHandler.CreateKnowledgeItem(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateKnowledgeItem: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created knowledgeItemResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("human-created item must be active, got %q", created.Status)
	}
	if created.CreatedByType != "member" {
		t.Fatalf("created_by_type must be member, got %q", created.CreatedByType)
	}

	// Agent actor → 403 via RequireHumanActor (the router gate). The handler
	// itself does no actor check, so drive the middleware exactly as wired.
	gated := RequireHumanActor(http.HandlerFunc(testHandler.CreateKnowledgeItem))
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+projectID+"/knowledge/items",
		createKnowledgeItemRequest{Kind: "gotcha", Title: "Agent fact"})
	req.Header.Set("X-Actor-Source", "task_token")
	req = withURLParam(req, "id", projectID)
	gated.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent create: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Malformed JSON → 400.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/knowledge/items",
		strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", projectID)
	testHandler.CreateKnowledgeItem(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Missing title → 400.
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+projectID+"/knowledge/items",
		createKnowledgeItemRequest{Kind: "gotcha", Title: "   "})
	req = withURLParam(req, "id", projectID)
	testHandler.CreateKnowledgeItem(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing title: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateKnowledgeItemApprove(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbName := fmt.Sprintf("kb-approve-%d", time.Now().UnixNano())
	projectID := kbTestProject(t, fmt.Sprintf("KB Approve Project %d", time.Now().UnixNano()))

	// Approve a proposed item → active.
	proposedID := kbInsertItem(t, projectID, kbName, "convention", "Proposed to approve", "proposed to approve", "proposed")
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPatch, "/api/knowledge-items/"+proposedID, updateKnowledgeItemRequest{Status: strptr("active")})
	req = withURLParam(req, "itemId", proposedID)
	testHandler.UpdateKnowledgeItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if status, _ := kbGetItemStatus(t, proposedID); status != "active" {
		t.Fatalf("approved item must be active, got %q", status)
	}

	// Invalid status value → 400.
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPatch, "/api/knowledge-items/"+proposedID, updateKnowledgeItemRequest{Status: strptr("bogus")})
	req = withURLParam(req, "itemId", proposedID)
	testHandler.UpdateKnowledgeItem(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid status: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Activation collision: a live twin with the same norm_title already exists,
	// so PATCHing a proposed item's title+status to collide trips the partial
	// unique index → 409. Seed the live twin under a distinct title, then a
	// proposed row, then PATCH the proposed row to collide.
	const clashNorm = "activation clash fact"
	kbInsertItem(t, projectID, kbName, "gotcha", "Activation clash fact", clashNorm, "active")
	twinProposed := kbInsertItem(t, projectID, kbName, "gotcha", "Distinct proposed title", "distinct proposed title", "proposed")
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPatch, "/api/knowledge-items/"+twinProposed, updateKnowledgeItemRequest{
		Status: strptr("active"),
		Title:  strptr("Activation clash fact"),
	})
	req = withURLParam(req, "itemId", twinProposed)
	testHandler.UpdateKnowledgeItem(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("activation collision: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Agent actor → 403 via RequireHumanActor.
	gated := RequireHumanActor(http.HandlerFunc(testHandler.UpdateKnowledgeItem))
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPatch, "/api/knowledge-items/"+proposedID, updateKnowledgeItemRequest{Status: strptr("archived")})
	req.Header.Set("X-Actor-Source", "task_token")
	req = withURLParam(req, "itemId", proposedID)
	gated.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent update: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Cross-workspace item id → 404 (GetKnowledgeItem is workspace-scoped).
	otherWS := kbSeedForeignWorkspace(t)
	foreignProject := kbSeedProjectInWorkspace(t, otherWS)
	foreignItem := kbInsertItemInWorkspace(t, otherWS, foreignProject, kbName, "gotcha", "Foreign", "foreign", "proposed")
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPatch, "/api/knowledge-items/"+foreignItem, updateKnowledgeItemRequest{Status: strptr("active")})
	req = withURLParam(req, "itemId", foreignItem)
	testHandler.UpdateKnowledgeItem(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace item: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteKnowledgeItem(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbName := fmt.Sprintf("kb-delete-%d", time.Now().UnixNano())
	projectID := kbTestProject(t, fmt.Sprintf("KB Delete Project %d", time.Now().UnixNano()))
	itemID := kbInsertItem(t, projectID, kbName, "gotcha", "Deletable fact", "deletable fact", "active")

	// Agent actor → 403 via RequireHumanActor.
	gated := RequireHumanActor(http.HandlerFunc(testHandler.DeleteKnowledgeItem))
	w := httptest.NewRecorder()
	req := newRequest(http.MethodDelete, "/api/knowledge-items/"+itemID, nil)
	req.Header.Set("X-Actor-Source", "task_token")
	req = withURLParam(req, "itemId", itemID)
	gated.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent delete: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := kbGetItemStatus(t, itemID); !ok {
		t.Fatalf("agent delete must not remove the row")
	}

	// Member delete → 204, then the row is gone.
	w = httptest.NewRecorder()
	req = newRequest(http.MethodDelete, "/api/knowledge-items/"+itemID, nil)
	req = withURLParam(req, "itemId", itemID)
	testHandler.DeleteKnowledgeItem(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("member delete: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := kbGetItemStatus(t, itemID); ok {
		t.Fatalf("member delete must remove the row")
	}

	// Second delete of the now-gone row → 404.
	w = httptest.NewRecorder()
	req = newRequest(http.MethodDelete, "/api/knowledge-items/"+itemID, nil)
	req = withURLParam(req, "itemId", itemID)
	testHandler.DeleteKnowledgeItem(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete of gone row: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// kbSeedForeignWorkspace inserts a throwaway workspace and returns its id, for
// the cross-workspace 404 sub-cases. Cleaned up (cascades to its projects/items).
func kbSeedForeignWorkspace(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	var wsID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ($1, $2)
		RETURNING id
	`, "KB Foreign WS", fmt.Sprintf("kb-foreign-%d", time.Now().UnixNano())).Scan(&wsID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, wsID)
	})
	return wsID
}

func kbSeedProjectInWorkspace(t *testing.T, wsID string) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project (workspace_id, title, status)
		VALUES ($1, $2, 'planned')
		RETURNING id
	`, wsID, fmt.Sprintf("Foreign Project %d", time.Now().UnixNano())).Scan(&projectID); err != nil {
		t.Fatalf("insert foreign project: %v", err)
	}
	return projectID
}

func kbInsertItemInWorkspace(t *testing.T, wsID, projectID, kbName, kind, title, normTitle, status string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO knowledge_item (
			workspace_id, project_id, kb_name, module, kind, title, body,
			norm_title, created_by_type, status
		)
		VALUES ($1, $2, $3, '', $4, $5, '', $6, 'member', $7)
		RETURNING id
	`, wsID, projectID, kbName, kind, title, normTitle, status).Scan(&id); err != nil {
		t.Fatalf("insert foreign knowledge item: %v", err)
	}
	return id
}

// Assert the response shape stays in sync with the db row (compile-time guard
// against a silently dropped field in knowledgeItemToResponse).
var _ = knowledgeItemToResponse(db.KnowledgeItem{})
