// The job model (docs/importers-plan.md §3.7).
//
// An import is a ROW, not a package-level variable. The Bitrix importer's
// progress is a single process-wide struct with a mutex, a run id and a Cancel
// func — one import at a time per process, progress lost on restart, invisible
// to a second instance, and a second run cancels whatever was going. That is
// fine for an operator backfill and disqualifying for a customer-facing
// importer.
//
// Everything in this file follows from the row:
//
//   - progress survives a restart, because it is written to import_job.totals;
//   - two workspaces import concurrently, because there is no shared state to
//     contend for — THERE IS NO SINGLETON IN THIS PACKAGE;
//   - a second import in the same workspace is REFUSED with a pointer at the
//     running one rather than cancelling it (GetActiveImportJob);
//   - the claim is a guarded UPDATE, so a retry or two servers racing the same
//     confirm produce exactly one run;
//   - the receipt is reconstructible after the fact.
//
// Progress publication is throttled — every N rows or every 2s, whichever is
// slower — so a 10k-issue import does not become 10k websocket frames. The
// mistake being avoided is already on record in the stress findings:
// synchronous fanout on the write path.
package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// import_job.status values. No CHECK backs these in the database on purpose (a
// new posture must not need a migration), so this is the list the code agrees
// on and an unknown value renders as a generic state rather than failing.
const (
	JobPending         = "pending"
	JobDryRun          = "dry_run"
	JobAwaitingConfirm = "awaiting_confirm"
	JobRunning         = "running"
	JobDone            = "done"
	JobFailed          = "failed"
	JobCancelled       = "cancelled"
)

// TerminalJobStatus reports whether a job is finished. Unknown statuses are
// treated as NOT terminal, so a value this build does not know cannot make a
// live job look finished.
func TerminalJobStatus(status string) bool {
	switch status {
	case JobDone, JobFailed, JobCancelled:
		return true
	}
	return false
}

// JobStore is the framework's view of the import_job table — again, exactly the
// generated signatures, so *db.Queries satisfies it directly.
type JobStore interface {
	CreateImportJob(ctx context.Context, arg db.CreateImportJobParams) (db.ImportJob, error)
	GetImportJob(ctx context.Context, arg db.GetImportJobParams) (db.ImportJob, error)
	GetActiveImportJob(ctx context.Context, workspaceID pgtype.UUID) (db.ImportJob, error)
	SetImportJobPlan(ctx context.Context, arg db.SetImportJobPlanParams) (db.ImportJob, error)
	StartImportJob(ctx context.Context, arg db.StartImportJobParams) (db.ImportJob, error)
	UpdateImportJobProgress(ctx context.Context, arg db.UpdateImportJobProgressParams) error
	FinishImportJob(ctx context.Context, arg db.FinishImportJobParams) (db.ImportJob, error)
}

var _ JobStore = (*db.Queries)(nil)

// ErrImportInFlight is returned when a workspace already has a job that has not
// reached a terminal state. It carries the offending job so the caller can point
// at it — the whole reason this replaced a global that cancelled its
// predecessor.
type ErrImportInFlight struct {
	JobID  string
	Status string
}

func (e *ErrImportInFlight) Error() string {
	return fmt.Sprintf("imports: job %s is already %s in this workspace", e.JobID, e.Status)
}

// Publisher receives throttled progress ticks for one workspace. It is a
// function, not the event bus itself, so this package stays free of the
// websocket layer; the handler passes something that publishes
// `import:progress` scoped to the workspace.
type Publisher func(workspaceID string, jobID string, p Progress)

