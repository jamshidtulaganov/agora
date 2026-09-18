package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Pinned reports — publishing an assistant artifact into a project
// (docs/assistant-domain-plan.md §Phase 2a).
//
// Everything else in assistant_artifacts.go is user-scoped: an artifact may
// aggregate numbers from every workspace its owner belongs to, so ownership of
// the conversation is the only gate that can be right. A PIN is the one place
// where that changes hands, and it is therefore the only place in the assistant
// surface where the two authorization models meet:
//
//	create a pin  — the artifact's OWNER (they are the only one who knows what
//	                the report aggregates), who must also be a member of the
//	                target project's workspace.
//	delete a pin  — the artifact's owner, OR an admin/owner of the workspace
//	                the report was published into (the workspace can always
//	                withdraw something from its own project page).
//	read a report — any member of the pin's workspace. The grant is the CURRENT
//	                body and nothing more; revisions stay with the owner.
//
// Two rules follow from that split and are enforced below rather than trusted:
//
//   - The pin's workspace is DERIVED from the project, never read off the
//     request. A caller cannot name a workspace and a project that disagree.
//   - Nothing reached through a pin exposes the session that produced the
//     artifact. The report reads never select session_id, so no later change to
//     a response struct can turn a published report into a pointer at a private
//     conversation.
//
// Not-found vs forbidden follows the surface being addressed. An artifact the
// caller does not own is NOT FOUND, as everywhere else in the assistant (the
// existence of someone's private artifact is itself the leak). A report in a
// workspace the caller is not in is NOT FOUND for the same reason. Only a
// member who is present but too junior to unpin gets a 403 — they can already
// see the report, so the refusal tells them nothing new.

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

// ReportUserRef names a person on a report row. Id plus display name only: the
// project page draws an attribution line, and an email here would publish a
// contact detail to everyone who can open the project.
type ReportUserRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReportSummaryResponse is one pinned report as the project page lists it, and
// also what a create returns. No content — see ListAssistantArtifactPinsByProject.
//
// `updated_at` is the ARTIFACT's, not the pin's: the question a reader has is
// "how fresh is this report", and re-running the recipe changes the artifact
// while leaving the pin untouched. `created_at` is the pin's — when it was
// published here.
type ReportSummaryResponse struct {
	PinID      string        `json:"pin_id"`
	ArtifactID string        `json:"artifact_id"`
	Title      string        `json:"title"`
	Kind       string        `json:"kind"`
	Version    int32         `json:"version"`
	UpdatedAt  string        `json:"updated_at"`
	CreatedAt  string        `json:"created_at"`
	PinnedBy   ReportUserRef `json:"pinned_by"`
	Owner      ReportUserRef `json:"owner"`
}

// ReportResponse is the single-report read: the same metadata plus the body a
// member renders. Same field names as the summary so one client renderer draws
// both.
type ReportResponse struct {
	ReportSummaryResponse
	Content string `json:"content"`
}

// ListProjectReportsResponse keeps the list inside an object rather than
// returning a bare array, so the endpoint can gain a cursor later without the
// installed clients that parse it having to change shape.
type ListProjectReportsResponse struct {
	Reports []ReportSummaryResponse `json:"reports"`
}

