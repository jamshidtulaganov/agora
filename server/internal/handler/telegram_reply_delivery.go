package handler

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// How an agent's group reply reaches Telegram: as message text, or as an
// attached HTML document.
//
// Telegram renders no markdown table. A reply containing one, pasted as message
// text, arrives as raw pipes and dashes — which is exactly what a question like
// "how many tasks per month" produces, so the most useful answers were the ones
// that arrived unreadable. Those replies go out as a spreadsheet instead: the
// numbers stay sortable and summable, which is what happens to a per-month or
// per-person breakdown the moment someone wants to act on it.
//
// Short prose stays a message on purpose. Turning "2417 ta, yil boshidan"
// into a file the reader must download and open is worse than the pipe soup —
// a chat where every answer is an attachment stops being a chat.

// telegramInlineLimit is the length past which a reply is attached instead of
// posted inline. Well under the Bot API's 4096-character cap: the cutoff is
// about what a person will read in a group without scrolling, not about what
// the API accepts.
const telegramInlineLimit = 900

// replyNeedsDocument reports whether a reply must go out as an attachment.
func replyNeedsDocument(reply string) bool {
	if utf8.RuneCountInString(visibleText(reply)) > telegramInlineLimit {
		return true
	}
	return containsMarkdownTable(reply)
}

// markdownLinkTarget matches the `](url)` half of a markdown link.
var markdownLinkTarget = regexp.MustCompile(`\]\((?:https?://)[^)\s]*\)`)

// visibleText approximates what the reader actually sees, which is what the
// length limit is about.
//
// A message quoting eight Bitrix tasks carries eight ~70-character URLs that
// Telegram never shows — it renders the label. Counting them pushed a six-line
// daily pulse over the limit and turned it into a PDF attachment, which cost
// the thing the message existed for: the tags stopped notifying anyone and the
// links stopped being tappable in the chat.
func visibleText(md string) string {
	return markdownLinkTarget.ReplaceAllString(md, "]")
}

// containsMarkdownTable looks for a header row followed by a |---|---| divider.
// The divider is what makes it a table rather than a line that happens to have
// a pipe in it, and it is the same signal the HTML renderer keys on — so a
// reply detected here is one the renderer will actually turn into a table.
func containsMarkdownTable(text string) bool {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "|") && isTableDivider(lines[i+1]) {
			return true
		}
	}
	return false
}

// replyDocumentFilename names the attachment after the minute it was produced.
// Several answers in one chat otherwise arrive as indistinguishable files, and
// a reader scrolling back cannot tell which question a file answered.
func replyDocumentFilename(now time.Time) string {
	return "javob-" + now.Format("2006-01-02-1504") + ".pdf"
}

// replyDocumentTitle names the worksheet. Kept generic — the agent's own first
// line carries the subject, and inventing a title here would put words in its
// mouth. Excel caps a sheet name at 31 characters; the writer enforces it.
func replyDocumentTitle(now time.Time) string {
	return "Javob — " + now.Format("02.01.2006 15:04")
}

// replyCaption is the text shown beside the attachment: the reply's first
// substantive line, so the chat conveys the answer without anyone opening the
// file. Most readers never will.
//
// Headings, table rows, list markers and rules are skipped — none of them read
// as a sentence, and a caption showing "|---|---|" is worse than none.
func replyCaption(reply string) string {
	for _, line := range strings.Split(reply, "\n") {
		t := strings.TrimSpace(line)
		// List markers require the trailing space. Without it "**Backlog o'sdi**"
		// — a bold opening line, which is how these replies usually start — is
		// mistaken for a bullet and skipped, leaving the caption empty.
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "|") ||
			strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") ||
			strings.HasPrefix(t, "> ") || strings.Trim(t, "-") == "" {
			continue
		}
		// Telegram captions take HTML, not markdown; strip the emphasis markers
		// rather than letting them show up as literal asterisks.
		t = strings.ReplaceAll(t, "**", "")
		t = strings.ReplaceAll(t, "`", "")
		return truncateCaption(t)
	}
	return "Javob ilova qilingan faylda."
}

// truncateCaption trims to the Bot API caption cap. Counted in RUNES: Telegram
// counts characters, so byte-limiting would cut an Uzbek or Russian caption at
// roughly half its allowed length.
func truncateCaption(text string) string {
	if utf8.RuneCountInString(text) <= telegramCaptionLimit {
		return text
	}
	return truncateRunes(text, telegramCaptionLimit-1) + "…"
}
