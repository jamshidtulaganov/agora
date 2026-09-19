package imports_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/importmem"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

func buildPlan(t *testing.T, store *importmem.Store, bundle *imports.Bundle, existing map[string]imports.ExistingIssue) *imports.Plan {
	t.Helper()
	resolver := imports.NewActorResolver(store, imports.ResolverConfig{
		Source: imports.SourceLinear, WorkspaceID: testWorkspaceID, DryRun: true,
	})
	mapping := imports.NewMapping(imports.SourceLinear, linearLikeDefaults(), imports.Overrides{})
	plan, err := imports.BuildPlan(context.Background(), bundle, mapping, resolver, existing, imports.Scope{}, imports.Limits{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return plan
}

// A fresh workspace: everything is a create, and the plan writes nothing.
func TestPlanOnAFreshWorkspaceIsAllCreates(t *testing.T) {
	store := importmem.New()
	plan := buildPlan(t, store, fixtureBundle(), map[string]imports.ExistingIssue{})

	if plan.Issues.Create != 4 || plan.Issues.Update != 0 || plan.Issues.Total != 4 {
		t.Fatalf("issues = %+v, want 4 creates", plan.Issues)
	}
	if plan.Comments.Create != 2 {
		t.Errorf("comments = %+v, want 2 creates", plan.Comments)
	}
	if len(plan.Containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(plan.Containers))
	}
	for _, c := range plan.Containers {
		if c.Action != "create_project" {
			t.Errorf("container %q action = %q, want create_project", c.Name, c.Action)
		}
	}
	if !plan.Exact {
		t.Error("a Linear plan with nothing truncated should report exact counts")
	}
	// The dry run is read-only, top to bottom.
	if len(store.Issues) != 0 || len(store.Users) != 0 || len(store.Calls) != 0 {
		t.Errorf("BuildPlan wrote something: %d issues, %d users, calls=%v",
			len(store.Issues), len(store.Users), store.Calls)
	}
}

// "Import now, import again after we finish the sprint in Linear" is a
// supported workflow, and the plan has to show it as updates rather than
// letting the operator fear 243 duplicates.
func TestPlanShowsTheCreateUpdateSplit(t *testing.T) {
	store := importmem.New()
	existing := map[string]imports.ExistingIssue{
		"iss-1": {ID: "aaaaaaaa-0000-4000-8000-000000000001", Number: 12},
		"iss-2": {ID: "aaaaaaaa-0000-4000-8000-000000000002", Number: 13},
	}
	plan := buildPlan(t, store, fixtureBundle(), existing)

	if plan.Issues.Update != 2 || plan.Issues.Create != 2 {
		t.Fatalf("issues = %+v, want 2 updates and 2 creates", plan.Issues)
	}
	// The two comments hang off an already-linked issue, so they are upserts.
	if plan.Comments.Update != 2 || plan.Comments.Create != 0 {
		t.Errorf("comments = %+v, want 2 updates", plan.Comments)
	}
}

// Every unmatched user is named in the report, and the report says where their
// rows will land — which is never the operator.
func TestPlanNamesUnmatchedUsersAndPromisesTheyAreNotTheOperator(t *testing.T) {
	store := importmem.New()
	ws := util.MustParseUUID(testWorkspaceID)
	store.AddMember(ws, "Kim Ryu", "kim@acme.io", "member")

	plan := buildPlan(t, store, fixtureBundle(), map[string]imports.ExistingIssue{})
	if plan.UnmatchedUsers != 1 {
		t.Fatalf("unmatched users = %d, want 1 (Dana Wu)", plan.UnmatchedUsers)
	}
	var dana imports.UserPlan
	for _, u := range plan.Users {
		if u.ExternalID == "lin-ghost" {
			dana = u
		}
	}
	if dana.Via != imports.ViaImportIdentity {
		t.Errorf("Dana's proposed resolution = %q, want the import identity", dana.Via)
	}
	if dana.Name != "Dana Wu" {
		t.Errorf("the report lost the source name: %+v", dana)
	}
	if !warningContains(plan.Warnings, "never to you") {
		t.Errorf("the plan does not promise unmatched authors are not the operator: %v", plan.Warnings)
	}
}

// A state nothing claimed is a line in the report, not a lost issue.
func TestPlanListsUnmappedStatuses(t *testing.T) {
	store := importmem.New()
	bundle := fixtureBundle()
	bundle.States = append(bundle.States, imports.State{ExternalID: "st-odd", Name: "Waiting on customer", Category: "mystery"})
	bundle.Issues[0].StateID = "st-odd"

	plan := buildPlan(t, store, bundle, map[string]imports.ExistingIssue{})
	if len(plan.UnmappedStatuses) != 1 || plan.UnmappedStatuses[0].Name != "Waiting on customer" {
		t.Fatalf("unmapped statuses = %+v", plan.UnmappedStatuses)
	}
	if plan.UnmappedStatuses[0].FellBackTo != imports.StatusTodo {
		t.Errorf("fell back to %q, want todo", plan.UnmappedStatuses[0].FellBackTo)
	}
	if !warningContains(plan.Warnings, "assign them before confirming") {
		t.Errorf("no operator-facing warning for unmapped states: %v", plan.Warnings)
	}
}

// Attachments are reported in BYTES with explicit budgets, and the report
// carries the one warning the operator cannot discover later.
func TestPlanReportsAttachmentBytesAndTheHonestWarning(t *testing.T) {
	store := importmem.New()
	bundle := fixtureBundle()
	bundle.Issues[0].Attachments = []imports.Attachment{
		{ExternalID: "att-1", Title: "screenshot.png", Size: 1 << 20},
		{ExternalID: "att-2", Title: "recording.mov", Size: 500 << 20}, // over the per-file cap
	}
	resolver := imports.NewActorResolver(store, imports.ResolverConfig{Source: imports.SourceLinear, WorkspaceID: testWorkspaceID, DryRun: true})
	mapping := imports.NewMapping(imports.SourceLinear, linearLikeDefaults(), imports.Overrides{})
	plan, err := imports.BuildPlan(context.Background(), bundle, mapping, resolver,
		map[string]imports.ExistingIssue{}, imports.Scope{}, imports.Limits{MaxAttachmentBytes: 25 << 20})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	if plan.Attachments.Count != 1 || plan.Attachments.Bytes != 1<<20 {
		t.Fatalf("attachments = %+v, want 1 file of 1 MiB", plan.Attachments)
	}
	if plan.Attachments.Skipped != 1 {
		t.Fatalf("skipped = %d, want the over-cap file listed", plan.Attachments.Skipped)
	}
	if len(plan.Attachments.SkippedReasons) == 0 {
		t.Error("an over-budget file was skipped with no reason; it must be listed, never silently truncated")
	}
	if !warningContains(plan.Warnings, "becomes unreachable") {
		t.Errorf("the plan omits the subscription-cancellation warning: %v", plan.Warnings)
	}
}

// A truncated fetch makes every headline number an estimate, whatever else the
// adapter claims. A report that says "240" when the token saw 190 is the
// failure mode that destroys trust in the whole product.
func TestTruncationMakesThePlanInexact(t *testing.T) {
	store := importmem.New()
	bundle := fixtureBundle()
	bundle.AddTruncated("issues", 50, "the credential cannot see team SEC")

	plan := buildPlan(t, store, bundle, map[string]imports.ExistingIssue{})
	if plan.Exact {
		t.Fatal("a plan with truncated entities reported exact counts")
	}
	if plan.Truncated["issues"] != 50 {
		t.Errorf("truncated = %v, want 50 issues", plan.Truncated)
	}
	if !warningContains(plan.Warnings, "cannot see team SEC") {
		t.Errorf("the truncation reason was lost: %v", plan.Warnings)
	}
}

// Relations that degrade, and relations that point outside the scope, are both
// reported — a cut edge is not an error the operator can act on, but they
// should know.
func TestPlanReportsRelationDegradeAndDangling(t *testing.T) {
	store := importmem.New()
	plan := buildPlan(t, store, fixtureBundle(), map[string]imports.ExistingIssue{})

	if plan.Relations.Total != 2 {
		t.Fatalf("relations = %+v, want 2", plan.Relations)
	}
	if plan.Relations.Degraded != 1 {
		t.Errorf("degraded = %d, want 1 (linear's `similar` has no Agora equivalent)", plan.Relations.Degraded)
	}
	if plan.Relations.Dangling != 1 {
		t.Errorf("dangling = %d, want 1 (the target is outside this import)", plan.Relations.Dangling)
	}
}

func warningContains(warnings []string, fragment string) bool {
	for _, w := range warnings {
		if strings.Contains(w, fragment) {
			return true
		}
	}
	return false
}
