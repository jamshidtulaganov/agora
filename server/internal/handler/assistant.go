package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Agora Assistant HTTP surface. These routes are USER-scoped: they carry no
// X-Workspace-ID, because one assistant conversation spans every workspace the
// person belongs to. Workspace scoping happens per tool call, in the executor.

const (
	// assistantMessageMaxLen caps one user turn. Generous enough to paste a
	// stack trace, small enough that a single message cannot exhaust the
	// model's context on its own.
	assistantMessageMaxLen = 8000
	// assistantSessionTitleMaxLen caps a rename.
	assistantSessionTitleMaxLen = 200
	// assistantSessionListLimit is how many sessions the switcher shows.
	assistantSessionListLimit = 50
)

// anthropicAPIKey reads the instance Anthropic key. Empty => the anthropic
// provider is unavailable and availability reports disabled.
func anthropicAPIKey() string {
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
}

// openaiAPIKey reads the instance OpenAI key. Empty => the openai provider is
// unavailable and availability reports disabled.
func openaiAPIKey() string {
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

// assistantProvider is the configured provider, normalized. Anything
// unrecognized falls back to zhipu (the free branded model) rather than
// disabling the feature on a typo.
func assistantProvider() string {
	switch strings.ToLower(config.String("AGORA_ASSISTANT_PROVIDER")) {
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	}
	return "zhipu"
}

// assistantModel resolves the model id for a provider.
//
// AGORA_ASSISTANT_MODEL defaults to the free Zhipu model, which is meaningless
// to Anthropic — so an operator who flips only the provider gets Anthropic's
// own default rather than a 404 from the provider.
func assistantModel(provider string) string {
	model := config.String("AGORA_ASSISTANT_MODEL")
	if provider == "anthropic" && (model == "" || model == llm.FreeModel) {
		return llm.DefaultAnthropicModel
	}
	if provider == "openai" && (model == "" || model == llm.FreeModel) {
		return llm.DefaultOpenAIModel
	}
	if provider == "zhipu" && model == "" {
		return llm.FreeModel
	}
	return model
}

// assistantProviderKey returns the API key backing a provider.
func assistantProviderKey(provider string) string {
	switch provider {
	case "anthropic":
		return anthropicAPIKey()
	case "openai":
		return openaiAPIKey()
	}
	return zhipuAPIKey()
}

// AssistantClient resolves the provider client for one run. Handed to the
// service as its ClientFactory so a config change takes effect on the next
// message without a restart.
func (h *Handler) AssistantClient() (llm.ToolChat, string, error) {
	provider := assistantProvider()
	key := assistantProviderKey(provider)
	if key == "" {
		return nil, "", llm.ErrNotConfigured
	}
	model := assistantModel(provider)
	switch provider {
	case "anthropic":
		return llm.NewAnthropicClient(key), model, nil
	case "openai":
		return llm.NewOpenAIClient(key), model, nil
	}
	return llm.NewZhipuClient(key), model, nil
}

// assistantEnabled reports whether the feature is both switched on and backed
// by a usable key.
func assistantEnabled() bool {
	return config.Bool("AGORA_ASSISTANT_ENABLED") && assistantProviderKey(assistantProvider()) != ""
}

// assistantModelLabel is the human name shown in the UI ("answered by …").
func assistantModelLabel() string {
	provider := assistantProvider()
	model := assistantModel(provider)
	name := "Agora"
	switch provider {
	case "anthropic":
		name = "Claude"
	case "openai":
		name = "GPT"
	}
	if model == "" {
		return name
	}
	return name + " (" + model + ")"
}

// ---------------------------------------------------------------------------
// Response shapes
// ---------------------------------------------------------------------------

type AssistantSessionResponse struct {
	ID               string               `json:"id"`
	Title            string               `json:"title"`
	FocusWorkspaceID *string              `json:"focus_workspace_id"`
	CreatedAt        string               `json:"created_at"`
	UpdatedAt        string               `json:"updated_at"`
	LatestRun        *assistant.RunRecord `json:"latest_run,omitempty"`
}

type AssistantToolCallResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type AssistantMessageResponse struct {
	ID         string                      `json:"id"`
	SessionID  string                      `json:"session_id"`
	Role       string                      `json:"role"`
	Content    string                      `json:"content"`
	ToolCalls  []AssistantToolCallResponse `json:"tool_calls,omitempty"`
	ToolCallID *string                     `json:"tool_call_id,omitempty"`
	ToolName   *string                     `json:"tool_name,omitempty"`
	ToolResult json.RawMessage             `json:"tool_result,omitempty"`
	CreatedAt  string                      `json:"created_at"`
}

func assistantSessionToResponse(s db.AssistantSession) AssistantSessionResponse {
	return AssistantSessionResponse{
		ID:               uuidToString(s.ID),
		Title:            s.Title,
		FocusWorkspaceID: uuidToPtr(s.FocusWorkspaceID),
		CreatedAt:        timestampToString(s.CreatedAt),
		UpdatedAt:        timestampToString(s.UpdatedAt),
	}
}

func assistantMessageToResponse(m db.AssistantMessage) AssistantMessageResponse {
	resp := AssistantMessageResponse{
		ID:         uuidToString(m.ID),
		SessionID:  uuidToString(m.SessionID),
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: textToPtr(m.ToolCallID),
		ToolName:   textToPtr(m.ToolName),
		CreatedAt:  timestampToString(m.CreatedAt),
	}
	if len(m.ToolCalls) > 0 {
		var calls []AssistantToolCallResponse
		// A blob that no longer parses renders as a message with no chips
		// rather than failing the whole transcript read.
		if err := json.Unmarshal(m.ToolCalls, &calls); err == nil {
			resp.ToolCalls = calls
		}
	}
	if len(m.ToolResult) > 0 {
		resp.ToolResult = json.RawMessage(m.ToolResult)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Ownership
// ---------------------------------------------------------------------------

// loadAssistantSessionForUser resolves a session id and proves the caller owns
// it. A session belonging to someone else is reported as NOT FOUND, never
// forbidden: the sessions are user-private, so acknowledging one exists would
// already be a leak.
func (h *Handler) loadAssistantSessionForUser(w http.ResponseWriter, r *http.Request, userID string) (db.AssistantSession, bool) {
	sessionUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "assistant session id")
	if !ok {
		return db.AssistantSession{}, false
	}
	session, err := h.Queries.GetAssistantSession(r.Context(), sessionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "assistant session not found")
		return db.AssistantSession{}, false
	}
	if uuidToString(session.UserID) != userID {
		writeError(w, http.StatusNotFound, "assistant session not found")
		return db.AssistantSession{}, false
	}
	return session, true
}

// assistantFocusWorkspace validates an optional focus workspace: it must be one
// the caller is actually a member of, so the prompt's "currently open
// workspace" can never point somewhere they cannot see.
func (h *Handler) assistantFocusWorkspace(w http.ResponseWriter, r *http.Request, userID, workspaceID string) (pgtype.UUID, bool) {
	if strings.TrimSpace(workspaceID) == "" {
		return pgtype.UUID{}, true
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "focus_workspace_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusForbidden, "you are not a member of that workspace")
		return pgtype.UUID{}, false
	}
	return wsUUID, true
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

type CreateAssistantSessionRequest struct {
	Title            string `json:"title"`
	FocusWorkspaceID string `json:"focus_workspace_id"`
}

func (h *Handler) CreateAssistantSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreateAssistantSessionRequest
	// An empty body is a valid "just give me a session" — but malformed JSON is
	// still a client bug and must not be silently swallowed into a default.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if len(title) > assistantSessionTitleMaxLen {
		writeError(w, http.StatusBadRequest, "title is too long")
		return
	}
	focus, ok := h.assistantFocusWorkspace(w, r, userID, req.FocusWorkspaceID)
	if !ok {
		return
	}

	session, err := h.Queries.CreateAssistantSession(r.Context(), db.CreateAssistantSessionParams{
		UserID:           parseUUID(userID),
		Title:            title,
		FocusWorkspaceID: focus,
	})
	if err != nil {
		slog.Warn("assistant: create session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create assistant session")
		return
	}
	writeJSON(w, http.StatusCreated, assistantSessionToResponse(session))
}

