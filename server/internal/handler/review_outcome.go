package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Review OUTCOME routing — the two exits of the review-first pipeline, entirely
// inside Agora:
//
//	review:pass  → open the merge request (maybeOpenPROnReviewPass)
//	review:fail  → status back to todo + routed to the dev side, with the
//	               reviewer's findings attached (maybeRouteToDevOnReviewFail)
//
// Both exits also post a Telegram notice when the project configured one. The
// per-USER notification needs nothing here: NotifyReviewVerdict already writes
// typed inbox items (review_failed / review_passed / merge_ready) and the
// EventInboxNew subscriber DMs each member recipient on Telegram. What is added
// here is the shared-ROOM notice (a team group chat), which has no inbox path.
//
// Why the MR opens on pass rather than earlier: the review reads the branch diff
// (sliceActionReviewBranchContext), so a rejected change never becomes a merge
// request at all — the noise of "MR opened, then closed" disappears, and the MR
// list stays a list of reviewed work.

// reviewFailAutorouteMaxAttempts bounds the review↔dev loop. A change that keeps
// failing review must stop bouncing and wait for a human; the review:fail label
// still surfaces it. Mirrors qaFailAutorouteMaxAttempts.
const reviewFailAutorouteMaxAttempts = 5

// reviewFailAutorouteCountKey is the loop-cap counter, reset on review:pass.
const reviewFailAutorouteCountKey = "review_fail_autoroute_count"

// openPRMarker tags an auto-fired open_pr dispatch so a second review:pass in
// the same cycle cannot summon a second opener (two agents racing the same
// branch push would open duplicate merge requests).
const openPRMarker = "<!--open-pr:auto-->"

// reviewFailAutorouteEnabled gates review:fail → todo + dev routing.
// Project-scoped, default off (it changes an issue's status automatically).
func (h *Handler) reviewFailAutorouteEnabled(ctx context.Context, issue db.Issue) bool {
	return config.BoolFrom(h.projectConfigOverrides(ctx, issue), "AGORA_REVIEW_FAIL_AUTOROUTE_ENABLED")
}

// reviewPassOpenPREnabled gates review:pass → open_pr. Project-scoped, default
// off: opening a merge request is an outward-facing write on the team's repo.
func (h *Handler) reviewPassOpenPREnabled(ctx context.Context, issue db.Issue) bool {
	return config.BoolFrom(h.projectConfigOverrides(ctx, issue), "AGORA_REVIEW_PASS_OPEN_PR_ENABLED")
}

// reviewTelegramNotifyEnabled gates the shared-room review notice. Separate from
// the report chat id so a workspace that already posts autopilot reports to a
// group does not silently start getting per-review chatter too.
func (h *Handler) reviewTelegramNotifyEnabled(ctx context.Context, issue db.Issue) bool {
	return config.BoolFrom(h.projectConfigOverrides(ctx, issue), "AGORA_TELEGRAM_REVIEW_NOTIFY_ENABLED")
}

// reviewVerdictSummary returns the reviewer's own one-line summary plus its
// blocker count, read from the newest ```review-result``` block. Falls back to a
// generic line when no block parses, so a caller always has something to say.
func (h *Handler) reviewVerdictSummary(ctx context.Context, issue db.Issue) (summary string, blockers int) {
	payload, _, _, _, found, err := h.TaskService.LatestReviewResultForIssue(ctx, issue)
	if err != nil || !found {
		return "", 0
	}
	for _, f := range payload.Findings {
		if strings.EqualFold(strings.TrimSpace(f.Severity), "blocker") {
			blockers++
		}
	}
	return strings.TrimSpace(payload.Summary), blockers
}

