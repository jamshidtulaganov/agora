package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// POST /api/issues/{id}/review-decision is the human half of agent review.
// The agent verdict is advisory; this endpoint records the human decision:
//
//   - approve: verify deterministic gates, approve the persisted release step,
//     and let the orchestration scheduler queue its routed release worker.
//   - request_changes: a non-empty note creates an atomic plan revision with
//     correction, integration, QA, review, and release steps. The DAG remains
//     the only scheduler; the audit comment cannot trigger side work.
//
// Route-gated by RequireHumanActor (a machine credential can never approve a
// merge or reject a review — modeled on qa-override) and membership-checked
// via loadIssueForUser.

// mergeApprovedLabel marks a human's Approve & merge decision. Distinct from
// merge:override (the escape hatch that FORCES done past an unmerged PR):
// merge:approved asserts "a human reviewed the gates and ordered the merge".
// Brand blue — a human decision, not a pass/fail verdict.
const (
	mergeApprovedLabel      = "merge:approved"
	mergeApprovedLabelColor = "#2563eb"
)

// reviewDecisionNoteMaxRunes caps the note at a comment-sized text.
const reviewDecisionNoteMaxRunes = 2000

type reviewDecisionRequest struct {
	Action          string `json:"action"` // "approve" | "request_changes"
	Note            string `json:"note"`
	ExpectedVersion int32  `json:"expected_version"`
	TargetStepID    string `json:"target_step_id"`
}

// CreateReviewDecision handles POST /api/issues/{id}/review-decision.
func (h *Handler) CreateReviewDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req reviewDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action := strings.TrimSpace(req.Action)
	if action != "approve" && action != "request_changes" {
		writeError(w, http.StatusBadRequest, "action must be 'approve' or 'request_changes'")
		return
	}
	note := strings.TrimSpace(req.Note)
	if runes := []rune(note); len(runes) > reviewDecisionNoteMaxRunes {
		note = string(runes[:reviewDecisionNoteMaxRunes-1]) + "…"
	}

	userUUID := parseUUID(userID)
	userName := "a teammate"
	if u, err := h.Queries.GetUser(r.Context(), userUUID); err == nil && strings.TrimSpace(u.Name) != "" {
		userName = u.Name
	}

	if action == "approve" {
		h.approveReviewDecision(w, r, issue, userID, userName, note)
		return
	}
	h.requestReviewChanges(w, r, issue, userID, userName, note, req.ExpectedVersion, req.TargetStepID)
}

// approveReviewDecision is the compatibility entry point for release approval.
// The persisted orchestration release step remains the only scheduler.
func (h *Handler) approveReviewDecision(w http.ResponseWriter, r *http.Request, issue db.Issue, userID, userName, note string) {
	ctx := r.Context()
	run, err := h.Queries.GetActiveOrchestrationRunForIssue(ctx, issue.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "approve requires an active orchestration release gate")
		return
	}
	steps, err := h.Queries.ListOrchestrationSteps(ctx, run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load active release gate failed")
		return
	}
	var release db.OrchestrationStep
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index].Stage == "release" && steps[index].Status == "waiting_approval" {
			release = steps[index]
			break
		}
	}
	if !release.ID.Valid {
		writeError(w, http.StatusConflict, "release gate is not waiting for approval")
		return
	}
	approved, err := h.approvePersistedOrchestrationStep(ctx, issue, run, release, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	h.recordReleaseApproval(r, issue, userID, userName, note)
	slog.Info("review decision: persisted release approved",
		"issue_id", uuidToString(issue.ID), "user_id", userID, "release_step_id", uuidToString(approved.ID))
	writeJSON(w, http.StatusOK, map[string]any{
		"action": "approve", "merged_dispatch": approved.AgentID.Valid,
		"release_step_id": uuidToString(approved.ID),
	})
}

