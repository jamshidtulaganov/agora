package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Non-owner issue visibility gate.
//
// Feature: a workspace member who is NOT the workspace owner may only SEE
// issues that are "theirs" — assigned to them directly, to an agent they own,
// or to a squad they (or an agent they own) belong to or lead. Owners (and
// agent/daemon actors) see everything. These tests exercise every read surface
// so a partial gate — which would leak — fails loudly.
//
// testUserID is the workspace owner (role 'owner'); the fixture adds a second
// member with role 'member' who is the restricted subject.

type visFixture struct {
	restrictedID string // role 'member' — the restricted subject
	ownedAgentID string // agent.owner_id = restrictedID
	humanSquadID string // squad with restrictedID as a human squad_member
	agentSquadID string // squad whose leader_id is restrictedID's agent

	// Issues the restricted member SHOULD see (one per ownership branch).
	ownIssue     string // assignee member = restrictedID
	agentIssue   string // assignee agent = ownedAgentID
	humanSqIssue string // assignee squad = humanSquadID
	agentSqIssue string // assignee squad = agentSquadID (via leader)

	// Issues the restricted member must NOT see.
	ownerIssue      string // assignee member = testUserID (workspace owner)
	foreignAgent    string // an agent owned by testUserID (not the restricted member)
	foreignAgentIss string // assignee agent = foreignAgent
}

func setupVisFixture(t *testing.T) *visFixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	runtimeID := handlerTestRuntimeID(t)

	fx := &visFixture{}

	// Restricted member (role 'member').
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Vis Restricted", fmt.Sprintf("vis-restricted-%d@agora.dev", suffix)).Scan(&fx.restrictedID); err != nil {
		t.Fatalf("create restricted user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, fx.restrictedID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, fx.restrictedID); err != nil {
		t.Fatalf("add restricted member: %v", err)
	}

	// Agent owned by the restricted member.
	fx.ownedAgentID = insertAgent(t, ctx, testWorkspaceID, runtimeID, fx.restrictedID,
		fmt.Sprintf("Vis Owned Agent %d", suffix))
	// A foreign agent owned by the workspace owner (testUserID).
	fx.foreignAgent = insertAgent(t, ctx, testWorkspaceID, runtimeID, testUserID,
		fmt.Sprintf("Vis Foreign Agent %d", suffix))

	// Squad the restricted member is a human member of. Its leader is a foreign
	// agent (satisfies NOT NULL leader_id) — the restricted member's only tie is
	// the squad_member row, so it exercises the human-member branch in isolation.
	foreignLeader := insertAgent(t, ctx, testWorkspaceID, runtimeID, testUserID,
		fmt.Sprintf("Vis Foreign Leader %d", suffix))
	fx.humanSquadID = insertSquad(t, ctx, testWorkspaceID, foreignLeader,
		fmt.Sprintf("VisHumanSquad-%d", suffix))
	if _, err := testPool.Exec(ctx, `
		INSERT INTO squad_member (squad_id, member_type, member_id) VALUES ($1, 'member', $2)
	`, fx.humanSquadID, fx.restrictedID); err != nil {
		t.Fatalf("add restricted as squad member: %v", err)
	}

	// Squad led by the restricted member's agent (canonical leader_id path — no
	// squad_member copy row).
	fx.agentSquadID = insertSquad(t, ctx, testWorkspaceID, fx.ownedAgentID,
		fmt.Sprintf("VisAgentSquad-%d", suffix))

	// Issues in every bucket.
	fx.ownIssue = insertIssueTo(t, ctx, testWorkspaceID, fmt.Sprintf("vis own %d", suffix), "member", fx.restrictedID)
	fx.agentIssue = insertIssueTo(t, ctx, testWorkspaceID, fmt.Sprintf("vis agent %d", suffix), "agent", fx.ownedAgentID)
	fx.humanSqIssue = insertIssueTo(t, ctx, testWorkspaceID, fmt.Sprintf("vis humansq %d", suffix), "squad", fx.humanSquadID)
	fx.agentSqIssue = insertIssueTo(t, ctx, testWorkspaceID, fmt.Sprintf("vis agentsq %d", suffix), "squad", fx.agentSquadID)
	fx.ownerIssue = insertIssueTo(t, ctx, testWorkspaceID, fmt.Sprintf("vis owner %d", suffix), "member", testUserID)
	fx.foreignAgentIss = insertIssueTo(t, ctx, testWorkspaceID, fmt.Sprintf("vis foreign-agent %d", suffix), "agent", fx.foreignAgent)
	return fx
}

