package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The import job surface (docs/importers-plan.md §3.7, §4.1).
//
// Four endpoints and one invariant: NOTHING is written into the workspace
// until a human confirms a plan they have read.
//
//	POST   …/import/dry-run           survey. Writes a job row and a plan; no
//	                                  workspace rows at all.
//	POST   …/import/jobs              confirm. Claims the previewed job and
//	                                  applies it out-of-band; 202 + job id.
//	GET    …/import/jobs/{jid}        status/plan/totals/failures for polling.
//	POST   …/import/jobs/{jid}/cancel stop, and record that it was a cancel
//	                                  rather than a failure.
//
// A second import in the same workspace is REFUSED with a pointer at the
// running one — it never cancels its predecessor, which is the one behaviour
// the Bitrix importer's process-global got wrong and the reason the job is a
// row. A parked *preview* is a different thing: it wrote nothing, so a fresh
// survey supersedes it rather than being blocked by it (that is what "argue
// with the report" in §4.1 does).

// importCancels holds the context cancel func of every import running IN THIS
// PROCESS, keyed by job id. It is process-local coordination, not shared
// state: on a multi-replica deployment a cancel reaches the replica that owns
// the run only when that is the replica serving the request. The row is still
// marked cancelled either way, so the operator's intent is recorded even when
// the walk itself keeps going for another page.
var importCancels sync.Map // job id (string) -> context.CancelFunc

// ---- responses -------------------------------------------------------------

// importJobTotals is one per-kind line of import_job.totals.
//
// The column deliberately carries TWO shapes over a job's life: a bare
// progress count while the run is in flight (`{"issues": 42}`, written by the
// throttled tracker) and the structured receipt at the end
// (`{"issues":{"created":40,…}}`, written by FinishImportJob). Normalizing
// both into one struct here means no client has to handle the union — and a
// shape this build does not recognize degrades to zeroes rather than failing
// the response.
type importJobTotals struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
	// Done is the in-flight count, set only while the job is still running.
	Done int `json:"done,omitempty"`
}

type importJobResponse struct {
	ID           string                     `json:"id"`
	WorkspaceID  string                     `json:"workspace_id"`
	ConnectionID string                     `json:"connection_id,omitempty"`
	Source       string                     `json:"source"`
	Status       string                     `json:"status"`
	Terminal     bool                       `json:"terminal"`
	Scope        imports.Scope              `json:"scope"`
	Plan         *imports.Plan              `json:"plan,omitempty"`
	Mapping      *imports.Overrides         `json:"mapping,omitempty"`
	Totals       map[string]importJobTotals `json:"totals"`
	Failures     []imports.Failure          `json:"failures"`
	ArtifactID   string                     `json:"artifact_id,omitempty"`
	CreatedBy    string                     `json:"created_by,omitempty"`
	StartedAt    string                     `json:"started_at,omitempty"`
	FinishedAt   string                     `json:"finished_at,omitempty"`
	CreatedAt    string                     `json:"created_at,omitempty"`
	UpdatedAt    string                     `json:"updated_at,omitempty"`
}

