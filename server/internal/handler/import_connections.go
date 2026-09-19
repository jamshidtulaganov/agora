package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The sealed source credential (docs/importers-plan.md §3.8).
//
// Three rules this file exists to keep, all of them structural rather than
// remembered:
//
//   - The token is probed BEFORE it is sealed. A bad key fails in two seconds
//     at the Settings screen instead of at row 4000 of a migration, and an
//     invalid key is never stored at all.
//   - The token is never echoed. The response type below has no field that
//     could carry one, and the listing/detail queries do not select
//     secret_encrypted, so a leak would take a deliberate new query.
//   - Without AGORA_IMPORT_SECRET_KEY the write path answers 503. Storing a
//     customer's Linear key in the clear "for now" is not one of the options.

// importConnectionResponse is the ONLY shape a connection is rendered in.
// Note what is absent and must stay absent: the secret, in any form —
// including a "last 4" tail. A Linear personal API key is short-lived
// operator-visible material and its suffix identifies nothing a label cannot.
type importConnectionResponse struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Label        string `json:"label"`
	BaseURL      string `json:"base_url,omitempty"`
	AccountEmail string `json:"account_email,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
	ProbeStatus  string `json:"probe_status"`
	ProbedAt     string `json:"probed_at,omitempty"`
	CreatedBy    string `json:"created_by,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

// importConnectionRow is the common shape of the four generated row types
// (create / list / get / probe), which differ only in name. Rendering through
// one struct keeps the response identical on every endpoint.
type importConnectionRow struct {
	ID           pgtype.UUID
	Source       string
	Label        string
	BaseUrl      string
	AccountEmail string
	Scopes       string
	ProbeStatus  string
	ProbedAt     pgtype.Timestamptz
	CreatedBy    pgtype.UUID
	CreatedAt    pgtype.Timestamptz
	UpdatedAt    pgtype.Timestamptz
}

