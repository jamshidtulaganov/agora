package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// DECIDE FROM THE PHONE (docs/orchestration-upgrade-plan.md §A3).
//
// The Telegram bridge already reaches every member: an inbox item becomes a DM
// (telegram_push_listeners.go → SendIssueInboxDM), and the question primitive
// already proves that a tap can be attributed to a person
// (telegram_agent_api.go, migration 180). What was missing is that NONE of the
// Telegram surfaces could ACT — they notified and linked back to the web app,
// so a merge approval still waited for someone to open a laptop.
//
// This file adds the two button pairs the plan names, and nothing else:
//
//   - on an ESCALATION DM: the escalation's own options, or a link when it is a
//     free-text question;
//   - on a MERGE_READY DM: Approve / Request changes.
//
// FOUR PROPERTIES, each load-bearing:
//
//  1. THE SAME HANDLER, NOT A PARALLEL PATH. A tap builds an in-process request
//     and runs the EXACT handler the web button posts to
//     (ResolveEscalation / CreateReviewDecision). There is no second
//     implementation of "approve" that can drift from the first, and every gate
//     inside those handlers — the workspace fence, the issue visibility gate,
//     the release-step state machine, the already-answered 409 — applies
//     unchanged.
//  2. THE HUMAN IS BOUND OR THERE ARE NO BUTTONS. The acting user is resolved
//     from the Telegram identity binding (user_external_identity), never from
//     the payload. An unbound chat is answered with a link to the app. The
//     synthetic request carries NO X-Actor-Source, so it is a human actor by
//     construction — which is exactly what it is: a person tapped a button.
//  3. REPLAY-SAFE, INDEX-BASED PAYLOADS. Like telegram_question, the INDEX
//     travels and the label is read back from the stored row, so a
//     client-supplied payload can never answer with text that was never
//     offered. A second tap on a settled decision loses the race inside the
//     handler (409) and says so.
//  4. THE DM'S OWN CHAT ONLY. The keyboard is delivered to one person's private
//     chat; a message forwarded into a group must not act. The tap is refused
//     unless it comes from that private chat, from that person.

// telegramDecisionPrefix namespaces these callbacks so a stray button from the
// create wizard ("nw:") or an agent question ("q:") can never be read as a
// decision. Kept to two characters: Telegram caps callback_data at 64 bytes and
// a UUID already spends 36 of them.
const telegramDecisionPrefix = "d:"

const (
	telegramDecisionEscalation = "e" // d:e:<escalation uuid>:<option index>
	telegramDecisionReview     = "r" // d:r:<issue uuid>:a|c
)

const (
	telegramReviewApprove       = "a"
	telegramReviewRequestChange = "c"
)

// telegramDecisionMaxOptions caps an escalation's option buttons. More than a
// handful wraps into an unreadable keyboard on a phone, and a gate nobody can
// read is not a gate. Options past the cap are still answerable in the app.
const telegramDecisionMaxOptions = 6

// telegramNoteWaitTTL bounds how long the bot waits for the free-text note that
// "Request changes" needs. Long enough to type a sentence, short enough that a
// forgotten prompt does not silently swallow the next task someone sends.
const telegramNoteWaitTTL = 10 * time.Minute

// ── pending note capture ────────────────────────────────────────────────────

// pendingDecisionNote is a "Request changes" tap waiting for its note.
//
// request_changes REQUIRES a note — the endpoint 400s without one, because the
// note becomes the correction worker's instruction. So it cannot be one tap: the
// tap opens a short window in which the next plain message in that DM becomes
// the note.
//
// IN-PROCESS AND EPHEMERAL, on purpose. This is a 30-second conversational
// state, not a decision: losing it to a restart costs a retyped sentence, and
// the fallback (the deep link into the app) is always present. Single instance
// only, like projectBuildLocks — a multi-instance deploy would simply see the
// window expire, never a wrong write.
type pendingDecisionNote struct {
	IssueID     string
	WorkspaceID string
	UserID      string
	Identifier  string
	ExpiresAt   time.Time
}

var decisionNoteWaits sync.Map // telegram user id (string) -> pendingDecisionNote

func setPendingDecisionNote(tgID string, p pendingDecisionNote) {
	p.ExpiresAt = time.Now().Add(telegramNoteWaitTTL)
	decisionNoteWaits.Store(tgID, p)
}

