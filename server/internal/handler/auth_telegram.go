package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Telegram bot-OTP login (NOT the Telegram Login Widget).
//
// Flow:
//  1. The web app calls POST /auth/telegram/start. We mint an unguessable
//     nonce, register it in the in-memory login store, and return a deep link
//     "https://t.me/<bot>?start=login_<nonce>". The app shows it as a button /
//     QR.
//  2. The user taps the link, opening a DM with the bot pre-filled with
//     "/start login_<nonce>". Telegram delivers that as an update to our public
//     webhook POST /telegram/webhook. We bind the nonce to the sender's
//     Telegram id, generate a 6-digit code, and DM it via the bot. We always
//     return 200 (Telegram retries non-2xx and we never want to leak flow
//     state to it).
//  3. The user types the code back into the web app, which calls POST
//     /auth/telegram/verify { nonce, code }. We verify against the store
//     (single-use, TTL'd, constant-time), find-or-create a user keyed by the
//     synthetic email tg<id>@telegram.local, auto-link the (telegram, id) ->
//     user external identity, then issue the SAME session (JWT + auth cookies)
//     as the email-code and Google login paths.
//
// The login store + bot client are Handler fields wired in New() from env
// (TELEGRAM_BOT_TOKEN / TELEGRAM_BOT_USERNAME / TELEGRAM_WEBHOOK_SECRET).
// telegramBot is nil when TELEGRAM_BOT_TOKEN is unset, so the start/verify
// handlers return 503 and /api/config omits telegram_bot_username.

// telegramSyntheticEmailDomain is the local-only domain used to derive a stable
// user email from a Telegram id. These addresses are never deliverable; they
// exist solely so a Telegram-only account fits the email-keyed user table the
// same way every other login path does.
const telegramSyntheticEmailDomain = "telegram.local"

// telegramWebhookSecretHeader is the header Telegram sends when the webhook was
// registered with a secret_token (see setWebhook). We compare it to
// TELEGRAM_WEBHOOK_SECRET when that env var is set.
const telegramWebhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

// newTelegramBotFromEnv constructs the bot client from TELEGRAM_BOT_TOKEN.
// Returns nil when the token is unset/blank so the handlers can 503 and the
// public config can hide the Telegram login option — mirroring how the Lark and
// Google integrations stay dormant until their secrets are provided. An
// optional TANDEM_TELEGRAM_API_BASE_URL override lets tests / proxies point the
// client at a mock server (left empty in normal operation).
func newTelegramBotFromEnv() *telegram.BotClient {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return nil
	}
	c := telegram.NewBotClient(token)
	if base := strings.TrimSpace(os.Getenv("TANDEM_TELEGRAM_API_BASE_URL")); base != "" {
		c.BaseURL = base
	}
	return c
}

// telegramBotUsername returns the configured bot username (without the leading
// @), used to build the t.me deep link and exposed via /api/config so the
// frontend can render the Telegram login button only when it is set.
func telegramBotUsername() string {
	return strings.TrimPrefix(strings.TrimSpace(os.Getenv("TELEGRAM_BOT_USERNAME")), "@")
}

// telegramLoginEnabled reports whether bot-OTP login is fully configured: both
// a bot client (token) and a username (for the deep link) are required.
func (h *Handler) telegramLoginEnabled() bool {
	return h.telegramBot != nil && telegramBotUsername() != ""
}

// generateLoginNonce returns a 128-bit URL-safe random nonce for the deep link.
func generateLoginNonce() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

type telegramStartResponse struct {
	Nonce    string `json:"nonce"`
	DeepLink string `json:"deep_link"`
}

// TelegramStart mints a login nonce and returns the bot deep link.
// POST /auth/telegram/start
func (h *Handler) TelegramStart(w http.ResponseWriter, r *http.Request) {
	if !h.telegramLoginEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Telegram login is not configured")
		return
	}
	if h.telegramLogins == nil {
		writeError(w, http.StatusServiceUnavailable, "Telegram login is not configured")
		return
	}

	nonce, err := generateLoginNonce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start telegram login")
		return
	}
	h.telegramLogins.Start(nonce)

	deepLink := fmt.Sprintf("https://t.me/%s?start=login_%s", telegramBotUsername(), nonce)
	writeJSON(w, http.StatusOK, telegramStartResponse{Nonce: nonce, DeepLink: deepLink})
}