func importJobResponseOf(job db.ImportJob) importJobResponse {
	resp := importJobResponse{
		ID:          uuidToString(job.ID),
		WorkspaceID: uuidToString(job.WorkspaceID),
		Source:      job.Source,
		Status:      job.Status,
		Terminal:    imports.TerminalJobStatus(job.Status),
		Totals:      decodeImportTotals(job.Totals),
		Failures:    decodeImportFailures(job.Failures),
		CreatedAt:   timestampToString(job.CreatedAt),
		UpdatedAt:   timestampToString(job.UpdatedAt),
	}
	if job.ConnectionID.Valid {
		resp.ConnectionID = uuidToString(job.ConnectionID)
	}
	if job.ArtifactID.Valid {
		resp.ArtifactID = uuidToString(job.ArtifactID)
	}
	if job.CreatedBy.Valid {
		resp.CreatedBy = uuidToString(job.CreatedBy)
	}
	if job.StartedAt.Valid {
		resp.StartedAt = job.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if job.FinishedAt.Valid {
		resp.FinishedAt = job.FinishedAt.Time.UTC().Format(time.RFC3339)
	}
	// A malformed scope/plan blob degrades to "nothing to show" rather than
	// failing the whole read: an old row must stay readable after the structs
	// grow a field.
	if scope, err := imports.DecodeScope(job.Scope); err == nil {
		resp.Scope = scope
	}
	if plan, err := imports.DecodePlan(job.Plan); err == nil && plan != nil {
		resp.Plan = plan
	}
	if len(job.Mapping) > 0 {
		var frozen imports.Overrides
		if err := json.Unmarshal(job.Mapping, &frozen); err == nil {
			resp.Mapping = &frozen
		}
	}
	return resp
}

func decodeImportTotals(blob []byte) map[string]importJobTotals {
	out := map[string]importJobTotals{}
	if len(blob) == 0 {
		return out
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(blob, &raw); err != nil {
		return out
	}
	for kind, value := range raw {
		var structured importJobTotals
		if err := json.Unmarshal(value, &structured); err == nil {
			out[kind] = structured
			continue
		}
		var done int
		if err := json.Unmarshal(value, &done); err == nil {
			out[kind] = importJobTotals{Done: done}
		}
	}
	return out
}

func decodeImportFailures(blob []byte) []imports.Failure {
	out := []imports.Failure{}
	if len(blob) == 0 {
		return out
	}
	if err := json.Unmarshal(blob, &out); err != nil {
		return []imports.Failure{}
	}
	return out
}

// writeImportInFlight is the 409 that points at the running job instead of
// cancelling it.
func writeImportInFlight(w http.ResponseWriter, err *imports.ErrImportInFlight) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":  "an import is already running in this workspace",
		"job_id": err.JobID,
		"status": err.Status,
	})
}

// ---- dry run ---------------------------------------------------------------

type dryRunImportRequest struct {
	ConnectionID string `json:"connection_id"`
	// Scope is what the operator chose to import. Absent means "everything the
	// credential can see", which the plan reports back before anyone confirms.
	Scope imports.Scope `json:"scope"`
	// Mapping layers this run's status/priority/container corrections on top
	// of the workspace's stored ones, for the "argue with the report" turn.
	Mapping *imports.Overrides `json:"mapping,omitempty"`
}

// DryRunImport runs fetch → normalize → map → diff and parks the plan on a job
// row. It writes NOTHING into the workspace.
//
// The walk can take minutes, so the work is detached from the request the
// moment it starts and the handler waits only for a bounded window: a small
// workspace answers with the plan itself (200), a large one answers with the
// job id (202) and the client polls GET …/import/jobs/{jid}. Both responses
// carry the job, so a client can use one code path.
func (h *Handler) DryRunImport(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req dryRunImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.ConnectionID), "connection_id")
	if !ok {
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}

	conn, adapter, err := h.importAdapterFor(r.Context(), wsUUID, connUUID)
	if err != nil {
		h.writeImportConnectionError(w, err)
		return
	}
	workspace, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the workspace")
		return
	}

	scope := clampImportScope(req.Scope)
	limits := importLimits()
	runner := h.importRunner(limits)

	job, err := h.openImportJob(r.Context(), runner, imports.OpenRequest{
		WorkspaceID:  wsUUID,
		ConnectionID: connUUID,
		Source:       conn.Source,
		Scope:        scope,
		CreatedBy:    creator,
		Status:       imports.JobDryRun,
	})
	if err != nil {
		var inFlight *imports.ErrImportInFlight
		if errors.As(err, &inFlight) {
			writeImportInFlight(w, inFlight)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to open the import job")
		return
	}

	mapping := importMappingFor(workspace.Settings, conn.Source, adapter.Defaults(), req.Mapping)
	resolver := h.importResolverFor(workspace.Settings, conn.Source, uuidToString(wsUUID), scope, true)

	type previewResult struct {
		job db.ImportJob
		err error
	}
	done := make(chan previewResult, 1)
	h.startImportWork(r.Context(), runner, job, func(ctx context.Context) {
		_, parked, err := runner.Preview(ctx, job, adapter, mapping, resolver)
		switch {
		case err != nil && ctx.Err() != nil:
			// The operator cancelled (or the job timed out) mid-survey.
			// Preview records a broken fetch as a failure; a cancel is not a
			// failure and the receipt must not call it one.
			if cancelled, cerr := runner.Cancel(ctx, parked); cerr == nil {
				parked = cancelled
			}
		case err != nil && !imports.TerminalJobStatus(parked.Status):
			// Preview fails the row itself when the FETCH breaks, but a
			// planning error leaves it open. A job stuck mid-preview would
			// hold the workspace's one in-flight slot forever, so close it.
			if failed, ferr := runner.Fail(ctx, parked, err); ferr == nil {
				parked = failed
			}
		}
		done <- previewResult{job: parked, err: err}
	})

	select {
	case res := <-done:
		if res.err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": "the source could not be surveyed: " + res.err.Error(),
				"job":   importJobResponseOf(res.job),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": importJobResponseOf(res.job)})
	case <-time.After(importDryRunWait()):
		// Still walking. The job row is the handle; the client polls it and
		// watches `import:progress` in the meantime.
		writeJSON(w, http.StatusAccepted, map[string]any{"job": importJobResponseOf(job)})
	}
}

