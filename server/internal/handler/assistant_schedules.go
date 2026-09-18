package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Scheduled refresh of a pinned report (docs/assistant-domain-plan.md
// §Phase 2b).
//
// A cadence on a pin is the only thing in the assistant that spends money with
// nobody watching, so every decision here is about bounding that:
//
//   - PRESETS, never cron. daily / weekdays / weekly + HH:MM + IANA timezone.
//     The tightest schedule anyone can express is once a day, so unattended
//     spend has a floor that does not depend on nobody mistyping a cron field.
//   - The slot is CLAIMED BEFORE the run (see ClaimDueAssistantReportSchedule):
//     at-most-once per slot. A failed refresh waits for the next slot rather
//     than retrying, because a retry storm against a model API is worse than a
//     report that is one day stale.
//   - A session that is already answering something is SKIPPED, not queued.
//     The owner's own conversation always wins; the robot never makes them wait
//     behind it.
//   - Two switches stop everything: AGORA_ASSISTANT_SCHEDULES_ENABLED (this
//     feature) and the assistant's own availability. Both are read every tick,
//     so turning one off in Settings → Configs takes effect within a minute
//     with no redeploy.
//
// Owner-only and human-actor-gated, exactly like pin/unpin: a standing schedule
// is standing spend AND standing disclosure — every refresh rewrites what a
// whole workspace reads. An agent token must not be able to create one.

// ---------------------------------------------------------------------------
// Cadence vocabulary
// ---------------------------------------------------------------------------

const (
	scheduleFrequencyDaily    = "daily"
	scheduleFrequencyWeekdays = "weekdays"
	scheduleFrequencyWeekly   = "weekly"
)

const (
	// reportScheduleBatch bounds one tick's work. Schedules are few by
	// construction — one per published report, at most one run a day each — so
	// a tick that wanted to start more than this is a bug, and stopping at the
	// limit leaves the rest for the next minute instead of starting a stampede.
	reportScheduleBatch = 50

	// reportScheduleErrorMax truncates what a failure writes to last_error. The
	// column feeds a badge tooltip, and a provider's multi-kilobyte error body
	// would be stored forever to be displayed as three lines.
	reportScheduleErrorMax = 500
)

// Outcomes as the project page reads them. "skipped" is deliberately distinct
// from "failed": nothing went wrong, the slot simply found the owner already
// talking to the assistant.
const (
	reportScheduleStatusOK      = "ok"
	reportScheduleStatusSkipped = "skipped"
	reportScheduleStatusFailed  = "failed"
)

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

// ReportScheduleResponse is the cadence as everyone in the workspace sees it —
// attached to the report rows, not fetched on its own. Members read it to know
// how fresh the report is and when it refreshes next; only the owner can change
// it.
//
// It carries no created_by and no session pointer: who set the cadence and
// which conversation it runs in are the owner's business, while "daily at 09:00,
// last run ok, next at …" is what a reader needs to trust the numbers.
type ReportScheduleResponse struct {
	Frequency string `json:"frequency"`
	Time      string `json:"time"`
	// Weekday is null for daily/weekdays. The key is always present so a client
	// can bind a form field to it without branching on shape.
	Weekday    *int32  `json:"weekday"`
	Timezone   string  `json:"timezone"`
	Enabled    bool    `json:"enabled"`
	LastRunAt  *string `json:"last_run_at"`
	LastStatus string  `json:"last_status"`
	NextRunAt  string  `json:"next_run_at"`
}

// reportScheduleEnvelope keeps the PUT response an object with one named key,
// so the endpoint can grow a sibling field later without changing the shape
// installed clients already parse.
type reportScheduleEnvelope struct {
	Schedule ReportScheduleResponse `json:"schedule"`
}

type putReportScheduleRequest struct {
	Frequency string `json:"frequency"`
	Time      string `json:"time"`
	Weekday   *int32 `json:"weekday"`
	Timezone  string `json:"timezone"`
}

func reportScheduleToResponse(s db.AssistantReportSchedule) ReportScheduleResponse {
	resp := ReportScheduleResponse{
		Frequency:  s.Frequency,
		Time:       s.AtTime,
		Timezone:   s.Timezone,
		Enabled:    s.Enabled,
		LastStatus: s.LastStatus,
		NextRunAt:  timestampToString(s.NextRunAt),
	}
	if s.Weekday.Valid {
		weekday := s.Weekday.Int32
		resp.Weekday = &weekday
	}
	if s.LastRunAt.Valid {
		at := timestampToString(s.LastRunAt)
		resp.LastRunAt = &at
	}
	return resp
}

// ---------------------------------------------------------------------------
// Schedule math
// ---------------------------------------------------------------------------

// parseScheduleTime accepts HH:MM in 24-hour form, leading zero optional on the
// hour. Rejecting everything else here is what lets nextRunAt treat the stored
// string as data rather than input.
func parseScheduleTime(atTime string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(atTime), ":")
	if len(parts) != 2 || len(parts[0]) < 1 || len(parts[0]) > 2 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", atTime)
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", atTime)
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", atTime)
	}
	return hour, minute, nil
}

