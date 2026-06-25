package runtrace

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// commentScanLimit caps comments read per issue when gathering signals. Issue
// p99 is ~30 comments; this is a defensive ceiling.
const commentScanLimit = 500

// BackfillOnce labels every pending run trace whose run finished before
// settleCutoff, deriving each outcome from live issue/comment/reaction state
// and persisting it. Returns the number of traces labeled. Per-trace failures
// are skipped (logged-by-omission) so one bad issue can't stall the sweep; only
// the initial list query surfaces a hard error.
func BackfillOnce(ctx context.Context, q *db.Queries, settleCutoff time.Time, limit int32) (int64, error) {
	traces, err := q.ListSettledPendingRunTraces(ctx, db.ListSettledPendingRunTracesParams{
		CreatedAt: pgtype.Timestamptz{Time: settleCutoff, Valid: true},
		Limit:     limit,
	})
	if err != nil {
		return 0, err
	}

	var labeled int64
	for _, tr := range traces {
		// Chat runs have no issue → no issue-shaped outcome signal. Leave them
		// pending; a chat-specific path can label them later.
		if !tr.IssueID.Valid {
			continue
		}
		sig, ok := gatherSignals(ctx, q, tr)
		if !ok {
			continue
		}
		out := DeriveOutcome(sig)
		if err := q.UpdateAgentRunTraceOutcome(ctx, db.UpdateAgentRunTraceOutcomeParams{
			TaskID:           tr.TaskID,
			FinalIssueStatus: pgtype.Text{String: sig.CurrentStatus, Valid: sig.CurrentStatus != ""},
			HumanRevised:     out.HumanRevised,
			Reopened:         out.Reopened,
			ReactionScore:    int32(out.ReactionScore),
			OutcomeLabel:     out.Label,
		}); err != nil {
			continue
		}
		labeled++
	}
	return labeled, nil
}

// gatherSignals reads the live outcome signals for one trace. ok=false means the
// required issue lookup failed and the trace should be retried next pass.
func gatherSignals(ctx context.Context, q *db.Queries, tr db.AgentRunTrace) (Signals, bool) {
	issue, err := q.GetIssue(ctx, tr.IssueID)
	if err != nil {
		return Signals{}, false
	}
	sig := Signals{CurrentStatus: issue.Status}
	if tr.IssueStatusAtRun.Valid {
		sig.StatusAtRun = tr.IssueStatusAtRun.String
	}

	// Human follow-up: any member comment created after the run closed
	// (created_at of the trace ≈ run completion time).
	since, _ := q.ListCommentsSinceForIssue(ctx, db.ListCommentsSinceForIssueParams{
		IssueID:     tr.IssueID,
		WorkspaceID: tr.WorkspaceID,
		CreatedAt:   tr.CreatedAt,
		Limit:       commentScanLimit,
	})
	for _, c := range since {
		if c.AuthorType == "member" {
			sig.HumanFollowUp = true
			break
		}
	}

	// Reaction score: net member reactions on this agent's own comments.
	all, _ := q.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     tr.IssueID,
		WorkspaceID: tr.WorkspaceID,
		Limit:       commentScanLimit,
	})
	var agentCommentIDs []pgtype.UUID
	for _, c := range all {
		if c.AuthorType == "agent" && c.AuthorID == tr.AgentID {
			agentCommentIDs = append(agentCommentIDs, c.ID)
		}
	}
	if len(agentCommentIDs) > 0 {
		reactions, _ := q.ListReactionsByCommentIDs(ctx, agentCommentIDs)
		for _, rx := range reactions {
			if rx.ActorType == "member" {
				sig.ReactionScore += ReactionDelta(rx.Emoji)
			}
		}
	}
	return sig, true
}
