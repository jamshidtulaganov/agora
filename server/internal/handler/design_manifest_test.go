package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/designcontext"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestRenderDesignContext(t *testing.T) {
	var m designcontext.Context
	m.Version = 1
	m.Kind = "tokens"
	m.Tokens.Colors = map[string]string{"primary": "#2563EB"}
	m.Components = []designcontext.Component{
		{Name: "DataTable", CodeRef: "src/DataTable.vue", Usage: "all lists"},
	}
	m.Conventions = []string{"BEM-ish classes"}
	m.AntiPatterns = []string{"no new global CSS"}
	m.LegacyNotes = "copy markup from protected/views"
	m.Sources = []designcontext.Source{{Kind: "repository", Locator: "packages/ui", ContentHash: "abc"}}

	got := renderDesignContext(m)
	for _, want := range []string{
		"APPROVED DESIGN CONTEXT (kind=tokens)",
		"derived, approved cache",
		"primary=#2563EB",
		"DataTable (src/DataTable.vue) — all lists",
		"CONVENTIONS: BEM-ish classes",
		"ANTI-PATTERNS (never do): no new global CSS",
		"LEGACY NOTES: copy markup from protected/views",
		"repository=packages/ui@abc",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest context missing %q:\n%s", want, got)
		}
	}
}

func TestRenderDesignContextLabeled(t *testing.T) {
	var m designcontext.Context
	m.Kind = "tokens"
	got := renderDesignContextLabeled(m, "WORKSPACE DESIGN CONTEXT (shared)")
	if !strings.Contains(got, "WORKSPACE DESIGN CONTEXT (shared) (kind=tokens)") {
		t.Errorf("labeled render missing the custom label:\n%s", got)
	}
	if !strings.Contains(renderDesignContext(m), "APPROVED DESIGN CONTEXT") {
		t.Error("default render must use the approved context label")
	}
}

func TestRenderDesignContext_ComponentCap(t *testing.T) {
	var m designcontext.Context
	m.Kind = "inventory"
	for i := 0; i < designContextMaxComponents+10; i++ {
		m.Components = append(m.Components, designcontext.Component{Name: "C" + string(rune('a'+i%26))})
	}
	got := renderDesignContext(m)
	if !strings.Contains(got, "+10 more") {
		t.Errorf("expected overflow note for >%d components:\n%s", designContextMaxComponents, got)
	}
}

func TestBuildSliceInstructionGenDesignManifest(t *testing.T) {
	got := buildSliceInstruction(sliceActionGenDesignContext, "")
	if got == "" {
		t.Fatal("gen_design_manifest must render an instruction")
	}
	for _, want := range []string{
		"DESIGN CONTEXT",
		"NOT a source of truth",
		"READ-ONLY",
		"\"version\":1",
		"kind=\"tokens\"",
		"kind=\"inventory\"",
		"LEGACY MONOLITHS",
		"frequency-rank",
		"```design-context",
		"content_hash",
		"UNDER ~150 lines",
		"stores this block as a proposal",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gen_design_manifest recipe missing %q", want)
		}
	}
	// Must forbid the enterprise-only Variables API.
	if !strings.Contains(got, "Do NOT attempt the Figma Variables API") {
		t.Error("recipe must forbid the Figma Variables API")
	}
	if !isKnownSliceActionKind(sliceActionGenDesignContext) {
		t.Error("gen_design_context must be a known slice action")
	}
	if sliceActionOpensPR(sliceActionGenDesignContext) {
		t.Error("gen_design_context must not open a PR")
	}
}

func TestResolveExpectedDesignContextRevision(t *testing.T) {
	want := int32(7)
	if got, ok := resolveExpectedDesignContextRevision(proposeDesignContextRequest{Context: []byte(`{}`), ExpectedRevision: &want}, 7); !ok || got != 7 {
		t.Fatalf("explicit revision = %d, %v", got, ok)
	}
	if got, ok := resolveExpectedDesignContextRevision(proposeDesignContextRequest{LegacyManifest: []byte(`{}`)}, 7); !ok || got != 7 {
		t.Fatalf("legacy compatibility revision = %d, %v", got, ok)
	}
	if _, ok := resolveExpectedDesignContextRevision(proposeDesignContextRequest{Context: []byte(`{}`)}, 7); ok {
		t.Fatal("new Design context writes must require expected_revision")
	}
}

func TestDesignContextRevisionFresh(t *testing.T) {
	document := designcontext.Context{Version: 1, Kind: "tokens"}
	document.Sources = []designcontext.Source{{Kind: "repository", Locator: "tokens.css", ContentHash: "abcdef12", CapturedAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	row := db.DesignContextRevision{Context: raw}
	if !designContextRevisionFresh(row) {
		t.Fatal("fresh proposal should be approvable")
	}
	document.Sources[0].CapturedAt = time.Now().UTC().Add(-designcontext.MaxAge - time.Hour).Format(time.RFC3339)
	row.Context, _ = json.Marshal(document)
	if designContextRevisionFresh(row) {
		t.Fatal("stale proposal must not be approvable")
	}
}

func TestDesignProposalRecipeHasManifestBootstrap(t *testing.T) {
	got := buildSliceInstruction(sliceActionDesignProposal, "")
	if !strings.Contains(got, "BOOTSTRAP") || !strings.Contains(got, "```design-context") {
		t.Error("design_proposal recipe must include the lazy manifest bootstrap")
	}
}