func importConnectionResponseOf(row importConnectionRow) importConnectionResponse {
	resp := importConnectionResponse{
		ID:           uuidToString(row.ID),
		Source:       row.Source,
		Label:        row.Label,
		BaseURL:      row.BaseUrl,
		AccountEmail: row.AccountEmail,
		Scopes:       row.Scopes,
		ProbeStatus:  row.ProbeStatus,
		CreatedAt:    timestampToString(row.CreatedAt),
		UpdatedAt:    timestampToString(row.UpdatedAt),
	}
	if row.CreatedBy.Valid {
		resp.CreatedBy = uuidToString(row.CreatedBy)
	}
	if row.ProbedAt.Valid {
		resp.ProbedAt = row.ProbedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

type createImportConnectionRequest struct {
	Source string `json:"source"`
	Label  string `json:"label"`
	// Secret is the source API token. It is read once, probed, sealed, and
	// then unreachable: it is never written to a log line, an event payload or
	// a response. `token` is accepted as an alias because that is what the
	// Settings screen calls it in every other integration.
	Secret  string `json:"secret"`
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
}

func (r createImportConnectionRequest) secret() string {
	if s := strings.TrimSpace(r.Secret); s != "" {
		return s
	}
	return strings.TrimSpace(r.Token)
}

// ListImportConnections returns the workspace's source connections, metadata
// only. Admin-gated like the write routes: a connection names which tracker a
// team is leaving, which is not something a guest needs.
func (h *Handler) ListImportConnections(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListImportConnections(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list import connections")
		return
	}
	out := make([]importConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, importConnectionResponseOf(importConnectionRow{
			ID: row.ID, Source: row.Source, Label: row.Label, BaseUrl: row.BaseUrl,
			AccountEmail: row.AccountEmail, Scopes: row.Scopes, ProbeStatus: row.ProbeStatus,
			ProbedAt: row.ProbedAt, CreatedBy: row.CreatedBy,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

// CreateImportConnection stores (or rotates) the source token for a
// (workspace, source, label).
//
// The order is the whole point: seal key first (503 if absent), then PROBE the
// token with the adapter, then store. A token the source rejects is refused
// with 422 and nothing is written — the same test-before-seal discipline as
// the Figma credential, and the reason a migration cannot die at row 4000 on a
// typo'd key.
func (h *Handler) CreateImportConnection(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req createImportConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" {
		source = imports.SourceLinear
	}
	if source != imports.SourceLinear {
		writeError(w, http.StatusBadRequest, "unsupported import source: "+source)
		return
	}
	secret := req.secret()
	if secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if source == imports.SourceLinear && baseURL != "" {
		// Linear has exactly one endpoint. Accepting a host here would let an
		// admin aim the customer's API key at a server they control.
		writeError(w, http.StatusBadRequest, "base_url is not used for Linear connections")
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}

	sealed, err := imports.SealSecret(secret)
	if err != nil {
		if writeImportSealError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to seal the import token")
		return
	}

	// Probe before storing. The adapter is built from the plaintext directly —
	// nothing has been written yet, so there is nothing to roll back.
	adapter, err := newImportAdapter(db.ImportConnection{Source: source, BaseUrl: baseURL}, secret)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	probe, probeErr := adapter.Probe(r.Context())
	switch {
	case probe.Status == imports.ProbeInvalid:
		detail := probe.Detail
		if detail == "" {
			detail = "the source rejected this token"
		}
		writeError(w, http.StatusUnprocessableEntity, "import_token_invalid: "+detail)
		return
	case probe.Status != imports.ProbeOK:
		// Unreachable is the source's problem, not the token's. Storing the
		// credential anyway would be the Figma posture — but an import that
		// cannot reach its source has nothing to do, and a connection whose
		// account is unverified would show a green row for a key nobody has
		// checked. Refuse and let the operator retry.
		detail := probe.Detail
		if detail == "" && probeErr != nil {
			detail = "could not reach the source"
		}
		writeError(w, http.StatusBadGateway, "import_source_unreachable: "+detail)
		return
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = defaultImportLabel(source, probe)
	}

	row, err := h.Queries.CreateImportConnection(r.Context(), db.CreateImportConnectionParams{
		WorkspaceID:     wsUUID,
		Source:          source,
		Label:           label,
		BaseUrl:         baseURL,
		AccountEmail:    probe.Account,
		SecretEncrypted: sealed,
		Scopes:          "",
		CreatedBy:       creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save the import connection")
		return
	}
	// The insert clears probe_status on a rotation, so the verdict this
	// request just earned is written back explicitly rather than left blank.
	probed, err := h.Queries.UpdateImportConnectionProbe(r.Context(), db.UpdateImportConnectionProbeParams{
		ID:          row.ID,
		WorkspaceID: wsUUID,
		ProbeStatus: probe.Status,
	})
	if err != nil {
		writeJSON(w, http.StatusCreated, importConnectionResponseOf(importConnectionRow{
			ID: row.ID, Source: row.Source, Label: row.Label, BaseUrl: row.BaseUrl,
			AccountEmail: row.AccountEmail, Scopes: row.Scopes, ProbeStatus: probe.Status,
			CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}))
		return
	}
	writeJSON(w, http.StatusCreated, importConnectionResponseOf(importConnectionRow{
		ID: probed.ID, Source: probed.Source, Label: probed.Label, BaseUrl: probed.BaseUrl,
		AccountEmail: probed.AccountEmail, Scopes: probed.Scopes, ProbeStatus: probed.ProbeStatus,
		ProbedAt: probed.ProbedAt, CreatedBy: probed.CreatedBy,
		CreatedAt: probed.CreatedAt, UpdatedAt: probed.UpdatedAt,
	}))
}

// defaultImportLabel names an unlabelled connection after what the probe
// found, so the Settings row reads "Linear · acme" rather than "Linear".
func defaultImportLabel(source string, probe imports.ProbeResult) string {
	name := imports.ImportIdentityName(source)
	if ref := strings.TrimSpace(probe.Ref); ref != "" {
		return name + " · " + ref
	}
	return name
}

// ProbeImportConnection re-checks a stored credential and records the verdict.
// It is the endpoint a "Check connection" button calls, and the one that turns
// a silently-expired token into a visible red row before an import needs it.
func (h *Handler) ProbeImportConnection(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "cid"), "connection id")
	if !ok {
		return
	}
	_, adapter, err := h.importAdapterFor(r.Context(), wsUUID, connUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "import connection not found")
			return
		}
		if writeImportSealError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load the import connection")
		return
	}
	probe, _ := adapter.Probe(r.Context())
	status := probe.Status
	if status == "" {
		status = imports.ProbeUnreachable
	}
	row, err := h.Queries.UpdateImportConnectionProbe(r.Context(), db.UpdateImportConnectionProbeParams{
		ID:          connUUID,
		WorkspaceID: wsUUID,
		ProbeStatus: status,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "import connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to record the probe result")
		return
	}
	resp := importConnectionResponseOf(importConnectionRow{
		ID: row.ID, Source: row.Source, Label: row.Label, BaseUrl: row.BaseUrl,
		AccountEmail: row.AccountEmail, Scopes: row.Scopes, ProbeStatus: row.ProbeStatus,
		ProbedAt: row.ProbedAt, CreatedBy: row.CreatedBy,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
	// probe.Detail is operator-facing prose from the adapter and, by the
	// Adapter contract, carries nothing derived from the token.
	writeJSON(w, http.StatusOK, map[string]any{
		"connection": resp,
		"probe": map[string]string{
			"status":  status,
			"account": probe.Account,
			"ref":     probe.Ref,
			"detail":  probe.Detail,
		},
	})
}

// ListImportContainers answers "what is in there?" for the scope picker —
// Linear teams, Jira projects — without paying for a full walk. The Adapter
// contract makes this cheap by design: no issues, no comments.
func (h *Handler) ListImportContainers(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "cid"), "connection id")
	if !ok {
		return
	}
	_, adapter, err := h.importAdapterFor(r.Context(), wsUUID, connUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "import connection not found")
			return
		}
		if writeImportSealError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load the import connection")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	containers, err := adapter.Containers(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not read the source: "+err.Error())
		return
	}
	if containers == nil {
		containers = []imports.Container{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": containers})
}

// DeleteImportConnection removes a connection and its sealed token. Jobs keep
// their history: import_job.connection_id is ON DELETE SET NULL, so a receipt
// survives the credential it was run with.
func (h *Handler) DeleteImportConnection(w http.ResponseWriter, r *http.Request) {
	if !requireImportEnabled(w) {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "cid"), "connection id")
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteImportConnection(r.Context(), db.DeleteImportConnectionParams{
		ID:          connUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete the import connection")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "import connection not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