// telegramUpdate is the subset of a Telegram update we consume: the inbound
// message text, the sender's numeric id + first_name, and the chat type. We DM
// the bound user via message.from.id (a private chat's id equals the user id);
// we deliberately do NOT use message.chat.id as the send target — see
// TelegramWebhook. chat.type lets us ignore non-private chats so a "/start" in
// a group never leaks the OTP to the group.
type telegramUpdate struct {
	Message *struct {
		Text string `json:"text"`
		From *struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
		} `json:"from"`
		Chat *struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

// TelegramWebhook receives bot updates. Public (no Tandem auth) — the optional
// secret-token header is the only authentication. On a "/start login_<nonce>"
// message it binds the nonce to the sender, generates a 6-digit code, and DMs
// it. ALWAYS returns 200 so Telegram does not retry and we never leak whether a
// nonce was valid.
// POST /telegram/webhook
func (h *Handler) TelegramWebhook(w http.ResponseWriter, r *http.Request) {
	// Per-IP rate limit BEFORE any work. This is public unauthenticated ingress
	// with the same abuse profile as the autopilot/Stripe webhooks, so it shares
	// the same limiter (h.WebhookIPRateLimiter) and budget knob. Bounds DM/JSON
	// amplification from a request flood. 429 (not 200) so a genuine flooder is
	// throttled; Telegram's real deliveries stay well under the budget.
	if h.WebhookIPRateLimiter != nil {
		if ip := h.clientIPForRateLimit(r); ip != "" {
			if !h.WebhookIPRateLimiter.Allow(r.Context(), ip) {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}
	}

	// Verify the secret token when configured. Constant-time compare so a
	// timing side-channel can't be used to recover the secret.
	if secret := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET")); secret != "" {
		got := r.Header.Get(telegramWebhookSecretHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			// Telegram never sends a wrong secret; a mismatch is a forged
			// request. 401 (not 200) because this is not a real Telegram retry
			// we need to suppress.
			writeError(w, http.StatusUnauthorized, "invalid webhook secret")
			return
		}
	}

	// If the bot/store isn't configured we still 200 so a stray webhook
	// registration doesn't cause Telegram to retry indefinitely.
	if h.telegramBot == nil || h.telegramLogins == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	var update telegramUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		// Malformed body: ack and drop.
		w.WriteHeader(http.StatusOK)
		return
	}
	if update.Message == nil || update.Message.From == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only ever process PRIVATE chats. ParseStartPayload accepts the group form
	// "/start@bot login_<nonce>", so a "/start" issued in a group would
	// otherwise bind the nonce and DM the OTP. We ack 200 (so Telegram does not
	// retry) and ignore anything that is not a 1:1 DM with the bot, so the code
	// is never delivered to a group context.
	if update.Message.Chat == nil || update.Message.Chat.Type != "private" {
		w.WriteHeader(http.StatusOK)
		return
	}

	nonce, ok := telegram.ParseStartPayload(update.Message.Text)
	if !ok {
		// Not a login start command — ignore (the bot may receive other DMs).
		w.WriteHeader(http.StatusOK)
		return
	}

	// Bind identity AND deliver the code to the SAME user (message.from.id). In
	// a private chat chat.id == from.id, but we send to from.id unconditionally
	// so the code can never be routed anywhere other than the bound account.
	telegramID := strconv.FormatInt(update.Message.From.ID, 10)
	firstName := strings.TrimSpace(update.Message.From.FirstName)

	code, err := generateCode()
	if err != nil {
		slog.Error("telegram webhook: failed to generate code", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if !h.telegramLogins.Bind(nonce, telegramID, firstName, code) {
		// Stale / expired / unknown nonce. Let the user know so they restart.
		_ = h.telegramBot.SendMessage(r.Context(), telegramID,
			"This login link has expired. Please start again from the app.")
		w.WriteHeader(http.StatusOK)
		return
	}

	msg := fmt.Sprintf("Your Tandem login code is: %s\n\nIt expires in 5 minutes. If you didn't request this, ignore this message.", code)
	if err := h.telegramBot.SendMessage(r.Context(), telegramID, msg); err != nil {
		// DM failed (e.g. the user blocked the bot). The code is still bound;
		// log and ack — the user simply won't get a code and can retry.
		slog.Warn("telegram webhook: failed to DM login code", "error", err, "telegram_id", telegramID)
	}

	w.WriteHeader(http.StatusOK)
}

type telegramVerifyRequest struct {
	Nonce string `json:"nonce"`
	Code  string `json:"code"`
}

// TelegramVerify exchanges a nonce+code for a session, exactly like VerifyCode /
// GoogleLogin: find-or-create the user, issue a JWT, set auth (+ CloudFront)
// cookies, and return the LoginResponse shape.
// POST /auth/telegram/verify
func (h *Handler) TelegramVerify(w http.ResponseWriter, r *http.Request) {
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

	telegramID, firstName, ok := h.telegramLogins.Verify(nonce, code)
	if !ok {
		// Wrong code, expired, unbound, or already used — all collapse to 401
		// so we don't leak which failure occurred.
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}

	email := telegramSyntheticEmail(telegramID)

	user, isNew, err := h.findOrCreateUser(r.Context(), email)
	if err != nil {
		var signupErr SignupError
		if errors.As(err, &signupErr) {
			writeError(w, http.StatusForbidden, signupErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	if isNew {
		evt := analytics.Signup(uuidToString(user.ID), user.Email, signupSourceFromRequest(r))
		evt.Properties["auth_method"] = "telegram"
		obsmetrics.RecordEvent(h.Analytics, h.Metrics, evt)

		// Seed the display name from the Telegram first_name on first login.
		// findOrCreateUser defaults a new user's name to the email local-part
		// (here the synthetic "tg<id>"), which is meaningless to the user, so we
		// overwrite it with first_name when Telegram provided one. Mirrors the
		// Google path's name backfill; we do NOT touch findOrCreateUser (shared).
		if firstName = strings.TrimSpace(firstName); firstName != "" && firstName != user.Name {
			updated, uerr := h.Queries.UpdateUser(r.Context(), db.UpdateUserParams{
				ID:   user.ID,
				Name: firstName,
			})
			if uerr != nil {
				slog.Warn("telegram verify: failed to set first_name as user name",
					append(logger.RequestAttrs(r), "error", uerr, "user_id", uuidToString(user.ID))...)
			} else {
				user = updated
			}
		}
	}

	// Auto-link the Telegram identity to this user so inbound Telegram events
	// (and future logins) resolve to the same account. Reuses the shared
	// external-identity mapping from external_identity.go.
	if err := h.linkExternalIdentity(r.Context(), providerTelegram, telegramID, uuidToString(user.ID)); err != nil {
		slog.Warn("telegram verify: failed to link external identity",
			append(logger.RequestAttrs(r), "error", err, "telegram_id", telegramID, "user_id", uuidToString(user.ID))...)
		// Non-fatal: the login still succeeds. The mapping can be re-linked on
		// the next login.
	}

	tokenString, err := h.issueJWT(user)
	if err != nil {
		slog.Warn("telegram login failed", append(logger.RequestAttrs(r), "error", err, "email", email)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set auth cookies", "error", err)
	}

	if h.CFSigner != nil {
		for _, cookie := range h.CFSigner.SignedCookies(time.Now().Add(auth.AuthTokenTTL())) {
			http.SetCookie(w, cookie)
		}
	}

	slog.Info("user logged in via telegram", append(logger.RequestAttrs(r), "user_id", uuidToString(user.ID), "email", user.Email)...)
	writeJSON(w, http.StatusOK, LoginResponse{
		Token: tokenString,
		User:  userToResponse(user),
	})
}

// telegramSyntheticEmail derives the stable, non-deliverable email used to key a
// Telegram-only user. Lowercased to match the normalization the other login
// paths apply before findOrCreateUser (telegram ids are numeric, so this is
// effectively a no-op, but it keeps the contract identical).
func telegramSyntheticEmail(telegramID string) string {
	return strings.ToLower(fmt.Sprintf("tg%s@%s", telegramID, telegramSyntheticEmailDomain))
}
