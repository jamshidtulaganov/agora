package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
	"github.com/multica-ai/multica/server/internal/integrations/githubrelease"
	"github.com/multica-ai/multica/server/internal/integrations/gitlabrelease"
	"github.com/multica-ai/multica/server/internal/integrations/releasehook"
	"github.com/multica-ai/multica/server/internal/integrations/sentry"
	"github.com/multica-ai/multica/server/internal/integrations/slack"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Release-integration connectors (release-hub Thread B / Phase 3-4). Each named
// connector turns one fired release event into one outbound side effect for a
// configured integration. The dispatcher (release_outbound.go) resolves the
// connector for a row's kind, unseals its secret, and calls it on a detached,
// individually bounded goroutine. Connectors are deliberately small adapters:
// they parse their kind's non-secret config + sealed secret, then delegate to a
// DB-free client in internal/integrations/* so the HTTP shape stays unit-testable
// against httptest servers. A connector must NEVER panic — it returns an error
// the dispatcher logs (best-effort delivery).

// releaseConnector delivers one release-lifecycle event to one integration. cfg
// is the row's non-secret config jsonb; secret is the unsealed secret blob
// (per-kind JSON; may be empty for kinds like bitrix that fall back to env);
// payload is the enriched event payload (event fields + resolved project/sprint
// names + workspace_id); changelog is the shipped-issue list (release:shipped
// only, empty otherwise).
type releaseConnector func(ctx context.Context, cfg json.RawMessage, secret []byte, eventType string, payload map[string]any, changelog []releaseChangelogEntry) error

// releaseChangelogEntry is one shipped issue, enriched for every connector: the
// human identifier + title + rolled-up verdict (release notes), plus the linked
// Bitrix task id (resolved only when a bitrix connector is configured — empty
// otherwise). The webhook connector maps it back to releasehook.ChangelogEntry;
// the git/slack connectors render identifier+title; bitrix reads BitrixTaskID.
type releaseChangelogEntry struct {
	ID           string
	Identifier   string
	Title        string
	Verdict      string
	BitrixTaskID string
}

// Shared, bounded clients — one per connector kind, reused across deliveries
// (each carries its own timeout-bounded http.Client). releaseHookClient lives in
// release_integration.go (shared with the probe path).
var (
	releaseSlackClient  = slack.NewClient()
	releaseGitHubClient = githubrelease.NewClient()
	releaseGitLabClient = gitlabrelease.NewClient()
	releaseSentryClient = sentry.NewClient()
)

// releaseConnectorFor returns the connector for a stored integration kind, or
// nil for an unknown kind (a row written by a newer server — the dispatcher skips
// it rather than crashing, enum-drift-downgrades-not-crashes).
func releaseConnectorFor(kind string) releaseConnector {
	switch kind {
	case "webhook":
		return releaseWebhookConnector
	case "slack":
		return releaseSlackConnector
	case "bitrix":
		return releaseBitrixConnector
	case "github_release":
		return releaseGitHubConnector
	case "gitlab_release":
		return releaseGitLabConnector
	case "sentry":
		return releaseSentryConnector
	default:
		return nil
	}
}

// ── Per-kind secret + config shapes ─────────────────────────────────────────

// slackSecret is the sealed blob for kind="slack": just the Incoming Webhook URL
// (possession = auth).
type slackSecret struct {
	WebhookURL string `json:"webhook_url"`
}

// githubReleaseSecret is the sealed blob for kind="github_release": a PAT.
type githubReleaseSecret struct {
	Token string `json:"token"`
}

// gitlabReleaseSecret is the sealed blob for kind="gitlab_release": a PAT plus
// the GitLab host (defaults to gitlab.com for SaaS).
type gitlabReleaseSecret struct {
	Token string `json:"token"`
	Host  string `json:"host"`
}

