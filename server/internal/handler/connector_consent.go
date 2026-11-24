package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/auth"
	"github.com/jamshidtulaganov/agora/server/internal/connectauth"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// connectorTokenLifetime matches the connector's refresh lifetime, so one
// approval keeps Claude or ChatGPT connected for the life of its token.
const connectorTokenLifetime = 90 * 24 * time.Hour

// ConnectorConsentResponse describes a pending "connect an AI assistant"
// request on Agora's consent page.
type ConnectorConsentResponse struct {
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
	// CancelURL returns the member to the app with access_denied.
	CancelURL string `json:"cancel_url"`
}

// ConnectorApproveResponse is where the browser goes after Allow.
type ConnectorApproveResponse struct {
	RedirectURL string `json:"redirect_url"`
}

// connectorBox reads the key shared with the MCP connector on each call, so
// the feature is off (404) wherever AGORA_MCP_OAUTH_KEY is not configured.
func connectorBox() (*secretbox.Box, bool) {
	key, err := secretbox.LoadKey(connectauth.KeyEnv)
	if err != nil {
		return nil, false
	}
	box, err := secretbox.New(key)
	return box, err == nil
}

func openConnectorRequest(w http.ResponseWriter, sealed string) (*secretbox.Box, connectauth.Request, bool) {
	box, ok := connectorBox()
	if !ok {
		writeError(w, http.StatusNotFound, "assistant connections are not enabled")
		return nil, connectauth.Request{}, false
	}
	req, err := connectauth.OpenRequest(box, sealed, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "this connection request expired; start again from your app")
		return nil, connectauth.Request{}, false
	}
	return box, req, true
}

// GetConnectorConsent shows which app asks for access, for the consent page.
func (h *Handler) GetConnectorConsent(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	_, req, ok := openConnectorRequest(w, r.URL.Query().Get("request"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ConnectorConsentResponse{
		ClientID:   req.ClientID,
		ClientName: req.ClientName,
		CancelURL:  req.Return + "&" + url.Values{"error": {"access_denied"}}.Encode(),
	})
}

// ApproveConnectorConsent mints a dedicated personal access token for the
// signed-in member and returns it to the connector, sealed and bound to the
// browser that started the flow. The member can revoke it in Settings → Tokens.
func (h *Handler) ApproveConnectorConsent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		Request string `json:"request"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	box, req, ok := openConnectorRequest(w, body.Request)
	if !ok {
		return
	}
	rawToken, err := auth.GeneratePATToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	now := time.Now()
	if _, err := h.Queries.CreatePersonalAccessToken(r.Context(), db.CreatePersonalAccessTokenParams{
		UserID:      parseUUID(userID),
		Name:        req.ClientName + " connector",
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: prefix,
		ExpiresAt:   pgtype.Timestamptz{Time: now.Add(connectorTokenLifetime), Valid: true},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	reply, err := connectauth.SealReply(box, rawToken, req.Binding, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to approve")
		return
	}
	writeJSON(w, http.StatusOK, ConnectorApproveResponse{
		RedirectURL: req.Return + "&" + url.Values{"reply": {reply}}.Encode(),
	})
}
