package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Review verdict capture (Review stage v2 — "agent reviews, human approves").
// A run_review agent posts a ```review-result``` fenced block on its verdict
// comment; the server captures it into the review:pass / review:fail LABEL
// the merge gate keys on. Modeled on CaptureQAEvidence's label-first contract,
// with one deliberate difference: there is NO review evidence table — the
// findings live in the comment itself, and the review-verdict endpoint
// resolves the latest block by scanning comments newest-first (the
// LatestDesignProposalForIssue pattern), so no new schema is needed.

// reviewResultBlockRe extracts the ```review-result``` fenced JSON the
// run_review recipe appends to its verdict comment. Mirrors qaResultBlockRe.
var reviewResultBlockRe = regexp.MustCompile("(?s)```review-result\\s*\\n(.*?)```")

// ReviewLabelPass / ReviewLabelFail are the reviewer gate's verdict label pair
// (replace-on-write, same as qa:pass/qa:fail). Colors match the QA pair so the
// two gates read consistently across every label surface.
const (
	ReviewLabelPass      = "review:pass"
	ReviewLabelFail      = "review:fail"
	reviewLabelPassColor = "#22c55e"
	reviewLabelFailColor = "#ef4444"
)

// ReviewFinding is one reviewer finding inside a review-result block. Line is
// a pointer so "whole-file" findings (line absent/null) survive the parse
// distinctly from line 0.
type ReviewFinding struct {
	File     string `json:"file"`
	Line     *int   `json:"line"`
	Severity string `json:"severity"` // "blocker" | "major" | "minor"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// ReviewResultPayload is the structured verdict a run_review agent emits.
// commit_sha is the PR head SHA the reviewer actually read (validated to the
// 7-40 hex shape, else discarded — same rule as the QA fence).
type ReviewResultPayload struct {
	Verdict       string          `json:"verdict"`
	Summary       string          `json:"summary"`
	CommitSha     string          `json:"commit_sha"`
	FilesReviewed int             `json:"files_reviewed"`
	Findings      []ReviewFinding `json:"findings"`
}

// ParseReviewResultBlock extracts + validates the ```review-result``` block
// from a comment. Returns ok=false on no block / malformed JSON / a verdict
// that is neither pass nor fail. The commit_sha is normalized through the same
// validCommitSha rule the QA fence uses (fail-open to "").
func ParseReviewResultBlock(content string) (p ReviewResultPayload, ok bool) {
	m := reviewResultBlockRe.FindStringSubmatch(content)
	if m == nil {
		return ReviewResultPayload{}, false
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &p); err != nil {
		return ReviewResultPayload{}, false
	}
	p.Verdict = strings.ToLower(strings.TrimSpace(p.Verdict))
	if p.Verdict != "pass" && p.Verdict != "fail" {
		return ReviewResultPayload{}, false
	}
	p.CommitSha = validCommitSha(p.CommitSha)
	return p, true
}

// CaptureReviewEvidence captures a run_review verdict comment: it attaches the
// review:pass / review:fail label the merge gate keys on (label FIRST — the
// CaptureQAEvidence ordering contract), detaches the opposite verdict label
// (replace-on-write), publishes the FULL label set, and fires the typed inbox
// notification on a NEWLY landed verdict. Returns the verdict ("pass"/"fail"/
// "") and whether the label was newly attached — the handler caller fires the
// downstream merge trigger only on a new attach, so an agent that ALSO set the
// label via CLI does not double-fire it.
//
// Why the server attaches the label: same reliability reason as the QA gate —
// the run_review agent is instructed to set the label itself, but the fenced
// block is the idempotent authority so a forgotten CLI step never stalls the
// merge gate. Best-effort + detached: any miss (no block, malformed JSON,
// label failure) silently no-ops and the verdict comment still posts.
func (s *TaskService) CaptureReviewEvidence(ctx context.Context, issue db.Issue, content string, reviewerID pgtype.UUID) (verdict string, newlyLabeled bool) {
	p, ok := ParseReviewResultBlock(content)
	if !ok {
		return "", false
	}

	// Reviewer≠author invariant, enforced at CAPTURE (not only at dispatch): an
	// agent must never mint its own review:pass by posting a review-result
	// block. If the issue's assignee is an AGENT and the reviewer posting this
	// block IS that agent (self-review), reject — attach no label, post
	// nothing, no-op cleanly.
	//
	// reviewerID may be a zero (invalid) UUID on ingress paths that cannot
	// attribute the author. A zero reviewerID is deliberately NOT treated as
	// self-review — some ingress genuinely can't prove authorship, so we
	// proceed rather than drop a legitimate verdict — but we log it so the gap
	// is visible.
	if reviewerID.Valid {
		if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" &&
			issue.AssigneeID.Valid && issue.AssigneeID.Bytes == reviewerID.Bytes {
			slog.Warn("capture review verdict: self-review REJECTED — the reviewer is the issue's author agent; no review label attached",
				"issue_id", util.UUIDToString(issue.ID), "reviewer_id", util.UUIDToString(reviewerID))
			return "", false
		}
	} else {
		slog.Info("capture review verdict: reviewer id is unattributed (zero UUID) — proceeding without the self-review check",
			"issue_id", util.UUIDToString(issue.ID))
	}

	label, color := ReviewLabelPass, reviewLabelPassColor
	opposite := ReviewLabelFail
	if p.Verdict == "fail" {
		label, color = ReviewLabelFail, reviewLabelFailColor
		opposite = ReviewLabelPass
	}

	if s.issueHasLabelName(ctx, issue, label) {
		// Agent already set it (e.g. via CLI) → the label handler already fired
		// the downstream triggers; don't report a NEW attach. Still enforce
		// replace-on-write below in case the opposite label lingers.
	} else {
		labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, label, color)
		if err != nil {
			slog.Warn("capture review verdict: ensure label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
			return "", false
		}
		if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("capture review verdict: attach label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
			return "", false
		}
		newlyLabeled = true
	}

	// A verdict REPLACES the previous one — detach the opposite gate label so
	// a failed-then-fixed-then-re-passed issue never carries both forever
	// (the qa:pass/qa:fail sticky-label lesson applied from day one here).
	s.DetachIssueLabelByName(ctx, issue, opposite)

	// Broadcast the FULL label set (SetDesignStateLabel pattern): the frontend
	// labels-changed handler replaces the issue's labels with the payload, so
	// an issue_id-only event would wipe them on every client. On a read failure
	// skip the broadcast — clients recover on their next query.
	if labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventIssueLabelsChanged,
			WorkspaceID: util.UUIDToString(issue.WorkspaceID),
			ActorType:   "agent",
			ActorID:     "",
			Payload: map[string]any{
				"issue_id": util.UUIDToString(issue.ID),
				"labels":   labelRowsToPayload(labels),
			},
		})
	} else {
		slog.Warn("capture review verdict: list labels for broadcast failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
	}
	slog.Info("review verdict: auto-attached gate label from review-result block",
		"issue_id", util.UUIDToString(issue.ID), "label", label)

	// Typed inbox notification — only on a NEWLY landed verdict, so a
	// re-posted identical verdict never re-notifies.
	if newlyLabeled {
		s.NotifyReviewVerdict(ctx, issue, p.Verdict, "agent", reviewerID, p.Summary)
	}
	return p.Verdict, newlyLabeled
}

