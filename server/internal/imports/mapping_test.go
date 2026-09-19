package imports

import "testing"

// testDefaults is a stand-in adapter default table shaped like Linear's: a
// category map, one pinned name, and refinements within the `started` bucket.
func testDefaults() Defaults {
	return Defaults{
		StatusByCategory: map[string]string{
			"backlog":   StatusBacklog,
			"triage":    StatusTodo,
			"unstarted": StatusTodo,
			"started":   StatusInProgress,
			"completed": StatusDone,
			"canceled":  StatusCancelled,
		},
		StatusByName: map[string]string{
			"duplicate": StatusCancelled,
		},
		StatusRefine: map[string][]Refinement{
			"started": {
				{Keywords: []string{"review", "qa", "testing"}, Status: StatusInReview},
				{Keywords: []string{"blocked", "on hold"}, Status: StatusBlocked},
			},
		},
	}
}

func TestStatusMapsCategoryFirstThenName(t *testing.T) {
	m := NewMapping(SourceLinear, testDefaults(), Overrides{})

	cases := []struct {
		name, category, want string
	}{
		{"Backlog", "backlog", StatusBacklog},
		{"Todo", "unstarted", StatusTodo},
		{"Triage", "triage", StatusTodo},
		{"In Progress", "started", StatusInProgress},
		// The reason the name refines within the category: "In Review" is a
		// `started` state in Linear, and flattening it to in_progress would
		// lose the column the team actually works out of.
		{"In Review", "started", StatusInReview},
		{"QA", "started", StatusInReview},
		{"Blocked", "started", StatusBlocked},
		{"Done", "completed", StatusDone},
		{"Canceled", "canceled", StatusCancelled},
		// A name the adapter pinned explicitly beats its category.
		{"Duplicate", "canceled", StatusCancelled},
	}
	for _, c := range cases {
		got := m.Status(State{Name: c.name, Category: c.category})
		if got != c.want {
			t.Errorf("Status(%q/%q) = %q, want %q", c.name, c.category, got, c.want)
		}
	}
}

// An unknown state must never drop an issue. It lands on todo and shows up in
// the report so the operator can assign it.
func TestUnknownStatusFallsBackAndIsReported(t *testing.T) {
	m := NewMapping(SourceLinear, testDefaults(), Overrides{})

	for i := 0; i < 3; i++ {
		if got := m.Status(State{Name: "Waiting on customer", Category: "something-new"}); got != StatusTodo {
			t.Fatalf("unknown status = %q, want %q (an issue must never be dropped)", got, StatusTodo)
		}
	}
	unmapped := m.Unmapped()
	if len(unmapped) != 1 {
		t.Fatalf("Unmapped() = %+v, want exactly one line", unmapped)
	}
	if unmapped[0].Name != "Waiting on customer" || unmapped[0].Issues != 3 {
		t.Errorf("Unmapped()[0] = %+v, want 3 issues on 'Waiting on customer'", unmapped[0])
	}
	if unmapped[0].FellBackTo != StatusTodo {
		t.Errorf("fell back to %q, want %q", unmapped[0].FellBackTo, StatusTodo)
	}
}

func TestWorkspaceOverrideBeatsAdapterDefault(t *testing.T) {
	settings := []byte(`{
		"import_mapping": {
			"linear": {
				"status": {"Waiting On Customer": "blocked", "In Progress": "in_review"},
				"priority": {"low": "none"},
				"containers": {"ENG": "3f7c1b2a-0000-4000-8000-000000000001"}
			},
			"jira": {"status": {"In Progress": "done"}}
		}
	}`)
	overrides := ParseOverrides(settings, SourceLinear)
	m := NewMapping(SourceLinear, testDefaults(), overrides)

	if got := m.Status(State{Name: "Waiting on customer", Category: "started"}); got != StatusBlocked {
		t.Errorf("override on an unmapped name = %q, want blocked", got)
	}
	if got := m.Status(State{Name: "In Progress", Category: "started"}); got != StatusInReview {
		t.Errorf("override on a mapped name = %q, want in_review (the override wins)", got)
	}
	if got := m.Priority(PriorityLow); got != PriorityNameNone {
		t.Errorf("priority override = %q, want none", got)
	}
	if id, ok := m.ContainerProject(Container{Key: "ENG", Name: "Engineering"}); !ok || id != "3f7c1b2a-0000-4000-8000-000000000001" {
		t.Errorf("ContainerProject = %q/%v, want the pinned project", id, ok)
	}
	// The other source's table must not leak into this one.
	if got := m.Status(State{Name: "In Progress", Category: "started"}); got == StatusDone {
		t.Error("a jira override was applied to a linear import")
	}
}

