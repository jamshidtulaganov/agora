package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/linear"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The job lifecycle (docs/importers-plan.md §3.7, §4.1).
//
// These tests drive the endpoints, not the framework: the applier's own
// ordering and idempotency are covered in internal/imports. What is proved
// here is the part only the HTTP layer can get wrong — that a dry run writes a
// plan and no workspace rows, that a confirm without a plan is refused, that a
// second import is refused with a pointer at the first, that a cancel is
// recorded as a cancel, and that none of it crosses a workspace boundary.

// importAPIFakeAdapter is the adapter seam the Runner already supports: Fetch
// is a function of the bundle, so an end-to-end job needs no GraphQL at all.
// (The real Linear adapter is exercised against a recorded fixture in
// internal/imports/linear and, through the probe, in the connection tests.)
type importAPIFakeAdapter struct {
	bundle   func() *imports.Bundle
	fetchErr error
	delay    time.Duration
	fetches  int32
}

func (a *importAPIFakeAdapter) Source() imports.Source {
	return imports.Source{Kind: imports.SourceLinear, Ref: "acme"}
}

func (a *importAPIFakeAdapter) Defaults() imports.Defaults { return linear.Defaults() }

func (a *importAPIFakeAdapter) Probe(context.Context) (imports.ProbeResult, error) {
	return imports.ProbeResult{Status: imports.ProbeOK, Account: "kim@acme.io", Ref: "acme"}, nil
}

func (a *importAPIFakeAdapter) Containers(context.Context) ([]imports.Container, error) {
	return a.bundle().Containers, nil
}

func (a *importAPIFakeAdapter) Fetch(ctx context.Context, scope imports.Scope, progress imports.ProgressFunc) (*imports.Bundle, error) {
	atomic.AddInt32(&a.fetches, 1)
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if a.fetchErr != nil {
		return nil, a.fetchErr
	}
	progress(imports.Progress{Phase: "fetch", Kind: "issues", Done: 2})
	return a.bundle(), nil
}

// importAPIBundle is a small but real-shaped Linear workspace: one team, two
// states, one label, one user who IS a member of the target workspace, and two
// issues, one of them with a comment.
func importAPIBundle(memberEmail string) func() *imports.Bundle {
	return func() *imports.Bundle {
		created := time.Date(2024, 3, 7, 9, 30, 0, 0, time.UTC)
		updated := time.Date(2025, 11, 2, 16, 5, 0, 0, time.UTC)
		return &imports.Bundle{
			Source: imports.Source{Kind: imports.SourceLinear, Ref: "acme"},
			Users: []imports.User{
				{ExternalID: "usr-kim", Name: "Kim Ryu", Email: memberEmail, Active: true},
			},
			Containers: []imports.Container{
				{ExternalID: "team-eng", Key: "ENG", Name: "Engineering"},
			},
			States: []imports.State{
				{ExternalID: "st-todo", ContainerID: "team-eng", Name: "Todo", Category: "unstarted"},
				{ExternalID: "st-done", ContainerID: "team-eng", Name: "Done", Category: "completed"},
			},
			Labels: []imports.Label{{ExternalID: "lab-bug", Name: "Import Bug", Color: "#ef4444"}},
			Issues: []imports.Issue{
				{
					ExternalID: "iss-142", Identifier: "ENG-142", Title: "Fix the login redirect loop",
					BodyMarkdown: "The redirect **loops** on SSO.",
					StateID:      "st-todo", ContainerID: "team-eng", Priority: imports.PriorityHigh,
					CreatorID: "usr-kim", AssigneeID: "usr-kim", LabelIDs: []string{"lab-bug"},
					CreatedAt: created, UpdatedAt: updated,
					Comments: []imports.Comment{{
						ExternalID: "cmt-1", AuthorID: "usr-kim", BodyMarkdown: "Reproduced on staging.",
						CreatedAt: created, UpdatedAt: created,
					}},
				},
				{
					ExternalID: "iss-143", Identifier: "ENG-143", Title: "Rotate the session key",
					StateID: "st-done", ContainerID: "team-eng", Priority: imports.PriorityMedium,
					CreatorID: "usr-kim", CreatedAt: created, UpdatedAt: updated,
				},
			},
		}
	}
}

