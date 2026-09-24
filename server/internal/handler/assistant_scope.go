package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// THE COVERAGE ENVELOPE — what every list-shaped tool result says about itself.
//
// docs/agora-assistant-final-plan.md §3, "Return honest data": a list result
// must describe scope, observation time, date range, pagination, truncation and
// partial failures, and exact totals must come from a matching aggregate query.
//
// The failure this exists to end is small and completely invisible: a tool caps
// at 20 rows, the model counts them, and the user reads "you have 20 open
// issues" over a workspace that has 143. Nothing errors. The chart is drawn.
// The number is wrong. The only fix is for the result itself to carry the
// difference between "what I returned" and "what there is", in a shape the
// prompt can require the model to quote.
//
//	"scope": {
//	  "workspaces_checked": ["main"],   // which workspaces this answer covers
//	  "failed": [],                     // checked but unreadable — named, never dropped
//	  "truncated": true,                // the list hit its cap; there is more
//	  "total": 143,                     // a REAL count, or null when unknown
//	  "window": {                       // analytics only: the range measured
//	    "from": "2026-09-17T00:00:00+04:00",
//	    "to":   "2026-09-17T23:50:12+04:00",
//	    "timezone": "Asia/Dubai"
//	  }
//	}
//
// `total` is null far more often than it is a number, and that is the point: a
// null is a fact ("I do not know how many there are"), whereas a page length
// dressed up as a total is a lie the model will repeat with confidence. It is
// only ever filled from a count query that runs the SAME predicates as the list
// (CountIssues), or from a list that was exhaustive by construction — a query
// with no LIMIT returned every row, so its length IS the total.

// assistantWindow is the exact instant range a time-scoped result covers,
// rendered in the timezone it was computed in.
type assistantWindow struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
}

// assistantScope is the coverage envelope attached to every list-shaped result.
type assistantScope struct {
	WorkspacesChecked []string         `json:"workspaces_checked"`
	Failed            []string         `json:"failed"`
	Truncated         bool             `json:"truncated"`
	Total             *int64           `json:"total"`
	Window            *assistantWindow `json:"window,omitempty"`
}

// assistantScopeBuilder accumulates coverage across a fan-out. Unscoped tools
// (list_my_issues, activity_digest, inbox_summary) visit every membership and
// must report BOTH halves: an answer that silently drops the workspace whose
// query errored is wrong even when every row it did return is right.
type assistantScopeBuilder struct {
	checked   []string
	failed    []string
	truncated bool
	total     int64
	// totalKnown goes false the moment any one workspace cannot produce an
	// exact count. A partial sum is not a total.
	totalKnown bool
	window     *assistantWindow
}

func newAssistantScopeBuilder() *assistantScopeBuilder {
	return &assistantScopeBuilder{totalKnown: true}
}

// checkedWorkspace records a workspace that answered, with its exact total when
// one is available (nil = unknown).
func (b *assistantScopeBuilder) checkedWorkspace(slug string, total *int64, truncated bool) {
	b.checked = append(b.checked, slug)
	b.truncated = b.truncated || truncated
	if total == nil {
		b.totalKnown = false
		return
	}
	b.total += *total
}

// failedWorkspace records one that did not. A failure also sinks the total: the
// rows behind it were never counted.
func (b *assistantScopeBuilder) failedWorkspace(slug string) {
	b.failed = append(b.failed, slug)
	b.totalKnown = false
}

func (b *assistantScopeBuilder) withWindow(w *assistantWindow) *assistantScopeBuilder {
	b.window = w
	return b
}

func (b *assistantScopeBuilder) scope() assistantScope {
	s := assistantScope{
		WorkspacesChecked: b.checked,
		Failed:            b.failed,
		Truncated:         b.truncated,
		Window:            b.window,
	}
	if s.WorkspacesChecked == nil {
		s.WorkspacesChecked = []string{}
	}
	if s.Failed == nil {
		s.Failed = []string{}
	}
	if b.totalKnown {
		total := b.total
		s.Total = &total
	}
	return s
}