// sentrySecret is the sealed blob for kind="sentry": an API token plus the base
// URL (overridable for self-hosted Sentry).
type sentrySecret struct {
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
}

// bitrixReleaseSecret is the OPTIONAL sealed blob for kind="bitrix": a
// per-workspace override portal webhook URL. Empty → the env BITRIX_WEBHOOK_URL
// portal is used.
type bitrixReleaseSecret struct {
	WebhookURL string `json:"webhook_url"`
}

// releaseConfigFields is the union of every kind's NON-secret config jsonb. Only
// the fields relevant to a given kind are populated; the rest stay empty.
type releaseConfigFields struct {
	Name        string `json:"name"`
	ChannelHint string `json:"channel_hint"` // slack (display only)
	Owner       string `json:"owner"`        // github_release
	Repo        string `json:"repo"`         // github_release
	ProjectPath string `json:"project_path"` // gitlab_release
	Org         string `json:"org"`          // sentry
	Project     string `json:"project"`      // sentry
}

func parseReleaseConfig(cfg json.RawMessage) releaseConfigFields {
	var c releaseConfigFields
	_ = json.Unmarshal(cfg, &c) // best-effort: a malformed blob → zero fields
	return c
}

// ── Connectors ──────────────────────────────────────────────────────────────

// releaseWebhookConnector reproduces the Phase 2 generic webhook: a signed POST
// of {event, workspace_id, ...payload, changelog} to the sealed URL.
func releaseWebhookConnector(ctx context.Context, _ json.RawMessage, secret []byte, eventType string, payload map[string]any, changelog []releaseChangelogEntry) error {
	var s webhookSecret
	if err := json.Unmarshal(secret, &s); err != nil || strings.TrimSpace(s.URL) == "" {
		return fmt.Errorf("release webhook: missing url")
	}
	body := map[string]any{"event": eventType}
	for k, v := range payload {
		body[k] = v
	}
	if len(changelog) > 0 {
		body["changelog"] = toWebhookChangelog(changelog)
	}
	return releaseHookClient.Deliver(ctx, s.URL, s.Signing, eventType, body)
}

// releaseSlackConnector posts a human message to a Slack Incoming Webhook.
func releaseSlackConnector(ctx context.Context, _ json.RawMessage, secret []byte, eventType string, payload map[string]any, changelog []releaseChangelogEntry) error {
	var s slackSecret
	if err := json.Unmarshal(secret, &s); err != nil || strings.TrimSpace(s.WebhookURL) == "" {
		return fmt.Errorf("release slack: missing webhook_url")
	}
	text := slackReleaseText(eventType, payload, changelog)
	if text == "" {
		return nil
	}
	return releaseSlackClient.PostMessage(ctx, s.WebhookURL, text)
}

