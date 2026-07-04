package service

import (
	"strings"
	"testing"
)

func TestClassifyIssueTier(t *testing.T) {
	long := strings.Repeat("detailed spec line; ", 50) // ~1000 chars

	tests := []struct {
		name  string
		title string
		desc  string
		want  string
	}{
		{"typo is trivial", "Fix typo in header", "", "tier:trivial"},
		// "rename" is deliberately NOT a keyword: it matched *atomic RENAME* in
		// a real backend task on the sd-main backlog (false positive).
		{"rename stays full (false-positive guard)", "atomic RENAME in cache rebuilder service", "", ""},
		{"alignment is light (the $2.82 case)", "Некорректное выравнивание блока «Общая сумма»", "", "tier:light"},
		{"color tweak is light", "Update button color to brand blue", "", "tier:light"},
		{"css keyword is light", "CSS spacing off on the report card", "small fix", "tier:light"},
		// Must NOT downgrade: Persian localization read "small" but was huge.
		{"translation/language stays full", "Persian language", "translate the app to Persian", ""},
		{"feature stays full", "Add new CRM integration with the A++ API", "", ""},
		{"empty stays full", "", "", ""},
		// Docs-only work is trivial even when the body is longer than a typo.
		{"docs task is trivial", "Add docs/MODULE_NOTES.md for the orders module", "", "tier:trivial"},
		{"readme is trivial", "Update README with the QA box URL", "", "tier:trivial"},
		{"markdown changelog is trivial", "Add a CHANGELOG.md entry", "", "tier:trivial"},
		// A docs mention on a huge body (over tierDocsMaxLen) still stays full.
		{"docs keyword but huge body stays full", "Update docs", strings.Repeat("y", 4100), ""},
		// Brevity gate: a small keyword on a long detailed body stays full.
		{"css keyword but long body stays full", "CSS tweak", long, ""},
		// A typo with a medium body falls through trivial (too long) to light.
		{"typo with medium body is light", "typo somewhere", strings.Repeat("x", 250), "tier:light"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyIssueTier(tt.title, tt.desc); got != tt.want {
				t.Errorf("classifyIssueTier(%q, <%d-char desc>) = %q, want %q",
					tt.title, len(tt.desc), got, tt.want)
			}
		})
	}
}
