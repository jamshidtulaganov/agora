package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestRecordDeployEvent covers the write path DeployIssueQA calls on every
// sync attempt: a success writes status="success", a failed sync writes
// status="failed", and the latest row wins on read regardless of insert
// order (append-only, same discipline as qa_evidence).
func TestRecordDeployEvent(t *testing.T) {
	ctx := context.Background()
	issueID := createTestIssue(t, "deploy event write path", "in_progress", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	wsUUID := testUUID(testWorkspaceID)
	issueUUID := testUUID(issueID)

	testHandler.recordDeployEvent(ctx, wsUUID, issueUUID, "feature/foo", "jamshid's box", true, "Switched to a new branch")

	latest, err := testHandler.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
		IssueID:     issueUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("get latest deploy event: %v", err)
	}
	if latest.Status != "success" || latest.Ref != "feature/foo" || latest.Target != "jamshid's box" {
		t.Errorf("unexpected row: %+v", latest)
	}

	// A second, failed attempt on the same issue must write a NEW row (no
	// upsert) and become the freshest one read back.
	testHandler.recordDeployEvent(ctx, wsUUID, issueUUID, "feature/foo", "jamshid's box", false, "ssh: connection refused")

	latest2, err := testHandler.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
		IssueID:     issueUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("get latest deploy event after 2nd write: %v", err)
	}
	if latest2.Status != "failed" || latest2.ID == latest.ID {
		t.Errorf("expected a NEW failed row to be latest, got %+v (first was %s)", latest2, uuidToString(latest.ID))
	}

	recent, err := testHandler.Queries.ListDeployEventsForIssue(ctx, db.ListDeployEventsForIssueParams{
		IssueID:     issueUUID,
		WorkspaceID: wsUUID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("list deploy events: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("expected 2 recorded deploy events, got %d", len(recent))
	}
}

// TestRecordDeployEventTruncatesSummary: the box's raw SSH output can be long
// (a full git fetch/checkout transcript) — the summary column is a display
// convenience, not the audit trail, so it's capped.
func TestRecordDeployEventTruncatesSummary(t *testing.T) {
	ctx := context.Background()
	issueID := createTestIssue(t, "deploy event long output", "in_progress", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	testHandler.recordDeployEvent(ctx, testUUID(testWorkspaceID), testUUID(issueID), "main", "box-1", true, string(long))

	latest, err := testHandler.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
		IssueID:     testUUID(issueID),
		WorkspaceID: testUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("get latest deploy event: %v", err)
	}
	if len(latest.Summary) > 500 {
		t.Errorf("expected summary to be capped at 500 bytes, got %d", len(latest.Summary))
	}
}

// TestGetIssueDeployEvents_Empty: an issue that has never been deployed
// returns a normal 200 with a null latest and an empty recent list — not an
// error (mirrors GetIssueQAEvidence's "null is a normal response" contract).
func TestGetIssueDeployEvents_Empty(t *testing.T) {
	issueID := createTestIssue(t, "deploy event empty read", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+issueID+"/deploy-events", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssueDeployEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp IssueDeployEventsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Latest != nil {
		t.Errorf("expected nil latest for a never-deployed issue, got %+v", resp.Latest)
	}
	if len(resp.Recent) != 0 {
		t.Errorf("expected empty recent list, got %+v", resp.Recent)
	}
}

// TestGetIssueDeployEvents_ReadPath: the handler's read side after write —
// latest reflects the freshest row, recent carries the full short history in
// descending recency order.
func TestGetIssueDeployEvents_ReadPath(t *testing.T) {
	ctx := context.Background()
	issueID := createTestIssue(t, "deploy event read path", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	wsUUID := testUUID(testWorkspaceID)
	issueUUID := testUUID(issueID)

	testHandler.recordDeployEvent(ctx, wsUUID, issueUUID, "feature/a", "qa-box", true, "first sync")
	testHandler.recordDeployEvent(ctx, wsUUID, issueUUID, "feature/a", "qa-box", true, "second sync, same branch")

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+issueID+"/deploy-events", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssueDeployEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp IssueDeployEventsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Latest == nil || resp.Latest.Summary != "second sync, same branch" {
		t.Fatalf("expected latest to be the most recent write, got %+v", resp.Latest)
	}
	if resp.Latest.Status != "success" || resp.Latest.Ref != "feature/a" {
		t.Errorf("unexpected latest fields: %+v", resp.Latest)
	}
	if len(resp.Recent) != 2 {
		t.Fatalf("expected 2 recent rows, got %d: %+v", len(resp.Recent), resp.Recent)
	}
	if resp.Recent[0].Summary != "second sync, same branch" {
		t.Errorf("expected recent[0] to be the freshest row, got %+v", resp.Recent[0])
	}
}

// TestGetIssueDeployEvents_NotFound: an unknown issue id 404s like every
// other issue-scoped read (loadIssueForUser's membership/existence check).
func TestGetIssueDeployEvents_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	fakeID := "00000000-0000-0000-0000-000000000000"
	req := newRequest("GET", "/api/issues/"+fakeID+"/deploy-events", nil)
	req = withURLParam(req, "id", fakeID)
	testHandler.GetIssueDeployEvents(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown issue, got %d: %s", w.Code, w.Body.String())
	}
}