// releaseBitrixConnector comments on each shipped issue's linked Bitrix task.
// Single-portal ENV-based like the rest of the Bitrix integration: the client is
// built from BITRIX_WEBHOOK_URL unless the row carries a per-workspace override.
// No-op cleanly when neither is set.
func releaseBitrixConnector(ctx context.Context, _ json.RawMessage, secret []byte, eventType string, payload map[string]any, changelog []releaseChangelogEntry) error {
	if eventType != protocol.EventReleaseShipped {
		return nil // shipping is the only Bitrix-relevant lifecycle event
	}
	webhookURL := strings.TrimSpace(bitrixWebhookURL())
	if len(secret) > 0 {
		var s bitrixReleaseSecret
		if json.Unmarshal(secret, &s) == nil && strings.TrimSpace(s.WebhookURL) != "" {
			webhookURL = strings.TrimSpace(s.WebhookURL)
		}
	}
	if webhookURL == "" {
		return nil // no portal configured → clean no-op
	}
	client := bitrix.NewClient(webhookURL)
	comment := fmt.Sprintf("🚀 Agora: shipped in %s → %s",
		fallbackStr(payloadString(payload, "sprint"), "release"),
		fallbackStr(payloadString(payload, "environment"), "production"))
	var firstErr error
	for _, e := range changelog {
		if e.BitrixTaskID == "" {
			continue
		}
		if err := client.AddTaskComment(ctx, e.BitrixTaskID, comment); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// releaseGitHubConnector creates a GitHub Release from the sprint changelog.
func releaseGitHubConnector(ctx context.Context, cfg json.RawMessage, secret []byte, eventType string, payload map[string]any, changelog []releaseChangelogEntry) error {
	if eventType != protocol.EventReleaseShipped {
		return nil
	}
	c := parseReleaseConfig(cfg)
	if strings.TrimSpace(c.Owner) == "" || strings.TrimSpace(c.Repo) == "" {
		return fmt.Errorf("release github: missing owner/repo")
	}
	var s githubReleaseSecret
	if err := json.Unmarshal(secret, &s); err != nil || strings.TrimSpace(s.Token) == "" {
		return fmt.Errorf("release github: missing token")
	}
	tag := sanitizeReleaseTag(releaseVersionLabel(payload))
	return releaseGitHubClient.CreateRelease(ctx, strings.TrimSpace(c.Owner), strings.TrimSpace(c.Repo), s.Token, githubrelease.Release{
		TagName: tag,
		Name:    releaseTitle(payload, tag),
		Body:    releaseChangelogMarkdown(changelog),
	})
}

// releaseGitLabConnector creates a GitLab Release from the sprint changelog.
func releaseGitLabConnector(ctx context.Context, cfg json.RawMessage, secret []byte, eventType string, payload map[string]any, changelog []releaseChangelogEntry) error {
	if eventType != protocol.EventReleaseShipped {
		return nil
	}
	c := parseReleaseConfig(cfg)
	if strings.TrimSpace(c.ProjectPath) == "" {
		return fmt.Errorf("release gitlab: missing project_path")
	}
	var s gitlabReleaseSecret
	if err := json.Unmarshal(secret, &s); err != nil || strings.TrimSpace(s.Token) == "" {
		return fmt.Errorf("release gitlab: missing token")
	}
	tag := sanitizeReleaseTag(releaseVersionLabel(payload))
	return releaseGitLabClient.CreateRelease(ctx, s.Host, strings.TrimSpace(c.ProjectPath), s.Token, gitlabrelease.Release{
		TagName:     tag,
		Name:        releaseTitle(payload, tag),
		Description: releaseChangelogMarkdown(changelog),
	})
}

// releaseSentryConnector creates a Sentry release + deploy for the environment.
func releaseSentryConnector(ctx context.Context, cfg json.RawMessage, secret []byte, eventType string, payload map[string]any, _ []releaseChangelogEntry) error {
	if eventType != protocol.EventReleaseShipped {
		return nil
	}
	c := parseReleaseConfig(cfg)
	if strings.TrimSpace(c.Org) == "" || strings.TrimSpace(c.Project) == "" {
		return fmt.Errorf("release sentry: missing org/project")
	}
	var s sentrySecret
	if err := json.Unmarshal(secret, &s); err != nil || strings.TrimSpace(s.Token) == "" {
		return fmt.Errorf("release sentry: missing token")
	}
	version := sanitizeReleaseTag(releaseVersionLabel(payload))
	return releaseSentryClient.CreateReleaseAndDeploy(ctx, s.BaseURL, strings.TrimSpace(c.Org), strings.TrimSpace(c.Project), s.Token, version, payloadString(payload, "environment"))
}

// ── Shared helpers ──────────────────────────────────────────────────────────

// toWebhookChangelog maps the enriched entries back to the webhook client's
// changelog shape (drops the Bitrix task id — a receiver-agnostic detail).
func toWebhookChangelog(in []releaseChangelogEntry) []releasehook.ChangelogEntry {
	out := make([]releasehook.ChangelogEntry, len(in))
	for i, e := range in {
		out[i] = releasehook.ChangelogEntry{Identifier: e.Identifier, Title: e.Title, Verdict: e.Verdict}
	}
	return out
}

// slackReleaseText builds the mrkdwn message body for a fired event, or "" when
// the event carries nothing worth posting.
func slackReleaseText(eventType string, payload map[string]any, changelog []releaseChangelogEntry) string {
	switch eventType {
	case protocol.EventReleaseShipped:
		var b strings.Builder
		fmt.Fprintf(&b, "🚀 %s shipped to %s",
			releaseLabel(payloadString(payload, "project"), payloadString(payload, "sprint")),
			fallbackStr(payloadString(payload, "environment"), "an environment"))
		for _, e := range changelog {
			b.WriteString("\n• ")
			b.WriteString(changelogLine(e))
		}
		return b.String()
	case protocol.EventDeployRecorded:
		return fmt.Sprintf("Deploy to %s: %s",
			fallbackStr(payloadString(payload, "target"), "an environment"),
			fallbackStr(payloadString(payload, "status"), "unknown"))
	default:
		return ""
	}
}

// releaseLabel renders "{project} · {sprint}" for the fields that are present.
func releaseLabel(project, sprint string) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(project) != "" {
		parts = append(parts, strings.TrimSpace(project))
	}
	if strings.TrimSpace(sprint) != "" {
		parts = append(parts, strings.TrimSpace(sprint))
	}
	if len(parts) == 0 {
		return "A release"
	}
	return strings.Join(parts, " · ")
}

// releaseTitle names the created git release: "{project} · {sprint}", falling
// back to the tag when no names resolved.
func releaseTitle(payload map[string]any, tag string) string {
	label := releaseLabel(payloadString(payload, "project"), payloadString(payload, "sprint"))
	if label == "A release" {
		return tag
	}
	return label
}

// releaseVersionLabel is the human basis for a release tag/version: the sprint
// name, else the deploy branch. Empty when neither resolved.
func releaseVersionLabel(payload map[string]any) string {
	if s := payloadString(payload, "sprint"); strings.TrimSpace(s) != "" {
		return s
	}
	return payloadString(payload, "branch")
}

// releaseChangelogMarkdown renders the shipped issues as a markdown bullet list
// (used as the GitHub/GitLab release body). Empty for an empty changelog.
func releaseChangelogMarkdown(cl []releaseChangelogEntry) string {
	if len(cl) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range cl {
		b.WriteString("- ")
		b.WriteString(changelogLine(e))
		b.WriteString("\n")
	}
	return b.String()
}

// changelogLine is one "IDENTIFIER — title" release-notes line, degrading
// gracefully when either half is missing.
func changelogLine(e releaseChangelogEntry) string {
	id := strings.TrimSpace(e.Identifier)
	title := strings.TrimSpace(e.Title)
	switch {
	case id != "" && title != "":
		return id + " — " + title
	case title != "":
		return title
	default:
		return id
	}
}

// sanitizeReleaseTag derives a safe git/Sentry tag "release-<slug>" from a human
// label, stripping any existing release prefix so it never double-prefixes.
func sanitizeReleaseTag(label string) string {
	label = strings.TrimSpace(label)
	lower := strings.ToLower(label)
	lower = strings.TrimPrefix(lower, "release-")
	lower = strings.TrimPrefix(lower, "release/")
	slug := slugifyRelease(lower)
	if slug == "" {
		slug = "release"
	}
	return "release-" + slug
}

// slugifyRelease lowercases and collapses non-alphanumeric runs to single
// dashes, trimming leading/trailing dashes — a URL/tag-safe slug.
func slugifyRelease(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case b.Len() > 0 && !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// payloadString reads a string field from the enriched event payload.
func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	s, _ := payload[key].(string)
	return s
}

// fallbackStr returns def when s is blank.
func fallbackStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