func takePendingDecisionNote(tgID string) (pendingDecisionNote, bool) {
	v, ok := decisionNoteWaits.Load(tgID)
	if !ok {
		return pendingDecisionNote{}, false
	}
	decisionNoteWaits.Delete(tgID)
	p, ok := v.(pendingDecisionNote)
	if !ok || time.Now().After(p.ExpiresAt) {
		return pendingDecisionNote{}, false
	}
	return p, true
}

// ── callback payloads ───────────────────────────────────────────────────────

type telegramDecisionCallback struct {
	Action string // telegramDecisionEscalation | telegramDecisionReview
	ID     string // escalation uuid | issue uuid
	Arg    string // option index | "a" | "c"
}

// parseTelegramDecisionCallback reads "d:<action>:<uuid>:<arg>". Anything else
// is not ours, and ownership is reported back so the wizard dispatcher knows
// whether to keep looking.
func parseTelegramDecisionCallback(data string) (telegramDecisionCallback, bool) {
	if !strings.HasPrefix(data, telegramDecisionPrefix) {
		return telegramDecisionCallback{}, false
	}
	parts := strings.Split(strings.TrimPrefix(data, telegramDecisionPrefix), ":")
	if len(parts) != 3 {
		return telegramDecisionCallback{}, false
	}
	switch parts[0] {
	case telegramDecisionEscalation, telegramDecisionReview:
	default:
		return telegramDecisionCallback{}, false
	}
	if _, err := util.ParseUUID(parts[1]); err != nil {
		return telegramDecisionCallback{}, false
	}
	return telegramDecisionCallback{Action: parts[0], ID: parts[1], Arg: parts[2]}, true
}

func escalationOptionCallback(escalationID string, index int) string {
	return telegramDecisionPrefix + telegramDecisionEscalation + ":" + escalationID + ":" + strconv.Itoa(index)
}

func reviewDecisionCallback(issueID, action string) string {
	return telegramDecisionPrefix + telegramDecisionReview + ":" + issueID + ":" + action
}

// ── keyboard construction (the DM side) ─────────────────────────────────────

// decisionDMKeyboard returns the inline keyboard for an inbox DM, or nil when
// this notification type carries no one-tap decision (in which case the caller
// falls back to the plain "open the app" button).
//
// It is given the RESOLVED recipient: SendIssueInboxDM only reaches a member
// whose Telegram identity is bound, so by the time a keyboard is built the
// human behind the buttons is already known. An unbound chat never gets here —
// it gets the app link, which is the honest thing to offer someone we cannot
// attribute a decision to.
func (h *Handler) decisionDMKeyboard(ctx context.Context, lang, notifType, issueID, link string) [][]telegram.Button {
	var rows [][]telegram.Button
	switch notifType {
	case "escalation":
		esc, ok := h.openEscalationForIssueID(ctx, issueID)
		if !ok {
			return nil
		}
		for i, opt := range esc.Options {
			if i >= telegramDecisionMaxOptions {
				break
			}
			label := strings.TrimSpace(opt)
			if label == "" {
				continue
			}
			// One button per row: option labels are sentences ("Deploy to
			// staging"), and side by side they truncate on a phone.
			rows = append(rows, []telegram.Button{{
				Text:         label,
				CallbackData: escalationOptionCallback(uuidToString(esc.ID), i),
			}})
		}
		if len(rows) == 0 {
			// A free-text escalation has no options to tap. Offering a fake
			// "OK" button would answer a question nobody read.
			return nil
		}
	case "merge_ready":
		rows = append(rows, []telegram.Button{
			{Text: decisionBtn(lang, "approve"), CallbackData: reviewDecisionCallback(issueID, telegramReviewApprove)},
			{Text: decisionBtn(lang, "changes"), CallbackData: reviewDecisionCallback(issueID, telegramReviewRequestChange)},
		})
	default:
		return nil
	}
	if link != "" {
		rows = append(rows, []telegram.Button{{Text: dmOpenButton(lang), URL: link}})
	}
	return rows
}

// openEscalationForIssueID resolves the issue's CURRENTLY open escalation. The
// buttons must reflect the open row, not whatever the inbox item's details said
// when it was written — an escalation refined by a second raise has new options.
func (h *Handler) openEscalationForIssueID(ctx context.Context, issueID string) (db.TaskEscalation, bool) {
	issueUUID, err := util.ParseUUID(issueID)
	if err != nil {
		return db.TaskEscalation{}, false
	}
	issue, err := h.Queries.GetIssue(ctx, issueUUID)
	if err != nil {
		return db.TaskEscalation{}, false
	}
	esc, err := h.Queries.GetOpenTaskEscalationForIssue(ctx, db.GetOpenTaskEscalationForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return db.TaskEscalation{}, false
	}
	return esc, true
}