func (h *Handler) ListAssistantSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	sessions, err := h.Queries.ListAssistantSessions(r.Context(), db.ListAssistantSessionsParams{
		UserID: parseUUID(userID),
		Limit:  assistantSessionListLimit,
	})
	if err != nil {
		slog.Warn("assistant: list sessions failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list assistant sessions")
		return
	}
	resp := make([]AssistantSessionResponse, 0, len(sessions))
	for _, s := range sessions {
		item := assistantSessionToResponse(s)
		if h.Assistant != nil {
			latest, err := h.Assistant.LatestRun(r.Context(), item.ID, userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load assistant run status")
				return
			}
			item.LatestRun = latest
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetAssistantSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}
	resp := assistantSessionToResponse(session)
	if h.Assistant != nil {
		latest, err := h.Assistant.LatestRun(r.Context(), resp.ID, userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load assistant run status")
			return
		}
		resp.LatestRun = latest
	}
	writeJSON(w, http.StatusOK, resp)
}

type PatchAssistantSessionRequest struct {
	Title *string `json:"title"`
	// Raw, not *string, because THREE states have to be distinguishable and a
	// pointer only carries two: the field absent (leave the focus alone), the
	// field set to JSON null (CLEAR it — the user removed the context chip),
	// and a workspace id (pin it). Decoded into *string, null and absent are
	// the same nil and removing the chip is a silent no-op that reappears on
	// the next refetch. Same shape as SendAssistantMessageRequest.Context.
	FocusWorkspaceID json.RawMessage `json:"focus_workspace_id"`
}