// recordReleaseApproval keeps compatibility labels and the human-readable
// audit trail in sync with the persisted release event. Failures here are
// recoverable display issues; they never start a second scheduler.
func (h *Handler) recordReleaseApproval(r *http.Request, issue db.Issue, userID, userName, note string) {
	ctx := r.Context()
	if !h.issueHasLabel(ctx, issue, mergeApprovedLabel) {
		labelID, err := h.ensureLabel(ctx, issue.WorkspaceID, mergeApprovedLabel, mergeApprovedLabelColor)
		if err != nil {
			slog.Warn("review decision: ensure merge:approved failed", "error", err, "issue_id", uuidToString(issue.ID))
			return
		}
		if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("review decision: attach merge:approved failed", "error", err, "issue_id", uuidToString(issue.ID))
			return
		}
		// Publish the FULL label set, not just {issue_id}: the frontend
		// labels-changed handler REPLACES the issue's labels with the payload,
		// so an issue_id-only event wipes every client's label cache. On a read
		// failure skip the broadcast — clients recover on their next query.
		if labels, ok := h.listLabelsForIssueSafe(r, issue.ID, issue.WorkspaceID); ok {
			h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
				"issue_id": uuidToString(issue.ID),
				"labels":   labelsToResponse(labels),
			})
		}
	}

	sysBody := "✅ Release approved by " + userName + ". The persisted release worker now owns the merge."
	if note != "" {
		sysBody += " Note: " + note
	}
	h.postDesignSystemComment(r, issue, sysBody)
}

// neutralizeNoteMentions defuses mention links in the durable audit record and
// worker instruction. Request changes does not use mentions as a scheduler.
func neutralizeNoteMentions(note string) string {
	return strings.ReplaceAll(note, "](mention://", "] (mention-stripped://")
}

type reviewCorrectionPlan struct {
	BaseStep         db.OrchestrationStep
	TargetStep       db.OrchestrationStep
	CorrectionAgent  pgtype.UUID
	IntegrationAgent pgtype.UUID
	QAAgent          pgtype.UUID
	ReviewAgent      pgtype.UUID
	SquadID          pgtype.UUID
	ControllerID     pgtype.UUID
	Capability       string
	Retire           []db.OrchestrationStep
}

func buildReviewCorrectionPlan(run db.OrchestrationRun, steps []db.OrchestrationStep, targetStepID string) (reviewCorrectionPlan, error) {
	plan := reviewCorrectionPlan{ControllerID: run.ControllerAgentID, Capability: "implementation"}
	for _, step := range steps {
		if step.Status == "queued" || step.Status == "running" {
			return plan, fmt.Errorf("active orchestration work must finish before requesting changes")
		}
		if (step.Status == "pending" || step.Status == "waiting_approval") && (step.Stage == "qa" || step.Stage == "review" || step.Stage == "release") {
			plan.Retire = append(plan.Retire, step)
		}
	}
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if step.Stage == "dev" && step.StepKind == "integration" && step.Status == "completed" && step.MergeStatus == "clean" && step.IntegrationStatus == "complete" && len(decodeArtifactRepos(step)) > 0 {
			plan.BaseStep = step
			break
		}
	}
	if !plan.BaseStep.ID.Valid {
		for index := len(steps) - 1; index >= 0; index-- {
			step := steps[index]
			if step.Stage == "dev" && step.StepKind == "task" && step.Status == "completed" && step.MergeStatus == "clean" && len(decodeArtifactRepos(step)) > 0 {
				plan.BaseStep = step
				break
			}
		}
	}
	if !plan.BaseStep.ID.Valid {
		return plan, fmt.Errorf("a clean completed development artifact is required")
	}

	requestedTarget := strings.TrimSpace(targetStepID)
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if step.Stage != "dev" || step.StepKind != "task" || step.Status != "completed" || !step.AgentID.Valid {
			continue
		}
		if requestedTarget != "" && uuidToString(step.ID) != requestedTarget {
			continue
		}
		plan.TargetStep = step
		break
	}
	if requestedTarget != "" && !plan.TargetStep.ID.Valid {
		return plan, fmt.Errorf("target_step_id must reference a completed development worker")
	}
	if plan.TargetStep.AgentID.Valid {
		plan.CorrectionAgent = plan.TargetStep.AgentID
		plan.Capability = plan.TargetStep.Capability
		plan.SquadID = plan.TargetStep.SquadID
	}
	if !plan.CorrectionAgent.Valid {
		plan.CorrectionAgent = run.ControllerAgentID
	}
	if !plan.CorrectionAgent.Valid {
		return plan, fmt.Errorf("no correction agent is available in the active plan")
	}
	if !validOrchestrationCapability(plan.Capability) || plan.Capability == "integration" {
		plan.Capability = "implementation"
	}
	if !plan.SquadID.Valid {
		plan.SquadID = plan.BaseStep.SquadID
	}

	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if !plan.IntegrationAgent.Valid && step.StepKind == "integration" && step.AgentID.Valid {
			plan.IntegrationAgent = step.AgentID
		}
		if !plan.QAAgent.Valid && step.Stage == "qa" && step.AgentID.Valid {
			plan.QAAgent = step.AgentID
		}
		if !plan.ReviewAgent.Valid && step.Stage == "review" && step.AgentID.Valid {
			plan.ReviewAgent = step.AgentID
		}
	}
	if !plan.IntegrationAgent.Valid {
		plan.IntegrationAgent = plan.ControllerID
	}
	if !plan.IntegrationAgent.Valid {
		plan.IntegrationAgent = plan.CorrectionAgent
	}
	if !plan.QAAgent.Valid {
		plan.QAAgent = plan.ControllerID
	}
	if !plan.QAAgent.Valid {
		plan.QAAgent = plan.CorrectionAgent
	}
	if !plan.ReviewAgent.Valid {
		plan.ReviewAgent = plan.ControllerID
	}
	if !plan.ReviewAgent.Valid {
		plan.ReviewAgent = plan.CorrectionAgent
	}
	return plan, nil
}

