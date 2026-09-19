package imports_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/importmem"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

const testWorkspaceID = "11111111-1111-4111-8111-111111111111"

func newResolver(t *testing.T, store *importmem.Store, cfg imports.ResolverConfig) *imports.ActorResolver {
	t.Helper()
	if cfg.Source == "" {
		cfg.Source = imports.SourceLinear
	}
	if cfg.WorkspaceID == "" {
		cfg.WorkspaceID = testWorkspaceID
	}
	return imports.NewActorResolver(store, cfg)
}

// Step 1: a link a previous run (or the user) made wins over everything.
func TestResolveUsesAnExistingIdentityLink(t *testing.T) {
	store := importmem.New()
	ws := util.MustParseUUID(testWorkspaceID)
	dana := store.AddMember(ws, "Dana Wu", "dana@acme.io", "member")
	if err := store.LinkExternalIdentity(context.Background(), imports.SourceLinear, "lin-dana", dana); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	r := newResolver(t, store, imports.ResolverConfig{})
	// Deliberately a DIFFERENT email on the source side: the link, not the
	// address, is the authority once one exists.
	got, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-dana", Email: "d.wu@personal.example", Name: "Dana Wu"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.UserID != dana || got.Via != imports.ViaLinkedIdentity {
		t.Fatalf("Resolve = %+v, want %s via %s", got, dana, imports.ViaLinkedIdentity)
	}
}

// Steps 2 and 3: the alias override, then a case-folded email match against a
// MEMBER OF THIS WORKSPACE. A match also records the link, so the next run is
// step 1.
func TestResolveMatchesMemberEmailAndAlias(t *testing.T) {
	store := importmem.New()
	ws := util.MustParseUUID(testWorkspaceID)
	kim := store.AddMember(ws, "Kim Ryu", "kim@acme.io", "member")

	r := newResolver(t, store, imports.ResolverConfig{
		Aliases: map[string]string{"kim.ryu@oldcorp.example": "kim@acme.io"},
	})

	folded, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-kim", Email: "KIM@ACME.IO", Name: "Kim Ryu"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if folded.UserID != kim || folded.Via != imports.ViaMemberEmail {
		t.Fatalf("case-folded match = %+v, want %s via %s", folded, kim, imports.ViaMemberEmail)
	}
	// The link was recorded, which is what makes the second run cheap and
	// survives a later email change.
	linked, _ := store.UserIDByExternalIdentity(context.Background(), imports.SourceLinear, "lin-kim")
	if linked != kim {
		t.Errorf("identity link after a match = %q, want %q", linked, kim)
	}

	aliased, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-kim-old", Email: "kim.ryu@oldcorp.example", Name: "Kim Ryu"})
	if err != nil {
		t.Fatalf("Resolve (alias): %v", err)
	}
	if aliased.UserID != kim || aliased.Via != imports.ViaAlias {
		t.Fatalf("alias match = %+v, want %s via %s", aliased, kim, imports.ViaAlias)
	}
}

// A stranger with an Agora account who is NOT a member of this workspace must
// not be bound by an import.
func TestResolveWillNotBindANonMember(t *testing.T) {
	store := importmem.New()
	// A user exists globally but is a member of some other workspace.
	otherWS := util.MustParseUUID("22222222-2222-4222-8222-222222222222")
	stranger := store.AddMember(otherWS, "Someone Else", "dana@acme.io", "member")

	r := newResolver(t, store, imports.ResolverConfig{})
	got, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-dana", Email: "dana@acme.io", Name: "Dana Wu"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.UserID == stranger {
		t.Fatal("an import bound a user who is not a member of the target workspace")
	}
	if got.Via != imports.ViaImportIdentity {
		t.Fatalf("unmatched stranger resolved via %q, want the import identity", got.Via)
	}
}

// Step 4 is OFF by default: silently growing the member roster during an import
// is a billing surprise and a security surprise at once.
func TestProvisioningIsOffByDefault(t *testing.T) {
	store := importmem.New()
	r := newResolver(t, store, imports.ResolverConfig{})
	got, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-new", Email: "new@acme.io", Name: "New Person"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Via != imports.ViaImportIdentity {
		t.Fatalf("an unmatched user was resolved via %q with provisioning off", got.Via)
	}
	if _, ok := store.UserByID(got.UserID); !ok {
		t.Fatal("the import identity user was not created")
	}
	if user, _ := store.UserByID(got.UserID); user.Email != imports.ImportIdentityEmail(imports.SourceLinear) {
		t.Fatalf("fallback user email = %q, want %q", user.Email, imports.ImportIdentityEmail(imports.SourceLinear))
	}

	withProvisioning := newResolver(t, store, imports.ResolverConfig{Provision: true})
	provisioned, err := withProvisioning.Resolve(context.Background(), imports.User{ExternalID: "lin-new", Email: "new@acme.io", Name: "New Person"})
	if err != nil {
		t.Fatalf("Resolve (provision): %v", err)
	}
	if provisioned.Via != imports.ViaProvisioned {
		t.Fatalf("with provisioning on, via = %q, want %q", provisioned.Via, imports.ViaProvisioned)
	}
	if roles := store.Members[testWorkspaceID]; roles[provisioned.UserID] != "member" {
		t.Error("a provisioned user was not added to the workspace")
	}
}

