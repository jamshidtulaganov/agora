package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The writes that closed the gap between what a person can do on a page and
// what they can ask the assistant to do: editing a sprint or an automation,
// running a squad, and keeping the knowledge base. Each
// one goes through the same HTTP handler the UI calls, as the requesting
// human, so permissions, validation and events stay defined in exactly one
// place — see assistantInvoke.

// --- sprints ---------------------------------------------------------------

type assistantUpdateSprintArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Sprint      string `json:"sprint"`
	Name        string `json:"name"`
	Goal        string `json:"goal"`
	Status      string `json:"status"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
}

func (h *Handler) assistantUpdateSprint(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateSprintArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	sprint, err := h.assistantResolveSprint(ctx, ws, args.Sprint)
	if err != nil {
		return nil, err
	}

	// UpdateSprint is a PUT: fields the caller omits would be blanked, so the
	// sprint's current values are the base and only what was asked changes.
	body := map[string]any{
		"name":   sprint.Name,
		"goal":   sprint.Goal,
		"status": sprint.Status,
	}
	if v := strings.TrimSpace(args.Name); v != "" {
		body["name"] = v
	}
	if v := strings.TrimSpace(args.Goal); v != "" {
		body["goal"] = v
	}
	if v := strings.TrimSpace(args.Status); v != "" {
		body["status"] = v
	}
	if v := strings.TrimSpace(args.StartDate); v != "" {
		body["start_date"] = v
	} else if sprint.StartDate.Valid {
		body["start_date"] = sprint.StartDate.Time.Format("2006-01-02")
	}
	if v := strings.TrimSpace(args.EndDate); v != "" {
		body["end_date"] = v
	} else if sprint.EndDate.Valid {
		body["end_date"] = sprint.EndDate.Time.Format("2006-01-02")
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}
	sprintID := uuidToString(sprint.ID)
	status, respBody := h.assistantInvoke(ctx, h.UpdateSprint, http.MethodPut, "/api/sprints/"+sprintID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": sprintID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the sprint")
	}
	var updated SprintResponse
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, errors.New("the sprint was updated but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"updated": true,
		"sprint": map[string]any{
			"id": updated.ID, "name": updated.Name, "status": updated.Status, "goal": updated.Goal,
		},
	})
}

// --- squads ----------------------------------------------------------------

// assistantResolveSquad turns a squad UUID or name into the squad.
func (h *Handler) assistantResolveSquad(ctx context.Context, ws db.Workspace, ref string) (db.Squad, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Squad{}, errors.New("squad is required (a name from list_squads, or a squad UUID)")
	}
	rows, err := h.Queries.ListSquads(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list squads failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Squad{}, errors.New("could not load squads")
	}
	byID, _ := util.ParseUUID(ref)
	needle := strings.ToLower(ref)
	var matches []db.Squad
	for _, s := range rows {
		if byID.Valid && s.ID == byID {
			return s, nil
		}
		if !byID.Valid && strings.ToLower(s.Name) == needle {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return db.Squad{}, fmt.Errorf("no squad called %q in that workspace — list_squads shows what exists", ref)
	default:
		names := make([]string, 0, len(matches))
		for _, s := range matches {
			names = append(names, s.Name+" ("+uuidToString(s.ID)+")")
		}
		return db.Squad{}, assistantAmbiguous("squad", ref, names)
	}
}

type assistantCreateSquadArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Leader      string `json:"leader"`
	Description string `json:"description"`
}

func (h *Handler) assistantCreateSquad(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateSquadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	// A squad cannot exist without a leader, and the leader must be an agent
	// in this workspace — CreateSquad rejects anything else.
	leader, err := h.assistantResolveAgent(ctx, caller, ws, args.Leader)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"name": name, "leader_id": uuidToString(leader.ID)}
	if d := strings.TrimSpace(args.Description); d != "" {
		body["description"] = d
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}
	status, respBody := h.assistantInvoke(ctx, h.CreateSquad, http.MethodPost, "/api/squads",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not create the squad")
	}
	var created map[string]any
	_ = json.Unmarshal(respBody, &created)
	return json.Marshal(map[string]any{"created": true, "squad": created})
}

type assistantUpdateSquadArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Squad       string `json:"squad"`
	Name        string `json:"name"`
	Leader      string `json:"leader"`
	Description string `json:"description"`
}

func (h *Handler) assistantUpdateSquad(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateSquadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	squad, err := h.assistantResolveSquad(ctx, ws, args.Squad)
	if err != nil {
		return nil, err
	}
	// UpdateSquad takes pointers, so only what was asked for is sent.
	body := map[string]any{}
	if v := strings.TrimSpace(args.Name); v != "" {
		body["name"] = v
	}
	if v := strings.TrimSpace(args.Description); v != "" {
		body["description"] = v
	}
	if v := strings.TrimSpace(args.Leader); v != "" {
		leader, lerr := h.assistantResolveAgent(ctx, caller, ws, v)
		if lerr != nil {
			return nil, lerr
		}
		body["leader_id"] = uuidToString(leader.ID)
	}
	if len(body) == 0 {
		return nil, errors.New("give a name, description or leader to change")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}
	squadID := uuidToString(squad.ID)
	status, respBody := h.assistantInvoke(ctx, h.UpdateSquad, http.MethodPut, "/api/squads/"+squadID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": squadID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the squad")
	}
	var updated map[string]any
	_ = json.Unmarshal(respBody, &updated)
	return json.Marshal(map[string]any{"updated": true, "squad": updated})
}

type assistantDeleteSquadArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Squad       string `json:"squad"`
}

func (h *Handler) assistantDeleteSquad(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantDeleteSquadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	squad, err := h.assistantResolveSquad(ctx, ws, args.Squad)
	if err != nil {
		return nil, err
	}
	squadID := uuidToString(squad.ID)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteSquad,
		Summary: "Permanently delete squad “" + squad.Name + "” in " + ws.Name +
			". Its membership and history go with it; the agents and people themselves are untouched. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "squad", Identifier: squadID, Title: squad.Name},
	})
	if out != nil || err != nil {
		return out, err
	}
	status, respBody := h.assistantInvoke(ctx, h.DeleteSquad, http.MethodDelete, "/api/squads/"+squadID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": squadID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the squad")
	}
	return json.Marshal(map[string]any{"deleted": true, "squad": squad.Name})
}

// --- automations -----------------------------------------------------------

type assistantUpdateAutomationArgs struct {
	WorkspaceID string          `json:"workspace_id"`
	Automation  string          `json:"automation"`
	Name        string          `json:"name"`
	Definition  json.RawMessage `json:"definition"`
}

func (h *Handler) assistantUpdateAutomation(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateAutomationArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	auto, err := h.assistantResolveAutomation(ctx, ws, args.Automation)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if v := strings.TrimSpace(args.Name); v != "" {
		body["name"] = v
	}
	if len(args.Definition) > 0 {
		body["definition"] = args.Definition
	}
	if len(body) == 0 {
		return nil, errors.New("give a name or a definition to change")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}
	autoID := uuidToString(auto.ID)
	status, respBody := h.assistantInvoke(ctx, h.UpdateAutomation, http.MethodPatch, "/api/automations/"+autoID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": autoID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the automation")
	}
	var updated map[string]any
	_ = json.Unmarshal(respBody, &updated)
	return json.Marshal(map[string]any{"updated": true, "automation": updated})
}

// --- knowledge -------------------------------------------------------------

type assistantCreateKnowledgeArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Pinned      *bool  `json:"pinned"`
}

func (h *Handler) assistantCreateKnowledge(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateKnowledgeArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	title, body := strings.TrimSpace(args.Title), strings.TrimSpace(args.Body)
	if title == "" || body == "" {
		return nil, errors.New("title and body are both required")
	}
	payload := map[string]any{"title": title, "body": body}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}
	status, respBody := h.assistantInvoke(ctx, h.CreateKnowledge, http.MethodPost, "/api/knowledge",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated && status != http.StatusOK && status != http.StatusAccepted {
		return nil, assistantHandlerError(status, respBody, "could not add the note")
	}
	var created knowledgeDocResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the note was added but its details could not be read back")
	}
	// Pinning is a separate field on the update endpoint, so a note asked for
	// pinned is created and then pinned, rather than silently unpinned.
	if args.Pinned != nil && *args.Pinned {
		pin, _ := json.Marshal(map[string]any{"pinned": true})
		if st, rb := h.assistantInvoke(ctx, h.UpdateKnowledge, http.MethodPatch, "/api/knowledge/"+created.ID,
			caller.ID, uuidToString(ws.ID), string(pin), map[string]string{"id": created.ID}); st != http.StatusOK {
			return nil, assistantHandlerError(st, rb, "the note was added but could not be pinned")
		}
		created.Pinned = true
	}
	return json.Marshal(map[string]any{
		"created": true,
		"document": map[string]any{
			"id": created.ID, "title": created.Title, "status": created.Status, "pinned": created.Pinned,
		},
	})
}

type assistantKnowledgeDocArgs struct {
	WorkspaceID string `json:"workspace_id"`
	DocID       string `json:"doc_id"`
	Title       string `json:"title"`
	Pinned      *bool  `json:"pinned"`
}

func (h *Handler) assistantUpdateKnowledge(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantKnowledgeDocArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	docID := strings.TrimSpace(args.DocID)
	if docID == "" {
		return nil, errors.New("doc_id is required (a UUID from list_knowledge or search_knowledge)")
	}
	body := map[string]any{}
	if v := strings.TrimSpace(args.Title); v != "" {
		body["title"] = v
	}
	if args.Pinned != nil {
		body["pinned"] = *args.Pinned
	}
	if len(body) == 0 {
		return nil, errors.New("give a title or pinned to change")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}
	status, respBody := h.assistantInvoke(ctx, h.UpdateKnowledge, http.MethodPatch, "/api/knowledge/"+docID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": docID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the document")
	}
	var updated knowledgeDocResponse
	_ = json.Unmarshal(respBody, &updated)
	return json.Marshal(map[string]any{
		"updated":  true,
		"document": map[string]any{"id": updated.ID, "title": updated.Title, "pinned": updated.Pinned},
	})
}

func (h *Handler) assistantReprocessKnowledge(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantKnowledgeDocArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	docID := strings.TrimSpace(args.DocID)
	if docID == "" {
		return nil, errors.New("doc_id is required (a UUID from list_knowledge)")
	}
	status, respBody := h.assistantInvoke(ctx, h.ReprocessKnowledge, http.MethodPost,
		"/api/knowledge/"+docID+"/reprocess", caller.ID, uuidToString(ws.ID), "",
		map[string]string{"id": docID})
	if status != http.StatusOK && status != http.StatusAccepted {
		return nil, assistantHandlerError(status, respBody, "could not reprocess the document")
	}
	return json.Marshal(map[string]any{"reprocessing": true, "doc_id": docID})
}

func (h *Handler) assistantDeleteKnowledge(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantKnowledgeDocArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	docID := strings.TrimSpace(args.DocID)
	if docID == "" {
		return nil, errors.New("doc_id is required (a UUID from list_knowledge)")
	}
	// The title makes the confirmation card readable: a UUID tells the person
	// nothing about what they are about to lose.
	title := docID
	if doc, err := h.Queries.GetKnowledgeDoc(ctx, db.GetKnowledgeDocParams{
		ID: parseUUID(docID), WorkspaceID: ws.ID,
	}); err == nil {
		title = doc.Title
	}
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteKnowledge,
		Summary: "Permanently delete “" + title + "” from the knowledge base in " + ws.Name +
			". Its sections go with it and the assistant and agents stop being able to cite it. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "knowledge", Identifier: docID, Title: title},
	})
	if out != nil || err != nil {
		return out, err
	}
	status, respBody := h.assistantInvoke(ctx, h.DeleteKnowledge, http.MethodDelete, "/api/knowledge/"+docID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": docID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the document")
	}
	return json.Marshal(map[string]any{"deleted": true, "document": title})
}
