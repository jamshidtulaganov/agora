// Package billing decides what a workspace's plan lets it do right now.
//
// The workspace_subscription row stores facts (plan, status, dates). This
// package turns those facts into an effective State at read time, so a trial
// that ends or a payment that lapses needs no job to flip a row.
//
// Two rules hold everywhere (docs/monetization-plan.md §3.5):
//   - The tracker is never gated. Issues, boards, members, attended agent runs
//     and the Assistant keep working on every plan, in every state. Only
//     UNATTENDED automation (automation rules, scheduled reports, scheduled and
//     webhook autopilots) has a plan limit.
//   - Fail open. A billing read error resolves to unlimited and logs. Billing
//     must never take the product down to protect revenue that was not at risk.
//
// With AGORA_BILLING_ENABLED off (self-host, local dev, and prod until the flag
// is flipped) every workspace resolves to unlimited with no DB read.
package billing

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TrialDays is the length of the Team trial every new workspace starts with.
const TrialDays = 14

// GraceDays is how long a paid plan keeps working after its period ends
// without a renewal, before it drops to Free.
const GraceDays = 14

const (
	PlanFree       = "free"
	PlanTeam       = "team"
	PlanEnterprise = "enterprise"
)

// Effective status values. trialing / active / canceled are stored; the rest
// are derived from dates.
const (
	StatusTrialing     = "trialing"
	StatusActive       = "active"
	StatusPastDue      = "past_due"      // period ended, inside the grace window
	StatusTrialExpired = "trial_expired" // trial ended, never paid
	StatusExpired      = "expired"       // grace ended
	StatusCanceled     = "canceled"
)

// Unlimited is the value of a limit with no ceiling.
const Unlimited = -1

// Limits are counts of unattended things a plan lets run. Unlimited = -1.
type Limits struct {
	Automations     int `json:"automations"`
	ReportSchedules int `json:"report_schedules"`
	Autopilots      int `json:"autopilots"`
}

// PlanInfo is one plan in the catalog the billing page shows.
type PlanInfo struct {
	ID                string `json:"id"`
	PriceMonthlyCents int    `json:"price_monthly_cents"` // per human, billed monthly
	PriceYearlyCents  int    `json:"price_yearly_cents"`  // per human per month, billed yearly
	Limits            Limits `json:"limits"`
}

var unlimitedLimits = Limits{Automations: Unlimited, ReportSchedules: Unlimited, Autopilots: Unlimited}

// Catalog is the plan list. A price or limit change is a code change with a test.
var Catalog = []PlanInfo{
	{ID: PlanFree, Limits: Limits{Automations: 2, ReportSchedules: 1, Autopilots: 0}},
	{ID: PlanTeam, PriceMonthlyCents: 1500, PriceYearlyCents: 1200, Limits: unlimitedLimits},
	{ID: PlanEnterprise, Limits: unlimitedLimits},
}

func planLimits(plan string) Limits {
	for _, p := range Catalog {
		if p.ID == plan {
			return p.Limits
		}
	}
	return Catalog[0].Limits
}

// State is what the workspace may do right now.
type State struct {
	// Enforced is false when billing is off; every limit is then Unlimited.
	Enforced bool `json:"enforced"`
	// Plan is the plan in force now (an expired trial is "free").
	Plan string `json:"plan"`
	// PurchasedPlan is the plan on the row (what was bought or trialed).
	PurchasedPlan    string     `json:"purchased_plan"`
	Status           string     `json:"status"`
	BillingInterval  string     `json:"billing_interval"`
	SeatsPaid        int        `json:"seats_paid"`
	TrialEndsAt      *time.Time `json:"trial_ends_at"`
	TrialDaysLeft    int        `json:"trial_days_left"`
	CurrentPeriodEnd *time.Time `json:"current_period_end"`
	GraceEndsAt      *time.Time `json:"grace_ends_at"`
	Limits           Limits     `json:"limits"`
}

// Enabled reports whether plan limits are enforced on this instance.
//
// Read from the process environment ONLY, deliberately not from the config
// registry: instance_config is writable by workspace owners from Settings →
// Configs, so a registry flag would let any tenant switch billing off for
// everyone.
func Enabled() bool {
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("AGORA_BILLING_ENABLED")))
	return err == nil && v
}

// Contact is where the billing page tells owners to write to upgrade while
// payments are activated by hand. Env-only for the same reason as Enabled.
func Contact() string { return strings.TrimSpace(os.Getenv("AGORA_BILLING_CONTACT")) }