func reportSummaryFromListRow(row db.ListAssistantArtifactPinsByProjectRow) ReportSummaryResponse {
	return ReportSummaryResponse{
		PinID:      uuidToString(row.ID),
		ArtifactID: uuidToString(row.ArtifactID),
		Title:      row.Title,
		Kind:       row.Kind,
		Version:    row.Version,
		UpdatedAt:  timestampToString(row.UpdatedAt),
		CreatedAt:  timestampToString(row.CreatedAt),
		PinnedBy:   ReportUserRef{ID: uuidToString(row.PinnedBy), Name: row.PinnedByName},
		Owner:      ReportUserRef{ID: uuidToString(row.OwnerID), Name: row.OwnerName},
	}
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// publishReportEvent fans a pin change out to the workspace it was published
// into. Ids only: every recipient refetches through the membership-gated
// endpoints, so the fanout itself can never carry a report body into a client
// that may not read it.
func (h *Handler) publishReportEvent(eventType, actorID string, pin db.AssistantArtifactPin) {
	h.publish(eventType, uuidToString(pin.WorkspaceID), "member", actorID, map[string]any{
		"pin_id":      uuidToString(pin.ID),
		"project_id":  uuidToString(pin.ProjectID),
		"artifact_id": uuidToString(pin.ArtifactID),
	})
}

// ---------------------------------------------------------------------------
// POST /api/assistant/artifacts/{id}/pins
// ---------------------------------------------------------------------------

type pinAssistantArtifactRequest struct {
	ProjectID string `json:"project_id"`
}

// PinAssistantArtifact publishes one of the caller's artifacts to a project.
//
// Order matters: ownership of the artifact is proved BEFORE the project is
// looked up, so a non-owner probing project ids learns nothing about which of
// them exist.
func (h *Handler) PinAssistantArtifact(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	artifact, err := h.loadAssistantArtifactForUser(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "assistant artifact not found")
		return
	}

	var body pinAssistantArtifactRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, body.ProjectID, "project_id")
	if !ok {
		return
	}

	// The project is resolved globally and its workspace is what the pin
	// stores — the caller's active workspace header has no say here. Pinning
	// into a project the caller cannot reach is reported as a missing project,
	// never as a permission problem, so this endpoint is not a directory of
	// other people's projects.
	project, err := h.Queries.GetProject(r.Context(), projectUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	workspaceID := uuidToString(project.WorkspaceID)
	if _, err := h.getWorkspaceMember(r.Context(), userID, workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	// The owner is the caller — loadAssistantArtifactForUser just proved it —
	// so one user read names both sides of the row the client renders.
	owner, err := h.Queries.GetUser(r.Context(), artifact.UserID)
	if err != nil {
		slog.Warn("reports: load artifact owner failed",
			"artifact_id", uuidToString(artifact.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "could not pin this report")
		return
	}

	pin, err := h.Queries.CreateAssistantArtifactPin(r.Context(), db.CreateAssistantArtifactPinParams{
		ArtifactID:  artifact.ID,
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		PinnedBy:    parseUUID(userID),
	})
	if err != nil {
		// UNIQUE (artifact_id, project_id). Publishing the same report to the
		// same project twice is one pin — answer with the one that exists and
		// 200 rather than 500, so a double-click or a retried request is a
		// no-op. No event: nothing changed.
		if isUniqueViolation(err) {
			existing, gerr := h.Queries.GetAssistantArtifactPinForArtifactAndProject(r.Context(),
				db.GetAssistantArtifactPinForArtifactAndProjectParams{
					ArtifactID: artifact.ID,
					ProjectID:  project.ID,
				})
			if gerr != nil {
				slog.Warn("reports: reread pin after conflict failed",
					"artifact_id", uuidToString(artifact.ID), "error", gerr)
				writeError(w, http.StatusInternalServerError, "could not pin this report")
				return
			}
			writeJSON(w, http.StatusOK, reportSummaryFromPin(existing, artifact, owner, owner))
			return
		}
		slog.Warn("reports: create pin failed",
			"artifact_id", uuidToString(artifact.ID), "project_id", uuidToString(project.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "could not pin this report")
		return
	}

	h.publishReportEvent(protocol.EventReportPinned, userID, pin)
	writeJSON(w, http.StatusCreated, reportSummaryFromPin(pin, artifact, owner, owner))
}

// reportSummaryFromPin builds the response for a pin the handler already holds
// in pieces, so the create path does not re-read through the joined query it
// just wrote the row for.
func reportSummaryFromPin(pin db.AssistantArtifactPin, artifact db.AssistantArtifact, owner, pinnedBy db.User) ReportSummaryResponse {
	return ReportSummaryResponse{
		PinID:      uuidToString(pin.ID),
		ArtifactID: uuidToString(artifact.ID),
		Title:      artifact.Title,
		Kind:       artifact.Kind,
		Version:    artifact.Version,
		UpdatedAt:  timestampToString(artifact.UpdatedAt),
		CreatedAt:  timestampToString(pin.CreatedAt),
		PinnedBy:   ReportUserRef{ID: uuidToString(pinnedBy.ID), Name: pinnedBy.Name},
		Owner:      ReportUserRef{ID: uuidToString(owner.ID), Name: owner.Name},
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/assistant/artifacts/{id}/pins/{pinId}
// ---------------------------------------------------------------------------

// UnpinAssistantArtifact withdraws the read grant. The artifact is untouched —
// unpinning is not a delete, and the owner's pane keeps rendering it.
func (h *Handler) UnpinAssistantArtifact(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// Both ids come straight off the URL, so both are validated here. The
	// artifact id is only ever COMPARED (the delete is keyed on the pin row the
	// lookup returned), but a malformed one must still be a 400 rather than a
	// zero UUID that silently matches nothing.
	artifactUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "artifact id")
	if !ok {
		return
	}
	pinUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "pinId"), "pin id")
	if !ok {
		return
	}

	pin, err := h.Queries.GetAssistantArtifactPin(r.Context(), pinUUID)
	if err != nil || uuidToString(pin.ArtifactID) != uuidToString(artifactUUID) {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}

	// Owner path first: the artifact loader is the same ownership chain every
	// other assistant read uses, and an owner needs no workspace standing to
	// withdraw their own report.
	_, ownerErr := h.loadAssistantArtifactForUser(r.Context(), uuidToString(pin.ArtifactID), userID)
	if ownerErr != nil {
		member, merr := h.getWorkspaceMember(r.Context(), userID, uuidToString(pin.WorkspaceID))
		if merr != nil {
			writeError(w, http.StatusNotFound, "report not found")
			return
		}
		if !roleAllowed(member.Role, "owner", "admin") {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
	}

	if err := h.Queries.DeleteAssistantArtifactPin(r.Context(), pin.ID); err != nil {
		slog.Warn("reports: delete pin failed", "pin_id", uuidToString(pin.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "could not unpin this report")
		return
	}

	h.publishReportEvent(protocol.EventReportUnpinned, userID, pin)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// GET /api/projects/{id}/reports
// ---------------------------------------------------------------------------

// ListProjectReports returns the reports published to one project, without
// their bodies. Gated exactly like GetProject: the workspace comes from the
// request, the project must be in it, and a project from another workspace is
// a 404 — so the list can never be served for a project the caller's active
// workspace does not contain.
func (h *Handler) ListProjectReports(w http.ResponseWriter, r *http.Request) {
	projectUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	rows, err := h.Queries.ListAssistantArtifactPinsByProject(r.Context(),
		db.ListAssistantArtifactPinsByProjectParams{
			WorkspaceID: project.WorkspaceID,
			ProjectID:   project.ID,
		})
	if err != nil {
		slog.Warn("reports: list project reports failed",
			"project_id", uuidToString(project.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list reports")
		return
	}

	reports := make([]ReportSummaryResponse, 0, len(rows))
	for _, row := range rows {
		reports = append(reports, reportSummaryFromListRow(row))
	}
	writeJSON(w, http.StatusOK, ListProjectReportsResponse{Reports: reports})
}

// ---------------------------------------------------------------------------
// GET /api/reports/{pinId}
// ---------------------------------------------------------------------------

// GetReport returns one published report with the artifact's CURRENT body.
//
// Deliberately NOT under the workspace-scoped group: the pin names its own
// workspace, and gating on that rather than on the request's active workspace
// is what lets a link to a report open from wherever the reader happens to be
// — the same reasoning as LocateIssue. Membership of the pin's workspace is
// the whole gate; a non-member gets the same not-found a missing pin does.
func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	pinUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "pinId"), "report id")
	if !ok {
		return
	}
	row, err := h.Queries.GetAssistantArtifactPinWithArtifact(r.Context(), pinUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(row.WorkspaceID)); err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}

	writeJSON(w, http.StatusOK, ReportResponse{
		ReportSummaryResponse: ReportSummaryResponse{
			PinID:      uuidToString(row.ID),
			ArtifactID: uuidToString(row.ArtifactID),
			Title:      row.Title,
			Kind:       row.Kind,
			Version:    row.Version,
			UpdatedAt:  timestampToString(row.UpdatedAt),
			CreatedAt:  timestampToString(row.CreatedAt),
			PinnedBy:   ReportUserRef{ID: uuidToString(row.PinnedBy), Name: row.PinnedByName},
			Owner:      ReportUserRef{ID: uuidToString(row.OwnerID), Name: row.OwnerName},
		},
		Content: row.Content,
	})
}

// ---------------------------------------------------------------------------
// Update fanout
// ---------------------------------------------------------------------------

// notifyPinnedReportUpdated tells every workspace an artifact is published into
// that its body changed. Called from the artifact write path AFTER the commit,
// so a fanout never announces a version that was rolled back.
//
// Best-effort by construction: the update has already succeeded and the user's
// pane is correct, so a failure here costs a stale project page until the next
// refetch — never a failed save. Artifacts are pinned rarely, so the list query
// is one index probe that returns nothing at all in the common case.
func (h *Handler) notifyPinnedReportUpdated(ctx context.Context, artifactID pgtype.UUID, actorID string) {
	pins, err := h.Queries.ListAssistantArtifactPinsByArtifact(ctx, artifactID)
	if err != nil {
		slog.Warn("reports: list pins for updated artifact failed",
			"artifact_id", uuidToString(artifactID), "error", err)
		return
	}
	for _, pin := range pins {
		h.publishReportEvent(protocol.EventReportUpdated, actorID, pin)
	}
}
