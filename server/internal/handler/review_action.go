package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/config"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Review stage v2 — "agent reviews, human approves" (see
// docs/review-stage-plan.md). The chain: qa:pass lands → maybeRunReviewOnQAPass
// dispatches a run_review to a reviewer that did NOT write the change → the
// reviewer's ```review-result``` block is captured into review:pass /
// review:fail (service.CaptureReviewEvidence) → the merge gate requires the
// review verdict for full-tier PR-backed issues (merge_readiness.go) → a human
// clicks Approve & merge or Request changes (review_decision.go).

// autoReviewEnabled gates the qa:pass → run_review auto-dispatch. Default off —
// opt-in, matching every other auto-* gate in slice_action.go. Project-scoped:
// a project may override AGORA_AUTO_REVIEW_ENABLED for its own issues.
func (h *Handler) autoReviewEnabled(ctx context.Context, issue db.Issue) bool {
	return config.BoolFrom(h.projectConfigOverrides(ctx, issue), "AGORA_AUTO_REVIEW_ENABLED")
}

// readyForHumanMergeMarker dedupes the human-facing "READY FOR HUMAN REVIEW +
// MERGE" note in maybeMergeOnQAPass: with the review gate in play the merge
// chain can be reached from both the qa:pass and the review:pass attach, and
// the note must post once per issue, not once per gate.
const readyForHumanMergeMarker = "<!-- ready-for-human-merge -->"

// reviewDispatchMarker tags an auto-fired run_review dispatch comment (inert
// HTML comment, invisible in rendered markdown) so a second qa:pass attach in
// the SAME review cycle — before the verdict lands — can see the in-flight
// dispatch and skip instead of summoning a second reviewer.
const reviewDispatchMarker = "<!--review-dispatch:auto-->"

// issueHasCommentMarker reports whether any comment on the issue contains the
// given marker string. Best-effort: a query error reports false.
func (h *Handler) issueHasCommentMarker(ctx context.Context, issue db.Issue, marker string) bool {
	// Newest-first (ListRecentCommentsForIssue): a dedup marker is written on
	// the freshest comments, so on a long issue (>500 comments) the ASC read
	// would return the OLDEST 500 and never see the recent marker — the dedup
	// would fail and the note would re-post (floodable).
	comments, err := h.Queries.ListRecentCommentsForIssue(ctx, db.ListRecentCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 500,
	})
	if err != nil {
		return false
	}
	for _, c := range comments {
		if strings.Contains(c.Content, marker) {
			return true
		}
	}
	return false
}

// issuePRNumberFromMetadata extracts the `pr_number` value from an issue's
// JSONB metadata, normalized whether it was stored as a JSON number or a
// numeric string. 0 when absent/unparsable (PR numbers are 1-based).
func issuePRNumberFromMetadata(raw []byte) int {
	meta := parseIssueMetadata(raw)
	v, ok := meta["pr_number"]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

// issueHasKnownPR reports whether the platform knows a pull request for this
// issue — the metadata pr_number stamp (daemon write-back) or at least one
// linked github_pull_request row (webhook sync). This is the review gate's
// applicability signal: no PR → there is no diff to review → no review gate.
func (h *Handler) issueHasKnownPR(ctx context.Context, issue db.Issue) bool {
	if issuePRNumberFromMetadata(issue.Metadata) > 0 {
		return true
	}
	prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID)
	return err == nil && len(prs) > 0
}

// reviewGateApplies reports whether the reviewer gate is REQUIRED for this
// issue (see reviewGateRequired for the full predicate: full tier + a diff to
// review + an active review). Mirrors the merge-readiness computation exactly
// so the auto-merge ordering and the endpoint can never disagree.
//
// ok=false means the labels could NOT be read. The MERGE chain must treat that
// as "cannot confirm the gate is satisfied" and fail CLOSED (do not
// auto-proceed) — the old fail-open returned "gate does not apply", which
// merged qa:pass-only work without the review that would otherwise be required.
func (h *Handler) reviewGateApplies(ctx context.Context, issue db.Issue) (required, ok bool) {
	labelRows, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return false, false
	}
	labels := make(map[string]bool, len(labelRows))
	for _, l := range labelRows {
		labels[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}
	return reviewGateRequired(reviewTierForLabels(labels), h.issueHasKnownPR(ctx, issue), labels, h.autoReviewEnabled(ctx, issue)), true
}

