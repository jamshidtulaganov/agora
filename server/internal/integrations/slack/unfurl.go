package slack

// chat.unfurl — the read-only half of Phase 1 (docs/slack-integration-plan.md
// §Phase 1 "Unfurl").
//
// Three properties of chat.unfurl shape this file:
//
//   - It REPLACES rather than appends, so a Slack retry of the same event is
//     naturally idempotent and Phase 1 needs no dedup table. (Phase 3, whose
//     actions create rows, does.)
//   - `user_auth_required` + `user_auth_url` is Slack's own sanctioned way to
//     say "link your account to see this". It renders privately, to the one
//     person who posted the link, and leaks nothing into the channel — which
//     is why it is both the safe answer for a link we will not show AND the
//     growth loop.
//   - Unfurls support Block Kit but NOT rich_text elements, so the card below
//     is header + context + actions and nothing exotic.
//
// What the card deliberately does not carry: the issue description. A
// description is the part of an issue most likely to hold a customer name, a
// credential or an internal argument, and an unfurl renders to everyone in the
// channel — including people who are not Agora users at all. Identifier,
// title, status, assignee, priority and project are enough to answer "what is
// this link?" and are the facts a teammate would have read out loud anyway.

import (
	"context"
	"strings"
	"unicode"
)

// Unfurl is the preview for one URL: a small Block Kit stack.
type Unfurl struct {
	Blocks []Block `json:"blocks,omitempty"`
}

// UnfurlRequest is the chat.unfurl payload.
//
// Unfurls is keyed by the URL exactly as Slack reported it in link_shared —
// a re-normalised key does not match and the unfurl silently does not appear.
type UnfurlRequest struct {
	Channel string            `json:"channel"`
	TS      string            `json:"ts"`
	Unfurls map[string]Unfurl `json:"unfurls"`
	// UserAuthRequired asks Slack to show the poster a private "connect your
	// account" prompt instead of a preview. Set for a link we will not show:
	// the Slack user is not linked to an Agora account, is not a member of
	// the workspace, or the issue-visibility gate says no.
	UserAuthRequired bool   `json:"user_auth_required,omitempty"`
	UserAuthURL      string `json:"user_auth_url,omitempty"`
	UserAuthMessage  string `json:"user_auth_message,omitempty"`
}

// UnfurlResult is chat.unfurl's response: the envelope and nothing else.
type UnfurlResult struct {
	slackResponse
}

// Unfurl attaches previews to the links in one message.
//
// Not rate-limited "Special" like chat.postMessage (it is an ordinary tier),
// and — like every other method Phase 1 touches — it is not one of the two
// methods the 2025 unlisted-app limits throttle. It still goes through the
// same `ok`-checked decode as every other call, so `ok:false` arrives as an
// *APIError carrying Slack's own code.
func (c *APIClient) Unfurl(ctx context.Context, token string, req UnfurlRequest) (UnfurlResult, error) {
	if req.Unfurls == nil {
		// Slack requires the field. An auth-prompt call legitimately carries
		// no previews, and `null` is rejected where `{}` is accepted.
		req.Unfurls = map[string]Unfurl{}
	}
	var out UnfurlResult
	err := c.call(ctx, "chat.unfurl", token, req, &out, &out.slackResponse)
	return out, err
}

// ---------------------------------------------------------------------------
// Cards
// ---------------------------------------------------------------------------

// AssigneeKind distinguishes the three assignee shapes so the card can keep an
// agent visually distinct, the way every other Agora surface does.
type AssigneeKind string

const (
	AssigneeMember AssigneeKind = "member"
	AssigneeAgent  AssigneeKind = "agent"
	AssigneeSquad  AssigneeKind = "squad"
)

// IssueUnfurl is the fact set an issue card renders. Every field is optional
// except Identifier: a card missing its status is a card with one fewer line,
// never a failed unfurl.
//
// There is no Description field, and adding one is a product decision, not a
// refactor — see the file header.
type IssueUnfurl struct {
	Identifier string
	Title      string
	URL        string
	Status     string
	Priority   string
	Assignee   string
	// AssigneeKind decides the marker in front of the assignee name.
	AssigneeKind AssigneeKind
	Project      string
	// Archived marks an issue that is out of the active board. Shown because
	// a link to an archived issue otherwise reads as current work.
	Archived bool
}

