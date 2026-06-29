package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// CardActionHandler reacts to a member tapping a `"type":"request"` button on
// one of the Bot's interactive cards. It is the card-action analogue of the
// message Dispatcher: the WS connector decodes the card.action.trigger frame
// and hands the action here, then ACKs 200. The handler does its work out of
// band (resolve the bound member, mutate the issue, patch the card via the IM
// API), so a slow Lark round-trip never holds the long-conn ACK budget.
type CardActionHandler interface {
	Handle(ctx context.Context, inst db.LarkInstallation, action CardAction) error
}

// CardActionDecoder is the optional card-action half of a FrameDecoder. The
// connector type-asserts its FrameDecoder to this interface so the standard
// message FrameDecoder interface stays untouched; only LarkJSONFrameDecoder
// (which implements both) is wired in production.
type CardActionDecoder interface {
	DecodeCardAction(payload []byte, inst db.LarkInstallation) (CardAction, bool, error)
}

// loggingCardActionHandler is a minimal CardActionHandler used to PROVE the
// card.action.trigger long-conn path end to end before the real mutation
// handler is built: it logs every received action and patches the tapped card
// into a confirmation so the user sees the round trip. It performs no issue
// mutation — that lands once the protocol is confirmed against a live Bot.
type loggingCardActionHandler struct {
	api   APIClient
	creds CredentialsProvider
	log   *slog.Logger
}

// NewLoggingCardActionHandler returns the protocol-proving handler. api/creds
// may be nil (then it only logs, no card patch).
func NewLoggingCardActionHandler(api APIClient, creds CredentialsProvider, log *slog.Logger) CardActionHandler {
	if log == nil {
		log = slog.Default()
	}
	return &loggingCardActionHandler{api: api, creds: creds, log: log}
}

func (h *loggingCardActionHandler) Handle(ctx context.Context, inst db.LarkInstallation, action CardAction) error {
	h.log.Info("lark card action received",
		"installation_id", uuidString(inst.ID),
		"open_id", string(action.OperatorOpenID),
		"message_id", action.MessageID,
		"value", fmt.Sprintf("%v", action.Value),
	)
	if h.api == nil || h.creds == nil || action.MessageID == "" {
		return nil
	}
	creds, err := h.creds.Credentials(ctx, inst)
	if err != nil {
		return fmt.Errorf("resolve creds: %w", err)
	}
	card, err := cardActionAckCard(action.Value)
	if err != nil {
		return err
	}
	return h.api.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: action.MessageID,
		CardJSON:          card,
	})
}

// cardActionAckCard builds a small green confirmation card that replaces the
// tapped card, proving the inbound event AND the outbound patch both work.
func cardActionAckCard(value map[string]string) (string, error) {
	label := value["action"]
	if label == "" {
		label = "action"
	}
	return noticeCard("green", "✅ Received: **"+label+"**")
}

// noticeCard builds a single-line card-1.0 notice with the given header color.
func noticeCard(headerColor, body string) (string, error) {
	doc := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": headerColor,
			"title":    map[string]any{"tag": "plain_text", "content": "Agora"},
		},
		"elements": []any{
			map[string]any{
				"tag":  "div",
				"text": map[string]any{"tag": "lark_md", "content": body},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// IssueStatusUpdater applies a card-action status change in the Agora core,
// attributed to a bound member. Implemented in the handler package (which owns
// the issue update + EventIssueUpdated publish); injected here so the lark
// package stays free of handler/HTTP coupling.
type IssueStatusUpdater interface {
	UpdateIssueStatusForLark(ctx context.Context, issueID, newStatus, actorUserID string) error
}

// CardActionQueries is the narrow DB surface the issue card-action handler
// needs (the bound-member identity gate). Pinned to an interface so tests can
// substitute a fake.
type CardActionQueries interface {
	GetLarkUserBindingByOpenID(ctx context.Context, arg db.GetLarkUserBindingByOpenIDParams) (db.LarkUserBinding, error)
}

// issueCardActionHandler is the production CardActionHandler. It resolves the
// tapping member (only a bound workspace member may act), applies the requested
// mutation through IssueStatusUpdater, and patches the card to the new state.
type issueCardActionHandler struct {
	queries CardActionQueries
	updater IssueStatusUpdater
	api     APIClient
	creds   CredentialsProvider
	log     *slog.Logger
}

// NewIssueCardActionHandler wires the production handler.
func NewIssueCardActionHandler(queries CardActionQueries, updater IssueStatusUpdater, api APIClient, creds CredentialsProvider, log *slog.Logger) CardActionHandler {
	if log == nil {
		log = slog.Default()
	}
	return &issueCardActionHandler{queries: queries, updater: updater, api: api, creds: creds, log: log}
}

func (h *issueCardActionHandler) Handle(ctx context.Context, inst db.LarkInstallation, action CardAction) error {
	cmd := action.Value["action"]
	issueID := action.Value["issue_id"]
	h.log.Info("lark card action",
		"installation_id", uuidString(inst.ID),
		"open_id", string(action.OperatorOpenID),
		"cmd", cmd, "issue_id", issueID)

	// Identity gate: only a bound workspace member may mutate.
	binding, err := h.queries.GetLarkUserBindingByOpenID(ctx, db.GetLarkUserBindingByOpenIDParams{
		InstallationID: inst.ID,
		LarkOpenID:     string(action.OperatorOpenID),
	})
	if err != nil {
		return h.patch(ctx, inst, action.MessageID, "yellow", "⚠️ Bind your Agora account first to use this.")
	}
	actorUserID := uuidString(binding.AgoraUserID)

	switch cmd {
	case "set_status":
		newStatus := action.Value["status"]
		if newStatus == "" || issueID == "" {
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Missing status or issue.")
		}
		if err := h.updater.UpdateIssueStatusForLark(ctx, issueID, newStatus, actorUserID); err != nil {
			h.log.Warn("lark card action: status update failed", "issue_id", issueID, "err", err.Error())
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Couldn't update status.")
		}
		return h.patch(ctx, inst, action.MessageID, "green", "✅ Status updated to **"+humanStatus(newStatus)+"**")
	default:
		return h.patch(ctx, inst, action.MessageID, "grey", "Unknown action.")
	}
}

func (h *issueCardActionHandler) patch(ctx context.Context, inst db.LarkInstallation, messageID, color, body string) error {
	if h.api == nil || h.creds == nil || messageID == "" {
		return nil
	}
	creds, err := h.creds.Credentials(ctx, inst)
	if err != nil {
		return fmt.Errorf("resolve creds: %w", err)
	}
	card, err := noticeCard(color, body)
	if err != nil {
		return err
	}
	return h.api.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: messageID,
		CardJSON:          card,
	})
}

// humanStatus maps an issue status value to a readable label for card copy.
func humanStatus(s string) string {
	switch s {
	case "in_review":
		return "In Review"
	case "in_progress":
		return "In Progress"
	case "todo":
		return "Todo"
	case "done":
		return "Done"
	case "backlog":
		return "Backlog"
	case "blocked":
		return "Blocked"
	case "cancelled":
		return "Cancelled"
	default:
		return s
	}
}