// assistantClearFocus is the wire value UpdateAssistantSession reads as "set
// focus_workspace_id back to NULL" — the empty-string sentinel the repo already
// uses for "user".timezone. It must be an explicitly Valid empty string:
// strToText("") is a NULL parameter, which the query reads as "leave it alone".
var assistantClearFocus = pgtype.Text{String: "", Valid: true}

func (h *Handler) PatchAssistantSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}

	var req PatchAssistantSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := db.UpdateAssistantSessionParams{ID: session.ID}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if len(title) > assistantSessionTitleMaxLen {
			writeError(w, http.StatusBadRequest, "title is too long")
			return
		}
		// Explicitly Valid, not strToText: an empty title must actually CLEAR
		// the title (strToText("") is NULL, which COALESCE reads as "leave it
		// alone" — a silent no-op). A cleared title is then re-derived from the
		// next message, which is the only sane meaning of "reset the name".
		params.Title = pgtype.Text{String: title, Valid: true}
	}
	if req.FocusWorkspaceID != nil {
		if bytes.Equal(bytes.TrimSpace(req.FocusWorkspaceID), []byte("null")) {
			params.FocusWorkspaceID = assistantClearFocus
		} else {
			var workspaceID string
			if err := json.Unmarshal(req.FocusWorkspaceID, &workspaceID); err != nil {
				writeError(w, http.StatusBadRequest, "invalid focus_workspace_id")
				return
			}
			// An empty string means the same thing as null on this field, and
			// arrives from clients that clear an input rather than delete a key.
			if strings.TrimSpace(workspaceID) == "" {
				params.FocusWorkspaceID = assistantClearFocus
			} else {
				focus, ok := h.assistantFocusWorkspace(w, r, userID, workspaceID)
				if !ok {
					return
				}
				params.FocusWorkspaceID = pgtype.Text{String: uuidToString(focus), Valid: true}
			}
		}
	}

	updated, err := h.Queries.UpdateAssistantSession(r.Context(), params)
	if err != nil {
		slog.Warn("assistant: update session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update assistant session")
		return
	}
	writeJSON(w, http.StatusOK, assistantSessionToResponse(updated))
}

