package service

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ProjectKBSkillName: explicit settings.kb_skill override wins; else the slug;
// a Cyrillic-only title (slug "") with no override yields "" (no lookup).
// Ported from the handler package when the function moved here for the compile
// pipeline (the override governs the compile target too).
func TestProjectKBSkillNameOverride(t *testing.T) {
	withOverride := db.Project{Title: "10 спринт (Июль)", Settings: []byte(`{"kb_skill":"sd-main-kb"}`)}
	if got := ProjectKBSkillName(withOverride); got != "sd-main-kb" {
		t.Errorf("override: want sd-main-kb, got %q", got)
	}
	bySlug := db.Project{Title: "sd-cs", Settings: []byte(`{}`)}
	if got := ProjectKBSkillName(bySlug); got != "sd-cs-kb" {
		t.Errorf("slug: want sd-cs-kb, got %q", got)
	}
	cyrillicNoOverride := db.Project{Title: "спринт", Settings: nil}
	if got := ProjectKBSkillName(cyrillicNoOverride); got != "" {
		t.Errorf("cyrillic-only title without override must yield empty, got %q", got)
	}
}
