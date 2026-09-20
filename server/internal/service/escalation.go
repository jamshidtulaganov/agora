package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Escalation — the agent's out-of-band "I am stuck" hatch
// (docs/orchestration-upgrade-plan.md §B1).
//
// Agora's agents structurally cannot ask a question mid-run: Claude Code is
// driven with --disallowedTools AskUserQuestion because there is no UI for a
// headless prompt to render in, and the standing convention ("put the question
// in an issue comment") is a convention, not a state — a comment does not park
// the run, does not reach an inbox, and does not stop the agent from guessing.
//
// An escalation is that state. The run ENDS (freeing the runtime slot), the
// task parks in waiting_human, a human is notified, and the answer is replayed
// into the same provider session when the task is resumed.
//
// Deliberately NOT a replacement for `agora telegram ask`: that verb keeps its
// job — a BLOCKING yes/no where a human is demonstrably present. Its 10–60
// minute timeouts are the right shape for "deploy to staging?" and the wrong
// shape for "I am stuck", measured against a 24.9h median human wait.

const (
	EscalationKindQuestion   = "question"
	EscalationKindBlocked    = "blocked"
	EscalationKindBudget     = "budget"
	EscalationKindPermission = "permission"
	EscalationKindRisk       = "risk"
)

// escalationPromptMaxRunes caps the one-sentence ask. The prompt is a
// notification title and an inbox row; anything longer belongs in Detail.
const escalationPromptMaxRunes = 400

// escalationDetailMaxRunes caps "what I tried". Generous — this is the
// context that saves the human a round trip — but not unbounded.
const escalationDetailMaxRunes = 4000

// escalationMaxOptions caps the shortcut list. More than a handful stops
// being a decision and starts being a form.
const escalationMaxOptions = 6

// escalationAnswerMaxRunes caps the human's reply. It becomes the resumed
// run's instruction, so it is comment-sized, not essay-sized.
const escalationAnswerMaxRunes = 4000

// ErrEscalationAlreadyResolved is returned when two humans answer the same
// escalation concurrently: the guarded UPDATE matches one of them, and the
// loser gets this rather than a second resume of the same task.
var ErrEscalationAlreadyResolved = errors.New("escalation already resolved")

// NormalizeEscalationKind validates an agent-supplied kind. Unknown values
// downgrade to "question" rather than failing the call: an agent that asks in
// a way we do not recognise must still reach a human. Reports whether the
// input was a known kind so a CLI can warn.
func NormalizeEscalationKind(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", EscalationKindQuestion:
		return EscalationKindQuestion, true
	case EscalationKindBlocked:
		return EscalationKindBlocked, true
	case EscalationKindBudget:
		return EscalationKindBudget, true
	case EscalationKindPermission:
		return EscalationKindPermission, true
	case EscalationKindRisk:
		return EscalationKindRisk, true
	default:
		return EscalationKindQuestion, false
	}
}

func clampRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if runes := []rune(s); len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return s
}

// OpenEscalationInput carries everything an escalation needs. Assembled by
// the handler (which owns issue loading and risk-tier resolution) so this
// layer never re-reads request state.
type OpenEscalationInput struct {
	Issue    db.Issue
	TaskID   pgtype.UUID // the run being parked; invalid when raised outside a run
	AgentID  pgtype.UUID
	Kind     string
	Prompt   string   // "what I need, in one sentence"
	Detail   string   // "what I tried"
	Options  []string // empty ⇒ free-text answer
	RiskTier string   // snapshot for queue ranking
}

