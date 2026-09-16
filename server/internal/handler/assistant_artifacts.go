package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

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

	artifact, err := h.Queries.CreateAssistantArtifact(ctx, db.CreateAssistantArtifactParams{
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

	updated, err := h.Queries.UpdateAssistantArtifact(ctx, params)
	if err != nil {
		slog.Warn("assistant: update artifact failed", "session_id", sessionID, "error", err)
		return nil, errors.New("could not save the artifact")
	}
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