// ── callback handling (the tap side) ────────────────────────────────────────

// handleTelegramDecisionCallback processes a decision-button tap on the
// platform bot. Returns false when the payload is not ours, so the create
// wizard keeps its own callbacks.
func (h *Handler) handleTelegramDecisionCallback(ctx context.Context, update telegramUpdate) bool {
	cb := update.CallbackQuery
	if cb == nil || cb.From == nil {
		return false
	}
	payload, ok := parseTelegramDecisionCallback(cb.Data)
	if !ok {
		return false
	}
	tgID := strconv.FormatInt(cb.From.ID, 10)
	lang := botLang(cb.From.LanguageCode)

	// THE DM'S OWN CHAT ONLY. The keyboard went to one person's private chat;
	// a forwarded message must not be able to decide anything.
	if cb.Message == nil || cb.Message.Chat == nil ||
		cb.Message.Chat.Type != "private" || cb.Message.Chat.ID != cb.From.ID {
		slog.Info("telegram decision: tap outside the recipient's own DM", "from", cb.From.ID)
		return true
	}

	// THE HUMAN, resolved from the binding and never from the payload.
	userID, err := h.userIDByExternalIdentity(ctx, providerTelegram, tgID)
	if err != nil || strings.TrimSpace(userID) == "" {
		h.botSendOpen(ctx, tgID, botT(lang, "notlinked"), botT(lang, "open.btn"))
		return true
	}

	switch payload.Action {
	case telegramDecisionEscalation:
		h.telegramAnswerEscalation(ctx, tgID, lang, userID, payload, cb.Message.Chat.ID, cb.Message.MessageID)
	case telegramDecisionReview:
		h.telegramReviewDecision(ctx, tgID, lang, userID, payload, cb.Message.Chat.ID, cb.Message.MessageID)
	}
	return true
}

// telegramAnswerEscalation resolves the tapped option from the STORED row and
// posts it through the real resolve endpoint as the bound member.
func (h *Handler) telegramAnswerEscalation(ctx context.Context, tgID, lang, userID string, payload telegramDecisionCallback, chatID, messageID int64) {
	escUUID, err := util.ParseUUID(payload.ID)
	if err != nil {
		return
	}
	// Tenancy discovery: the callback carries only the escalation id (64 bytes
	// of payload leaves no room for a workspace UUID). Every check after this
	// line is scoped to the workspace this row names.
	esc, err := h.Queries.GetTaskEscalationByID(ctx, escUUID)
	if err != nil {
		h.botSend(ctx, tgID, decisionT(lang, "decision.gone"))
		return
	}
	workspaceID := uuidToString(esc.WorkspaceID)
	if !h.telegramActorIsMember(ctx, userID, workspaceID) {
		h.botSendOpen(ctx, tgID, decisionT(lang, "decision.noaccess"), botT(lang, "open.btn"))
		return
	}

	index, err := strconv.Atoi(payload.Arg)
	if err != nil || index < 0 || index >= len(esc.Options) {
		return
	}
	// The LABEL is read from what was stored, never from the callback payload:
	// the payload is attacker-controllable and would otherwise let someone
	// answer with text that was never offered.
	answer := esc.Options[index]

	status, body := h.invokeAsMember(ctx, userID, workspaceID, h.ResolveEscalation,
		http.MethodPost, "/api/escalations/"+payload.ID+"/resolve",
		map[string]string{"escalationId": payload.ID},
		map[string]string{"answer": answer})

	switch {
	case status == http.StatusOK:
		h.replaceDecisionKeyboard(ctx, chatID, messageID, decisionT(lang, "decision.answered")+"\n\n<b>"+html.EscapeString(answer)+"</b>")
	case status == http.StatusConflict:
		h.replaceDecisionKeyboard(ctx, chatID, messageID, decisionT(lang, "decision.already"))
	default:
		slog.Warn("telegram decision: escalation answer refused",
			"status", status, "escalation_id", payload.ID, "body", truncateForLog(body))
		h.botSendOpen(ctx, tgID, decisionT(lang, "decision.failed"), botT(lang, "open.btn"))
	}
}

