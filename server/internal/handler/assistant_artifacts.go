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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Assistant ARTIFACTS — the two tools that produce them and the two endpoints
// that read them back. See docs/agora-assistant-artifacts-plan.md §3-§5.
//
// These are the only tools in the catalog scoped to the SESSION rather than to
// a workspace, so they do not go through assistantMembership. Their gate is
// ownership of the conversation: artifact -> session -> user_id must be the
// caller. That is strictly narrower than a workspace gate (a session is
// private to one person), and it is the right one — an artifact may aggregate
// numbers from every workspace the user belongs to, so there is no single
// workspace that could own it.
//
// A session that is not the caller's is reported as NOT FOUND, never
// forbidden, matching loadAssistantSessionForUser: sessions are user-private,
// so confirming one exists would already be the leak.
//
// REVISIONS (docs/agora-assistant-final-plan.md §4 Phase 4). The artifact row
// is the CURRENT pointer; assistant_artifact_revision is append-only history.
// Every write here keeps the two in one transaction, so there is no window in
// which a body exists without the revision recording it, or a version number
// names a body nobody can read back:
//
//	create_artifact  -> INSERT artifact (v1) + INSERT revision v1
//	update_artifact  -> UPDATE ... version = version + 1 RETURNING
//	                    + INSERT revision at the RETURNED version
//	                    + trim to the retention cap
//
// Concurrency rests on that UPDATE. The new version is computed by Postgres
// from the row it locked, and the lock is held until the transaction commits,
// so two runs updating the same artifact at the same moment serialize: the
// second reads the first's committed version and leaves with the next one.
// Neither can compute the same number, which is what makes the returned
// version safe to use as the revision's key.

// errAssistantNoSession is what a tool sees when it is invoked outside a run —
// there is no conversation to attach an artifact to. In production this cannot
// happen (the run loop always passes its session); it is a real error rather
// than a panic so a future caller gets a readable refusal.
var errAssistantNoSession = errors.New("artifacts can only be produced inside an assistant conversation")

// errAssistantArtifactNotFound is deliberately identical whether the artifact
// does not exist or belongs to someone else — the same not-found semantics the
// session reads use, so neither the tool nor the endpoint is an existence
// oracle.
var errAssistantArtifactNotFound = errors.New("artifact not found in this conversation")

// ---------------------------------------------------------------------------
// Ownership
// ---------------------------------------------------------------------------

// assistantSessionForCaller resolves the run's session and proves the caller
// owns it.
func (h *Handler) assistantSessionForCaller(ctx context.Context, caller assistantCaller, sessionID string) (db.AssistantSession, error) {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return db.AssistantSession{}, errAssistantNoSession
	}
	sessionUUID, err := util.ParseUUID(trimmed)
	if err != nil {
		return db.AssistantSession{}, errAssistantNoSession
	}
	session, err := h.Queries.GetAssistantSession(ctx, sessionUUID)
	if err != nil {
		return db.AssistantSession{}, errAssistantNoSession
	}
	if uuidToString(session.UserID) != caller.ID {
		return db.AssistantSession{}, errAssistantNoSession
	}
	return session, nil
}

