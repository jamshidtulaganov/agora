package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Per-developer standing dev servers ("preview per project → per user"). Each
// member declares their OWN deployed dev-server URL per project — the box they
// already develop against (e.g. https://jamshid.sdteam.uz for sd-main). QA
// preview resolution routes an issue to the assignee developer's box (see
// qa_target.go userDevServerURL) before the project-wide qa_smoke_url.
//
// Writes are self-only: a member manages their own row, never a teammate's.
// Unlike local_directory.preview_url this URL is PUBLIC by design — the hosted
// web app iframes it directly, so loopback/RFC-1918 restrictions don't apply.

type userDevServerEntry struct {
	UserID    string `json:"user_id"`
	BaseURL   string `json:"base_url"`
	UpdatedAt string `json:"updated_at"`
}

// validateDevServerURL rejects anything that is not a plain absolute http(s)
// URL. Credentials-in-URL are rejected — the row is workspace-readable and a
// basic-auth secret must not ride in it.
func validateDevServerURL(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "base_url is required"
	}
	if len(trimmed) > 500 {
		return "", "base_url is too long"
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "base_url is not a valid URL"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "base_url must be http or https"
	}
	if parsed.Host == "" {
		return "", "base_url must include a host"
	}
	if parsed.User != nil {
		return "", "base_url must not embed credentials"
	}
	return trimmed, ""
}

// ListProjectDevServers returns every member's dev server for the project.
// Any workspace member may read — the UI shows teammates' boxes read-only.
func (h *Handler) ListProjectDevServers(w http.ResponseWriter, r *http.Request) {
	project, wsID, ok := h.loadProjectForConfig(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, wsID, "project not found"); !ok {
		return
	}
	rows, err := h.Queries.ListUserDevServersForProject(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list dev servers")
		return
	}
	entries := make([]userDevServerEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, userDevServerEntry{
			UserID:    uuidToString(row.UserID),
			BaseURL:   row.BaseUrl,
			UpdatedAt: row.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dev_servers": entries})
}

type setMyDevServerRequest struct {
	BaseURL string `json:"base_url"`
}

// SetMyProjectDevServer upserts the CALLER's dev server for the project.
// Any member, self-only, human-only (router gate).
func (h *Handler) SetMyProjectDevServer(w http.ResponseWriter, r *http.Request) {
	project, wsID, ok := h.loadProjectForConfig(w, r)
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, wsID, "project not found")
	if !ok {
		return
	}
	var req setMyDevServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	normalized, verr := validateDevServerURL(req.BaseURL)
	if verr != "" {
		writeError(w, http.StatusBadRequest, verr)
		return
	}
	row, err := h.Queries.UpsertUserDevServer(r.Context(), db.UpsertUserDevServerParams{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		UserID:      member.UserID,
		BaseUrl:     normalized,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save dev server")
		return
	}
	writeJSON(w, http.StatusOK, userDevServerEntry{
		UserID:    uuidToString(row.UserID),
		BaseURL:   row.BaseUrl,
		UpdatedAt: row.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// DeleteMyProjectDevServer removes the CALLER's dev server for the project.
func (h *Handler) DeleteMyProjectDevServer(w http.ResponseWriter, r *http.Request) {
	project, wsID, ok := h.loadProjectForConfig(w, r)
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, wsID, "project not found")
	if !ok {
		return
	}
	if err := h.Queries.DeleteUserDevServer(r.Context(), db.DeleteUserDevServerParams{
		ProjectID: project.ID,
		UserID:    member.UserID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete dev server")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
