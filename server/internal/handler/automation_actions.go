package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Automation action executors. Each one is small, independently guarded, and
// attributed to the actor type "automation" so its own effects cannot re-enter the
// engine (see emitAutomationEvent's first guard).
//
// Scope discipline: every action here manipulates TASK state (status, assignee,
// labels, comments), dispatches an existing agent slice action, or sends a Telegram
// notice. There is no "call an arbitrary URL" action — that would turn a task
// automation into an unaudited egress path.

// runAutomationActions executes a rule's actions in order and returns one outcome
// per action, how many applied, and the first error.
//
// A failing action does NOT abort the rest: a rule like "label it, comment, notify"
// should still notify when the label already existed. The run row records each
// outcome, so a partial application is visible rather than silent.
func (h *Handler) runAutomationActions(
	ctx context.Context, rule db.Automation, ev AutomationEvent, actions []automationAction,
) ([]automationActionOutcome, int, error) {
	outcomes := make([]automationActionOutcome, 0, len(actions))
	applied := 0
	var firstErr error

	// Actions mutate the issue (status, assignee, labels), so each step re-reads it
	// rather than working from the event's snapshot — otherwise "set status then
	// assign" would write the assignee onto a stale row.
	issue := ev.Issue
	for _, action := range actions {
		if fresh, err := h.Queries.GetIssue(ctx, issue.ID); err == nil {
			issue = fresh
		}
		// A filter node decides whether the flow continues. It is not an action:
		// stopping here is the rule working as written, so the remaining steps are
		// recorded as not-run rather than failed.
		if strings.TrimSpace(action.Type) == automationStepFilter {
			facts := h.automationFactsFor(ctx, AutomationEvent{
				Trigger: ev.Trigger, Issue: issue, FromStatus: ev.FromStatus, ToStatus: ev.ToStatus,
				Label: ev.Label, Stage: ev.Stage, PrevStage: ev.PrevStage,
				CommentBody: ev.CommentBody, CommentAuthor: ev.CommentAuthor, ActorType: ev.ActorType,
			})
			ok, reason := evaluateAutomationConditions(action.Conditions, facts)
			outcomes = append(outcomes, automationActionOutcome{
				Type: automationStepFilter, OK: true,
				Detail: map[bool]string{true: "passed", false: "stopped the flow: " + reason}[ok],
			})
			if !ok {
				return outcomes, applied, firstErr
			}
			continue
		}
		detail, err := h.runAutomationAction(ctx, rule, ev, issue, action)
		if err != nil {
			outcomes = append(outcomes, automationActionOutcome{Type: action.Type, OK: false, Detail: err.Error()})
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("automation action failed",
				"error", err, "automation_id", uuidToString(rule.ID), "action", action.Type,
				"issue_id", uuidToString(issue.ID))
			continue
		}
		applied++
		outcomes = append(outcomes, automationActionOutcome{Type: action.Type, OK: true, Detail: detail})
	}
	return outcomes, applied, firstErr
}

func (h *Handler) runAutomationAction(
	ctx context.Context, rule db.Automation, ev AutomationEvent, issue db.Issue, action automationAction,
) (string, error) {
	switch strings.TrimSpace(action.Type) {
	case automationActionSetStatus:
		return h.automationSetStatus(ctx, issue, action)
	case automationActionAssign:
		return h.automationAssign(ctx, issue, action)
	case automationActionAddLabel:
		return h.automationAddLabel(ctx, issue, action)
	case automationActionRemoveLabel:
		return h.automationRemoveLabel(ctx, issue, action)
	case automationActionPostComment:
		return h.automationPostComment(ctx, rule, issue, action)
	case automationActionDispatchSlice:
		return h.automationDispatchSlice(ctx, rule, issue, action)
	case automationActionSendTelegram:
		return h.automationSendTelegram(ctx, rule, ev, issue, action)
	default:
		return "", fmt.Errorf("unknown action type %q", action.Type)
	}
}

// automationSetStatus moves the issue. Already-in-that-status is a no-op success,
// not a failure: a rule saying "on review:fail put it back in todo" is satisfied
// when the issue is already there.
func (h *Handler) automationSetStatus(ctx context.Context, issue db.Issue, action automationAction) (string, error) {
	status := strings.ToLower(strings.TrimSpace(action.Config["status"]))
	if !isKnownIssueStatus(status) {
		return "", fmt.Errorf("invalid status %q", status)
	}
	if strings.EqualFold(issue.Status, status) {
		return "already " + status, nil
	}
	if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: issue.ID, Status: status, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return "", fmt.Errorf("set status: %w", err)
	}
	// Publish so boards and the desktop app move live. Attributed to the automation
	// actor, which the engine ignores on the way back in.
	h.publish(protocol.EventIssueUpdated, uuidToString(issue.WorkspaceID), automationActorType, uuidToString(issue.ID),
		map[string]any{"issue_id": uuidToString(issue.ID), "status": status, "status_changed": true})
	return issue.Status + " → " + status, nil
}