// Runner drives one import_job row through its lifecycle. Construct one per
// request; it holds nothing another request would want.
type Runner struct {
	Jobs    JobStore
	Store   Store
	Publish Publisher
	Limits  Limits
	// ProgressEvery is how many rows between writes/publishes; ProgressInterval
	// is the minimum wall-clock gap. A tick has to clear BOTH ("every N rows or
	// every 2s, whichever is slower").
	ProgressEvery    int
	ProgressInterval time.Duration
	Now              func() time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// EnsureIdle returns ErrImportInFlight when the workspace already has a live
// job. Callers run this BEFORE opening a new one; it is the refusal that
// replaces the Bitrix global's implicit cancel.
func (r *Runner) EnsureIdle(ctx context.Context, workspaceID pgtype.UUID) error {
	active, err := r.Jobs.GetActiveImportJob(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return &ErrImportInFlight{JobID: util.UUIDToString(active.ID), Status: active.Status}
}

// OpenRequest is what it takes to open a job row.
type OpenRequest struct {
	WorkspaceID  pgtype.UUID
	ConnectionID pgtype.UUID
	Source       string
	Scope        Scope
	CreatedBy    pgtype.UUID
	// Status is the posture the job opens in: dry_run for a preview,
	// pending for a run that will be confirmed later.
	Status string
}

// Open creates the job row, refusing when one is already in flight.
func (r *Runner) Open(ctx context.Context, req OpenRequest) (db.ImportJob, error) {
	if err := r.EnsureIdle(ctx, req.WorkspaceID); err != nil {
		return db.ImportJob{}, err
	}
	scope, err := json.Marshal(req.Scope)
	if err != nil {
		return db.ImportJob{}, fmt.Errorf("imports: marshal scope: %w", err)
	}
	status := req.Status
	if status == "" {
		status = JobPending
	}
	return r.Jobs.CreateImportJob(ctx, db.CreateImportJobParams{
		WorkspaceID:  req.WorkspaceID,
		ConnectionID: req.ConnectionID,
		Source:       req.Source,
		Status:       status,
		Scope:        scope,
		CreatedBy:    req.CreatedBy,
	})
}

// Preview runs fetch → normalize → map → diff with zero writes and parks the
// result on the job row as awaiting_confirm. The mapping is frozen alongside
// the plan, so a settings edit between preview and confirm cannot change what
// the human authorized.
func (r *Runner) Preview(
	ctx context.Context,
	job db.ImportJob,
	adapter Adapter,
	mapping *Mapping,
	resolver *ActorResolver,
) (*Plan, db.ImportJob, error) {
	scope, err := DecodeScope(job.Scope)
	if err != nil {
		return nil, job, err
	}

	bundle, err := adapter.Fetch(ctx, scope, r.fetchProgress(job))
	if err != nil {
		finished, ferr := r.Fail(ctx, job, err)
		if ferr != nil {
			return nil, job, errors.Join(err, ferr)
		}
		return nil, finished, err
	}

	existing, err := r.existingIssues(ctx, job.WorkspaceID, job.Source)
	if err != nil {
		return nil, job, err
	}

	plan, err := BuildPlan(ctx, bundle, mapping, resolver, existing, scope, r.Limits)
	if err != nil {
		return nil, job, err
	}

	planBlob, err := json.Marshal(plan)
	if err != nil {
		return nil, job, fmt.Errorf("imports: marshal plan: %w", err)
	}
	mappingBlob, err := json.Marshal(mapping.Frozen())
	if err != nil {
		return nil, job, fmt.Errorf("imports: marshal mapping: %w", err)
	}
	parked, err := r.Jobs.SetImportJobPlan(ctx, db.SetImportJobPlanParams{
		ID:          job.ID,
		WorkspaceID: job.WorkspaceID,
		Status:      JobAwaitingConfirm,
		Plan:        planBlob,
		Mapping:     mappingBlob,
	})
	if err != nil {
		return plan, job, err
	}
	return plan, parked, nil
}

// Run claims the job and applies a bundle. The claim is the guarded UPDATE:
// exactly one caller gets the row back, so a retried confirm or a second server
// racing the same request does not produce two runs.
//
// The bundle is fetched inside Run rather than being passed in, because the
// confirm may be minutes after the preview and the import must apply what the
// source holds NOW, not a cached snapshot the operator never saw.
func (r *Runner) Run(
	ctx context.Context,
	job db.ImportJob,
	adapter Adapter,
	applier *Applier,
) (db.ImportJob, *Result, error) {
	claimed, err := r.Jobs.StartImportJob(ctx, db.StartImportJobParams{
		ID:          job.ID,
		WorkspaceID: job.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Someone else claimed it, or it is already terminal. Not our run.
			return job, nil, &ErrImportInFlight{JobID: util.UUIDToString(job.ID), Status: job.Status}
		}
		return job, nil, err
	}

	scope, err := DecodeScope(claimed.Scope)
	if err != nil {
		finished, ferr := r.Fail(ctx, claimed, err)
		return finished, nil, errors.Join(err, ferr)
	}

	bundle, err := adapter.Fetch(ctx, scope, r.fetchProgress(claimed))
	if err != nil {
		finished, ferr := r.finish(ctx, claimed, statusForError(ctx, err), nil, err)
		return finished, nil, errors.Join(err, ferr)
	}

	tracker := r.newTracker(claimed)
	result, err := applier.Apply(ctx, bundle, tracker.tick)
	if err != nil {
		finished, ferr := r.finish(ctx, claimed, statusForError(ctx, err), result, err)
		return finished, result, errors.Join(err, ferr)
	}

	finished, ferr := r.finish(ctx, claimed, JobDone, result, nil)
	return finished, result, ferr
}

// Cancel moves a job to the cancelled terminal state. Cancellation of the work
// itself is the caller's context; this records the outcome.
func (r *Runner) Cancel(ctx context.Context, job db.ImportJob) (db.ImportJob, error) {
	return r.finish(ctx, job, JobCancelled, nil, errors.New("cancelled by the operator"))
}

// Fail records a terminal failure with its reason.
func (r *Runner) Fail(ctx context.Context, job db.ImportJob, cause error) (db.ImportJob, error) {
	return r.finish(ctx, job, JobFailed, nil, cause)
}

// statusForError distinguishes "the operator cancelled" from "it broke". A
// cancelled run is not a failed run, and the receipt must not call it one.
func statusForError(ctx context.Context, err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return JobCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return JobCancelled
	}
	return JobFailed
}

func (r *Runner) finish(ctx context.Context, job db.ImportJob, status string, result *Result, cause error) (db.ImportJob, error) {
	totals := []byte(`{}`)
	failures := []byte(`[]`)
	if result != nil {
		if blob, err := json.Marshal(result.Totals); err == nil {
			totals = blob
		}
		if blob, err := json.Marshal(result.Failures); err == nil && len(result.Failures) > 0 {
			failures = blob
		}
	}
	if cause != nil {
		failures = appendFailure(failures, Failure{Kind: "job", Identifier: util.UUIDToString(job.ID), Reason: cause.Error()})
	}
	// A cancelled context cannot write the terminal row, and a job stuck in
	// 'running' blocks the workspace forever. Detach so the receipt lands.
	writeCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		writeCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
	}
	return r.Jobs.FinishImportJob(writeCtx, db.FinishImportJobParams{
		ID:          job.ID,
		WorkspaceID: job.WorkspaceID,
		Status:      status,
		Totals:      totals,
		Failures:    failures,
	})
}