// nextRunAt returns the first moment strictly after `after` that satisfies the
// cadence, as a real instant.
//
// The arithmetic is done on CALENDAR FIELDS in the schedule's own location —
// each candidate is rebuilt with time.Date(y, m, d+i, hour, minute, …, loc)
// rather than by adding 24h to an instant. That is the whole DST story: "09:00
// in New York" stays 09:00 across a transition, and the interval between two
// runs is 23 or 25 hours on those two days a year, which is what the owner
// asked for. Adding durations would have drifted the wall clock by an hour and
// kept it there.
//
// Two stdlib behaviors are inherited on purpose, because they are the only
// sensible answers and the alternative is inventing our own: a wall time that
// does not exist (02:30 on a spring-forward day) normalizes forward into the
// new offset, and a time that happens twice on a fall-back day fires once.
//
// weekday is JS getDay() numbering (0=Sunday..6=Saturday) and is read only for
// the weekly cadence.
func nextRunAt(frequency, atTime string, weekday int, timezone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown timezone %q", timezone)
	}
	hour, minute, err := parseScheduleTime(atTime)
	if err != nil {
		return time.Time{}, err
	}
	switch frequency {
	case scheduleFrequencyDaily, scheduleFrequencyWeekdays:
	case scheduleFrequencyWeekly:
		if weekday < 0 || weekday > 6 {
			return time.Time{}, fmt.Errorf("weekday %d is outside 0..6", weekday)
		}
	default:
		return time.Time{}, fmt.Errorf("unknown frequency %q", frequency)
	}

	local := after.In(loc)
	year, month, day := local.Date()
	// Eight candidates covers every preset: the worst case is a weekly schedule
	// whose weekday is today but whose time has already passed, which lands
	// seven days out.
	for i := 0; i < 8; i++ {
		candidate := time.Date(year, month, day+i, hour, minute, 0, 0, loc)
		if !candidate.After(after) {
			continue
		}
		switch frequency {
		case scheduleFrequencyDaily:
			return candidate, nil
		case scheduleFrequencyWeekdays:
			if wd := candidate.Weekday(); wd != time.Saturday && wd != time.Sunday {
				return candidate, nil
			}
		case scheduleFrequencyWeekly:
			if int(candidate.Weekday()) == weekday {
				return candidate, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("no run within eight days for %s at %s", frequency, atTime)
}

// ---------------------------------------------------------------------------
// PUT /api/assistant/artifacts/{id}/pins/{pinId}/schedule
// ---------------------------------------------------------------------------

// loadReportPinForOwner resolves the {id}/{pinId} pair to a pin the CALLER OWNS
// THE ARTIFACT OF, writing the refusal itself.
//
// Same chain as unpin, and deliberately stricter than it: unpinning is also
// open to a workspace admin (the workspace can withdraw something from its own
// page), but scheduling is not. A cadence spends the owner's model budget and
// rewrites the owner's artifact, so only the owner may set one.
func (h *Handler) loadReportPinForOwner(w http.ResponseWriter, r *http.Request, userID string) (db.AssistantArtifactPin, bool) {
	artifactUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "artifact id")
	if !ok {
		return db.AssistantArtifactPin{}, false
	}
	pinUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "pinId"), "pin id")
	if !ok {
		return db.AssistantArtifactPin{}, false
	}
	// Ownership of the artifact is proved BEFORE the pin is read, so a caller
	// who owns nothing learns nothing about which pin ids exist.
	artifact, err := h.loadAssistantArtifactForUser(r.Context(), uuidToString(artifactUUID), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return db.AssistantArtifactPin{}, false
	}
	pin, err := h.Queries.GetAssistantArtifactPin(r.Context(), pinUUID)
	if err != nil || uuidToString(pin.ArtifactID) != uuidToString(artifact.ID) {
		writeError(w, http.StatusNotFound, "report not found")
		return db.AssistantArtifactPin{}, false
	}
	return pin, true
}

