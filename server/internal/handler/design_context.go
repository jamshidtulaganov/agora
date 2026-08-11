package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/designcontext"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

type proposeDesignContextRequest struct {
	Context          json.RawMessage `json:"context"`
	LegacyManifest   json.RawMessage `json:"manifest"`
	ExpectedRevision *int32          `json:"expected_revision"`
}

type reviewDesignContextRequest struct {
	ExpectedRevision int32  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type designContextRevisionResponse struct {
	ID              string                  `json:"id"`
	Revision        int32                   `json:"revision"`
	BaseRevision    int32                   `json:"base_revision"`
	Status          string                  `json:"status"`
	Context         json.RawMessage         `json:"context"`
	ContextHash     string                  `json:"context_hash"`
	SourceHash      string                  `json:"source_hash"`
	Freshness       designcontext.Freshness `json:"freshness"`
	ProposedByType  string                  `json:"proposed_by_type"`
	ProposedByID    *string                 `json:"proposed_by_id"`
	ReviewedBy      *string                 `json:"reviewed_by"`
	GeneratedAt     *string                 `json:"generated_at"`
	ProposedAt      string                  `json:"proposed_at"`
	ReviewedAt      *string                 `json:"reviewed_at"`
	RejectionReason string                  `json:"rejection_reason,omitempty"`
}

type designContextStateResponse struct {
	Active    *designContextRevisionResponse  `json:"active"`
	Proposal  *designContextRevisionResponse  `json:"proposal"`
	History   []designContextRevisionResponse `json:"history"`
	Effective json.RawMessage                 `json:"effective,omitempty"`
}

func (h *Handler) GetProjectDesignContext(w http.ResponseWriter, r *http.Request) {
	_, project, ok := h.requireProjectMember(w, r, false)
	if !ok {
		return
	}
	active, err := h.Queries.GetActiveProjectDesignContext(r.Context(), db.GetActiveProjectDesignContextParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load active design context")
		return
	}
	proposal, err := h.Queries.GetProposedProjectDesignContext(r.Context(), db.GetProposedProjectDesignContextParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load design context proposal")
		return
	}
	history, err := h.Queries.ListProjectDesignContextHistory(r.Context(), db.ListProjectDesignContextHistoryParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Limit: 20})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load design context history")
		return
	}
	state := buildDesignContextState(active, proposal, history)
	state.Effective = h.effectiveDesignContextJSON(r.Context(), project.WorkspaceID, pgtype.UUID{Bytes: project.ID.Bytes, Valid: true})
	writeJSON(w, http.StatusOK, state)
}

func (h *Handler) GetWorkspaceDesignContext(w http.ResponseWriter, r *http.Request) {
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	active, err := h.Queries.GetActiveWorkspaceDesignContext(r.Context(), wsID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load active design context")
		return
	}
	proposal, err := h.Queries.GetProposedWorkspaceDesignContext(r.Context(), wsID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load design context proposal")
		return
	}
	history, err := h.Queries.ListWorkspaceDesignContextHistory(r.Context(), db.ListWorkspaceDesignContextHistoryParams{WorkspaceID: wsID, Limit: 20})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load design context history")
		return
	}
	writeJSON(w, http.StatusOK, buildDesignContextState(active, proposal, history))
}