// telegramReviewDecision runs Approve, or opens the note window for Request
// changes. Both end in CreateReviewDecision — the same endpoint the web button
// posts to, with the same RequireHumanActor semantics (this request carries no
// machine actor source, because a person tapped it).
func (h *Handler) telegramReviewDecision(ctx context.Context, tgID, lang, userID string, payload telegramDecisionCallback, chatID, messageID int64) {
	issueUUID, err := util.ParseUUID(payload.ID)
	if err != nil {
		return
	}
	issue, err := h.Queries.GetIssue(ctx, issueUUID)
	if err != nil {
		h.botSend(ctx, tgID, decisionT(lang, "decision.gone"))
		return
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	if !h.telegramActorIsMember(ctx, userID, workspaceID) {
		h.botSendOpen(ctx, tgID, decisionT(lang, "decision.noaccess"), botT(lang, "open.btn"))
		return
	}
	identifier := h.issueKey(ctx, issue)

	if payload.Arg == telegramReviewRequestChange {
		// request_changes without a note is a 400 by design — the note IS the
		// correction worker's instruction. So this cannot be one tap: open a
		// short window and take the next message as the note.
		setPendingDecisionNote(tgID, pendingDecisionNote{
			IssueID:     uuidToString(issue.ID),
			WorkspaceID: workspaceID,
			UserID:      userID,
			Identifier:  identifier,
		})
		h.botSend(ctx, tgID, decisionT(lang, "decision.notePrompt"))
		return
	}

	status, body := h.invokeAsMember(ctx, userID, workspaceID, h.CreateReviewDecision,
		http.MethodPost, "/api/issues/"+uuidToString(issue.ID)+"/review-decision",
		map[string]string{"id": uuidToString(issue.ID)},
		map[string]string{"action": "approve"})

	switch {
	case status == http.StatusOK:
		h.replaceDecisionKeyboard(ctx, chatID, messageID, decisionT(lang, "decision.approved")+" <b>"+html.EscapeString(identifier)+"</b>")
	case status == http.StatusConflict:
		// The gate moved under the tap (already approved, or the release step is
		// not waiting). The handler's own message is the accurate one.
		h.replaceDecisionKeyboard(ctx, chatID, messageID, decisionT(lang, "decision.already"))
	default:
		slog.Warn("telegram decision: approve refused",
			"status", status, "issue_id", uuidToString(issue.ID), "body", truncateForLog(body))
		h.botSendOpen(ctx, tgID, decisionT(lang, "decision.failed"), botT(lang, "open.btn"))
	}
}

// maybeCaptureDecisionNote consumes a plain DM as the pending "Request changes"
// note. Returns true when it owned the message, so the create wizard does not
// also turn the note into a new task.
func (h *Handler) maybeCaptureDecisionNote(ctx context.Context, tgID, lang, text string) bool {
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		return false // a command cancels the window rather than becoming a note
	}
	pending, ok := takePendingDecisionNote(tgID)
	if !ok {
		return false
	}
	note := strings.TrimSpace(text)
	if note == "" {
		return false
	}
	status, body := h.invokeAsMember(ctx, pending.UserID, pending.WorkspaceID, h.CreateReviewDecision,
		http.MethodPost, "/api/issues/"+pending.IssueID+"/review-decision",
		map[string]string{"id": pending.IssueID},
		map[string]string{"action": "request_changes", "note": note})
	if status == http.StatusOK {
		h.botSend(ctx, tgID, decisionT(lang, "decision.changesSent")+" <b>"+html.EscapeString(pending.Identifier)+"</b>")
		return true
	}
	slog.Warn("telegram decision: request_changes refused",
		"status", status, "issue_id", pending.IssueID, "body", truncateForLog(body))
	h.botSendOpen(ctx, tgID, decisionT(lang, "decision.failed"), botT(lang, "open.btn"))
	return true
}

// replaceDecisionKeyboard swaps the buttons for the outcome, so nobody taps a
// decision that is already settled and the chat records what was decided.
func (h *Handler) replaceDecisionKeyboard(ctx context.Context, chatID, messageID int64, text string) {
	if h.telegramBot == nil || chatID == 0 || messageID == 0 {
		return
	}
	if err := h.telegramBot.EditButtons(ctx, strconv.FormatInt(chatID, 10), messageID, text, nil); err != nil {
		slog.Warn("telegram decision: edit keyboard failed", "error", err)
	}
}

// telegramActorIsMember is the membership check the router's workspace
// middleware would have applied. The synthetic request below bypasses that
// middleware, so the check happens here instead of nowhere.
func (h *Handler) telegramActorIsMember(ctx context.Context, userID, workspaceID string) bool {
	uid, err := util.ParseUUID(userID)
	if err != nil {
		return false
	}
	wsID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return false
	}
	_, err = h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      uid,
		WorkspaceID: wsID,
	})
	return err == nil
}

