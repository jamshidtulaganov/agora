package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// External identity providers. A Agora user is mapped to their id on these
// systems so inbound events resolve to a member (e.g. Bitrix RESPONSIBLE_ID ->
// member) and alternate logins bind to the same account (Telegram).
const (
	providerTelegram = "telegram"
	providerBitrix   = "bitrix"
)

// errExternalIdentityClaimed is returned by linkExternalIdentity when the
// (provider, external_id) is already mapped to a DIFFERENT user. The mapping is
// left untouched — a member must not be able to re-link (steal) another user's
// external identity.
var errExternalIdentityClaimed = errors.New("external identity already linked to another user")

// linkExternalIdentity binds a (provider, external_id) -> user_id mapping. It
// is idempotent for the SAME user (re-linking your own id is a no-op upsert),
// but it will NOT overwrite a mapping owned by a different user: that returns
// errExternalIdentityClaimed and leaves the existing mapping intact, closing the
// identity-steal hole. Raw pgx (no sqlc) because user_external_identity is
// intentionally outside the generated set.
//
// The ON CONFLICT ... DO UPDATE is guarded by a WHERE clause that only fires
// when the existing row already belongs to this user. When the conflict row
// belongs to someone else the WHERE fails, no row is updated, and ExecResult
// reports 0 rows affected — which we translate into the sentinel error.
func (h *Handler) linkExternalIdentity(ctx context.Context, provider, externalID, userID string) error {
	tag, err := h.DB.Exec(ctx,
		`INSERT INTO user_external_identity (provider, external_id, user_id)
		 VALUES ($1, $2, $3::uuid)
		 ON CONFLICT (provider, external_id)
		 DO UPDATE SET user_id = EXCLUDED.user_id, updated_at = now()
		 WHERE user_external_identity.user_id = EXCLUDED.user_id`,
		provider, externalID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// No insert and no update fired: the row exists and is owned by a
		// different user. Do NOT overwrite — surface the steal attempt.
		return errExternalIdentityClaimed
	}
	return nil
}

// userIDByExternalIdentity returns the user_id mapped to (provider, external_id),
// or "" when no mapping exists.
func (h *Handler) userIDByExternalIdentity(ctx context.Context, provider, externalID string) (string, error) {
	var userID string
	err := h.DB.QueryRow(ctx,
		`SELECT user_id::text FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
		provider, externalID).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return userID, nil
}

// telegramIDByUserID returns the Telegram external_id linked to userID, or ""
// when the user has no linked Telegram identity. The reverse of
// userIDByExternalIdentity, used by the bot push path to find a recipient's
// chat. Raw pgx (user_external_identity is intentionally outside the sqlc set).
// A user has at most one telegram identity (the link-steal guard in
// linkExternalIdentity prevents rebinding), so LIMIT 1 is belt-and-suspenders.
func (h *Handler) telegramIDByUserID(ctx context.Context, userID string) (string, error) {
	var externalID string
	err := h.DB.QueryRow(ctx,
		`SELECT external_id FROM user_external_identity WHERE provider = $1 AND user_id = $2::uuid LIMIT 1`,
		providerTelegram, userID).Scan(&externalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return externalID, nil
}

// bitrixIDByUserID returns the user's linked Bitrix responsible id, or "" when
// none. Lets AcceptInvitation honour a Bitrix-pinned invite even when the
// accepter logs in with an email different from the one the invite was
// addressed to (the whole point of pinning a Bitrix user to the invite).
func (h *Handler) bitrixIDByUserID(ctx context.Context, userID string) string {
	var externalID string
	if err := h.DB.QueryRow(ctx,
		`SELECT external_id FROM user_external_identity WHERE provider = $1 AND user_id = $2::uuid LIMIT 1`,
		providerBitrix, userID).Scan(&externalID); err != nil {
		return ""
	}
	return strings.TrimSpace(externalID)
}

type linkBitrixRequest struct {
	BitrixUserID string `json:"bitrix_user_id"`
}

// LinkBitrixIdentity maps the authenticated user to their Bitrix24 user id so a
// synced Bitrix task's RESPONSIBLE_ID resolves to this member on the board.
// POST /api/me/links/bitrix
func (h *Handler) LinkBitrixIdentity(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req linkBitrixRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	bitrixID := strings.TrimSpace(req.BitrixUserID)
	if bitrixID == "" {
		writeError(w, http.StatusBadRequest, "bitrix_user_id required")
		return
	}
	if err := h.linkExternalIdentity(r.Context(), providerBitrix, bitrixID, userID); err != nil {
		if errors.Is(err, errExternalIdentityClaimed) {
			writeError(w, http.StatusConflict, "bitrix_user_id already linked to another account")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to link identity")
		return
	}
	writeJSON(w, http.StatusOK, externalLinkResponse{Provider: providerBitrix, ExternalID: bitrixID})
}

type externalLinkResponse struct {
	Provider   string `json:"provider"`
	ExternalID string `json:"external_id"`
}

// ListMyLinks returns the authenticated user's external identity mappings.
// GET /api/me/links
func (h *Handler) ListMyLinks(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(),
		`SELECT provider, external_id FROM user_external_identity WHERE user_id = $1::uuid ORDER BY provider`,
		userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list links")
		return
	}
	defer rows.Close()

	links := []externalLinkResponse{}
	for rows.Next() {
		var l externalLinkResponse
		if err := rows.Scan(&l.Provider, &l.ExternalID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read links")
			return
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read links")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

// unlinkExternalIdentity removes every mapping of provider for userID. Used
// when rebinding Telegram so a user keeps at most one telegram identity
// (telegramIDByUserID returns LIMIT 1).
func (h *Handler) unlinkExternalIdentity(ctx context.Context, provider, userID string) error {
	_, err := h.DB.Exec(ctx,
		`DELETE FROM user_external_identity WHERE provider = $1 AND user_id = $2::uuid`,
		provider, userID)
	return err
}

// replaceTelegramIdentity atomically swaps the caller's Telegram mapping. The
// delete and guarded insert live in one transaction, so a losing concurrent
// claim cannot erase a previously valid link.
func (h *Handler) replaceTelegramIdentity(ctx context.Context, telegramID, userID string) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx,
		`DELETE FROM user_external_identity WHERE provider = $1 AND user_id = $2::uuid`,
		providerTelegram, userID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO user_external_identity (provider, external_id, user_id)
		 VALUES ($1, $2, $3::uuid)
		 ON CONFLICT (provider, external_id)
		 DO UPDATE SET user_id = EXCLUDED.user_id, updated_at = now()
		 WHERE user_external_identity.user_id = EXCLUDED.user_id`,
		providerTelegram, telegramID, userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return errExternalIdentityClaimed
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return errExternalIdentityClaimed
	}
	return tx.Commit(ctx)
}