func (h *Handler) ProposeProjectDesignContext(w http.ResponseWriter, r *http.Request) {
	userID, project, ok := h.requireProjectMember(w, r, true)
	if !ok {
		return
	}
	req, document, ok := decodeDesignContextProposal(w, r)
	if !ok {
		return
	}
	currentRevision := int32(0)
	if active, err := h.Queries.GetActiveProjectDesignContext(r.Context(), db.GetActiveProjectDesignContextParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID}); err == nil {
		currentRevision = active.Revision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load active design context")
		return
	}
	expectedRevision, ok := resolveExpectedDesignContextRevision(req, currentRevision)
	if !ok || expectedRevision != currentRevision {
		writeError(w, http.StatusConflict, "revision_conflict: design context changed; refresh before proposing")
		return
	}
	contextHash, sourceHash, contextJSON, sourcesJSON, err := designcontext.Hash(document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode design context")
		return
	}
	nextRevision, err := h.Queries.GetNextProjectDesignContextRevision(r.Context(), db.GetNextProjectDesignContextRevisionParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate design context revision")
		return
	}
	row, err := h.Queries.CreateProjectDesignContextProposal(r.Context(), db.CreateProjectDesignContextProposalParams{
		WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Revision: nextRevision, BaseRevision: currentRevision,
		Context: contextJSON, ContextHash: contextHash, SourceHash: sourceHash, Sources: sourcesJSON,
		ProposedByType: "member", ProposedByID: parseUUID(userID), GeneratedAt: pgtype.Timestamptz{},
	})
	if designContextProposalConflict(err) {
		writeError(w, http.StatusConflict, "proposal_pending: review or reject the existing design context proposal first")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create design context proposal")
		return
	}
	writeJSON(w, http.StatusCreated, designContextRevisionToResponse(row))
}

func (h *Handler) ProposeWorkspaceDesignContext(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	req, document, ok := decodeDesignContextProposal(w, r)
	if !ok {
		return
	}
	currentRevision := int32(0)
	if active, err := h.Queries.GetActiveWorkspaceDesignContext(r.Context(), wsID); err == nil {
		currentRevision = active.Revision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load active design context")
		return
	}
	expectedRevision, ok := resolveExpectedDesignContextRevision(req, currentRevision)
	if !ok || expectedRevision != currentRevision {
		writeError(w, http.StatusConflict, "revision_conflict: design context changed; refresh before proposing")
		return
	}
	contextHash, sourceHash, contextJSON, sourcesJSON, err := designcontext.Hash(document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode design context")
		return
	}
	nextRevision, err := h.Queries.GetNextWorkspaceDesignContextRevision(r.Context(), wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate design context revision")
		return
	}
	row, err := h.Queries.CreateWorkspaceDesignContextProposal(r.Context(), db.CreateWorkspaceDesignContextProposalParams{
		WorkspaceID: wsID, Revision: nextRevision, BaseRevision: currentRevision,
		Context: contextJSON, ContextHash: contextHash, SourceHash: sourceHash, Sources: sourcesJSON,
		ProposedByType: "member", ProposedByID: parseUUID(userID), GeneratedAt: pgtype.Timestamptz{},
	})
	if designContextProposalConflict(err) {
		writeError(w, http.StatusConflict, "proposal_pending: review or reject the existing design context proposal first")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create design context proposal")
		return
	}
	writeJSON(w, http.StatusCreated, designContextRevisionToResponse(row))
}

func (h *Handler) ApproveProjectDesignContext(w http.ResponseWriter, r *http.Request) {
	userID, project, ok := h.requireProjectMember(w, r, true)
	if !ok {
		return
	}
	req, ok := decodeDesignContextReview(w, r)
	if !ok {
		return
	}
	row, err := h.reviewProjectDesignContext(r.Context(), project, parseUUID(userID), req, true)
	h.writeDesignContextReviewResult(w, row, err)
}

func (h *Handler) RejectProjectDesignContext(w http.ResponseWriter, r *http.Request) {
	userID, project, ok := h.requireProjectMember(w, r, true)
	if !ok {
		return
	}
	req, ok := decodeDesignContextReview(w, r)
	if !ok {
		return
	}
	row, err := h.reviewProjectDesignContext(r.Context(), project, parseUUID(userID), req, false)
	h.writeDesignContextReviewResult(w, row, err)
}

func (h *Handler) ApproveWorkspaceDesignContext(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	req, ok := decodeDesignContextReview(w, r)
	if !ok {
		return
	}
	row, err := h.reviewWorkspaceDesignContext(r.Context(), wsID, parseUUID(userID), req, true)
	h.writeDesignContextReviewResult(w, row, err)
}

