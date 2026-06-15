package bitrix

import (
	"strings"
	"testing"
)

func TestBBCodeToMarkdown(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantHas     string
		wantMissing string // a tag that must be gone ("" = no check)
	}{
		{"bold", "В [b]остатках[/b] есть", "**остатках**", "[b]"},
		{"url labeled", "see [url=/settings/x]Settings[/url]", "[Settings](/settings/x)", "[url"},
		{"user mention", "[USER=521]Bilol[/USER], ждем мердж", "Bilol, ждем мердж", "[USER"},
		{"numbered list", "[list=1]\n[*]one\n[*]two\n[/list]", "- one", "[*]"},
		{"unordered list", "[list]\n[*]a\n[/list]", "- a", "[list"},
		{"plain passthrough", "no tags here", "no tags here", "["},
		{"strip unknown", "x [color=red]y[/color] z", "y", "[color"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BBCodeToMarkdown(c.in)
			if !strings.Contains(got, c.wantHas) {
				t.Errorf("BBCodeToMarkdown(%q) = %q, want contains %q", c.in, got, c.wantHas)
			}
			if c.wantMissing != "" && strings.Contains(got, c.wantMissing) {
				t.Errorf("BBCodeToMarkdown(%q) = %q, must not contain %q", c.in, got, c.wantMissing)
			}
		})
	}
}
