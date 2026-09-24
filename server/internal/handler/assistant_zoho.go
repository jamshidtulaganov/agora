package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	"github.com/jamshidtulaganov/agora/server/internal/zohoread"
)

// Zoho in the Assistant: the chatting person's own Zoho CRM and Desk, read
// with their own grant (zoho_account.go), through the same read-only tools
// agents use (zohoread). Nothing here can write to Zoho.

// assistantIntegrations is the Service.Integrations hook. A person with a
// connected Zoho gets the Zoho tools and a note saying whose Zoho it is; a
// person without one gets only a note on how to connect (when this server
// offers Zoho at all).
func (h *Handler) assistantIntegrations(ctx context.Context, userID string) ([]llm.Tool, string) {
	uid, err := util.ParseUUID(userID)
	if err != nil {
		return nil, ""
	}
	if clients, ok := h.zohoClientsForUser(ctx, uid); ok {
		return assistant.ZohoToolSpecs(), zohoAssistantNote(clients.Me)
	}
	if _, available := h.zohoOAuth(); available {
		return nil, "ZOHO: this person hasn't connected Zoho. If they ask about Zoho tickets, deals or other " +
			"Zoho data, tell them to connect their own Zoho account in Settings → Profile → Connected accounts; " +
			"you then read Zoho as them, with their own Zoho permissions, and never change anything in Zoho."
	}
	return nil, ""
}

func zohoAssistantNote(me zohoread.Identity) string {
	var who []string
	if me.Email != "" {
		who = append(who, me.Email)
	}
	if me.CRMRole != "" {
		role := "CRM role " + me.CRMRole
		if me.CRMProfile != "" {
			role += " (" + me.CRMProfile + " profile)"
		}
		who = append(who, role)
	}
	if len(me.DeskDepartments) > 0 {
		who = append(who, "Desk departments "+strings.Join(me.DeskDepartments, ", "))
	}
	var b strings.Builder
	b.WriteString("ZOHO (read-only): the zoho_* tools read this person's own Zoho CRM and Zoho Desk")
	if len(who) > 0 {
		b.WriteString(" as " + strings.Join(who, "; "))
	}
	b.WriteString(". Zoho only returns what they are allowed to see — never suggest you can see more, and never ")
	b.WriteString("present a list as complete when more_records is true. You cannot create, change or delete anything ")
	b.WriteString("in Zoho: if asked to, say so plainly and offer what they could do in Zoho themselves. Numbers and lists ")
	b.WriteString("about Zoho come only from tool results in this conversation. Before a COQL search, learn the api names ")
	b.WriteString("with zoho_crm_modules / zoho_crm_fields. Quote ticket numbers (#4821) and link records with the url ")
	b.WriteString("a tool returns. Before putting Zoho data into anything other people can see (an issue, a comment, a ")
	b.WriteString("shared report), tell the person that everyone who can see it will see that data.")
	return b.String()
}

// assistantZohoTool runs one Zoho read as the chatting person.
func (h *Handler) assistantZohoTool(ctx context.Context, caller assistantCaller, name string, raw json.RawMessage) (json.RawMessage, error) {
	clients, ok := h.zohoClientsForUser(ctx, caller.UUID)
	if !ok {
		return nil, errors.New("this person hasn't connected Zoho (or it needs reconnecting): they can do it in Settings → Profile → Connected accounts")
	}
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errors.New("arguments must be a JSON object")
		}
	}
	call := zohoCall{UserID: caller.UUID, Source: "assistant"}
	if focus := assistant.FocusWorkspaceFrom(ctx); focus != "" {
		call.WorkspaceID, _ = util.ParseUUID(focus)
	}
	res, err := h.runZohoTool(ctx, call, clients, name, args)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res.Value)
}
