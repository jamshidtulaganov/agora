package handler

import "testing"

// TestEvaluateAutomationConditions covers the semantics a rule author relies on.
// Case-insensitivity is load-bearing on this deployment: statuses, labels and kanban
// column names are typed by humans in two languages, and a rule that silently fails
// on "Code Review" vs "code review" is worse than no rule at all.
func TestEvaluateAutomationConditions(t *testing.T) {
	facts := newAutomationFacts()
	facts.set("stage", "Code Review")
	facts.set("to_status", "in_review")
	facts.set("title", "Отчёты: add totals")
	facts.Labels["review:fail"] = true
	facts.Labels["tier:medium"] = true

	cases := []struct {
		name  string
		conds []automationCondition
		want  bool
	}{
		{"empty always matches", nil, true},
		{"eq is case-insensitive", []automationCondition{{Field: "stage", Op: automationOpEq, Value: "code review"}}, true},
		{"eq mismatch", []automationCondition{{Field: "stage", Op: automationOpEq, Value: "testing"}}, false},
		{"in matches any", []automationCondition{{Field: "to_status", Op: automationOpIn, Value: []any{"done", "in_review"}}}, true},
		{"not_in excludes", []automationCondition{{Field: "to_status", Op: automationOpNotIn, Value: []any{"in_review"}}}, false},
		{"contains, cyrillic", []automationCondition{{Field: "title", Op: automationOpContains, Value: "отчёты"}}, true},
		{"exists on a present field", []automationCondition{{Field: "stage", Op: automationOpExists}}, true},
		{"exists on a missing field", []automationCondition{{Field: "label", Op: automationOpExists}}, false},
		{"has_label", []automationCondition{{Field: "labels", Op: automationOpHasLabel, Value: "review:fail"}}, true},
		{"has_label missing", []automationCondition{{Field: "labels", Op: automationOpHasLabel, Value: "qa:pass"}}, false},
		{"not_has_label", []automationCondition{{Field: "labels", Op: automationOpNotHasLabel, Value: "qa:pass"}}, true},
		{"AND of two clauses", []automationCondition{
			{Field: "stage", Op: automationOpEq, Value: "Code Review"},
			{Field: "labels", Op: automationOpNotHasLabel, Value: "qa:pass"},
		}, true},
		{"AND fails when one clause fails", []automationCondition{
			{Field: "stage", Op: automationOpEq, Value: "Code Review"},
			{Field: "labels", Op: automationOpHasLabel, Value: "qa:pass"},
		}, false},
		// An unknown operator must never match: matching would run actions the
		// author never asked for.
		{"unknown operator never matches", []automationCondition{{Field: "stage", Op: "regex", Value: ".*"}}, false},
	}
	for _, c := range cases {
		got, reason := evaluateAutomationConditions(c.conds, facts)
		if got != c.want {
			t.Errorf("%s: matched = %v, want %v (reason %q)", c.name, got, c.want, reason)
		}
		if !got && reason == "" {
			t.Errorf("%s: a failed match must explain itself", c.name)
		}
	}
}