// THE RULE. An unmatched author is never the importing user. Asserted twice:
// behaviourally, and structurally — the resolver has no field that could hold
// an operator id, so there is no branch a future edit could reach it from.
func TestUnmatchedAuthorIsNeverTheOperator(t *testing.T) {
	store := importmem.New()
	ws := util.MustParseUUID(testWorkspaceID)
	operator := store.AddMember(ws, "Jamshid (the operator)", "operator@acme.io", "owner")

	r := newResolver(t, store, imports.ResolverConfig{})
	got, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-ghost", Email: "ghost@gone.example", Name: "Ghost Author"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.UserID == operator {
		t.Fatal("an unmatched author was attributed to the operator — the exact bug the Bitrix importer's comment names")
	}
	if got.Via != imports.ViaImportIdentity {
		t.Fatalf("via = %q, want %q", got.Via, imports.ViaImportIdentity)
	}
	// The real name survives, so provenance is not lost by the attribution.
	if got.Name != "Ghost Author" {
		t.Errorf("preserved name = %q, want the source's own", got.Name)
	}

	// Structural half: no field of the resolver can carry a user id supplied
	// by the caller other than through the identity store itself.
	typ := reflect.TypeOf(imports.ActorResolver{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "operator") || strings.Contains(name, "actor") ||
			strings.Contains(name, "createdby") || strings.Contains(name, "fallbackuser") {
			t.Errorf("ActorResolver has field %q; the operator must be unreachable from the resolver by construction", typ.Field(i).Name)
		}
	}
	cfgType := reflect.TypeOf(imports.ResolverConfig{})
	for i := 0; i < cfgType.NumField(); i++ {
		name := strings.ToLower(cfgType.Field(i).Name)
		if strings.Contains(name, "operator") || strings.Contains(name, "user") || strings.Contains(name, "createdby") {
			t.Errorf("ResolverConfig has field %q; nothing may hand the resolver a user to fall back to", cfgType.Field(i).Name)
		}
	}
}

// The attribution identity is one global, undeliverable account per source,
// following bitrix-import@bitrix.local.
func TestImportIdentityIsPerSourceAndUndeliverable(t *testing.T) {
	if got := imports.ImportIdentityEmail(imports.SourceLinear); got != "linear-import@linear.local" {
		t.Errorf("linear import identity = %q", got)
	}
	if got := imports.ImportIdentityEmail(imports.SourceJira); got != "jira-import@jira.local" {
		t.Errorf("jira import identity = %q", got)
	}
	if got := imports.ImportIdentityName(imports.SourceLinear); got != "Linear" {
		t.Errorf("linear import identity name = %q", got)
	}
	// Two runs share one account rather than making a new ghost each time.
	store := importmem.New()
	first := newResolver(t, store, imports.ResolverConfig{})
	second := newResolver(t, store, imports.ResolverConfig{})
	a, err := first.ImportIdentity(context.Background())
	if err != nil {
		t.Fatalf("ImportIdentity: %v", err)
	}
	b, err := second.ImportIdentity(context.Background())
	if err != nil {
		t.Fatalf("ImportIdentity (second run): %v", err)
	}
	if a != b {
		t.Errorf("two runs produced two attribution users (%s, %s)", a, b)
	}
}

// A dry run writes nothing at all — not even the users it would create.
func TestDryRunResolverWritesNothing(t *testing.T) {
	store := importmem.New()
	r := newResolver(t, store, imports.ResolverConfig{Provision: true, DryRun: true})

	rows := r.Preview(context.Background(), []imports.User{
		{ExternalID: "lin-new", Email: "new@acme.io", Name: "New Person"},
		{ExternalID: "lin-ghost", Name: "No Email"},
	})
	if len(rows) != 2 {
		t.Fatalf("Preview returned %d rows, want 2", len(rows))
	}
	if rows[0].Via != imports.ViaWouldProvision {
		t.Errorf("row 0 via = %q, want %q", rows[0].Via, imports.ViaWouldProvision)
	}
	if rows[1].Via != imports.ViaImportIdentity {
		t.Errorf("row 1 via = %q, want %q", rows[1].Via, imports.ViaImportIdentity)
	}
	for name, count := range store.Calls {
		switch name {
		case "EnsureUser", "EnsureMember", "LinkExternalIdentity":
			t.Errorf("a dry run called %s %d times; it must write nothing", name, count)
		}
	}
	if len(store.Users) != 0 {
		t.Errorf("a dry run created %d users", len(store.Users))
	}
}

// A bot/integration actor goes straight to attribution rather than matching a
// human who happens to share the integration's address.
func TestBotActorsDoNotMatchHumans(t *testing.T) {
	store := importmem.New()
	ws := util.MustParseUUID(testWorkspaceID)
	human := store.AddMember(ws, "Deploy Bot Owner", "bot@acme.io", "member")

	r := newResolver(t, store, imports.ResolverConfig{})
	got, err := r.Resolve(context.Background(), imports.User{ExternalID: "lin-bot", Email: "bot@acme.io", Name: "Linear Bot", Bot: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.UserID == human {
		t.Fatal("a bot actor was matched to a human member")
	}
	if got.Via != imports.ViaImportIdentity {
		t.Fatalf("bot via = %q, want the import identity", got.Via)
	}
}