// importAPIUseAdapter installs a fake adapter for one test.
func importAPIUseAdapter(t *testing.T, adapter imports.Adapter) {
	t.Helper()
	prev := newImportAdapter
	newImportAdapter = func(conn db.ImportConnection, token string) (imports.Adapter, error) {
		return adapter, nil
	}
	t.Cleanup(func() { newImportAdapter = prev })
}

// importAPIFakeWorkspace sets up a workspace, a sealed connection and a fake
// adapter whose bundle references the workspace owner by email.
func importAPIFakeWorkspace(t *testing.T, slug string, adapter *importAPIFakeAdapter) (workspaceID, userID, connectionID string) {
	t.Helper()
	importAPISealKey(t)
	// An applied import creates the source's attribution user, which is a
	// GLOBAL row rather than a workspace one. Registered before the workspace
	// helper's own cleanup so it runs after it (t.Cleanup is LIFO): the rows
	// referencing the account go with the workspace first, and the delete is
	// best-effort because another workspace's import may still own it.
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = $1`, imports.ImportIdentityEmail(imports.SourceLinear))
	})
	workspaceID, userID = importAPIWorkspace(t, slug)
	var email string
	if err := testPool.QueryRow(context.Background(), `SELECT email FROM "user" WHERE id = $1`, userID).Scan(&email); err != nil {
		t.Fatalf("read owner email: %v", err)
	}
	if adapter.bundle == nil {
		adapter.bundle = importAPIBundle(email)
	}
	importAPIUseAdapter(t, adapter)
	connectionID = importAPICreateConnection(t, workspaceID, userID)
	return workspaceID, userID, connectionID
}

// importAPIDryRun posts a dry run and returns the decoded job.
func importAPIDryRun(t *testing.T, workspaceID, userID string, body map[string]any) (int, importJobResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.DryRunImport(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID+"/import/dry-run", workspaceID, userID, body))
	var decoded struct {
		Job importJobResponse `json:"job"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	if rec.Code >= 400 && rec.Code != http.StatusConflict {
		t.Logf("dry-run %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, decoded.Job
}

func importAPIConfirm(t *testing.T, workspaceID, userID string, body map[string]any) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.CreateImportJob(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID+"/import/jobs", workspaceID, userID, body))
	return rec.Code, rec.Body.String()
}

func importAPIGetJob(t *testing.T, workspaceID, userID, jobID string) (int, importJobResponse) {
	t.Helper()
	req := importAPIRequest(t, http.MethodGet, "/api/workspaces/"+workspaceID+"/import/jobs/"+jobID, workspaceID, userID, nil)
	rec := httptest.NewRecorder()
	testHandler.GetImportJob(rec, withExtraURLParam(req, "jid", jobID))
	var decoded struct {
		Job importJobResponse `json:"job"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded.Job
}

// importAPIAwaitTerminal polls the job until it stops moving. The run is
// out-of-band by design, so the test waits the way a client does.
func importAPIAwaitTerminal(t *testing.T, workspaceID, userID, jobID string) importJobResponse {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last importJobResponse
	for time.Now().Before(deadline) {
		code, job := importAPIGetJob(t, workspaceID, userID, jobID)
		if code != http.StatusOK {
			t.Fatalf("poll job: want 200, got %d", code)
		}
		last = job
		if job.Terminal {
			return job
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("job %s never reached a terminal state (last status %q)", jobID, last.Status)
	return last
}

func importAPIIssueCount(t *testing.T, workspaceID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1`, workspaceID).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	return n
}

