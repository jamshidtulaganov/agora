package slack

// conversations.* — the two read/open calls Phase 1 routing needs.
//
//   - ConversationsList backs the channel picker (PR 2's
//     GET /api/workspaces/{id}/slack/channels proxy). It is a Tier-2 method
//     at 20+/min per workspace, and — unlike conversations.history /
//     conversations.replies — it is NOT one of the two methods the
//     2025-05-29 unlisted-app limits throttle to 1 req/min. A picker that
//     paged through it is therefore safe on an unlisted install link.
//   - ConversationsOpen resolves a Slack user id to the IM channel a DM is
//     posted into. chat.postMessage would accept a bare user id as `channel`,
//     but Slack documents conversations.open as the supported path and it is
//     the one that fails loudly (`user_not_found`, `cannot_dm_bot`) instead of
//     silently dropping the message.
//
// Both follow api.go's contract: the bot token is an argument, never client
// state; the response is decoded into an explicit struct and `ok`-checked; a
// failure is an *APIError carrying Slack's own code.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Conversation types requested by the picker. Public and private channels
// only: a DM or group DM is not something a workspace routes notifications to,
// and asking for them would widen the token's read surface for no product.
const conversationTypes = "public_channel,private_channel"

// maxConversationsPageSize is Slack's recommended ceiling for one page.
// Slack's own guidance is "no more than 200 results at a time".
const maxConversationsPageSize = 200

// Conversation is the subset of a Slack channel object the picker renders.
// IsMember matters: chat.postMessage to a public channel the bot has not
// joined works only with chat:write.public, and not at all for a private one,
// so the UI has to be able to say "invite the app first".
type Conversation struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsPrivate  bool   `json:"is_private"`
	IsArchived bool   `json:"is_archived"`
	IsMember   bool   `json:"is_member"`
	IsChannel  bool   `json:"is_channel"`
	IsGroup    bool   `json:"is_group"`
}

// ConversationsListResult is one page of channels plus the cursor for the next.
type ConversationsListResult struct {
	slackResponse
	Channels         []Conversation `json:"channels"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// NextCursor is the opaque cursor for the following page, or "" at the end.
func (r ConversationsListResult) NextCursor() string {
	return strings.TrimSpace(r.ResponseMetadata.NextCursor)
}

// ConversationsListParams are the picker's knobs. A zero value is valid.
type ConversationsListParams struct {
	// Limit is clamped to [1, maxConversationsPageSize].
	Limit int
	// Cursor continues a previous page (response_metadata.next_cursor).
	Cursor string
}

// ConversationsList lists the channels the installation can see.
//
// conversations.list is a GET/form method, not one of the JSON-friendly ones,
// so it does not go through api.go's JSON `call`.
func (c *APIClient) ConversationsList(ctx context.Context, token string, p ConversationsListParams) (ConversationsListResult, error) {
	limit := p.Limit
	if limit <= 0 || limit > maxConversationsPageSize {
		limit = maxConversationsPageSize
	}
	q := url.Values{}
	q.Set("types", conversationTypes)
	q.Set("exclude_archived", "true")
	q.Set("limit", strconv.Itoa(limit))
	if cursor := strings.TrimSpace(p.Cursor); cursor != "" {
		q.Set("cursor", cursor)
	}

	var out ConversationsListResult
	err := c.get(ctx, "conversations.list", token, q, &out, &out.slackResponse)
	return out, err
}

// ConversationsOpenResult carries the IM channel id a DM is posted into.
type ConversationsOpenResult struct {
	slackResponse
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
}

// ConversationsOpen resolves (or creates) the IM channel with one Slack user.
func (c *APIClient) ConversationsOpen(ctx context.Context, token, slackUserID string) (ConversationsOpenResult, error) {
	var out ConversationsOpenResult
	payload := map[string]any{"users": strings.TrimSpace(slackUserID)}
	err := c.call(ctx, "conversations.open", token, payload, &out, &out.slackResponse)
	return out, err
}

// get performs one form-encoded Web API read. Same failure taxonomy as
// api.go's call — 429 keeps Slack's Retry-After, a non-2xx is a transport
// failure, and `ok:false` becomes an *APIError with Slack's own code — so the
// delivery queue and the handlers branch on one error type, not two.
func (c *APIClient) get(ctx context.Context, method, token string, q url.Values, out any, env *slackResponse) error {
	if strings.TrimSpace(token) == "" {
		return &APIError{Method: method, Code: "not_authed"}
	}
	endpoint := c.baseURL + "/" + method
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("slack: build %s request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Agora-Slack/1")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %s request failed: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("slack: read %s response: %w", method, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return &APIError{
			Method:     method,
			Code:       rateLimitedCode,
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Method: method, StatusCode: resp.StatusCode}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("slack: decode %s response: %w", method, err)
	}
	if !env.OK {
		return &APIError{Method: method, Code: env.Error, StatusCode: resp.StatusCode}
	}
	return nil
}