func (h *Handler) RejectWorkspaceDesignContext(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	req, ok := decodeDesignContextReview(w, r)
	if !ok {
		return
	}
	row, err := h.reviewWorkspaceDesignContext(r.Context(), wsID, parseUUID(userID), req, false)
	h.writeDesignContextReviewResult(w, row, err)
}

func decodeDesignContextProposal(w http.ResponseWriter, r *http.Request) (proposeDesignContextRequest, designcontext.Context, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return proposeDesignContextRequest{}, designcontext.Context{}, false
	}
	var req proposeDesignContextRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, designcontext.Context{}, false
	}
	contextRaw := req.Context
	if len(contextRaw) == 0 {
		contextRaw = req.LegacyManifest
	}
	if len(contextRaw) == 0 {
		writeError(w, http.StatusBadRequest, "context is required")
		return req, designcontext.Context{}, false
	}
	document, err := designcontext.DecodeProposal(contextRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return req, designcontext.Context{}, false
	}
	return req, document, true
}

// Installed clients may still send the legacy `manifest` transport without an
// optimistic revision. Preserve that wire contract while all new Design
// context writes fail closed unless the caller supplies expected_revision.
func resolveExpectedDesignContextRevision(req proposeDesignContextRequest, current int32) (int32, bool) {
	if req.ExpectedRevision != nil {
		return *req.ExpectedRevision, true
	}
	if len(req.Context) == 0 && len(req.LegacyManifest) > 0 {
		return current, true
	}
	return 0, false
}

func decodeDesignContextReview(w http.ResponseWriter, r *http.Request) (reviewDesignContextRequest, bool) {
	var req reviewDesignContextRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	if req.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "expected_revision must be non-negative")
		return req, false
	}
	if len(req.Reason) > 1000 {
		writeError(w, http.StatusBadRequest, "reason is too long")
		return req, false
	}
	return req, true
}

func (h *Handler) requireProjectMember(w http.ResponseWriter, r *http.Request, admin bool) (string, db.Project, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return "", db.Project{}, false
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return "", db.Project{}, false
	}
	project, err := h.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return "", db.Project{}, false
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(userID), WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return "", db.Project{}, false
	}
	if admin && member.Role != "owner" && member.Role != "admin" {
		writeError(w, http.StatusForbidden, "owner or admin role required")
		return "", db.Project{}, false
	}
	return userID, project, true
}

func (h *Handler) reviewProjectDesignContext(ctx context.Context, project db.Project, reviewer pgtype.UUID, req reviewDesignContextRequest, approve bool) (db.DesignContextRevision, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.DesignContextRevision{}, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	proposal, err := q.GetProposedProjectDesignContext(ctx, db.GetProposedProjectDesignContextParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID})
	if err != nil || proposal.BaseRevision != req.ExpectedRevision {
		return db.DesignContextRevision{}, errDesignContextRevisionConflict
	}
	if approve && !designContextRevisionFresh(proposal) {
		return db.DesignContextRevision{}, errDesignContextStale
	}
	row, err := reviewDesignContextTx(ctx, q, proposal, reviewer, req.Reason, approve, func() (db.DesignContextRevision, error) {
		return q.GetActiveProjectDesignContext(ctx, db.GetActiveProjectDesignContextParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID})
	})
	if err != nil {
		return db.DesignContextRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.DesignContextRevision{}, err
	}
	return row, nil
}

func (h *Handler) reviewWorkspaceDesignContext(ctx context.Context, wsID, reviewer pgtype.UUID, req reviewDesignContextRequest, approve bool) (db.DesignContextRevision, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.DesignContextRevision{}, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	proposal, err := q.GetProposedWorkspaceDesignContext(ctx, wsID)
	if err != nil || proposal.BaseRevision != req.ExpectedRevision {
		return db.DesignContextRevision{}, errDesignContextRevisionConflict
	}
	if approve && !designContextRevisionFresh(proposal) {
		return db.DesignContextRevision{}, errDesignContextStale
	}
	row, err := reviewDesignContextTx(ctx, q, proposal, reviewer, req.Reason, approve, func() (db.DesignContextRevision, error) {
		return q.GetActiveWorkspaceDesignContext(ctx, wsID)
	})
	if err != nil {
		return db.DesignContextRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.DesignContextRevision{}, err
	}
	return row, nil
}

