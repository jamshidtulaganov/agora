package bitrix

import (
	"regexp"
	"strings"
)

// Bitrix task descriptions + comments arrive as BBCode ([b], [list], [url], …).
// Agora renders Markdown, so without conversion the raw tags show literally.
// BBCodeToMarkdown maps the common Bitrix tags to Markdown; unknown tags are
// stripped. It is best-effort and never errors.
var (
	bbURLLabeled = regexp.MustCompile(`(?is)\[url=([^\]]+)\](.*?)\[/url\]`)
	bbURLBare    = regexp.MustCompile(`(?is)\[url\](.*?)\[/url\]`)
	bbUser       = regexp.MustCompile(`(?is)\[user=[^\]]*\](.*?)\[/user\]`)
	bbImg        = regexp.MustCompile(`(?is)\[img[^\]]*\](.*?)\[/img\]`)
	bbItem       = regexp.MustCompile(`(?i)\[\*\]`)
	bbListOpen   = regexp.MustCompile(`(?i)\[list[^\]]*\]`)
	bbListClose  = regexp.MustCompile(`(?i)\[/list\]`)
	bbQuoteOpen  = regexp.MustCompile(`(?i)\[quote[^\]]*\]`)
	bbQuoteClose = regexp.MustCompile(`(?i)\[/quote\]`)
	bbBr         = regexp.MustCompile(`(?i)\[br/?\]`)
	bbBold       = regexp.MustCompile(`(?i)\[/?b\]`)
	bbItalic     = regexp.MustCompile(`(?i)\[/?i\]`)
	bbStrike     = regexp.MustCompile(`(?i)\[/?s\]`)
	bbUnder      = regexp.MustCompile(`(?i)\[/?u\]`)
	bbCodeTag    = regexp.MustCompile(`(?i)\[/?code\]`)
	// Conservative: strip only known leftover bbcode tags, not arbitrary
	// "[word]" — otherwise it would eat the "[text]" of a converted markdown
	// link (RE2 has no lookahead to exclude a following "(").
	bbLeftover = regexp.MustCompile(`(?i)\[/?(?:color|size|font|center|right|left|justify|quote|code|table|thead|tbody|tr|td|th|spoiler|video|h[1-6]|sub|sup|hr|attach|attachment|cut|p)(=[^\]]*)?\]`)
	bbBlankLines = regexp.MustCompile(`\n{3,}`)
)

// BBCodeToMarkdown converts Bitrix BBCode to Markdown. Returns the input
// unchanged when it contains no brackets (the common short-comment case).
func BBCodeToMarkdown(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	// Content-bearing tags first (capture inner text / target).
	s = bbURLLabeled.ReplaceAllString(s, "[$2]($1)")
	s = bbURLBare.ReplaceAllString(s, "$1")
	s = bbUser.ReplaceAllString(s, "$1")
	s = bbImg.ReplaceAllString(s, "$1")
	// Structural tags.
	s = bbItem.ReplaceAllString(s, "\n- ")
	s = bbListOpen.ReplaceAllString(s, "\n")
	s = bbListClose.ReplaceAllString(s, "\n")
	s = bbQuoteOpen.ReplaceAllString(s, "\n> ")
	s = bbQuoteClose.ReplaceAllString(s, "\n")
	s = bbBr.ReplaceAllString(s, "\n")
	// Inline styling.
	s = bbBold.ReplaceAllString(s, "**")
	s = bbItalic.ReplaceAllString(s, "*")
	s = bbStrike.ReplaceAllString(s, "~~")
	s = bbUnder.ReplaceAllString(s, "")
	s = bbCodeTag.ReplaceAllString(s, "`")
	// Strip any remaining/unknown tags, then tidy whitespace.
	s = bbLeftover.ReplaceAllString(s, "")
	s = bbBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