// TestAutomationValueStrings covers the value shapes the editor may send for one
// condition: a bare string, a number, a bool, or a list.
func TestAutomationValueStrings(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  []string
	}{
		{"string", "todo", []string{"todo"}},
		{"trimmed", "  todo  ", []string{"todo"}},
		{"bool", true, []string{"true"}},
		{"number", float64(3), []string{"3"}},
		{"list", []any{"a", "b"}, []string{"a", "b"}},
		{"string list", []string{"a", "", "b"}, []string{"a", "b"}},
		{"nil", nil, nil},
		{"unsupported", map[string]any{"a": 1}, nil},
	}
	for _, c := range cases {
		got := automationValueStrings(c.value)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

// TestValidateAutomationRule keeps the editor honest: a rule the engine could not
// honour must be rejected at save time rather than saved and silently inert.
func TestValidateAutomationRule(t *testing.T) {
	okAction := automationAction{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}}

	cases := []struct {
		name    string
		trigger string
		conds   []automationCondition
		actions []automationAction
		wantErr bool
	}{
		{"minimal valid", automationTriggerStatusChanged, nil, []automationAction{okAction}, false},
		{"unknown trigger", "issue.exploded", nil, []automationAction{okAction}, true},
		{"no actions", automationTriggerStatusChanged, nil, nil, true},
		{"unknown action", automationTriggerStatusChanged, nil, []automationAction{{Type: "call_url"}}, true},
		{"bad status", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationActionSetStatus, Config: map[string]string{"status": "shipped"}}}, true},
		{"unknown slice kind", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationActionDispatchSlice, Config: map[string]string{"kind": "do_magic"}}}, true},
		{"known slice kind", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationActionDispatchSlice, Config: map[string]string{"kind": sliceActionRunReview}}}, false},
		{"assign to agent needs an id", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationActionAssign, Config: map[string]string{"target": "agent"}}}, true},
		{"assign to orchestrator", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationActionAssign, Config: map[string]string{"target": "orchestrator"}}}, false},
		{"telegram needs a destination", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationActionSendTelegram, Config: map[string]string{}}}, true},
		{"filter needs conditions", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationStepFilter}}, true},
		{"filter with conditions", automationTriggerStatusChanged, nil,
			[]automationAction{{Type: automationStepFilter, Conditions: []automationCondition{{Field: "status", Op: automationOpEq, Value: "todo"}}}, okAction}, false},
		{"unknown operator", automationTriggerStatusChanged,
			[]automationCondition{{Field: "status", Op: "regex", Value: ".*"}}, []automationAction{okAction}, true},
		{"condition without a value", automationTriggerStatusChanged,
			[]automationCondition{{Field: "status", Op: automationOpEq}}, []automationAction{okAction}, true},
		{"exists needs no value", automationTriggerStatusChanged,
			[]automationCondition{{Field: "status", Op: automationOpExists}}, []automationAction{okAction}, false},
	}
	for _, c := range cases {
		err := validateAutomationRule(c.trigger, c.conds, c.actions)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

// TestAutomationRecipesAreValid guards the built-in gallery: a recipe that fails
// validation would 500 on install, and the gallery is the first thing a new team
// touches.
func TestAutomationRecipesAreValid(t *testing.T) {
	recipes := automationRecipes()
	if len(recipes) == 0 {
		t.Fatal("the recipe gallery must not be empty")
	}
	seen := map[string]bool{}
	for _, recipe := range recipes {
		if recipe.Key == "" || recipe.Title == "" || recipe.Description == "" {
			t.Errorf("recipe %q is missing metadata", recipe.Key)
		}
		if seen[recipe.Key] {
			t.Errorf("duplicate recipe key %q", recipe.Key)
		}
		seen[recipe.Key] = true
		if len(recipe.Flows) == 0 {
			t.Errorf("recipe %q installs no flows", recipe.Key)
		}
		for _, flow := range recipe.Flows {
			if err := validateAutomationRule(flow.TriggerType, flow.Conditions, flow.Actions); err != nil {
				t.Errorf("recipe %q flow %q is invalid: %v", recipe.Key, flow.Name, err)
			}
		}
	}
	if _, ok := automationRecipeByKey(automationRecipeCodeReview); !ok {
		t.Error("the code-review recipe must be installable by key")
	}
	if _, ok := automationRecipeByKey("nope"); ok {
		t.Error("an unknown key must not resolve")
	}
}

// TestAutomationConfigInt covers the loop-guard overrides, which arrive as JSON
// numbers from the API and as strings from a hand-edited row.
func TestAutomationConfigInt(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"absent uses the default", `{}`, automationDefaultMinIntervalSeconds},
		{"number", `{"min_interval_seconds": 5}`, 5},
		{"numeric string", `{"min_interval_seconds": "7"}`, 7},
		{"zero disables the cooldown", `{"min_interval_seconds": 0}`, 0},
		{"negative falls back", `{"min_interval_seconds": -3}`, automationDefaultMinIntervalSeconds},
		{"malformed json falls back", `{oops`, automationDefaultMinIntervalSeconds},
		{"nil falls back", ``, automationDefaultMinIntervalSeconds},
	}
	for _, c := range cases {
		var raw []byte
		if c.raw != "" {
			raw = []byte(c.raw)
		}
		if got := automationConfigInt(raw, "min_interval_seconds", automationDefaultMinIntervalSeconds); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