func reviewDesignContextTx(ctx context.Context, q *db.Queries, proposal db.DesignContextRevision, reviewer pgtype.UUID, reason string, approve bool, active func() (db.DesignContextRevision, error)) (db.DesignContextRevision, error) {
	if !approve {
		return q.RejectDesignContextProposal(ctx, db.RejectDesignContextProposalParams{ReviewedBy: reviewer, RejectionReason: strings.TrimSpace(reason), ID: proposal.ID, BaseRevision: proposal.BaseRevision})
	}
	current, err := active()
	if err == nil {
		rows, updateErr := q.SupersedeDesignContextRevision(ctx, db.SupersedeDesignContextRevisionParams{ReviewedBy: reviewer, ID: current.ID, Revision: proposal.BaseRevision})
		if updateErr != nil || rows != 1 {
			return db.DesignContextRevision{}, errDesignContextRevisionConflict
		}
	} else if !errors.Is(err, pgx.ErrNoRows) || proposal.BaseRevision != 0 {
		return db.DesignContextRevision{}, errDesignContextRevisionConflict
	}
	return q.ActivateDesignContextProposal(ctx, db.ActivateDesignContextProposalParams{ReviewedBy: reviewer, ID: proposal.ID, BaseRevision: proposal.BaseRevision})
}

var (
	errDesignContextRevisionConflict = errors.New("design context revision conflict")
	errDesignContextStale            = errors.New("design context proposal is stale")
)

func designContextRevisionFresh(row db.DesignContextRevision) bool {
	document, err := designcontext.ParseStored(row.Context)
	return err == nil && designcontext.EvaluateFreshness(document, time.Now().UTC()).Status == "fresh"
}

func (h *Handler) writeDesignContextReviewResult(w http.ResponseWriter, row db.DesignContextRevision, err error) {
	if errors.Is(err, errDesignContextRevisionConflict) || errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "revision_conflict: proposal is stale or was already reviewed")
		return
	}
	if errors.Is(err, errDesignContextStale) {
		writeError(w, http.StatusConflict, "stale_context: regenerate the Design context before approval")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to review design context")
		return
	}
	writeJSON(w, http.StatusOK, designContextRevisionToResponse(row))
}

func designContextProposalConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func buildDesignContextState(active, proposal db.DesignContextRevision, history []db.DesignContextRevision) designContextStateResponse {
	state := designContextStateResponse{History: make([]designContextRevisionResponse, 0, len(history))}
	if active.ID.Valid {
		value := designContextRevisionToResponse(active)
		state.Active = &value
	}
	if proposal.ID.Valid {
		value := designContextRevisionToResponse(proposal)
		state.Proposal = &value
	}
	for _, row := range history {
		state.History = append(state.History, designContextRevisionToResponse(row))
	}
	return state
}

func designContextRevisionToResponse(row db.DesignContextRevision) designContextRevisionResponse {
	document, _ := designcontext.ParseStored(row.Context)
	return designContextRevisionResponse{
		ID: uuidToString(row.ID), Revision: row.Revision, BaseRevision: row.BaseRevision, Status: row.Status,
		Context: json.RawMessage(row.Context), ContextHash: row.ContextHash, SourceHash: row.SourceHash,
		Freshness: designcontext.EvaluateFreshness(document, time.Now().UTC()), ProposedByType: row.ProposedByType,
		ProposedByID: uuidToPtr(row.ProposedByID), ReviewedBy: uuidToPtr(row.ReviewedBy),
		GeneratedAt: timestampToPtr(row.GeneratedAt), ProposedAt: timestampToString(row.ProposedAt),
		ReviewedAt: timestampToPtr(row.ReviewedAt), RejectionReason: row.RejectionReason,
	}
}

