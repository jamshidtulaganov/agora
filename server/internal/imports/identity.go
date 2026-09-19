// Identity mapping (docs/importers-plan.md §3.3).
//
// There is one rule in this file and it is stated as a rule because it is the
// complaint every migration generates:
//
//	AN UNMATCHED AUTHOR IS NEVER WRITTEN AS THE IMPORTING USER.
//
// The Bitrix importer fixed exactly this bug and its comment says so — comments
// "mis-showed every external author as the operator who ran the import (the
// reported bug)". Bitrix fixed it with a conditional; here it is STRUCTURAL:
// ActorResolver has no field, parameter or method that can carry the operator's
// user id, so there is no branch that could reach for it. The test that asserts
// this (TestResolverCannotReachTheOperator) is reflective, not behavioural,
// because a behavioural test only covers the paths someone already thought of.
//
// Resolution order, first hit wins:
//
//  1. user_external_identity(provider=<source>, external_id=<source user id>) —
//     a link a previous run or the user themselves made.
//  2. workspace.settings.import_identity_aliases — the operator's explicit
//     "this address is that person", lifted from bitrix_identity_aliases.
//  3. Email match against an existing MEMBER OF THIS WORKSPACE, case-folded.
//     Members only: matching against all users would let an import bind a
//     stranger's account.
//  4. Provision — create user + member and link the identity. Off unless the
//     operator turned it on for this run; growing the roster silently is a
//     billing surprise and a security surprise at once.
//  5. The source's import identity — a single global attribution user,
//     linear-import@linear.local, following bitrix-import@bitrix.local. The
//     .local domain is undeliverable and can never log in, so it is a pure
//     attribution identity; the real name is preserved on the row's linkage
//     blob and rendered as a provenance line.
package imports

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// IdentityStore is the framework's narrow view of the identity tables.
//
// It speaks in string uuids rather than pgtype/sqlc types on purpose:
// user_external_identity is deliberately outside the generated set (raw pgx in
// handler/external_identity.go), so there are no generated types to reuse and
// inventing some here would be a second source of truth for a table that
// already has one.
type IdentityStore interface {
	// UserIDByExternalIdentity returns the user linked to (provider,
	// externalID), or "" when there is no link. Not an error — "nobody linked
	// this yet" is the normal case on a first import.
	UserIDByExternalIdentity(ctx context.Context, provider, externalID string) (string, error)

	// LinkExternalIdentity binds (provider, externalID) to userID. It must NOT
	// overwrite a link owned by a different user (the link-steal guard); when
	// it refuses, the importer treats the actor as unresolved rather than
	// failing the run.
	LinkExternalIdentity(ctx context.Context, provider, externalID, userID string) error

	// MemberUserIDByEmail returns the user id of a MEMBER OF THIS WORKSPACE
	// whose email case-folds to email, or "" when there is none. Scoped to the
	// workspace by contract: a global match would bind a stranger.
	MemberUserIDByEmail(ctx context.Context, workspaceID, email string) (string, error)

	// EnsureUser returns the id of the user with this email, creating them
	// when absent. Used for provisioning and for the import identity.
	EnsureUser(ctx context.Context, email, name string) (string, error)

	// EnsureMember adds userID to the workspace when they are not already a
	// member. Idempotent.
	EnsureMember(ctx context.Context, workspaceID, userID, role string) error
}