// ---- confirm ---------------------------------------------------------------

type createImportJobRequest struct {
	ConnectionID string `json:"connection_id"`
	// JobID names the previewed job being confirmed. Optional: with no id the
	// workspace's parked preview is used, which is the only one there can be.
	JobID string `json:"job_id,omitempty"`
	// Scope and Mapping are re-checked against what was previewed, never
	// re-applied. A confirm that carries something the plan was not computed
	// with is refused, because the plan is the thing the human authorized.
	Scope   *imports.Scope     `json:"scope,omitempty"`
	Mapping *imports.Overrides `json:"mapping,omitempty"`
}

// CreateImportJob confirms a previewed plan and starts the run.
//
// This is the one gesture that writes into the workspace, so it re-checks
// everything at the moment of confirmation (§4.2): the connection still
// exists, the job is a preview that produced a plan, and the scope/mapping the
// caller believes they are confirming are the ones the plan was computed with.
// The mapping frozen on the row — not today's workspace settings — is what the
// applier runs with.
func (h *Handler) CreateImportJob(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req createImportJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.ConnectionID), "connection_id")
	if !ok {
		return
	}

	job, ok := h.resolveImportJobToConfirm(w, r, wsUUID, connUUID, req.JobID)
	if !ok {
		return
	}
	scope, err := imports.DecodeScope(job.Scope)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "this job's scope can no longer be read; run a new dry run")
		return
	}
	if req.Scope != nil && !sameImportScope(*req.Scope, scope) {
		writeError(w, http.StatusUnprocessableEntity,
			"the scope changed since the preview; run a new dry run and confirm that plan")
		return
	}

	var frozen imports.Overrides
	if len(job.Mapping) > 0 {
		_ = json.Unmarshal(job.Mapping, &frozen)
	}
	if req.Mapping != nil && !importOverridesCovered(frozen, sanitizeImportOverrides(*req.Mapping, job.Source)) {
		writeError(w, http.StatusUnprocessableEntity,
			"the mapping changed since the preview; run a new dry run and confirm that plan")
		return
	}

	conn, adapter, err := h.importAdapterFor(r.Context(), wsUUID, connUUID)
	if err != nil {
		h.writeImportConnectionError(w, err)
		return
	}
	workspace, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the workspace")
		return
	}

	limits := importLimits()
	runner := h.importRunner(limits)
	// The frozen overrides, not ParseOverrides(workspace.Settings): a settings
	// edit between preview and confirm must not change what was authorized.
	mapping := imports.NewMapping(conn.Source, adapter.Defaults(), frozen)
	resolver := h.importResolverFor(workspace.Settings, conn.Source, uuidToString(wsUUID), scope, false)

	applier := &imports.Applier{
		Store:       h.Queries,
		Resolver:    resolver,
		Mapping:     mapping,
		WorkspaceID: wsUUID,
		ImportID:    uuidToString(job.ID),
		Source:      conn.Source,
		Limits:      limits,
	}
	// Attachments need BOTH halves of the seam. When either is missing the
	// applier reports the files as skipped with a reason — never drops them.
	if scope.WantsAttachments() {
		if opener, okOpener := adapter.(imports.AttachmentOpener); okOpener && h.Storage != nil {
			applier.Opener = opener
			applier.Sink = &importAttachmentSink{
				h:           h,
				workspaceID: wsUUID,
				resolver:    resolver,
				maxBytes:    limits.MaxAttachmentBytes,
			}
		}
	}

	h.startImportWork(r.Context(), runner, job, func(ctx context.Context) {
		finished, _, err := runner.Run(ctx, job, adapter, applier)
		if err != nil {
			slog.Warn("import job finished with an error",
				"job_id", uuidToString(job.ID),
				"workspace_id", uuidToString(wsUUID),
				"status", finished.Status,
				"error", err)
		}
	})

	// 202, not 200: the run is out-of-band by design. The caller's next move
	// is to watch the job, not to wait on this request.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id": uuidToString(job.ID),
		"status": imports.JobRunning,
		"job":    importJobResponseOf(job),
	})
}