// PutAssistantReportSchedule sets — or replaces — the standing refresh cadence
// of one published report. Create-or-replace rather than POST/PATCH because a
// pin has at most one cadence (UNIQUE pin_id), so "this is the schedule now" is
// the only operation the surface needs.
func (h *Handler) PutAssistantReportSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	pin, ok := h.loadReportPinForOwner(w, r, userID)
	if !ok {
		return
	}

	var body putReportScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	frequency := strings.ToLower(strings.TrimSpace(body.Frequency))
	switch frequency {
	case scheduleFrequencyDaily, scheduleFrequencyWeekdays, scheduleFrequencyWeekly:
	default:
		writeError(w, http.StatusBadRequest, "frequency must be daily, weekdays or weekly")
		return
	}
	atTime := strings.TrimSpace(body.Time)
	if _, _, err := parseScheduleTime(atTime); err != nil {
		writeError(w, http.StatusBadRequest, "time must be HH:MM")
		return
	}
	// A weekly cadence without a day is not a cadence, so it is refused rather
	// than defaulted: guessing Monday here would silently publish a report on a
	// day nobody chose.
	weekday := pgtype.Int4{}
	weekdayValue := 0
	if frequency == scheduleFrequencyWeekly {
		if body.Weekday == nil || *body.Weekday < 0 || *body.Weekday > 6 {
			writeError(w, http.StatusBadRequest, "weekly needs a weekday between 0 (Sunday) and 6 (Saturday)")
			return
		}
		weekdayValue = int(*body.Weekday)
		weekday = pgtype.Int4{Int32: *body.Weekday, Valid: true}
	}
	// The timezone is stored, not resolved to an offset: an offset would be
	// wrong for half the year, and "09:00 where the owner lives" is the thing
	// actually being asked for.
	timezone := strings.TrimSpace(body.Timezone)
	if _, err := time.LoadLocation(timezone); err != nil || timezone == "" {
		writeError(w, http.StatusBadRequest, "timezone must be a known IANA name (e.g. Asia/Tashkent)")
		return
	}

	next, err := nextRunAt(frequency, atTime, weekdayValue, timezone, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "this schedule has no next run")
		return
	}

	schedule, err := h.Queries.UpsertAssistantReportSchedule(r.Context(), db.UpsertAssistantReportScheduleParams{
		PinID:     pin.ID,
		Frequency: frequency,
		AtTime:    atTime,
		Weekday:   weekday,
		Timezone:  timezone,
		CreatedBy: parseUUID(userID),
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	})
	if err != nil {
		slog.Warn("reports: upsert schedule failed", "pin_id", uuidToString(pin.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "could not save this schedule")
		return
	}

	h.publishReportEvent(protocol.EventReportScheduleChanged, userID, pin)
	writeJSON(w, http.StatusOK, reportScheduleEnvelope{Schedule: reportScheduleToResponse(schedule)})
}

// ---------------------------------------------------------------------------
// DELETE /api/assistant/artifacts/{id}/pins/{pinId}/schedule
// ---------------------------------------------------------------------------

