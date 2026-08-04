package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Human QA override with PROVENANCE (Phase 2 of the QA-stage review — "make
// the human first-class"). The QA lens's Override used to be two bare label
// calls from the client: nothing recorded WHO overrode or WHY, the /qa queue
// kept showing the agent's stale summary on an overridden row, and
// qa_evidence.verdict kept contradicting the label. This endpoint makes an
// override a real, attributed verdict:
//
//   - the qa:pass/qa:fail LABEL flips exactly as before (attach + detach the
//     opposite + the same downstream automation chain the label handler fires);
//   - the qa_evidence CURRENT row is REPLACED via the same ("", "") upsert key
//     CaptureQAEvidence uses — ONE current evidence row per issue, now with
//     source="human", the reason as summary, and the override actor stamped
//     into result_json.override (the prior agent result_json is PRESERVED
//     underneath so the command-table evidence stays visible);
//   - a timeline comment records the decision in prose.
//
// ReconcileQAState then reflects the override naturally, with the intended
// matrix: override to FAIL → fail (the label wins); override to PASS with a
// case's latest run still failing → pass_with_failing_cases (an override
// asserts the human's verdict, it does NOT erase a known-red case — the chip
// shows "Pass · N failing" and the merge gate stays closed); override to PASS
// with no failing case → pass. stale/blocked labels, if present, keep their
// usual precedence — an override does not clear qa:stale/qa:blocked.
//
// Route-gated by RequireHumanActor (a machine credential can never override)
// and membership-checked via loadIssueForUser.

// qaOverrideReasonMaxRunes caps the reason at a comment-sized note.
const qaOverrideReasonMaxRunes = 2000

type qaOverrideRequest struct {
	Verdict string `json:"verdict"` // "pass" | "fail"
	Reason  string `json:"reason"`  // optional free text — WHY the human overrode
}

// qaOverrideStamp is what gets merged into result_json.override — the durable
// actor identity + reason (zero-migration: qa_evidence has no actor column,
// and the result blob is already schema-loose on both sides).
type qaOverrideStamp struct {
	ByUserID string `json:"by_user_id"`
	ByName   string `json:"by_name"`
	Reason   string `json:"reason"`
}

// OverrideQAVerdict handles POST /api/issues/{id}/qa-override.
func (h *Handler) OverrideQAVerdict(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req qaOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	verdict := strings.ToLower(strings.TrimSpace(req.Verdict))
	if verdict != "pass" && verdict != "fail" {
		writeError(w, http.StatusBadRequest, "verdict must be pass or fail")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if runes := []rune(reason); len(runes) > qaOverrideReasonMaxRunes {
		reason = string(runes[:qaOverrideReasonMaxRunes-1]) + "…"
	}

	userUUID := parseUUID(userID)
	userName := "a teammate"
	if u, err := h.Queries.GetUser(r.Context(), userUUID); err == nil && strings.TrimSpace(u.Name) != "" {
		userName = u.Name
	}

	label, color, opposite := "qa:pass", "#22c55e", "qa:fail"
	if verdict == "fail" {
		label, color, opposite = "qa:fail", "#ef4444", "qa:pass"
	}

	// LABEL FIRST — same ordering contract as CaptureQAEvidence: the label is
	// what the merge gate reads, so evidence must never exist without it.
	alreadyHad := h.issueHasLabelNameHandler(r.Context(), issue, label)
	// hadOpposite BEFORE the detach below: an override to pass that displaces
	// a qa:fail is a RECOVERY (NotifyQAVerdict distinguishes it).
	hadOpposite := h.issueHasLabelNameHandler(r.Context(), issue, opposite)
	if !alreadyHad {
		labelID, err := h.ensureLabel(r.Context(), issue.WorkspaceID, label, color)
		if err != nil {
			slog.Warn("qa override: ensure label failed", "error", err, "label", label, "issue_id", uuidToString(issue.ID))
			writeError(w, http.StatusInternalServerError, "failed to set the verdict label")
			return
		}
		if err := h.Queries.AttachLabelToIssue(r.Context(), db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("qa override: attach label failed", "error", err, "label", label, "issue_id", uuidToString(issue.ID))
			writeError(w, http.StatusInternalServerError, "failed to set the verdict label")
			return
		}
	}
	h.TaskService.DetachIssueLabelByName(r.Context(), issue, opposite)

	// EVIDENCE — replace the current row (same ("", "") key CaptureQAEvidence
	// writes, so there is always exactly ONE current row per issue). The prior
	// result_json is preserved with the override stamp merged in, so the
	// Checks section keeps rendering the command table the agent produced
	// while verdict/source/summary now speak for the human.
	// Run identity (Phase 3): triggered_by="human". commit_sha is PRESERVED
	// from the prior row — the human is judging the same tested state the
	// agent reported, and keeping the sha means stale-green invalidation
	// (head moved past the evidence sha → stale) still applies to overridden
	// verdicts instead of being silently disarmed by an override.
	priorCommitSha := ""
	if prior, perr := h.Queries.GetLatestQAEvidenceForIssue(r.Context(), db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); perr == nil {
		priorCommitSha = prior.CommitSha
	}
	resultJSON := h.overrideResultJSON(r.Context(), issue, qaOverrideStamp{
		ByUserID: userID, ByName: userName, Reason: reason,
	})
	summary := reason
	if summary == "" {
		summary = "Overridden by " + userName
	}
	evidence, err := h.Queries.UpsertQAEvidence(r.Context(), db.UpsertQAEvidenceParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		BaselineRef: "",
		BranchSha:   "",
		Verdict:     verdict,
		Summary:     summary,
		ResultJson:  resultJSON,
		Source:      "human",
		CommitSha:   priorCommitSha,
		TriggeredBy: "human",
	})
	if err != nil {
		slog.Warn("qa override: evidence upsert failed", "error", err, "issue_id", uuidToString(issue.ID))
		writeError(w, http.StatusInternalServerError, "failed to record the override")
		return
	}

	// TIMELINE — the decision in prose, attributed. Member-authored so every
	// surface that renders comments (timeline, Bitrix sync, exports) carries
	// the same record.
	note := "QA verdict overridden to " + strings.ToUpper(verdict) + " by " + userName
	if reason != "" {
		note += ": " + reason
	}
	if comment, cerr := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    userUUID,
		Content:     note,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	}); cerr != nil {
		slog.Warn("qa override: timeline comment failed", "error", cerr, "issue_id", uuidToString(issue.ID))
	} else {
		h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
			"comment": map[string]any{
				"id":          uuidToString(comment.ID),
				"issue_id":    uuidToString(issue.ID),
				"author_type": "member",
				"author_id":   userID,
				"content":     comment.Content,
				"type":        comment.Type,
				"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title": issue.Title,
		})
	}

	h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"issue_id": uuidToString(issue.ID),
	})
	h.publish(protocol.EventQAEvidenceReady, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"issue_id": uuidToString(issue.ID),
		"verdict":  verdict,
	})

	// Same downstream automation chain a label attach fires (merge-on-pass,
	// dev-lead routing / auto-bug on fail, auto-docs) — only on a NEW attach,
	// mirroring the label handler's idempotency guard.
	if !alreadyHad {
		go h.maybeAutoDocsOnLabel(context.Background(), issue, label, userID)
		go h.maybeMergeOnQAPass(context.Background(), issue, label, userID)
		go h.maybeRunReviewOnQAPass(context.Background(), issue, label, userID)
		go h.maybeRouteToDevLeadOnQAFail(context.Background(), issue, label, userID)
		go h.maybeAutoFileBugOnQAFail(context.Background(), issue, label, userID)
		go h.clearQAFailAutorouteBudget(context.Background(), issue, label)
	}

	// Typed inbox notification — same dispatcher CaptureQAEvidence uses. The
	// overriding human is excluded by NotifyQAVerdict itself (actor=member);
	// an override to pass that displaced a qa:fail notifies as a recovery.
	h.TaskService.NotifyQAVerdict(r.Context(), issue, verdict, hadOpposite, "member", userUUID, summary)

	slog.Info("qa verdict overridden", "issue_id", uuidToString(issue.ID), "verdict", verdict, "user_id", userID)
	reconciled := h.reconciledQAState(r.Context(), issue, true)
	writeJSON(w, http.StatusOK, qaEvidenceToResponse(evidence, reconciled))
}

// overrideResultJSON builds the override row's result_json: the CURRENT
// evidence row's result blob (preserved so the command table survives the
// override) with the override stamp merged at the top-level "override" key.
// A missing/malformed prior blob degrades to just the stamp — never fails
// the override.
func (h *Handler) overrideResultJSON(ctx context.Context, issue db.Issue, stamp qaOverrideStamp) []byte {
	base := map[string]json.RawMessage{}
	if prior, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil && len(prior.ResultJson) > 0 {
		// Best-effort: a non-object prior blob just gets replaced.
		_ = json.Unmarshal(prior.ResultJson, &base)
	}
	stampRaw, err := json.Marshal(stamp)
	if err != nil {
		return []byte("{}")
	}
	base["override"] = stampRaw
	out, err := json.Marshal(base)
	if err != nil {
		return []byte("{}")
	}
	return out
}
