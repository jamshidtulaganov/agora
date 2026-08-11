package designcontext

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeProposalRejectsLooseOrUnprovenancedContext(t *testing.T) {
	valid := `{"version":1,"kind":"tokens","figma":{},"tokens":{"colors":{"brand":"#2563EB"},"typography":{},"spacing":{}},"components":[],"conventions":[],"anti_patterns":[],"sources":[{"kind":"repository","locator":"packages/ui/styles/tokens.css","content_hash":"abcdef12","captured_at":"2026-08-11T06:00:00Z"}]}`
	if _, err := DecodeProposal([]byte(valid)); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}
	for _, raw := range []string{
		`{"version":1,"kind":"tokens","figma":{},"tokens":{"colors":{},"typography":{},"spacing":{}},"components":[],"conventions":[],"anti_patterns":[],"sources":[],"future":true}`,
		`{"version":1,"kind":"tokens","figma":{},"tokens":{"colors":{},"typography":{},"spacing":{}},"components":[],"conventions":[],"anti_patterns":[],"sources":[]}`,
		`{"version":1,"kind":"tokens","figma":{},"components":[],"conventions":[],"anti_patterns":[],"sources":[{"kind":"repository","locator":"tokens.css","content_hash":"abcdef12","captured_at":"2026-08-11T06:00:00Z"}]}`,
	} {
		if _, err := DecodeProposal([]byte(raw)); err == nil {
			t.Fatalf("invalid context accepted: %s", raw)
		}
	}
}

func TestMergeUsesDeterministicProjectOverrides(t *testing.T) {
	workspace := Context{Version: 1, Kind: "tokens"}
	normalize(&workspace)
	workspace.Tokens.Colors["brand"] = "blue"
	workspace.Tokens.Spacing["sm"] = "4px"
	workspace.Components = []Component{{Name: "Button", CodeRef: "shared/button"}, {Name: "Card"}}
	workspace.Conventions = []string{"Use tokens"}
	workspace.Sources = []Source{{Kind: "repository", Locator: "shared", ContentHash: "a", CapturedAt: "2026-08-11T10:00:00Z"}}

	project := Context{Version: 1, Kind: "inventory"}
	normalize(&project)
	project.Tokens.Colors["brand"] = "navy"
	project.Components = []Component{{Name: "button", CodeRef: "project/button"}, {Name: "Dialog"}}
	project.Conventions = []string{"Use tokens", "Keep routes thin"}
	project.Sources = []Source{{Kind: "repository", Locator: "project", ContentHash: "b", CapturedAt: "2026-08-11T10:00:00Z"}}

	got := Merge(workspace, project)
	if got.Kind != "inventory" || got.Tokens.Colors["brand"] != "navy" || got.Tokens.Spacing["sm"] != "4px" {
		t.Fatalf("unexpected merged tokens: %+v", got)
	}
	if len(got.Components) != 3 || strings.ToLower(got.Components[0].Name) != "button" || got.Components[0].CodeRef != "project/button" {
		t.Fatalf("components were not deterministically overridden: %+v", got.Components)
	}
	if len(got.Conventions) != 2 || len(got.Sources) != 2 {
		t.Fatalf("lists were not merged: %+v", got)
	}
	if workspace.Tokens.Colors["brand"] != "blue" || workspace.Components[0].CodeRef != "shared/button" {
		t.Fatalf("merge mutated workspace input: %+v", workspace)
	}
}

func TestEvaluateFreshness(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	c := Context{Sources: []Source{{Kind: "figma", Locator: "file", ContentHash: "abcdef12", CapturedAt: now.Add(-time.Hour).Format(time.RFC3339)}}}
	if got := EvaluateFreshness(c, now); got.Status != "fresh" {
		t.Fatalf("freshness = %+v", got)
	}
	c.Sources[0].CapturedAt = now.Add(-MaxAge - time.Hour).Format(time.RFC3339)
	if got := EvaluateFreshness(c, now); got.Status != "stale" || len(got.StaleSources) != 1 {
		t.Fatalf("freshness = %+v", got)
	}
}