// DeleteAssistantReportSchedule stops the standing refresh. The pin, the
// artifact and the report's readers are untouched — only the robot stops.
//
// Idempotent: deleting a cadence that is not there is a 204, because the
// caller's intent ("this report must not refresh itself") is satisfied either
// way, and a 404 would make a retried request look like a failure. The event
// fires only when a row actually went away — nothing changed, nothing to
// announce — which is the same rule the idempotent pin create follows.
func (h *Handler) DeleteAssistantReportSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	pin, ok := h.loadReportPinForOwner(w, r, userID)
	if !ok {
		return
	}
	removed, err := h.Queries.DeleteAssistantReportSchedule(r.Context(), pin.ID)
	if err != nil {
		slog.Warn("reports: delete schedule failed", "pin_id", uuidToString(pin.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "could not remove this schedule")
		return
	}
	if removed > 0 {
		h.publishReportEvent(protocol.EventReportScheduleChanged, userID, pin)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// The ticker
// ---------------------------------------------------------------------------

// RunDueReportSchedules is one tick of the scheduled-refresh loop, called from
// the cmd/server ticker. It lives in this package because both gates it reads —
// the feature switch and assistantEnabled() — are properties of the assistant
// surface, not of the process that happens to own the timer.
//
// Returns immediately, doing no query at all, whenever either gate is off: the
// kill switch must cost nothing on an install that never uses this.
func (h *Handler) RunDueReportSchedules(ctx context.Context) {
	if !config.Bool("AGORA_ASSISTANT_SCHEDULES_ENABLED") {
		return
	}
	if h.Assistant == nil || !assistantEnabled() {
		return
	}
	due, err := h.Queries.ListDueAssistantReportSchedules(ctx, reportScheduleBatch)
	if err != nil {
		slog.Warn("report schedules: list due failed", "error", err)
		return
	}
	// Sequential on purpose. A tick handles a handful of rows, each one only
	// ACCEPTS a run (the assistant's own worker does the talking), so there is
	// no wall-clock argument for fanning out — and one goroutine per due row
	// would be an unbounded way to start model calls.
	for _, schedule := range due {
		h.runReportSchedule(ctx, schedule)
	}
}

// runReportSchedule claims one due slot and starts its refresh.
func (h *Handler) runReportSchedule(ctx context.Context, schedule db.AssistantReportSchedule) {
	next, err := nextRunAt(schedule.Frequency, schedule.AtTime, int(schedule.Weekday.Int32), schedule.Timezone, time.Now())
	if err != nil {
		// A stored cadence that no longer computes (a timezone dropped from the
		// system tzdata, a row written before a validation change) must not spin
		// the ticker forever. Park it a day out, claim the slot anyway, and say
		// why in last_error so the owner can fix it.
		claimed, ok := h.claimReportSchedule(ctx, schedule, time.Now().Add(24*time.Hour))
		if !ok {
			return
		}
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusFailed, err.Error())
		return
	}
	claimed, ok := h.claimReportSchedule(ctx, schedule, next)
	if !ok {
		return
	}

	// pin -> artifact -> session is the same ownership chain every assistant
	// read uses, walked in that order: a refresh runs in the conversation that
	// produced the report, as the person who owns it.
	pin, err := h.Queries.GetAssistantArtifactPin(ctx, claimed.PinID)
	if err != nil {
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusFailed, "the pinned report is no longer available")
		return
	}
	artifact, err := h.Queries.GetAssistantArtifact(ctx, pin.ArtifactID)
	if err != nil {
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusFailed, "the report's artifact is no longer available")
		return
	}
	session, err := h.Queries.GetAssistantSession(ctx, artifact.SessionID)
	if err != nil {
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusFailed, "the conversation that produced this report is gone")
		return
	}
	owner := uuidToString(artifact.UserID)
	if uuidToString(session.UserID) != owner {
		// Not reachable through any write path; asserted rather than assumed
		// because the run below executes tools AS this user.
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusFailed, "the report's owner no longer owns its conversation")
		return
	}

	// The owner's own conversation always wins: a refresh never queues behind a
	// live run and never runs beside one.
	sessionID := uuidToString(session.ID)
	if h.Assistant.HasActiveRun(sessionID) {
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusSkipped, "the conversation was busy at this slot")
		return
	}

	runContext := assistant.RunContext{
		WorkspaceID: uuidToPtr(session.FocusWorkspaceID),
		// The schedule's timezone is also the report's: "yesterday" in a daily
		// standup means yesterday where the owner set the cadence.
		Timezone: claimed.Timezone,
	}
	if _, err := h.startAssistantRun(ctx, session, owner, scheduledRefreshMessage(artifact), nil, "", runContext, []byte(`{}`)); err != nil {
		// The in-memory check above cannot see a run another server process
		// started, so the database's own exclusion is the second half of the
		// same answer — still a skip, not a failure.
		if errors.Is(err, assistant.ErrRunInProgress) {
			h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusSkipped, "the conversation was busy at this slot")
			return
		}
		slog.Warn("report schedules: start refresh failed",
			"schedule_id", uuidToString(claimed.ID), "session_id", sessionID, "error", err)
		h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusFailed, err.Error())
		return
	}

	// "ok" means the refresh STARTED. The run's own outcome reaches the owner
	// through their transcript and the artifact's next version — a schedule row
	// that tried to mirror it would be a second, slower copy of state the
	// assistant already publishes.
	h.recordReportScheduleOutcome(ctx, claimed.ID, reportScheduleStatusOK, "")
}

