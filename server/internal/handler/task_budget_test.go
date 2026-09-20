package handler

import "testing"

// Budget resolution (docs/orchestration-upgrade-plan.md §B2). The property
// under test throughout is FAIL-OPEN: a cap nobody set — or one somebody
// mistyped — must mean NO cap, never a cap of zero that fails every run
// instantly. A budget that wedges a fleet is worse than no budget.

func TestBudgetTierForLabels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		labels map[string]bool
		want   string
	}{
		{"untiered resolves to medium, not trivial", nil, budgetTierMedium},
		{"explicit medium", map[string]bool{"tier:medium": true}, budgetTierMedium},
		{"trivial", map[string]bool{"tier:trivial": true}, budgetTierTrivial},
		{"light", map[string]bool{"tier:light": true}, budgetTierLight},
		{"heavy", map[string]bool{"tier:heavy": true}, budgetTierHeavy},
		{"context:large counts as heavy", map[string]bool{"context:large": true}, budgetTierHeavy},
		// Resolve UP when several are present: under-budgeting a big task
		// produces a blown budget and a human interruption, which is the
		// expensive outcome.
		{"heavy wins over trivial", map[string]bool{"tier:heavy": true, "tier:trivial": true}, budgetTierHeavy},
		{"light wins over trivial", map[string]bool{"tier:light": true, "tier:trivial": true}, budgetTierLight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := budgetTierForLabels(c.labels); got != c.want {
				t.Errorf("budgetTierForLabels = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseBudgetUSDFailsOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
	}{
		{"", 0},
		{"   ", 0},
		{"5", 5},
		{"5.50", 5.5},
		{"$5.50", 5.5},   // an operator typing dollars into a dollar field
		{"0", 0},         // an explicit zero is "off", never "fail every run"
		{"-3", 0},        // ditto
		{"abc", 0},       // a typo disables the cap rather than imposing $0
		{"5 dollars", 0}, // unparsable ⇒ off
	}
	for _, c := range cases {
		if got := parseBudgetUSD(c.in); got != c.want {
			t.Errorf("parseBudgetUSD(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveTaskBudgetsPerTier(t *testing.T) {
	t.Parallel()
	overrides := map[string]string{
		"AGORA_TASK_BUDGET_USD_TRIVIAL": "0.50",
		"AGORA_TASK_BUDGET_USD_LIGHT":   "1.50",
		"AGORA_TASK_BUDGET_USD_MEDIUM":  "5",
		"AGORA_TASK_BUDGET_USD_HEAVY":   "15",
		"AGORA_TASK_MAX_TURNS":          "120",
		"AGORA_TASK_TIMEOUT_MINUTES":    "45",
	}

	got := resolveTaskBudgets(overrides, map[string]bool{"tier:trivial": true})
	if got.MaxBudgetUSD != 0.50 {
		t.Errorf("trivial dollar ceiling = %v, want 0.50", got.MaxBudgetUSD)
	}
	if got.MaxTurns != 120 {
		t.Errorf("MaxTurns = %d, want 120", got.MaxTurns)
	}
	if got.TimeoutSeconds != 45*60 {
		t.Errorf("TimeoutSeconds = %d, want %d", got.TimeoutSeconds, 45*60)
	}

	if got := resolveTaskBudgets(overrides, map[string]bool{"tier:heavy": true}); got.MaxBudgetUSD != 15 {
		t.Errorf("heavy dollar ceiling = %v, want 15", got.MaxBudgetUSD)
	}
	// An untiered issue takes the medium ceiling, which is what the rest of
	// the pipeline already assumes about an unclassified task.
	if got := resolveTaskBudgets(overrides, nil); got.MaxBudgetUSD != 5 {
		t.Errorf("untiered dollar ceiling = %v, want the medium 5", got.MaxBudgetUSD)
	}
}

// With nothing configured the dollar cap and the wall clock are OFF — the
// wall clock deliberately so, because the daemon-wide default of 0 is a
// design decision (MUL-3064) whose liveness net is the idle / tool / startup
// watchdogs. The turn cap is the one budget that ships ON: it is a
// runaway-loop guard, and a run that trips it escalates instead of retrying.
func TestResolveTaskBudgetsDefaults(t *testing.T) {
	got := resolveTaskBudgets(nil, nil)
	if got.MaxBudgetUSD != 0 {
		t.Errorf("dollar cap must default to off, got %v", got.MaxBudgetUSD)
	}
	if got.TimeoutSeconds != 0 {
		t.Errorf("wall clock must default to off (MUL-3064), got %d", got.TimeoutSeconds)
	}
	if got.MaxTurns <= 0 {
		t.Fatalf("the turn cap must actually be SET by default, got %d", got.MaxTurns)
	}
	if got.MaxTurns < 100 {
		t.Errorf("default turn cap %d is low enough to trip legitimate work", got.MaxTurns)
	}
}

// A negative value in either integer field is a misconfiguration, not an
// instruction to cap at a negative number.
func TestResolveTaskBudgetsNegativesAreOff(t *testing.T) {
	t.Parallel()
	got := resolveTaskBudgets(map[string]string{
		"AGORA_TASK_MAX_TURNS":       "-5",
		"AGORA_TASK_TIMEOUT_MINUTES": "-10",
	}, nil)
	if got.MaxTurns != 0 || got.TimeoutSeconds != 0 {
		t.Errorf("negatives must resolve to no cap, got %+v", got)
	}
}