// OpenEscalation raises (or refreshes) the issue's open escalation, parks the
// raising task, tells the issue, and notifies the humans.
//
// One open row per issue is a DATABASE guarantee (partial unique index), not
// a convention: a second raise on the same issue updates the existing row.
// That is deliberate — an agent that refines what it needs must not stack a
// second question onto the same human, which is the "agents DDoSing our
// attention" failure the whole design exists to avoid.
func (s *TaskService) OpenEscalation(ctx context.Context, in OpenEscalationInput) (db.TaskEscalation, error) {
	prompt := clampRunes(in.Prompt, escalationPromptMaxRunes)
	if prompt == "" {
		return db.TaskEscalation{}, fmt.Errorf("escalation prompt is required")
	}
	kind, _ := NormalizeEscalationKind(in.Kind)

	options := make([]string, 0, len(in.Options))
	for _, opt := range in.Options {
		if trimmed := clampRunes(opt, escalationPromptMaxRunes); trimmed != "" {
			options = append(options, trimmed)
			if len(options) == escalationMaxOptions {
				break
			}
		}
	}

	esc, err := s.Queries.CreateTaskEscalation(ctx, db.CreateTaskEscalationParams{
		WorkspaceID: in.Issue.WorkspaceID,
		IssueID:     in.Issue.ID,
		TaskID:      in.TaskID,
		AgentID:     in.AgentID,
		Kind:        kind,
		Prompt:      prompt,
		Detail:      clampRunes(in.Detail, escalationDetailMaxRunes),
		Options:     options,
		RiskTier:    strings.TrimSpace(in.RiskTier),
	})
	if err != nil {
		return db.TaskEscalation{}, fmt.Errorf("create escalation: %w", err)
	}

	// Park the run. Budget escalations are raised from the failure handler,
	// where the task is already terminal — nothing to park.
	if in.TaskID.Valid && kind != EscalationKindBudget {
		if _, perr := s.MarkTaskWaitingHuman(ctx, in.TaskID, prompt); perr != nil {
			// Not fatal: the escalation row IS the state of record. A task
			// that could not be parked (already terminal, cancelled mid-flight)
			// still leaves a question a human must answer.
			slog.Warn("escalation: parking the task failed",
				"escalation_id", util.UUIDToString(esc.ID),
				"task_id", util.UUIDToString(in.TaskID),
				"error", perr,
			)
		}
	}

	s.postEscalationComment(ctx, in.Issue, esc)
	s.notifyEscalation(ctx, in.Issue, esc)
	s.broadcastEscalation(ctx, protocol.EventEscalationOpened, in.Issue, esc)

	slog.Info("escalation raised",
		"escalation_id", util.UUIDToString(esc.ID),
		"issue_id", util.UUIDToString(in.Issue.ID),
		"task_id", util.UUIDToString(in.TaskID),
		"agent_id", util.UUIDToString(in.AgentID),
		"kind", kind,
		"risk_tier", esc.RiskTier,
	)
	return esc, nil
}

