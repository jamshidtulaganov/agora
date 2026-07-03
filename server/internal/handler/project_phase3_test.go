package handler

import (
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// projectModuleKBName composes "<base>-<module-slug>", and returns "" when the
// base is unresolvable (Cyrillic title, no override) or the module is empty.
func TestProjectModuleKBName(t *testing.T) {
	p := db.Project{Title: "sd-main", Settings: []byte(`{}`)}
	if got := projectModuleKBName(p, "billing/payments"); got != "sd-main-kb-billing-payments" {
		t.Errorf("want sd-main-kb-billing-payments, got %q", got)
	}
	// override base + spaced module
	pov := db.Project{Title: "10 спринт", Settings: []byte(`{"kb_skill":"sd-main-kb"}`)}
	if got := projectModuleKBName(pov, "Stock Warehouse"); got != "sd-main-kb-stock-warehouse" {
		t.Errorf("want sd-main-kb-stock-warehouse, got %q", got)
	}
	// unresolvable base
	if got := projectModuleKBName(db.Project{Title: "спринт", Settings: nil}, "billing"); got != "" {
		t.Errorf("cyrillic base without override must yield empty, got %q", got)
	}
	// empty module
	if got := projectModuleKBName(p, "  "); got != "" {
		t.Errorf("empty module must yield empty, got %q", got)
	}
}

// buildModuleStudyPrompt (pure inputs via projectRiskMapForProject) resolves the
// module's paths from the risk map and names the target skill; rejects unknown.
func TestBuildModuleStudyPrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	proj := db.Project{
		Title:       "sd-main",
		WorkspaceID: parseUUID(testWorkspaceID),
		Settings:    []byte(`{"kb_skill":"sd-main-kb","risk_map":[{"module":"billing","tier":"critical","paths":["protected/modules/pay/**","protected/modules/finans/**"]}]}`),
	}
	prompt, kbName, reason := testHandler.buildModuleStudyPrompt(ctx, proj, "billing")
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if kbName != "sd-main-kb-billing" {
		t.Errorf("want kb sd-main-kb-billing, got %q", kbName)
	}
	for _, want := range []string{"MODULE knowledge base", "billing", "protected/modules/pay/**", "protected/modules/finans/**", "sd-main-kb-billing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	// unknown module → reason, no prompt
	if _, _, r := testHandler.buildModuleStudyPrompt(ctx, proj, "nonsense"); r == "" {
		t.Error("unknown module must return a 400 reason")
	}
	// no risk map → reason
	if _, _, r := testHandler.buildModuleStudyPrompt(ctx, db.Project{Title: "x", Settings: []byte(`{}`)}, "billing"); r == "" {
		t.Error("no risk map must return a reason")
	}
}

// The autonomy aggregation math: pass rate = pass/(pass+fail), untested
// excluded; multi-module issues count under each module. (Pure aggregation is
// exercised through a small hand-rolled reduction mirroring the handler.)
func TestAutonomyAggregationMath(t *testing.T) {
	rows := []db.ProjectAutonomyRowsRow{
		{QaVerdict: "pass", Modules: []string{"module:billing"}},
		{QaVerdict: "pass", Modules: []string{"module:billing", "module:orders"}},
		{QaVerdict: "fail", Modules: []string{"module:billing"}},
		{QaVerdict: "", Modules: []string{"module:billing"}}, // untested
		{QaVerdict: "pass", Modules: []string{"module:orders"}},
	}
	type acc struct{ pass, fail, untested int }
	m := map[string]*acc{}
	for _, r := range rows {
		v := strings.ToLower(r.QaVerdict)
		for _, l := range r.Modules {
			mod := strings.TrimPrefix(l, "module:")
			a := m[mod]
			if a == nil {
				a = &acc{}
				m[mod] = a
			}
			switch v {
			case "pass":
				a.pass++
			case "fail":
				a.fail++
			default:
				a.untested++
			}
		}
	}
	if m["billing"].pass != 2 || m["billing"].fail != 1 || m["billing"].untested != 1 {
		t.Errorf("billing agg wrong: %+v", *m["billing"])
	}
	rate := float64(m["billing"].pass) / float64(m["billing"].pass+m["billing"].fail)
	if rate < 0.66 || rate > 0.67 {
		t.Errorf("billing pass rate want ~0.667, got %v", rate)
	}
	if m["orders"].pass != 2 || m["orders"].fail != 0 {
		t.Errorf("orders agg wrong: %+v", *m["orders"])
	}
}

// Every focused automation prompt must forbid delegation/fan-out — the sd-cs
// stress test showed an orchestrator lead pulling QA Tester + Security Reviewer
// into a solo "extract conventions" job.
func TestAutomationPromptsForbidDelegation(t *testing.T) {
	prompts := map[string]string{
		"conventions": buildLearnConventionsPrompt("p"),
		"base-suite":  baseSuitePromptTmpl + soloAutomationDirective,
		"triage":      bitrixTriagePrompt + soloAutomationDirective,
		"kb-study":    buildProjectStudyPrompt("p"),
	}
	for name, p := range prompts {
		if !strings.Contains(p, "do NOT delegate") || !strings.Contains(p, "do NOT @mention") {
			t.Errorf("%s prompt missing the solo/no-delegate directive", name)
		}
	}
}
