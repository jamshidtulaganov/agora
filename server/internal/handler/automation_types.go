package handler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Automations: user-defined task-management rules — WHEN <trigger> IF <conditions>
// THEN <actions>. This file is the CONTRACT (registries, JSON shapes, validation,
// condition evaluation) and is deliberately PURE — no DB, no handler state — so the
// semantics a team sees in the editor are unit-testable without a database.
//
// Scope is task management on purpose: statuses, assignees, labels, comments,
// agent slice actions and Telegram notices. It is not a general integration bus,
// so there is no "call any URL" action to audit, rate-limit or leak through.

// Trigger types. Text, not an enum, because the engine matches against this
// registry: a row carrying an unknown trigger is inert (never matches) instead of
// breaking a deploy or blocking a migration.
const (
	automationTriggerIssueCreated   = "issue.created"
	automationTriggerStatusChanged  = "issue.status_changed"
	automationTriggerAssigned       = "issue.assigned"
	automationTriggerLabelAttached  = "issue.label_attached"
	automationTriggerCommentCreated = "comment.created"
	automationTriggerStageChanged   = "tracker.stage_changed"
)

// Step types. The stored `actions` array is the FLOW: an ordered list of nodes the
// canvas renders under the trigger node. Most are actions; `filter` is a decision
// node that stops the flow when its conditions do not hold, which is how a
// Zapier-style "trigger → filter → act → filter → act" flow is expressed without a
// branching graph. A future if/else can add a second path without changing this.
const (
	automationStepFilter          = "filter"
	automationActionDispatchSlice = "dispatch_slice_action"
	automationActionSetStatus     = "set_status"
	automationActionAssign        = "assign"
	automationActionAddLabel      = "add_label"
	automationActionRemoveLabel   = "remove_label"
	automationActionPostComment   = "post_comment"
	automationActionSendTelegram  = "send_telegram"
)

// Condition operators.
const (
	automationOpEq          = "eq"
	automationOpNeq         = "neq"
	automationOpIn          = "in"
	automationOpNotIn       = "not_in"
	automationOpContains    = "contains"
	automationOpExists      = "exists"
	automationOpHasLabel    = "has_label"
	automationOpNotHasLabel = "not_has_label"
)

// automationActorType is the actor every engine-driven write is attributed to. It
// is the primary loop guard: the engine ignores any event carrying this actor, so
// an automation's own writes cannot re-trigger it (or another automation) directly.
const automationActorType = "automation"

// Loop-guard defaults. An automation may lower/raise them via trigger_config
// (min_interval_seconds, max_per_hour) but never disable them.
const (
	automationDefaultMinIntervalSeconds = 30
	automationDefaultMaxPerHour         = 20
	automationGuardWindowSeconds        = 3600
)

// automationTriggers is the trigger registry, in the order the editor shows them.
var automationTriggers = []string{
	automationTriggerStageChanged,
	automationTriggerStatusChanged,
	automationTriggerLabelAttached,
	automationTriggerAssigned,
	automationTriggerIssueCreated,
	automationTriggerCommentCreated,
}

// automationActions is the step registry, in editor order (what the canvas offers
// when a human clicks "add step").
var automationActions = []string{
	automationStepFilter,
	automationActionDispatchSlice,
	automationActionSetStatus,
	automationActionAssign,
	automationActionAddLabel,
	automationActionRemoveLabel,
	automationActionPostComment,
	automationActionSendTelegram,
}

// automationCondition is one clause. All clauses on a rule must hold (AND); an
// empty list always matches. OR is expressed by writing two automations, which
// keeps both the editor and the audit trail readable.
type automationCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// automationAction is one flow step. Config is step-specific and validated per
// type; Conditions is used only by the `filter` step, where it decides whether the
// flow continues past this node.
type automationAction struct {
	Type       string                `json:"type"`
	Config     map[string]string     `json:"config,omitempty"`
	Conditions []automationCondition `json:"conditions,omitempty"`
}