// ResolveEscalation records a human's answer and replays it into the parked
// run. Returns the answered escalation and the resumed task (nil when there
// was nothing to resume — the parked task was cancelled, or the escalation
// was raised outside a run).
//
// The answer becomes the resumed run's instruction: a free-text redirect and
// a picked option are the same mechanism, which is why "terminate / redirect"
// needs no separate verb.
func (s *TaskService) ResolveEscalation(
	ctx context.Context,
	issue db.Issue,
	escalationID pgtype.UUID,
	answer string,
	userID pgtype.UUID,
) (db.TaskEscalation, *db.AgentTaskQueue, error) {
	answer = clampRunes(answer, escalationAnswerMaxRunes)
	if answer == "" {
		return db.TaskEscalation{}, nil, fmt.Errorf("an answer is required")
	}

	esc, err := s.Queries.AnswerTaskEscalation(ctx, db.AnswerTaskEscalationParams{
		ID:          escalationID,
		WorkspaceID: issue.WorkspaceID,
		Answer:      pgtype.Text{String: answer, Valid: true},
		AnsweredBy:  userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.TaskEscalation{}, nil, ErrEscalationAlreadyResolved
		}
		return db.TaskEscalation{}, nil, fmt.Errorf("answer escalation: %w", err)
	}

	// Order matters: the answer becomes a COMMENT first, and the resumed run
	// is pointed at it. That is the mechanism by which "the answer becomes the
	// prompt" — the daemon already injects a task's trigger comment with a
	// "focus on THIS comment" instruction, so no new prompt plumbing is needed.
	answerCommentID := s.postEscalationAnswerComment(ctx, issue, esc, userID)

	resumed, rerr := s.ResumeEscalatedTask(ctx, issue, esc, answerCommentID)
	if rerr != nil {
		// The decision is recorded either way. Surfacing the answer without a
		// resumed run is strictly better than losing the human's decision
		// because the re-enqueue failed.
		slog.Warn("escalation: resume failed after answer",
			"escalation_id", util.UUIDToString(esc.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"error", rerr,
		)
	}
	if resumed != nil {
		if updated, uerr := s.Queries.SetTaskEscalationResumedTask(ctx, db.SetTaskEscalationResumedTaskParams{
			ID:            esc.ID,
			WorkspaceID:   issue.WorkspaceID,
			ResumedTaskID: resumed.ID,
		}); uerr == nil {
			esc = updated
		}
	}

	s.broadcastEscalation(ctx, protocol.EventEscalationResolved, issue, esc)

	slog.Info("escalation answered",
		"escalation_id", util.UUIDToString(esc.ID),
		"issue_id", util.UUIDToString(issue.ID),
		"answered_by", util.UUIDToString(userID),
		"resumed_task_id", util.UUIDToString(esc.ResumedTaskID),
	)
	return esc, resumed, nil
}

// CancelEscalation withdraws an open escalation without answering it — the
// question became moot (issue cancelled, work redirected elsewhere). The
// parked task is NOT resumed; whoever cancels owns what happens next.
func (s *TaskService) CancelEscalation(
	ctx context.Context,
	issue db.Issue,
	escalationID pgtype.UUID,
	userID pgtype.UUID,
) (db.TaskEscalation, error) {
	esc, err := s.Queries.CancelTaskEscalation(ctx, db.CancelTaskEscalationParams{
		ID:          escalationID,
		WorkspaceID: issue.WorkspaceID,
		AnsweredBy:  userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.TaskEscalation{}, ErrEscalationAlreadyResolved
		}
		return db.TaskEscalation{}, fmt.Errorf("cancel escalation: %w", err)
	}
	s.broadcastEscalation(ctx, protocol.EventEscalationResolved, issue, esc)
	return esc, nil
}

// RaiseBudgetEscalation converts an exhausted budget into a question for a
// human. Called from the failure path, never by an agent.
//
// This is the other half of excluding ReasonBudgetExhausted from
// retryableReasons: a budget that silently retries is not a budget, and a
// budget that silently stops is indistinguishable from a broken daemon. The
// only honest third option is to say so, to a person, in their inbox.
func (s *TaskService) RaiseBudgetEscalation(ctx context.Context, task db.AgentTaskQueue, detail string) {
	if !task.IssueID.Valid {
		return // chat / autopilot runs have no issue to hang the question on
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("budget escalation: issue lookup failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	if issue.Status == "done" || issue.Status == "cancelled" {
		return
	}
	if _, err := s.OpenEscalation(ctx, OpenEscalationInput{
		Issue:   issue,
		TaskID:  task.ID,
		AgentID: task.AgentID,
		Kind:    EscalationKindBudget,
		Prompt:  "This run hit its budget before finishing. Raise the budget, narrow the task, or say how to proceed.",
		Detail:  clampRunes(detail, escalationDetailMaxRunes),
	}); err != nil {
		slog.Warn("budget escalation: open failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"error", err)
	}
}

// --- notification / fan-out -------------------------------------------------

// postEscalationComment puts the question on the issue itself. The inbox is
// the notification; the issue is the record — a person arriving from a link
// six hours later must see what was asked without opening their inbox.
func (s *TaskService) postEscalationComment(ctx context.Context, issue db.Issue, esc db.TaskEscalation) {
	var b strings.Builder
	b.WriteString("🙋 **Waiting on a human** — ")
	b.WriteString(esc.Prompt)
	if esc.Detail != "" {
		b.WriteString("\n\nWhat was tried: ")
		b.WriteString(esc.Detail)
	}
	if len(esc.Options) > 0 {
		b.WriteString("\n\nOptions:\n")
		for _, opt := range esc.Options {
			b.WriteString("- ")
			b.WriteString(opt)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n\nThe run has stopped and is waiting for an answer — answer it on this issue or from your inbox.")

	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     b.String(),
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("escalation: system comment failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	s.publishSystemComment(issue, comment)
}

// postEscalationAnswerComment writes the human's answer onto the issue and
// returns its id so the resumed run can be pointed at it.
//
// Authored as the answering MEMBER, not as "system": the agent is about to be
// told to act on this text, and the agent should see who decided. Written
// through Queries directly (not the comment handler) so the comment-trigger
// machinery does not ALSO enqueue a task — the resume below is the one run
// this answer produces.
func (s *TaskService) postEscalationAnswerComment(ctx context.Context, issue db.Issue, esc db.TaskEscalation, userID pgtype.UUID) pgtype.UUID {
	var b strings.Builder
	b.WriteString("✅ **Answer to: ")
	b.WriteString(esc.Prompt)
	b.WriteString("**\n\n")
	b.WriteString(esc.Answer.String)

	authorType, authorID, commentType := "system", pgtype.UUID{Valid: true}, "system"
	if userID.Valid {
		authorType, authorID, commentType = "member", userID, "comment"
	}
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  authorType,
		AuthorID:    authorID,
		Content:     b.String(),
		Type:        commentType,
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("escalation: answer comment failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return pgtype.UUID{}
	}
	s.publishSystemComment(issue, comment)
	return comment.ID
}

func (s *TaskService) publishSystemComment(issue db.Issue, comment db.Comment) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "system",
		Payload: map[string]any{
			"comment": map[string]any{
				"id":           util.UUIDToString(comment.ID),
				"issue_id":     util.UUIDToString(comment.IssueID),
				"author_type":  comment.AuthorType,
				"author_id":    util.UUIDToString(comment.AuthorID),
				"content":      comment.Content,
				"type":         comment.Type,
				"created_at":   util.TimestampToString(comment.CreatedAt),
				"workspace_id": util.UUIDToString(comment.WorkspaceID),
			},
		},
	})
}

// notifyEscalation writes the typed inbox item. Recipients mirror
// NotifyQAVerdict: the assignee (an agent routes to its OWNER — the human
// responsible for its work), the human creator, and human subscribers.
//
// Severity is always action_required: an escalation is by definition a run
// that has stopped and cannot restart without a person.
func (s *TaskService) notifyEscalation(ctx context.Context, issue db.Issue, esc db.TaskEscalation) {
	recipients := map[string]pgtype.UUID{}
	add := func(id pgtype.UUID) {
		if id.Valid {
			recipients[util.UUIDToString(id)] = id
		}
	}
	if issue.AssigneeType.Valid && issue.AssigneeID.Valid {
		switch issue.AssigneeType.String {
		case "member":
			add(issue.AssigneeID)
		case "agent":
			if agent, err := s.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
			}); err == nil {
				add(agent.OwnerID)
			}
		case "squad":
			if squad, err := s.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
				ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
			}); err == nil {
				if leader, err := s.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
					ID: squad.LeaderID, WorkspaceID: issue.WorkspaceID,
				}); err == nil {
					add(leader.OwnerID)
				}
			}
		}
	}
	if issue.CreatorType == "member" && issue.CreatorID.Valid {
		add(issue.CreatorID)
	}
	if subs, err := s.Queries.ListIssueSubscribers(ctx, issue.ID); err == nil {
		for _, sub := range subs {
			if sub.UserType == "member" && sub.UserID.Valid {
				add(sub.UserID)
			}
		}
	}
	if len(recipients) == 0 {
		slog.Warn("escalation: nobody to notify",
			"escalation_id", util.UUIDToString(esc.ID),
			"issue_id", util.UUIDToString(issue.ID))
		return
	}

	prefix := s.getIssuePrefix(issue.WorkspaceID)
	details, _ := json.Marshal(map[string]any{
		"escalation_id": util.UUIDToString(esc.ID),
		"issue_id":      util.UUIDToString(issue.ID),
		"identifier":    fmt.Sprintf("%s-%d", prefix, issue.Number),
		"kind":          esc.Kind,
		"prompt":        esc.Prompt,
		"risk_tier":     esc.RiskTier,
	})

	actorType := pgtype.Text{}
	if esc.AgentID.Valid {
		actorType = pgtype.Text{String: "agent", Valid: true}
	}
	for _, uid := range recipients {
		item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   issue.WorkspaceID,
			RecipientType: "member",
			RecipientID:   uid,
			Type:          "escalation",
			Severity:      "action_required",
			IssueID:       issue.ID,
			Title:         issue.Title,
			Body:          pgtype.Text{String: esc.Prompt, Valid: true},
			ActorType:     actorType,
			ActorID:       esc.AgentID,
			Details:       details,
		})
		if err != nil {
			slog.Warn("escalation: inbox write failed",
				"escalation_id", util.UUIDToString(esc.ID), "error", err)
			continue
		}
		s.publishQuickCreateInbox(item, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(esc.AgentID), issue.Status)
	}
	slog.Info("escalation inbox notified",
		"escalation_id", util.UUIDToString(esc.ID),
		"recipients", len(recipients))
}