// TestDryRunImport_ParksAPlanAndWritesNothing — the survey produces a plan on
// the job row and not one workspace row. Nothing before the confirm writes.
func TestDryRunImport_ParksAPlanAndWritesNothing(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-dryrun", adapter)

	code, job := importAPIDryRun(t, wsID, userID, map[string]any{"connection_id": connID})
	if code != http.StatusOK {
		t.Fatalf("dry run: want 200, got %d", code)
	}
	if job.Status != imports.JobAwaitingConfirm {
		t.Errorf("status = %q, want awaiting_confirm", job.Status)
	}
	if job.Plan == nil {
		t.Fatal("dry run produced no plan")
	}
	if job.Plan.Issues.Create != 2 || job.Plan.Issues.Update != 0 {
		t.Errorf("issue plan = %+v, want create 2 / update 0", job.Plan.Issues)
	}
	if job.Plan.Comments.Create != 1 {
		t.Errorf("comment plan = %+v, want create 1", job.Plan.Comments)
	}
	if len(job.Plan.Containers) != 1 || job.Plan.Containers[0].Action != "create_project" {
		t.Errorf("container plan = %+v, want one project to create", job.Plan.Containers)
	}
	if len(job.Plan.Users) != 1 || !job.Plan.Users[0].Matched() {
		t.Errorf("user plan = %+v, want the workspace member matched by email", job.Plan.Users)
	}
	if n := importAPIIssueCount(t, wsID); n != 0 {
		t.Fatalf("the dry run wrote %d issues; it must write none", n)
	}
	if got := atomic.LoadInt32(&adapter.fetches); got != 1 {
		t.Errorf("adapter fetched %d times, want 1", got)
	}
}

// TestCreateImportJob_RefusesWithoutAReviewedPlan — a confirm that arrives
// before anyone has surveyed anything is the plan card with the box
// pre-ticked. It is refused, and it names the next step.
func TestCreateImportJob_RefusesWithoutAReviewedPlan(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-noplan", adapter)

	code, body := importAPIConfirm(t, wsID, userID, map[string]any{"connection_id": connID})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("confirm with no plan: want 422, got %d: %s", code, body)
	}
	if !strings.Contains(body, "dry run") {
		t.Errorf("refusal does not name the next step: %s", body)
	}
	if n := importAPIIssueCount(t, wsID); n != 0 {
		t.Fatalf("a refused confirm wrote %d issues", n)
	}
}