// mine returns the four issue ids the restricted member is entitled to see.
func (fx *visFixture) mine() []string {
	return []string{fx.ownIssue, fx.agentIssue, fx.humanSqIssue, fx.agentSqIssue}
}

// hidden returns the issue ids the restricted member must never see.
func (fx *visFixture) hidden() []string {
	return []string{fx.ownerIssue, fx.foreignAgentIss}
}

// --- request helpers ---

// issueIDsFrom runs a handler that emits {"issues": [...]} and returns the ids.
func issueIDsFrom(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Issues []IssueResponse `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode issues: %v", err)
	}
	ids := make([]string, 0, len(resp.Issues))
	for _, iss := range resp.Issues {
		ids = append(ids, iss.ID)
	}
	return ids
}

// assertVisibility asserts the restricted member's "mine" issues are all present
// and every "hidden" issue is absent.
func assertVisibility(t *testing.T, surface string, fx *visFixture, got []string) {
	t.Helper()
	for _, id := range fx.mine() {
		if !containsIssueID(got, id) {
			t.Errorf("%s: restricted member should see own issue %s but did not; got %v", surface, id, got)
		}
	}
	for _, id := range fx.hidden() {
		if containsIssueID(got, id) {
			t.Errorf("%s: LEAK — restricted member saw hidden issue %s; got %v", surface, id, got)
		}
	}
}

func listAs(t *testing.T, userID, query string) []string {
	t.Helper()
	path := fmt.Sprintf("/api/issues?workspace_id=%s&limit=500%s", testWorkspaceID, query)
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequestAs(userID, "GET", path, nil))
	return issueIDsFrom(t, w)
}

// --- surface 1: paged list ---

func TestIssueVisibility_ListIssues_RestrictedSeesOnlyOwn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)
	assertVisibility(t, "ListIssues(paged)", fx, listAs(t, fx.restrictedID, ""))
}

func TestIssueVisibility_ListIssues_OwnerSeesAll(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)
	got := listAs(t, testUserID, "")
	for _, id := range append(fx.mine(), fx.hidden()...) {
		if !containsIssueID(got, id) {
			t.Errorf("owner should see every issue but missed %s; got %v", id, got)
		}
	}
}

// --- surface 2: open_only path (ListOpenIssues, a distinct sqlc query) ---

func TestIssueVisibility_ListIssues_OpenOnly_RestrictedSeesOnlyOwn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)
	assertVisibility(t, "ListIssues(open_only)", fx, listAs(t, fx.restrictedID, "&open_only=true"))
}

// --- surface 3: grouped board (dynamic SQL, separate builder) ---

func TestIssueVisibility_ListGroupedIssues_RestrictedSeesOnlyOwn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)
	path := fmt.Sprintf("/api/issues/grouped?workspace_id=%s&group_by=assignee&statuses=todo&limit=500", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListGroupedIssues(w, newRequestAs(fx.restrictedID, "GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp GroupedIssuesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode grouped: %v", err)
	}
	got := []string{}
	for _, g := range resp.Groups {
		for _, iss := range g.Issues {
			got = append(got, iss.ID)
		}
	}
	assertVisibility(t, "ListGroupedIssues", fx, got)
}

// --- surface 4: search ---

func TestIssueVisibility_SearchIssues_RestrictedSeesOnlyOwn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)
	// All fixture issue titles share the token "vis"; search for it.
	path := fmt.Sprintf("/api/issues/search?workspace_id=%s&q=vis&limit=50", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.SearchIssues(w, newRequestAs(fx.restrictedID, "GET", path, nil))
	got := issueIDsFrom(t, w)
	// Search may not surface every own issue (ranking/limit), but it must NEVER
	// surface a hidden one.
	for _, id := range fx.hidden() {
		if containsIssueID(got, id) {
			t.Errorf("SearchIssues LEAK: restricted member saw hidden issue %s; got %v", id, got)
		}
	}
	// At least the direct-assignee issue should rank in.
	if !containsIssueID(got, fx.ownIssue) {
		t.Errorf("SearchIssues: restricted member should find own issue %s; got %v", fx.ownIssue, got)
	}
}

// --- surface 5: issue detail (GetIssue via the loadIssueForUser choke point) ---

func TestIssueVisibility_GetIssue_RestrictedGated(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)

	// Own issue → 200.
	w := httptest.NewRecorder()
	testHandler.GetIssue(w, withURLParam(newRequestAs(fx.restrictedID, "GET", "/api/issues/"+fx.ownIssue, nil), "id", fx.ownIssue))
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue(own) as restricted: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Foreign issue → 404 (existence not revealed).
	w = httptest.NewRecorder()
	testHandler.GetIssue(w, withURLParam(newRequestAs(fx.restrictedID, "GET", "/api/issues/"+fx.ownerIssue, nil), "id", fx.ownerIssue))
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetIssue(foreign) as restricted: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Owner sees the same foreign issue fine.
	w = httptest.NewRecorder()
	testHandler.GetIssue(w, withURLParam(newRequest("GET", "/api/issues/"+fx.ownerIssue, nil), "id", fx.ownerIssue))
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue(foreign) as owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// --- surface 6: sprint board (ListIssuesBySprint) ---

func TestIssueVisibility_ListSprintIssues_RestrictedSeesOnlyOwn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := setupVisFixture(t)

	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status) VALUES ($1, 'Vis Project', 'planned') RETURNING id`,
		testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	var sprintID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO sprint (workspace_id, project_id, name, status) VALUES ($1, $2, 'Vis Sprint', 'active') RETURNING id`,
		testWorkspaceID, projectID).Scan(&sprintID); err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM sprint WHERE id = $1`, sprintID) })

	// Attach one own issue and one hidden issue to the sprint.
	for _, iid := range []string{fx.ownIssue, fx.ownerIssue} {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO issue_to_sprint (issue_id, sprint_id) VALUES ($1::uuid, $2::uuid)`, iid, sprintID); err != nil {
			t.Fatalf("attach issue to sprint: %v", err)
		}
	}

	w := httptest.NewRecorder()
	testHandler.ListSprintIssues(w, withURLParam(newRequestAs(fx.restrictedID, "GET", "/api/sprints/"+sprintID+"/issues", nil), "id", sprintID))
	got := issueIDsFrom(t, w)
	if !containsIssueID(got, fx.ownIssue) {
		t.Errorf("sprint board: restricted member should see own sprint issue %s; got %v", fx.ownIssue, got)
	}
	if containsIssueID(got, fx.ownerIssue) {
		t.Errorf("sprint board LEAK: restricted member saw hidden sprint issue %s; got %v", fx.ownerIssue, got)
	}
}

// --- surface 7: batch children (ListChildrenByParents) ---

func TestIssueVisibility_ListChildrenByParents_RestrictedSeesOnlyOwn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := setupVisFixture(t)

	// A parent owned by the workspace owner, with one child assigned to the
	// restricted member and one to the owner. Batch-enumerating the parent must
	// only surface the restricted member's own child.
	parent := fx.ownerIssue
	ownChild := insertIssueTo(t, ctx, testWorkspaceID, "vis child mine", "member", fx.restrictedID)
	hiddenChild := insertIssueTo(t, ctx, testWorkspaceID, "vis child hidden", "member", testUserID)
	for _, c := range []string{ownChild, hiddenChild} {
		if _, err := testPool.Exec(ctx, `UPDATE issue SET parent_issue_id = $1 WHERE id = $2`, parent, c); err != nil {
			t.Fatalf("set parent: %v", err)
		}
	}

	path := fmt.Sprintf("/api/issues/children?workspace_id=%s&parent_ids=%s", testWorkspaceID, parent)
	w := httptest.NewRecorder()
	testHandler.ListChildrenByParents(w, newRequestAs(fx.restrictedID, "GET", path, nil))
	got := issueIDsFrom(t, w)
	if !containsIssueID(got, ownChild) {
		t.Errorf("children: restricted member should see own child %s; got %v", ownChild, got)
	}
	if containsIssueID(got, hiddenChild) {
		t.Errorf("children LEAK: restricted member saw hidden child %s; got %v", hiddenChild, got)
	}
}

// --- surface 8: agent / daemon actors are exempt (X-Actor-Source) ---

func TestIssueVisibility_AgentActorExempt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := setupVisFixture(t)

	// Same restricted user id, but the request is authenticated as a runtime
	// (cloud_pat). X-Actor-Source is server-set only, so its presence means a
	// non-human actor that must see the whole workspace.
	path := fmt.Sprintf("/api/issues?workspace_id=%s&limit=500", testWorkspaceID)
	req := newRequestAs(fx.restrictedID, "GET", path, nil)
	req.Header.Set("X-Actor-Source", "cloud_pat")
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, req)
	got := issueIDsFrom(t, w)
	for _, id := range append(fx.mine(), fx.hidden()...) {
		if !containsIssueID(got, id) {
			t.Errorf("agent actor should see every issue but missed %s; got %v", id, got)
		}
	}
}
