package usermerge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RemoveMembersOptions takes people out of workspaces they should never have
// been added to. The Zoho Projects migration used to read a project's whole
// task history for its people, so anyone who ever owned or created a task —
// including employees who left years ago — became a member; this undoes that.
//
// The accounts themselves are kept. A former colleague's name stays on the
// issues and comments they wrote, which is what makes the history readable;
// only their access goes away, and AddMembers can put someone back if they
// turn out to still belong.
type RemoveMembersOptions struct {
	// Emails is the people to remove; an account's own address or any of its
	// login aliases.
	Emails []string
	// WorkspaceSlug limits the removal to one workspace. Empty means every
	// workspace the migration created (settings.zoho_project_id is set),
	// which is the usual cleanup.
	WorkspaceSlug string
	// AllWorkspaces widens it to every workspace, migrated or not.
	AllWorkspaces bool
	// KeepEmails inverts the selection: instead of removing the people named
	// in Emails, remove every member in scope whose address is NOT in this
	// list. It is the roster of people who still work here, so anyone the
	// roster does not vouch for goes — including someone who left before the
	// company started keeping a list, whom an explicit Emails list would miss.
	//
	// Because this removes by absence, a mistake here is much wider than a
	// mistake in Emails: an empty or truncated roster would empty every
	// workspace. RemoveMembers refuses a roster below KeepEmailsMinimum, and
	// a member whose login alias is on the roster is kept (the account may
	// have been merged under a different address).
	KeepEmails []string
	Apply      bool
}

// KeepEmailsMinimum is the smallest roster RemoveMembers will act on. A list
// that arrives empty or half-written is the difference between removing a few
// ex-colleagues and removing everyone, and nothing else in the call would
// look wrong. Ten is arbitrary but far below any real company roster and far
// above a truncated one.
const KeepEmailsMinimum = 10

// RemoveMemberResult is what happened to one person in one workspace.
type RemoveMemberResult struct {
	Email         string
	WorkspaceSlug string
	Role          string
	Unassigned    int // issues that were assigned to them
	Runtimes      int // agent runtimes they own here (see RemoveMembers)
	// Removed is true when this membership was deleted (or, on a dry run,
	// would have been). The caller phrases it: a dry run must not tell the
	// operator that something "was removed" when the transaction is about to
	// roll back. Outcome carries the reason a person was NOT removed.
	Removed bool
	Outcome string
}