// loadAssistantArtifactForUser walks the ownership chain the plan specifies:
// artifact -> session -> user_id. The denormalized artifact.user_id is checked
// too, so a row whose columns ever disagreed with its session would be refused
// by both rather than trusted by one.
//
// Used by the update tool AND by both endpoints, so there is exactly one place
// where "may this person see this artifact" is decided.
func (h *Handler) loadAssistantArtifactForUser(ctx context.Context, artifactID, userID string) (db.AssistantArtifact, error) {
	artifactUUID, err := util.ParseUUID(strings.TrimSpace(artifactID))
	if err != nil {
		return db.AssistantArtifact{}, errAssistantArtifactNotFound
	}
	artifact, err := h.Queries.GetAssistantArtifact(ctx, artifactUUID)
	if err != nil {
		return db.AssistantArtifact{}, errAssistantArtifactNotFound
	}
	if uuidToString(artifact.UserID) != userID {
		return db.AssistantArtifact{}, errAssistantArtifactNotFound
	}
	session, err := h.Queries.GetAssistantSession(ctx, artifact.SessionID)
	if err != nil || uuidToString(session.UserID) != userID {
		return db.AssistantArtifact{}, errAssistantArtifactNotFound
	}
	return artifact, nil
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

type assistantCreateArtifactArgs struct {
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

type assistantUpdateArtifactArgs struct {
	ArtifactID string  `json:"artifact_id"`
	Content    string  `json:"content"`
	Title      *string `json:"title"`
	// ExpectedVersion is the optional concurrency check: "I am editing the
	// version I read". A pointer, not an int, so 0 is distinguishable from
	// absent — omitted means an unconditional update, which stays the default
	// because most updates follow a create or an update in the same run and
	// nothing else can have touched the artifact in between.
	ExpectedVersion *int32 `json:"expected_version"`
}

// errAssistantStaleArtifactVersion is the refusal an expected_version mismatch
// produces. It is written as a CORRECTION: it names the version the artifact is
// actually at, so the model's next move is to re-read that version and rewrite
// on top of it — not to strip expected_version and clobber whatever landed.
func errAssistantStaleArtifactVersion(current, expected int32) error {
	return fmt.Errorf("stale version: this artifact is now at version %d, not the version %d you expected — "+
		"it changed after you read it. Re-read it (its revisions are kept) and send an update built on "+
		"version %d, passing expected_version %d", current, expected, current, current)
}

// assistantArtifactResult is the tool_result both tools return. The transcript
// renders an ArtifactCard straight from this — id to open the pane, title and
// kind to label it, version to show the "v2" badge — with no second fetch.
type assistantArtifactResult struct {
	ArtifactID string `json:"artifact_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Version    int32  `json:"version"`
}

// assistantCreateArtifact writes one new artifact into the run's session.
//
// Validation runs BEFORE the insert and before the count query, so a malformed
// chart spec costs the model one correctable tool error and leaves the session
// byte-for-byte unchanged — no half-written row for the pane to choke on.
func (h *Handler) assistantCreateArtifact(ctx context.Context, caller assistantCaller, sessionID string, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateArtifactArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	title := strings.TrimSpace(args.Title)
	kind := strings.ToLower(strings.TrimSpace(args.Kind))
	if err := assistant.ValidateArtifact(kind, title, args.Content); err != nil {
		return nil, err
	}

	session, err := h.assistantSessionForCaller(ctx, caller, sessionID)
	if err != nil {
		return nil, err
	}

	count, err := h.Queries.CountAssistantArtifactsBySession(ctx, session.ID)
	if err != nil {
		slog.Warn("assistant: count artifacts failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not check how many artifacts this conversation already has")
	}
	if count >= assistant.MaxArtifactsPerSession {
		// Phrased as the correction it is: the model's next move should be
		// update_artifact, not a retry of the same create.
		return nil, errors.New("this conversation already has the maximum of " +
			strconv.Itoa(assistant.MaxArtifactsPerSession) +
			" artifacts — update an existing one with update_artifact instead of creating another")
	}

	// The artifact and its v1 revision are one transaction: an artifact whose
	// history starts at v2 would make the version picker lie about where the
	// document began, and there is no later moment at which the original body
	// could be recovered.
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("assistant: create artifact tx failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	artifact, err := qtx.CreateAssistantArtifact(ctx, db.CreateAssistantArtifactParams{
		SessionID: session.ID,
		// The session's owner, never the caller string: the denormalized
		// column can then never disagree with the join that authorizes reads.
		UserID:  session.UserID,
		Title:   title,
		Kind:    kind,
		Content: args.Content,
	})
	if err != nil {
		slog.Warn("assistant: create artifact failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}
	if _, err := qtx.CreateAssistantArtifactRevision(ctx, db.CreateAssistantArtifactRevisionParams{
		ArtifactID: artifact.ID,
		Version:    artifact.Version,
		Title:      artifact.Title,
		Content:    artifact.Content,
	}); err != nil {
		slog.Warn("assistant: create artifact revision failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("assistant: create artifact commit failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}
	return json.Marshal(assistantArtifactResult{
		ArtifactID: uuidToString(artifact.ID),
		Title:      artifact.Title,
		Kind:       artifact.Kind,
		Version:    artifact.Version,
	})
}

// assistantUpdateArtifact replaces an artifact's body and bumps its version.
//
// The kind is NOT an argument: it is read from the stored row and the new
// content is validated against it. Letting the model restate the kind would
// let "make it a line chart" silently turn a chart into markdown behind a pane
// that is already rendering it as a chart.
func (h *Handler) assistantUpdateArtifact(ctx context.Context, caller assistantCaller, sessionID string, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateArtifactArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	if strings.TrimSpace(args.ArtifactID) == "" {
		return nil, errors.New("artifact_id is required — use the id from the create_artifact result")
	}

	session, err := h.assistantSessionForCaller(ctx, caller, sessionID)
	if err != nil {
		return nil, err
	}
	artifact, err := h.loadAssistantArtifactForUser(ctx, args.ArtifactID, caller.ID)
	if err != nil {
		return nil, err
	}
	// Deliberately narrower than "the caller owns it": an artifact from
	// another of this user's conversations is theirs to READ, but rewriting it
	// from here would bump a version behind a pane this run is not attached
	// to, and the model can only have learned that id from outside this
	// transcript. Same not-found wording, so it stays a correction rather than
	// a hint that the id was real.
	if uuidToString(artifact.SessionID) != uuidToString(session.ID) {
		return nil, errAssistantArtifactNotFound
	}

	if err := assistant.ValidateArtifactContent(artifact.Kind, args.Content); err != nil {
		return nil, err
	}

	params := db.UpdateAssistantArtifactParams{ID: artifact.ID, Content: args.Content}
	if args.Title != nil {
		title := strings.TrimSpace(*args.Title)
		if err := assistant.ValidateArtifactTitle(title); err != nil {
			return nil, err
		}
		params.Title = strToText(title)
	}
	if args.ExpectedVersion != nil {
		// Checked twice on purpose. Here, against the row just loaded, so the
		// common mismatch costs no transaction and the error names the version
		// this read saw. And again in the UPDATE's WHERE clause below, which is
		// the authoritative one: between this read and that write another run
		// can commit, and only a condition evaluated under the row lock can
		// refuse that.
		if *args.ExpectedVersion != artifact.Version {
			return nil, errAssistantStaleArtifactVersion(artifact.Version, *args.ExpectedVersion)
		}
		params.ExpectedVersion = pgtype.Int4{Int32: *args.ExpectedVersion, Valid: true}
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("assistant: update artifact tx failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	// The bump and the revision are one transaction, in this order on purpose:
	// the UPDATE locks the row and RETURNS the version Postgres computed, and
	// the INSERT files this body under exactly that number while the lock is
	// still held. A concurrent updater cannot be between them.
	updated, err := qtx.UpdateAssistantArtifact(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing matched. Roll back first, then read the row again to find out
		// which of the two WHERE terms failed — the refusal has to name a real
		// number, not the one we hoped for.
		_ = tx.Rollback(ctx)
		current, rerr := h.Queries.GetAssistantArtifact(ctx, artifact.ID)
		if rerr != nil || args.ExpectedVersion == nil {
			// The id no longer matches anything: the artifact (or its session)
			// was deleted between the load and this write. Without an
			// expected_version that is the ONLY way to get here, so the
			// not-found is the whole story.
			return nil, errAssistantArtifactNotFound
		}
		return nil, errAssistantStaleArtifactVersion(current.Version, *args.ExpectedVersion)
	}
	if err != nil {
		slog.Warn("assistant: update artifact failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}

	if _, err := qtx.CreateAssistantArtifactRevision(ctx, db.CreateAssistantArtifactRevisionParams{
		ArtifactID: updated.ID,
		Version:    updated.Version,
		Title:      updated.Title,
		Content:    updated.Content,
	}); err != nil {
		// UNIQUE (artifact_id, version) tripped. This is NOT the concurrent
		// case — the row lock above already serialized those — so it means a
		// revision for this version exists with a different body: the pointer
		// and the history disagree. REFUSE rather than retry at version+1: a
		// retry would file this body under a number the artifact row never
		// held while leaving the disagreement in place, and it would do so
		// silently. Rolling back keeps the artifact exactly as the user last
		// saw it and turns the inconsistency into something a person reads.
		if isUniqueViolation(err) {
			slog.Error("assistant: artifact revision version collision",
				"artifact_id", uuidToString(updated.ID), "version", updated.Version)
			return nil, fmt.Errorf("could not save the artifact: version %d of it already has a stored revision — "+
				"nothing was changed", updated.Version)
		}
		slog.Warn("assistant: append artifact revision failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}

	// Retention, inside the same transaction so the history is never observed
	// over its cap and a failed trim cannot leave an appended revision behind.
	if err := qtx.TrimAssistantArtifactRevisions(ctx, db.TrimAssistantArtifactRevisionsParams{
		ArtifactID: updated.ID,
		// The cap counts v1, which is pinned, so the window of trimmable
		// newest-first revisions is one smaller.
		KeepNewest: assistant.MaxArtifactRevisions - 1,
	}); err != nil {
		slog.Warn("assistant: trim artifact revisions failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Warn("assistant: update artifact commit failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}

	// A refreshed report is the one change a project page cannot learn about
	// from its own surface — the pin row did not move, only the body behind it.
	// Emitted at the write site and only after the commit, so no workspace is
	// ever told about a version that was rolled back. See assistant_pins.go.
	h.notifyPinnedReportUpdated(ctx, updated.ID, caller.ID)

	return json.Marshal(assistantArtifactResult{
		ArtifactID: uuidToString(updated.ID),
		Title:      updated.Title,
		Kind:       updated.Kind,
		Version:    updated.Version,
	})
}

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

// AssistantArtifactSummaryResponse is one row of a session's artifact list.
// Content is deliberately absent — see the list query.
type AssistantArtifactSummaryResponse struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Version   int32  `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// AssistantArtifactResponse is the detail read: the summary plus the body the
// viewer renders.
type AssistantArtifactResponse struct {
	AssistantArtifactSummaryResponse
	Content string `json:"content"`
}

func assistantArtifactToResponse(a db.AssistantArtifact) AssistantArtifactResponse {
	return AssistantArtifactResponse{
		AssistantArtifactSummaryResponse: AssistantArtifactSummaryResponse{
			ID:        uuidToString(a.ID),
			SessionID: uuidToString(a.SessionID),
			Title:     a.Title,
			Kind:      a.Kind,
			Version:   a.Version,
			CreatedAt: timestampToString(a.CreatedAt),
			UpdatedAt: timestampToString(a.UpdatedAt),
		},
		Content: a.Content,
	}
}

// GetAssistantArtifact returns one artifact with its full content. Owner-only
// through the session chain; anything else is a 404.
func (h *Handler) GetAssistantArtifact(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	artifact, err := h.loadAssistantArtifactForUser(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "assistant artifact not found")
		return
	}
	writeJSON(w, http.StatusOK, assistantArtifactToResponse(artifact))
}

// ListAssistantSessionArtifacts returns every artifact produced in one of the
// caller's sessions, without their bodies.
func (h *Handler) ListAssistantSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}
	rows, err := h.Queries.ListAssistantArtifactsBySession(r.Context(), session.ID)
	if err != nil {
		slog.Warn("assistant: list artifacts failed", "session_id", uuidToString(session.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list assistant artifacts")
		return
	}
	resp := make([]AssistantArtifactSummaryResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, AssistantArtifactSummaryResponse{
			ID:        uuidToString(row.ID),
			SessionID: uuidToString(row.SessionID),
			Title:     row.Title,
			Kind:      row.Kind,
			Version:   row.Version,
			CreatedAt: timestampToString(row.CreatedAt),
			UpdatedAt: timestampToString(row.UpdatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Revision endpoints
// ---------------------------------------------------------------------------

// AssistantArtifactRevisionSummaryResponse is one entry in the version picker.
// Content is absent for the same reason it is absent from the artifact list: a
// body is up to 256 KB and up to 50 of them can be stored per artifact, while
// the picker draws a number, a label and a date.
type AssistantArtifactRevisionSummaryResponse struct {
	ID         string `json:"id"`
	ArtifactID string `json:"artifact_id"`
	Version    int32  `json:"version"`
	Title      string `json:"title"`
	CreatedAt  string `json:"created_at"`
}

// AssistantArtifactRevisionResponse is the detail read: the summary plus the
// body that version held. Field names mirror the artifact shape so one client
// renderer can draw a historical version and the current one.
type AssistantArtifactRevisionResponse struct {
	AssistantArtifactRevisionSummaryResponse
	Content string `json:"content"`
}

// ListAssistantArtifactRevisions returns the artifact's history, newest first,
// without bodies. Owner-only through the same artifact -> session -> user
// chain as every other artifact read; anything else is a 404.
func (h *Handler) ListAssistantArtifactRevisions(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	artifact, err := h.loadAssistantArtifactForUser(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "assistant artifact not found")
		return
	}
	rows, err := h.Queries.ListAssistantArtifactRevisions(r.Context(), artifact.ID)
	if err != nil {
		slog.Warn("assistant: list artifact revisions failed",
			"artifact_id", uuidToString(artifact.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list assistant artifact revisions")
		return
	}
	resp := make([]AssistantArtifactRevisionSummaryResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, AssistantArtifactRevisionSummaryResponse{
			ID:         uuidToString(row.ID),
			ArtifactID: uuidToString(row.ArtifactID),
			Version:    row.Version,
			Title:      row.Title,
			CreatedAt:  timestampToString(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetAssistantArtifactRevision returns one historical version in full.
//
// A version that is not a positive integer is a 404 rather than a 400: the
// artifact reads above already answer "not yours" and "never existed" with the
// same not-found, and a client that asks for v9 of a four-version artifact
// wants the same branch as one that asks for "abc". One failure mode, one
// handler on the other side.
func (h *Handler) GetAssistantArtifactRevision(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	artifact, err := h.loadAssistantArtifactForUser(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "assistant artifact revision not found")
		return
	}
	version, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "version")))
	if err != nil || version < 1 {
		writeError(w, http.StatusNotFound, "assistant artifact revision not found")
		return
	}
	revision, err := h.Queries.GetAssistantArtifactRevision(r.Context(), db.GetAssistantArtifactRevisionParams{
		ArtifactID: artifact.ID,
		Version:    int32(version),
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("assistant: get artifact revision failed",
				"artifact_id", uuidToString(artifact.ID), "version", version, "error", err)
		}
		writeError(w, http.StatusNotFound, "assistant artifact revision not found")
		return
	}
	writeJSON(w, http.StatusOK, AssistantArtifactRevisionResponse{
		AssistantArtifactRevisionSummaryResponse: AssistantArtifactRevisionSummaryResponse{
			ID:         uuidToString(revision.ID),
			ArtifactID: uuidToString(revision.ArtifactID),
			Version:    revision.Version,
			Title:      revision.Title,
			CreatedAt:  timestampToString(revision.CreatedAt),
		},
		Content: revision.Content,
	})
}
