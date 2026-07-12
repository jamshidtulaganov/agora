package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/integrations/releasehook"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-workspace release integrations (release-hub Thread B / Phase 2). A
// workspace admin configures outbound connectors that fire on release-lifecycle
// events. Phase 2 wires only kind="webhook": a signed POST to an arbitrary URL.
//
// The webhook URL + optional HMAC signing secret are sealed at rest with a
// secretbox loaded from AGORA_RELEASE_SECRET_KEY and decrypted only server-side
// in the dispatcher (release_outbound.go). If the key is unset the write
// endpoints fail closed (503) rather than store a URL in plaintext — a webhook
// URL is a capability that exfiltrates release data, so it is treated as a
// secret, never returned by any endpoint.

var (
	releaseBoxOnce sync.Once
	releaseBoxVal  *secretbox.Box
	releaseBoxErr  error
)

func releaseIntegrationBox() (*secretbox.Box, error) {
	releaseBoxOnce.Do(func() {
		key, err := secretbox.LoadKey("AGORA_RELEASE_SECRET_KEY")
		if err != nil {
			releaseBoxErr = err
			return
		}
		releaseBoxVal, releaseBoxErr = secretbox.New(key)
	})
	return releaseBoxVal, releaseBoxErr
}

// Short event names stored in release_integration.events[]. The bus event
// constants (protocol.EventDeployRecorded / EventReleaseShipped) map to these
// via releaseEventShortName so the stored filter is stable even if the wire
// event string ever changes.
const (
	releaseEventDeployRecorded = "deploy_recorded"
	releaseEventReleaseShipped = "release_shipped"
)

// releaseHookClient is the shared outbound webhook client (probe + deliver).
var releaseHookClient = releasehook.NewClient()

// webhookSecret is the sealed blob for a kind="webhook" integration: the
// receiver URL plus an optional HMAC signing secret. Sealed as JSON so both
// halves ride in the one secret_encrypted column.
type webhookSecret struct {
	URL     string `json:"url"`
	Signing string `json:"signing,omitempty"`
}

func sealWebhookSecret(box *secretbox.Box, s webhookSecret) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return box.Seal(raw)
}

func openWebhookSecret(box *secretbox.Box, sealed []byte) (webhookSecret, bool) {
	if len(sealed) == 0 {
		return webhookSecret{}, false
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return webhookSecret{}, false
	}
	var s webhookSecret
	if json.Unmarshal(plain, &s) != nil {
		return webhookSecret{}, false
	}
	return s, true
}

