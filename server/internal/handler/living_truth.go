package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/config"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// LIVING TRUTH — Tier 2: the tracker saying out loud where it has probably
// stopped being true (docs/living-truth-plan.md).
//
// Tier 1 lives in the GitHub webhook and the failed-task sweep: those are
// FACTS (a PR with close intent merged; a task died with no PR in flight), so
// they move the issue and write provenance. This file is the other half, and
// it is deliberately the opposite kind of thing: a stale signal is INFERENCE —
// "nobody has touched this in five days" is not the same claim as "this is
// done" — so it is computed on read and it never, under any threshold, writes.
//
// Two consequences worth stating, because both are load-bearing:
//
//  1. NOTHING IS STORED. There is no `stale` column and no sweeper stamping
//     one, which is what keeps the staleness signal from itself going stale —
//     the classic failure where the freshness indicator is the least fresh
//     thing on the page. Every row here is derived from issue.updated_at, the
//     comment table, the task queue and the PR links at the moment of the call.
//  2. IT IS A SEPARATE ENDPOINT, not a join into the list/board query. The
//     board is the hot path; staleness is a side panel. The frontend fetches
//     this beside the list and merges client-side, so a slow day for this
//     query can never slow down the board.
//
// Thresholds come from instance config on every call (no process-lifetime
// caching), so an operator changing AGORA_STALE_* in Settings → Configs sees
// the effect on the next refresh.

// Stale reasons. Four, and the frontend must degrade an unknown fifth to a
// generic "stale" rendering rather than dropping the row (the enum-drift rule
// in CLAUDE.md) — which is exactly why they ship as strings the query emits
// rather than as an integer the two sides have to agree on.
const (
	staleReasonIdle         = "idle"
	staleReasonReviewDone   = "review_done"
	staleReasonReopenedWork = "reopened_work"
	staleReasonBlockedQuiet = "blocked_quiet"

	staleDefaultIdleDays    = 5
	staleDefaultReviewDays  = 2
	staleDefaultBlockedDays = 7
	staleMaxThresholdDays   = 3650
	// 0 is allowed and means "flag as soon as there is any gap at all" — a
	// deliberate operator choice, not a bug. Negative is not: it would put the
	// cutoff in the future and flag the whole workspace.
	staleMinThresholdDays = 0
)

// StaleIssueResponse is one stale row as every surface sees it — the endpoint,
// the assistant tool, and (through them) the list indicator and the issue
// detail sentence. The contract is fixed in the plan: issue_id, identifier,
// title, status, reason, since.
//
// `since` is the instant the matching rule MEASURED FROM, not "when this went
// stale": last activity for the two quiet rules, the moment the last linked PR
// resolved for review_done, and when the still-open PR was opened for
// reopened_work. Handing back the measured-from instant lets the caller render
// an age without asking a second question, and keeps the age honest if the
// threshold is changed later.
type StaleIssueResponse struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Since      string `json:"since"`
}

// staleThresholds is the instance's current answer to "how long is too long".
type staleThresholds struct {
	IdleDays       int32
	ReviewDoneDays int32
	BlockedDays    int32
}

// currentStaleThresholds reads the three AGORA_STALE_* keys, clamping each into
// a sane band. A negative threshold would make now() - interval land in the
// future and flag the entire workspace; an absurd one would silently disable
// the signal without saying so. Clamping keeps a typo in Settings → Configs
// from looking like a product bug.
func currentStaleThresholds() staleThresholds {
	return staleThresholds{
		IdleDays:       clampStaleDays(config.Int("AGORA_STALE_IDLE_DAYS", staleDefaultIdleDays)),
		ReviewDoneDays: clampStaleDays(config.Int("AGORA_STALE_REVIEW_DONE_DAYS", staleDefaultReviewDays)),
		BlockedDays:    clampStaleDays(config.Int("AGORA_STALE_BLOCKED_DAYS", staleDefaultBlockedDays)),
	}
}

func clampStaleDays(days int) int32 {
	if days < staleMinThresholdDays {
		return staleMinThresholdDays
	}
	if days > staleMaxThresholdDays {
		return staleMaxThresholdDays
	}
	return int32(days)
}

// listStaleIssues is the one read both the endpoint and the assistant tool go
// through, so the two can never drift into different definitions of stale.
// projectID and restrictToUser are optional (zero value = not filtered).
func (h *Handler) listStaleIssues(ctx context.Context, workspaceID, projectID, restrictToUser pgtype.UUID) ([]StaleIssueResponse, error) {
	th := currentStaleThresholds()
	rows, err := h.Queries.ListStaleIssues(ctx, db.ListStaleIssuesParams{
		WorkspaceID:    workspaceID,
		ProjectID:      projectID,
		RestrictToUser: restrictToUser,
		IdleDays:       th.IdleDays,
		ReviewDoneDays: th.ReviewDoneDays,
		BlockedDays:    th.BlockedDays,
	})
	if err != nil {
		return nil, err
	}

	prefix := h.getIssuePrefix(ctx, workspaceID)
	out := make([]StaleIssueResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, StaleIssueResponse{
			IssueID:    uuidToString(row.IssueID),
			Identifier: prefix + "-" + strconv.Itoa(int(row.Number)),
			Title:      row.Title,
			Status:     row.Status,
			Reason:     row.Reason,
			Since:      staleTimestamp(row.Since),
		})
	}
	return out, nil
}