// assistantExactScope is the envelope for a single-workspace list whose query
// had no LIMIT: every row came back, so the count is exact and nothing is
// truncated.
func assistantExactScope(slug string, rows int) assistantScope {
	b := newAssistantScopeBuilder()
	total := int64(rows)
	b.checkedWorkspace(slug, &total, false)
	return b.scope()
}

// assistantCappedScope is the envelope for a single-workspace list that ran
// under a LIMIT. `total` is nil when no matching count query exists — which is
// honest, and is the case the prompt turns into "showing the first 20 — there
// are more".
func assistantCappedScope(slug string, rows, limit int, total *int64) assistantScope {
	b := newAssistantScopeBuilder()
	truncated := rows >= limit
	if total != nil {
		truncated = *total > int64(rows)
	}
	b.checkedWorkspace(slug, total, truncated)
	return b.scope()
}

// assistantScopedResult marshals a tool payload with its coverage envelope.
// Every list-returning tool ends in this call, so "did you remember the scope"
// is answered by the type system rather than by review.
func assistantScopedResult(payload map[string]any, scope assistantScope) (json.RawMessage, error) {
	payload["scope"] = scope
	return json.Marshal(payload)
}

// ---------------------------------------------------------------------------
// Timezone
// ---------------------------------------------------------------------------

// assistantTimezone resolves the zone a window must be computed in, in the only
// order that is defensible:
//
//  1. the timezone the CLIENT captured when this message was sent
//     (assistant_run.context_timezone, migration 197) — it is the browser's,
//     so it is where the user actually is right now;
//  2. the user's stored profile timezone, for a run that carried none;
//  3. UTC.
//
// Never the server's local zone: that is an accident of where the container
// runs, and it would make "today" mean different things on two deployments of
// the same product.
func (h *Handler) assistantTimezone(ctx context.Context, caller assistantCaller) string {
	if tz := strings.TrimSpace(assistant.TimezoneFrom(ctx)); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if user, err := h.Queries.GetUser(ctx, caller.UUID); err == nil && user.Timezone.Valid {
		if tz := strings.TrimSpace(user.Timezone.String); tz != "" {
			if _, lerr := time.LoadLocation(tz); lerr == nil {
				return tz
			}
			slog.Warn("assistant: unknown profile timezone, falling back to UTC",
				"user_id", util.UUIDToString(caller.UUID), "timezone", tz)
		}
	}
	return "UTC"
}

// assistantDayWindow turns "the last n days" into the caller's calendar window
// plus the envelope that describes it. n = 1 is today, and an issue filed at
// 23:50 local belongs to it whatever the UTC date has already become.
func (h *Handler) assistantDayWindow(ctx context.Context, caller assistantCaller, days int) (time.Time, time.Time, *assistantWindow) {
	tz := h.assistantTimezone(ctx, caller)
	loc := assistant.LoadLocation(tz)
	from, to := assistant.DayWindow(time.Now(), loc, days)
	return from, to, &assistantWindow{
		From:     from.Format(time.RFC3339),
		To:       to.Format(time.RFC3339),
		Timezone: tz,
	}
}

// assistantRollingWindow describes a window whose boundary is a fixed interval
// back from now rather than a local midnight — a query whose range is baked
// into its SQL. The instants are absolute; the timezone is only how they are
// rendered, and saying otherwise would be the same lie as an invented total.
func (h *Handler) assistantRollingWindow(ctx context.Context, caller assistantCaller, d time.Duration) (time.Time, time.Time, *assistantWindow) {
	tz := h.assistantTimezone(ctx, caller)
	loc := assistant.LoadLocation(tz)
	to := time.Now().In(loc)
	from := to.Add(-d)
	return from, to, &assistantWindow{
		From:     from.Format(time.RFC3339),
		To:       to.Format(time.RFC3339),
		Timezone: tz,
	}
}