// automationAssign routes the issue to a resolved owner. "orchestrator" is the
// total resolver every agent-run task has (squad lead, or the solo agent itself),
// so a rule can say "give it back to whoever owns it" without naming anyone.
func (h *Handler) automationAssign(ctx context.Context, issue db.Issue, action automationAction) (string, error) {
	target := strings.ToLower(strings.TrimSpace(action.Config["target"]))

	var assigneeType pgtype.Text
	var assigneeID pgtype.UUID
	label := ""

	switch target {
	case "none":
		assigneeType, assigneeID = pgtype.Text{}, pgtype.UUID{}
		label = "unassigned"
	case "orchestrator":
		agent, ok := h.orchestratorForIssue(ctx, issue)
		if !ok {
			return "", errors.New("no agent orchestrator resolves for this issue")
		}
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		assigneeID = agent.ID
		label = "orchestrator " + agent.Name
	case "qa_leader":
		agent, ok := h.qaSquadLeader(ctx, issue.WorkspaceID)
		if !ok {
			return "", errors.New("this workspace has no QA squad leader")
		}
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		assigneeID = agent.ID
		label = "QA leader " + agent.Name
	case "reviewer":
		agent, ok := h.resolveReviewerAgent(ctx, issue)
		if !ok {
			return "", errors.New("no reviewer distinct from the author resolves")
		}
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		assigneeID = agent.ID
		label = "reviewer " + agent.Name
	case "agent":
		id, err := parseUUIDErr(strings.TrimSpace(action.Config["agent_id"]))
		if err != nil {
			return "", fmt.Errorf("invalid agent_id: %w", err)
		}
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: id, WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return "", errors.New("agent_id is not an agent in this workspace")
		}
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		assigneeID = agent.ID
		label = agent.Name
	default:
		return "", fmt.Errorf("invalid assign target %q", target)
	}

	if sameIssueAssignee(issue, assigneeType, assigneeID) {
		return "already assigned to " + label, nil
	}
	if _, err := h.Queries.UpdateIssueAssignee(ctx, db.UpdateIssueAssigneeParams{
		ID: issue.ID, AssigneeType: assigneeType, AssigneeID: assigneeID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return "", fmt.Errorf("assign: %w", err)
	}
	h.publish(protocol.EventIssueUpdated, uuidToString(issue.WorkspaceID), automationActorType, uuidToString(issue.ID),
		map[string]any{"issue_id": uuidToString(issue.ID)})
	return "assigned to " + label, nil
}

// automationAddLabel attaches a label, creating it if the workspace has never used
// it. Colour is optional; a neutral grey keeps a rule from having to pick one.
func (h *Handler) automationAddLabel(ctx context.Context, issue db.Issue, action automationAction) (string, error) {
	name := strings.TrimSpace(action.Config["name"])
	if name == "" {
		return "", errors.New("no label name")
	}
	if h.issueHasLabelNameHandler(ctx, issue, name) {
		return name + " already attached", nil
	}
	color := strings.TrimSpace(action.Config["color"])
	if color == "" {
		color = "#64748b"
	}
	labelID, err := h.ensureLabel(ctx, issue.WorkspaceID, name, color)
	if err != nil {
		return "", fmt.Errorf("ensure label: %w", err)
	}
	if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return "", fmt.Errorf("attach label: %w", err)
	}
	h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), automationActorType, uuidToString(issue.ID),
		map[string]any{"issue_id": uuidToString(issue.ID)})
	return "attached " + name, nil
}

func (h *Handler) automationRemoveLabel(ctx context.Context, issue db.Issue, action automationAction) (string, error) {
	name := strings.TrimSpace(action.Config["name"])
	if name == "" {
		return "", errors.New("no label name")
	}
	if !h.issueHasLabelNameHandler(ctx, issue, name) {
		return name + " was not attached", nil
	}
	h.TaskService.DetachIssueLabelByName(ctx, issue, name)
	h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), automationActorType, uuidToString(issue.ID),
		map[string]any{"issue_id": uuidToString(issue.ID)})
	return "removed " + name, nil
}

