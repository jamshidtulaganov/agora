package billing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Change is an operator's manual plan change: from admintool or the platform
// admin page. Never accepted from a tenant-facing endpoint.
type Change struct {
	// Mode is "team", "enterprise", "free" or "trial".
	Mode     string `json:"mode"`
	Interval string `json:"interval"`    // "", "month" or "year" (paid plans)
	Seats    int    `json:"seats"`       // seats paid for (paid plans)
	Until    string `json:"until"`       // paid through YYYY-MM-DD; "" = no end date
	TrialEnd string `json:"trial_until"` // trial ends YYYY-MM-DD (Mode "trial")
	Note     string `json:"note"`        // e.g. an invoice number
}

// Params validates the change and builds the row to write (WorkspaceID unset).
func (c Change) Params() (db.SetWorkspaceSubscriptionParams, error) {
	p := db.SetWorkspaceSubscriptionParams{
		BillingInterval: strings.TrimSpace(c.Interval),
		Provider:        "manual",
		Note:            strings.TrimSpace(c.Note),
	}
	if c.Seats < 0 || c.Seats > 100000 {
		return p, errors.New("seats must be between 0 and 100000")
	}
	p.SeatsPaid = int32(c.Seats)
	if len(p.Note) > 500 {
		return p, errors.New("note is too long (max 500 characters)")
	}

	switch strings.TrimSpace(c.Mode) {
	case "trial":
		end, err := ParseDay(c.TrialEnd)
		if err != nil {
			return p, fmt.Errorf("trial_until: %w", err)
		}
		p.Plan, p.Status = PlanTeam, StatusTrialing
		p.TrialEndsAt = pgtype.Timestamptz{Time: end, Valid: true}
		p.BillingInterval, p.SeatsPaid = "", 0
	case PlanTeam, PlanEnterprise:
		if p.BillingInterval != "" && p.BillingInterval != "month" && p.BillingInterval != "year" {
			return p, errors.New("interval must be month or year")
		}
		p.Plan, p.Status = strings.TrimSpace(c.Mode), StatusActive
		if strings.TrimSpace(c.Until) != "" {
			end, err := ParseDay(c.Until)
			if err != nil {
				return p, fmt.Errorf("until: %w", err)
			}
			p.CurrentPeriodEnd = pgtype.Timestamptz{Time: end, Valid: true}
		}
	case PlanFree:
		p.Plan, p.Status = PlanFree, StatusActive
		p.BillingInterval, p.SeatsPaid = "", 0
	default:
		return p, errors.New("mode must be team, enterprise, free or trial")
	}
	return p, nil
}

// ParseDay reads YYYY-MM-DD as the END of that day in UTC, so "until
// 2026-12-31" keeps the plan working through the 31st.
func ParseDay(s string) (time.Time, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not YYYY-MM-DD", s)
	}
	return d.Add(24*time.Hour - time.Second), nil
}
