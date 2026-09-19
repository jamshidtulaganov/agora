package slack

// Block Kit builders.
//
// A Block is a map rather than a struct on purpose: Block Kit reuses the same
// JSON keys for differently-typed values across block types (a `section`'s
// "text" is a text object, a `markdown` block's "text" is a raw string), so one
// Go struct cannot model both without lying about one of them. The builders
// below are the only supported way to construct one, and each enforces the
// documented limit for its type — a payload Slack rejects with
// `invalid_blocks` is indistinguishable, from the user's seat, from a
// notification we never sent.
//
// Limits are from docs.slack.dev (Block Kit reference, verified 2026-09-19).

import (
	"strings"
	"unicode/utf8"
)

// Block Kit size limits we enforce at construction.
const (
	// MaxBlocksPerMessage is Slack's cap for a message payload (modals and
	// Home tabs allow 100; we only post messages in Phase 1).
	MaxBlocksPerMessage = 50
	// MaxSectionTextChars caps a section's text.
	MaxSectionTextChars = 3000
	// MaxSectionFields / MaxFieldChars cap a section's two-column field list.
	MaxSectionFields = 10
	MaxFieldChars    = 2000
	// MaxHeaderChars caps a header block's plain-text title.
	MaxHeaderChars = 150
	// MaxContextElements caps the small grey line under a card.
	MaxContextElements = 10
	// MaxMarkdownChars is the cumulative limit for ALL markdown blocks in one
	// payload — a Phase 2 report artifact is delivered as itself, and this is
	// the budget it has to fit in.
	MaxMarkdownChars = 12000
	// MaxButtonTextChars caps a button label.
	MaxButtonTextChars = 75
)

// Block is one Block Kit block, ready to marshal.
type Block map[string]any

// TextObject kinds.
const (
	textPlain  = "plain_text"
	textMrkdwn = "mrkdwn"
)

// PlainText builds a plain_text object. Emoji are rendered (`:rocket:` works)
// which is Slack's default and what every card here wants.
func PlainText(text string) map[string]any {
	return map[string]any{"type": textPlain, "text": text, "emoji": true}
}

// Mrkdwn builds a mrkdwn text object — Slack's own flavour (`*bold*`,
// `<url|label>`), not CommonMark. Use MarkdownBlock for real markdown.
func Mrkdwn(text string) map[string]any {
	return map[string]any{"type": textMrkdwn, "text": text}
}

// HeaderBlock is the large title line at the top of a card.
func HeaderBlock(text string) Block {
	return Block{"type": "header", "text": PlainText(Truncate(text, MaxHeaderChars))}
}

// SectionBlock is the workhorse: one mrkdwn paragraph.
func SectionBlock(text string) Block {
	return Block{"type": "section", "text": Mrkdwn(Truncate(text, MaxSectionTextChars))}
}

// SectionFields renders up to MaxSectionFields mrkdwn cells in two columns —
// the compact "Status / Assignee / Priority / Project" grid an issue card
// wants. Extra fields are dropped rather than overflowing into a rejected
// payload; callers with more than ten facts should be summarising, not paging.
func SectionFields(fields ...string) Block {
	if len(fields) > MaxSectionFields {
		fields = fields[:MaxSectionFields]
	}
	out := make([]any, 0, len(fields))
	for _, f := range fields {
		out = append(out, Mrkdwn(Truncate(f, MaxFieldChars)))
	}
	return Block{"type": "section", "fields": out}
}

// ContextBlock is the small grey line under a card.
func ContextBlock(items ...string) Block {
	if len(items) > MaxContextElements {
		items = items[:MaxContextElements]
	}
	elements := make([]any, 0, len(items))
	for _, item := range items {
		elements = append(elements, Mrkdwn(Truncate(item, MaxFieldChars)))
	}
	return Block{"type": "context", "elements": elements}
}

// DividerBlock is a horizontal rule.
func DividerBlock() Block {
	return Block{"type": "divider"}
}

// MarkdownBlock carries real markdown (headings, tables, task lists, fenced
// code) and is messages-only. Phase 2 delivers a report artifact through this
// block instead of hand-translating it into sections.
func MarkdownBlock(text string) Block {
	return Block{"type": "markdown", "text": Truncate(text, MaxMarkdownChars)}
}

// LinkButton is an "Open in Agora" style button. URL buttons carry no action
// payload, so they need no interactivity endpoint — which is why Phase 1 cards
// can have one at all.
func LinkButton(label, url string) map[string]any {
	return map[string]any{
		"type": "button",
		"text": PlainText(Truncate(label, MaxButtonTextChars)),
		"url":  url,
	}
}

// ActionsBlock holds interactive elements (Phase 1: link buttons only).
func ActionsBlock(elements ...map[string]any) Block {
	out := make([]any, 0, len(elements))
	for _, el := range elements {
		out = append(out, el)
	}
	return Block{"type": "actions", "elements": out}
}

// ClampBlocks enforces the per-message block cap. Truncating the tail is the
// right failure: the header and the first sections carry the identity and the
// point, and the alternative is Slack rejecting the whole message.
func ClampBlocks(blocks []Block) []Block {
	if len(blocks) <= MaxBlocksPerMessage {
		return blocks
	}
	return blocks[:MaxBlocksPerMessage]
}

// Truncate shortens text to max runes, marking the cut with an ellipsis so a
// reader can tell the difference between "short" and "trimmed". Rune-based
// because Slack counts characters, and a byte slice would split a multi-byte
// rune and produce invalid UTF-8 in the payload.
func Truncate(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	if max == 1 {
		return "…"
	}
	var sb strings.Builder
	count := 0
	for _, r := range text {
		if count >= max-1 {
			break
		}
		sb.WriteRune(r)
		count++
	}
	return sb.String() + "…"
}