func (h *Handler) effectiveDesignContextJSON(ctx context.Context, wsID, projectID pgtype.UUID) json.RawMessage {
	workspace, workspaceOK := h.activeWorkspaceDesignContext(ctx, wsID)
	project, projectOK := h.activeProjectDesignContext(ctx, wsID, projectID)
	if !workspaceOK && !projectOK {
		return nil
	}
	var merged designcontext.Context
	if workspaceOK && projectOK {
		merged = designcontext.Merge(workspace, project)
	} else if workspaceOK {
		merged = workspace
	} else {
		merged = project
	}
	raw, _ := json.Marshal(merged)
	return raw
}

func (h *Handler) activeWorkspaceDesignContext(ctx context.Context, wsID pgtype.UUID) (designcontext.Context, bool) {
	row, err := h.Queries.GetActiveWorkspaceDesignContext(ctx, wsID)
	if err != nil {
		return designcontext.Context{}, false
	}
	document, err := designcontext.ParseStored(row.Context)
	return document, err == nil
}

func (h *Handler) activeProjectDesignContext(ctx context.Context, wsID, projectID pgtype.UUID) (designcontext.Context, bool) {
	if !projectID.Valid {
		return designcontext.Context{}, false
	}
	row, err := h.Queries.GetActiveProjectDesignContext(ctx, db.GetActiveProjectDesignContextParams{WorkspaceID: wsID, ProjectID: projectID})
	if err != nil {
		return designcontext.Context{}, false
	}
	document, err := designcontext.ParseStored(row.Context)
	return document, err == nil
}

func (h *Handler) SyncProjectDesignContext(w http.ResponseWriter, r *http.Request) {
	h.fireProjectDesignChore(w, r, sliceActionGenDesignContext, "Design context refresh — ",
		"Auto-created chore: refresh the project's generated design context from authoritative Figma, Storybook, and repository sources. The resulting proposal requires owner/admin approval before activation.")
}

func (h *Handler) SyncProjectDesignAudit(w http.ResponseWriter, r *http.Request) {
	h.fireProjectDesignChore(w, r, sliceActionDesignAudit, "Design context audit — ",
		"Auto-created chore: audit the project against its approved design context and report off-token values, duplicated markup, unmanaged components, and proposed tokens.")
}

func (h *Handler) fireProjectDesignChore(w http.ResponseWriter, r *http.Request, kind, titlePrefix, description string) {
	userID, project, ok := h.requireProjectMember(w, r, true)
	if !ok {
		return
	}
	seed := db.Issue{ProjectID: pgtype.UUID{Bytes: project.ID.Bytes, Valid: true}, WorkspaceID: project.WorkspaceID}
	designer, ok := h.resolveDesignerAgent(r.Context(), seed)
	if !ok || !h.canAccessPrivateAgent(r.Context(), designer, "member", userID, uuidToString(project.WorkspaceID)) {
		writeError(w, http.StatusConflict, "no_designer_available: configure a ready design agent or a 'design' squad leader")
		return
	}
	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID: project.WorkspaceID, Title: titlePrefix + project.Title,
		Description: pgtype.Text{String: description, Valid: true}, Status: "todo", Priority: "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: designer.ID,
		CreatorType: "member", CreatorID: parseUUID(userID), ProjectID: pgtype.UUID{Bytes: project.ID.Bytes, Valid: true},
	}, service.IssueCreateOpts{ActorID: userID})
	if errors.Is(err, service.ErrActiveDuplicate) {
		writeError(w, http.StatusConflict, "already_running: a "+kind+" chore is already open for this project")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create chore: "+err.Error())
		return
	}
	issue := res.Issue
	instruction := buildSliceInstruction(kind, "") + h.sliceActionDesignContextContext(r.Context(), issue)
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(designer.Name), uuidToString(designer.ID)) + instruction
	comment, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, AuthorType: "member", AuthorID: parseUUID(userID),
		Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fire "+kind)
		return
	}
	h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", userID, nil)
	slog.Info("project design chore fired", append(logger.RequestAttrs(r),
		"kind", kind, "project_id", uuidToString(project.ID), "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(designer.ID))...)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "issue_id": uuidToString(issue.ID)})
}