type reviewCorrectionRevisionResult struct {
	UpdatedIssue     db.Issue
	Comment          db.Comment
	Version          int32
	RevisionID       pgtype.UUID
	CorrectionStepID pgtype.UUID
}

func (h *Handler) createReviewCorrectionRevision(r *http.Request, issue db.Issue, run db.OrchestrationRun, plan reviewCorrectionPlan, userID, userName, note string, expectedVersion int32) (reviewCorrectionRevisionResult, error) {
	result := reviewCorrectionRevisionResult{}
	if expectedVersion < 1 {
		expectedVersion = run.PlanVersion
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		return result, fmt.Errorf("begin plan revision: %w", err)
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	advanced, err := qtx.AdvanceOrchestrationPlanVersion(r.Context(), db.AdvanceOrchestrationPlanVersionParams{ID: run.ID, PlanVersion: expectedVersion})
	if err != nil {
		return result, fmt.Errorf("plan version changed; reload and retry")
	}
	for _, step := range plan.Retire {
		if _, err = qtx.RetirePendingOrchestrationStep(r.Context(), db.RetirePendingOrchestrationStepParams{ID: step.ID, RetiredInVersion: pgtype.Int4{Int32: advanced.PlanVersion, Valid: true}}); err != nil {
			return result, fmt.Errorf("retire stale gate: %w", err)
		}
	}
	steps, err := qtx.ListOrchestrationSteps(r.Context(), run.ID)
	if err != nil {
		return result, err
	}
	position := int32(len(steps))
	versionSuffix := fmt.Sprintf("v%d", advanced.PlanVersion)
	parentID := plan.TargetStep.ID
	if !parentID.Valid {
		parentID = plan.BaseStep.ID
	}
	createStep := func(key, title, stage, kind, capability string, agentID, dependencyID, parentStepID pgtype.UUID, approval bool, instructions string) (db.OrchestrationStep, error) {
		maxAttempts := int32(2)
		if approval {
			maxAttempts = 1
		}
		created, createErr := qtx.CreateOrchestrationStep(r.Context(), db.CreateOrchestrationStepParams{
			RunID: run.ID, StepKey: key, Title: title, Stage: stage, Position: position,
			AgentID: agentID, ModelOverride: pgtype.Text{}, DependsOnStepID: dependencyID, ApprovalRequired: approval,
			MaxAttempts: maxAttempts, Instructions: instructions, ParentStepID: parentStepID,
			SquadID: plan.SquadID, ControllerAgentID: plan.ControllerID,
			IntroducedInVersion: advanced.PlanVersion, StepKind: kind, Capability: capability,
		})
		position++
		if createErr != nil {
			return created, createErr
		}
		if dependencyID.Valid {
			createErr = qtx.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: created.ID, DependsOnStepID: dependencyID})
		}
		return created, createErr
	}
	correction, err := createStep(
		"changes-"+versionSuffix, "Address requested changes", "dev", "task", plan.Capability,
		plan.CorrectionAgent, plan.BaseStep.ID, parentID, false,
		"Start from the exact integrated artifact handed off by the dependency. Address this human review request: "+note+". Keep the correction focused, verify it, and leave a clean committed branch for integration.",
	)
	if err != nil {
		return result, fmt.Errorf("create correction step: %w", err)
	}
	integration, err := createStep(
		"integrate-"+versionSuffix, "Integrate requested changes", "dev", "integration", "integration",
		plan.IntegrationAgent, correction.ID, pgtype.UUID{}, false,
		"Integrate the exact correction dependency, verify every dependency HEAD is present, resolve conflicts without dropping reviewed work, and leave a clean committed integration HEAD.",
	)
	if err != nil {
		return result, fmt.Errorf("create integration step: %w", err)
	}
	qa, err := createStep(
		"qa-"+versionSuffix, "Verify corrected integration", "qa", "task", "qa",
		plan.QAAgent, integration.ID, pgtype.UUID{}, false,
		"Verify the exact corrected integration HEAD. Run the required deterministic checks and report evidence without modifying the artifact.",
	)
	if err != nil {
		return result, fmt.Errorf("create QA step: %w", err)
	}
	review, err := createStep(
		"review-"+versionSuffix, "Review corrected integration", "review", "task", "review",
		plan.ReviewAgent, integration.ID, pgtype.UUID{}, false,
		"Review the exact corrected integration HEAD against the human request and prior findings. Report evidence without modifying the artifact.",
	)
	if err != nil {
		return result, fmt.Errorf("create review step: %w", err)
	}
	releaseAgent := plan.ControllerID
	if !releaseAgent.Valid {
		releaseAgent = plan.IntegrationAgent
	}
	release, err := createStep(
		"release-"+versionSuffix, "Approve corrected release", "release", "task", "release",
		releaseAgent, qa.ID, pgtype.UUID{}, true,
		"After human approval, merge only the exact corrected integration verified by QA and review. Verify the pull request identity and reviewed HEAD before merging; stop and report if either moved.",
	)
	if err != nil {
		return result, fmt.Errorf("create release step: %w", err)
	}
	if err = qtx.AddOrchestrationStepDependency(r.Context(), db.AddOrchestrationStepDependencyParams{StepID: release.ID, DependsOnStepID: review.ID}); err != nil {
		return result, fmt.Errorf("join corrected gates: %w", err)
	}

	patch, _ := json.Marshal(map[string]any{
		"operation": "request_changes", "target_step_id": uuidToString(parentID),
		"base_artifact_step_id": uuidToString(plan.BaseStep.ID), "note": note,
		"added_step_ids": []string{uuidToString(correction.ID), uuidToString(integration.ID), uuidToString(qa.ID), uuidToString(review.ID), uuidToString(release.ID)},
	})
	revision, err := qtx.CreateOrchestrationPlanRevision(r.Context(), db.CreateOrchestrationPlanRevisionParams{
		RunID: run.ID, Version: advanced.PlanVersion, ActorType: "member", ActorID: parseUUID(userID),
		Reason: "Changes requested by " + userName, Patch: patch,
	})
	if err != nil {
		return result, fmt.Errorf("record plan revision: %w", err)
	}
	updated, err := qtx.UpdateIssueStatus(r.Context(), db.UpdateIssueStatusParams{ID: issue.ID, Status: "in_progress", WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return result, fmt.Errorf("move issue to in_progress: %w", err)
	}
	comment, err := qtx.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, AuthorType: "member", AuthorID: parseUUID(userID),
		Content: fmt.Sprintf("Changes requested by %s. Plan revised to v%d and routed through correction, integration, QA, review, and release. Note: %s", userName, advanced.PlanVersion, note),
		Type:    "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		return result, fmt.Errorf("record change request: %w", err)
	}
	if _, err = qtx.SetOrchestrationRunStatus(r.Context(), db.SetOrchestrationRunStatusParams{ID: run.ID, Status: "running"}); err != nil {
		return result, fmt.Errorf("resume orchestration: %w", err)
	}
	if err = tx.Commit(r.Context()); err != nil {
		return result, err
	}
	return reviewCorrectionRevisionResult{
		UpdatedIssue: updated, Comment: comment, Version: advanced.PlanVersion,
		RevisionID: revision.ID, CorrectionStepID: correction.ID,
	}, nil
}

