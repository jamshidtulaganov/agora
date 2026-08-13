package telegram

import (
	"context"
	"html"
	"regexp"
	"strings"
)

// Markdown → Telegram HTML.
//
// Agents write markdown, because that is what every other surface they post to
// takes. Telegram takes HTML, and a narrow subset of it: bold, italic,
// underline, strike, code, pre, links, blockquote. No headings, no tables, no
// lists. Sending markdown raw means readers see literal `**` around the number
// that mattered.
//
// Everything is HTML-escaped FIRST, so agent-authored text can never inject
// markup, and the tags below are added afterwards. The alternative — escaping
// after conversion — would strip the tags we just produced.
//
// Unsupported constructs degrade rather than break: a heading becomes bold, a
// bullet becomes "• ", a table row is left alone. A partial renderer that is
// honest about its scope beats a half-correct general one; the same choice the
// spreadsheet renderer makes.

var (
	// Code first, so emphasis inside a code span is left literal.
	tgCodeBlock = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\n?(.*?)```")
	tgInlineRe  = regexp.MustCompile("`([^`\n]+)`")
	// Bold before italic: `**x**` must not be read as two italic markers.
	tgBoldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	// Italic requires a non-space neighbour so `2 * 3 * 4` stays arithmetic.
	tgItalicRe = regexp.MustCompile(`(^|[\s(])[*_]([^\s*_][^*_\n]*?)[*_]($|[\s).,!?:;])`)
	tgLinkRe   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	tgHeadRe   = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+(.+)$`)
	tgBulletRe = regexp.MustCompile(`(?m)^(\s*)[-*]\s+`)
	// No backreference: Go's RE2 has none, so the three rule spellings are
	// spelled out.
	tgRuleRe = regexp.MustCompile(`(?m)^\s*(?:-{3,}|\*{3,}|_{3,})\s*$`)
)

// codeSpanPlaceholder keeps converted code out of later passes. A bold marker
// inside a code span is content, not formatting.
const codeSpanPlaceholder = "\x00tgcode%d\x00"

// MarkdownToHTML converts the markdown agents write into Telegram HTML.
func MarkdownToHTML(md string) string {
	text := strings.ReplaceAll(md, "\r\n", "\n")

	// Extract code first, on the RAW text, then escape each piece separately —
	// otherwise the escaping pass would mangle the delimiters we match on.
	var codes []string
	stash := func(inner string) string {
		codes = append(codes, inner)
		return strings.Replace(codeSpanPlaceholder, "%d", itoa(len(codes)-1), 1)
	}
	text = tgCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		return stash("<pre>" + html.EscapeString(tgCodeBlock.FindStringSubmatch(m)[1]) + "</pre>")
	})
	text = tgInlineRe.ReplaceAllStringFunc(text, func(m string) string {
		return stash("<code>" + html.EscapeString(tgInlineRe.FindStringSubmatch(m)[1]) + "</code>")
	})

	text = html.EscapeString(text)

	// A rule has no Telegram equivalent; an em-dash line reads as the break it
	// was meant to be, where "---" would just look like leftover markup.
	text = tgRuleRe.ReplaceAllString(text, "———")
	// Headings become bold: Telegram has no heading, and dropping them would
	// lose the report's structure entirely.
	text = tgHeadRe.ReplaceAllString(text, "<b>$1</b>")
	text = tgLinkRe.ReplaceAllString(text, `<a href="$2">$1</a>`)
	text = tgBoldRe.ReplaceAllString(text, "<b>$1</b>")
	text = tgItalicRe.ReplaceAllString(text, "$1<i>$2</i>$3")
	text = tgBulletRe.ReplaceAllString(text, "$1• ")

	for i, code := range codes {
		text = strings.Replace(text, strings.Replace(codeSpanPlaceholder, "%d", itoa(i), 1), code, 1)
	}
	return text
}

// itoa avoids pulling strconv in for one digit-heavy helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// SendMarkdown delivers agent-authored markdown as Telegram HTML.
//
// Separate from SendMessage, which stays plain: the OTP login path must never
// have its code reinterpreted as formatting.
func (c *BotClient) SendMarkdown(ctx context.Context, chatID, md string) error {
	return c.sendMessage(ctx, sendMessageRequest{
		ChatID:    chatID,
		Text:      MarkdownToHTML(md),
		ParseMode: "HTML",
	})
}

// SendMarkdownReply is SendMarkdown with an exact Telegram reply target. The
// target is captured when the Agora task is enqueued, so concurrent questions
// in one group cannot cross their answers.
func (c *BotClient) SendMarkdownReply(ctx context.Context, chatID, md string, replyTo int64) error {
	req := sendMessageRequest{
		ChatID:    chatID,
		Text:      MarkdownToHTML(md),
		ParseMode: "HTML",
	}
	if replyTo > 0 {
		req.ReplyParameters = &replyParameters{
			MessageID: replyTo, AllowSendingWithoutReply: true,
		}
	}
	return c.sendMessage(ctx, req)
}