// automationPostComment posts the rule's message on the issue timeline, attributed
// to the issue's creator (there is no automation "user" row to author it). The body
// goes through the normal comment-trigger path, so a body that @mentions an agent
// summons that agent — which is how a rule can hand work to a human's own agent
// without a dedicated action type.
func (h *Handler) automationPostComment(ctx context.Context, rule db.Automation, issue db.Issue, action automationAction) (string, error) {
	body := strings.TrimSpace(action.Config["body"])
	if body == "" {
		return "", errors.New("no body")
	}
	body = automationExpandTemplate(body, issue, h.issueKey(ctx, issue), rule.Name)
	authorType := issue.CreatorType
	if authorType != "member" && authorType != "agent" {
		authorType = "member"
	}
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  authorType,
		AuthorID:    issue.CreatorID,
		Content:     body,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		return "", fmt.Errorf("create comment: %w", err)
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(issue.CreatorID), nil)
	return "commented", nil
}

// automationDispatchSlice fires an existing agent slice action (run_review, run_qa,
// gen_test_cases, open_pr, commit_tests, …). It reuses the platform's own
// instruction assembly, so an automation-dispatched review is byte-for-byte the
// review the hardcoded hook dispatches — there is no second prompt to keep in sync.
func (h *Handler) automationDispatchSlice(ctx context.Context, rule db.Automation, issue db.Issue, action automationAction) (string, error) {
	kind := strings.TrimSpace(action.Config["kind"])
	if !isKnownSliceActionKind(kind) {
		return "", fmt.Errorf("unknown slice action kind %q", kind)
	}
	agent, err := h.automationResolveAgent(ctx, issue, kind, strings.TrimSpace(action.Config["agent"]), strings.TrimSpace(action.Config["agent_id"]))
	if err != nil {
		return "", err
	}
	// Don't stack a second identical task on the same agent for the same issue.
	if pending, perr := h.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
		IssueID: issue.ID, AgentID: agent.ID,
	}); perr == nil && pending {
		return agent.Name + " already has a pending task on this issue", nil
	}

	instruction := h.automationSliceInstruction(ctx, issue, kind)
	authorType, authorID := h.dispatchAuthor(ctx, issue, issue.CreatorType, uuidToString(issue.CreatorID))
	if !authorID.Valid {
		return "", errors.New("no valid dispatch author for this issue")
	}
	content := agentProtocolMarker(kind) +
		fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(agent.Name), uuidToString(agent.ID)) + instruction
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
		return "", fmt.Errorf("create dispatch comment: %w", err)
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(authorID), nil)
	return "dispatched " + kind + " to " + agent.Name, nil
}

// automationResolveAgent picks the agent a dispatched slice action goes to. Default
// (empty selector) follows the platform's own convention: QA-family actions go to
// the QA squad, review goes to an independent reviewer, everything else to the
// issue's orchestrator.
func (h *Handler) automationResolveAgent(ctx context.Context, issue db.Issue, kind, selector, agentID string) (db.Agent, error) {
	switch selector {
	case "agent":
		id, err := parseUUIDErr(agentID)
		if err != nil {
			return db.Agent{}, fmt.Errorf("invalid agent_id: %w", err)
		}
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			return db.Agent{}, errors.New("agent_id is not an agent in this workspace")
		}
		return agent, nil
	case "reviewer":
		agent, ok := h.resolveReviewerAgent(ctx, issue)
		if !ok {
			return db.Agent{}, errors.New("no reviewer distinct from the author resolves")
		}
		return agent, nil
	case "qa":
		agents := filterQAAgentsForScope(h.qaSquadAgents(ctx, issue.WorkspaceID), h.issueQAScopeTrivial(ctx, issue))
		if len(agents) == 0 {
			return db.Agent{}, errors.New("this workspace has no ready QA agent")
		}
		return h.pickLeastBusyQAAgent(ctx, agents), nil
	case "orchestrator":
		agent, ok := h.orchestratorForIssue(ctx, issue)
		if !ok {
			return db.Agent{}, errors.New("no agent orchestrator resolves for this issue")
		}
		return agent, nil
	case "":
		// Convention-based default.
		if kind == sliceActionRunReview {
			return h.automationResolveAgent(ctx, issue, kind, "reviewer", "")
		}
		if isQASliceAction(kind) {
			return h.automationResolveAgent(ctx, issue, kind, "qa", "")
		}
		return h.automationResolveAgent(ctx, issue, kind, "orchestrator", "")
	default:
		return db.Agent{}, fmt.Errorf("unknown agent selector %q", selector)
	}
}

