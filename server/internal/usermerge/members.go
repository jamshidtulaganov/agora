package usermerge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AddMembersOptions adds existing accounts to a workspace directly (no
// invitation), e.g. to build a cross-department workspace from people who
// are already in Agora.
type AddMembersOptions struct {
	WorkspaceSlug string
	Role          string
	// Emails may be an account's own address or one of its login aliases.
	Emails []string
	Apply  bool
}

// AddMemberResult is what happened for one email.
type AddMemberResult struct {
	Email   string
	Outcome string
}

// AddMembers never lowers a role: an existing member keeps the higher of
// their current and the requested role.
func AddMembers(ctx context.Context, db DB, opts AddMembersOptions) ([]AddMemberResult, error) {
	if _, ok := roleRank[opts.Role]; !ok {
		return nil, fmt.Errorf("role %q must be member, admin or owner", opts.Role)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var wsID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workspace WHERE slug = $1`, opts.WorkspaceSlug).Scan(&wsID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("no workspace with slug %q", opts.WorkspaceSlug)
		}
		return nil, err
	}

	var results []AddMemberResult
	for _, raw := range opts.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		var userID string
		err := tx.QueryRow(ctx, `
			SELECT u.id::text FROM "user" u
			WHERE u.email = $1 OR u.id IN (SELECT user_id FROM user_email_alias WHERE email = $1)
			ORDER BY (u.email = $1) DESC LIMIT 1`, email).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("no account for %s", email)
		}
		if err != nil {
			return nil, err
		}

		var current string
		err = tx.QueryRow(ctx, `SELECT role FROM member WHERE workspace_id = $1 AND user_id = $2`, wsID, userID).Scan(&current)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)`, wsID, userID, opts.Role); err != nil {
				return nil, err
			}
			results = append(results, AddMemberResult{Email: email, Outcome: "added as " + opts.Role})
		case err != nil:
			return nil, err
		case higherRole(current, opts.Role) != current:
			if _, err := tx.Exec(ctx, `UPDATE member SET role = $3 WHERE workspace_id = $1 AND user_id = $2`, wsID, userID, opts.Role); err != nil {
				return nil, err
			}
			results = append(results, AddMemberResult{Email: email, Outcome: fmt.Sprintf("raised %s -> %s", current, opts.Role)})
		default:
			results = append(results, AddMemberResult{Email: email, Outcome: "already " + current})
		}
	}

	if opts.Apply {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
	}
	return results, nil
}
