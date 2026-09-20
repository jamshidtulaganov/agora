package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/config"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Per-task budgets (docs/orchestration-upgrade-plan.md §B2).
//
// Before this, the contract an Agora agent ran under was: unlimited wall
// clock (DefaultAgentTimeout = 0, deliberately — MUL-3064), unlimited turns
// (MaxTurns plumbed end-to-end and never set), unlimited money (no spend
// concept anywhere), and no way to ask a question. Budgets are the half of
// the fix that makes an agent STOP; escalation is the half that gives the
// stop somewhere to go. Shipping either alone is worse than shipping neither
// — a budget with no hatch is just a new failure, and a hatch with no budget
// never fires.
//
// Resolution happens HERE, at claim time, not in the daemon: the budget is a
// property of the ISSUE (its tier), and the daemon has never seen a tier. The
// resolved numbers ride the claim response as additive fields, so an old
// daemon ignores them and keeps the previous unbounded behaviour.

// budgetTier names the four cost tiers the pipeline already speaks in
// (service/issue_tier.go writes tier:trivial / tier:light; the claim path
// injects tier:medium under AGORA_MEDIUM_TIER; a human may set tier:heavy).
// "" is not a tier — an untiered issue resolves to medium, which is what the
// rest of the pipeline already assumes.
const (
	budgetTierTrivial = "trivial"
	budgetTierLight   = "light"
	budgetTierMedium  = "medium"
	budgetTierHeavy   = "heavy"
)

// budgetTierForLabels maps an issue's label set onto a budget tier. Heavy
// wins over medium wins over light wins over trivial when several are
// present — always resolve UP, because under-budgeting a big task produces a
// blown budget and an escalation a human has to answer, which is the
// expensive outcome this whole system exists to reduce.
//
// context:large is treated as heavy for the same reason: a 1M-context task is
// not a medium task that happens to read more.
func budgetTierForLabels(labels map[string]bool) string {
	switch {
	case labels["tier:heavy"], labels["context:large"]:
		return budgetTierHeavy
	case labels["tier:light"]:
		return budgetTierLight
	case labels["tier:trivial"]:
		return budgetTierTrivial
	default:
		// tier:medium and untiered both land here. An issue nobody has
		// classified gets the middle budget, not the smallest one.
		return budgetTierMedium
	}
}

// taskBudgets is what the claim response carries to the daemon. A zero in any
// field means "no cap" — never "a cap of zero". That asymmetry is load
// bearing: a cap nobody configured must never wedge a fleet (fail-open), and
// an old server talking to a new daemon sends zeros.
type taskBudgets struct {
	MaxTurns       int
	MaxBudgetUSD   float64
	TimeoutSeconds int
}

// budgetUSDKeyForTier returns the registry key holding the dollar ceiling for
// a tier. Named exactly as docs/orchestration-upgrade-plan.md §F1 specifies.
func budgetUSDKeyForTier(tier string) string {
	switch tier {
	case budgetTierTrivial:
		return "AGORA_TASK_BUDGET_USD_TRIVIAL"
	case budgetTierLight:
		return "AGORA_TASK_BUDGET_USD_LIGHT"
	case budgetTierHeavy:
		return "AGORA_TASK_BUDGET_USD_HEAVY"
	default:
		return "AGORA_TASK_BUDGET_USD_MEDIUM"
	}
}

// parseBudgetUSD reads a dollar amount from a config string. Empty, unparsable
// and non-positive all mean "off" — a typo in a budget field must disable the
// cap, never impose a $0 one that fails every run instantly.
func parseBudgetUSD(raw string) float64 {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "$"))
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// resolveTaskBudgets computes the three per-task budgets for an issue.
// overrides is the issue's project-scoped config map (projectConfigOverrides),
// so one project can run a tighter fleet than another without an operator
// touching instance-wide settings.
func resolveTaskBudgets(overrides map[string]string, labels map[string]bool) taskBudgets {
	tier := budgetTierForLabels(labels)

	turns := config.IntFrom(overrides, "AGORA_TASK_MAX_TURNS", 0)
	if turns < 0 {
		turns = 0
	}
	timeoutMinutes := config.IntFrom(overrides, "AGORA_TASK_TIMEOUT_MINUTES", 0)
	if timeoutMinutes < 0 {
		timeoutMinutes = 0
	}
	return taskBudgets{
		MaxTurns:       turns,
		MaxBudgetUSD:   parseBudgetUSD(config.StringFrom(overrides, budgetUSDKeyForTier(tier))),
		TimeoutSeconds: timeoutMinutes * 60,
	}
}

// taskBudgetsForIssue is the claim-path entry point: resolve the project's
// config overrides once, then compute the budgets from the label set the
// caller already loaded for cost tiering.
func (h *Handler) taskBudgetsForIssue(ctx context.Context, issue db.Issue, labels map[string]bool) taskBudgets {
	return resolveTaskBudgets(h.projectConfigOverrides(ctx, issue), labels)
}
