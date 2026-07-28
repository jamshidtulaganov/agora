package handler

import (
	"strings"
	"testing"
	"time"
)

func TestReplyNeedsDocumentForTables(t *testing.T) {
	// The regression this exists for: a per-month breakdown arrived in the
	// group as raw pipes because Telegram renders no markdown table.
	table := "Shu yil 2417 ta vazifa ochildi.\n\n| Oy | Yaratildi |\n|---|---|\n| Yanvar | 360 |\n"
	if !replyNeedsDocument(table) {
		t.Fatal("a reply containing a table must be attached, not posted inline")
	}
}

func TestReplyStaysInlineWhenShortProse(t *testing.T) {
	// A chat where every answer is a downloadable file stops being a chat.
	if replyNeedsDocument("2417 ta — yil boshidan bugungacha.") {
		t.Fatal("short prose must stay a message")
	}
	// A pipe without a divider is not a table; it is a sentence with a pipe.
	if replyNeedsDocument("Filtr: status | assignee bo'yicha ishlaydi.") {
		t.Fatal("a stray pipe must not be mistaken for a table")
	}
}

func TestReplyNeedsDocumentCountsRunes(t *testing.T) {
	// Cyrillic and Uzbek text is multi-byte. Counting bytes would attach at
	// roughly half the intended length, turning ordinary answers into files.
	long := strings.Repeat("о", telegramInlineLimit-10)
	if replyNeedsDocument(long) {
		t.Fatal("a reply under the rune limit must stay inline")
	}
	if !replyNeedsDocument(strings.Repeat("о", telegramInlineLimit+1)) {
		t.Fatal("a reply over the rune limit must be attached")
	}
}

func TestReplyCaptionPicksTheFirstSentence(t *testing.T) {
	// Most readers never open the attachment, so the caption has to carry the
	// answer. A caption of "|---|---|" would be worse than none.
	reply := "# Hisobot\n\n| a | b |\n|---|---|\n\n**Backlog 41 taga o'sdi** — nisbat 6:1.\n"
	got := replyCaption(reply)
	if got != "Backlog 41 taga o'sdi — nisbat 6:1." {
		t.Fatalf("got %q", got)
	}
}

func TestReplyCaptionFallsBackWhenNothingReadable(t *testing.T) {
	// A reply that is nothing but a table still needs a caption; an empty one
	// makes the attachment look like a failed send.
	if got := replyCaption("| a | b |\n|---|---|\n| 1 | 2 |"); got == "" {
		t.Fatal("caption must never be empty")
	}
}

func TestReplyCaptionRespectsTheRuneCap(t *testing.T) {
	got := replyCaption(strings.Repeat("о", telegramCaptionLimit*2))
	if n := len([]rune(got)); n > telegramCaptionLimit {
		t.Fatalf("caption is %d runes, over the %d cap", n, telegramCaptionLimit)
	}
}

func TestReplyDocumentFilenameIsDistinct(t *testing.T) {
	// Several answers in one chat must not arrive as indistinguishable files.
	a := replyDocumentFilename(time.Date(2026, 7, 28, 11, 36, 0, 0, time.UTC))
	b := replyDocumentFilename(time.Date(2026, 7, 28, 14, 2, 0, 0, time.UTC))
	if a == b {
		t.Fatalf("filenames collide: %s", a)
	}
	if !strings.HasSuffix(a, ".html") {
		t.Fatalf("got %s, want an .html attachment", a)
	}
}
