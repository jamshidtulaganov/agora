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
			got := BBCodeToMarkdown(c.in, "")
			if !strings.Contains(got, c.wantHas) {
				t.Errorf("BBCodeToMarkdown(%q) = %q, want contains %q", c.in, got, c.wantHas)
			}
			if c.wantMissing != "" && strings.Contains(got, c.wantMissing) {
				t.Errorf("BBCodeToMarkdown(%q) = %q, must not contain %q", c.in, got, c.wantMissing)
			}
		})
	}
}

// TestBBCodeToMarkdownAbsolutizesPortalLinks covers the desktop route-error bug:
// Bitrix writes portal-internal links WITHOUT an origin, so a converted
// "[Deal](/crm/deal/details/19951/)" looked like an app route. Clicking one in
// the desktop app pushed "/workgroups/group/105/" into the renderer's router,
// which answered 404 "No route matches URL".
func TestBBCodeToMarkdownAbsolutizesPortalLinks(t *testing.T) {
	const origin = "https://salesdoc.bitrix24.kz"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "labeled bbcode link to a portal path",
			in:   "[url=/crm/deal/details/19951/]Deal[/url]",
			want: "[Deal](https://salesdoc.bitrix24.kz/crm/deal/details/19951/)",
		},
		{
			name: "workgroup path from the reported error",
			in:   "see [url=/workgroups/group/105/]the group[/url]",
			want: "see [the group](https://salesdoc.bitrix24.kz/workgroups/group/105/)",
		},
		{
			name: "markdown link with no bbcode at all",
			in:   "[Deal](/crm/deal/details/31217/)",
			want: "[Deal](https://salesdoc.bitrix24.kz/crm/deal/details/31217/)",
		},
		{
			name: "already absolute is untouched",
			in:   "[url=https://example.com/x]x[/url]",
			want: "[x](https://example.com/x)",
		},
		{
			name: "protocol-relative is left alone",
			in:   "[Deal](//other.host/path)",
			want: "[Deal](//other.host/path)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BBCodeToMarkdown(c.in, origin); got != c.want {
				t.Errorf("BBCodeToMarkdown(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Agora's own paths must survive: the importer appends attachment links to the
// same description after conversion, and rewriting those onto the portal origin
// would break every imported file and video.
func TestBBCodeToMarkdownKeepsAgoraPaths(t *testing.T) {
	const origin = "https://salesdoc.bitrix24.kz"
	in := "[clip.mp4](/uploads/workspaces/ws-1/abc.mp4) and [dl](/api/attachments/7/download)"
	if got := BBCodeToMarkdown(in, origin); got != in {
		t.Errorf("Agora paths were rewritten: got %q, want %q", got, in)
	}
}

// A blank origin (Bitrix unconfigured, or an unparseable webhook URL) must leave
// the text exactly as it was rather than producing "](/x)" -> "](/x)" garbage.
func TestBBCodeToMarkdownWithoutOriginLeavesLinksRelative(t *testing.T) {
	in := "[Deal](/crm/deal/details/1/)"
	if got := BBCodeToMarkdown(in, ""); got != in {
		t.Errorf("got %q, want unchanged %q", got, in)
	}
}

func TestPortalOrigin(t *testing.T) {
	cases := map[string]string{
		"https://salesdoc.bitrix24.kz/rest/191/tok/": "https://salesdoc.bitrix24.kz",
		"https://salesdoc.bitrix24.kz/rest/191/tok":  "https://salesdoc.bitrix24.kz",
		"http://portal.local/rest/1/t/":              "http://portal.local",
		"":                                           "",
		"not a url":                                  "",
		"/rest/191/tok/":                             "",
	}
	for in, want := range cases {
		if got := PortalOrigin(in); got != want {
			t.Errorf("PortalOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}
