package usermerge

import (
	"context"
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
	Apply         bool
}

// RemoveMemberResult is what happened to one person in one workspace.
type RemoveMemberResult struct {
	Email         string
	WorkspaceSlug string
	Role          string
	Unassigned    int // issues that were assigned to them
	Runtimes      int // agent runtimes they own here (see RemoveMembers)
	Outcome       string
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
	if len(opts.Emails) == 0 {
		return nil, errors.New("give at least one email")
	}
	if opts.WorkspaceSlug != "" && opts.AllWorkspaces {
		return nil, errors.New("--workspace and --all-workspaces are mutually exclusive")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// scope narrows which memberships are in play.
	scope, args := `w.settings ? 'zoho_project_id'`, []any{}
	switch {
	case opts.WorkspaceSlug != "":
		scope, args = `w.slug = $2`, []any{opts.WorkspaceSlug}
	case opts.AllWorkspaces:
		scope = `true`
	}

	var results []RemoveMemberResult
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
		type membership struct{ memberID, wsID, slug, role string }
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
			// An owner is never removed by this tool: a workspace with no
			// owner cannot be administered, and an owner is not the kind of
			// account the migration added by accident.
			if m.role == "owner" {
				results = append(results, RemoveMemberResult{
					Email: email, WorkspaceSlug: m.slug, Role: m.role,
					Outcome: "kept: owner, remove by hand if intended",
				})
				continue
			}

			// A person who owns a runtime has daemons, agents and possibly
			// running tasks hanging off them. Converging all of that is what
			// the server's revokeAndRemoveMember path is for; this tool only
			// undoes a bad import, so it steps back rather than half-doing it.
			var runtimes int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM agent_runtime WHERE workspace_id = $1 AND owner_id = $2`,
				m.wsID, userID).Scan(&runtimes); err != nil {
				return nil, err
			}
			if runtimes > 0 {
				results = append(results, RemoveMemberResult{
					Email: email, WorkspaceSlug: m.slug, Role: m.role, Runtimes: runtimes,
					Outcome: "kept: owns a runtime, remove from the workspace UI instead",
				})
				continue
			}

			tag, err := tx.Exec(ctx, `
				UPDATE issue SET assignee_type = NULL, assignee_id = NULL, updated_at = now()
				 WHERE workspace_id = $1 AND assignee_type = 'member' AND assignee_id = $2`, m.wsID, userID)
			if err != nil {
				return nil, err
			}
			unassigned := int(tag.RowsAffected())

			if _, err := tx.Exec(ctx, `DELETE FROM member WHERE id = $1`, m.memberID); err != nil {
				return nil, err
			}
			results = append(results, RemoveMemberResult{
				Email: email, WorkspaceSlug: m.slug, Role: m.role, Unassigned: unassigned,
				Outcome: fmt.Sprintf("removed (%s)", m.role),
			})
		}
	}

	if opts.Apply {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
	}
	return results, nil
}
