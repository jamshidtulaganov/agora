package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// insertTestDeployEvent seeds a deploy_event row directly through the query the
// live capture path uses (service.CaptureDeployEvent → InsertDeployEvent). The
// handler-side writer recordDeployEvent was removed with the connected-box
// code; the append-only storage and read path these tests exercise
// (GetIssueDeployEvents) are the live deploy/stage-cockpit surface.
func insertTestDeployEvent(t *testing.T, ctx context.Context, ws, issue pgtype.UUID, ref, target, status, summary string) {
	t.Helper()
	if _, err := testHandler.Queries.InsertDeployEvent(ctx, db.InsertDeployEventParams{
		WorkspaceID: ws,
		IssueID:     issue,
		Ref:         ref,
		Target:      target,
		Status:      status,
		Summary:     summary,
	}); err != nil {
		t.Fatalf("seed deploy event: %v", err)
	}
}

// TestDeployEventAppendOnly covers the deploy_event storage contract the live
// read path depends on: each write is a NEW append-only row (no upsert), status
// is persisted verbatim, and the latest row wins on read regardless of insert
// order (same discipline as qa_evidence).
func TestDeployEventAppendOnly(t *testing.T) {
	ctx := context.Background()
	issueID := createTestIssue(t, "deploy event write path", "in_progress", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	wsUUID := testUUID(testWorkspaceID)
	issueUUID := testUUID(issueID)

	insertTestDeployEvent(t, ctx, wsUUID, issueUUID, "feature/foo", "staging", "success", "Switched to a new branch")

	latest, err := testHandler.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
		IssueID:     issueUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("get latest deploy event: %v", err)
	}
	if latest.Status != "success" || latest.Ref != "feature/foo" || latest.Target != "staging" {
		t.Errorf("unexpected row: %+v", latest)
	}

	// A second, failed attempt on the same issue must write a NEW row (no
	// upsert) and become the freshest one read back.
	insertTestDeployEvent(t, ctx, wsUUID, issueUUID, "feature/foo", "staging", "failed", "pipeline failed")

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

	insertTestDeployEvent(t, ctx, wsUUID, issueUUID, "feature/a", "staging", "success", "first sync")
	insertTestDeployEvent(t, ctx, wsUUID, issueUUID, "feature/a", "staging", "success", "second sync, same branch")

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
