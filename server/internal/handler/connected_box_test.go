package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestConnectedBoxFeatureFlag pins the additive/opt-in contract: with the flag
// OFF the endpoints fail closed (404), so the Remote Boxes feature is inert for
// any deployment that hasn't enabled it.
func TestConnectedBoxFeatureFlag(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "") // explicitly off
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes?workspace_id="+testWorkspaceID, map[string]any{
		"label": "jamshid", "ssh_host": "jamshid.sdteam.uz", "ssh_user": "dev",
	})
	testHandler.CreateConnectedBox(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("flag off: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestConnectedBoxCRUD covers create → list → delete with the flag on, plus the
// ssh_port default and required-field validation.
func TestConnectedBoxCRUD(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")

	// --- create ---
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes?workspace_id="+testWorkspaceID, map[string]any{
		"label": "jamshid", "ssh_host": "jamshid.sdteam.uz", "ssh_user": "dev",
	})
	testHandler.CreateConnectedBox(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var box ConnectedBoxResponse
	if err := json.NewDecoder(w.Body).Decode(&box); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM connected_box WHERE id = $1`, box.ID)
	})
	if box.SSHPort != 22 {
		t.Errorf("ssh_port should default to 22, got %d", box.SSHPort)
	}
	if box.Status != "pending" {
		t.Errorf("new box status should be pending, got %q", box.Status)
	}
	if box.OwnerID == nil || *box.OwnerID != testUserID {
		t.Errorf("owner should be the caller %q, got %v", testUserID, box.OwnerID)
	}

	// --- list contains it ---
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/remote-boxes?workspace_id="+testWorkspaceID, nil)
	testHandler.ListConnectedBoxes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Boxes []ConnectedBoxResponse `json:"boxes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, b := range listResp.Boxes {
		if b.ID == box.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created box not present in list")
	}

	// --- delete ---
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/remote-boxes/"+box.ID+"?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "id", box.ID)
	testHandler.DeleteConnectedBox(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// --- gone from list ---
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/remote-boxes?workspace_id="+testWorkspaceID, nil)
	testHandler.ListConnectedBoxes(w, req)
	_ = json.NewDecoder(w.Body).Decode(&listResp)
	for _, b := range listResp.Boxes {
		if b.ID == box.ID {
			t.Fatal("box should be gone after delete")
		}
	}
}

// TestConnectedBoxValidation covers required-field rejection.
func TestConnectedBoxValidation(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes?workspace_id="+testWorkspaceID, map[string]any{
		"label": "  ", "ssh_host": "", "ssh_user": "dev",
	})
	testHandler.CreateConnectedBox(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("blank required fields: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestConnectedBoxTenancy proves the WHERE workspace_id scoping at the query
// layer: a box created in one workspace is invisible to another (Get/Delete with
// a foreign workspace id never matches), so no cross-tenant leak is possible.
func TestConnectedBoxTenancy(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	ctx := context.Background()
	box, err := testHandler.Queries.CreateConnectedBox(ctx, db.CreateConnectedBoxParams{
		WorkspaceID:  testUUID(testWorkspaceID),
		OwnerID:      testUUID(testUserID),
		Label:        "qa",
		SshHost:      "qa.sdteam.uz",
		SshUser:      "qa",
		SshPort:      22,
		DeployPubkey: "",
	})
	if err != nil {
		t.Fatalf("seed box: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM connected_box WHERE id = $1`, box.ID)
	})

	foreignWs := testUUID("99999999-9999-9999-9999-999999999999")
	if _, err := testHandler.Queries.GetConnectedBox(ctx, db.GetConnectedBoxParams{
		ID:          box.ID,
		WorkspaceID: foreignWs,
	}); err == nil {
		t.Error("Get with a foreign workspace must NOT find the box")
	}

	// Delete scoped to a foreign workspace is a no-op; the row survives.
	_ = testHandler.Queries.DeleteConnectedBox(ctx, db.DeleteConnectedBoxParams{
		ID:          box.ID,
		WorkspaceID: foreignWs,
	})
	if _, err := testHandler.Queries.GetConnectedBox(ctx, db.GetConnectedBoxParams{
		ID:          box.ID,
		WorkspaceID: testUUID(testWorkspaceID),
	}); err != nil {
		t.Errorf("box must survive a foreign-workspace delete, got err: %v", err)
	}
}