// LatestReviewResultForIssue returns the newest agent comment carrying a
// parsable review-result block: the payload, the comment id, its author agent
// id, and when it was posted. found=false when no agent comment on the issue
// carries a valid block (an unparsable block is skipped — an older valid
// verdict still resolves). Mirrors LatestDesignProposalForIssue's
// newest-first comment scan; there is deliberately no review table to query.
func (s *TaskService) LatestReviewResultForIssue(ctx context.Context, issue db.Issue) (p ReviewResultPayload, commentID, reviewerID pgtype.UUID, reviewedAt pgtype.Timestamptz, found bool, err error) {
	// Newest-first at the DB (ORDER BY created_at DESC): on a long issue the
	// ASC ListCommentsForIssue capped at a LIMIT would read the OLDEST N rows
	// and never see a fresh verdict. ListRecentCommentsForIssue returns the
	// most-recent rows, so the newest valid verdict always resolves.
	comments, err := s.Queries.ListRecentCommentsForIssue(ctx, db.ListRecentCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       500,
	})
	if err != nil {
		return ReviewResultPayload{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.Timestamptz{}, false, err
	}
	// Already newest-first: the first agent comment with a valid block wins.
	for _, c := range comments {
		if c.AuthorType != "agent" {
			continue
		}
		payload, ok := ParseReviewResultBlock(c.Content)
		if !ok {
			continue
		}
		return payload, c.ID, c.AuthorID, c.CreatedAt, true, nil
	}
	return ReviewResultPayload{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.Timestamptz{}, false, nil
}
