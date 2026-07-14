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

// sealWebhookSecret marshals + seals a webhook secret. Retained as a helper
// because the fan-out test seeds rows through it; the create/update handlers now
// seal every kind through the generic buildReleaseSecretPlain + box.Seal path.
func sealWebhookSecret(box *secretbox.Box, s webhookSecret) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return box.Seal(raw)
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
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`

	// webhook (sealed): the receiver URL + optional HMAC signing secret.
	URL    string `json:"url"`    // write-only, never returned
	Secret string `json:"secret"` // write-only HMAC signing secret

	// slack (sealed webhook_url; channel_hint is non-secret display config).
	WebhookURL  string `json:"webhook_url"`
	ChannelHint string `json:"channel_hint"`

	// github_release / gitlab_release / sentry share a sealed token.
	Token string `json:"token"`

	// github_release config (non-secret).
	Owner string `json:"owner"`
	Repo  string `json:"repo"`

	// gitlab_release: host is sealed with the token; project_path is config.
	Host        string `json:"host"`
	ProjectPath string `json:"project_path"`

	// sentry: base_url is sealed with the token; org + project are config.
	BaseURL string `json:"base_url"`
	Org     string `json:"org"`
	Project string `json:"project"`
}

// isKnownReleaseKind reports whether kind is a connector a workspace may
// configure. The dispatcher's releaseConnectorFor must stay in sync with this.
func isKnownReleaseKind(kind string) bool {
	switch kind {
	case "webhook", "slack", "bitrix", "github_release", "gitlab_release", "sentry":
		return true
	default:
		return false
	}
}

// releaseSecretRequiredOnCreate reports whether a kind must carry a secret at
// create time. bitrix is the exception — its portal comes from BITRIX_WEBHOOK_URL
// so a per-workspace override secret is optional.
func releaseSecretRequiredOnCreate(kind string) bool { return kind != "bitrix" }

// releaseSecretProvided reports whether the request carries this kind's
// secret-bearing field (used on update to decide reseal-vs-keep).
func releaseSecretProvided(kind string, req *releaseIntegrationRequest) bool {
	switch kind {
	case "webhook":
		return strings.TrimSpace(req.URL) != ""
	case "slack", "bitrix":
		return strings.TrimSpace(req.WebhookURL) != ""
	case "github_release", "gitlab_release", "sentry":
		return strings.TrimSpace(req.Token) != ""
	default:
		return false
	}
}

// buildReleaseConfig assembles the NON-secret config jsonb for a kind, merging
// the request over the existing config (so a metadata-only edit keeps prior
// values) and validating that the kind's required config fields are present.
// Returns (config, "", true) on success or ("", message, false) with a 400
// message on a missing required field.
func buildReleaseConfig(kind string, req *releaseIntegrationRequest, existing releaseConfigFields) (json.RawMessage, string, bool) {
	pick := func(next, prev string) string {
		if s := strings.TrimSpace(next); s != "" {
			return s
		}
		return strings.TrimSpace(prev)
	}
	out := map[string]string{"name": pick(req.Name, existing.Name)}
	switch kind {
	case "webhook", "bitrix":
		// name only
	case "slack":
		if h := pick(req.ChannelHint, existing.ChannelHint); h != "" {
			out["channel_hint"] = h
		}
	case "github_release":
		out["owner"] = pick(req.Owner, existing.Owner)
		out["repo"] = pick(req.Repo, existing.Repo)
		if out["owner"] == "" || out["repo"] == "" {
			return nil, "github_release requires owner and repo", false
		}
	case "gitlab_release":
		out["project_path"] = pick(req.ProjectPath, existing.ProjectPath)
		if out["project_path"] == "" {
			return nil, "gitlab_release requires project_path", false
		}
	case "sentry":
		out["org"] = pick(req.Org, existing.Org)
		out["project"] = pick(req.Project, existing.Project)
		if out["org"] == "" || out["project"] == "" {
			return nil, "sentry requires org and project", false
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, "invalid config", false
	}
	return raw, "", true
}

// buildReleaseSecretPlain validates + marshals the plaintext secret blob for a
// kind (the bytes the caller then seals). Returns (plain, "", true) on success
// or (nil, message, false) with a 400 message on an invalid/missing field. Only
// called when the kind's secret field was provided.
func buildReleaseSecretPlain(kind string, req *releaseIntegrationRequest) ([]byte, string, bool) {
	marshal := func(v any) ([]byte, string, bool) {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, "invalid secret", false
		}
		return raw, "", true
	}
	switch kind {
	case "webhook":
		url, ok := validWebhookURL(req.URL)
		if !ok {
			return nil, "url must be an absolute http(s) URL", false
		}
		return marshal(webhookSecret{URL: url, Signing: strings.TrimSpace(req.Secret)})
	case "slack":
		url, ok := validWebhookURL(req.WebhookURL)
		if !ok {
			return nil, "webhook_url must be an absolute http(s) URL", false
		}
		return marshal(slackSecret{WebhookURL: url})
	case "bitrix":
		url, ok := validWebhookURL(req.WebhookURL)
		if !ok {
			return nil, "webhook_url must be an absolute http(s) URL", false
		}
		return marshal(bitrixReleaseSecret{WebhookURL: url})
	case "github_release":
		token := strings.TrimSpace(req.Token)
		if token == "" {
			return nil, "github_release requires a token", false
		}
		return marshal(githubReleaseSecret{Token: token})
	case "gitlab_release":
		token := strings.TrimSpace(req.Token)
		if token == "" {
			return nil, "gitlab_release requires a token", false
		}
		return marshal(gitlabReleaseSecret{Token: token, Host: strings.TrimSpace(req.Host)})
	case "sentry":
		token := strings.TrimSpace(req.Token)
		if token == "" {
			return nil, "sentry requires a token", false
		}
		return marshal(sentrySecret{Token: token, BaseURL: strings.TrimSpace(req.BaseURL)})
	default:
		return nil, "unsupported kind", false
	}
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
	if !isKnownReleaseKind(kind) {
		writeError(w, http.StatusBadRequest, "unsupported integration kind")
		return
	}
	eventsList := normalizeReleaseEvents(req.Events)
	if len(eventsList) == 0 {
		writeError(w, http.StatusBadRequest, "at least one event is required (deploy_recorded, release_shipped)")
		return
	}
	config, cmsg, ok := buildReleaseConfig(kind, &req, releaseConfigFields{})
	if !ok {
		writeError(w, http.StatusBadRequest, cmsg)
		return
	}

	// Build → probe → seal the secret. bitrix may omit it (env-driven portal);
	// every other kind requires one at create time.
	var sealed []byte
	probeStatus := ""
	if releaseSecretProvided(kind, &req) {
		plain, smsg, ok := buildReleaseSecretPlain(kind, &req)
		if !ok {
			writeError(w, http.StatusBadRequest, smsg)
			return
		}
		ps, invalid := h.probeReleaseKind(r.Context(), kind, config, plain)
		if invalid {
			writeError(w, http.StatusUnprocessableEntity, "release_integration_invalid: the credential was rejected (401/403)")
			return
		}
		probeStatus = ps
		sealed, err = box.Seal(plain)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to seal integration secret")
			return
		}
	} else if releaseSecretRequiredOnCreate(kind) {
		writeError(w, http.StatusBadRequest, "a secret is required for this integration kind")
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
		Config:          config,
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
	// Kind is immutable — a metadata/secret edit cannot repurpose the row.
	kind := existing.Kind
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// Config: merge the request over the existing config so a metadata-only edit
	// keeps prior owner/repo/org/etc.
	config, cmsg, ok := buildReleaseConfig(kind, &req, parseReleaseConfig(existing.Config))
	if !ok {
		writeError(w, http.StatusBadRequest, cmsg)
		return
	}
	// Secret: a new secret field rotates the sealed blob + re-probes; otherwise
	// the stored secret + probe status are kept verbatim.
	sealed := existing.SecretEncrypted
	probeStatus := existing.ProbeStatus
	if releaseSecretProvided(kind, &req) {
		plain, smsg, ok := buildReleaseSecretPlain(kind, &req)
		if !ok {
			writeError(w, http.StatusBadRequest, smsg)
			return
		}
		ps, invalid := h.probeReleaseKind(r.Context(), kind, config, plain)
		if invalid {
			writeError(w, http.StatusUnprocessableEntity, "release_integration_invalid: the credential was rejected (401/403)")
			return
		}
		probeStatus = ps
		sealed, err = box.Seal(plain)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to seal integration secret")
			return
		}
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

// probeReleaseKind validates a kind's credential at save time WITHOUT delivering
// a real event, classifying the outcome into the stored probe_status + whether
// the save must be rejected (an unambiguous 401/403 auth rejection). Cheap authed
// GETs for github/gitlab/sentry; an OPTIONS reachability probe for webhook. Slack
// (a test POST is destructive) and bitrix (env-driven portal) are NOT probed —
// they return an empty probe_status so the UI shows no badge. Always called with
// the same config + plaintext secret about to be sealed.
func (h *Handler) probeReleaseKind(ctx context.Context, kind string, config json.RawMessage, plain []byte) (probeStatus string, invalid bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch kind {
	case "webhook":
		var s webhookSecret
		if json.Unmarshal(plain, &s) != nil {
			return "", false
		}
		status, reachable := releaseHookClient.Probe(ctx, s.URL)
		return classifyReleaseProbe(status, reachable)
	case "github_release":
		cfg := parseReleaseConfig(config)
		var s githubReleaseSecret
		if json.Unmarshal(plain, &s) != nil {
			return "", false
		}
		status, reachable := releaseGitHubClient.ValidateToken(ctx, cfg.Owner, cfg.Repo, s.Token)
		return classifyReleaseProbe(status, reachable)
	case "gitlab_release":
		cfg := parseReleaseConfig(config)
		var s gitlabReleaseSecret
		if json.Unmarshal(plain, &s) != nil {
			return "", false
		}
		status, reachable := releaseGitLabClient.ValidateToken(ctx, s.Host, cfg.ProjectPath, s.Token)
		return classifyReleaseProbe(status, reachable)
	case "sentry":
		cfg := parseReleaseConfig(config)
		var s sentrySecret
		if json.Unmarshal(plain, &s) != nil {
			return "", false
		}
		status, reachable := releaseSentryClient.ValidateToken(ctx, s.BaseURL, cfg.Org, s.Token)
		return classifyReleaseProbe(status, reachable)
	default:
		// slack (destructive to probe) + bitrix (env-based) → unprobed.
		return "", false
	}
}