func appendFailure(blob []byte, f Failure) []byte {
	var list []Failure
	if len(blob) > 0 {
		_ = json.Unmarshal(blob, &list)
	}
	list = append(list, f)
	out, err := json.Marshal(list)
	if err != nil {
		return blob
	}
	return out
}

// existingIssues loads what this source already linked in the workspace, for
// the create/update split. One query, not N lookups.
func (r *Runner) existingIssues(ctx context.Context, workspaceID pgtype.UUID, source string) (map[string]ExistingIssue, error) {
	if r.Store == nil {
		return map[string]ExistingIssue{}, nil
	}
	rows, err := r.Store.ListIssuesByExternalSource(ctx, db.ListIssuesByExternalSourceParams{
		WorkspaceID: workspaceID,
		Source:      source,
	})
	if err != nil {
		return nil, fmt.Errorf("imports: list linked issues: %w", err)
	}
	out := make(map[string]ExistingIssue, len(rows))
	for _, row := range rows {
		externalID := externalIDString(row.ExternalID)
		if externalID == "" {
			continue
		}
		out[externalID] = ExistingIssue{ID: util.UUIDToString(row.ID), Number: row.Number}
	}
	return out, nil
}

// externalIDString unwraps the `metadata -> … ->> 'id'` projection. sqlc types
// a jsonb text extraction as interface{} because it is nullable and untyped at
// the SQL level, so this is where the assumption is checked rather than cast.
func externalIDString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case nil:
		return ""
	default:
		return fmt.Sprint(s)
	}
}