// RemoveMembers unassigns a person's issues and then drops their membership,
// both inside one transaction so a removal never leaves an issue assigned to
// someone who can no longer open it.
//
// Unassigning rather than reassigning is deliberate: the tool has no way to
// know who should pick the work up, and an issue with no assignee shows up in
// the team's triage, while an issue assigned to someone who left is invisible.
// The issue keeps its creator, comments and history.
func RemoveMembers(ctx context.Context, db DB, opts RemoveMembersOptions) ([]RemoveMemberResult, error) {
	keepMode := len(opts.KeepEmails) > 0
	switch {
	case keepMode && len(opts.Emails) > 0:
		return nil, errors.New("give either emails to remove or a keep list, not both")
	case !keepMode && len(opts.Emails) == 0:
		return nil, errors.New("give at least one email")
	case keepMode && len(opts.KeepEmails) < KeepEmailsMinimum:
		return nil, fmt.Errorf(
			"the keep list has only %d addresses; refusing, because removing everyone absent from a "+
				"short list would empty the workspaces (minimum %d)", len(opts.KeepEmails), KeepEmailsMinimum)
	}
	if opts.WorkspaceSlug != "" && opts.AllWorkspaces {
		return nil, errors.New("--workspace and --all-workspaces are mutually exclusive")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var results []RemoveMemberResult

	if keepMode {
		// The roster path binds no user id, so its first free placeholder is $1.
		scope, args := workspaceScope(opts, 1)
		results, err = removeMembersNotOnRoster(ctx, tx, opts, scope, args)
		if err != nil {
			return nil, err
		}
		if opts.Apply {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
		}
		return results, nil
	}

	// The email path binds the user id as $1, so the scope starts at $2.
	scope, args := workspaceScope(opts, 2)

	for _, raw := range opts.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		var userID string
		err := tx.QueryRow(ctx, `
			SELECT u.id::text FROM "user" u
			WHERE u.email = $1 OR u.id IN (SELECT user_id FROM user_email_alias WHERE email = $1)
			ORDER BY (u.email = $1) DESC LIMIT 1`, email).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			results = append(results, RemoveMemberResult{Email: email, Outcome: "no account"})
			continue
		}
		if err != nil {
			return nil, err
		}

		rows, err := tx.Query(ctx, `
			SELECT m.id::text, w.id::text, w.slug, m.role
			  FROM member m JOIN workspace w ON w.id = m.workspace_id
			 WHERE m.user_id = $1 AND `+scope+`
			 ORDER BY w.slug`, append([]any{userID}, args...)...)
		if err != nil {
			return nil, err
		}
		var found []membership
		for rows.Next() {
			var m membership
			if err := rows.Scan(&m.memberID, &m.wsID, &m.slug, &m.role); err != nil {
				rows.Close()
				return nil, err
			}
			found = append(found, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(found) == 0 {
			results = append(results, RemoveMemberResult{Email: email, Outcome: "not a member in scope"})
			continue
		}

		for _, m := range found {
			res, err := removeOneMembership(ctx, tx, email, userID, m)
			if err != nil {
				return nil, err
			}
			results = append(results, res)
		}
	}

	if opts.Apply {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// workspaceScope narrows which memberships are in play. next is the first
// placeholder number the caller has not already bound, because the two paths
// bind a different number of arguments before the scope.
func workspaceScope(opts RemoveMembersOptions, next int) (string, []any) {
	switch {
	case opts.WorkspaceSlug != "":
		return fmt.Sprintf("w.slug = $%d", next), []any{opts.WorkspaceSlug}
	case opts.AllWorkspaces:
		return "true", nil
	default:
		return "w.settings ? 'zoho_project_id'", nil
	}
}

// membership is one person's seat in one workspace.
type membership struct{ memberID, wsID, slug, role string }

// removeOneMembership unassigns the person's issues in that workspace and
// deletes the member row. Two cases step back instead of half-doing the job:
// an owner (a workspace with no owner cannot be administered) and anyone who
// owns an agent runtime, whose daemons, agents and running tasks need the
// server's revokeAndRemoveMember cascade rather than raw SQL.
func removeOneMembership(ctx context.Context, tx pgx.Tx, email, userID string, m membership) (RemoveMemberResult, error) {
	if m.role == "owner" {
		return RemoveMemberResult{
			Email: email, WorkspaceSlug: m.slug, Role: m.role,
			Outcome: "kept: owner, remove by hand if intended",
		}, nil
	}

	var runtimes int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM agent_runtime WHERE workspace_id = $1 AND owner_id = $2`,
		m.wsID, userID).Scan(&runtimes); err != nil {
		return RemoveMemberResult{}, err
	}
	if runtimes > 0 {
		return RemoveMemberResult{
			Email: email, WorkspaceSlug: m.slug, Role: m.role, Runtimes: runtimes,
			Outcome: "kept: owns a runtime, remove from the workspace UI instead",
		}, nil
	}

	unassigned, err := unassignIssuesOf(ctx, tx, email, userID, m)
	if err != nil {
		return RemoveMemberResult{}, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM member WHERE id = $1`, m.memberID); err != nil {
		return RemoveMemberResult{}, err
	}
	return RemoveMemberResult{
		Email: email, WorkspaceSlug: m.slug, Role: m.role, Unassigned: unassigned,
		Removed: true,
	}, nil
}

// removeMembersNotOnRoster removes every member in scope whose address the
// roster does not vouch for. It reads the memberships rather than the roster,
// so a person who left before anyone kept a list is caught too.
//
// An account is kept when its own address OR any of its login aliases is on
// the roster: the account-merge tool folds a person's second address into one
// account, and the surviving address is not always the one Zoho knows.
func removeMembersNotOnRoster(ctx context.Context, tx pgx.Tx, opts RemoveMembersOptions, scope string, args []any) ([]RemoveMemberResult, error) {
	keep := make(map[string]bool, len(opts.KeepEmails))
	for _, raw := range opts.KeepEmails {
		if e := strings.ToLower(strings.TrimSpace(raw)); e != "" {
			keep[e] = true
		}
	}
	if len(keep) < KeepEmailsMinimum {
		return nil, fmt.Errorf("the keep list has only %d usable addresses (minimum %d)", len(keep), KeepEmailsMinimum)
	}

	rows, err := tx.Query(ctx, `
		SELECT m.id::text, w.id::text, w.slug, m.role, u.id::text, u.email,
		       COALESCE(ARRAY(SELECT email FROM user_email_alias WHERE user_id = u.id), '{}')
		  FROM member m
		  JOIN workspace w ON w.id = m.workspace_id
		  JOIN "user" u ON u.id = m.user_id
		 WHERE `+scope+`
		 ORDER BY w.slug, u.email`, args...)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		m       membership
		userID  string
		email   string
		aliases []string
	}
	var all []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.m.memberID, &c.m.wsID, &c.m.slug, &c.m.role, &c.userID, &c.email, &c.aliases); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var results []RemoveMemberResult
	for _, c := range all {
		email := strings.ToLower(strings.TrimSpace(c.email))
		if keep[email] {
			continue
		}
		vouched := false
		for _, a := range c.aliases {
			if keep[strings.ToLower(strings.TrimSpace(a))] {
				vouched = true
				break
			}
		}
		if vouched {
			continue
		}
		res, err := removeOneMembership(ctx, tx, email, c.userID, c.m)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

// unassignIssuesOf clears the person's assignments in one workspace and leaves
// a trail of who held each issue.
//
// Nulling the assignee alone loses the answer to "whose was this?" — the
// column is the only record, and a tool writing raw SQL bypasses the
// application's activity listener, so the change would not even appear in the
// issue's timeline. For work handed over because someone left, that answer is
// the useful part: it is what lets whoever triages the issue next find the
// context, and it is what makes the removal undoable.
//
// So each issue keeps metadata.unassigned_from (the address, readable in the
// Metadata panel without a join to a member row that no longer exists) and
// gains an activity_log row in the same shape the application writes for an
// assignee change, actor 'system', so the timeline reads normally.
func unassignIssuesOf(ctx context.Context, tx pgx.Tx, email, userID string, m membership) (int, error) {
	rows, err := tx.Query(ctx, `
		UPDATE issue
		   SET assignee_type = NULL,
		       assignee_id   = NULL,
		       metadata      = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
		                         'unassigned_from', $3::text,
		                         'unassigned_reason', 'member removed from workspace'),
		       updated_at    = now()
		 WHERE workspace_id = $1 AND assignee_type = 'member' AND assignee_id = $2
		 RETURNING id`, m.wsID, userID, email)
	if err != nil {
		return 0, err
	}
	var issueIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		issueIDs = append(issueIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(issueIDs) == 0 {
		return 0, nil
	}

	// Same details keys the application's assignee_changed listener writes
	// (from_type/from_id, no to_*), so existing timeline readers need no
	// special case for these rows.
	details, err := json.Marshal(map[string]string{
		"from_type":  "member",
		"from_id":    userID,
		"from_email": email,
		"reason":     "member removed from workspace",
	})
	if err != nil {
		return 0, err
	}
	for _, id := range issueIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity_log (workspace_id, issue_id, actor_type, action, details)
			VALUES ($1, $2, 'system', 'assignee_changed', $3)`, m.wsID, id, details); err != nil {
			return 0, err
		}
	}
	return len(issueIDs), nil
}