// resolveImportJobToConfirm finds the previewed job a confirm refers to and
// proves it is confirmable. Every refusal names what the operator should do.
func (h *Handler) resolveImportJobToConfirm(w http.ResponseWriter, r *http.Request, wsUUID, connUUID pgtype.UUID, jobID string) (db.ImportJob, bool) {
	var (
		job db.ImportJob
		err error
	)
	if jobID = strings.TrimSpace(jobID); jobID != "" {
		jobUUID, ok := parseUUIDOrBadRequest(w, jobID, "job_id")
		if !ok {
			return db.ImportJob{}, false
		}
		job, err = h.Queries.GetImportJob(r.Context(), db.GetImportJobParams{ID: jobUUID, WorkspaceID: wsUUID})
	} else {
		job, err = h.Queries.GetActiveImportJob(r.Context(), wsUUID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity,
				"there is no reviewed plan to confirm; run a dry run first")
			return db.ImportJob{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load the import job")
		return db.ImportJob{}, false
	}
	if job.Status == imports.JobRunning {
		writeImportInFlight(w, &imports.ErrImportInFlight{JobID: uuidToString(job.ID), Status: job.Status})
		return db.ImportJob{}, false
	}
	if imports.TerminalJobStatus(job.Status) {
		writeError(w, http.StatusUnprocessableEntity,
			"that import already finished ("+job.Status+"); run a new dry run to import again")
		return db.ImportJob{}, false
	}
	if job.Status != imports.JobAwaitingConfirm || len(job.Plan) == 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"the preview has not produced a plan yet; poll the job until it is awaiting_confirm")
		return db.ImportJob{}, false
	}
	if !job.ConnectionID.Valid || uuidToString(job.ConnectionID) != uuidToString(connUUID) {
		writeError(w, http.StatusUnprocessableEntity,
			"that plan was produced with a different connection; run a dry run with this one")
		return db.ImportJob{}, false
	}
	return job, true
}

// ---- read + cancel ---------------------------------------------------------

// GetImportJob returns one job: status, the plan it parked, the receipt
// numbers and the bounded failure list. This is the polling endpoint.
func (h *Handler) GetImportJob(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	jobUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "jid"), "job id")
	if !ok {
		return
	}
	job, err := h.Queries.GetImportJob(r.Context(), db.GetImportJobParams{ID: jobUUID, WorkspaceID: wsUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "import job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load the import job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": importJobResponseOf(job)})
}

// ListImportJobs returns the workspace's import history, newest first, with
// the active one (if any) named separately so a client does not have to infer
// it from the list.
func (h *Handler) ListImportJobs(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListImportJobs(r.Context(), db.ListImportJobsParams{WorkspaceID: wsUUID, Limit: 20})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list import jobs")
		return
	}
	out := make([]importJobResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, importJobResponseOf(row))
	}
	resp := map[string]any{"jobs": out}
	if active, err := h.Queries.GetActiveImportJob(r.Context(), wsUUID); err == nil {
		resp["active_job_id"] = uuidToString(active.ID)
	}
	writeJSON(w, http.StatusOK, resp)
}

// CancelImportJob stops a run and records that it was a cancel, not a failure.
//
// When the run is owned by THIS process the context is cancelled and the run
// writes its own terminal row — which keeps the partial totals it had already
// earned. Otherwise (a parked preview, or a run on another replica) the row is
// marked cancelled here, which is the honest record of the operator's
// decision even if a walk elsewhere takes another page to notice.
func (h *Handler) CancelImportJob(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	jobUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "jid"), "job id")
	if !ok {
		return
	}
	job, err := h.Queries.GetImportJob(r.Context(), db.GetImportJobParams{ID: jobUUID, WorkspaceID: wsUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "import job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load the import job")
		return
	}
	if imports.TerminalJobStatus(job.Status) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "this import already finished (" + job.Status + ")",
			"job":   importJobResponseOf(job),
		})
		return
	}
	if cancelLocalImportJob(uuidToString(job.ID)) {
		writeJSON(w, http.StatusAccepted, map[string]any{"job": importJobResponseOf(job)})
		return
	}
	runner := h.importRunner(importLimits())
	finished, err := runner.Cancel(r.Context(), job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel the import job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": importJobResponseOf(finished)})
}

