// Package usermerge folds one account into another when both belong to the
// same person — e.g. a gmail sign-up and the company account the Zoho
// migration created. Everything the dropped account owns moves to the kept
// one, workspace roles take the higher of the two, and the dropped address
// becomes a login alias of the kept account.
package usermerge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// DB is the subset of pgxpool.Pool / pgx.Conn the merge needs.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Account is one side of a merge.
type Account struct {
	ID    string
	Email string
	Name  string
}

// ColumnChange is how many rows of one column moved to the kept account, and
// how many were removed because the kept account already had the same row
// (a unique key collision — e.g. both accounts had pinned the same issue).
type ColumnChange struct {
	Table   string
	Column  string
	Moved   int64
	Removed int64
}

// RoleChange is a workspace both accounts were members of.
type RoleChange struct {
	WorkspaceID string
	KeptRole    string
	DroppedRole string
	FinalRole   string
}

// Report describes a merge. With DryRun the same work ran inside a
// transaction that was rolled back.
type Report struct {
	Keep    Account
	Drop    Account
	DryRun  bool
	Columns []ColumnChange
	Roles   []RoleChange
	Aliases []string
}

// Options for one merge.
type Options struct {
	KeepEmail string
	DropEmail string
	// Apply commits the merge; false runs it and rolls back (a dry run).
	Apply bool
}

var roleRank = map[string]int{"member": 1, "admin": 2, "owner": 3}

func higherRole(a, b string) string {
	if roleRank[b] > roleRank[a] {
		return b
	}
	return a
}

