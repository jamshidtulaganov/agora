package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func squadRequest(t *testing.T, method, squadID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, "/api/squads/"+squadID, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("workspaceId", testWorkspaceID)
	rctx.URLParams.Add("id", squadID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func createSquadFixture(t *testing.T, leaderID string) string {
	t.Helper()
	var squadID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4)
		RETURNING id
	`, testWorkspaceID, "transaction-test-squad", leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID)
	})
	return squadID
}

func addSquadMemberFixture(t *testing.T, squadID, agentID, role string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO squad_member (squad_id, member_type, member_id, role)
		VALUES ($1, 'agent', $2, $3)
	`, squadID, agentID, role); err != nil {
		t.Fatalf("add squad member: %v", err)
	}
}

func TestUpdateSquadLeaderNormalizesMemberRoles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	oldLeader := createHandlerTestAgent(t, "Squad Role Old Leader", nil)
	newLeader := createHandlerTestAgent(t, "Squad Role New Leader", nil)
	staleLeader := createHandlerTestAgent(t, "Squad Role Stale Leader", nil)
	squadID := createSquadFixture(t, oldLeader)
	addSquadMemberFixture(t, squadID, oldLeader, "leader")
	addSquadMemberFixture(t, squadID, newLeader, "backend")
	addSquadMemberFixture(t, squadID, staleLeader, "leader")

	w := httptest.NewRecorder()
	testHandler.UpdateSquad(w, squadRequest(t, http.MethodPut, squadID, map[string]any{
		"leader_id": newLeader,
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateSquad: got %d: %s", w.Code, w.Body.String())
	}

	var persistedLeader string
	if err := testPool.QueryRow(context.Background(), `SELECT leader_id FROM squad WHERE id = $1`, squadID).Scan(&persistedLeader); err != nil {
		t.Fatal(err)
	}
	if persistedLeader != newLeader {
		t.Fatalf("leader_id = %s, want %s", persistedLeader, newLeader)
	}

	roles := map[string]string{}
	rows, err := testPool.Query(context.Background(), `
		SELECT member_id::text, role FROM squad_member WHERE squad_id = $1
	`, squadID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var memberID, role string
		if err := rows.Scan(&memberID, &role); err != nil {
			t.Fatal(err)
		}
		roles[memberID] = role
	}
	if roles[newLeader] != "leader" {
		t.Fatalf("new leader role = %q, want leader", roles[newLeader])
	}
	if roles[oldLeader] != "member" || roles[staleLeader] != "member" {
		t.Fatalf("stale leader roles were not cleared: old=%q stale=%q", roles[oldLeader], roles[staleLeader])
	}

	for name, body := range map[string]map[string]any{
		"non-leader cannot claim leader role": {
			"member_type": "agent", "member_id": oldLeader, "role": "leader",
		},
		"canonical leader cannot drop leader role": {
			"member_type": "agent", "member_id": newLeader, "role": "backend",
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler.UpdateSquadMemberRole(w, squadRequest(t, http.MethodPatch, squadID, body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestUpdateSquadModelRoutingMode(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	leaderID := createHandlerTestAgent(t, "Squad Routing Leader", nil)
	squadID := createSquadFixture(t, leaderID)

	w := httptest.NewRecorder()
	testHandler.UpdateSquad(w, squadRequest(t, http.MethodPut, squadID, map[string]any{
		"model_routing_mode": "balanced",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateSquad: got %d: %s", w.Code, w.Body.String())
	}
	var persisted string
	if err := testPool.QueryRow(context.Background(), `SELECT model_routing_mode FROM squad WHERE id = $1`, squadID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != modelRoutingBalanced {
		t.Fatalf("model_routing_mode = %q, want %q", persisted, modelRoutingBalanced)
	}

	w = httptest.NewRecorder()
	testHandler.UpdateSquad(w, squadRequest(t, http.MethodPut, squadID, map[string]any{
		"model_routing_mode": "auto",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid model_routing_mode: got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteSquadTransfersReferencesBeforeArchive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	leaderID := createHandlerTestAgent(t, "Squad Archive Leader", nil)
	squadID := createSquadFixture(t, leaderID)
	addSquadMemberFixture(t, squadID, leaderID, "leader")

	ctx := context.Background()
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title, assignee_type, assignee_id)
		VALUES ($1, 'member', $2, 'squad archive issue', 'squad', $3)
		RETURNING id
	`, testWorkspaceID, testUserID, squadID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	var autopilotID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (
			workspace_id, title, assignee_type, assignee_id,
			execution_mode, created_by_type, created_by_id, status
		)
		VALUES ($1, 'squad archive autopilot', 'squad', $2, 'create_issue', 'member', $3, 'active')
		RETURNING id
	`, testWorkspaceID, squadID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("create autopilot: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, autopilotID) })

	w := httptest.NewRecorder()
	testHandler.DeleteSquad(w, squadRequest(t, http.MethodDelete, squadID, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteSquad: got %d: %s", w.Code, w.Body.String())
	}

	var archived bool
	if err := testPool.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM squad WHERE id = $1`, squadID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Fatal("squad was not archived")
	}
	for name, query := range map[string]string{
		"issue":     `SELECT assignee_type, assignee_id::text FROM issue WHERE id = $1`,
		"autopilot": `SELECT assignee_type, assignee_id::text FROM autopilot WHERE id = $1`,
	} {
		id := issueID
		if name == "autopilot" {
			id = autopilotID
		}
		var assigneeType, assigneeID string
		if err := testPool.QueryRow(ctx, query, id).Scan(&assigneeType, &assigneeID); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if assigneeType != "agent" || assigneeID != leaderID {
			t.Fatalf("%s assignee = %s/%s, want agent/%s", name, assigneeType, assigneeID, leaderID)
		}
	}
}