// requestReviewChanges creates a new versioned DAG cycle. It never uses an
// @mention comment as an implicit second scheduler.
func (h *Handler) requestReviewChanges(w http.ResponseWriter, r *http.Request, issue db.Issue, userID, userName, note string, expectedVersion int32, targetStepID string) {
	ctx := r.Context()
	if note == "" {
		writeError(w, http.StatusBadRequest, "note is required for request_changes")
		return
	}
	// The note becomes both an agent instruction and an audit comment. Keep it
	// inert so neither current nor future mention parsers can turn it into work.
	note = neutralizeNoteMentions(note)

	run, err := h.Queries.GetActiveOrchestrationRunForIssue(ctx, issue.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "request_changes requires an active orchestration plan")
		return
	}
	steps, err := h.Queries.ListOrchestrationSteps(ctx, run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load active plan failed")
		return
	}
	plan, err := buildReviewCorrectionPlan(run, steps, targetStepID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if _, err = h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: plan.CorrectionAgent, WorkspaceID: issue.WorkspaceID}); err != nil {
		writeError(w, http.StatusConflict, "correction agent is no longer available in this workspace")
		return
	}
	result, err := h.createReviewCorrectionRevision(r, issue, run, plan, userID, userName, note, expectedVersion)
	if err != nil {
		if strings.Contains(err.Error(), "plan version changed") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create correction plan revision")
		}
		return
	}
	// Broadcast the status transition so boards / lists / other open detail
	// views refresh off in_review — a comment-only event leaves them stale
	// (they don't recompute status from a new comment). Matches how the HTTP
	// status handlers publish transitions (issue, status_changed, prev_status).
	h.publish(protocol.EventIssueUpdated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"issue":          issueToResponse(result.UpdatedIssue, h.getIssuePrefix(ctx, issue.WorkspaceID)),
		"status_changed": true,
		"prev_status":    issue.Status,
	})

	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"comment":      commentToResponse(result.Comment, nil, nil),
		"issue_title":  issue.Title,
		"issue_status": "in_progress",
	})
	h.createOrchestrationEvent(ctx, run.ID, result.CorrectionStepID, "plan_revised", "member", parseUUID(userID), map[string]any{
		"version": result.Version, "operation": "request_changes", "reason": note,
	})
	dispatchErr := h.dispatchNextOrchestrationStep(ctx, run.ID, result.UpdatedIssue)
	slog.Info("review decision: changes requested",
		"issue_id", uuidToString(issue.ID), "user_id", userID, "plan_version", result.Version, "dispatch_error", dispatchErr)
	writeJSON(w, http.StatusOK, map[string]any{
		"action": "request_changes", "status": "in_progress", "dispatched": dispatchErr == nil,
		"plan_version": result.Version, "revision_id": uuidToString(result.RevisionID), "correction_step_id": uuidToString(result.CorrectionStepID),
	})
}
