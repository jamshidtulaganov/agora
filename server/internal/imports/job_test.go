package imports_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/importmem"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// stubAdapter is the smallest thing that satisfies imports.Adapter: it returns
// a fixed bundle. The adapter contract itself is tested against a recorded
// fixture in the linear package; here it only has to exist.
type stubAdapter struct {
	bundle  *imports.Bundle
	fetched int
	err     error
}

func (s *stubAdapter) Source() imports.Source {
	return imports.Source{Kind: imports.SourceLinear, Ref: "acme"}
}
func (s *stubAdapter) Defaults() imports.Defaults { return linearLikeDefaults() }
func (s *stubAdapter) Probe(context.Context) (imports.ProbeResult, error) {
	return imports.ProbeResult{Status: imports.ProbeOK}, nil
}
func (s *stubAdapter) Containers(context.Context) ([]imports.Container, error) {
	return s.bundle.Containers, nil
}
func (s *stubAdapter) Fetch(ctx context.Context, _ imports.Scope, progress imports.ProgressFunc) (*imports.Bundle, error) {
	s.fetched++
	if s.err != nil {
		return nil, s.err
	}
	progress(imports.Progress{Phase: "fetch", Kind: imports.KindIssue, Done: len(s.bundle.Issues)})
	return s.bundle, nil
}

func newRunner(store *importmem.Store) *imports.Runner {
	// The real clock on purpose: the throttle gates on wall time as well as
	// row count, and a frozen clock would silently disable half of it.
	return &imports.Runner{
		Jobs:             store,
		Store:            store,
		ProgressEvery:    1,
		ProgressInterval: time.Nanosecond,
	}
}