// maybeOpenPROnReviewPass dispatches open_pr when a review:pass lands and no
// pull/merge request exists yet — the review-first order's "clean review opens
// the request" step. Guards, cheapest first:
//   - AGORA_REVIEW_PASS_OPEN_PR_ENABLED off (default);
//   - the label is not review:pass;
//   - a standing review:fail (fail wins — the verdict pair may be mid-replace);
//   - a PR/MR already exists → nothing to open;
//   - no resolvable branch → nothing to open it FROM;
//   - a dispatch already went out for this issue.
//
// The opener is the AUTHOR side (the issue's orchestrator / agent assignee), never
// the reviewer: a reviewer that pushes is no longer an independent reviewer.
func (h *Handler) maybeOpenPROnReviewPass(ctx context.Context, issue db.Issue, labelName, actorID string) {
	if h.orchestrationOwnsIssuePipeline(ctx, issue.ID) {
		return
	}
	if !h.reviewPassOpenPREnabled(ctx, issue) {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != service.ReviewLabelPass {
		return
	}
	defer lockIssueQA(uuidToString(issue.ID))()

	if h.issueHasLabel(ctx, issue, service.ReviewLabelFail) {
		return
	}
	if h.issueHasKnownPR(ctx, issue) {
		return
	}
	branch := h.issueReviewBranch(ctx, issue)
	if branch == "" {
		slog.Info("auto open_pr: no resolvable branch — skipping", "issue_id", uuidToString(issue.ID))
		return
	}
	if h.issueHasCommentMarker(ctx, issue, openPRMarker) {
		return
	}
	opener, ok := h.orchestratorForIssue(ctx, issue)
	if !ok {
		slog.Info("auto open_pr: no agent orchestrator to open the request — leaving to a human",
			"issue_id", uuidToString(issue.ID))
		return
	}

	instruction := buildSliceInstruction(sliceActionOpenPR, "") + h.sliceActionOpenPRContext(ctx, issue, branch)
	authorType, authorID := h.dispatchAuthor(ctx, issue, "member", actorID)
	if !authorID.Valid {
		slog.Warn("auto open_pr: no valid dispatch author, skipping", "issue_id", uuidToString(issue.ID))
		return
	}
	content := agentProtocolMarker(sliceActionOpenPR) + openPRMarker + "\n" +
		fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(opener.Name), uuidToString(opener.ID)) + instruction
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
		slog.Warn("auto open_pr: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(authorID), nil)
	slog.Info("auto open_pr fired on review:pass",
		"issue_id", uuidToString(issue.ID), "agent_id", uuidToString(opener.ID), "branch", branch)
}

// sliceActionOpenPRContext names the branch, the base branch and the reviewed
// commit for the open_pr dispatch, so the opener neither guesses a base nor
// re-derives what was reviewed.
func (h *Handler) sliceActionOpenPRContext(ctx context.Context, issue db.Issue, branch string) string {
	isGitLab, hint := h.issueGitLabRepoConfig(ctx, issue)
	var b strings.Builder
	b.WriteString(" BRANCH TO PROPOSE: `" + branch + "`.")
	if isGitLab {
		b.WriteString(" BASE BRANCH: `" + gitlabBaseBranch(hint) +
			"` — this is a GitLab repository, so use the merge-request push options (there is no `gh` here).")
	} else {
		b.WriteString(" BASE BRANCH: the repository's integration branch (`gh pr create --base <base>`).")
	}
	if key := h.issueKey(ctx, issue); key != "" {
		b.WriteString(" ISSUE KEY: " + key + ".")
	}
	if payload, _, reviewerID, _, found, err := h.TaskService.LatestReviewResultForIssue(ctx, issue); err == nil && found {
		reviewer := "the review agent"
		if reviewerID.Valid {
			if agent, aerr := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: reviewerID, WorkspaceID: issue.WorkspaceID,
			}); aerr == nil {
				reviewer = agent.Name
			}
		}
		b.WriteString(" REVIEW THAT PASSED: reviewer " + reviewer)
		if sha := strings.TrimSpace(payload.CommitSha); sha != "" {
			b.WriteString(", reviewed commit " + sha)
		}
		if s := strings.TrimSpace(payload.Summary); s != "" {
			b.WriteString(" — \"" + s + "\"")
		}
		b.WriteString(". Name these in the request body.")
	}
	return b.String()
}

