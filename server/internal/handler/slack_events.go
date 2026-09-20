package handler

// Slack Events API ingress: POST /slack/events.
//
// The handler's entire job in PR 1 is: read the RAW body → verify the
// signature and timestamp → answer url_verification → act on the two lifecycle
// events that invalidate a stored credential → 200. Nothing that can block
// runs here. Slack's budget is "respond within three seconds", and an app that
// fails "more than 95% of delivery attempts within 60 minutes" has its event
// subscriptions temporarily disabled — so a slow path here is not a latency
// problem, it is an outage.
//
// Event fan-out (notifications) is PR 2 and unfurling is PR 3; until then every
// other event is acknowledged and dropped on the floor, which is exactly what
// a subscription we have not implemented yet should do.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
)

// slackEventsMaxBody caps what we read from an ingress request. Slack event
// payloads are small; a body larger than this is not a Slack event.
const slackEventsMaxBody = 1 << 20 // 1 MiB

// slackEventEnvelope is the outer payload of every Events API delivery. Parsed
// into an explicit struct, never a bare map: an unknown event type must read as
// "a type we do not handle", not as a missing key panic.
type slackEventEnvelope struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	TeamID    string `json:"team_id"`
	APIAppID  string `json:"api_app_id"`
	EventID   string `json:"event_id"`
	Event     struct {
		Type string `json:"type"`
		// link_shared: who posted, where, and which links Slack found. The
		// event carries NO surrounding message text — by design on Slack's
		// side — so the URL string is the entire input to the unfurl.
		User      string            `json:"user"`
		Channel   string            `json:"channel"`
		MessageTS string            `json:"message_ts"`
		Links     []slackSharedLink `json:"links"`
		// tokens_revoked carries the revoked token ids by kind. A revoked
		// *user* token does not kill the installation; a revoked bot token does.
		Tokens struct {
			OAuth []string `json:"oauth"`
			Bot   []string `json:"bot"`
		} `json:"tokens"`
	} `json:"event"`
}

// SlackEvents handles POST /slack/events.
//
// Mounted outside the authenticated group: the signature over the raw body IS
// the credential, exactly as /api/webhooks/github and /telegram/webhook work.
func (h *Handler) SlackEvents(w http.ResponseWriter, r *http.Request) {
	signingSecret := slackSigningSecret()
	if signingSecret == "" {
		// Degraded mode is explicit. Without the signing secret we cannot tell a
		// real delivery from a forged one, and "accept everything" is not a
		// fallback — it is an open write path into the workspace.
		writeError(w, http.StatusServiceUnavailable, "slack integration is not configured")
		return
	}

	// The signature covers the RAW bytes: read them before any decode, and
	// never re-serialize a decoded payload to verify.
	body, err := io.ReadAll(io.LimitReader(r.Body, slackEventsMaxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	if err := slack.VerifyRequestSignature(signingSecret, r.Header, body, time.Now()); err != nil {
		// The reason is logged, never echoed: telling a prober which check
		// failed is free reconnaissance.
		slog.Warn("slack events: signature rejected", "error", err)
		writeError(w, http.StatusUnauthorized, "invalid slack signature")
		return
	}

	var envelope slackEventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid slack event payload")
		return
	}

	// When the deployment names its app, refuse deliveries from another one.
	// A signature match already proves the sender holds this app's signing
	// secret, so this is defence in depth against a mis-pointed subscription.
	if want := strings.TrimSpace(os.Getenv("AGORA_SLACK_APP_ID")); want != "" &&
		envelope.APIAppID != "" && envelope.APIAppID != want {
		slog.Warn("slack events: app id mismatch", "got", envelope.APIAppID)
		writeError(w, http.StatusUnauthorized, "unexpected slack app")
		return
	}

	switch envelope.Type {
	case "url_verification":
		// Setup handshake: echo the challenge verbatim, as text/plain.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, envelope.Challenge)
		return

	case "event_callback":
		h.handleSlackEventCallback(r, envelope)

	default:
		// Unknown envelope type (Slack adds them over time). Ack, ignore.
		slog.Debug("slack events: unhandled envelope type", "type", envelope.Type)
	}

	// Always 200 past verification. A non-2xx buys a retry storm and, at
	// volume, a disabled subscription — and nothing we could do with a retry
	// of an event we chose not to handle.
	w.WriteHeader(http.StatusOK)
}

// handleSlackEventCallback dispatches the events Phase 1 subscribes to:
// link_shared (unfurling, slack_unfurl.go) and the two lifecycle events that
// invalidate a stored bot token. Everything else is acknowledged and ignored.
//
// Both events arrive with a team id and NO workspace context, and one Slack
// team may back several Agora workspaces sharing one bot token — so the
// revocation is team-wide by construction.
func (h *Handler) handleSlackEventCallback(r *http.Request, envelope slackEventEnvelope) {
	teamID := strings.TrimSpace(envelope.TeamID)

	switch envelope.Event.Type {
	case "link_shared":
		// Detaches immediately: resolving an issue and calling chat.unfurl is
		// database and network work, and it must not run inside the
		// three-second ack budget.
		h.handleSlackLinkShared(envelope)

	case "app_uninstalled":
		if teamID == "" {
			return
		}
		h.revokeSlackInstallationsForTeam(r, teamID, "app_uninstalled")

	case "tokens_revoked":
		// A revoked *user* token only breaks that person's link (PR 3's personal
		// connect); the installation keeps working. Only a revoked bot token
		// kills it.
		if teamID == "" || len(envelope.Event.Tokens.Bot) == 0 {
			return
		}
		h.revokeSlackInstallationsForTeam(r, teamID, "tokens_revoked")

	default:
		slog.Debug("slack events: unhandled event type", "type", envelope.Event.Type, "event_id", envelope.EventID)
	}
}

// revokeSlackInstallationsForTeam marks every active installation for a Slack
// team revoked. Rows are marked, not deleted, so the audit trail survives and
// a re-install flips them back.
func (h *Handler) revokeSlackInstallationsForTeam(r *http.Request, teamID, reason string) {
	affected, err := h.Queries.RevokeSlackInstallationsForTeam(r.Context(), teamID)
	if err != nil {
		// Logged, not surfaced: Slack gets its 200 either way, and the delivery
		// path independently marks an installation revoked the first time the
		// dead token is used (slack.IsTokenInvalid).
		slog.Error("slack events: failed to revoke installations", "team_id", teamID, "reason", reason, "error", err)
		return
	}
	if affected > 0 {
		slog.Info("slack events: installations revoked", "team_id", teamID, "reason", reason, "count", affected)
	}
}
