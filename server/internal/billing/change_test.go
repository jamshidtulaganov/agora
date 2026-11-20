package billing

import "testing"

func TestChangeParams(t *testing.T) {
	p, err := Change{Mode: "team", Interval: "year", Seats: 5, Until: "2027-11-20", Note: " inv-1 "}.Params()
	if err != nil || p.Plan != PlanTeam || p.Status != StatusActive || p.SeatsPaid != 5 || !p.CurrentPeriodEnd.Valid || p.Note != "inv-1" {
		t.Fatalf("team: %+v %v", p, err)
	}
	if got := p.CurrentPeriodEnd.Time.Format("2006-01-02 15:04"); got != "2027-11-20 23:59" {
		t.Fatalf("until must be end of day UTC, got %s", got)
	}
	p, err = Change{Mode: "trial", TrialEnd: "2026-12-31", Seats: 9, Interval: "year"}.Params()
	if err != nil || p.Status != StatusTrialing || !p.TrialEndsAt.Valid || p.SeatsPaid != 0 || p.BillingInterval != "" {
		t.Fatalf("trial: %+v %v", p, err)
	}
	p, err = Change{Mode: "free", Seats: 3, Interval: "month"}.Params()
	if err != nil || p.Plan != PlanFree || p.SeatsPaid != 0 || p.BillingInterval != "" {
		t.Fatalf("free: %+v %v", p, err)
	}
	for _, bad := range []Change{
		{Mode: "gold"},
		{Mode: "trial"},
		{Mode: "team", Interval: "week"},
		{Mode: "team", Until: "20-11-2027"},
		{Mode: "team", Seats: -1},
	} {
		if _, err := bad.Params(); err == nil {
			t.Fatalf("expected error for %+v", bad)
		}
	}
}
