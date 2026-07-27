package handler

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The report body is AGENT-authored, so it is untrusted input. Escaping must
// happen before any markup is added, or a report could inject tags into a file
// the team opens.
func TestRenderReportBodyEscapesAgentText(t *testing.T) {
	got := renderReportBody("Backlog <script>alert(1)</script> grew & widened")
	if strings.Contains(got, "<script>") {
		t.Fatalf("agent text was not escaped: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") || !strings.Contains(got, "&amp;") {
		t.Errorf("expected escaped entities, got: %s", got)
	}
}

func TestRenderReportBodyTable(t *testing.T) {
	md := "| Name | Tasks | Share |\n|---|---|---|\n| Alisher | 596 | **24.7%** |\n| Bilol | 194 | 8.0% |"
	got := renderReportBody(md)

	for _, want := range []string{"<table>", "<th>Name</th>", "<td>596</td>", "<strong>24.7%</strong>", "</tbody>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The divider row must be consumed, never rendered as data.
	if strings.Contains(got, "<td>---</td>") {
		t.Error("table divider leaked into the body")
	}
	// One header row plus two data rows.
	if n := strings.Count(got, "<tr>"); n != 3 {
		t.Errorf("rows = %d, want 3 (header + 2 data)", n)
	}
}

func TestRenderReportBodyInline(t *testing.T) {
	t.Run("bold and code", func(t *testing.T) {
		got := renderReportBody("Open grew **+41** and `truncated: false`")
		if !strings.Contains(got, "<strong>+41</strong>") || !strings.Contains(got, "<code>truncated: false</code>") {
			t.Errorf("inline rendering failed: %s", got)
		}
	})

	// An unmatched ** must stay literal. Opening a tag with no close would
	// swallow the rest of the report into bold.
	t.Run("unmatched emphasis stays literal", func(t *testing.T) {
		got := renderReportBody("Backlog **grew a lot")
		if strings.Contains(got, "<strong>") {
			t.Errorf("unpaired ** opened a tag: %s", got)
		}
	})
}

func TestRenderReportBodyStructure(t *testing.T) {
	md := "# Report\n\n## Backlog\n\nText here.\n\n---\n\n- first\n- second"
	got := renderReportBody(md)

	// Headings shift down one level: the page already owns the H1.
	if !strings.Contains(got, "<h2>Report</h2>") || !strings.Contains(got, "<h3>Backlog</h3>") {
		t.Errorf("heading levels wrong: %s", got)
	}
	if !strings.Contains(got, "<hr>") {
		t.Error("missing rule")
	}
	if !strings.Contains(got, "<li>first</li>") || !strings.Contains(got, "<li>second</li>") {
		t.Errorf("list not rendered: %s", got)
	}
	if strings.Count(got, "<ul>") != 1 {
		t.Errorf("expected one list, got %d", strings.Count(got, "<ul>"))
	}
}

func TestRenderReportHTMLIsSelfContained(t *testing.T) {
	page := string(renderReportHTML("Weekly report", "## Backlog\n\nGrew by **41**."))

	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Error("not a standalone document")
	}
	// Opened straight from a Telegram attachment, often offline — anything
	// remote would simply fail to load.
	for _, remote := range []string{"http://", "https://", "<link", "<script"} {
		if strings.Contains(page, remote) {
			t.Errorf("page references something external (%q); it must be self-contained", remote)
		}
	}
	if !strings.Contains(page, "prefers-color-scheme") {
		t.Error("no dark-mode handling; the file is read on phones at night")
	}
	if !strings.Contains(page, "<strong>41</strong>") {
		t.Error("body was not rendered into the page")
	}
}

func TestReportCaption(t *testing.T) {
	t.Run("takes the first real sentence, not a heading", func(t *testing.T) {
		body := "## Bitrix Weekly\n\n### BACKLOG\n\nOxirgi 7 kunda **49** vazifa ochildi.\n\n| a | b |"
		got := reportCaption("Weekly report", body)
		if !strings.Contains(got, "<b>Weekly report</b>") {
			t.Errorf("title missing: %s", got)
		}
		if !strings.Contains(got, "Oxirgi 7 kunda 49 vazifa ochildi.") {
			t.Errorf("first sentence missing or emphasis not stripped: %s", got)
		}
		if strings.Contains(got, "**") || strings.Contains(got, "BACKLOG") {
			t.Errorf("caption carried markdown or a heading: %s", got)
		}
	})

	// Telegram caps captions in CHARACTERS, so the check is rune-based —
	// byte-limiting would halve the usable length of a Cyrillic caption.
	t.Run("stays within the Bot API cap", func(t *testing.T) {
		got := reportCaption("T", strings.Repeat("juda uzun gap ", 400))
		if n := utf8.RuneCountInString(got); n > telegramCaptionLimit {
			t.Fatalf("caption %d runes exceeds %d — the upload would be rejected", n, telegramCaptionLimit)
		}
	})

	t.Run("cyrillic caption is not cut in half by byte counting", func(t *testing.T) {
		got := reportCaption("Отчёт", strings.Repeat("задача выполнена ", 200))
		n := utf8.RuneCountInString(got)
		if n > telegramCaptionLimit {
			t.Fatalf("%d runes exceeds cap", n)
		}
		// A byte-based limit would land near 512 runes; rune-based should use
		// most of the allowance.
		if n < 900 {
			t.Errorf("cyrillic caption truncated to %d runes — byte counting leaked back in", n)
		}
		if !utf8.ValidString(got) {
			t.Error("caption cut mid-character")
		}
	})

	t.Run("body with no prose still yields a valid caption", func(t *testing.T) {
		got := reportCaption("Weekly report", "## Only headings\n\n---")
		if !strings.Contains(got, "Weekly report") {
			t.Errorf("caption lost its title: %s", got)
		}
	})
}
