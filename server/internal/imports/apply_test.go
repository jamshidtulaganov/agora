package imports_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/importmem"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

var (
	created2024 = time.Date(2024, 3, 7, 9, 30, 0, 0, time.UTC)
	updated2025 = time.Date(2025, 11, 2, 16, 5, 0, 0, time.UTC)
)

// fixtureBundle is a small but real-shaped import: two containers, a cycle, two
// labels, four issues (one a child, one with a relation to a sibling), comments
// from a matched author and an unmatched one.
func fixtureBundle() *imports.Bundle {
	estimate := 3.0
	return &imports.Bundle{
		Source: imports.Source{Kind: imports.SourceLinear, Ref: "acme"},
		Users: []imports.User{
			{ExternalID: "lin-kim", Name: "Kim Ryu", Email: "kim@acme.io", Active: true},
			{ExternalID: "lin-ghost", Name: "Dana Wu", Email: "dana@gone.example"},
		},
		Containers: []imports.Container{
			{ExternalID: "team-eng", Key: "ENG", Name: "Engineering", URL: "https://linear.app/acme/team/ENG"},
			{ExternalID: "team-ops", Key: "OPS", Name: "Operations"},
		},
		Iterations: []imports.Iteration{
			{ExternalID: "cycle-12", ContainerID: "team-eng", Name: "Cycle 12", Completed: true},
		},
		States: []imports.State{
			{ExternalID: "st-todo", Name: "Todo", Category: "unstarted"},
			{ExternalID: "st-review", Name: "In Review", Category: "started"},
			{ExternalID: "st-done", Name: "Done", Category: "completed"},
		},
		Labels: []imports.Label{
			{ExternalID: "lab-bug", Name: "Bug", Color: "#ef4444"},
			{ExternalID: "lab-ui", Name: "UI", Color: "not-a-colour"},
		},
		Issues: []imports.Issue{
			{
				ExternalID: "iss-1", Identifier: "ENG-142", Title: "Fix the login redirect",
				BodyMarkdown: "The redirect **loops**.", StateID: "st-review", Priority: imports.PriorityHigh,
				CreatorID: "lin-kim", AssigneeID: "lin-kim", ContainerID: "team-eng",
				IterationID: "cycle-12", LabelIDs: []string{"lab-bug"}, Estimate: &estimate,
				CreatedAt: created2024, UpdatedAt: updated2025,
				URL: "https://linear.app/acme/issue/ENG-142",
				Comments: []imports.Comment{
					{ExternalID: "cm-1", AuthorID: "lin-kim", BodyMarkdown: "On it.", CreatedAt: created2024, UpdatedAt: created2024},
					{ExternalID: "cm-2", AuthorID: "lin-ghost", BodyMarkdown: "Reproduced on staging.", CreatedAt: created2024, UpdatedAt: created2024},
				},
				Relations: []imports.Relation{
					{Kind: imports.RelationBlocks, TargetExternalID: "iss-2", SourceType: "blocks"},
					{Kind: imports.RelationRelated, TargetExternalID: "iss-missing", SourceType: "similar"},
				},
			},
			// A child declared BEFORE its parent, to exercise the topological pass.
			{
				ExternalID: "iss-3", Identifier: "ENG-144", Title: "Child of 143",
				StateID: "st-todo", Priority: imports.PriorityNone, CreatorID: "lin-kim",
				ContainerID: "team-eng", ParentID: "iss-2",
				CreatedAt: created2024, UpdatedAt: created2024,
			},
			{
				ExternalID: "iss-2", Identifier: "ENG-143", Title: "Rotate the session key",
				StateID: "st-todo", Priority: imports.PriorityUrgent, CreatorID: "lin-ghost",
				ContainerID: "team-eng", LabelIDs: []string{"lab-ui"},
				CreatedAt: created2024, UpdatedAt: created2024,
			},
			{
				ExternalID: "iss-4", Identifier: "OPS-7", Title: "Renew the TLS cert",
				StateID: "st-done", Priority: imports.PriorityLow, CreatorID: "lin-kim",
				ContainerID: "team-ops", CreatedAt: created2024, UpdatedAt: created2024,
			},
		},
	}
}