func unlimitedState() State {
	return State{Enforced: false, Plan: PlanTeam, PurchasedPlan: PlanTeam, Status: StatusActive, Limits: unlimitedLimits}
}

func tsPtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// daysLeft rounds up: 13 days and 2 hours left reads as 14 days.
func daysLeft(end, now time.Time) int {
	if !end.After(now) {
		return 0
	}
	return int(math.Ceil(end.Sub(now).Hours() / 24))
}

// Effective derives the State from a stored row. row == nil means the
// workspace has no subscription row, which is the Free plan.
func Effective(row *db.WorkspaceSubscription, now time.Time) State {
	if row == nil {
		return State{Enforced: true, Plan: PlanFree, PurchasedPlan: PlanFree, Status: StatusActive, Limits: planLimits(PlanFree)}
	}
	st := State{
		Enforced:         true,
		Plan:             row.Plan,
		PurchasedPlan:    row.Plan,
		Status:           row.Status,
		BillingInterval:  row.BillingInterval,
		SeatsPaid:        int(row.SeatsPaid),
		TrialEndsAt:      tsPtr(row.TrialEndsAt),
		CurrentPeriodEnd: tsPtr(row.CurrentPeriodEnd),
	}
	switch row.Status {
	case StatusTrialing:
		if st.TrialEndsAt == nil || !st.TrialEndsAt.After(now) {
			st.Plan, st.Status = PlanFree, StatusTrialExpired
		} else {
			st.TrialDaysLeft = daysLeft(*st.TrialEndsAt, now)
		}
	case StatusActive:
		if st.CurrentPeriodEnd != nil && !st.CurrentPeriodEnd.After(now) {
			grace := st.CurrentPeriodEnd.AddDate(0, 0, GraceDays)
			st.GraceEndsAt = &grace
			if grace.After(now) {
				st.Status = StatusPastDue
			} else {
				st.Plan, st.Status = PlanFree, StatusExpired
			}
		}
	case StatusCanceled:
		st.Plan = PlanFree
	default:
		// Unknown stored status: keep the stored plan rather than downgrade a
		// paying workspace on a value this build does not know.
	}
	st.Limits = planLimits(st.Plan)
	return st
}

// Reader is the one query the resolver needs.
type Reader interface {
	GetWorkspaceSubscription(ctx context.Context, workspaceID pgtype.UUID) (db.WorkspaceSubscription, error)
}

// ForWorkspace resolves the workspace's State now. Off → unlimited, no read.
// A read error → unlimited (fail open) and a warning.
func ForWorkspace(ctx context.Context, q Reader, workspaceID pgtype.UUID) State {
	if !Enabled() {
		return unlimitedState()
	}
	return resolve(ctx, q, workspaceID, time.Now())
}

// Describe resolves the State for display, even when billing is off, so the
// billing page can show the trial before enforcement is flipped on.
func Describe(ctx context.Context, q Reader, workspaceID pgtype.UUID) State {
	st := resolve(ctx, q, workspaceID, time.Now())
	st.Enforced = Enabled()
	return st
}

func resolve(ctx context.Context, q Reader, workspaceID pgtype.UUID, now time.Time) State {
	row, err := q.GetWorkspaceSubscription(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Effective(nil, now)
	}
	if err != nil {
		slog.Warn("billing: read subscription failed; allowing", "error", err)
		return unlimitedState()
	}
	return Effective(&row, now)
}

// TrialEnd is when a trial started at now ends.
func TrialEnd(now time.Time) time.Time { return now.AddDate(0, 0, TrialDays) }

// LimitedIDs lists the IDs a limit lets run (the oldest N). allowAll is true
// when the limit is Unlimited, in which case ids is nil and need not be read.
type IDLister func(ctx context.Context, limit int32) ([]pgtype.UUID, error)

// Allowed reports whether id is among the items a limit lets run. On a read
// error it allows (fail open).
func Allowed(ctx context.Context, limit int, id pgtype.UUID, list IDLister) bool {
	if limit < 0 {
		return true
	}
	if limit == 0 {
		return false
	}
	ids, err := list(ctx, int32(limit))
	if err != nil {
		slog.Warn("billing: list allowed ids failed; allowing", "error", err)
		return true
	}
	for _, a := range ids {
		if a == id {
			return true
		}
	}
	return false
}
