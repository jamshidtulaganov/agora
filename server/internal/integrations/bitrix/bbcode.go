package bitrix

import (
	"net/url"
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
	bbLeftover   = regexp.MustCompile(`(?i)\[/?(?:color|size|font|center|right|left|justify|quote|code|table|thead|tbody|tr|td|th|spoiler|video|h[1-6]|sub|sup|hr|attach|attachment|cut|p)(=[^\]]*)?\]`)
	bbBlankLines = regexp.MustCompile(`\n{3,}`)
	// Markdown link target that is root-relative ("](/crm/deal/details/19951/)").
	// Bitrix writes portal-internal links without an origin, so these arrive as
	// paths. Left as-is they look like app routes: clicking one in the desktop app
	// pushed "/workgroups/group/105/" into the renderer's router and produced a
	// 404 route error instead of opening the portal.
	mdRelativeLink = regexp.MustCompile(`\]\((/[^)\s]*)\)`)
)

// agoraOwnedPathPrefixes are paths that belong to Agora, not the Bitrix portal.
// The importer appends attachment links (/uploads/...) to the same description
// after conversion, and a Bitrix text could contain such a path literally —
// rewriting those onto the portal origin would break real attachments.
var agoraOwnedPathPrefixes = []string{"/uploads/", "/api/"}

// PortalOrigin extracts "scheme://host" from a Bitrix inbound-webhook base URL
// (https://<portal>.bitrix24.<tld>/rest/<user>/<token>/). Returns "" when the
// input is empty or unparseable, which callers treat as "leave links alone".
func PortalOrigin(webhookURL string) string {
	raw := strings.TrimSpace(webhookURL)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// AbsolutizePortalLinks rewrites root-relative markdown link targets onto the
// portal origin so they open in Bitrix instead of being resolved as app routes.
// A blank origin (Bitrix not configured) returns the input untouched.
//
// Exported because the backfill of already-imported content MUST use the same
// implementation as the importer. A second regex written for the migration would
// be free to drift from this one, and the two disagreeing is precisely the bug
// that would go unnoticed.
func AbsolutizePortalLinks(s, portalOrigin string) string {
	origin := strings.TrimRight(strings.TrimSpace(portalOrigin), "/")
	if origin == "" || !strings.Contains(s, "](/") {
		return s
	}
	return mdRelativeLink.ReplaceAllStringFunc(s, func(match string) string {
		sub := mdRelativeLink.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		path := sub[1]
		// "//host/path" is protocol-relative, already absolute enough.
		if strings.HasPrefix(path, "//") {
			return match
		}
		for _, prefix := range agoraOwnedPathPrefixes {
			if strings.HasPrefix(path, prefix) {
				return match
			}
		}
		return "](" + origin + path + ")"
	})
}

// BBCodeToMarkdown converts Bitrix BBCode to Markdown. portalOrigin is the
// "scheme://host" of the Bitrix portal (see PortalOrigin); it absolutizes the
// root-relative links Bitrix writes for portal-internal targets. Pass "" to skip
// that rewrite. Returns the input unchanged when it contains no brackets and no
// relative link (the common short-comment case).
func BBCodeToMarkdown(s, portalOrigin string) string {
	if !strings.Contains(s, "[") {
		return AbsolutizePortalLinks(s, portalOrigin)
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
	// Last: the [url=...] conversion above has produced the markdown links whose
	// targets need absolutizing.
	s = AbsolutizePortalLinks(s, portalOrigin)
	return strings.TrimSpace(s)
}
