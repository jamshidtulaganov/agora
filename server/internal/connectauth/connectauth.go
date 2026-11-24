// Package connectauth carries the "connect an AI assistant" consent between
// the public MCP connector (cmd/agora-mcp) and the Agora backend.
//
// The connector seals a Request when Claude or ChatGPT starts OAuth and sends
// the member's browser to Agora's consent page. After the member allows it,
// the backend seals a Reply holding a freshly minted personal access token and
// sends the browser back to the connector. Both services hold the same key
// (AGORA_MCP_OAUTH_KEY); nothing else can read or forge either envelope.
package connectauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

// KeyEnv names the 32-byte base64 key shared by the connector and backend.
const KeyEnv = "AGORA_MCP_OAUTH_KEY"

const (
	audRequest = "agora-connect/request"
	audReply   = "agora-connect/reply"
	// RequestLifetime bounds how long the member may take on the consent page.
	RequestLifetime = 10 * time.Minute
	// ReplyLifetime bounds the hop from Agora back to the connector.
	ReplyLifetime = 2 * time.Minute
)

// Request is what the consent page shows and where the reply returns to.
type Request struct {
	Aud        string `json:"aud"`
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
	// Return is the connector callback URL, including Claude's original
	// authorization parameters. The backend appends reply or error to it.
	Return string `json:"return"`
	// Binding ties the reply to the browser that started the flow (a digest of
	// the connector's consent cookie). The connector verifies it on return.
	Binding string `json:"binding"`
	Exp     int64  `json:"exp"`
}

// Reply carries the member's new token back to the connector.
type Reply struct {
	Aud     string `json:"aud"`
	Token   string `json:"token"`
	Binding string `json:"binding"`
	Exp     int64  `json:"exp"`
}

var ErrInvalid = errors.New("connect request is invalid or expired")

func seal(box *secretbox.Box, v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sealed, err := box.Seal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func open(box *secretbox.Box, s string, v any) error {
	if s == "" || len(s) > 8192 {
		return ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ErrInvalid
	}
	data, err := box.Open(raw)
	if err != nil || json.Unmarshal(data, v) != nil {
		return ErrInvalid
	}
	return nil
}

// SealRequest stamps the audience and expiry and seals r.
func SealRequest(box *secretbox.Box, r Request, now time.Time) (string, error) {
	r.Aud, r.Exp = audRequest, now.Add(RequestLifetime).Unix()
	return seal(box, r)
}

// OpenRequest returns a valid, unexpired request whose return URL is HTTPS.
func OpenRequest(box *secretbox.Box, s string, now time.Time) (Request, error) {
	var r Request
	if err := open(box, s, &r); err != nil {
		return Request{}, err
	}
	if r.Aud != audRequest || r.Exp <= now.Unix() || r.ClientName == "" || r.Binding == "" || !strings.HasPrefix(r.Return, "https://") {
		return Request{}, ErrInvalid
	}
	return r, nil
}

// SealReply seals a token for the browser binding of the request it answers.
func SealReply(box *secretbox.Box, token, binding string, now time.Time) (string, error) {
	return seal(box, Reply{Aud: audReply, Token: token, Binding: binding, Exp: now.Add(ReplyLifetime).Unix()})
}

// OpenReply returns a valid, unexpired reply.
func OpenReply(box *secretbox.Box, s string, now time.Time) (Reply, error) {
	var r Reply
	if err := open(box, s, &r); err != nil {
		return Reply{}, err
	}
	if r.Aud != audReply || r.Exp <= now.Unix() || r.Token == "" || r.Binding == "" {
		return Reply{}, ErrInvalid
	}
	return r, nil
}
