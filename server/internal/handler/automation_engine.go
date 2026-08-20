package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The automation engine: one entry point (emitAutomationEvent) called from the
// places that already KNOW what happened — a status write, a label attach, a
// comment insert, a tracker column move. It deliberately does not subscribe to the
// event bus: several bus payloads carry only an issue id (labels:changed) or no
// before/after (issue:updated), and a rules engine that has to re-derive "what
// changed" from a thin payload gets it wrong in exactly the cases a human wrote the
// rule for.
//
// Every write the engine performs is attributed to the actor type "automation",
// which is the first loop guard: emitAutomationEvent drops any event carrying that
// actor, so a rule's own effects cannot re-enter the engine. The second and third
// guards are per (automation, issue): a cooldown, and a cap on applications per
// hour, both read from the automation_run audit trail.

// AutomationEvent is the fact set an emit point hands the engine. Only the fields
// relevant to the trigger need to be set; everything else is derived from Issue.
type AutomationEvent struct {
	Trigger string
	Issue   db.Issue

	// Status transition (issue.status_changed).
	FromStatus string
	ToStatus   string

	// Label attach (issue.label_attached).
	Label string

	// Tracker column move (tracker.stage_changed).
	Stage     string
	PrevStage string

	// Comment insert (comment.created).
	CommentID     pgtype.UUID
	CommentBody   string
	CommentAuthor string // "member" | "agent"

	// Who caused it. ActorType "automation" short-circuits the whole engine.
	ActorType string
	ActorID   string
}

// emitAutomationEvent evaluates every enabled automation for this workspace and
// trigger, and applies the ones whose conditions hold. Detached and best-effort:
// the caller is a write path (a status change, a label attach) and must never fail
// or slow down because a rule misbehaved.
func (h *Handler) emitAutomationEvent(ctx context.Context, ev AutomationEvent) {
	if strings.TrimSpace(ev.Trigger) == "" || !ev.Issue.ID.Valid {
		return
	}
	// Loop guard #1: never react to our own writes.
	if strings.EqualFold(strings.TrimSpace(ev.ActorType), automationActorType) {
		return
	}
	safeGo("automation:"+ev.Trigger+":"+uuidToString(ev.Issue.ID), func() {
		h.runAutomationsForEvent(context.Background(), ev)
	})
}

// runAutomationsForEvent is the synchronous body (called on the detached
// goroutine, and directly by tests).
func (h *Handler) runAutomationsForEvent(ctx context.Context, ev AutomationEvent) {
	rules, err := h.Queries.ListEnabledAutomationsForTrigger(ctx, db.ListEnabledAutomationsForTriggerParams{
		WorkspaceID: ev.Issue.WorkspaceID,
		TriggerType: ev.Trigger,
	})
	if err != nil {
		slog.Warn("automation: list rules failed", "error", err, "trigger", ev.Trigger)
		return
	}
	if len(rules) == 0 {
		return
	}
	facts := h.automationFactsFor(ctx, ev)
	for _, rule := range rules {
		h.applyAutomation(ctx, rule, ev, facts)
	}
}

// automationFactsFor flattens the event + issue into the fact set conditions are
// evaluated against. Labels are read ONCE per event, not per rule.
func (h *Handler) automationFactsFor(ctx context.Context, ev AutomationEvent) automationFacts {
	facts := newAutomationFacts()
	issue := ev.Issue

	facts.set("status", issue.Status)
	facts.set("from_status", ev.FromStatus)
	facts.set("to_status", ev.ToStatus)
	facts.set("label", ev.Label)
	facts.set("stage", ev.Stage)
	facts.set("prev_stage", ev.PrevStage)
	facts.set("title", issue.Title)
	facts.set("actor_type", ev.ActorType)
	facts.set("comment_author_type", ev.CommentAuthor)
	facts.set("comment_body", ev.CommentBody)
	if issue.ProjectID.Valid {
		facts.set("project_id", uuidToString(issue.ProjectID))
	}
	if issue.AssigneeType.Valid {
		facts.set("assignee_type", issue.AssigneeType.String)
	}
	if issue.AssigneeID.Valid {
		facts.set("assignee_id", uuidToString(issue.AssigneeID))
	}
	facts.set("priority", issue.Priority)

	if labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		for _, l := range labels {
			facts.Labels[strings.ToLower(strings.TrimSpace(l.Name))] = true
		}
	}
	// The attached label rides in the set too: a label_attached rule reads it via
	// either `label eq x` or `has_label x`, and both must agree even if the read
	// above raced the attach.
	if l := strings.ToLower(strings.TrimSpace(ev.Label)); l != "" {
		facts.Labels[l] = true
	}
	return facts
}