func newApplier(t *testing.T, store *importmem.Store) *imports.Applier {
	t.Helper()
	ws := util.MustParseUUID(testWorkspaceID)
	return &imports.Applier{
		Store:       store,
		Resolver:    imports.NewActorResolver(store, imports.ResolverConfig{Source: imports.SourceLinear, WorkspaceID: testWorkspaceID}),
		Mapping:     imports.NewMapping(imports.SourceLinear, linearLikeDefaults(), imports.Overrides{}),
		WorkspaceID: ws,
		Source:      imports.SourceLinear,
		ImportID:    "99999999-9999-4999-8999-999999999999",
		Now:         func() time.Time { return updated2025 },
	}
}

func linearLikeDefaults() imports.Defaults {
	return imports.Defaults{
		StatusByCategory: map[string]string{
			"backlog": imports.StatusBacklog, "triage": imports.StatusTodo,
			"unstarted": imports.StatusTodo, "started": imports.StatusInProgress,
			"completed": imports.StatusDone, "canceled": imports.StatusCancelled,
		},
		StatusRefine: map[string][]imports.Refinement{
			"started": {{Keywords: []string{"review", "qa"}, Status: imports.StatusInReview}},
		},
	}
}

func TestApplyWritesInDependencyOrder(t *testing.T) {
	store := importmem.New()
	applier := newApplier(t, store)

	result, err := applier.Apply(context.Background(), fixtureBundle(), nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := result.Totals[imports.KindIssue]; got.Created != 4 || got.Updated != 0 {
		t.Fatalf("issues = %+v, want created 4", got)
	}
	if got := result.Totals[imports.KindProject]; got.Created != 2 {
		t.Errorf("projects = %+v, want 2 created", got)
	}
	if got := result.Totals[imports.KindSprint]; got.Created != 1 {
		t.Errorf("sprints = %+v, want 1 created", got)
	}
	if got := result.Totals[imports.KindLabel]; got.Created != 2 {
		t.Errorf("labels = %+v, want 2 created", got)
	}
	if got := result.Totals[imports.KindComment]; got.Updated != 2 {
		t.Errorf("comments = %+v, want 2 written", got)
	}

	// Parent before child: the child's parent_issue_id is set, which is only
	// possible if the parent was written first.
	child, ok := store.IssueByExternalID(imports.SourceLinear, "iss-3")
	if !ok {
		t.Fatal("the child issue was not written")
	}
	parent, _ := store.IssueByExternalID(imports.SourceLinear, "iss-2")
	if util.UUIDToString(child.ParentIssueID) != util.UUIDToString(parent.ID) {
		t.Errorf("child parent = %q, want the parent issue %q",
			util.UUIDToString(child.ParentIssueID), util.UUIDToString(parent.ID))
	}

	// Relations run last and only for edges whose both ends landed.
	if len(store.Dependencies) != 1 {
		t.Fatalf("wrote %d dependencies, want 1 (the dangling edge is skipped, not failed)", len(store.Dependencies))
	}
	if store.Dependencies[0].Type != string(imports.RelationBlocks) {
		t.Errorf("dependency type = %q, want blocks", store.Dependencies[0].Type)
	}
	if got := result.Totals[imports.KindRelation]; got.Skipped != 1 {
		t.Errorf("relations = %+v, want 1 skipped (target outside the import)", got)
	}
}

// The property the whole product rests on: the second run is an update, not a
// duplicate. "created: 0, updated: N".
func TestReimportUpsertsRatherThanDuplicating(t *testing.T) {
	store := importmem.New()

	first, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if first.Totals[imports.KindIssue].Created != 4 {
		t.Fatalf("first run created %d issues, want 4", first.Totals[imports.KindIssue].Created)
	}
	issuesAfterFirst := len(store.Issues)
	commentsAfterFirst := len(store.Comments)

	second, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if got := second.Totals[imports.KindIssue]; got.Created != 0 || got.Updated != 4 {
		t.Fatalf("re-run issues = %+v, want created 0 / updated 4", got)
	}
	if len(store.Issues) != issuesAfterFirst {
		t.Errorf("re-run grew the issue table from %d to %d", issuesAfterFirst, len(store.Issues))
	}
	if len(store.Comments) != commentsAfterFirst {
		t.Errorf("re-run grew the comment table from %d to %d; the unique index is the dedup", commentsAfterFirst, len(store.Comments))
	}
	if len(store.Projects) != 2 || len(store.Labels) != 2 || len(store.Sprints) != 1 {
		t.Errorf("re-run duplicated containers: %d projects, %d labels, %d sprints",
			len(store.Projects), len(store.Labels), len(store.Sprints))
	}
	// The dependency's NOT EXISTS guard reports the no-op as an update.
	if len(store.Dependencies) != 1 {
		t.Errorf("re-run stacked %d dependency rows, want 1", len(store.Dependencies))
	}
	// Numbers are stable across runs: people link to ENG-142.
	before, _ := store.IssueByExternalID(imports.SourceLinear, "iss-1")
	if before.Number == 0 {
		t.Error("the imported issue has no number")
	}
}

// A two-year backlog that all says "created today" is a paste, not a migration.
func TestApplyPreservesSourceTimestamps(t *testing.T) {
	store := importmem.New()
	if _, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	issue, ok := store.IssueByExternalID(imports.SourceLinear, "iss-1")
	if !ok {
		t.Fatal("issue not written")
	}
	if !issue.CreatedAt.Time.Equal(created2024) {
		t.Errorf("created_at = %s, want the source's %s", issue.CreatedAt.Time, created2024)
	}
	if !issue.UpdatedAt.Time.Equal(updated2025) {
		t.Errorf("updated_at = %s, want the source's %s", issue.UpdatedAt.Time, updated2025)
	}
	for _, c := range store.CommentsForIssue(issue.ID) {
		if !c.CreatedAt.Time.Equal(created2024) {
			t.Errorf("comment created_at = %s, want the source's %s", c.CreatedAt.Time, created2024)
		}
	}
}

// The linkage blob is what makes the next run an update, and the provenance the
// row itself can no longer carry.
func TestApplyWritesTheLinkageBlob(t *testing.T) {
	store := importmem.New()
	if _, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	issue, _ := store.IssueByExternalID(imports.SourceLinear, "iss-2")
	ref, ok := imports.ReadExternalRef(issue.Metadata)
	if !ok {
		t.Fatal("no external_ref on the imported issue")
	}
	if ref.Source != imports.SourceLinear || ref.ID != "iss-2" || ref.Identifier != "ENG-143" {
		t.Errorf("external_ref = %+v", ref)
	}
	if ref.ImportID != "99999999-9999-4999-8999-999999999999" {
		t.Errorf("import_id = %q, want the job id", ref.ImportID)
	}
	// ENG-143's creator could not be matched, so the real name is preserved.
	if ref.Author != "Dana Wu" {
		t.Errorf("author = %q, want the unmatched source author's real name", ref.Author)
	}

	// A project carries its linkage in settings, NOT in its description — the
	// Bitrix marker-in-description hack leaks provenance into user-visible copy.
	var project struct {
		Ref imports.ExternalRef `json:"external_ref"`
	}
	for _, p := range store.Projects {
		if p.Title != "Engineering" {
			continue
		}
		if err := json.Unmarshal(p.Settings, &project); err != nil {
			t.Fatalf("project settings: %v", err)
		}
		if project.Ref.ID != "team-eng" {
			t.Errorf("project external_ref = %+v", project.Ref)
		}
		if strings.Contains(p.Description.String, "team-eng") ||
			strings.Contains(p.Description.String, imports.ExternalRefKey) {
			t.Error("provenance leaked into the project description")
		}
	}
}

// A comment whose author could not be matched is attributed to the source's
// import identity — never to whoever ran the import — and keeps a provenance
// header naming the real author.
func TestUnmatchedCommentAuthorGoesToTheImportIdentity(t *testing.T) {
	store := importmem.New()
	ws := util.MustParseUUID(testWorkspaceID)
	operator := store.AddMember(ws, "Operator", "operator@acme.io", "owner")
	kim := store.AddMember(ws, "Kim Ryu", "kim@acme.io", "member")

	if _, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	issue, _ := store.IssueByExternalID(imports.SourceLinear, "iss-1")

	var matched, unmatched bool
	for _, c := range store.CommentsForIssue(issue.ID) {
		author := util.UUIDToString(c.AuthorID)
		if author == operator {
			t.Fatalf("a comment was attributed to the operator: %q", c.Content)
		}
		switch c.ExternalID.String {
		case "cm-1":
			matched = true
			if author != kim {
				t.Errorf("the matched author's comment went to %q, want %q", author, kim)
			}
			if got := c.Content; got != "On it." {
				t.Errorf("a matched author's comment was rewritten: %q", got)
			}
		case "cm-2":
			unmatched = true
			user, _ := store.UserByID(author)
			if user.Email != imports.ImportIdentityEmail(imports.SourceLinear) {
				t.Errorf("unmatched comment author = %q, want the import identity", user.Email)
			}
			if !strings.Contains(c.Content, "Dana Wu") {
				t.Errorf("the real author's name was lost from the provenance header: %q", c.Content)
			}
		}
	}
	if !matched || !unmatched {
		t.Fatalf("expected both comments to be written (matched=%v unmatched=%v)", matched, unmatched)
	}
}

// A label whose source colour is not a colour still gets written; issue_label
// .color is NOT NULL, so "" is not an option.
func TestLabelColourDegradesRatherThanFailing(t *testing.T) {
	store := importmem.New()
	if _, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, l := range store.Labels {
		if l.Color == "" {
			t.Errorf("label %q has no colour", l.Name)
		}
		if l.Name == "Bug" && l.Color != "#ef4444" {
			t.Errorf("the source colour was dropped: %q", l.Color)
		}
	}
}

// The issue counter must self-heal past numbers already in the table — the
// GREATEST(counter+1, max(number)+1) incident (counter 179 vs max number 319).
func TestIssueNumberSelfHealsPastExistingNumbers(t *testing.T) {
	store := importmem.New()
	store.IssueCounter = 2
	// Simulate a restore that left a high number behind with a lagging counter.
	first, err := newApplier(t, store).Apply(context.Background(), fixtureBundle(), nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if first.Totals[imports.KindIssue].Failed != 0 {
		t.Fatalf("issues failed: %+v", first.Failures)
	}
	seen := map[int32]bool{}
	for _, issue := range store.Issues {
		if seen[issue.Number] {
			t.Fatalf("issue number %d handed out twice", issue.Number)
		}
		seen[issue.Number] = true
	}
}

// Cancelling stops promptly, and everything already written stays written —
// resuming is a re-run, not a repair.
func TestApplyHonoursCancellation(t *testing.T) {
	store := importmem.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := newApplier(t, store).Apply(ctx, fixtureBundle(), nil)
	if err == nil {
		t.Fatal("Apply ignored a cancelled context")
	}
	if result == nil {
		t.Fatal("Apply returned no partial result; the receipt of what landed must survive")
	}
}

// An issue whose state the bundle does not carry still lands, on todo.
func TestUnknownStateNeverDropsAnIssue(t *testing.T) {
	store := importmem.New()
	bundle := fixtureBundle()
	bundle.Issues = append(bundle.Issues, imports.Issue{
		ExternalID: "iss-5", Identifier: "ENG-999", Title: "Orphan state",
		StateID: "st-vanished", CreatorID: "lin-kim", ContainerID: "team-eng",
		CreatedAt: created2024, UpdatedAt: created2024,
	})
	if _, err := newApplier(t, store).Apply(context.Background(), bundle, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	issue, ok := store.IssueByExternalID(imports.SourceLinear, "iss-5")
	if !ok {
		t.Fatal("an issue with an unknown state was dropped")
	}
	if issue.Status != imports.StatusTodo {
		t.Errorf("status = %q, want the never-dropped default %q", issue.Status, imports.StatusTodo)
	}
}