// TestCreateImportJob_AppliesTheConfirmedPlan — the whole round trip, and the
// re-run property the upsert exists for: a second import of the same source is
// "created 0, updated N", never a duplicate set.
func TestCreateImportJob_AppliesTheConfirmedPlan(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-apply", adapter)

	_, previewed := importAPIDryRun(t, wsID, userID, map[string]any{"connection_id": connID})
	code, body := importAPIConfirm(t, wsID, userID, map[string]any{"connection_id": connID})
	if code != http.StatusAccepted {
		t.Fatalf("confirm: want 202, got %d: %s", code, body)
	}
	if !strings.Contains(body, previewed.ID) {
		t.Errorf("confirm started a different job than the one previewed: %s", body)
	}

	done := importAPIAwaitTerminal(t, wsID, userID, previewed.ID)
	if done.Status != imports.JobDone {
		t.Fatalf("job status = %q (failures %+v), want done", done.Status, done.Failures)
	}
	if got := done.Totals[imports.KindIssue]; got.Created != 2 {
		t.Errorf("issue totals = %+v, want created 2", got)
	}
	// The comment upsert is one statement and cannot report which branch it
	// took, so the applier counts every landed comment as an update. What the
	// endpoint must prove is that it landed at all.
	if got := done.Totals[imports.KindComment]; got.Created+got.Updated != 1 {
		t.Errorf("comment totals = %+v, want one comment landed", got)
	}
	if n := importAPIIssueCount(t, wsID); n != 2 {
		t.Fatalf("workspace has %d issues after the import, want 2", n)
	}

	// The issues carry their external ref, which is what makes the next run an
	// update rather than a duplicate.
	var linked int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM issue
		WHERE workspace_id = $1 AND metadata -> 'external_ref' ->> 'source' = 'linear'`, wsID).Scan(&linked); err != nil {
		t.Fatalf("count linked issues: %v", err)
	}
	if linked != 2 {
		t.Errorf("%d issues carry an external ref, want 2", linked)
	}

	// Re-run: same bundle, same workspace.
	_, second := importAPIDryRun(t, wsID, userID, map[string]any{"connection_id": connID})
	if second.Plan == nil || second.Plan.Issues.Create != 0 || second.Plan.Issues.Update != 2 {
		t.Fatalf("re-run plan = %+v, want create 0 / update 2", second.Plan)
	}
	if code, body := importAPIConfirm(t, wsID, userID, map[string]any{"connection_id": connID}); code != http.StatusAccepted {
		t.Fatalf("re-confirm: want 202, got %d: %s", code, body)
	}
	rerun := importAPIAwaitTerminal(t, wsID, userID, second.ID)
	if rerun.Status != imports.JobDone {
		t.Fatalf("re-run status = %q (failures %+v), want done", rerun.Status, rerun.Failures)
	}
	if got := rerun.Totals[imports.KindIssue]; got.Created != 0 || got.Updated != 2 {
		t.Errorf("re-run issue totals = %+v, want created 0 / updated 2", got)
	}
	if n := importAPIIssueCount(t, wsID); n != 2 {
		t.Errorf("re-running duplicated issues: %d in the workspace, want 2", n)
	}
}

// TestImport_SecondImportIsRefusedWithAPointer — the rule the Bitrix global
// got wrong. A running import is never cancelled by the next request; the
// next request is refused, and told which job is in the way.
func TestImport_SecondImportIsRefusedWithAPointer(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-inflight", adapter)

	var runningID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO import_job (workspace_id, connection_id, source, status, scope)
		VALUES ($1, $2, 'linear', 'running', '{}'::jsonb)
		RETURNING id`, wsID, connID).Scan(&runningID); err != nil {
		t.Fatalf("seed running job: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.DryRunImport(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/dry-run", wsID, userID,
			map[string]any{"connection_id": connID}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("dry run while an import runs: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var conflict struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.JobID != runningID {
		t.Errorf("conflict job_id = %q, want the running job %q", conflict.JobID, runningID)
	}
	if conflict.Status != imports.JobRunning {
		t.Errorf("conflict status = %q, want running", conflict.Status)
	}

	code, body := importAPIConfirm(t, wsID, userID, map[string]any{"connection_id": connID})
	if code != http.StatusConflict {
		t.Fatalf("confirm while an import runs: want 409, got %d: %s", code, body)
	}
	if !strings.Contains(body, runningID) {
		t.Errorf("confirm conflict does not point at the running job: %s", body)
	}

	// And the running row is untouched — refused, not cancelled.
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM import_job WHERE id = $1`, runningID).Scan(&status); err != nil {
		t.Fatalf("read running job: %v", err)
	}
	if status != imports.JobRunning {
		t.Errorf("the running job was moved to %q by a second request", status)
	}
}

// TestDryRunImport_SupersedesAParkedPreview — a preview wrote nothing, so
// surveying again must not be blocked by it. This is what "argue with the
// report" does on every turn.
func TestDryRunImport_SupersedesAParkedPreview(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-supersede", adapter)

	code, first := importAPIDryRun(t, wsID, userID, map[string]any{"connection_id": connID})
	if code != http.StatusOK {
		t.Fatalf("first dry run: want 200, got %d", code)
	}
	code, second := importAPIDryRun(t, wsID, userID, map[string]any{
		"connection_id": connID,
		"scope":         map[string]any{"containers": []string{"team-eng"}},
	})
	if code != http.StatusOK {
		t.Fatalf("second dry run: want 200, got %d", code)
	}
	if second.ID == first.ID {
		t.Fatal("the second survey reused the first job row; its scope would be stale")
	}
	if second.Status != imports.JobAwaitingConfirm {
		t.Errorf("second status = %q, want awaiting_confirm", second.Status)
	}
	if len(second.Scope.Containers) != 1 || second.Scope.Containers[0] != "team-eng" {
		t.Errorf("second scope = %+v, want the newly chosen container", second.Scope)
	}

	_, superseded := importAPIGetJob(t, wsID, userID, first.ID)
	if superseded.Status != imports.JobCancelled {
		t.Errorf("superseded preview status = %q, want cancelled", superseded.Status)
	}
}

// TestCreateImportJob_RefusesAChangedScope — the confirmation binds the plan.
// A confirm carrying a scope the plan was not computed with is refused rather
// than silently applying either one.
func TestCreateImportJob_RefusesAChangedScope(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-scopedrift", adapter)

	if code, _ := importAPIDryRun(t, wsID, userID, map[string]any{
		"connection_id": connID,
		"scope":         map[string]any{"containers": []string{"team-eng"}},
	}); code != http.StatusOK {
		t.Fatalf("dry run: want 200, got %d", code)
	}
	code, body := importAPIConfirm(t, wsID, userID, map[string]any{
		"connection_id": connID,
		"scope":         map[string]any{"containers": []string{"team-ops"}},
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("confirm with a changed scope: want 422, got %d: %s", code, body)
	}
	if n := importAPIIssueCount(t, wsID); n != 0 {
		t.Fatalf("a refused confirm wrote %d issues", n)
	}
}

// TestCancelImportJob — cancelling a parked preview records a cancel, and
// cancelling something already finished is refused rather than rewriting its
// receipt.
func TestCancelImportJob(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-cancel", adapter)

	_, job := importAPIDryRun(t, wsID, userID, map[string]any{"connection_id": connID})
	if job.ID == "" {
		t.Fatal("dry run returned no job")
	}

	cancel := func() *httptest.ResponseRecorder {
		req := importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/jobs/"+job.ID+"/cancel", wsID, userID, nil)
		rec := httptest.NewRecorder()
		testHandler.CancelImportJob(rec, withExtraURLParam(req, "jid", job.ID))
		return rec
	}

	rec := cancel()
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Job importJobResponse `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if body.Job.Status != imports.JobCancelled {
		t.Errorf("status after cancel = %q, want cancelled", body.Job.Status)
	}
	if !body.Job.Terminal {
		t.Error("a cancelled job must report terminal")
	}

	if again := cancel(); again.Code != http.StatusConflict {
		t.Errorf("cancel of a finished job: want 409, got %d: %s", again.Code, again.Body.String())
	}

	// Confirming a cancelled plan is refused too — the plan is gone.
	if code, resp := importAPIConfirm(t, wsID, userID, map[string]any{
		"connection_id": connID, "job_id": job.ID,
	}); code != http.StatusUnprocessableEntity {
		t.Errorf("confirm of a cancelled job: want 422, got %d: %s", code, resp)
	}
}

// TestImportJobs_WorkspaceFencing — a job id from another tenant is a 404 on
// every verb, and the cross-tenant cancel does not touch the row.
func TestImportJobs_WorkspaceFencing(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsA, userA, connA := importAPIFakeWorkspace(t, "import-api-jobfence-a", adapter)
	wsB, userB := importAPIWorkspace(t, "import-api-jobfence-b")

	_, job := importAPIDryRun(t, wsA, userA, map[string]any{"connection_id": connA})
	if job.ID == "" {
		t.Fatal("dry run returned no job")
	}

	if code, _ := importAPIGetJob(t, wsB, userB, job.ID); code != http.StatusNotFound {
		t.Errorf("GET job across workspaces: want 404, got %d", code)
	}

	req := importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsB+"/import/jobs/"+job.ID+"/cancel", wsB, userB, nil)
	rec := httptest.NewRecorder()
	testHandler.CancelImportJob(rec, withExtraURLParam(req, "jid", job.ID))
	if rec.Code != http.StatusNotFound {
		t.Errorf("cancel across workspaces: want 404, got %d: %s", rec.Code, rec.Body.String())
	}

	if code, still := importAPIGetJob(t, wsA, userA, job.ID); code != http.StatusOK || still.Status != imports.JobAwaitingConfirm {
		t.Errorf("workspace A's job changed: code %d status %q", code, still.Status)
	}
}