// staleTimestamp renders `since` in the same RFC3339-ish shape the rest of the
// API uses. An invalid timestamp cannot happen for a row the query returned
// (every branch of the CASE coalesces to a NOT NULL column), but an empty
// string is a safer answer than a zero date the frontend would render as 1970.
func staleTimestamp(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

// ListStaleIssues (GET /api/issues/staleness) — workspace-scoped, read-only.
//
// Optional ?project_id= narrows to one project, which is what the board does
// when the user is looking at a single project rather than the whole workspace.
// The non-owner visibility gate is the same one the issue list applies, so this
// endpoint can never show a restricted member an issue the list would hide from
// them.
func (h *Handler) ListStaleIssues(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}

	var projectUUID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("project_id")); raw != "" {
		parsed, pok := parseUUIDOrBadRequest(w, raw, "project_id")
		if !pok {
			return
		}
		projectUUID = parsed
	}

	stale, err := h.listStaleIssues(r.Context(), wsUUID, projectUUID, h.issueVisibilityRestriction(r))
	if err != nil {
		slog.Warn("staleness: list failed", "workspace_id", uuidToString(wsUUID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute issue staleness")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"stale": stale})
}

// ---------------------------------------------------------------------------
// list_stale_issues — the assistant's read of the same signal
// ---------------------------------------------------------------------------

// assistantListStaleIssuesArgs mirrors the endpoint: one workspace, optionally
// one project. `project` takes a UUID or a title, like every other project
// argument in the catalog, because the model has a title far more often than
// it has an id.
type assistantListStaleIssuesArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
}

// assistantStaleNote rides with every answer. The model's instinct on reading
// "this issue has not moved in nine days" is to fix it, and fixing it means
// writing a status nobody asked it to write — off a signal that is, by
// construction, a guess. So the payload says what the row is and what it is
// not, in the same breath as the rows themselves.
const assistantStaleNote = "These are GUESSES from timestamps, not facts: an issue can be perfectly healthy and still " +
	"appear here (long-running work, a deliberate pause, a PR reviewed outside Agora). Report them, name the reason " +
	"and the age, and let the user decide. Never change a status because a row appeared in this list."

// assistantListStaleIssues answers "what in this workspace has probably stopped
// being true" from exactly the query GET /api/issues/staleness runs — same
// thresholds, same visibility gate, same four reasons. Two surfaces, one
// definition of stale.
//
// The scope envelope is the EXACT kind (the query has no LIMIT, so every
// matching row came back and its length IS the total). That matters here more
// than usual: "you have 3 stale issues" is the sentence this tool exists to
// make sayable, and it is only sayable when the count is real.
func (h *Handler) assistantListStaleIssues(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantListStaleIssuesArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	var projectUUID pgtype.UUID
	if ref := strings.TrimSpace(args.ProjectID); ref != "" {
		project, perr := h.assistantResolveProject(ctx, ws, ref)
		if perr != nil {
			return nil, perr
		}
		projectUUID = project.ID
	}

	stale, err := h.listStaleIssues(ctx, ws.ID, projectUUID, assistantVisibilityRestriction(role, caller.UUID))
	if err != nil {
		slog.Warn("assistant: list stale issues failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not compute issue staleness")
	}

	// The link is built the same way every other issue chip in the catalog is,
	// so a stale row the model quotes is clickable rather than a bare id.
	type staleRow struct {
		StaleIssueResponse
		WorkspaceSlug string `json:"workspace_slug"`
		URLPath       string `json:"url_path"`
	}
	rows := make([]staleRow, 0, len(stale))
	for _, s := range stale {
		rows = append(rows, staleRow{
			StaleIssueResponse: s,
			WorkspaceSlug:      ws.Slug,
			URLPath:            assistantIssueURLPath(ws.Slug, s.Identifier),
		})
	}

	th := currentStaleThresholds()
	return assistantScopedResult(map[string]any{
		"stale":          rows,
		"returned_count": len(rows),
		"reasons": map[string]string{
			staleReasonIdle:         "in progress, but no task, no open pull request and nothing said for " + strconv.Itoa(int(th.IdleDays)) + "+ days",
			staleReasonReviewDone:   "in review, but every linked pull request merged or closed " + strconv.Itoa(int(th.ReviewDoneDays)) + "+ days ago",
			staleReasonReopenedWork: "marked done, but a linked pull request is open again",
			staleReasonBlockedQuiet: "blocked and untouched for " + strconv.Itoa(int(th.BlockedDays)) + "+ days",
		},
		"note": assistantStaleNote,
	}, assistantExactScope(ws.Slug, len(rows)))
}

// ---------------------------------------------------------------------------
// Tier 1 — provenance on every derived move
// ---------------------------------------------------------------------------

// postDerivedStatusProvenance writes the one-line system comment that explains a
// status Agora moved by itself.
//
// This is the whole of Tier 1a, and the reason it exists is trust rather than
// bookkeeping. Agora already advanced issues on a merged close-intent PR and
// already reset a stuck issue when its task died — correctly, and completely
// silently. A silent auto-move reads to the team as "who moved my issue", and
// once that question is asked the derivation is disabled. The same move WITH a
// sentence saying what caused it reads as the tracker doing its job. Same
// write, opposite effect on trust.
//
// It reuses the existing system-comment path exactly — author_type 'system'
// with the zero UUID (the column is NOT NULL; the frontend branches on
// author_type, never on the id), type 'system' — so these land in the timeline
// beside the child-done notice and the no-repo nudge, and the notification /
// subscriber listeners short-circuit on them as they already do for every other
// system comment.
//
// BEST EFFORT, ALWAYS. The status move has already committed by the time this
// runs; a failed comment must never turn a completed transition into an error
// path. Every failure logs and returns.
func (h *Handler) postDerivedStatusProvenance(ctx context.Context, issue db.Issue, content string) {
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("provenance: create system comment failed",
			"error", err, "issue_id", uuidToString(issue.ID))
		return
	}

	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment":             commentToResponse(comment, nil, nil),
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
	})
}
