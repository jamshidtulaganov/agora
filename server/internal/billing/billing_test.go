package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func TestEffective(t *testing.T) {
	cases := []struct {
		name       string
		row        *db.WorkspaceSubscription
		wantPlan   string
		wantStatus string
		wantDays   int
	}{
		{"no row is free", nil, PlanFree, StatusActive, 0},
		{"fresh trial", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusTrialing, TrialEndsAt: ts(TrialEnd(now))}, PlanTeam, StatusTrialing, 14},
		{"trial partial day rounds up", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusTrialing, TrialEndsAt: ts(now.Add(26 * time.Hour))}, PlanTeam, StatusTrialing, 2},
		{"trial ended", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusTrialing, TrialEndsAt: ts(now.Add(-time.Minute))}, PlanFree, StatusTrialExpired, 0},
		{"trial row without end date", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusTrialing}, PlanFree, StatusTrialExpired, 0},
		{"paid, no end date", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusActive}, PlanTeam, StatusActive, 0},
		{"paid, period running", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusActive, CurrentPeriodEnd: ts(now.AddDate(0, 1, 0))}, PlanTeam, StatusActive, 0},
		{"paid, in grace", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusActive, CurrentPeriodEnd: ts(now.AddDate(0, 0, -3))}, PlanTeam, StatusPastDue, 0},
		{"paid, grace over", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusActive, CurrentPeriodEnd: ts(now.AddDate(0, 0, -GraceDays-1))}, PlanFree, StatusExpired, 0},
		{"canceled", &db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusCanceled}, PlanFree, StatusCanceled, 0},
		{"unknown status keeps plan", &db.WorkspaceSubscription{Plan: PlanTeam, Status: "paused"}, PlanTeam, "paused", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := Effective(c.row, now)
			if st.Plan != c.wantPlan || st.Status != c.wantStatus || st.TrialDaysLeft != c.wantDays {
				t.Fatalf("got plan=%s status=%s days=%d, want %s %s %d", st.Plan, st.Status, st.TrialDaysLeft, c.wantPlan, c.wantStatus, c.wantDays)
			}
			if st.Limits != planLimits(c.wantPlan) {
				t.Fatalf("limits %+v do not match plan %s", st.Limits, c.wantPlan)
			}
		})
	}
}

func TestFreeLimitsOnlyGateAutomation(t *testing.T) {
	free := planLimits(PlanFree)
	if free.Automations != 2 || free.ReportSchedules != 1 || free.Autopilots != 0 {
		t.Fatalf("free limits changed: %+v", free)
	}
	if planLimits(PlanTeam) != unlimitedLimits {
		t.Fatal("team must be unlimited")
	}
}

type fakeReader struct {
	row db.WorkspaceSubscription
	err error
}

func (f fakeReader) GetWorkspaceSubscription(context.Context, pgtype.UUID) (db.WorkspaceSubscription, error) {
	return f.row, f.err
}

func TestForWorkspace(t *testing.T) {
	ctx := context.Background()
	expired := fakeReader{row: db.WorkspaceSubscription{Plan: PlanTeam, Status: StatusTrialing, TrialEndsAt: ts(time.Now().Add(-time.Hour))}}

	t.Setenv("AGORA_BILLING_ENABLED", "")
	if st := ForWorkspace(ctx, expired, pgtype.UUID{}); st.Enforced || st.Limits != unlimitedLimits {
		t.Fatalf("billing off must be unlimited, got %+v", st)
	}
	if st := Describe(ctx, expired, pgtype.UUID{}); st.Enforced || st.Status != StatusTrialExpired {
		t.Fatalf("describe must show the real state while off, got %+v", st)
	}

	t.Setenv("AGORA_BILLING_ENABLED", "true")
	if st := ForWorkspace(ctx, expired, pgtype.UUID{}); !st.Enforced || st.Plan != PlanFree {
		t.Fatalf("expired trial must be free when on, got %+v", st)
	}
	if st := ForWorkspace(ctx, fakeReader{err: pgx.ErrNoRows}, pgtype.UUID{}); st.Plan != PlanFree {
		t.Fatalf("no row must be free, got %+v", st)
	}
	if st := ForWorkspace(ctx, fakeReader{err: errors.New("db down")}, pgtype.UUID{}); st.Limits != unlimitedLimits {
		t.Fatalf("read error must fail open, got %+v", st)
	}
}

func TestAllowed(t *testing.T) {
	ctx := context.Background()
	a := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	b := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	list := func(context.Context, int32) ([]pgtype.UUID, error) { return []pgtype.UUID{a}, nil }
	failing := func(context.Context, int32) ([]pgtype.UUID, error) { return nil, errors.New("boom") }

	if !Allowed(ctx, Unlimited, b, list) {
		t.Fatal("unlimited must allow")
	}
	if Allowed(ctx, 0, a, list) {
		t.Fatal("zero must block")
	}
	if !Allowed(ctx, 1, a, list) || Allowed(ctx, 1, b, list) {
		t.Fatal("only the oldest N may run")
	}
	if !Allowed(ctx, 1, b, failing) {
		t.Fatal("list error must fail open")
	}
}
