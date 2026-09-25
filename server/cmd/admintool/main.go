// Command admintool runs one-off account operations against DATABASE_URL.
// Every command is a dry run unless --apply is given.
//
//	admintool merge-users [--apply] KEEP_EMAIL:DROP_EMAIL ...
//	admintool add-members [--apply] --workspace SLUG --role member|admin|owner EMAIL ...
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jamshidtulaganov/agora/server/internal/usermerge"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fail(errors.New("DATABASE_URL is not set"))
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fail(err)
	}
	defer pool.Close()

	switch os.Args[1] {
	case "merge-users":
		err = mergeUsers(ctx, pool, os.Args[2:])
	case "add-members":
		err = addMembers(ctx, pool, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fail(err)
	}
}

func mergeUsers(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("merge-users", flag.ExitOnError)
	apply := fs.Bool("apply", false, "commit the merge (default: dry run)")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("give at least one KEEP_EMAIL:DROP_EMAIL pair")
	}
	for _, pair := range fs.Args() {
		keep, drop, ok := strings.Cut(pair, ":")
		if !ok {
			return fmt.Errorf("%q is not KEEP_EMAIL:DROP_EMAIL", pair)
		}
		report, err := usermerge.Merge(ctx, pool, usermerge.Options{KeepEmail: keep, DropEmail: drop, Apply: *apply})
		if err != nil {
			return fmt.Errorf("%s: %w", pair, err)
		}
		printReport(report)
	}
	return nil
}

func printReport(r usermerge.Report) {
	mode := "APPLIED"
	if r.DryRun {
		mode = "DRY RUN (rolled back)"
	}
	fmt.Printf("\n== %s: %s (%s) <- %s (%s)\n", mode, r.Keep.Email, r.Keep.Name, r.Drop.Email, r.Drop.Name)
	for _, c := range r.Roles {
		fmt.Printf("   shared workspace %s: kept %s, dropped %s -> %s\n", c.WorkspaceID, c.KeptRole, c.DroppedRole, c.FinalRole)
	}
	for _, c := range r.Columns {
		fmt.Printf("   %-40s moved %d", c.Table+"."+c.Column, c.Moved)
		if c.Removed > 0 {
			fmt.Printf(", removed %d duplicate(s)", c.Removed)
		}
		fmt.Println()
	}
	fmt.Printf("   login aliases now: %s\n", strings.Join(r.Aliases, ", "))
}

func addMembers(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("add-members", flag.ExitOnError)
	apply := fs.Bool("apply", false, "commit (default: dry run)")
	slug := fs.String("workspace", "", "workspace slug")
	role := fs.String("role", "member", "member, admin or owner")
	_ = fs.Parse(args)
	if *slug == "" || fs.NArg() == 0 {
		return errors.New("give --workspace and at least one email")
	}
	results, err := usermerge.AddMembers(ctx, pool, usermerge.AddMembersOptions{
		WorkspaceSlug: *slug, Role: *role, Emails: fs.Args(), Apply: *apply,
	})
	if err != nil {
		return err
	}
	mode := "APPLIED"
	if !*apply {
		mode = "DRY RUN (rolled back)"
	}
	fmt.Printf("\n== %s: add to %s as %s\n", mode, *slug, *role)
	for _, r := range results {
		fmt.Printf("   %-36s %s\n", r.Email, r.Outcome)
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:\n  admintool merge-users [--apply] KEEP_EMAIL:DROP_EMAIL ...\n  admintool add-members [--apply] --workspace SLUG --role member|admin|owner EMAIL ...")
	os.Exit(2)
}

func fail(err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		err = errors.New("not found")
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
