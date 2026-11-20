package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jamshidtulaganov/agora/server/internal/billing"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// setPlan is the manual payment path: an operator records that a workspace
// paid (or put it back on Free) until a payment provider is wired in.
//
//	admintool set-plan [--apply] --workspace SLUG --plan team|enterprise|free
//	          [--interval month|year] [--seats N] [--until YYYY-MM-DD] [--note TEXT]
//	admintool set-plan [--apply] --workspace SLUG --trial-until YYYY-MM-DD
func setPlan(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("set-plan", flag.ExitOnError)
	apply := fs.Bool("apply", false, "commit (default: dry run)")
	slug := fs.String("workspace", "", "workspace slug")
	plan := fs.String("plan", "", "team, enterprise or free")
	interval := fs.String("interval", "", "month or year (paid plans)")
	seats := fs.Int("seats", 0, "seats paid for")
	until := fs.String("until", "", "paid through YYYY-MM-DD (empty = no end date)")
	trialUntil := fs.String("trial-until", "", "put the workspace on a Team trial ending YYYY-MM-DD")
	note := fs.String("note", "", "free-text note, e.g. the invoice number")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("give --workspace")
	}

	mode := *plan
	if *trialUntil != "" {
		if *plan != "" {
			return errors.New("give --plan or --trial-until, not both")
		}
		mode = "trial"
	}
	params, err := billing.Change{
		Mode: mode, Interval: *interval, Seats: *seats, Until: *until, TrialEnd: *trialUntil, Note: *note,
	}.Params()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	ws, err := q.GetWorkspaceBySlug(ctx, *slug)
	if err != nil {
		return fmt.Errorf("workspace %q: %w", *slug, err)
	}
	params.WorkspaceID = ws.ID

	before, err := q.GetWorkspaceSubscription(ctx, ws.ID)
	var beforePtr *db.WorkspaceSubscription
	if err == nil {
		beforePtr = &before
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	after, err := q.SetWorkspaceSubscription(ctx, params)
	if err != nil {
		return err
	}

	result := "APPLIED"
	if *apply {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	} else {
		result = "DRY RUN (rolled back)"
	}
	now := time.Now()
	fmt.Printf("\n== %s: plan for %s (%s)\n", result, ws.Name, ws.Slug)
	fmt.Printf("   before: %s\n", describeState(billing.Effective(beforePtr, now)))
	fmt.Printf("   after:  %s\n", describeState(billing.Effective(&after, now)))
	return nil
}

// showPlan prints a workspace's plan as the product sees it.
func showPlan(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("show-plan", flag.ExitOnError)
	slug := fs.String("workspace", "", "workspace slug")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("give --workspace")
	}
	q := db.New(pool)
	ws, err := q.GetWorkspaceBySlug(ctx, *slug)
	if err != nil {
		return fmt.Errorf("workspace %q: %w", *slug, err)
	}
	row, err := q.GetWorkspaceSubscription(ctx, ws.ID)
	var rowPtr *db.WorkspaceSubscription
	if err == nil {
		rowPtr = &row
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	fmt.Printf("%s (%s): %s\n", ws.Name, ws.Slug, describeState(billing.Effective(rowPtr, time.Now())))
	if rowPtr != nil && row.Note != "" {
		fmt.Printf("   note: %s (provider %s)\n", row.Note, row.Provider)
	}
	return nil
}

func describeState(st billing.State) string {
	out := fmt.Sprintf("plan=%s status=%s", st.Plan, st.Status)
	if st.TrialEndsAt != nil {
		out += fmt.Sprintf(" trial_ends=%s (%d days left)", st.TrialEndsAt.UTC().Format("2006-01-02"), st.TrialDaysLeft)
	}
	if st.CurrentPeriodEnd != nil {
		out += " paid_until=" + st.CurrentPeriodEnd.UTC().Format("2006-01-02")
	}
	if st.BillingInterval != "" {
		out += " interval=" + st.BillingInterval
	}
	if st.SeatsPaid > 0 {
		out += fmt.Sprintf(" seats=%d", st.SeatsPaid)
	}
	return out
}