// StartTelegramLink mints a bot-OTP nonce for linking Telegram to the
// already-authenticated account (Settings → Notifications). Same deep-link
// shape as login, but verify attaches the identity to the current user instead
// of issuing a new session.
// POST /api/me/links/telegram/start
func (h *Handler) StartTelegramLink(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !h.telegramLoginEnabled() || h.telegramLogins == nil {
		writeError(w, http.StatusServiceUnavailable, "Telegram login is not configured")
		return
	}

	nonce, err := generateLoginNonce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start telegram link")
		return
	}
	if err := h.startTelegramLogin(r.Context(), nonce); err != nil {
		slog.Error("telegram link: failed to persist nonce", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start telegram link")
		return
	}

	deepLink := fmt.Sprintf("https://t.me/%s?start=login_%s", telegramBotUsername(), nonce)
	writeJSON(w, http.StatusOK, telegramStartResponse{Nonce: nonce, DeepLink: deepLink})
}

// VerifyTelegramLink consumes a nonce+OTP and binds the Telegram id to the
// authenticated user. Unlike TelegramVerify it does NOT create a synthetic
// user or issue a session — that would steal the caller away from their email
// account. Conflict when the Telegram id is already owned by someone else.
// POST /api/me/links/telegram/verify
func (h *Handler) VerifyTelegramLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if h.telegramLogins == nil {
		writeError(w, http.StatusServiceUnavailable, "Telegram login is not configured")
		return
	}

	var req telegramVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	nonce := strings.TrimSpace(req.Nonce)
	code := strings.TrimSpace(req.Code)
	if nonce == "" || code == "" {
		writeError(w, http.StatusBadRequest, "nonce and code are required")
		return
	}

	telegramID, _, okVerify := h.verifyTelegramLogin(r.Context(), nonce, code)
	if !okVerify {
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}

	if err := h.replaceTelegramIdentity(r.Context(), telegramID, userID); err != nil {
		if errors.Is(err, errExternalIdentityClaimed) {
			writeError(w, http.StatusConflict, "telegram account already linked to another Agora user")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to link identity")
		return
	}
	writeJSON(w, http.StatusOK, externalLinkResponse{Provider: providerTelegram, ExternalID: telegramID})
}

// UnlinkTelegramIdentity removes the authenticated user's Telegram mapping so
// inbox push stops. Does not touch per-agent bot installations.
// DELETE /api/me/links/telegram
func (h *Handler) UnlinkTelegramIdentity(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if err := h.unlinkExternalIdentity(r.Context(), providerTelegram, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unlink identity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