// maybeRouteToDevOnReviewFail is the review:fail exit: the change goes BACK to
// the dev side and the issue returns to todo (re-queued, un-owned work — the same
// semantics a tracker's "Returned" column carries). Mirrors the qa:fail autoroute
// including its loop cap, and carries the reviewer's blocking findings so the
// retry differs from a blind one.
func (h *Handler) maybeRouteToDevOnReviewFail(ctx context.Context, issue db.Issue, labelName, actorID string) {
	if h.orchestrationOwnsIssuePipeline(ctx, issue.ID) {
		return
	}
	if !h.reviewFailAutorouteEnabled(ctx, issue) {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != service.ReviewLabelFail {
		return
	}
	// Every agent-run task has an orchestrator (squad lead, or the solo agent
	// itself). A human/member-assigned or unassigned issue has none → manual
	// triage stands, exactly as on the qa:fail path.
	leader, ok := h.orchestratorForIssue(ctx, issue)
	if !ok {
		return
	}
	attempts := issueMetadataInt(issue.Metadata, reviewFailAutorouteCountKey)
	if attempts >= reviewFailAutorouteMaxAttempts {
		slog.Info("review-fail autoroute: attempt cap reached, leaving for human",
			"issue_id", uuidToString(issue.ID), "attempts", attempts)
		return
	}

	summary, blockers := h.reviewVerdictSummary(ctx, issue)
	headline := "Code review FAILED"
	if blockers > 0 {
		headline += fmt.Sprintf(" (%d blocking finding(s))", blockers)
	}
	if summary != "" {
		headline += ": " + summary
	} else {
		headline += "."
	}

	if _, err := h.Queries.UpdateIssueAssignee(ctx, db.UpdateIssueAssigneeParams{
		ID: issue.ID, AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID: leader.ID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("review-fail autoroute: reassign failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: issue.ID, Status: "todo", WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("review-fail autoroute: status reset failed", "error", err, "issue_id", uuidToString(issue.ID))
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Key: reviewFailAutorouteCountKey, Value: []byte(strconv.Itoa(attempts + 1)),
	}); err != nil {
		slog.Warn("review-fail autoroute: attempt-count stamp failed", "error", err, "issue_id", uuidToString(issue.ID))
	}

	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(leader.Name), uuidToString(leader.ID)) +
		headline + " Read the reviewer's findings above (each names a file, a line and a severity), fix every " +
		"`blocker`, and address the `major` findings or say why they stand. Then move the task back to " +
		"in_review so the review re-runs — do NOT open a merge request yourself while the review is red."
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		AuthorType: "member", AuthorID: parseUUID(actorID),
		Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("review-fail autoroute: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, "member", actorID, nil)
	slog.Info("review-fail autoroute: returned to todo and routed to the orchestrator",
		"issue_id", uuidToString(issue.ID),
		"orchestrator_agent_id", uuidToString(leader.ID), "attempt", attempts+1)
}

// clearReviewFailAutorouteBudget resets the review↔dev loop counter when the
// review finally passes, so a later regression starts with a fresh budget
// instead of inheriting a spent one. Mirrors clearQAFailAutorouteBudget.
func (h *Handler) clearReviewFailAutorouteBudget(ctx context.Context, issue db.Issue, labelName string) {
	if strings.ToLower(strings.TrimSpace(labelName)) != service.ReviewLabelPass {
		return
	}
	if issueMetadataInt(issue.Metadata, reviewFailAutorouteCountKey) == 0 {
		return
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Key: reviewFailAutorouteCountKey, Value: []byte("0"),
	}); err != nil {
		slog.Warn("review-fail autoroute: budget reset failed", "error", err, "issue_id", uuidToString(issue.ID))
	}
}

// onReviewVerdictLabel is the SINGLE entry point for everything a landed review
// verdict triggers, so the three ingress paths (CLI label attach, HTTP verdict
// comment capture, task-completion capture) cannot drift apart in what they fire.
// Every step is individually gated, guarded and best-effort.
//
// Order is deliberate: routing runs BEFORE the notice, so the Telegram message
// describes the step that was actually taken rather than one still pending.
func (h *Handler) onReviewVerdictLabel(ctx context.Context, issue db.Issue, gateLabel, actorID string) {
	label := strings.ToLower(strings.TrimSpace(gateLabel))
	if label != service.ReviewLabelPass && label != service.ReviewLabelFail {
		return
	}
	if label == service.ReviewLabelPass {
		h.clearReviewFailAutorouteBudget(ctx, issue, gateLabel)
		h.maybeOpenPROnReviewPass(ctx, issue, gateLabel, actorID)
		h.maybeRunTestsOnReviewPass(ctx, issue, gateLabel, actorID)
	} else {
		h.maybeRouteToDevOnReviewFail(ctx, issue, gateLabel, actorID)
	}
	verdict := "pass"
	if label == service.ReviewLabelFail {
		verdict = "fail"
	}
	// Re-read: the routing above may have changed the status/assignee the notice
	// reports on.
	fresh := issue
	if reloaded, err := h.Queries.GetIssue(ctx, issue.ID); err == nil {
		fresh = reloaded
	}
	h.SendReviewVerdictGroupNotify(ctx, fresh, verdict, h.reviewVerdictNextStep(ctx, fresh, verdict))

	// A verdict label set by an AGENT lands through the capture path, not the label
	// endpoint, so automations have to be emitted here too — otherwise a rule on
	// "review:fail attached" would fire for a human's CLI attach and stay silent for
	// the reviewer's own verdict, which is the case people actually write it for.
	h.emitAutomationEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: fresh, Label: label,
		ActorType: "agent", ActorID: actorID,
	})
}