// applyAutomation runs one rule against one event. Order of checks is cheapest
// first, and every non-application is RECORDED (status "skipped" with a reason) so
// a rule that appears to do nothing can be explained from the UI instead of
// guessed at. Only the project-scope mismatch is silent — a project-scoped rule
// seeing other projects' issues is normal traffic, not a decision worth a row.
func (h *Handler) applyAutomation(ctx context.Context, rule db.Automation, ev AutomationEvent, facts automationFacts) {
	if rule.ProjectID.Valid {
		if !ev.Issue.ProjectID.Valid || ev.Issue.ProjectID.Bytes != rule.ProjectID.Bytes {
			return
		}
	}

	conditions, err := decodeAutomationConditions(rule.Conditions)
	if err != nil {
		h.recordAutomationRun(ctx, rule, ev, "failed", 0, nil, "conditions are not valid JSON: "+err.Error())
		return
	}
	actions, err := decodeAutomationActions(rule.Actions)
	if err != nil {
		h.recordAutomationRun(ctx, rule, ev, "failed", 0, nil, "actions are not valid JSON: "+err.Error())
		return
	}
	if len(actions) == 0 {
		h.recordAutomationRun(ctx, rule, ev, "skipped", 0, nil, "the rule has no actions")
		return
	}
	if ok, reason := evaluateAutomationConditions(conditions, facts); !ok {
		h.recordAutomationRun(ctx, rule, ev, "skipped", 0, nil, reason)
		return
	}

	// Serialize per issue BEFORE the loop guard, and write the audit row before
	// releasing. The guard reads the trail, so it only holds if check → act →
	// record is one critical section: checked outside the lock, twenty concurrent
	// events for one issue all read "no prior run", pass together, and each
	// applies in turn — the stress test caught exactly that (600 applications
	// where the cooldown promised 30).
	unlock := lockIssueQA(uuidToString(ev.Issue.ID))
	if blocked, reason := h.automationLoopGuard(ctx, rule, ev); blocked {
		h.recordAutomationRun(ctx, rule, ev, "skipped", 0, nil, reason)
		unlock()
		return
	}
	outcomes, applied, firstErr := h.runAutomationActions(ctx, rule, ev, actions)

	status := "applied"
	errText := ""
	if firstErr != nil {
		errText = firstErr.Error()
		// A pipeline with one failed step is failed even when an earlier step
		// succeeded. actions_applied still preserves the partial progress, while
		// the top-level status keeps the failure visible and retryable in the UI.
		status = "failed"
	}
	h.recordAutomationRun(ctx, rule, ev, status, applied, outcomes, errText)
	unlock()
	if err := h.Queries.RecordAutomationFired(ctx, rule.ID); err != nil {
		slog.Warn("automation: counter bump failed", "error", err, "automation_id", uuidToString(rule.ID))
	}
	slog.Info("automation applied",
		"automation_id", uuidToString(rule.ID), "name", rule.Name,
		"trigger", ev.Trigger, "issue_id", uuidToString(ev.Issue.ID),
		"actions_applied", applied, "status", status)
}