// automationFacts is the flat view of an event a rule is evaluated against:
// scalar fields plus the issue's label set (lower-cased).
type automationFacts struct {
	Fields map[string]string
	Labels map[string]bool
}

func newAutomationFacts() automationFacts {
	return automationFacts{Fields: map[string]string{}, Labels: map[string]bool{}}
}

// field returns a fact, trimmed. Missing facts read as "" so a condition on a
// field this trigger does not carry simply fails to match rather than panicking.
func (f automationFacts) field(name string) string {
	return strings.TrimSpace(f.Fields[strings.ToLower(strings.TrimSpace(name))])
}

func (f automationFacts) set(name, value string) {
	f.Fields[strings.ToLower(strings.TrimSpace(name))] = value
}

// automationValueStrings normalizes a condition value into a comparison set.
// Accepts a single string/number/bool or an array of them, so the editor can send
// either without the engine caring.
func automationValueStrings(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return []string{strings.TrimSpace(v)}
	case bool:
		return []string{fmt.Sprintf("%t", v)}
	case float64:
		return []string{strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", v), "0"), ".")}
	case json.Number:
		return []string{v.String()}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, automationValueStrings(item)...)
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// evaluateAutomationConditions reports whether every clause holds, plus a
// human-readable reason for the FIRST clause that failed — the reason is written
// to the run row so a rule that "does nothing" can be explained without guessing.
//
// Comparisons are case-insensitive throughout: statuses, labels and kanban column
// names are typed by humans in two languages on this deployment, and a rule that
// silently fails on "Code Review" vs "code review" is worse than useless.
func evaluateAutomationConditions(conditions []automationCondition, facts automationFacts) (bool, string) {
	for _, cond := range conditions {
		ok, reason := evaluateAutomationCondition(cond, facts)
		if !ok {
			return false, reason
		}
	}
	return true, ""
}

func evaluateAutomationCondition(cond automationCondition, facts automationFacts) (bool, string) {
	field := strings.ToLower(strings.TrimSpace(cond.Field))
	op := strings.ToLower(strings.TrimSpace(cond.Op))
	want := automationValueStrings(cond.Value)
	got := facts.field(field)

	switch op {
	case automationOpHasLabel:
		for _, label := range want {
			if !facts.Labels[strings.ToLower(label)] {
				return false, fmt.Sprintf("label %q is not on the issue", label)
			}
		}
		return true, ""
	case automationOpNotHasLabel:
		for _, label := range want {
			if facts.Labels[strings.ToLower(label)] {
				return false, fmt.Sprintf("label %q is on the issue", label)
			}
		}
		return true, ""
	case automationOpExists:
		if got == "" {
			return false, fmt.Sprintf("%s is empty", field)
		}
		return true, ""
	case automationOpEq, automationOpIn:
		for _, candidate := range want {
			if strings.EqualFold(got, candidate) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%s is %q, expected one of %v", field, got, want)
	case automationOpNeq, automationOpNotIn:
		for _, candidate := range want {
			if strings.EqualFold(got, candidate) {
				return false, fmt.Sprintf("%s is %q, which is excluded", field, got)
			}
		}
		return true, ""
	case automationOpContains:
		lowered := strings.ToLower(got)
		for _, candidate := range want {
			if candidate != "" && strings.Contains(lowered, strings.ToLower(candidate)) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%s does not contain any of %v", field, want)
	default:
		// An unknown operator must NOT silently match — that would run actions the
		// author never asked for.
		return false, fmt.Sprintf("unknown condition operator %q", op)
	}
}

// validateAutomationRule rejects a rule the engine could not honour, so the editor
// gets a 400 instead of saving something inert. Kept strict on the registries
// (trigger/action/op names) and lenient on values, which are free-form by design.
func validateAutomationRule(trigger string, conditions []automationCondition, actions []automationAction) error {
	if !automationTriggerKnown(trigger) {
		return fmt.Errorf("unknown trigger %q", trigger)
	}
	if len(actions) == 0 {
		return fmt.Errorf("an automation needs at least one action")
	}
	for _, cond := range conditions {
		if strings.TrimSpace(cond.Field) == "" {
			return fmt.Errorf("a condition needs a field")
		}
		switch strings.ToLower(strings.TrimSpace(cond.Op)) {
		case automationOpEq, automationOpNeq, automationOpIn, automationOpNotIn,
			automationOpContains, automationOpExists, automationOpHasLabel, automationOpNotHasLabel:
		default:
			return fmt.Errorf("unknown condition operator %q", cond.Op)
		}
		if strings.ToLower(strings.TrimSpace(cond.Op)) != automationOpExists && len(automationValueStrings(cond.Value)) == 0 {
			return fmt.Errorf("condition on %q needs a value", cond.Field)
		}
	}
	for _, action := range actions {
		if err := validateAutomationAction(action); err != nil {
			return err
		}
	}
	return nil
}

func validateAutomationAction(action automationAction) error {
	switch strings.TrimSpace(action.Type) {
	case automationStepFilter:
		if len(action.Conditions) == 0 {
			return fmt.Errorf("a filter step needs at least one condition")
		}
		for _, cond := range action.Conditions {
			if strings.TrimSpace(cond.Field) == "" {
				return fmt.Errorf("a filter condition needs a field")
			}
		}
	case automationActionDispatchSlice:
		kind := strings.TrimSpace(action.Config["kind"])
		if !isKnownSliceActionKind(kind) {
			return fmt.Errorf("dispatch_slice_action needs a known kind, got %q", kind)
		}
	case automationActionSetStatus:
		status := strings.TrimSpace(action.Config["status"])
		if !isKnownIssueStatus(status) {
			return fmt.Errorf("set_status needs a valid status, got %q", status)
		}
	case automationActionAssign:
		switch strings.TrimSpace(action.Config["target"]) {
		case "orchestrator", "qa_leader", "reviewer", "none":
		case "agent":
			if strings.TrimSpace(action.Config["agent_id"]) == "" {
				return fmt.Errorf("assign to a specific agent needs agent_id")
			}
		default:
			return fmt.Errorf("assign needs target orchestrator|qa_leader|reviewer|agent|none")
		}
	case automationActionAddLabel, automationActionRemoveLabel:
		if strings.TrimSpace(action.Config["name"]) == "" {
			return fmt.Errorf("%s needs a label name", action.Type)
		}
	case automationActionPostComment:
		if strings.TrimSpace(action.Config["body"]) == "" {
			return fmt.Errorf("post_comment needs a body")
		}
	case automationActionSendTelegram:
		switch strings.TrimSpace(action.Config["destination"]) {
		case "group", "owner":
		default:
			return fmt.Errorf("send_telegram needs destination group|owner")
		}
		// chat_id is optional and only meaningful for a group: the destination is
		// otherwise resolved from the agent's own bound group.
		if strings.TrimSpace(action.Config["chat_id"]) != "" && strings.TrimSpace(action.Config["destination"]) != "group" {
			return fmt.Errorf("chat_id only applies to destination group")
		}
	default:
		return fmt.Errorf("unknown action type %q", action.Type)
	}
	return nil
}

func automationTriggerKnown(trigger string) bool {
	for _, known := range automationTriggers {
		if known == strings.TrimSpace(trigger) {
			return true
		}
	}
	return false
}

// isKnownIssueStatus guards the set_status action against a typo that would write
// a status no board column renders.
func isKnownIssueStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "backlog", "todo", "in_progress", "in_review", "done", "cancelled", "blocked":
		return true
	default:
		return false
	}
}

// decodeAutomationConditions / decodeAutomationActions read the stored JSONB.
// A malformed column yields an empty list plus an error the caller logs: the rule
// then matches nothing (conditions) or does nothing (actions) instead of the
// engine panicking on a hand-edited row.
func decodeAutomationConditions(raw []byte) ([]automationCondition, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []automationCondition
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeAutomationActions(raw []byte) ([]automationAction, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []automationAction
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