// ---------------------------------------------------------------------------
// Exact totals
// ---------------------------------------------------------------------------

// assistantCountIssues (with its per-status twin below, which list_my_issues
// uses) is the only source of a non-null scope.total for an issue list. It runs
// CountIssues, whose predicates are a line-by-line copy of ListIssues' —
// archive filter and non-owner visibility gate included — so the number
// describes the same set the rows were drawn from.
//
// A failed count is nil, never zero: "I could not count" and "there are none"
// are different answers, and only one of them is safe to put in a sentence.
func (h *Handler) assistantCountIssues(ctx context.Context, params db.CountIssuesParams) *int64 {
	total, err := h.Queries.CountIssues(ctx, params)
	if err != nil {
		slog.Warn("assistant: count issues failed",
			"workspace_id", util.UUIDToString(params.WorkspaceID), "error", err)
		return nil
	}
	return &total
}

// assistantIssueStatuses is every value the issue.status CHECK constraint
// allows. An unfiltered by_status names each of them, zero included, so "how
// many are blocked" reads a 0 instead of inferring one from a missing key.
var assistantIssueStatuses = []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"}

// assistantCountIssuesByStatus is the per-status twin of assistantCountIssues:
// CountIssuesByStatus runs the same predicates as ListIssues and CountIssues,
// grouped by status, so the split sums to the same exact total.
//
// A failed count is nil, never an empty split, for the same reason a failed
// total is nil rather than zero.
func (h *Handler) assistantCountIssuesByStatus(ctx context.Context, params db.CountIssuesByStatusParams) []db.CountIssuesByStatusRow {
	rows, err := h.Queries.CountIssuesByStatus(ctx, params)
	if err != nil {
		slog.Warn("assistant: count issues by status failed",
			"workspace_id", util.UUIDToString(params.WorkspaceID), "error", err)
		return nil
	}
	if rows == nil {
		// Counted, and there are none: an empty split, not an unknown one.
		rows = []db.CountIssuesByStatusRow{}
	}
	return rows
}

// assistantStatusTally sums exact per-status counts across one workspace or a
// fan-out of them. It is only ever read next to a known scope.total: one
// workspace that could not be counted sinks the total, and with it the split.
type assistantStatusTally struct {
	byStatus map[string]int64
}

// newAssistantStatusTally seeds the split. With a status filter only that
// status is named — seeding the others would state "0 todo" about issues the
// filter never looked at.
func newAssistantStatusTally(statusFilter string) *assistantStatusTally {
	t := &assistantStatusTally{byStatus: map[string]int64{}}
	if statusFilter != "" {
		t.byStatus[statusFilter] = 0
		return t
	}
	for _, status := range assistantIssueStatuses {
		t.byStatus[status] = 0
	}
	return t
}

// add folds one workspace's CountIssuesByStatus rows in and returns that
// workspace's exact total — nil when the count failed (rows == nil).
func (t *assistantStatusTally) add(rows []db.CountIssuesByStatusRow) *int64 {
	if rows == nil {
		return nil
	}
	var total int64
	for _, row := range rows {
		t.byStatus[row.Status] += row.Count
		total += row.Count
	}
	return &total
}

// assistantOptionalText is pgtype.Text for a filter the model may have omitted.
func assistantOptionalText(value string) pgtype.Text {
	if v := strings.TrimSpace(value); v != "" {
		return pgtype.Text{String: v, Valid: true}
	}
	return pgtype.Text{}
}

// assistantRosterScope is the envelope for a list OF workspaces, where the rows
// and the coverage are the same thing: every workspace named is both a row and
// a workspace checked, and there are no more of either.
func assistantRosterScope(slugs []string) assistantScope {
	if slugs == nil {
		slugs = []string{}
	}
	total := int64(len(slugs))
	return assistantScope{
		WorkspacesChecked: slugs,
		Failed:            []string{},
		Truncated:         false,
		Total:             &total,
	}
}