// A settings document that is junk, or holds an Agora status that does not
// exist, must not be able to fail an import: bad overrides are dropped here so
// a typo cannot produce a CHECK violation mid-run.
func TestOverridesRejectJunkRatherThanFailing(t *testing.T) {
	if got := ParseOverrides([]byte(`not json at all`), SourceLinear); len(got.Status) != 0 {
		t.Errorf("malformed settings produced %+v, want empty overrides", got)
	}
	if got := ParseOverrides(nil, SourceLinear); len(got.Status) != 0 {
		t.Errorf("nil settings produced %+v, want empty overrides", got)
	}
	overrides := ParseOverrides([]byte(`{"import_mapping":{"linear":{"status":{"Open":"shipped","Shut":"done"}}}}`), SourceLinear)
	if _, bad := overrides.Status["open"]; bad {
		t.Error("an override naming a status the issue table does not admit was kept")
	}
	if overrides.Status["shut"] != StatusDone {
		t.Errorf("a valid override alongside an invalid one was dropped: %+v", overrides.Status)
	}
}

// Linear's priority integers ARE the canonical ladder, so the default table is
// the identity. This is the bijection §2.1 calls "almost embarrassingly clean";
// the test exists so a later edit cannot quietly rotate it.
func TestPriorityLadderIsABijection(t *testing.T) {
	m := NewMapping(SourceLinear, testDefaults(), Overrides{})
	want := map[Priority]string{
		PriorityNone:   PriorityNameNone,
		PriorityUrgent: PriorityNameUrgent,
		PriorityHigh:   PriorityNameHigh,
		PriorityMedium: PriorityNameMedium,
		PriorityLow:    PriorityNameLow,
	}
	for p, name := range want {
		if got := m.Priority(p); got != name {
			t.Errorf("Priority(%d) = %q, want %q", p, got, name)
		}
	}
	// Out of range degrades rather than writing an invalid priority.
	if got := m.Priority(Priority(97)); got != PriorityNameNone {
		t.Errorf("Priority(97) = %q, want none", got)
	}
	if table := m.PriorityTable(); len(table) != 5 {
		t.Errorf("PriorityTable has %d rows, want the five-rung ladder", len(table))
	}
}

func TestAliasesAreFoldedBothWays(t *testing.T) {
	aliases := ParseAliases([]byte(`{"import_identity_aliases":{"  Dana@Old.Example ":"dana@ACME.io"}}`))
	if got := aliases["dana@old.example"]; got != "dana@acme.io" {
		t.Errorf("alias = %q, want the folded canonical address", got)
	}
	if ParseAliases([]byte(`{}`)) != nil {
		t.Error("an empty settings document should produce no alias map")
	}
}

// Relation vocabulary: four values in, everything else degrades to `related`
// with the source's own name preserved by the caller.
func TestRelationsDegradeRatherThanCrash(t *testing.T) {
	cases := []struct {
		in    string
		want  RelationKind
		exact bool
	}{
		{"blocks", RelationBlocks, true},
		{"blocked_by", RelationBlockedBy, true},
		{"duplicate", RelationDuplicate, true},
		{"related", RelationRelated, true},
		{"similar", RelationRelated, false}, // Linear's fourth value
		{"Cloners", RelationRelated, false}, // a Jira installation-defined type
		{"", RelationRelated, false},
	}
	for _, c := range cases {
		got, exact := DegradeRelation(c.in)
		if got != c.want || exact != c.exact {
			t.Errorf("DegradeRelation(%q) = %q/%v, want %q/%v", c.in, got, exact, c.want, c.exact)
		}
	}
}

func TestStatusTableExplainsHowEachRowWasDecided(t *testing.T) {
	overrides := ParseOverrides([]byte(`{"import_mapping":{"linear":{"status":{"In Progress":"in_review"}}}}`), SourceLinear)
	m := NewMapping(SourceLinear, testDefaults(), overrides)
	table := m.StatusTable([]State{
		{Name: "In Progress", Category: "started"},
		{Name: "Done", Category: "completed"},
		{Name: "Duplicate", Category: "canceled"},
		{Name: "Waiting on customer", Category: "mystery"},
	})
	got := map[string]StatusMapping{}
	for _, row := range table {
		got[row.SourceName] = row
	}
	if got["In Progress"].Via != "override" {
		t.Errorf("In Progress via = %q, want override", got["In Progress"].Via)
	}
	if got["Done"].Via != "default:category" {
		t.Errorf("Done via = %q, want default:category", got["Done"].Via)
	}
	if got["Duplicate"].Via != "default:name" {
		t.Errorf("Duplicate via = %q, want default:name", got["Duplicate"].Via)
	}
	if got["Waiting on customer"].Via != "fallback" {
		t.Errorf("unmapped via = %q, want fallback", got["Waiting on customer"].Via)
	}
}