// claimReportSchedule advances the slot before the run. A lost compare-and-swap
// is not an error: it means another server process claimed this slot, or the
// owner changed the cadence between the list and the claim. Either way this
// process must not run it.
func (h *Handler) claimReportSchedule(ctx context.Context, schedule db.AssistantReportSchedule, next time.Time) (db.AssistantReportSchedule, bool) {
	claimed, err := h.Queries.ClaimDueAssistantReportSchedule(ctx, db.ClaimDueAssistantReportScheduleParams{
		ID:          schedule.ID,
		NextRunAt:   pgtype.Timestamptz{Time: next, Valid: true},
		ClaimedSlot: schedule.NextRunAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.AssistantReportSchedule{}, false
	}
	if err != nil {
		slog.Warn("report schedules: claim failed", "schedule_id", uuidToString(schedule.ID), "error", err)
		return db.AssistantReportSchedule{}, false
	}
	return claimed, true
}

func (h *Handler) recordReportScheduleOutcome(ctx context.Context, scheduleID pgtype.UUID, status, message string) {
	if len(message) > reportScheduleErrorMax {
		message = message[:reportScheduleErrorMax]
	}
	if err := h.Queries.UpdateAssistantReportScheduleOutcome(ctx, db.UpdateAssistantReportScheduleOutcomeParams{
		ID:         scheduleID,
		LastStatus: status,
		LastError:  message,
	}); err != nil {
		slog.Warn("report schedules: record outcome failed",
			"schedule_id", uuidToString(scheduleID), "status", status, "error", err)
	}
}

// scheduledRefreshMessage is the synthetic user turn a due slot sends. It is
// composed here and stored nowhere: the cadence row holds no prompt, so a
// schedule can never become a second place where a report's definition lives.
//
// It names the artifact by id, title and kind, and insists on update_artifact,
// because the recipe must land on the SAME artifact every published reader is
// looking at — a create would leave the project page pinned to a report that
// silently stopped being refreshed.
func scheduledRefreshMessage(artifact db.AssistantArtifact) string {
	return fmt.Sprintf(
		"Scheduled refresh: re-run the standing report that produced artifact %s titled '%s' (kind %s) "+
			"with fresh data and save it with update_artifact on that same artifact — never create_artifact. "+
			"If the title matches no standing recipe, rebuild from the structure visible in this conversation's history.",
		uuidToString(artifact.ID), artifact.Title, artifact.Kind)
}