// DecodeScope reads import_job.scope. A malformed or empty blob decodes to the
// zero Scope rather than erroring, because an old job row must still be
// readable after the struct grows a field.
func DecodeScope(blob []byte) (Scope, error) {
	var scope Scope
	if len(blob) == 0 {
		return scope, nil
	}
	if err := json.Unmarshal(blob, &scope); err != nil {
		return Scope{}, fmt.Errorf("imports: decode scope: %w", err)
	}
	return scope, nil
}

// DecodePlan reads import_job.plan back, for the receipt and for the assistant.
func DecodePlan(blob []byte) (*Plan, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	var plan Plan
	if err := json.Unmarshal(blob, &plan); err != nil {
		return nil, fmt.Errorf("imports: decode plan: %w", err)
	}
	return &plan, nil
}

// --- progress ---------------------------------------------------------------

// progressTracker throttles both the DB write and the event publish. It holds
// no lock because one tracker belongs to one run on one goroutine — the
// applier calls tick synchronously.
type progressTracker struct {
	runner *Runner
	job    db.ImportJob
	totals map[string]int
	last   time.Time
	since  int
}

func (r *Runner) newTracker(job db.ImportJob) *progressTracker {
	return &progressTracker{runner: r, job: job, totals: map[string]int{}, last: r.now()}
}

func (t *progressTracker) tick(p Progress) {
	t.totals[p.Kind] = p.Done
	t.since++

	every := t.runner.ProgressEvery
	if every <= 0 {
		every = 50
	}
	interval := t.runner.ProgressInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	now := t.runner.now()
	// "Every N rows or every 2s, whichever is SLOWER": both gates must clear,
	// so a fast import publishes on the clock and a slow one on the row count.
	if t.since < every || now.Sub(t.last) < interval {
		return
	}
	t.flush(p)
	t.last = now
	t.since = 0
}

func (t *progressTracker) flush(p Progress) {
	blob, err := json.Marshal(t.totals)
	if err != nil {
		return
	}
	// Progress is best-effort: a failed progress write must not fail an import
	// that is otherwise succeeding. The terminal FinishImportJob carries the
	// authoritative numbers.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = t.runner.Jobs.UpdateImportJobProgress(ctx, db.UpdateImportJobProgressParams{
		ID:          t.job.ID,
		WorkspaceID: t.job.WorkspaceID,
		Totals:      blob,
	})
	if t.runner.Publish != nil {
		t.runner.Publish(util.UUIDToString(t.job.WorkspaceID), util.UUIDToString(t.job.ID), p)
	}
}

// fetchProgress publishes the adapter's fetch ticks without writing them to the
// row: a 40-minute walk should not be a blank screen, but "fetched 4,000 of
// unknown" is not a receipt number and does not belong in totals.
func (r *Runner) fetchProgress(job db.ImportJob) ProgressFunc {
	if r.Publish == nil {
		return NopProgress
	}
	last := r.now()
	return func(p Progress) {
		now := r.now()
		interval := r.ProgressInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		if now.Sub(last) < interval {
			return
		}
		last = now
		r.Publish(util.UUIDToString(job.WorkspaceID), util.UUIDToString(job.ID), p)
	}
}
