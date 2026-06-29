package lark

import (
	"context"
	"encoding/json"
)

// OpenIDCardSender posts an interactive card directly to a user's open_id
// (not into a chat). It is a deliberately small role interface — the real
// httpAPIClient implements it, the stub does not — so proactive-notify call
// sites type-assert against it and no-op gracefully when outbound Lark is not
// wired. This keeps the broad APIClient interface (and its many test mocks)
// untouched.
type OpenIDCardSender interface {
	SendCardToOpenID(ctx context.Context, p SendCardToOpenIDParams) (string, error)
}

// SendCardToOpenIDParams is the input for an open_id-targeted card send.
type SendCardToOpenIDParams struct {
	InstallationID InstallationCredentials
	OpenID         OpenID
	// CardJSON is the raw Lark interactive card JSON body, passed through
	// opaque so the card template can evolve without touching the transport.
	CardJSON string
}

// IssueNotifyCard builds the interactive-card JSON for a proactive issue
// notification DMed to a member: a short reason line plus a button that opens
// the issue in Agora. headline is the bold card title (e.g. an emoji + the
// inbox title); body is an optional secondary line (e.g. a comment snippet or
// "Backlog → Todo"); issueURL is the absolute web link the button opens;
// buttonLabel is the button caption. A blank issueURL omits the button so the
// card still delivers as a plain notice.
func IssueNotifyCard(headline, body, issueURL, buttonLabel string) (string, error) {
	elements := []any{}
	if body != "" {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": body},
		})
	}
	if issueURL != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{
				map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": buttonLabel},
					"type": "primary",
					"url":  issueURL,
				},
			},
		})
	}
	// Lark rejects a card with an empty elements array; keep at least the
	// headline as a body element when there's nothing else.
	if len(elements) == 0 {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": headline},
		})
	}
	doc := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": headline},
		},
		"elements": elements,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
