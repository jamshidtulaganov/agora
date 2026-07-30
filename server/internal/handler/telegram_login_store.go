package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/config"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sharedTelegramLoginStoreEnabled switches the short-lived Telegram login
// state from the single-process fallback to PostgreSQL. Cloud deployments need
// the shared store because a rolling deploy can route start, webhook, and
// verify requests to different backend processes.
func (h *Handler) sharedTelegramLoginStoreEnabled() bool {
	if h.Queries == nil {
		return false
	}
	return config.Bool("AGORA_TELEGRAM_SHARED_LOGIN_STORE") ||
		config.String("APP_ENV") == "production"
}

func (h *Handler) startTelegramLogin(ctx context.Context, nonce string) error {
	if h.sharedTelegramLoginStoreEnabled() {
		return h.Queries.CreateTelegramLoginAttempt(ctx, nonce)
	}
	h.telegramLogins.Start(nonce)
	return nil
}

func (h *Handler) bindTelegramLogin(
	ctx context.Context,
	nonce string,
	identity string,
	firstName string,
	code string,
) bool {
	if h.sharedTelegramLoginStoreEnabled() {
		affected, err := h.Queries.BindTelegramLoginAttempt(ctx, db.BindTelegramLoginAttemptParams{
			Nonce:            nonce,
			TelegramIdentity: identity,
			FirstName:        firstName,
			Code:             code,
		})
		return err == nil && affected == 1
	}
	return h.telegramLogins.Bind(nonce, identity, firstName, code)
}

func (h *Handler) verifyTelegramLogin(
	ctx context.Context,
	nonce string,
	code string,
) (identity string, firstName string, ok bool) {
	if h.sharedTelegramLoginStoreEnabled() {
		result, err := h.Queries.VerifyTelegramLoginAttempt(
			ctx,
			db.VerifyTelegramLoginAttemptParams{Nonce: nonce, SubmittedCode: code},
		)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Error("telegram login: failed to verify shared login attempt", "error", err)
			}
			return "", "", false
		}
		if !result.Valid {
			return "", "", false
		}
		return result.TelegramIdentity, result.FirstName, true
	}
	return h.telegramLogins.Verify(nonce, code)
}