// ── the in-process invoker ──────────────────────────────────────────────────

// invokeAsMember runs an HTTP handler in process, as a named human member of a
// named workspace, and returns what it wrote.
//
// This is deliberately NOT a re-implementation of the endpoints. A Telegram tap
// and a web click must do the same thing, and the only way to guarantee that is
// for them to run the same function. The synthetic request carries exactly what
// the router's middleware would have stamped — the user, the workspace, the chi
// URL params — and NOTHING ELSE. In particular it carries no X-Actor-Source, so
// IsMachineActor is false and the RequireHumanActor contract holds: a person
// tapped this button, and the caller has already proven which person by
// resolving the Telegram binding and re-checking membership.
func (h *Handler) invokeAsMember(
	ctx context.Context,
	userID, workspaceID string,
	handler http.HandlerFunc,
	method, target string,
	urlParams map[string]string,
	body any,
) (int, []byte) {
	var reader io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return http.StatusInternalServerError, nil
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", workspaceID)

	routeCtx := chi.NewRouteContext()
	for k, v := range urlParams {
		routeCtx.URLParams.Add(k, v)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rec := &capturedResponse{status: http.StatusOK}
	handler(rec, req)
	return rec.status, rec.body.Bytes()
}

// capturedResponse is a minimal http.ResponseWriter that keeps what a handler
// wrote. Deliberately not httptest.NewRecorder: this is production code.
type capturedResponse struct {
	status  int
	headers http.Header
	body    bytes.Buffer
}

func (c *capturedResponse) Header() http.Header {
	if c.headers == nil {
		c.headers = http.Header{}
	}
	return c.headers
}

func (c *capturedResponse) Write(b []byte) (int, error) { return c.body.Write(b) }

func (c *capturedResponse) WriteHeader(status int) { c.status = status }

// truncateForLog keeps a refused handler's body readable in a log line without
// pasting a whole JSON document into it.
func truncateForLog(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// ── i18n ────────────────────────────────────────────────────────────────────

// decisionStrings mirrors botStrings' shape (ru default for this fork). Kept
// beside the flow rather than merged into botStrings so a decision button's
// wording can be changed without touching the create wizard's copy.
var decisionStrings = map[string]map[string]string{
	"en": {
		"approve":              "✅ Approve",
		"changes":              "✍️ Request changes",
		"decision.answered":    "Answered.",
		"decision.approved":    "✅ Approved",
		"decision.already":     "Already decided — someone got there first.",
		"decision.failed":      "Couldn’t apply that from here. Open Agora to finish it.",
		"decision.gone":        "That decision is no longer available.",
		"decision.noaccess":    "You’re not a member of that workspace.",
		"decision.notePrompt":  "What needs to change? Send it as your next message.",
		"decision.changesSent": "✍️ Changes requested on",
	},
	"ru": {
		"approve":              "✅ Одобрить",
		"changes":              "✍️ Вернуть на доработку",
		"decision.answered":    "Ответ записан.",
		"decision.approved":    "✅ Одобрено",
		"decision.already":     "Решение уже принято — кто-то успел раньше.",
		"decision.failed":      "Не удалось выполнить отсюда. Откройте Agora, чтобы завершить.",
		"decision.gone":        "Это решение больше недоступно.",
		"decision.noaccess":    "Вы не состоите в этом пространстве.",
		"decision.notePrompt":  "Что нужно изменить? Отправьте следующим сообщением.",
		"decision.changesSent": "✍️ Доработка запрошена:",
	},
	"uz": {
		"approve":              "✅ Tasdiqlash",
		"changes":              "✍️ Qayta ishlashga qaytarish",
		"decision.answered":    "Javob yozildi.",
		"decision.approved":    "✅ Tasdiqlandi",
		"decision.already":     "Qaror allaqachon qabul qilingan.",
		"decision.failed":      "Bu yerdan bajarib bo‘lmadi. Agora’ni oching.",
		"decision.gone":        "Bu qaror endi mavjud emas.",
		"decision.noaccess":    "Siz bu ish maydoni a’zosi emassiz.",
		"decision.notePrompt":  "Nimani o‘zgartirish kerak? Keyingi xabarda yuboring.",
		"decision.changesSent": "✍️ Qayta ishlash so‘raldi:",
	},
}

func decisionBtn(lang, key string) string { return decisionT(lang, key) }

func decisionT(lang, key string) string {
	if m := decisionStrings[lang]; m != nil {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if v, ok := decisionStrings["ru"][key]; ok {
		return v
	}
	return key
}