// reviewDispatchInFlight reports whether an auto-fired run_review dispatch is
// still awaiting its verdict: the NEWEST dispatch-marker comment has no
// review-result comment after it. A verdict posted after the marker closes the
// cycle, so a later cycle (labels cleared on in_review re-entry, fresh qa:pass)
// dispatches again. Best-effort: a query error reports false (dispatch
// proceeds; the reviewer-side pending-task guard still caps duplicates).
func (h *Handler) reviewDispatchInFlight(ctx context.Context, issue db.Issue) bool {
	// Newest-first (ListRecentCommentsForIssue) so a fresh dispatch/verdict on a
	// long issue is never hidden past a LIMIT of the OLDEST rows. Index 0 is the
	// newest comment: the newest dispatch is in flight until a verdict posted
	// AFTER it (i.e. newer → a smaller index) closes the cycle.
	comments, err := h.Queries.ListRecentCommentsForIssue(ctx, db.ListRecentCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 500,
	})
	if err != nil {
		return false
	}
	firstDispatch, firstVerdict := -1, -1
	for i, c := range comments {
		if firstDispatch == -1 && strings.Contains(c.Content, reviewDispatchMarker) {
			firstDispatch = i
		}
		if firstVerdict == -1 {
			if _, ok := service.ParseReviewResultBlock(c.Content); ok {
				firstVerdict = i
			}
		}
	}
	if firstDispatch == -1 {
		return false
	}
	return firstVerdict == -1 || firstVerdict > firstDispatch
}

// devSquadAgentsForIssue returns the ready agents of the DEV squad the issue's
// work belongs to (the squad it is assigned to, or the squad of the agent it
// is assigned to) — leader plus agent members, deduped. Empty for solo /
// non-squad issues.
func (h *Handler) devSquadAgentsForIssue(ctx context.Context, issue db.Issue) []db.Agent {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return nil
	}
	var squad db.Squad
	switch issue.AssigneeType.String {
	case "squad":
		sq, err := h.Queries.GetSquad(ctx, issue.AssigneeID)
		if err != nil {
			return nil
		}
		squad = sq
	case "agent":
		squads, err := h.Queries.ListSquadsByMember(ctx, db.ListSquadsByMemberParams{
			WorkspaceID: issue.WorkspaceID, MemberType: "agent", MemberID: issue.AssigneeID,
		})
		if err != nil || len(squads) == 0 {
			return nil
		}
		squad = squads[0]
	default:
		return nil
	}
	seen := map[string]bool{}
	var agents []db.Agent
	add := func(id pgtype.UUID) {
		if !id.Valid {
			return
		}
		k := uuidToString(id)
		if seen[k] {
			return
		}
		seen[k] = true
		a, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: issue.WorkspaceID})
		if err == nil && sliceAgentReady(a) {
			agents = append(agents, a)
		}
	}
	add(squad.LeaderID)
	if members, err := h.Queries.ListSquadMembers(ctx, squad.ID); err == nil {
		for _, m := range members {
			if m.MemberType == "agent" {
				add(m.MemberID)
			}
		}
	}
	return agents
}

// resolveReviewerAgent picks the run_review reviewer with ONE hard invariant:
// the reviewer must differ from the AUTHOR agent (the issue's agent assignee —
// an agent must never approve its own diff). Resolution order:
//  1. the dev squad leader, when it is not the author;
//  2. the least-busy other ready dev-squad agent (author excluded);
//  3. the QA squad leader (cross-squad reviewer of last resort);
//  4. none → ok=false, the caller skips with a log.
func (h *Handler) resolveReviewerAgent(ctx context.Context, issue db.Issue) (db.Agent, bool) {
	authorID := ""
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		authorID = uuidToString(issue.AssigneeID)
	}
	// A per-issue review cast wins — the orchestrator pinned a reviewer for this
	// task — but the hard invariant holds: a cast reviewer that IS the author is
	// ignored (an agent never reviews its own diff) and falls through below.
	if cast, ok := h.castAgentForStage(ctx, issue, metaCastReviewAgent); ok && uuidToString(cast.ID) != authorID {
		return cast, true
	}
	if leader, ok := h.orchestratorForIssue(ctx, issue); ok && sliceAgentReady(leader) && uuidToString(leader.ID) != authorID {
		return leader, true
	}
	var candidates []db.Agent
	for _, a := range h.devSquadAgentsForIssue(ctx, issue) {
		if uuidToString(a.ID) != authorID {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) > 0 {
		return h.pickLeastBusyQAAgent(ctx, candidates), true
	}
	if leader, ok := h.qaSquadLeader(ctx, issue.WorkspaceID); ok && uuidToString(leader.ID) != authorID {
		return leader, true
	}
	return db.Agent{}, false
}

