package slack

import "testing"

// The link parser is the entire input to unfurling: Slack sends URLs and
// nothing else. A table test is the right shape because every row here is a
// string somebody will eventually paste into a channel, and the cost of a
// wrong answer is either a missing preview or — far worse — a preview of the
// wrong object.
func TestParseAgoraLink(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
		want AgoraLink
	}{
		{
			name: "canonical issue link (the shape slackIssueURL writes)",
			raw:  "https://app.agora.dev/mobile/issues/MUL-123",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "app.agora.dev", WorkspaceSlug: "mobile", Identifier: "MUL-123"},
		},
		{
			name: "issue link by uuid",
			raw:  "https://app.agora.dev/mobile/issues/6f1a3c22-0000-4000-8000-00000000abcd",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "app.agora.dev", WorkspaceSlug: "mobile", Identifier: "6f1a3c22-0000-4000-8000-00000000abcd"},
		},
		{
			name: "project link",
			raw:  "https://app.agora.dev/mobile/projects/6f1a3c22-0000-4000-8000-00000000abcd",
			ok:   true,
			want: AgoraLink{Kind: LinkKindProject, Host: "app.agora.dev", WorkspaceSlug: "mobile", Identifier: "6f1a3c22-0000-4000-8000-00000000abcd"},
		},
		{
			name: "trailing slash is what an address bar produces",
			raw:  "https://app.agora.dev/mobile/issues/MUL-1/",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "app.agora.dev", WorkspaceSlug: "mobile", Identifier: "MUL-1"},
		},
		{
			name: "query and fragment are not part of the identity",
			raw:  "https://app.agora.dev/mobile/issues/MUL-1?from=slack#comment-3",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "app.agora.dev", WorkspaceSlug: "mobile", Identifier: "MUL-1"},
		},
		{
			name: "host is lower-cased so the fence compares like with like",
			raw:  "https://APP.Agora.DEV/mobile/issues/MUL-1",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "app.agora.dev", WorkspaceSlug: "mobile", Identifier: "MUL-1"},
		},
		{
			name: "port survives, because the fence compares host:port",
			raw:  "http://localhost:3000/mobile/issues/MUL-1",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "localhost:3000", WorkspaceSlug: "mobile", Identifier: "MUL-1"},
		},
		{
			name: "percent-encoded slug decodes",
			raw:  "https://app.agora.dev/my%20team/issues/MUL-1",
			ok:   true,
			want: AgoraLink{Kind: LinkKindIssue, Host: "app.agora.dev", WorkspaceSlug: "my team", Identifier: "MUL-1"},
		},

		// Everything below must produce NO unfurl. Silence is the correct
		// answer for a URL we cannot resolve to exactly one object.
		{name: "workspace root", raw: "https://app.agora.dev/mobile"},
		{name: "issues index, no identifier", raw: "https://app.agora.dev/mobile/issues"},
		{name: "empty identifier", raw: "https://app.agora.dev/mobile/issues/"},
		{name: "one segment too deep", raw: "https://app.agora.dev/mobile/issues/MUL-1/edit"},
		{name: "a section this phase does not unfurl", raw: "https://app.agora.dev/mobile/settings/slack"},
		{name: "global route, no workspace", raw: "https://app.agora.dev/inbox"},
		{name: "not a URL", raw: "MUL-123"},
		{name: "relative reference has no host", raw: "/mobile/issues/MUL-1"},
		{name: "non-http scheme", raw: "mailto:someone@agora.dev"},
		{name: "javascript scheme", raw: "javascript:alert(1)//app.agora.dev/mobile/issues/MUL-1"},
		{name: "empty", raw: ""},
		{name: "whitespace", raw: "   "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ParseAgoraLink(test.raw)
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, test.ok, got)
			}
			if !test.ok {
				return
			}
			if got != test.want {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

// A segment long enough to be an attack (or a bug) never reaches a database
// lookup.
func TestParseAgoraLinkRefusesOversizedSegments(t *testing.T) {
	long := make([]byte, maxLinkSegment+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, ok := ParseAgoraLink("https://app.agora.dev/" + string(long) + "/issues/MUL-1"); ok {
		t.Fatal("an oversized slug must not parse")
	}
	if _, ok := ParseAgoraLink("https://app.agora.dev/mobile/issues/" + string(long)); ok {
		t.Fatal("an oversized identifier must not parse")
	}
}
