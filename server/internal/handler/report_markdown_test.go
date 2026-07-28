package handler

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The report body is AGENT-authored, so it is untrusted input. Escaping must
// happen before any markup is added, or a report could inject tags into a file
// the team opens.
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
