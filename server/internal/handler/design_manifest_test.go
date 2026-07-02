package handler

import (
	"strings"
	"testing"
)

func TestRenderDesignManifestContext(t *testing.T) {
	var m designManifest
	m.Kind = "tokens"
	m.Revision = 3
	m.Tokens.Colors = map[string]string{"primary": "#2563EB"}
	m.Components = []struct {
		Name        string `json:"name"`
		CodeRef     string `json:"code_ref"`
		FigmaNodeID string `json:"figma_node_id"`
		Usage       string `json:"usage"`
	}{
		{Name: "DataTable", CodeRef: "src/DataTable.vue", Usage: "all lists"},
	}
	m.Conventions = []string{"BEM-ish classes"}
	m.AntiPatterns = []string{"no new global CSS"}
	m.LegacyNotes = "copy markup from protected/views"

	got := renderDesignManifestContext(m)
	for _, want := range []string{
		"PROJECT DESIGN SYSTEM (rev 3, kind=tokens)",
		"primary=#2563EB",
		"DataTable (src/DataTable.vue) — all lists",
		"CONVENTIONS: BEM-ish classes",
		"ANTI-PATTERNS (never do): no new global CSS",
		"LEGACY NOTES: copy markup from protected/views",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest context missing %q:\n%s", want, got)
		}
	}
}

func TestRenderDesignManifestContextLabeled(t *testing.T) {
	var m designManifest
	m.Kind = "tokens"
	m.Revision = 2
	got := renderDesignManifestContextLabeled(m, "WORKSPACE DESIGN SYSTEM (shared)")
	if !strings.Contains(got, "WORKSPACE DESIGN SYSTEM (shared) (rev 2, kind=tokens)") {
		t.Errorf("labeled render missing the custom label:\n%s", got)
	}
	// The default renderer keeps the PROJECT label (back-compat).
	if !strings.Contains(renderDesignManifestContext(m), "PROJECT DESIGN SYSTEM (rev 2") {
		t.Error("default render must keep the PROJECT label")
	}
}

func TestRenderDesignManifestContext_ComponentCap(t *testing.T) {
	var m designManifest
	m.Kind = "inventory"
	for i := 0; i < designManifestMaxComponents+10; i++ {
		m.Components = append(m.Components, struct {
			Name        string `json:"name"`
			CodeRef     string `json:"code_ref"`
			FigmaNodeID string `json:"figma_node_id"`
			Usage       string `json:"usage"`
		}{Name: "C" + string(rune('a'+i%26))})
	}
	got := renderDesignManifestContext(m)
	if !strings.Contains(got, "+10 more") {
		t.Errorf("expected overflow note for >%d components:\n%s", designManifestMaxComponents, got)
	}
}

func TestBuildSliceInstructionGenDesignManifest(t *testing.T) {
	got := buildSliceInstruction(sliceActionGenDesignManifest, "")
	if got == "" {
		t.Fatal("gen_design_manifest must render an instruction")
	}
	for _, want := range []string{
		"DESIGN MANIFEST",
		"READ-ONLY",
		"kind=\"tokens\"",
		"kind=\"inventory\"",
		"LEGACY MONOLITHS",
		"frequency-rank",
		"```design-manifest",
		"UNDER ~150 lines",
		"PRESERVE any human-added entries",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gen_design_manifest recipe missing %q", want)
		}
	}
	// Must forbid the enterprise-only Variables API.
	if !strings.Contains(got, "Do NOT attempt the Figma Variables API") {
		t.Error("recipe must forbid the Figma Variables API")
	}
	if !isKnownSliceActionKind(sliceActionGenDesignManifest) {
		t.Error("gen_design_manifest must be a known slice action")
	}
	if sliceActionOpensPR(sliceActionGenDesignManifest) {
		t.Error("gen_design_manifest must not open a PR")
	}
}

func TestDesignProposalRecipeHasManifestBootstrap(t *testing.T) {
	got := buildSliceInstruction(sliceActionDesignProposal, "")
	if !strings.Contains(got, "BOOTSTRAP") || !strings.Contains(got, "```design-manifest") {
		t.Error("design_proposal recipe must include the lazy manifest bootstrap")
	}
}
