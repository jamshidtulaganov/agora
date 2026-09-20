package handler

import (
	"context"
	"testing"
)

// The risk map used by every case below. It is deliberately shaped like a real
// one: a money module with two globs (a subtree and a filename pattern), a
// shared-surface module, an isolated safe module — and nothing covering the
// rest of the repo, which is the case the "unknown is guarded" rule exists for.
var riskTierTestMap = []riskMapEntry{
	{Module: "billing", Tier: "critical", Paths: []string{"protected/modules/pay/**", "protected/controllers/Kassa*"}, Owner: "Davron"},
	{Module: "shared-ui", Tier: "guarded", Paths: []string{"assets/js/shared/**"}},
	{Module: "reports", Tier: "safe", Paths: []string{"protected/views/report/**", "docs"}},
}

func TestRiskGlobMatch(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
		why        string
	}{
		{"protected/modules/pay/**", "protected/modules/pay/models/Invoice.php", true, "** spans several segments"},
		{"protected/modules/pay/**", "protected/modules/pay", true, "** matches zero segments — the directory itself"},
		{"protected/modules/pay/**", "protected/modules/payroll/X.php", false, "a segment prefix is not a segment match"},
		{"protected/controllers/Kassa*", "protected/controllers/KassaController.php", true, "* inside a segment"},
		{"protected/controllers/Kassa*", "protected/controllers/sub/KassaController.php", false, "* must not cross a /"},
		{"docs", "docs/adr/0001.md", true, "a bare name covers its whole subtree"},
		{"docs", "documentation/x.md", false, "prefix of a segment is not a match"},
		{"assets/js/shared/**", "./assets/js/shared/grid.js", true, "a leading ./ is normalized away"},
		{"/assets/js/shared/**", "assets/js/shared/grid.js", true, "a leading / is normalized away"},
		{"assets/js/shared/", "assets/js/shared/grid.js", true, "a trailing / is normalized away"},
		{"**/migrations/**", "server/db/migrations/001.sql", true, "a leading ** anchors anywhere"},
		{"", "anything", false, "an empty glob matches nothing"},
		{"docs", "", false, "an empty path matches nothing"},
	}
	for _, c := range cases {
		if got := riskGlobMatch(c.glob, c.path); got != c.want {
			t.Errorf("riskGlobMatch(%q, %q) = %v, want %v — %s", c.glob, c.path, got, c.want, c.why)
		}
	}
}

// The core safety rule: the strictest matching tier wins, and a path NO glob
// claims is guarded — never safe.
func TestClassifyRiskTierForPaths(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"a critical path alone", []string{"protected/modules/pay/Invoice.php"}, riskTierCritical},
		{"critical wins over safe in the same diff", []string{"protected/views/report/list.php", "protected/controllers/KassaController.php"}, riskTierCritical},
		{"guarded wins over safe", []string{"protected/views/report/list.php", "assets/js/shared/grid.js"}, riskTierGuarded},
		{"every path safe ⇒ safe", []string{"protected/views/report/list.php", "docs/readme.md"}, riskTierSafe},
		{"an UNMATCHED path drags the whole diff to guarded", []string{"protected/views/report/list.php", "random/file.go"}, riskTierGuarded},
		{"only unmatched paths ⇒ guarded, never safe", []string{"random/file.go"}, riskTierGuarded},
		{"no paths at all ⇒ no opinion, not safe", nil, ""},
		{"blank paths are ignored, not counted as unknown", []string{"  ", "docs/x.md"}, riskTierSafe},
	}
	for _, c := range cases {
		if got := classifyRiskTierForPaths(riskTierTestMap, c.paths); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := classifyRiskTierForPaths(nil, []string{"protected/modules/pay/x.php"}); got != "" {
		t.Errorf("no risk map must yield no opinion, got %q", got)
	}
}

// An entry with a missing or unrecognised tier is GUARDED. A typo in a safety
// control must never silently mean "safe".
func TestNormalizeRiskMapTier(t *testing.T) {
	for in, want := range map[string]string{
		"critical": riskTierCritical, "CRITICAL": riskTierCritical,
		"safe": riskTierSafe, " safe ": riskTierSafe,
		"guarded": riskTierGuarded, "": riskTierGuarded,
		"medium": riskTierGuarded, "none": riskTierGuarded,
	} {
		if got := normalizeRiskMapTier(in); got != want {
			t.Errorf("normalizeRiskMapTier(%q) = %q, want %q", in, got, want)
		}
	}
}