// TestDryRunImport_AdapterFailureIsReportedNotPanicked — a source that breaks
// mid-walk ends as a recorded failure on the job row. The background goroutine
// has no middleware.Recoverer behind it, so "it degrades" is load-bearing.
func TestDryRunImport_AdapterFailureIsReportedNotPanicked(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{fetchErr: errImportAPIFetch}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-fetchfail", adapter)

	rec := httptest.NewRecorder()
	testHandler.DryRunImport(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/dry-run", wsID, userID,
			map[string]any{"connection_id": connID}))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("failed survey: want 502, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Job importJobResponse `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode failure response: %v", err)
	}
	if body.Job.Status != imports.JobFailed {
		t.Errorf("status = %q, want failed", body.Job.Status)
	}
	if len(body.Job.Failures) == 0 {
		t.Error("a failed survey recorded no reason")
	}
	// The workspace is not left wedged: the next survey opens a fresh job.
	if code, next := importAPIDryRun(t, wsID, userID, map[string]any{"connection_id": connID}); code != http.StatusBadGateway || next.ID == body.Job.ID {
		t.Errorf("a failed survey blocked the next one (code %d, job %q)", code, next.ID)
	}
}

// TestDryRunImport_SealKeyUnsetIs503 — the seal key can disappear between
// storing a connection and using it (a rotation, a bad deploy). The import
// path fails closed rather than reading a token it cannot decrypt.
func TestDryRunImport_SealKeyUnsetIs503(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	adapter := &importAPIFakeAdapter{}
	wsID, userID, connID := importAPIFakeWorkspace(t, "import-api-jobnokey", adapter)

	t.Setenv(imports.SecretKeyEnv, "")
	rec := httptest.NewRecorder()
	testHandler.DryRunImport(rec,
		importAPIRequest(t, http.MethodPost, "/api/workspaces/"+wsID+"/import/dry-run", wsID, userID,
			map[string]any{"connection_id": connID}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("dry run with no seal key: want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

var errImportAPIFetch = &importAPIError{msg: "linear returned a malformed page"}

type importAPIError struct{ msg string }

func (e *importAPIError) Error() string { return e.msg }

// TestPublishImportProgress — the throttled tick reaches the workspace room as
// `import:progress` with counters only. The authoritative state stays the job
// row, which the client reads through the membership-gated endpoint; a fanout
// must never become the thing that leaks what is being imported.
func TestPublishImportProgress(t *testing.T) {
	if testHandler == nil || testHandler.Bus == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	received := make(chan events.Event, 4)
	testHandler.Bus.Subscribe(protocol.EventImportProgress, func(e events.Event) {
		received <- e
	})

	testHandler.publishImportProgress("ws-1", "job-1", imports.Progress{
		Phase: "apply", Kind: imports.KindIssue, Done: 120,
	})

	select {
	case e := <-received:
		if e.WorkspaceID != "ws-1" {
			t.Errorf("workspace = %q, want the importing workspace", e.WorkspaceID)
		}
		if e.ActorType != "system" {
			t.Errorf("actor_type = %q, want system", e.ActorType)
		}
		payload, ok := e.Payload.(protocol.ImportProgressPayload)
		if !ok {
			t.Fatalf("payload type = %T, want ImportProgressPayload", e.Payload)
		}
		if payload.JobID != "job-1" || payload.Done != 120 || payload.Kind != imports.KindIssue {
			t.Errorf("payload = %+v, want the job's counters", payload)
		}
		if payload.Total != 0 {
			t.Errorf("total = %d, want 0 when the source cannot produce one", payload.Total)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no import:progress event was published")
	}
}
