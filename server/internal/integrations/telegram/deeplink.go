package telegram

import (
	"encoding/base64"
	"strings"
)

// Mini App deep links. A Mini App is opened via
// https://t.me/<bot>/<shortName>?startapp=<param>, where <param> is restricted
// to [A-Za-z0-9_-] and at most 64 chars. We base64url-encode (no padding) a
// compact "i:<issueID>" string so the bot can DM a button that lands the user
// on a specific issue. The SPA reads the raw value from
// window.Telegram.WebApp.initDataUnsafe.start_param and decodes it.

// miniAppStartIssuePrefix tags a start_param that names an issue to open.
const miniAppStartIssuePrefix = "i:"

// MiniAppStartParam encodes an "open issue <issueID> in workspace <wsSlug>"
// deep-link payload as base64url of "i:<wsSlug>:<issueID>" (or "i:<issueID>"
// when wsSlug is empty, which the SPA still decodes for backward compatibility).
// The workspace slug lets the SPA switch to the issue's workspace before loading
// it — the backend scopes issue lookups to the active workspace, so a deep link
// to an issue in a different workspace would otherwise 404. ~within Telegram's
// 64-char start_param limit. Opaque-but-decodable, not encrypted: it only names
// an issue the recipient is already authorized to see.
func MiniAppStartParam(wsSlug, issueID string) string {
	payload := miniAppStartIssuePrefix + issueID
	if wsSlug != "" {
		payload = miniAppStartIssuePrefix + wsSlug + ":" + issueID
	}
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// MiniAppLink builds a Mini App deep link that opens the app at startParam.
// Two launch surfaces are supported, chosen by whether shortName is set:
//
//   - Named app (@BotFather /newapp):   https://t.me/<bot>/<shortName>?startapp=<param>
//   - Main App   (@BotFather Main App): https://t.me/<bot>?startapp=<param>
//
// Only the bot username is required (the Main App needs no short name), so a
// deployment that configured a Main App still gets a tappable button. Returns ""
// only when botUsername is unset (the caller then sends a plain text DM). A
// leading "@" on botUsername is tolerated.
func MiniAppLink(botUsername, shortName, startParam string) string {
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	shortName = strings.TrimSpace(shortName)
	if botUsername == "" {
		return ""
	}
	link := "https://t.me/" + botUsername
	if shortName != "" {
		link += "/" + shortName
	}
	if startParam != "" {
		link += "?startapp=" + startParam
	}
	return link
}