func openJob(t *testing.T, runner *imports.Runner, ws pgtype.UUID, status string) db.ImportJob {
	t.Helper()
	job, err := runner.Open(context.Background(), imports.OpenRequest{
		WorkspaceID: ws,
		Source:      imports.SourceLinear,
		Status:      status,
		Scope:       imports.Scope{Containers: []string{"team-eng"}},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return job
}

// A second import in the same workspace is REFUSED with a pointer at the
// running one. The Bitrix global cancelled whatever was going, which is only
// safe when there is exactly one operator.
func TestSecondImportIsRefusedNotCancelling(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	ws := util.MustParseUUID(testWorkspaceID)

	first := openJob(t, runner, ws, imports.JobPending)

	_, err := runner.Open(context.Background(), imports.OpenRequest{
		WorkspaceID: ws, Source: imports.SourceLinear, Status: imports.JobPending,
	})
	var inFlight *imports.ErrImportInFlight
	if !errors.As(err, &inFlight) {
		t.Fatalf("second Open err = %v, want ErrImportInFlight", err)
	}
	if inFlight.JobID != util.UUIDToString(first.ID) {
		t.Errorf("the refusal points at %q, want the live job %q", inFlight.JobID, util.UUIDToString(first.ID))
	}
	if len(store.Jobs) != 1 {
		t.Errorf("the refused open created a second job row (%d rows)", len(store.Jobs))
	}
	// And the live job was not touched.
	if store.Jobs[0].Status != imports.JobPending {
		t.Errorf("the live job's status changed to %q; a second import must not cancel it", store.Jobs[0].Status)
	}
}

// The claim is a guarded UPDATE: a retried confirm, or two servers racing the
// same request, produce exactly one run.
func TestClaimIsWonExactlyOnce(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	ws := util.MustParseUUID(testWorkspaceID)
	job := openJob(t, runner, ws, imports.JobPending)
	adapter := &stubAdapter{bundle: fixtureBundle()}

	applier := newApplier(t, store)
	finished, result, err := runner.Run(context.Background(), job, adapter, applier)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if finished.Status != imports.JobDone || !finished.FinishedAt.Valid {
		t.Fatalf("finished job = %s (finished_at valid=%v)", finished.Status, finished.FinishedAt.Valid)
	}
	if result.Totals[imports.KindIssue].Created != 4 {
		t.Fatalf("totals = %+v", result.Totals[imports.KindIssue])
	}

	// A retry of the same confirm must not run a second import.
	_, _, err = runner.Run(context.Background(), job, adapter, newApplier(t, store))
	var inFlight *imports.ErrImportInFlight
	if !errors.As(err, &inFlight) {
		t.Fatalf("second Run err = %v, want the claim to be refused", err)
	}
	if adapter.fetched != 1 {
		t.Errorf("the adapter fetched %d times; a lost claim must not fetch", adapter.fetched)
	}
	if len(store.Issues) != 4 {
		t.Errorf("a second Run duplicated issues: %d rows", len(store.Issues))
	}
}

// The receipt is a row: totals and failures survive the process, and a finished
// job frees the workspace for the next import.
func TestReceiptLandsOnTheRow(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	ws := util.MustParseUUID(testWorkspaceID)
	job := openJob(t, runner, ws, imports.JobPending)

	finished, _, err := runner.Run(context.Background(), job, &stubAdapter{bundle: fixtureBundle()}, newApplier(t, store))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var totals map[string]imports.EntityTotals
	if err := json.Unmarshal(finished.Totals, &totals); err != nil {
		t.Fatalf("totals are not readable json: %v (%s)", err, finished.Totals)
	}
	if totals[imports.KindIssue].Created != 4 {
		t.Errorf("row totals = %+v", totals)
	}
	if err := runner.EnsureIdle(context.Background(), ws); err != nil {
		t.Errorf("a finished job still blocks the workspace: %v", err)
	}
}

// A fetch that fails leaves a terminal row with the reason, not a job stuck in
// 'running' that blocks the workspace forever.
func TestFetchFailureFinishesTheJob(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	ws := util.MustParseUUID(testWorkspaceID)
	job := openJob(t, runner, ws, imports.JobPending)

	adapter := &stubAdapter{bundle: fixtureBundle(), err: errors.New("linear returned 401")}
	finished, _, err := runner.Run(context.Background(), job, adapter, newApplier(t, store))
	if err == nil {
		t.Fatal("Run swallowed a fetch failure")
	}
	if finished.Status != imports.JobFailed {
		t.Fatalf("status = %q, want failed", finished.Status)
	}
	var failures []imports.Failure
	if err := json.Unmarshal(finished.Failures, &failures); err != nil || len(failures) == 0 {
		t.Fatalf("no failure recorded on the row: %s", finished.Failures)
	}
	if failures[0].Reason == "" {
		t.Error("the failure has no reason")
	}
	if err := runner.EnsureIdle(context.Background(), ws); err != nil {
		t.Errorf("a failed job still blocks the workspace: %v", err)
	}
}

// A cancelled run is not a failed run, and the terminal row still lands even
// though the context that carried the work is dead.
func TestCancelledRunIsCancelledNotFailed(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	ws := util.MustParseUUID(testWorkspaceID)
	job := openJob(t, runner, ws, imports.JobPending)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finished, _, err := runner.Run(ctx, job, &stubAdapter{bundle: fixtureBundle()}, newApplier(t, store))
	if err == nil {
		t.Fatal("Run ignored a cancelled context")
	}
	if finished.Status != imports.JobCancelled {
		t.Fatalf("status = %q, want cancelled — a cancelled run is not a failed run", finished.Status)
	}
	if !finished.FinishedAt.Valid {
		t.Error("the terminal row did not land; a job stuck in 'running' blocks the workspace forever")
	}
}

// The preview parks the plan and the frozen mapping on the row and writes
// nothing to the workspace.
func TestPreviewParksThePlanAndFreezesTheMapping(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	ws := util.MustParseUUID(testWorkspaceID)
	job := openJob(t, runner, ws, imports.JobDryRun)

	overrides := imports.ParseOverrides([]byte(`{"import_mapping":{"linear":{"status":{"In Review":"blocked"}}}}`), imports.SourceLinear)
	mapping := imports.NewMapping(imports.SourceLinear, linearLikeDefaults(), overrides)
	resolver := imports.NewActorResolver(store, imports.ResolverConfig{
		Source: imports.SourceLinear, WorkspaceID: testWorkspaceID, DryRun: true,
	})

	plan, parked, err := runner.Preview(context.Background(), job, &stubAdapter{bundle: fixtureBundle()}, mapping, resolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.Issues.Create != 4 {
		t.Errorf("plan = %+v", plan.Issues)
	}
	if parked.Status != imports.JobAwaitingConfirm {
		t.Fatalf("status after preview = %q, want awaiting_confirm", parked.Status)
	}
	var frozen imports.Overrides
	if err := json.Unmarshal(parked.Mapping, &frozen); err != nil {
		t.Fatalf("frozen mapping unreadable: %v", err)
	}
	if frozen.Status["in review"] != imports.StatusBlocked {
		t.Errorf("frozen mapping = %+v, want the override the human saw", frozen.Status)
	}
	if len(store.Issues) != 0 {
		t.Errorf("a dry run wrote %d issues", len(store.Issues))
	}
	// And the plan is readable back off the row, which is what makes the
	// receipt reconstructible after the fact.
	decoded, err := imports.DecodePlan(parked.Plan)
	if err != nil || decoded == nil {
		t.Fatalf("DecodePlan: %v", err)
	}
	if decoded.Issues.Total != 4 {
		t.Errorf("decoded plan = %+v", decoded.Issues)
	}
}

// Progress is throttled and written to the row, not fanned out per issue.
func TestProgressIsThrottledAndPersisted(t *testing.T) {
	store := importmem.New()
	runner := newRunner(store)
	published := 0
	runner.Publish = func(string, string, imports.Progress) { published++ }
	runner.ProgressEvery = 3
	runner.ProgressInterval = time.Nanosecond

	ws := util.MustParseUUID(testWorkspaceID)
	job := openJob(t, runner, ws, imports.JobPending)
	if _, _, err := runner.Run(context.Background(), job, &stubAdapter{bundle: fixtureBundle()}, newApplier(t, store)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Four issues at one publish per three rows: strictly fewer ticks than
	// rows. The rule being enforced is "not one frame per row".
	if published >= 4 {
		t.Errorf("published %d progress events for 4 issues; the throttle is not working", published)
	}
	if store.Calls["UpdateImportJobProgress"] == 0 {
		t.Error("no progress was written to the row; it must survive a restart")
	}
}

// An old job row whose scope predates a new field must still decode.
func TestScopeDecodeToleratesOldRows(t *testing.T) {
	scope, err := imports.DecodeScope([]byte(`{"containers":["team-eng"],"unknown_future_field":true}`))
	if err != nil {
		t.Fatalf("DecodeScope: %v", err)
	}
	if len(scope.Containers) != 1 || scope.Containers[0] != "team-eng" {
		t.Errorf("scope = %+v", scope)
	}
	// Comments and attachments default ON for a migration: a tracker without
	// its discussion is a paste.
	if !scope.WantsComments() || !scope.WantsAttachments() {
		t.Error("an old scope row defaulted comments/attachments off")
	}
	if _, err := imports.DecodeScope(nil); err != nil {
		t.Errorf("an empty scope should decode to the zero value: %v", err)
	}
}