// broadcastEscalation fans the state change out to every connected client in
// the workspace. Workspace-scoped, never global — an escalation names an
// issue, and issues are tenanted.
func (s *TaskService) broadcastEscalation(ctx context.Context, eventType string, issue db.Issue, esc db.TaskEscalation) {
	_ = ctx
	actorID := ""
	if esc.AgentID.Valid {
		actorID = util.UUIDToString(esc.AgentID)
	}
	s.Bus.Publish(events.Event{
		Type:        eventType,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     actorID,
		Payload: map[string]any{
			"issue_id":   util.UUIDToString(issue.ID),
			"escalation": EscalationToMap(esc),
		},
	})
}

// EscalationToMap is the single wire shape for an escalation — used by the WS
// payload and by the REST responses so a client parses one thing.
func EscalationToMap(esc db.TaskEscalation) map[string]any {
	options := esc.Options
	if options == nil {
		options = []string{}
	}
	return map[string]any{
		"id":              util.UUIDToString(esc.ID),
		"workspace_id":    util.UUIDToString(esc.WorkspaceID),
		"issue_id":        util.UUIDToString(esc.IssueID),
		"task_id":         util.UUIDToString(esc.TaskID),
		"agent_id":        util.UUIDToString(esc.AgentID),
		"kind":            esc.Kind,
		"prompt":          esc.Prompt,
		"detail":          esc.Detail,
		"options":         options,
		"risk_tier":       esc.RiskTier,
		"status":          esc.Status,
		"answer":          esc.Answer.String,
		"answered_by":     util.UUIDToString(esc.AnsweredBy),
		"answered_at":     util.TimestampToString(esc.AnsweredAt),
		"resumed_task_id": util.UUIDToString(esc.ResumedTaskID),
		"raised_at":       util.TimestampToString(esc.RaisedAt),
	}
}
