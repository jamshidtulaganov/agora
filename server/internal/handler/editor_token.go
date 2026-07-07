package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-user editor tokens ("Settings → editor account integration"): a human
// pastes a GitHub/GitLab personal access token once, and the daemon injects it
// into THAT user's co-code editor (code-server) environment — gh CLI + HTTPS
// git in the editor terminal authenticate without a per-worktree browser
// sign-in. A token is either the user's GLOBAL default (workspace_id NULL) or
// a per-WORKSPACE override (work vs personal identity per workspace); an
// editor launched on a workspace's issue resolves workspace-specific first,
// then global. Sealed at rest with the same AGORA_GIT_SECRET_KEY secretbox as
// git_credential (one ops knob for all sealed material). The raw token is
// never returned to the frontend — only a masked tail.

var editorTokenProviders = map[string]bool{"github": true, "gitlab": true}

// editorEnvVarsForProvider maps a provider to the env vars its tooling reads.
// GH_TOKEN + GITHUB_TOKEN both set for github (gh CLI reads GH_TOKEN first,
// most actions/tools read GITHUB_TOKEN); GITLAB_TOKEN for glab.
func editorEnvVarsForProvider(provider string) []string {
	switch provider {
	case "github":
		return []string{"GH_TOKEN", "GITHUB_TOKEN"}
	case "gitlab":
		return []string{"GITLAB_TOKEN"}
	default:
		return nil
	}
}

type editorTokenResponse struct {
	Provider    string `json:"provider"`
	MaskedTal   string `json:"masked"`
	WorkspaceID string `json:"workspace_id"` // "" = global default
	UpdatedAt   string `json:"updated_at"`
}

func maskToken(tail string) string {
	r := []rune(tail)
	if len(r) <= 4 {
		return "••••"
	}
	return "••••" + string(r[len(r)-4:])
}

// ListEditorTokens returns the caller's configured editor tokens, masked.
// GET /api/me/editor-tokens
func (h *Handler) ListEditorTokens(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListUserEditorTokens(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list editor tokens")
		return
	}
	out := []editorTokenResponse{}
	box, boxErr := gitCredentialBox()
	for _, row := range rows {
		masked := "••••"
		if boxErr == nil {
			if plain, oerr := box.Open(row.TokenSealed); oerr == nil {
				masked = maskToken(string(plain))
			}
		}
		item := editorTokenResponse{Provider: row.Provider, MaskedTal: masked}
		if row.WorkspaceID.Valid {
			item.WorkspaceID = uuidToString(row.WorkspaceID)
		}
		if row.UpdatedAt.Valid {
			item.UpdatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// PutEditorToken saves (upserts) one provider token for the caller — global by
// default, or scoped to one workspace when workspace_id is set.
// PUT /api/me/editor-tokens  {provider, token, workspace_id?}
func (h *Handler) PutEditorToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req struct {
		Provider    string `json:"provider"`
		Token       string `json:"token"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if !editorTokenProviders[provider] {
		writeError(w, http.StatusBadRequest, "provider must be github or gitlab")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" || utf8.RuneCountInString(token) > 300 {
		writeError(w, http.StatusBadRequest, "token is required (max 300 chars)")
		return
	}
	box, err := gitCredentialBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "editor tokens are not configured on this server (AGORA_GIT_SECRET_KEY unset)")
		return
	}
	sealed, err := box.Seal([]byte(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal token")
		return
	}

	if raw := strings.TrimSpace(req.WorkspaceID); raw != "" {
		wsUUID, perr := util.ParseUUID(raw)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		// A workspace override only makes sense on a workspace the caller is a
		// member of; this also blocks junk rows against arbitrary uuids.
		if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
			UserID: parseUUID(userID), WorkspaceID: wsUUID,
		}); merr != nil {
			writeError(w, http.StatusForbidden, "not a member of this workspace")
			return
		}
		if err := h.Queries.UpsertUserEditorTokenWorkspace(r.Context(), db.UpsertUserEditorTokenWorkspaceParams{
			UserID: parseUUID(userID), Provider: provider, TokenSealed: sealed, WorkspaceID: wsUUID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save token")
			return
		}
		slog.Info("editor token saved", "user_id", userID, "provider", provider, "workspace_id", raw)
		writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "masked": maskToken(token), "workspace_id": raw})
		return
	}

	if err := h.Queries.UpsertUserEditorTokenGlobal(r.Context(), db.UpsertUserEditorTokenGlobalParams{
		UserID: parseUUID(userID), Provider: provider, TokenSealed: sealed,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save token")
		return
	}
	slog.Info("editor token saved", "user_id", userID, "provider", provider, "workspace_id", "")
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "masked": maskToken(token), "workspace_id": ""})
}

// DeleteEditorToken removes one provider token for the caller: the global row
// by default, or a workspace override via ?workspace_id=.
// DELETE /api/me/editor-tokens/{provider}[?workspace_id=…]
func (h *Handler) DeleteEditorToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "provider")))
	if !editorTokenProviders[provider] {
		writeError(w, http.StatusBadRequest, "provider must be github or gitlab")
		return
	}
	var wsUUID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("workspace_id")); raw != "" {
		id, perr := util.ParseUUID(raw)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		wsUUID = id
	}
	n, err := h.Queries.DeleteUserEditorToken(r.Context(), db.DeleteUserEditorTokenParams{
		UserID: parseUUID(userID), Provider: provider, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete token")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "no token for this provider")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// editorEnvForUser resolves the env map injected into a user's editor
// (code-server) process for an editor opened on workspaceID's issue: per
// provider, a workspace-scoped token wins over the global default. Best-effort
// — no tokens / no key → nil (editor launches as before).
func (h *Handler) editorEnvForUser(ctx context.Context, userID, workspaceID pgtype.UUID) map[string]string {
	rows, err := h.Queries.ListUserEditorTokensForWorkspace(ctx, db.ListUserEditorTokensForWorkspaceParams{
		UserID: userID, WorkspaceID: workspaceID,
	})
	if err != nil || len(rows) == 0 {
		return nil
	}
	box, err := gitCredentialBox()
	if err != nil {
		return nil
	}
	// provider → sealed, workspace-specific overriding global.
	chosen := map[string][]byte{}
	scoped := map[string]bool{}
	for _, row := range rows {
		if scoped[row.Provider] {
			continue // a workspace row already won for this provider
		}
		chosen[row.Provider] = row.TokenSealed
		if row.WorkspaceID.Valid {
			scoped[row.Provider] = true
		}
	}
	env := map[string]string{}
	for provider, sealed := range chosen {
		plain, oerr := box.Open(sealed)
		if oerr != nil {
			continue
		}
		for _, k := range editorEnvVarsForProvider(provider) {
			env[k] = string(plain)
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}