// Merge folds the DropEmail account into the KeepEmail account in one
// transaction. Both emails must be the accounts' own addresses (not aliases).
func Merge(ctx context.Context, db DB, opts Options) (Report, error) {
	keepEmail := strings.ToLower(strings.TrimSpace(opts.KeepEmail))
	dropEmail := strings.ToLower(strings.TrimSpace(opts.DropEmail))
	if keepEmail == "" || dropEmail == "" {
		return Report{}, errors.New("both emails are required")
	}
	if keepEmail == dropEmail {
		return Report{}, errors.New("keep and drop are the same address")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	keep, err := loadAccount(ctx, tx, keepEmail)
	if err != nil {
		return Report{}, fmt.Errorf("keep account %s: %w", keepEmail, err)
	}
	drop, err := loadAccount(ctx, tx, dropEmail)
	if err != nil {
		return Report{}, fmt.Errorf("drop account %s: %w", dropEmail, err)
	}
	report := Report{Keep: keep, Drop: drop, DryRun: !opts.Apply}

	roles, err := mergeMemberships(ctx, tx, keep.ID, drop.ID)
	if err != nil {
		return Report{}, fmt.Errorf("memberships: %w", err)
	}
	report.Roles = roles

	columns, err := uuidColumns(ctx, tx)
	if err != nil {
		return Report{}, fmt.Errorf("list columns: %w", err)
	}
	for _, col := range columns {
		change, err := moveColumn(ctx, tx, col, keep.ID, drop.ID)
		if err != nil {
			return Report{}, fmt.Errorf("%s.%s: %w", col.table, col.column, err)
		}
		if change.Moved > 0 || change.Removed > 0 {
			report.Columns = append(report.Columns, change)
		}
	}

	if err := fillProfile(ctx, tx, keep.ID, drop.ID); err != nil {
		return Report{}, fmt.Errorf("profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, drop.ID); err != nil {
		return Report{}, fmt.Errorf("delete dropped account: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_email_alias (email, user_id) VALUES ($1, $2)
		 ON CONFLICT (email) DO UPDATE SET user_id = EXCLUDED.user_id`,
		drop.Email, keep.ID,
	); err != nil {
		return Report{}, fmt.Errorf("add login alias: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT email FROM user_email_alias WHERE user_id = $1 ORDER BY email`, keep.ID)
	if err != nil {
		return Report{}, err
	}
	aliases, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return Report{}, err
	}
	report.Aliases = aliases

	if opts.Apply {
		if err := tx.Commit(ctx); err != nil {
			return Report{}, err
		}
	}
	return report, nil
}

func loadAccount(ctx context.Context, tx pgx.Tx, email string) (Account, error) {
	var a Account
	var id pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT id, email, name FROM "user" WHERE email = $1`, email).Scan(&id, &a.Email, &a.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, errors.New("no account with this email (aliases don't count)")
	}
	if err != nil {
		return Account{}, err
	}
	a.ID = uuidString(id)
	return a, nil
}

// mergeMemberships resolves workspaces both accounts belong to before the
// generic column pass: the kept membership takes the higher role and the
// dropped one is removed. Memberships only the dropped account has are left
// for the column pass to move.
func mergeMemberships(ctx context.Context, tx pgx.Tx, keepID, dropID string) ([]RoleChange, error) {
	rows, err := tx.Query(ctx, `
		SELECT k.workspace_id::text, k.role, d.role
		FROM member k JOIN member d ON d.workspace_id = k.workspace_id
		WHERE k.user_id = $1 AND d.user_id = $2
		ORDER BY k.workspace_id`, keepID, dropID)
	if err != nil {
		return nil, err
	}
	var changes []RoleChange
	for rows.Next() {
		var c RoleChange
		if err := rows.Scan(&c.WorkspaceID, &c.KeptRole, &c.DroppedRole); err != nil {
			rows.Close()
			return nil, err
		}
		c.FinalRole = higherRole(c.KeptRole, c.DroppedRole)
		changes = append(changes, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, c := range changes {
		if c.FinalRole != c.KeptRole {
			if _, err := tx.Exec(ctx, `UPDATE member SET role = $3 WHERE workspace_id = $1 AND user_id = $2`,
				c.WorkspaceID, keepID, c.FinalRole); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`,
			c.WorkspaceID, dropID); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

type column struct {
	table  string
	column string
}

// uuidColumns lists every uuid column of every table except user.id itself.
// Rewriting *all* of them — foreign keys, polymorphic actor/assignee ids and
// columns added after this was written — is what makes the merge complete;
// random UUIDs never collide across tables, so only real references match.
func uuidColumns(ctx context.Context, tx pgx.Tx) ([]column, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.table_name, c.column_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema()
		  AND t.table_type = 'BASE TABLE'
		  AND c.udt_name = 'uuid'
		  AND NOT (c.table_name = 'user' AND c.column_name = 'id')
		ORDER BY c.table_name, c.column_name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (column, error) {
		var c column
		err := r.Scan(&c.table, &c.column)
		return c, err
	})
}

const uniqueViolation = "23505"

// moveColumn points one column's references at the kept account. It tries
// one bulk UPDATE first; if the kept account already has an identical row
// (unique violation), it goes row by row and removes the dropped account's
// duplicate instead of moving it.
func moveColumn(ctx context.Context, tx pgx.Tx, col column, keepID, dropID string) (ColumnChange, error) {
	change := ColumnChange{Table: col.table, Column: col.column}
	ident := pgx.Identifier{col.table}.Sanitize()
	colIdent := pgx.Identifier{col.column}.Sanitize()

	var count int64
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, ident, colIdent), dropID).Scan(&count); err != nil {
		return change, err
	}
	if count == 0 {
		return change, nil
	}

	if _, err := tx.Exec(ctx, "SAVEPOINT merge_bulk"); err != nil {
		return change, err
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = $1 WHERE %s = $2`, ident, colIdent, colIdent), keepID, dropID)
	if err == nil {
		change.Moved = tag.RowsAffected()
		_, err = tx.Exec(ctx, "RELEASE SAVEPOINT merge_bulk")
		return change, err
	}
	if !isUniqueViolation(err) {
		return change, err
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT merge_bulk"); err != nil {
		return change, err
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT ctid::text FROM %s WHERE %s = $1`, ident, colIdent), dropID)
	if err != nil {
		return change, err
	}
	ctids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return change, err
	}
	for _, ctid := range ctids {
		if _, err := tx.Exec(ctx, "SAVEPOINT merge_row"); err != nil {
			return change, err
		}
		_, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = $1 WHERE ctid = $2::tid`, ident, colIdent), keepID, ctid)
		if err == nil {
			change.Moved++
			if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT merge_row"); err != nil {
				return change, err
			}
			continue
		}
		if !isUniqueViolation(err) {
			return change, err
		}
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT merge_row"); err != nil {
			return change, err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE ctid = $1::tid`, ident), ctid); err != nil {
			return change, err
		}
		change.Removed++
	}
	return change, nil
}

// fillProfile copies what the kept account is missing from the dropped one —
// most often the kept account was created silently and never signed in, while
// the dropped one is the account the person actually used.
func fillProfile(ctx context.Context, tx pgx.Tx, keepID, dropID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE "user" k SET
			avatar_url               = COALESCE(k.avatar_url, d.avatar_url),
			onboarded_at             = COALESCE(k.onboarded_at, d.onboarded_at),
			onboarding_questionnaire = CASE WHEN k.onboarded_at IS NULL THEN d.onboarding_questionnaire ELSE k.onboarding_questionnaire END,
			language                 = COALESCE(k.language, d.language),
			timezone                 = COALESCE(k.timezone, d.timezone),
			profile_description      = COALESCE(NULLIF(k.profile_description, ''), d.profile_description),
			hidden_nav               = CASE WHEN k.hidden_nav_customized_at IS NULL THEN d.hidden_nav ELSE k.hidden_nav END,
			hidden_nav_customized_at = COALESCE(k.hidden_nav_customized_at, d.hidden_nav_customized_at),
			updated_at               = now()
		FROM "user" d
		WHERE k.id = $1 AND d.id = $2`, keepID, dropID)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