// maxUnfurlTitleChars keeps the header inside Slack's plain-text cap once the
// identifier and separator are accounted for.
const maxUnfurlTitleChars = MaxHeaderChars - 24

// IssueUnfurlBlocks renders the compact issue card:
//
//	[ header  ] MUL-123 · Retry the failed payment webhook
//	[ context ] *Status* In progress · *Assignee* 🤖 mobile-dev · *Priority* High · *Project* Mobile
//	[ actions ] [ Open in Agora ]
//
// The button is a URL button, so it carries no action payload and needs no
// interactivity endpoint — which is why a Phase 1 unfurl can have one at all.
func IssueUnfurlBlocks(u IssueUnfurl) []Block {
	header := strings.TrimSpace(u.Identifier)
	if title := strings.TrimSpace(u.Title); title != "" {
		if header != "" {
			header += " · "
		}
		header += Truncate(title, maxUnfurlTitleChars)
	}
	if header == "" {
		// Nothing to identify the object with: no card. The caller treats a
		// nil block list as "do not unfurl this link".
		return nil
	}

	blocks := []Block{HeaderBlock(header)}

	facts := []string{}
	if status := HumanizeToken(u.Status); status != "" {
		facts = append(facts, "*Status* "+status)
	}
	if assignee := strings.TrimSpace(u.Assignee); assignee != "" {
		facts = append(facts, "*Assignee* "+assigneeMarker(u.AssigneeKind)+assignee)
	}
	if priority := HumanizeToken(u.Priority); priority != "" {
		facts = append(facts, "*Priority* "+priority)
	}
	if project := strings.TrimSpace(u.Project); project != "" {
		facts = append(facts, "*Project* "+project)
	}
	if u.Archived {
		facts = append(facts, "*Archived*")
	}
	if len(facts) > 0 {
		blocks = append(blocks, ContextBlock(strings.Join(facts, "  ·  ")))
	}
	if url := strings.TrimSpace(u.URL); url != "" {
		blocks = append(blocks, ActionsBlock(LinkButton("Open in Agora", url)))
	}
	return ClampBlocks(blocks)
}

// ProjectUnfurl is the fact set a project card renders. Name and link only:
// a project's description is long-form context written for the team, and the
// same leak argument that keeps an issue description out of a channel keeps a
// project description out of one.
type ProjectUnfurl struct {
	Name   string
	URL    string
	Status string
}

// ProjectUnfurlBlocks renders the project card.
func ProjectUnfurlBlocks(p ProjectUnfurl) []Block {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil
	}
	blocks := []Block{HeaderBlock(Truncate(name, MaxHeaderChars))}
	if status := HumanizeToken(p.Status); status != "" {
		blocks = append(blocks, ContextBlock("*Status* "+status))
	}
	if url := strings.TrimSpace(p.URL); url != "" {
		blocks = append(blocks, ActionsBlock(LinkButton("Open in Agora", url)))
	}
	return ClampBlocks(blocks)
}

// assigneeMarker keeps an agent assignee distinct from a human one, which is
// the convention every other Agora surface follows (purple + robot icon in the
// app; an emoji is the Slack-native equivalent).
func assigneeMarker(kind AssigneeKind) string {
	switch kind {
	case AssigneeAgent:
		return ":robot_face: "
	case AssigneeSquad:
		return ":busts_in_silhouette: "
	default:
		return ""
	}
}

// HumanizeToken turns a stored enum token into display text:
// "in_progress" → "In progress", "urgent" → "Urgent".
//
// A token this function has never seen renders as itself, humanised — which is
// exactly the enum-drift posture the rest of the codebase takes: a new status
// from a newer server downgrades to a readable label instead of an empty line
// or a panic.
func HumanizeToken(raw string) string {
	token := strings.TrimSpace(raw)
	if token == "" {
		return ""
	}
	token = strings.ReplaceAll(strings.ReplaceAll(token, "_", " "), "-", " ")
	token = strings.Join(strings.Fields(token), " ")
	if token == "" {
		return ""
	}
	runes := []rune(token)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
