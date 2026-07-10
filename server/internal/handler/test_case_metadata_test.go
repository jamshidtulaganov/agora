package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Phase-2 test-case metadata (preconditions / priority / modality — migration
// 155). Create is fail-open: unknown priority/modality NORMALIZE to the
// defaults (p2 / "") so an agent emitting a drifted enum never loses the case.
// Update is fail-closed: an explicit human edit sending garbage is a client
// bug and gets a 400, mirroring the existing kind/category validation.

func TestCreateIssueTestCaseMetadata(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	issueID := createTestIssue(t, "tc metadata create", "in_progress", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	post := func(body map[string]any) (int, TestCaseResponse) {
		req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/test-cases", body), "id", issueID)
		rec := httptest.NewRecorder()
		testHandler.CreateIssueTestCase(rec, req)
		var resp TestCaseResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		return rec.Code, resp
	}

	// Valid metadata persists; priority/modality are case-normalized.
	code, resp := post(map[string]any{
		"title":         "login works",
		"steps":         "1. open login → expects: form renders",
		"expected":      "user lands on dashboard",
		"kind":          "manual",
		"category":      "positive",
		"preconditions": "A seeded admin account exists",
		"priority":      "P1",
		"modality":      "UI",
	})
	if code != http.StatusCreated {
		t.Fatalf("create with metadata: got %d", code)
	}
	if resp.Preconditions != "A seeded admin account exists" || resp.Priority != "p1" || resp.Modality != "ui" {
		t.Errorf("metadata not persisted/normalized: %+v", resp)
	}

	// Round-trip through the DB row too, not just the create response.
	row, err := testHandler.Queries.GetTestCase(context.Background(), db.GetTestCaseParams{
		ID: testUUID(resp.ID), WorkspaceID: testUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("get created case: %v", err)
	}
	if row.Preconditions != "A seeded admin account exists" || row.Priority != "p1" || row.Modality != "ui" {
		t.Errorf("DB row missing metadata: preconditions=%q priority=%q modality=%q", row.Preconditions, row.Priority, row.Modality)
	}

	// Garbage priority/modality on CREATE normalizes (fail-open), never 4xx.
	code, resp = post(map[string]any{
		"title":    "garbage enums downgrade",
		"kind":     "manual",
		"priority": "urgent",
		"modality": "telepathy",
	})
	if code != http.StatusCreated {
		t.Fatalf("create with garbage enums should still 201, got %d", code)
	}
	if resp.Priority != "p2" || resp.Modality != "" {
		t.Errorf("garbage enums must normalize to p2/\"\": priority=%q modality=%q", resp.Priority, resp.Modality)
	}

	// Omitted fields get the defaults.
	code, resp = post(map[string]any{"title": "defaults", "kind": "manual"})
	if code != http.StatusCreated || resp.Priority != "p2" || resp.Modality != "" || resp.Preconditions != "" {
		t.Errorf("defaults: code=%d priority=%q modality=%q preconditions=%q", code, resp.Priority, resp.Modality, resp.Preconditions)
	}
}

func TestUpdateTestCaseMetadata(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	issueID := createTestIssue(t, "tc metadata update", "in_progress", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	created, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: testUUID(testWorkspaceID),
		IssueID:     testUUID(issueID),
		Title:       "editable case",
		Steps:       "1. do the thing",
		Expected:    "it works",
		Kind:        "manual",
		Source:      "human",
		AuthorType:  "member",
		AuthorID:    testUUID(testUserID),
		Category:    "positive",
		Priority:    "p2",
	})
	if err != nil {
		t.Fatalf("seed case: %v", err)
	}
	caseID := uuidToString(created.ID)

	patch := func(body map[string]any) (int, TestCaseResponse) {
		req := withURLParam(newRequest(http.MethodPatch, "/api/test-cases/"+caseID, body), "id", caseID)
		rec := httptest.NewRecorder()
		testHandler.UpdateTestCaseHandler(rec, req)
		var resp TestCaseResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		return rec.Code, resp
	}

	// Partial update of only the new fields; title must survive (COALESCE).
	code, resp := patch(map[string]any{
		"preconditions": "VPN connected; test tenant selected",
		"priority":      "p3",
		"modality":      "api",
	})
	if code != http.StatusOK {
		t.Fatalf("update metadata: got %d", code)
	}
	if resp.Preconditions != "VPN connected; test tenant selected" || resp.Priority != "p3" || resp.Modality != "api" {
		t.Errorf("metadata not updated: %+v", resp)
	}
	if resp.Title != "editable case" {
		t.Errorf("partial update clobbered title: %q", resp.Title)
	}

	// Modality "" is a legitimate edit: clears back to legacy/unspecified.
	code, resp = patch(map[string]any{"modality": ""})
	if code != http.StatusOK || resp.Modality != "" {
		t.Errorf("clearing modality: code=%d modality=%q", code, resp.Modality)
	}
	// Priority "" normalizes to the p2 default rather than erroring.
	code, resp = patch(map[string]any{"priority": ""})
	if code != http.StatusOK || resp.Priority != "p2" {
		t.Errorf("empty priority should normalize to p2: code=%d priority=%q", code, resp.Priority)
	}

	// Garbage on UPDATE is rejected — 400, and the row is untouched.
	if code, _ := patch(map[string]any{"priority": "urgent"}); code != http.StatusBadRequest {
		t.Errorf("garbage priority on update must 400, got %d", code)
	}
	if code, _ := patch(map[string]any{"modality": "telepathy"}); code != http.StatusBadRequest {
		t.Errorf("garbage modality on update must 400, got %d", code)
	}
	row, err := testHandler.Queries.GetTestCase(ctx, db.GetTestCaseParams{
		ID: created.ID, WorkspaceID: testUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("reload case: %v", err)
	}
	if row.Priority != "p2" || row.Modality != "" {
		t.Errorf("rejected updates must not write: priority=%q modality=%q", row.Priority, row.Modality)
	}
}
