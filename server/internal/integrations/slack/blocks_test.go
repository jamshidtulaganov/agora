package slack

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSectionBlock_Shape(t *testing.T) {
	raw, err := json.Marshal(SectionBlock("*MUL-1* failed"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["type"] != "section" {
		t.Fatalf("type = %v", got["type"])
	}
	text, ok := got["text"].(map[string]any)
	if !ok {
		t.Fatalf("text is not an object: %v", got["text"])
	}
	if text["type"] != "mrkdwn" || text["text"] != "*MUL-1* failed" {
		t.Fatalf("text = %v", text)
	}
}

func TestHeaderBlock_UsesPlainText(t *testing.T) {
	block := HeaderBlock("Sprint report")
	text, ok := block["text"].(map[string]any)
	if !ok {
		t.Fatalf("text is not an object: %v", block["text"])
	}
	// A header block only accepts plain_text; sending mrkdwn there is rejected
	// with invalid_blocks and the whole message is lost.
	if text["type"] != "plain_text" {
		t.Fatalf("header text type = %v", text["type"])
	}
}

func TestMarkdownBlock_UsesRawString(t *testing.T) {
	// The markdown block's "text" is a raw string, not a text object — the
	// difference the map-based Block type exists to allow.
	block := MarkdownBlock("# Sprint\n\n| a | b |\n| - | - |\n")
	if _, isString := block["text"].(string); !isString {
		t.Fatalf("markdown text must be a raw string, got %T", block["text"])
	}
}

func TestBlockCaps(t *testing.T) {
	long := strings.Repeat("x", 20000)

	if got := SectionBlock(long)["text"].(map[string]any)["text"].(string); utf8.RuneCountInString(got) > MaxSectionTextChars {
		t.Fatalf("section text = %d runes, cap %d", utf8.RuneCountInString(got), MaxSectionTextChars)
	}
	if got := HeaderBlock(long)["text"].(map[string]any)["text"].(string); utf8.RuneCountInString(got) > MaxHeaderChars {
		t.Fatalf("header text = %d runes, cap %d", utf8.RuneCountInString(got), MaxHeaderChars)
	}
	if got := MarkdownBlock(long)["text"].(string); utf8.RuneCountInString(got) > MaxMarkdownChars {
		t.Fatalf("markdown text = %d runes, cap %d", utf8.RuneCountInString(got), MaxMarkdownChars)
	}

	fields := make([]string, 25)
	for i := range fields {
		fields[i] = "field"
	}
	if got := SectionFields(fields...)["fields"].([]any); len(got) > MaxSectionFields {
		t.Fatalf("fields = %d, cap %d", len(got), MaxSectionFields)
	}

	items := make([]string, 25)
	for i := range items {
		items[i] = "item"
	}
	if got := ContextBlock(items...)["elements"].([]any); len(got) > MaxContextElements {
		t.Fatalf("context elements = %d, cap %d", len(got), MaxContextElements)
	}

	blocks := make([]Block, 120)
	for i := range blocks {
		blocks[i] = DividerBlock()
	}
	if got := ClampBlocks(blocks); len(got) != MaxBlocksPerMessage {
		t.Fatalf("clamped to %d blocks, want %d", len(got), MaxBlocksPerMessage)
	}
	if got := ClampBlocks(blocks[:3]); len(got) != 3 {
		t.Fatalf("a short message must be left alone, got %d blocks", len(got))
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		// Rune-safe: cutting on bytes would emit invalid UTF-8 into the payload.
		{"тестовое сообщение", 5, "тест…"},
		{"🚀🚀🚀🚀", 2, "🚀…"},
	}
	for _, tc := range tests {
		if got := Truncate(tc.in, tc.max); got != tc.want {
			t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
	if !utf8.ValidString(Truncate("тестовое", 3)) {
		t.Fatal("truncation produced invalid UTF-8")
	}
}

func TestLinkButton(t *testing.T) {
	btn := LinkButton("Open in Agora", "https://agora.dev/acme/issues/MUL-1")
	if btn["type"] != "button" || btn["url"] == "" {
		t.Fatalf("unexpected button: %v", btn)
	}
	// A URL button carries no action_id payload, so Phase 1 needs no
	// interactivity endpoint to ship one.
	if _, hasAction := btn["action_id"]; hasAction {
		t.Fatal("Phase 1 buttons must not require an interactivity endpoint")
	}
}
