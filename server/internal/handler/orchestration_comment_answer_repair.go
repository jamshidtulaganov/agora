package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const orchestrationCommentAnswerRepairBatchSize = 100
const orchestrationCommentAnswerOutboxEventKind = "comment_answer_pending"

// createCommentWithOrchestrationAnswerOutbox keeps the user's trigger choice
// durable. suppress_agent_ids can deliberately remove an orchestration-answer
// trigger, so a later repair must not infer intent from mention text alone.
// For a selected answer trigger, the comment and its outbox event commit in
// the same transaction; either both are visible or neither is.
func (h *Handler) createCommentWithOrchestrationAnswerOutbox(
	ctx context.Context,
	params db.CreateCommentParams,
	answerTrigger *commentAgentTrigger,
) (db.Comment, error) {
	if answerTrigger == nil {
		return h.Queries.CreateComment(ctx, params)
	}
	if h.TxStarter == nil {
		return db.Comment{}, fmt.Errorf("orchestration answer outbox transactions are not configured")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.Comment{}, fmt.Errorf("begin orchestration answer outbox: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	comment, err := qtx.CreateComment(ctx, params)
	if err != nil {
		return db.Comment{}, err
	}

	step, err := qtx.GetOrchestrationStep(ctx, answerTrigger.StepID)
	if err != nil || step.Status != "waiting_input" || step.AgentID != answerTrigger.Agent.ID {
		// The question raced to another state after trigger preview. Preserve the
		// user's comment, but do not invent a stale delivery intent.
		if err = tx.Commit(ctx); err != nil {
			return db.Comment{}, fmt.Errorf("commit comment after orchestration answer race: %w", err)
		}
		return comment, nil
	}
	run, err := qtx.GetOrchestrationRun(ctx, step.RunID)
	if err != nil || run.IssueID != params.IssueID || run.WorkspaceID != params.WorkspaceID || orchestrationRunStatusIsTerminal(run.Status) {
		if err = tx.Commit(ctx); err != nil {
			return db.Comment{}, fmt.Errorf("commit comment after orchestration run race: %w", err)
		}
		return comment, nil
	}
	question, err := qtx.GetOrchestrationQuestionForUpdate(ctx, db.GetOrchestrationQuestionForUpdateParams{
		ID: answerTrigger.QuestionID, StepID: step.ID,
	})
	if err != nil || question.RunID != run.ID || question.ResolvedAt.Valid {
		// The step can race with an answer between trigger computation and this
		// transaction. The comment remains valid, but there is no longer an exact
		// question delivery target to persist.
		if err = tx.Commit(ctx); err != nil {
			return db.Comment{}, fmt.Errorf("commit comment after orchestration question race: %w", err)
		}
		return comment, nil
	}
	details, _ := json.Marshal(map[string]any{
		"comment_id":  uuidToString(comment.ID),
		"agent_id":    uuidToString(answerTrigger.Agent.ID),
		"question_id": uuidToString(question.ID),
	})
	if _, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		RunID: run.ID, StepID: step.ID, Kind: orchestrationCommentAnswerOutboxEventKind,
		ActorType: "member", ActorID: comment.AuthorID, Details: details,
	}); err != nil {
		return db.Comment{}, fmt.Errorf("record orchestration answer outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return db.Comment{}, fmt.Errorf("commit orchestration answer outbox: %w", err)
	}
	return comment, nil
}

// reconcileOrchestrationAnswerComments closes the only non-transactional
// boundary in the tagged-answer path: CreateComment commits before the
// orchestration answer transaction begins. If the process exits (or Postgres
// briefly fails) between those commits, the atomic comment + pending event
// remains the outbox record and this scan replays it through the normal
// idempotent answer path.
//
// Selection intentionally requires all of the trigger facts that can be
// established durably:
//   - the run and question are still active/open;
//   - the member comment was created after that open question;
//   - exactly one waiting step's agent is explicitly mentioned; and
//   - comment-answer:<comment-id> has not already been recorded.
//
// The Go trigger computation is repeated before mutation so private-agent,
// archived-agent, and exact mention parsing rules stay identical to the live
// CreateComment path.
func (h *Handler) reconcileOrchestrationAnswerComments(ctx context.Context) (int, error) {
	if h.DB == nil {
		return 0, fmt.Errorf("orchestration comment answer repair database is not configured")
	}

	rows, err := h.DB.Query(ctx, `
SELECT DISTINCT answer_comment.id, answer_comment.created_at
FROM orchestration_run AS active_run
JOIN orchestration_event AS answer_outbox
  ON answer_outbox.run_id = active_run.id
 AND answer_outbox.kind = $2
JOIN comment AS answer_comment
  ON answer_comment.issue_id = active_run.issue_id
 AND answer_comment.workspace_id = active_run.workspace_id
 AND answer_outbox.details->>'comment_id' = answer_comment.id::text
WHERE active_run.status IN ('running', 'waiting_approval', 'waiting_input', 'blocked')
  AND answer_comment.author_type = 'member'
  AND answer_comment.content !~* '^[[:space:]]*/note([[:space:]]|$)'
  AND EXISTS (
      SELECT 1
      FROM orchestration_step AS waiting_step
      JOIN agent AS waiting_agent
        ON waiting_agent.id = waiting_step.agent_id
       AND waiting_agent.workspace_id = active_run.workspace_id
      JOIN orchestration_message AS open_question
        ON open_question.step_id = waiting_step.id
       AND open_question.id::text = answer_outbox.details->>'question_id'
       AND open_question.kind = 'question'
       AND open_question.expects_reply
       AND open_question.resolved_at IS NULL
      WHERE waiting_step.run_id = active_run.id
        AND waiting_step.id = answer_outbox.step_id
        AND waiting_step.status = 'waiting_input'
        AND waiting_step.agent_id IS NOT NULL
        AND waiting_agent.runtime_id IS NOT NULL
        AND waiting_agent.archived_at IS NULL
        AND (
            waiting_agent.visibility <> 'private'
            OR waiting_agent.owner_id = answer_comment.author_id
            OR EXISTS (
                SELECT 1
                FROM member AS answer_author_membership
                WHERE answer_author_membership.workspace_id = active_run.workspace_id
                  AND answer_author_membership.user_id = answer_comment.author_id
                  AND answer_author_membership.role IN ('owner', 'admin')
            )
        )
        AND answer_comment.created_at >= open_question.created_at
        AND strpos(
              lower(answer_comment.content),
              '](mention://agent/' || lower(waiting_step.agent_id::text) || ')'
            ) > 0
  )
  AND 1 = (
      SELECT count(*)
      FROM orchestration_step AS mentioned_waiting_step
      WHERE mentioned_waiting_step.run_id = active_run.id
        AND mentioned_waiting_step.status = 'waiting_input'
        AND mentioned_waiting_step.agent_id IS NOT NULL
        AND strpos(
              lower(answer_comment.content),
              '](mention://agent/' || lower(mentioned_waiting_step.agent_id::text) || ')'
            ) > 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM orchestration_message AS saved_answer
      WHERE saved_answer.run_id = active_run.id
        AND saved_answer.idempotency_key = 'comment-answer:' || answer_comment.id::text
  )
ORDER BY answer_comment.created_at, answer_comment.id
LIMIT $1
`, orchestrationCommentAnswerRepairBatchSize, orchestrationCommentAnswerOutboxEventKind)
	if err != nil {
		return 0, fmt.Errorf("list durable tagged orchestration answers: %w", err)
	}
	commentIDs := make([]pgtype.UUID, 0, orchestrationCommentAnswerRepairBatchSize)
	for rows.Next() {
		var commentID pgtype.UUID
		var createdAt pgtype.Timestamptz
		if scanErr := rows.Scan(&commentID, &createdAt); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("scan durable tagged orchestration answer: %w", scanErr)
		}
		commentIDs = append(commentIDs, commentID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate durable tagged orchestration answers: %w", err)
	}
	rows.Close()

	repaired := 0
	var repairErrors []error
	for _, commentID := range commentIDs {
		comment, loadErr := h.Queries.GetComment(ctx, commentID)
		if loadErr != nil {
			repairErrors = append(repairErrors, fmt.Errorf("load tagged answer comment %s: %w", uuidToString(commentID), loadErr))
			continue
		}
		issue, loadErr := h.Queries.GetIssue(ctx, comment.IssueID)
		if loadErr != nil {
			repairErrors = append(repairErrors, fmt.Errorf("load tagged answer issue for comment %s: %w", uuidToString(commentID), loadErr))
			continue
		}
		triggers := h.computeOrchestrationAnswerCommentTrigger(
			ctx, issue, comment.Content, comment.AuthorType, uuidToString(comment.AuthorID),
		)
		if len(triggers) != 1 {
			// State or access changed after the candidate snapshot. A future
			// explicit answer can still resolve the question; never guess here.
			continue
		}

		trigger := triggers[0]
		respondErr := h.respondToOrchestrationQuestionFromComment(ctx, issue, trigger, comment.ID)
		if respondErr == nil {
			repaired++
			continue
		}

		// respondTo... commits before it dispatches. Count a durably-saved
		// answer as repaired even when downstream dispatch failed; the runnable
		// orchestration sweeper owns that second, separately durable boundary.
		if orchestrationCommentAnswerWasPersisted(ctx, h.Queries, trigger.StepID, comment) {
			repaired++
		}
		repairErrors = append(repairErrors, fmt.Errorf("replay tagged answer comment %s: %w", uuidToString(comment.ID), respondErr))
	}

	return repaired, errors.Join(repairErrors...)
}

func orchestrationCommentAnswerWasPersisted(ctx context.Context, queries *db.Queries, stepID pgtype.UUID, comment db.Comment) bool {
	run, err := queries.GetOrchestrationRunByStep(ctx, stepID)
	if err != nil {
		return false
	}
	answer, err := queries.GetOrchestrationMessageByIdempotencyKey(ctx, db.GetOrchestrationMessageByIdempotencyKeyParams{
		RunID: run.ID, IdempotencyKey: "comment-answer:" + uuidToString(comment.ID),
	})
	return err == nil && answer.StepID == stepID && answer.ActorType == "member" && answer.ActorID == comment.AuthorID
}