// ---- plumbing --------------------------------------------------------------

// writeImportConnectionError maps the three ways loading a connection fails.
// The seal-key case is 503 and must never degrade into anything else.
func (h *Handler) writeImportConnectionError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "import connection not found")
		return
	}
	if writeImportSealError(w, err) {
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to load the import connection: "+err.Error())
}

// openImportJob opens a job row, refusing a RUNNING import and superseding a
// parked preview.
//
// The distinction is the whole §3.7 rule: a run that is writing rows is never
// cancelled out from under itself, while a preview that wrote nothing is just
// a stale piece of paper — blocking a new survey on it would make "argue with
// the report" impossible and leave the workspace wedged behind a plan nobody
// intends to confirm.
func (h *Handler) openImportJob(ctx context.Context, runner *imports.Runner, req imports.OpenRequest) (db.ImportJob, error) {
	active, err := h.Queries.GetActiveImportJob(ctx, req.WorkspaceID)
	switch {
	case err == nil:
		if active.Status == imports.JobRunning {
			return db.ImportJob{}, &imports.ErrImportInFlight{
				JobID:  uuidToString(active.ID),
				Status: active.Status,
			}
		}
		cancelLocalImportJob(uuidToString(active.ID))
		if _, cerr := runner.Cancel(ctx, active); cerr != nil {
			return db.ImportJob{}, fmt.Errorf("supersede the parked preview: %w", cerr)
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Nothing in flight — the normal first import.
	default:
		return db.ImportJob{}, err
	}
	return runner.Open(ctx, req)
}

// startImportWork detaches a run from the request that started it. The request
// is over in milliseconds and the walk is not, so a client closing its
// connection must not cancel an import it already confirmed.
func (h *Handler) startImportWork(parent context.Context, runner *imports.Runner, job db.ImportJob, fn func(ctx context.Context)) {
	jobID := uuidToString(job.ID)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), importJobTimeout())
	importCancels.Store(jobID, cancel)
	go func() {
		defer cancel()
		defer importCancels.Delete(jobID)
		defer func() {
			if rec := recover(); rec != nil {
				// There is no middleware.Recoverer out here: an unrecovered
				// panic takes the process down AND leaves the row in a
				// non-terminal state, which would hold the workspace's one
				// in-flight slot forever.
				slog.Error("import job panicked", "job_id", jobID, "panic", rec)
				failCtx, failCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer failCancel()
				_, _ = runner.Fail(failCtx, job, fmt.Errorf("the import crashed: %v", rec))
			}
		}()
		fn(ctx)
	}()
}

// cancelLocalImportJob cancels a run owned by this process and reports whether
// there was one.
func cancelLocalImportJob(jobID string) bool {
	value, ok := importCancels.Load(jobID)
	if !ok {
		return false
	}
	if cancel, isFunc := value.(context.CancelFunc); isFunc {
		cancel()
		return true
	}
	return false
}

// clampImportScope applies the instance cap to a caller-chosen scope. 0 means
// uncapped on both sides; an operator cap is a ceiling, never a floor.
func clampImportScope(scope imports.Scope) imports.Scope {
	ceiling := config.Int(cfgImportMaxIssues, 0)
	if ceiling > 0 && (scope.MaxIssues == 0 || scope.MaxIssues > ceiling) {
		scope.MaxIssues = ceiling
	}
	return scope
}

// sameImportScope compares two scopes by their stored form, which is exactly
// what the job row round-trips.
func sameImportScope(a, b imports.Scope) bool {
	left, err := json.Marshal(clampImportScope(a))
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

// importOverridesCovered reports whether every correction the caller is
// confirming is present, unchanged, in the mapping frozen on the job. It is a
// subset check rather than equality because the frozen mapping legitimately
// carries the workspace's stored overrides as well as this run's.
func importOverridesCovered(frozen, requested imports.Overrides) bool {
	return mapCovered(frozen.Status, requested.Status) &&
		mapCovered(frozen.Priority, requested.Priority) &&
		mapCovered(frozen.Containers, requested.Containers)
}

func mapCovered(frozen, requested map[string]string) bool {
	for key, want := range requested {
		if got, ok := frozen[key]; !ok || got != want {
			return false
		}
	}
	return true
}