type releaseIntegrationResponse struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Config      json.RawMessage `json:"config"`
	Events      []string        `json:"events"`
	Enabled     bool            `json:"enabled"`
	ProbeStatus string          `json:"probe_status"`
	HasSecret   bool            `json:"has_secret"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// configOrEmpty guards against a NULL/empty jsonb rendering as `null` in the
// response — always emit a valid `{}` object.
func configOrEmpty(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(raw)
}

func releaseIntegrationFromListRow(row db.ListReleaseIntegrationsByWorkspaceRow) releaseIntegrationResponse {
	return releaseIntegrationResponse{
		ID:          uuidToString(row.ID),
		Kind:        row.Kind,
		Config:      configOrEmpty(row.Config),
		Events:      row.Events,
		Enabled:     row.Enabled,
		ProbeStatus: row.ProbeStatus,
		HasSecret:   row.HasSecret,
		CreatedAt:   timestampToString(row.CreatedAt),
		UpdatedAt:   timestampToString(row.UpdatedAt),
	}
}

func releaseIntegrationFromModel(row db.ReleaseIntegration) releaseIntegrationResponse {
	return releaseIntegrationResponse{
		ID:          uuidToString(row.ID),
		Kind:        row.Kind,
		Config:      configOrEmpty(row.Config),
		Events:      row.Events,
		Enabled:     row.Enabled,
		ProbeStatus: row.ProbeStatus,
		HasSecret:   len(row.SecretEncrypted) > 0,
		CreatedAt:   timestampToString(row.CreatedAt),
		UpdatedAt:   timestampToString(row.UpdatedAt),
	}
}

type releaseIntegrationRequest struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`    // sealed; write-only, never returned
	Secret  string   `json:"secret"` // optional HMAC signing secret; write-only
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`
}

// normalizeReleaseEvents keeps only the known short event names, de-duplicated
// and in a stable order. An unknown value (enum drift from a newer client) is
// dropped rather than stored.
func normalizeReleaseEvents(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range in {
		switch strings.TrimSpace(e) {
		case releaseEventDeployRecorded:
			if !seen[releaseEventDeployRecorded] {
				seen[releaseEventDeployRecorded] = true
				out = append(out, releaseEventDeployRecorded)
			}
		case releaseEventReleaseShipped:
			if !seen[releaseEventReleaseShipped] {
				seen[releaseEventReleaseShipped] = true
				out = append(out, releaseEventReleaseShipped)
			}
		}
	}
	return out
}

// validWebhookURL requires an absolute http(s) URL with a host. Returns the
// trimmed URL and ok=false for anything else.
func validWebhookURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	return raw, true
}

// classifyReleaseProbe maps an OPTIONS-probe outcome to the stored probe_status
// and whether the save must be rejected. Only a definite auth rejection
// (401/403) counts as invalid; a transport error or a 4xx/5xx is a receiver-
// side condition that must NOT block saving (the URL may only accept POST) — it
// saves with probe_status "unreachable" so the UI can flag it.
func classifyReleaseProbe(status int, reachable bool) (probeStatus string, invalid bool) {
	switch {
	case !reachable:
		return "unreachable", false
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "invalid", true
	case status >= 200 && status < 400:
		return "ok", false
	default:
		return "unreachable", false
	}
}

// releaseActorIsAgent rejects an agent actor from the mutating endpoints — a
// compromised agent must not be able to point release data at an exfiltration
// URL. Header-driven (no DB), so it runs before the seal-key check and keeps
// the fail-closed 503 path DB-free. Returns true (and writes 403) for agents.
func (h *Handler) releaseActorIsAgent(w http.ResponseWriter, r *http.Request, wsID string) bool {
	if actorType, _ := h.resolveActor(r, requestUserID(r), wsID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not manage release integrations")
		return true
	}
	return false
}

// ListReleaseIntegrations returns the workspace's integrations WITHOUT any
// secret material (the query never selects secret_encrypted; has_secret is a
// computed boolean). Member-visible so the integrations tab renders for
// non-admins.
func (h *Handler) ListReleaseIntegrations(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListReleaseIntegrationsByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list release integrations")
		return
	}
	resp := make([]releaseIntegrationResponse, len(rows))
	for i, row := range rows {
		resp[i] = releaseIntegrationFromListRow(row)
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateReleaseIntegration adds a webhook integration. The URL is probed before
// sealing so a URL that hard-rejects our probe (401/403) is caught at save time
// (422) instead of silently failing every future delivery.
func (h *Handler) CreateReleaseIntegration(w http.ResponseWriter, r *http.Request) {
	wsIDStr := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, wsIDStr, "workspace id")
	if !ok {
		return
	}
	if h.releaseActorIsAgent(w, r, wsIDStr) {
		return
	}
	box, err := releaseIntegrationBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "release integrations are not configured on this server (AGORA_RELEASE_SECRET_KEY unset)")
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, wsIDStr, "workspace not found", "owner", "admin"); !ok {
		return
	}
	var req releaseIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = "webhook"
	}
	if kind != "webhook" {
		writeError(w, http.StatusBadRequest, "only webhook integrations are supported")
		return
	}
	url, ok := validWebhookURL(req.URL)
	if !ok {
		writeError(w, http.StatusBadRequest, "url must be an absolute http(s) URL")
		return
	}
	eventsList := normalizeReleaseEvents(req.Events)
	if len(eventsList) == 0 {
		writeError(w, http.StatusBadRequest, "at least one event is required (deploy_recorded, release_shipped)")
		return
	}

	probeStatus, invalid := h.probeReleaseWebhook(r.Context(), url)
	if invalid {
		writeError(w, http.StatusUnprocessableEntity, "release_webhook_invalid: the URL rejected the probe (401/403)")
		return
	}

	sealed, err := sealWebhookSecret(box, webhookSecret{URL: url, Signing: strings.TrimSpace(req.Secret)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal webhook secret")
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.Queries.InsertReleaseIntegration(r.Context(), db.InsertReleaseIntegrationParams{
		WorkspaceID:     wsUUID,
		Kind:            kind,
		Config:          releaseConfigJSON(req.Name),
		SecretEncrypted: sealed,
		Events:          eventsList,
		Enabled:         enabled,
		ProbeStatus:     probeStatus,
		CreatedBy:       creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save release integration")
		return
	}
	writeJSON(w, http.StatusOK, releaseIntegrationFromModel(row))
}

// UpdateReleaseIntegration edits an existing integration. A metadata-only edit
// (name/events/enabled) keeps the stored URL; sending a new url re-seals +
// re-probes it.
func (h *Handler) UpdateReleaseIntegration(w http.ResponseWriter, r *http.Request) {
	wsIDStr := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, wsIDStr, "workspace id")
	if !ok {
		return
	}
	if h.releaseActorIsAgent(w, r, wsIDStr) {
		return
	}
	box, err := releaseIntegrationBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "release integrations are not configured on this server (AGORA_RELEASE_SECRET_KEY unset)")
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, wsIDStr, "workspace not found", "owner", "admin"); !ok {
		return
	}
	intUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "integrationId"), "integration id")
	if !ok {
		return
	}
	existing, err := h.Queries.GetReleaseIntegration(r.Context(), db.GetReleaseIntegrationParams{
		ID:          intUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "release integration not found")
		return
	}
	var req releaseIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	eventsList := normalizeReleaseEvents(req.Events)
	if len(eventsList) == 0 {
		writeError(w, http.StatusBadRequest, "at least one event is required (deploy_recorded, release_shipped)")
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sealed := existing.SecretEncrypted
	probeStatus := existing.ProbeStatus
	// A new URL rotates the sealed blob and re-probes; otherwise the stored one
	// is kept verbatim (a metadata edit never drops the URL).
	if strings.TrimSpace(req.URL) != "" {
		url, ok := validWebhookURL(req.URL)
		if !ok {
			writeError(w, http.StatusBadRequest, "url must be an absolute http(s) URL")
			return
		}
		signing := strings.TrimSpace(req.Secret)
		if signing == "" {
			// Preserve the existing signing secret when only the URL changed.
			if prev, ok := openWebhookSecret(box, existing.SecretEncrypted); ok {
				signing = prev.Signing
			}
		}
		ps, invalid := h.probeReleaseWebhook(r.Context(), url)
		if invalid {
			writeError(w, http.StatusUnprocessableEntity, "release_webhook_invalid: the URL rejected the probe (401/403)")
			return
		}
		probeStatus = ps
		sealed, err = sealWebhookSecret(box, webhookSecret{URL: url, Signing: signing})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to seal webhook secret")
			return
		}
	}
	config := existing.Config
	if strings.TrimSpace(req.Name) != "" {
		config = releaseConfigJSON(req.Name)
	}
	row, err := h.Queries.UpdateReleaseIntegration(r.Context(), db.UpdateReleaseIntegrationParams{
		ID:              intUUID,
		WorkspaceID:     wsUUID,
		Kind:            existing.Kind,
		Config:          config,
		SecretEncrypted: sealed,
		Events:          eventsList,
		Enabled:         enabled,
		ProbeStatus:     probeStatus,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update release integration")
		return
	}
	writeJSON(w, http.StatusOK, releaseIntegrationFromModel(row))
}

// DeleteReleaseIntegration removes an integration by id (scoped to workspace).
func (h *Handler) DeleteReleaseIntegration(w http.ResponseWriter, r *http.Request) {
	wsIDStr := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, wsIDStr, "workspace id")
	if !ok {
		return
	}
	if h.releaseActorIsAgent(w, r, wsIDStr) {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, wsIDStr, "workspace not found", "owner", "admin"); !ok {
		return
	}
	intUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "integrationId"), "integration id")
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteReleaseIntegration(r.Context(), db.DeleteReleaseIntegrationParams{
		ID:          intUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete release integration")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "release integration not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// probeReleaseWebhook runs a bounded OPTIONS probe against the URL and
// classifies the outcome. Split out so create + update share it.
func (h *Handler) probeReleaseWebhook(ctx context.Context, url string) (probeStatus string, invalid bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, reachable := releaseHookClient.Probe(ctx, url)
	return classifyReleaseProbe(status, reachable)
}

// releaseConfigJSON builds the non-secret config blob for a webhook. Only a
// display name today; kept as a helper so the shape has one definition.
func releaseConfigJSON(name string) []byte {
	raw, err := json.Marshal(map[string]string{"name": strings.TrimSpace(name)})
	if err != nil {
		return []byte("{}")
	}
	return raw
}