// Resolution is how one source actor was resolved, and how that was reached.
// Via is part of the report: the operator corrects rows they disagree with
// before confirming, and "why did it pick that" is the question they ask first.
type Resolution struct {
	UserID string `json:"user_id"`
	Via    string `json:"via"`
	// Name is the source's own name for the actor, preserved verbatim. When
	// Via is ViaImportIdentity this is the only remaining trace of who wrote
	// the comment, so it goes on the linkage blob.
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Matched reports whether the actor became a real person rather than the
// source's attribution identity.
func (r Resolution) Matched() bool { return r.Via != ViaImportIdentity && r.UserID != "" }

// How a resolution was reached. These strings appear in the dry-run report.
const (
	ViaLinkedIdentity = "linked_identity"
	ViaAlias          = "alias"
	ViaMemberEmail    = "member_email"
	ViaProvisioned    = "provisioned"
	ViaImportIdentity = "import_identity"
	// ViaWouldProvision is a DRY-RUN-only verdict: this actor would be
	// provisioned if the run were real. The dry run writes nothing, so it
	// reports the intent instead of performing it.
	ViaWouldProvision = "would_provision"
)

// ImportIdentityEmail is the dedicated attribution address for a source. The
// .local TLD is undeliverable and email-code login rejects synthetic domains,
// so the account can never be logged into — it exists only to own rows whose
// real author is not an Agora user.
func ImportIdentityEmail(source string) string {
	src := foldKey(source)
	if src == "" {
		src = "import"
	}
	return fmt.Sprintf("%s-import@%s.local", src, src)
}

// ImportIdentityName is what the attribution user is called in the UI:
// the source's name, title-cased ("Linear", "Jira").
func ImportIdentityName(source string) string {
	src := strings.TrimSpace(source)
	if src == "" {
		return "Import"
	}
	return strings.ToUpper(src[:1]) + src[1:]
}

// ActorResolver resolves source actors to Agora users for ONE import.
//
// Note what is not here: no operator id, no "created by", no fallback user
// field. The applier is given this resolver and nothing else with which to
// attribute a row, which is what makes rule §3.3 structural rather than a
// convention someone can forget.
type ActorResolver struct {
	store       IdentityStore
	source      string
	workspaceID string
	aliases     map[string]string
	provision   bool
	// dryRun makes the resolver report what it WOULD do at steps 4 and 5
	// instead of doing it. A dry run writes nothing, including users.
	dryRun bool

	cache          map[string]Resolution
	importIdentity string
}

// ResolverConfig is the resolver's construction. Every field is data the
// operator chose or the workspace holds; none of it is the operator.
type ResolverConfig struct {
	Source      string
	WorkspaceID string
	// Aliases is workspace.settings.import_identity_aliases, already folded.
	Aliases map[string]string
	// Provision turns on step 4. Off by default.
	Provision bool
	// DryRun makes the resolver read-only.
	DryRun bool
}

// NewActorResolver builds a resolver for one import run.
func NewActorResolver(store IdentityStore, cfg ResolverConfig) *ActorResolver {
	return &ActorResolver{
		store:       store,
		source:      cfg.Source,
		workspaceID: cfg.WorkspaceID,
		aliases:     cfg.Aliases,
		provision:   cfg.Provision,
		dryRun:      cfg.DryRun,
		cache:       map[string]Resolution{},
	}
}

// ErrNoIdentityStore is returned when a resolver was built without a store.
// It is a programming error, surfaced rather than nil-panicking mid-import.
var ErrNoIdentityStore = errors.New("imports: resolver has no identity store")

// Resolve maps one source user to an Agora user id, walking the five steps in
// order. It is memoized per run: a 10k-issue import references the same twenty
// people constantly and each one should cost one lookup.
//
// An error from a step is NOT fatal to the run — it degrades to the import
// identity, because losing the author of a comment is better than losing the
// comment. The error is still returned so the caller can record a failure line.
func (r *ActorResolver) Resolve(ctx context.Context, u User) (Resolution, error) {
	if r == nil || r.store == nil {
		return Resolution{}, ErrNoIdentityStore
	}
	key := u.ExternalID
	if key == "" {
		key = foldKey(u.Email)
	}
	if key != "" {
		if cached, ok := r.cache[key]; ok {
			return cached, nil
		}
	}

	res, err := r.resolve(ctx, u)
	if res.UserID == "" && res.Via != ViaWouldProvision {
		// Every path that did not produce a user ends at the import identity —
		// never at the operator, who this type cannot see.
		fallback, ferr := r.ImportIdentity(ctx)
		if ferr != nil {
			return Resolution{Via: ViaImportIdentity, Name: u.Name, Email: u.Email}, errors.Join(err, ferr)
		}
		res = Resolution{UserID: fallback, Via: ViaImportIdentity, Name: u.Name, Email: u.Email}
	}
	if key != "" {
		r.cache[key] = res
	}
	return res, err
}

func (r *ActorResolver) resolve(ctx context.Context, u User) (Resolution, error) {
	// 1. A link a previous run or the user themselves made.
	if u.ExternalID != "" {
		userID, err := r.store.UserIDByExternalIdentity(ctx, r.source, u.ExternalID)
		if err != nil {
			return Resolution{Name: u.Name, Email: u.Email}, err
		}
		if userID != "" {
			return Resolution{UserID: userID, Via: ViaLinkedIdentity, Name: u.Name, Email: u.Email}, nil
		}
	}

	// A bot/integration actor is never a person. It goes straight to the
	// attribution identity rather than matching a human who happens to share
	// the integration's address.
	if u.Bot {
		return Resolution{Name: u.Name, Email: u.Email}, nil
	}

	email := foldKey(u.Email)
	// 2. The operator's explicit "this address is that person".
	if alias, ok := r.aliases[email]; ok && alias != "" {
		email = alias
	}

	// 3. A member of THIS workspace with that address.
	if email != "" {
		userID, err := r.store.MemberUserIDByEmail(ctx, r.workspaceID, email)
		if err != nil {
			return Resolution{Name: u.Name, Email: u.Email}, err
		}
		if userID != "" {
			via := ViaMemberEmail
			if alias, ok := r.aliases[foldKey(u.Email)]; ok && alias != "" {
				via = ViaAlias
			}
			// Record the link so the NEXT run is step 1 rather than step 3 —
			// and so a later email change does not re-orphan the history. A
			// refusal (the id belongs to someone else) is not fatal.
			if !r.dryRun && u.ExternalID != "" {
				if err := r.store.LinkExternalIdentity(ctx, r.source, u.ExternalID, userID); err != nil {
					return Resolution{UserID: userID, Via: via, Name: u.Name, Email: u.Email}, err
				}
			}
			return Resolution{UserID: userID, Via: via, Name: u.Name, Email: u.Email}, nil
		}
	}

	// 4. Provision, only when the operator turned it on for this run.
	if r.provision && email != "" {
		if r.dryRun {
			return Resolution{Via: ViaWouldProvision, Name: u.Name, Email: u.Email}, nil
		}
		userID, err := r.store.EnsureUser(ctx, email, displayName(u))
		if err != nil {
			return Resolution{Name: u.Name, Email: u.Email}, err
		}
		if err := r.store.EnsureMember(ctx, r.workspaceID, userID, "member"); err != nil {
			return Resolution{Name: u.Name, Email: u.Email}, err
		}
		if u.ExternalID != "" {
			if err := r.store.LinkExternalIdentity(ctx, r.source, u.ExternalID, userID); err != nil {
				return Resolution{UserID: userID, Via: ViaProvisioned, Name: u.Name, Email: u.Email}, err
			}
		}
		return Resolution{UserID: userID, Via: ViaProvisioned, Name: u.Name, Email: u.Email}, nil
	}

	// 5. Caller falls through to the import identity.
	return Resolution{Name: u.Name, Email: u.Email}, nil
}

// ImportIdentity returns the source's dedicated attribution user, creating it
// and adding it to the workspace on first use. In a dry run it returns "" with
// no error: nothing is written, and the plan reports the attribution rather
// than performing it.
func (r *ActorResolver) ImportIdentity(ctx context.Context) (string, error) {
	if r.dryRun {
		return "", nil
	}
	if r.importIdentity != "" {
		return r.importIdentity, nil
	}
	if r.store == nil {
		return "", ErrNoIdentityStore
	}
	userID, err := r.store.EnsureUser(ctx, ImportIdentityEmail(r.source), ImportIdentityName(r.source))
	if err != nil {
		return "", err
	}
	if err := r.store.EnsureMember(ctx, r.workspaceID, userID, "member"); err != nil {
		return "", err
	}
	r.importIdentity = userID
	return userID, nil
}

// Preview resolves every user in a bundle without writing anything, for the
// dry-run report. Errors are collected per user rather than aborting: the
// operator needs to see all twenty problems at once, not the first one.
func (r *ActorResolver) Preview(ctx context.Context, users []User) []UserPlan {
	out := make([]UserPlan, 0, len(users))
	for _, u := range users {
		res, err := r.Resolve(ctx, u)
		row := UserPlan{
			ExternalID: u.ExternalID,
			Name:       u.Name,
			Email:      u.Email,
			Via:        res.Via,
			UserID:     res.UserID,
		}
		if row.Via == "" {
			row.Via = ViaImportIdentity
		}
		if err != nil {
			row.Problem = err.Error()
		}
		out = append(out, row)
	}
	return out
}

// UserPlan is one row of the report's identity table: every source user, its
// proposed resolution, and how that was reached. The operator corrects any row
// in conversation before confirming.
type UserPlan struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name,omitempty"`
	Email      string `json:"email,omitempty"`
	Via        string `json:"via"`
	UserID     string `json:"user_id,omitempty"`
	Problem    string `json:"problem,omitempty"`
}

// Matched reports whether this row became a real person.
func (p UserPlan) Matched() bool {
	return p.Via == ViaLinkedIdentity || p.Via == ViaAlias || p.Via == ViaMemberEmail || p.Via == ViaProvisioned
}

func displayName(u User) string {
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	if email := strings.TrimSpace(u.Email); email != "" {
		if at := strings.IndexByte(email, '@'); at > 0 {
			return email[:at]
		}
		return email
	}
	return "Imported user"
}