// automationSliceInstruction assembles the same instruction the platform's own
// dispatchers send for this kind, including the per-kind context blocks (the PR or
// branch to review, the QA smoke target, the manifest, the case list). Anything
// unlisted gets the bare recipe, which is what a manual fire would send too.
func (h *Handler) automationSliceInstruction(ctx context.Context, issue db.Issue, kind string) string {
	instruction := buildSliceInstruction(kind, "")
	switch kind {
	case sliceActionRunReview:
		instruction += h.sliceActionReviewPRContext(ctx, issue)
		if !h.issueHasKnownPR(ctx, issue) {
			if branch := h.issueReviewBranch(ctx, issue); branch != "" {
				instruction += h.sliceActionReviewBranchContext(ctx, issue, branch)
			}
		}
		if brief := issueBriefNote(issue.Description.String, issue.AcceptanceCriteria); brief != "" {
			instruction += "\n" + brief
		}
	case sliceActionRunQA, sliceActionRunTests:
		if url := h.resolveQAPreviewURL(ctx, issue); url != "" {
			instruction += " SMOKE TARGET: the app is served at " + url + " — run the cases against THAT url."
		}
		instruction += h.sliceActionQASmokeContext(ctx, issue)
		instruction += h.sliceActionQAManifestContext(ctx, issue)
		instruction += h.sliceActionQADocsContext(ctx, issue)
	case sliceActionOpenPR:
		if branch := h.issueReviewBranch(ctx, issue); branch != "" {
			instruction += h.sliceActionOpenPRContext(ctx, issue, branch)
		}
	}
	return instruction
}

// automationSendTelegram posts the rule's notice to the project's shared room or
// DMs the issue's owner. Both destinations are best-effort by design: a rule whose
// point is "move the task" must not fail because a chat id is unset.
func (h *Handler) automationSendTelegram(
	ctx context.Context, rule db.Automation, ev AutomationEvent, issue db.Issue, action automationAction,
) (string, error) {
	if h.telegramBot == nil {
		return "", errors.New("no Telegram bot is configured on this deployment")
	}
	text := strings.TrimSpace(action.Config["text"])
	if text == "" {
		text = rule.Name
	}
	text = automationExpandTemplate(text, issue, h.issueKey(ctx, issue), rule.Name)

	switch strings.ToLower(strings.TrimSpace(action.Config["destination"])) {
	case "group":
		// The room is RESOLVED, not configured: a workspace binds groups in
		// Settings → Integrations → Telegram, so the chat id lives on the agent's
		// installation. A step may still name one explicitly (chat_id) to post
		// somewhere other than the agent's own room.
		dest, sent := h.sendIssueTelegramGroupNotice(ctx, issue, action.Config["chat_id"], text, "", "")
		if !sent {
			if dest.chatID == "" {
				return "", errors.New("no Telegram group is bound for this issue — connect a bot and add a group, or set chat_id on this step")
			}
			return "", fmt.Errorf("telegram send to %s failed", dest.chatID)
		}
		return "notified " + dest.chatID + " via the " + dest.via + " bot", nil
	case "owner":
		userID := h.automationIssueOwnerUserID(ctx, issue)
		if userID == "" {
			return "", errors.New("this issue has no human owner to notify")
		}
		tgID, err := h.telegramIDByUserID(ctx, userID)
		if err != nil || tgID == "" {
			return "", errors.New("the owner has no linked Telegram account")
		}
		if err := h.telegramBot.SendMessage(ctx, tgID, text); err != nil {
			return "", fmt.Errorf("telegram DM: %w", err)
		}
		return "notified the owner", nil
	default:
		return "", errors.New("destination must be group or owner")
	}
}

// automationIssueOwnerUserID resolves the human behind an issue: the member
// assignee, else the OWNER of the agent assignee (an agent has no Telegram
// identity of its own), else the human creator.
func (h *Handler) automationIssueOwnerUserID(ctx context.Context, issue db.Issue) string {
	if issue.AssigneeType.Valid && issue.AssigneeID.Valid {
		switch issue.AssigneeType.String {
		case "member":
			if member, err := h.Queries.GetMember(ctx, issue.AssigneeID); err == nil {
				return uuidToString(member.UserID)
			}
		case "agent":
			if agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
			}); err == nil {
				return uuidToString(agent.OwnerID)
			}
		}
	}
	if issue.CreatorType == "member" && issue.CreatorID.Valid {
		if member, err := h.Queries.GetMember(ctx, issue.CreatorID); err == nil {
			return uuidToString(member.UserID)
		}
		return uuidToString(issue.CreatorID)
	}
	return ""
}

// automationExpandTemplate substitutes the few placeholders a rule body may use.
// Deliberately tiny: a full template language in a notification body is an
// injection surface and a support burden, and these four cover the real messages.
func automationExpandTemplate(text string, issue db.Issue, issueKey, ruleName string) string {
	replacer := strings.NewReplacer(
		"{{issue}}", issueKey,
		"{{title}}", issue.Title,
		"{{status}}", issue.Status,
		"{{automation}}", ruleName,
	)
	return replacer.Replace(text)
}
