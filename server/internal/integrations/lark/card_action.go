package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

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

// IssueCardActions applies card-action mutations in the Agora core, attributed
// to a bound member. Implemented in the handler package (which owns the issue
// update + EventIssueUpdated publish + the qa:pass auto_docs automation);
// injected here so the lark package stays free of handler/HTTP coupling.
type IssueCardActions interface {
	UpdateIssueStatusForLark(ctx context.Context, issueID, newStatus, actorUserID string) error
	AttachLabelByNameForLark(ctx context.Context, issueID, labelName, actorUserID string) error
	AssignIssueToMemberForLark(ctx context.Context, issueID, memberUserID string) error
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
	updater IssueCardActions
	api     APIClient
	creds   CredentialsProvider
	log     *slog.Logger
}

// NewIssueCardActionHandler wires the production handler.
func NewIssueCardActionHandler(queries CardActionQueries, updater IssueCardActions, api APIClient, creds CredentialsProvider, log *slog.Logger) CardActionHandler {
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

	if issueID == "" {
		return h.patch(ctx, inst, action.MessageID, "red", "❌ Missing issue.")
	}

	switch cmd {
	case "set_status":
		newStatus := action.Value["status"]
		if newStatus == "" {
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Missing status.")
		}
		if err := h.updater.UpdateIssueStatusForLark(ctx, issueID, newStatus, actorUserID); err != nil {
			h.log.Warn("lark card action: status update failed", "issue_id", issueID, "err", err.Error())
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Couldn't update status.")
		}
		return h.patch(ctx, inst, action.MessageID, "green", "✅ Status updated to **"+humanStatus(newStatus)+"**")
	case "qa_pass":
		if err := h.updater.AttachLabelByNameForLark(ctx, issueID, "qa:pass", actorUserID); err != nil {
			h.log.Warn("lark card action: qa:pass failed", "issue_id", issueID, "err", err.Error())
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Couldn't apply QA pass.")
		}
		return h.patch(ctx, inst, action.MessageID, "green", "✅ QA passed")
	case "qa_fail":
		if err := h.updater.AttachLabelByNameForLark(ctx, issueID, "qa:fail", actorUserID); err != nil {
			h.log.Warn("lark card action: qa:fail failed", "issue_id", issueID, "err", err.Error())
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Couldn't apply QA fail.")
		}
		return h.patch(ctx, inst, action.MessageID, "red", "❌ QA failed")
	case "assign_me":
		if err := h.updater.AssignIssueToMemberForLark(ctx, issueID, actorUserID); err != nil {
			h.log.Warn("lark card action: assign failed", "issue_id", issueID, "err", err.Error())
			return h.patch(ctx, inst, action.MessageID, "red", "❌ Couldn't assign.")
		}
		return h.patch(ctx, inst, action.MessageID, "green", "✅ Assigned to you")
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

// IssueActionCard builds the interactive confirmation card posted after /issue:
// the new issue's identifier + title, status-transition buttons that mutate it
// in place (handled by issueCardActionHandler), and an Open-in-Agora link.
// identifier is the human key (e.g. MUL-42); issueID is the UUID the buttons
// carry; issueURL is the absolute web link (empty omits the link button).
func IssueActionCard(identifier, title, issueID, issueURL string) (string, error) {
	header := "Created " + identifier
	body := strings.TrimSpace(title)
	if body == "" {
		body = "_(no title)_"
	}

	buttons := []any{
		statusButton("Mark In Review", "in_review", issueID),
		statusButton("Done", "done", issueID),
		actionButton("Assign to me", "default", map[string]string{"action": "assign_me", "issue_id": issueID}),
		actionButton("QA ✅", "default", map[string]string{"action": "qa_pass", "issue_id": issueID}),
		actionButton("QA ❌", "default", map[string]string{"action": "qa_fail", "issue_id": issueID}),
	}
	if issueURL != "" {
		buttons = append(buttons, map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "plain_text", "content": "Open in Agora"},
			"type": "default",
			"url":  issueURL,
		})
	}

	doc := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": header},
		},
		"elements": []any{
			map[string]any{
				"tag":  "div",
				"text": map[string]any{"tag": "lark_md", "content": body},
			},
			map[string]any{"tag": "action", "actions": buttons},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// statusButton builds a request-button that sets an issue's status when tapped.
func statusButton(label, status, issueID string) map[string]any {
	return actionButton(label, "primary", map[string]string{"action": "set_status", "status": status, "issue_id": issueID})
}

// actionButton builds a request-button carrying an arbitrary card-action value
// payload (handled by issueCardActionHandler).
func actionButton(label, btnType string, value map[string]string) map[string]any {
	return map[string]any{
		"tag":   "button",
		"text":  map[string]any{"tag": "plain_text", "content": label},
		"type":  btnType,
		"value": value,
	}
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