// automationLoopGuard enforces the cooldown and the hourly cap for this
// (automation, issue) pair. Both read the audit trail, so they survive a restart —
// an in-memory counter would reset exactly when a runaway rule is hottest.
//
// Guards are per ISSUE, not per rule: a rule legitimately fires across many issues
// at once (a sprint-wide status sweep), while repeated firing on ONE issue inside
// seconds is the signature of a rule feeding itself.
func (h *Handler) automationLoopGuard(ctx context.Context, rule db.Automation, ev AutomationEvent) (bool, string) {
	minInterval := automationConfigInt(rule.TriggerConfig, "min_interval_seconds", automationDefaultMinIntervalSeconds)
	maxPerHour := automationConfigInt(rule.TriggerConfig, "max_per_hour", automationDefaultMaxPerHour)

	if minInterval > 0 {
		last, err := h.Queries.LatestAppliedAutomationRunForIssue(ctx, db.LatestAppliedAutomationRunForIssueParams{
			AutomationID: rule.ID, IssueID: ev.Issue.ID,
		})
		if err == nil && last.CreatedAt.Valid {
			elapsed := int(time.Since(last.CreatedAt.Time).Seconds())
			if elapsed < minInterval {
				return true, "cooldown: this rule applied to this issue " + strconv.Itoa(elapsed) +
					"s ago (minimum " + strconv.Itoa(minInterval) + "s)"
			}
		}
	}
	if maxPerHour > 0 {
		count, err := h.Queries.CountRecentAutomationRunsForIssue(ctx, db.CountRecentAutomationRunsForIssueParams{
			AutomationID:  rule.ID,
			IssueID:       ev.Issue.ID,
			WindowSeconds: int32(automationGuardWindowSeconds),
		})
		if err == nil && count >= int64(maxPerHour) {
			return true, "rate cap: this rule already applied " + strconv.FormatInt(count, 10) +
				" times to this issue in the last hour (maximum " + strconv.Itoa(maxPerHour) + ")"
		}
	}
	return false, ""
}

// automationConfigInt reads a positive integer out of trigger_config, tolerating
// both a JSON number and a numeric string (the editor sends strings).
func automationConfigInt(raw []byte, key string, fallback int) int {
	if len(raw) == 0 {
		return fallback
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fallback
	}
	switch v := cfg[key].(type) {
	case float64:
		if v >= 0 {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}

// automationActionOutcome is the per-action audit entry.
type automationActionOutcome struct {
	Type   string `json:"type"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// recordAutomationRun writes the audit row. Best-effort: losing the row must not
// undo the actions that already ran, but it IS logged, because the row is also the
// loop guard's memory — silently losing it would loosen the guard.
func (h *Handler) recordAutomationRun(
	ctx context.Context, rule db.Automation, ev AutomationEvent,
	status string, applied int, outcomes []automationActionOutcome, errText string,
) (db.AutomationRun, error) {
	return h.recordAutomationRunWithMetadata(ctx, rule, ev, status, applied, outcomes, errText, nil)
}

func (h *Handler) recordAutomationRunWithMetadata(
	ctx context.Context, rule db.Automation, ev AutomationEvent,
	status string, applied int, outcomes []automationActionOutcome, errText string,
	metadata map[string]any,
) (db.AutomationRun, error) {
	detail := map[string]any{
		"actions":    outcomes,
		"actor_type": ev.ActorType,
		"actor_id":   ev.ActorID,
	}
	for key, value := range metadata {
		detail[key] = value
	}
	if status == "skipped" && errText != "" {
		detail["reason"] = errText
		errText = ""
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		payload = []byte(`{}`)
	}
	row, createErr := h.Queries.CreateAutomationRun(ctx, db.CreateAutomationRunParams{
		AutomationID:   rule.ID,
		WorkspaceID:    rule.WorkspaceID,
		IssueID:        ev.Issue.ID,
		TriggerType:    ev.Trigger,
		Status:         status,
		ActionsApplied: int32(applied),
		Detail:         payload,
		Error:          errText,
	})
	if createErr != nil {
		slog.Warn("automation: run row write failed",
			"error", createErr, "automation_id", uuidToString(rule.ID), "status", status)
	}
	// Live update: the run history and the list's counters refresh over WS the
	// moment an evaluation lands. Attributed to the automation actor — the engine
	// drops its own events on the way back in, so this cannot loop.
	h.publish(protocol.EventAutomationRun, uuidToString(rule.WorkspaceID), automationActorType, uuidToString(rule.ID),
		map[string]any{
			"automation_id": uuidToString(rule.ID),
			"issue_id":      uuidToString(ev.Issue.ID),
			"status":        status,
			"trigger_type":  ev.Trigger,
		})
	return row, createErr
}