// sliceActionReviewPRContext appends the concrete PR pointer(s) to a
// run_review instruction so the reviewer goes straight to the diff instead of
// hunting: the metadata pr_number (daemon write-back) and/or the linked PR
// rows' number + branch + head SHA (webhook sync). "" when the platform knows
// no PR — the template's own locate steps then apply.
func (h *Handler) sliceActionReviewPRContext(ctx context.Context, issue db.Issue) string {
	var b strings.Builder
	if n := issuePRNumberFromMetadata(issue.Metadata); n > 0 {
		b.WriteString(fmt.Sprintf(" PR TO REVIEW: this issue's pull request is #%d — read its diff with `gh pr diff %d` and its head SHA with `gh pr view %d --json headRefOid`.", n, n, n))
	}
	if prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID); err == nil {
		for _, pr := range prs {
			if pr.MergedAt.Valid || !strings.EqualFold(strings.TrimSpace(pr.State), "open") {
				continue
			}
			b.WriteString(fmt.Sprintf(" LINKED OPEN PR: #%d (%s/%s", pr.PrNumber, pr.RepoOwner, pr.RepoName))
			if branch := strings.TrimSpace(pr.Branch.String); pr.Branch.Valid && branch != "" {
				b.WriteString(", branch `" + branch + "`")
			}
			if sha := strings.TrimSpace(pr.HeadSha); sha != "" {
				b.WriteString(", head " + sha)
			}
			b.WriteString(").")
		}
	}
	return b.String()
}

// maybeRunReviewOnQAPass is the qa:pass → run_review auto-dispatch (Review
// stage v2). Fired detached from the SAME three newlyLabeled qa:pass call
// sites as maybeMergeOnQAPass (label attach, verdict capture, human
// override). Guards, cheapest first — each a real no-op case:
//   - AGORA_AUTO_REVIEW_ENABLED off (default) → feature not opted in;
//   - the label is not qa:pass;
//   - a review verdict already stands (review:pass/review:fail) — the cycle
//     is judged; a fresh cycle clears the labels first (clearStaleQAGateLabels);
//   - no known PR → nothing to review (the gate doesn't apply either);
//   - a dispatch from THIS cycle is still awaiting its verdict (marker check);
//   - no reviewer resolves that differs from the author agent.
//
// Best-effort + detached: any miss silently no-ops so a label attach never
// fails because of it.
func (h *Handler) maybeRunReviewOnQAPass(ctx context.Context, issue db.Issue, labelName, userID string) {
	if !h.autoReviewEnabled(ctx, issue) {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != "qa:pass" {
		return
	}
	// Manual pipeline mode: the orchestrator drives review itself. Wake it to
	// dispatch run_review instead of auto-selecting a reviewer.
	if pipelineManual(issue) {
		h.wakeOrchestratorManual(ctx, issue, "QA passed on this task — dispatch code review (run_review) to your reviewer pick", userID)
		return
	}
	// Serialize per issue: two ingress paths can land the same qa:pass
	// concurrently (capture + CLI label attach), and both would pass the
	// marker check before either writes its dispatch comment.
	defer lockIssueQA(uuidToString(issue.ID))()

	if h.issueHasLabel(ctx, issue, service.ReviewLabelPass) || h.issueHasLabel(ctx, issue, service.ReviewLabelFail) {
		return
	}
	if !h.issueHasKnownPR(ctx, issue) {
		return
	}
	if h.reviewDispatchInFlight(ctx, issue) {
		return
	}
	reviewer, ok := h.resolveReviewerAgent(ctx, issue)
	if !ok {
		slog.Info("auto run_review: no reviewer distinct from the author resolves — skipping",
			"issue_id", uuidToString(issue.ID))
		return
	}

	instruction := buildSliceInstruction(sliceActionRunReview, "") + h.sliceActionReviewPRContext(ctx, issue)
	if brief := issueBriefNote(issue.Description.String, issue.AcceptanceCriteria); brief != "" {
		instruction += "\n" + brief
	}

	// The ORCHESTRATOR is shown dispatching the review — not the human who
	// merely nudged the status (falls back to the actor when there is no agent
	// orchestrator).
	authorType, authorID := h.dispatchAuthor(ctx, issue, "member", userID)
	if !authorID.Valid {
		slog.Warn("auto run_review: no valid dispatch author, skipping", "actor_id", userID, "issue_id", uuidToString(issue.ID))
		return
	}
	content := agentProtocolMarker(sliceActionRunReview) + reviewDispatchMarker + "\n" +
		fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(reviewer.Name), uuidToString(reviewer.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  authorType,
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto run_review: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(authorID), nil)
	slog.Info("auto run_review fired on qa:pass",
		"issue_id", uuidToString(issue.ID), "reviewer_agent_id", uuidToString(reviewer.ID))
}