// The precedence rule — the whole point of §A1.1. Read the `why` of each case
// as the requirement it encodes.
func TestResolveRiskTierPrecedence(t *testing.T) {
	criticalPaths := []string{"protected/modules/pay/Invoice.php"}
	safePaths := []string{"docs/readme.md"}

	cases := []struct {
		name       string
		mapped     bool
		paths      []string
		labels     []string
		wantTier   string
		wantSource string
	}{
		{
			name: "derived beats a weaker self-reported label",
			// The whole finding: an agent that labels its own money-path diff
			// risk:safe must not be believed.
			mapped: true, paths: criticalPaths, labels: []string{"risk:safe"},
			wantTier: riskTierCritical, wantSource: riskSourceDerived,
		},
		{
			name:   "a STRICTER label still escalates",
			mapped: true, paths: safePaths, labels: []string{"risk:critical"},
			wantTier: riskTierCritical, wantSource: riskSourceLabel,
		},
		{
			name:   "derived with no label at all",
			mapped: true, paths: safePaths, labels: nil,
			wantTier: riskTierSafe, wantSource: riskSourceDerived,
		},
		{
			name: "NO EVIDENCE ⇒ the label decides, exactly as before this change",
			// No PR yet, sprint mode's shared branch, GitHub App unconfigured:
			// behaviour must be identical to the pre-derivation product.
			mapped: true, paths: nil, labels: []string{"risk:safe"},
			wantTier: riskTierSafe, wantSource: riskSourceLabel,
		},
		{
			name:   "no evidence and no label in a risk-mapped project ⇒ fail closed",
			mapped: true, paths: nil, labels: []string{"tier:light"},
			wantTier: riskTierGuarded, wantSource: riskSourceMapDefault,
		},
		{
			name:   "no risk map, but a label ⇒ the label, pre-risk-map behaviour",
			mapped: false, paths: criticalPaths, labels: []string{"risk:guarded"},
			wantTier: riskTierGuarded, wantSource: riskSourceLabel,
		},
		{
			name:   "no risk map and no label ⇒ no opinion",
			mapped: false, paths: nil, labels: []string{"tier:trivial"},
			wantTier: "", wantSource: riskSourceNone,
		},
		{
			name:   "a sticky label pair resolves to the stricter half",
			mapped: false, paths: nil, labels: []string{"risk:safe", "risk:critical"},
			wantTier: riskTierCritical, wantSource: riskSourceLabel,
		},
	}
	for _, c := range cases {
		entries := riskTierTestMap
		if !c.mapped {
			entries = nil
		}
		got := resolveRiskTier(entries, c.mapped, c.paths, c.labels)
		if got.Tier != c.wantTier || got.Source != c.wantSource {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, got.Tier, got.Source, c.wantTier, c.wantSource)
		}
	}
}

// §A1.3: the absence of an opinion is explicit on the wire. "" must never reach
// a client that could read it as "safe".
func TestAPIRiskTier(t *testing.T) {
	for in, want := range map[string]string{
		"":             riskTierUnclassified,
		"safe":         riskTierSafe,
		"guarded":      riskTierGuarded,
		"critical":     riskTierCritical,
		"catastrophic": riskTierUnclassified, // a tier this server does not know
		"unclassified": riskTierUnclassified,
	} {
		if got := apiRiskTier(in); got != want {
			t.Errorf("apiRiskTier(%q) = %q, want %q", in, got, want)
		}
	}
	res := riskTierResolution{Tier: "", Source: riskSourceNone}
	if res.APITier() != riskTierUnclassified {
		t.Errorf("resolution.APITier() = %q, want unclassified", res.APITier())
	}
}

// ── the five pipeline consumers' entry point, against a real database ───────

// THE FINDING, end to end: an agent that labels its own money-path diff
// risk:safe no longer gets to decide the tier that gates auto-merge, the done
// gate, QA depth and the evidence floor. issueRiskTier is what all five read,
// so this is the assertion that the self-report is actually gone.
func TestIssueRiskTierPrefersTheDerivedTierOverTheAgentsLabel(t *testing.T) {
	ctx := t.Context()
	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, settings)
		 VALUES ($1::uuid, 'Derived Tier', jsonb_build_object('risk_map',
		   '[{"module":"billing","tier":"critical","paths":["pay/**"]},
		     {"module":"docs","tier":"safe","paths":["docs/**"]}]'::jsonb))
		 RETURNING id::text`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1::uuid`, projectID) })

	var issueID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number)
		 VALUES ($1::uuid, $2::uuid, 'agent says this is safe', 'in_review', 'member', $3::uuid,
		         (6000000 + floor(random()*900000))::int)
		 RETURNING id::text`, testWorkspaceID, projectID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1::uuid`, issueID) })

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	// Before any PR exists there is nothing to classify, so the label decides —
	// exactly the pre-derivation behaviour, which is what keeps this change from
	// silently re-tiering every existing issue.
	attachLabel(t, issueID, "risk:safe")
	if got := testHandler.issueRiskTier(ctx, issue); got != riskTierSafe {
		t.Fatalf("with no diff evidence the label must still decide, got %q", got)
	}

	// Now the PR lands, and it touches the money module.
	linkPR(t, issueID, "open", []string{"pay/Invoice.php", "docs/readme.md"})
	if got := testHandler.issueRiskTier(ctx, issue); got != riskTierCritical {
		t.Fatalf("the derived tier must beat the agent's risk:safe label, got %q", got)
	}
	res := testHandler.resolveIssueRiskTier(ctx, issue)
	if res.Source != riskSourceDerived || res.KnownPaths != 2 {
		t.Fatalf("the resolution must say it was derived and from how much evidence: %+v", res)
	}
}
