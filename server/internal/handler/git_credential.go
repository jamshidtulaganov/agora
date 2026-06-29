package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-workspace git credentials let one workspace clone private repos across
// several git accounts (e.g. two GitHub accounts for two companies). The PAT is
// sealed at rest with a secretbox loaded from AGORA_GIT_SECRET_KEY; if that key
// is unset the endpoints fail closed (503) rather than store plaintext.

var (
	gitBoxOnce sync.Once
	gitBoxVal  *secretbox.Box
	gitBoxErr  error
)

func gitCredentialBox() (*secretbox.Box, error) {
	gitBoxOnce.Do(func() {
		key, err := secretbox.LoadKey("AGORA_GIT_SECRET_KEY")
		if err != nil {
			gitBoxErr = err
			return
		}
		gitBoxVal, gitBoxErr = secretbox.New(key)
	})
	return gitBoxVal, gitBoxErr
}

type gitCredentialResponse struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Provider  string `json:"provider"`
	Host      string `json:"host"`
	Owner     string `json:"owner"`
	Username  string `json:"username"`
	AuthKind  string `json:"auth_kind"`
	CreatedAt string `json:"created_at"`
}

type createGitCredentialRequest struct {
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Host     string `json:"host"`
	Owner    string `json:"owner"`
	Username string `json:"username"`
	AuthKind string `json:"auth_kind"`
	Secret   string `json:"secret"` // write-only PAT; never returned
}

// ListGitCredentials returns the workspace's git credentials WITHOUT the sealed
// secret (the query selects metadata only).
func (h *Handler) ListGitCredentials(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListGitCredentials(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list git credentials")
		return
	}
	resp := make([]gitCredentialResponse, len(rows))
	for i, c := range rows {
		resp[i] = gitCredentialResponse{
			ID:        uuidToString(c.ID),
			Label:     c.Label,
			Provider:  c.Provider,
			Host:      c.Host,
			Owner:     c.Owner,
			Username:  c.Username,
			AuthKind:  c.AuthKind,
			CreatedAt: timestampToString(c.CreatedAt),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateGitCredential adds or rotates the credential for a (workspace, host,
// owner). The PAT is sealed before storage and never echoed back.
func (h *Handler) CreateGitCredential(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req createGitCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// host/owner are stored lowercased so the daemon's repo→credential match is
	// case-insensitive.
	req.Label = strings.TrimSpace(req.Label)
	req.Owner = strings.ToLower(strings.TrimSpace(req.Owner))
	req.Host = strings.ToLower(strings.TrimSpace(req.Host))
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.AuthKind = strings.ToLower(strings.TrimSpace(req.AuthKind))
	req.Username = strings.TrimSpace(req.Username)
	secret := strings.TrimSpace(req.Secret)

	if req.Owner == "" {
		writeError(w, http.StatusBadRequest, "owner is required")
		return
	}
	if secret == "" {
		writeError(w, http.StatusBadRequest, "secret (token) is required")
		return
	}
	if req.Host == "" {
		req.Host = "github.com"
	}
	if req.Provider == "" {
		req.Provider = "github"
	}
	if req.AuthKind == "" {
		req.AuthKind = "token"
	}
	if req.AuthKind != "token" {
		writeError(w, http.StatusBadRequest, "only token credentials are supported")
		return
	}
	if req.Label == "" {
		req.Label = req.Owner
	}

	box, err := gitCredentialBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "git credentials are not configured on this server (AGORA_GIT_SECRET_KEY unset)")
		return
	}
	sealed, err := box.Seal([]byte(secret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal secret")
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}
	row, err := h.Queries.UpsertGitCredential(r.Context(), db.UpsertGitCredentialParams{
		WorkspaceID:     wsUUID,
		Label:           req.Label,
		Provider:        req.Provider,
		Host:            req.Host,
		Owner:           req.Owner,
		Username:        req.Username,
		AuthKind:        req.AuthKind,
		SecretEncrypted: sealed,
		CreatedBy:       creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save git credential")
		return
	}
	writeJSON(w, http.StatusOK, gitCredentialResponse{
		ID:        uuidToString(row.ID),
		Label:     row.Label,
		Provider:  row.Provider,
		Host:      row.Host,
		Owner:     row.Owner,
		Username:  row.Username,
		AuthKind:  row.AuthKind,
		CreatedAt: timestampToString(row.CreatedAt),
	})
}

// DeleteGitCredential removes a credential by id (scoped to the workspace).
func (h *Handler) DeleteGitCredential(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	credUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "credId"), "credential id")
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteGitCredential(r.Context(), db.DeleteGitCredentialParams{
		ID:          credUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete git credential")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "git credential not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
