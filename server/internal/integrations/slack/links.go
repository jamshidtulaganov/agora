package slack

// Agora link parsing for unfurls (docs/slack-integration-plan.md §Phase 1 —
// "Unfurl").
//
// Slack sends `link_shared` with the URLs it found in a message and nothing
// else — not the surrounding text, not the message's other content. So the
// only input to "which Agora object is this?" is the URL string, and the only
// honest way to answer it is a pure function that recognises exactly the two
// canonical shapes and refuses everything else:
//
//	https://<app host>/{workspaceSlug}/issues/{IDENT}
//	https://<app host>/{workspaceSlug}/projects/{id}
//
// Canonical is not a coincidence: slackIssueURL (handler/slack_notify.go)
// builds precisely this shape, so a link copied out of a Slack notification
// unfurls when a teammate pastes it back.
//
// The parser is deliberately strict — exactly three path segments, a known
// middle segment, a non-empty identifier. A URL it does not recognise produces
// no unfurl at all, which is the right failure: an unfurl we cannot back with
// a resolved object would either be wrong or would leak the fact that a slug
// exists.
//
// Host checking is NOT done here. The parser reports the host it saw and the
// caller compares it against the deployment's own origin, because only the
// handler knows what that is.

import (
	"net/url"
	"strings"
)

// LinkKind is what an Agora URL points at.
type LinkKind string

const (
	// LinkKindIssue is /{slug}/issues/{IDENT-or-uuid}.
	LinkKindIssue LinkKind = "issue"
	// LinkKindProject is /{slug}/projects/{id}.
	LinkKindProject LinkKind = "project"
)

// AgoraLink is one recognised Agora URL.
type AgoraLink struct {
	Kind LinkKind
	// Host is the URL's host (lower-cased, port included when present). The
	// caller fences on it; the parser only reports it.
	Host string
	// WorkspaceSlug is the first path segment.
	WorkspaceSlug string
	// Identifier is the last path segment: an issue key (MUL-123) or a UUID
	// for an issue, a UUID for a project.
	Identifier string
}

// maxLinkSegment bounds each path segment. A slug or an identifier longer than
// this is not one of ours, and refusing early keeps a hostile URL from
// reaching a database lookup.
const maxLinkSegment = 128

// ParseAgoraLink recognises a canonical Agora issue or project URL.
//
// Returns ok=false for anything else — a different path shape, an unknown
// section, a non-HTTP scheme, an empty segment. Query strings and fragments
// are ignored (a link with `?from=slack` is the same link), and one trailing
// slash is tolerated because that is what a browser's address bar produces.
func ParseAgoraLink(raw string) (AgoraLink, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 2048 {
		return AgoraLink{}, false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return AgoraLink{}, false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		// mailto:, javascript:, or a relative reference. None of them is a
		// link Slack would have unfurled to us, and none is resolvable.
		return AgoraLink{}, false
	}
	if u.Host == "" {
		return AgoraLink{}, false
	}

	// u.Path is already percent-decoded, which is what we want: a slug posted
	// as %41gora is the same workspace.
	segments := []string{}
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if seg = strings.TrimSpace(seg); seg != "" {
			segments = append(segments, seg)
		}
	}
	if len(segments) != 3 {
		return AgoraLink{}, false
	}
	for _, seg := range segments {
		if len(seg) > maxLinkSegment {
			return AgoraLink{}, false
		}
	}

	var kind LinkKind
	switch segments[1] {
	case "issues":
		kind = LinkKindIssue
	case "projects":
		kind = LinkKindProject
	default:
		// /{slug}/settings/… , /{slug}/agents/… — real Agora URLs that this
		// phase does not unfurl. Silence, not a guess.
		return AgoraLink{}, false
	}

	return AgoraLink{
		Kind:          kind,
		Host:          strings.ToLower(u.Host),
		WorkspaceSlug: segments[0],
		Identifier:    segments[2],
	}, true
}