func (h *Handler) DeleteAssistantSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}

	if h.Assistant != nil {
		latest, err := h.Assistant.LatestRun(r.Context(), uuidToString(session.ID), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load assistant run status")
			return
		}
		if latest != nil && (latest.Status == "queued" || latest.Status == "running") {
			_, _ = h.Assistant.RequestCancel(r.Context(), latest.ID, userID)
			writeError(w, http.StatusConflict, "the assistant is still stopping; try deleting the session again shortly")
			return
		}
	}

	if err := h.Queries.DeleteAssistantSession(r.Context(), db.DeleteAssistantSessionParams{
		ID:     session.ID,
		UserID: parseUUID(userID),
	}); err != nil {
		slog.Warn("assistant: delete session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete assistant session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

type SendAssistantMessageRequest struct {
	Content   string `json:"content"`
	RequestID string `json:"request_id"`
	Context   *struct {
		WorkspaceID   json.RawMessage `json:"workspace_id"`
		Timezone      string          `json:"timezone"`
		ProjectID     json.RawMessage `json:"project_id"`
		AttachmentIDs []string        `json:"attachment_ids"`
	} `json:"context"`
}

type SendAssistantMessageResponse struct {
	MessageID string `json:"message_id"`
	RunID     string `json:"run_id"`
	CreatedAt string `json:"created_at"`
}

// SendAssistantMessage persists the user's turn and starts the reply loop,
// returning 202 immediately. The reply itself arrives over the websocket
// (assistant:message / assistant:tool_activity / assistant:run_finished);
// GET messages is the polling fallback.
func (h *Handler) SendAssistantMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}
	if h.Assistant == nil || !assistantEnabled() {
		writeError(w, http.StatusServiceUnavailable, "the Agora Assistant is not available on this instance")
		return
	}

	var req SendAssistantMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if len(content) > assistantMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long (max "+strconv.Itoa(assistantMessageMaxLen)+" characters)")
		return
	}
	if req.Context != nil && req.Context.WorkspaceID == nil {
		writeError(w, http.StatusBadRequest, "context.workspace_id is required (use null for no workspace)")
		return
	}
	var requestID *string
	if req.RequestID != "" {
		if _, err := uuid.Parse(req.RequestID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request_id")
			return
		}
		requestID = &req.RequestID
	}
	requestContext := ""
	if req.Context != nil {
		raw, _ := json.Marshal(req.Context)
		requestContext = string(raw)
	}
	if requestID != nil {
		previous, err := h.Assistant.FindRequest(r.Context(), uuidToString(session.ID), *requestID, content, requestContext)
		if errors.Is(err, assistant.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "request_id was already used with different content or context")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check request_id")
			return
		}
		if previous != nil {
			writeJSON(w, http.StatusAccepted, SendAssistantMessageResponse{MessageID: uuidToString(previous.Message.ID), RunID: previous.Run.ID, CreatedAt: timestampToString(previous.Message.CreatedAt)})
			return
		}
	}
	runContext := assistant.RunContext{WorkspaceID: uuidToPtr(session.FocusWorkspaceID)}
	if req.Context != nil {
		if req.Context.WorkspaceID != nil {
			if bytes.Equal(bytes.TrimSpace(req.Context.WorkspaceID), []byte("null")) {
				runContext.WorkspaceID = nil
			} else {
				var workspaceID string
				if err := json.Unmarshal(req.Context.WorkspaceID, &workspaceID); err != nil {
					writeError(w, http.StatusBadRequest, "invalid workspace_id")
					return
				}
				focus, valid := h.assistantFocusWorkspace(w, r, userID, workspaceID)
				if !valid {
					return
				}
				runContext.WorkspaceID = uuidToPtr(focus)
			}
		}
		if req.Context.Timezone != "" {
			if _, err := time.LoadLocation(req.Context.Timezone); err != nil {
				writeError(w, http.StatusBadRequest, "invalid timezone")
				return
			}
			runContext.Timezone = req.Context.Timezone
		}
		if req.Context.ProjectID != nil && !bytes.Equal(bytes.TrimSpace(req.Context.ProjectID), []byte("null")) {
			var projectID string
			if err := json.Unmarshal(req.Context.ProjectID, &projectID); err != nil {
				writeError(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			if _, err := uuid.Parse(projectID); err != nil {
				writeError(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			runContext.ProjectID = &projectID
		}
		runContext.AttachmentIDs = req.Context.AttachmentIDs
	}
	if runContext.WorkspaceID != nil {
		if _, valid := h.assistantFocusWorkspace(w, r, userID, *runContext.WorkspaceID); !valid {
			return
		}
	}
	snapshot, valid := h.prepareAssistantContext(w, r, userID, runContext)
	if !valid {
		return
	}

	sessionID := uuidToString(session.ID)

	accepted, err := h.Assistant.AcceptRun(r.Context(), session, userID, content, requestID, requestContext, runContext, snapshot)
	if errors.Is(err, assistant.ErrRunInProgress) {
		writeError(w, http.StatusConflict, "the assistant is still answering your previous message")
		return
	}
	if errors.Is(err, assistant.ErrRequestConflict) {
		writeError(w, http.StatusConflict, "request_id was already used with different content or context")
		return
	}
	if err != nil {
		slog.Warn("assistant: start run failed", "session_id", sessionID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send message")
		return
	}

	// First message names the session, so the switcher is never a list of
	// "Untitled".
	if session.Title == "" && !accepted.Duplicate {
		if _, err := h.Queries.UpdateAssistantSession(r.Context(), db.UpdateAssistantSessionParams{
			ID:    session.ID,
			Title: strToText(assistant.DeriveSessionTitle(content)),
		}); err != nil {
			slog.Warn("assistant: auto-title failed", "session_id", sessionID, "error", err)
		}
	}

	messageID := uuidToString(accepted.Message.ID)
	createdAt := timestampToString(accepted.Message.CreatedAt)
	runID := accepted.Run.ID

	// Echo the user's own turn so other devices on the same account see it.
	if h.Bus != nil && !accepted.Duplicate {
		h.Bus.Publish(events.Event{
			Type:      protocol.EventAssistantMessage,
			ActorType: "member",
			ActorID:   userID,
			Payload: protocol.AssistantMessagePayload{
				UserID:    userID,
				SessionID: sessionID,
				RunID:     runID,
				MessageID: messageID,
				Role:      "user",
				Content:   content,
				CreatedAt: createdAt,
			},
		})
	}

	writeJSON(w, http.StatusAccepted, SendAssistantMessageResponse{
		MessageID: messageID,
		RunID:     runID,
		CreatedAt: createdAt,
	})
}

func (h *Handler) ListAssistantMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}
	messages, err := h.Queries.ListAssistantMessages(r.Context(), session.ID)
	if err != nil {
		slog.Warn("assistant: list messages failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list assistant messages")
		return
	}
	resp := make([]AssistantMessageResponse, 0, len(messages))
	for _, m := range messages {
		resp = append(resp, assistantMessageToResponse(m))
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Runs + availability
// ---------------------------------------------------------------------------

// CancelAssistantRun stops a run the caller owns. An unknown, finished, or
// someone else's run is a 404 — the run registry must not be probeable.
func (h *Handler) CancelAssistantRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	runID := strings.TrimSpace(chi.URLParam(r, "id"))
	if _, ok := parseUUIDOrBadRequest(w, runID, "assistant run id"); !ok {
		return
	}
	if h.Assistant == nil {
		writeError(w, http.StatusServiceUnavailable, "assistant unavailable")
		return
	}
	cancelled, err := h.Assistant.RequestCancel(r.Context(), runID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel assistant run")
		return
	}
	if !cancelled {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListAssistantRuns(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.loadAssistantSessionForUser(w, r, userID)
	if !ok {
		return
	}
	if h.Assistant == nil {
		writeError(w, http.StatusServiceUnavailable, "assistant unavailable")
		return
	}
	runs, err := h.Assistant.ListRuns(r.Context(), uuidToString(session.ID), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load assistant runs")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) GetAssistantRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	runID := chi.URLParam(r, "id")
	if _, ok := parseUUIDOrBadRequest(w, runID, "assistant run id"); !ok {
		return
	}
	if h.Assistant == nil {
		writeError(w, http.StatusServiceUnavailable, "assistant unavailable")
		return
	}
	run, err := h.Assistant.GetRun(r.Context(), runID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

type AssistantAvailabilityResponse struct {
	Enabled    bool   `json:"enabled"`
	ModelLabel string `json:"model_label"`
}

// GetAssistantAvailability is the UI gate: the nav item and the page hide
// entirely when the feature is off or unkeyed.
func (h *Handler) GetAssistantAvailability(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	enabled := h.Assistant != nil && assistantEnabled()
	resp := AssistantAvailabilityResponse{Enabled: enabled}
	if enabled {
		resp.ModelLabel = assistantModelLabel()
	}
	writeJSON(w, http.StatusOK, resp)
}
