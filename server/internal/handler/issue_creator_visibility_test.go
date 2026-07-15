package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A member-created issue used to disappear immediately after POST when the
// member left it unassigned (or assigned it to somebody else's agent). The
// restricted visibility gate recognized assignees but not the human creator,
// so the creator received 404 on detail and could not find the issue in lists.
func TestIssueCreatorCanReadOwnUnassignedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	creatorID := createWorkspaceMemberUser(
		t,
		"Issue creator visibility",
		fmt.Sprintf("issue-creator-%d@agora.dev", time.Now().UnixNano()),
	)
	unrelatedID := createWorkspaceMemberUser(
		t,
		"Unrelated issue member",
		fmt.Sprintf("issue-unrelated-%d@agora.dev", time.Now().UnixNano()),
	)

	w := httptest.NewRecorder()
	req := newRequestAs(creatorID, http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Creator-visible unassigned issue",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}

	w = httptest.NewRecorder()
	req = withURLParam(
		newRequestAs(creatorID, http.MethodGet, "/api/issues/"+created.ID, nil),
		"id",
		created.ID,
	)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("creator get issue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequestAs(creatorID, http.MethodGet, "/api/issues?workspace_id="+testWorkspaceID, nil)
	testHandler.ListIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("creator list issues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), created.ID) {
		t.Fatalf("creator list did not contain created issue %s: %s", created.ID, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withURLParam(
		newRequestAs(unrelatedID, http.MethodGet, "/api/issues/"+created.ID, nil),
		"id",
		created.ID,
	)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unrelated member get issue: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
