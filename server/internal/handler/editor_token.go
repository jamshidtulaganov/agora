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
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-user editor tokens ("Settings → editor account integration"): a human
// pastes a GitHub/GitLab personal access token once, and the daemon injects it
// into THAT user's co-code editor (code-server) environment — gh CLI + HTTPS
// git in the editor terminal authenticate without a per-worktree browser
// sign-in. Sealed at rest with the same AGORA_GIT_SECRET_KEY secretbox as
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
	Provider  string `json:"provider"`
	MaskedTal string `json:"masked"`
	UpdatedAt string `json:"updated_at"`
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
		if row.UpdatedAt.Valid {
			item.UpdatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// PutEditorToken saves (upserts) one provider token for the caller.
// PUT /api/me/editor-tokens  {provider, token}
func (h *Handler) PutEditorToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Token    string `json:"token"`
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
	if err := h.Queries.UpsertUserEditorToken(r.Context(), db.UpsertUserEditorTokenParams{
		UserID: parseUUID(userID), Provider: provider, TokenSealed: sealed,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save token")
		return
	}
	slog.Info("editor token saved", "user_id", userID, "provider", provider)
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "masked": maskToken(token)})
}

// DeleteEditorToken removes one provider token for the caller.
// DELETE /api/me/editor-tokens/{provider}
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
	n, err := h.Queries.DeleteUserEditorToken(r.Context(), db.DeleteUserEditorTokenParams{
		UserID: parseUUID(userID), Provider: provider,
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
// (code-server) process: unsealed tokens keyed by the vars their tooling
// reads. Best-effort — no tokens / no key → nil (editor launches as before).
func (h *Handler) editorEnvForUser(ctx context.Context, userID pgtype.UUID) map[string]string {
	rows, err := h.Queries.ListUserEditorTokens(ctx, userID)
	if err != nil || len(rows) == 0 {
		return nil
	}
	box, err := gitCredentialBox()
	if err != nil {
		return nil
	}
	env := map[string]string{}
	for _, row := range rows {
		plain, oerr := box.Open(row.TokenSealed)
		if oerr != nil {
			continue
		}
		for _, k := range editorEnvVarsForProvider(row.Provider) {
			env[k] = string(plain)
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}
