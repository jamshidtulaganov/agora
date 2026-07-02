package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Phase 2 of the hybrid auto-instructions feature: the "tailor" action behind
// the squad-detail "✨ tailor" button. It takes the static default orchestrator
// brief (squad_orchestrator.go) and appends a roster section derived from the
// squad's ACTUAL members — each agent's name, role, and catalog description —
// plus routing guidance, so the leader's brief reflects who it can actually
// delegate to instead of a generic template.
//
// This is deterministic + instant (no LLM): the backend has no synchronous
// model client, and real per-squad LLM generation would need either a backend
// Anthropic key or an async agent-runtime round-trip. The roster the compose
// builds is exactly the context such an LLM upgrade would prompt on, so this
// logic is the shared core either way.
//
// NOTE: not yet wired to a route — SuggestSquadInstructions is registered in
// router.go (`POST .../squads/{id}/suggest-instructions`) and reached from the
// frontend via a new client method + a squad-detail button. Those three edits
// touch files another session is currently editing (router.go, client.ts), so
// they land once that work is committed and the tree is clean.

type squadRosterEntry struct {
	name        string
	role        string
	description string
	isLeader    bool
}

// composeTailoredOrchestratorInstructions builds a squad-specific orchestrator
// brief: the default policy, then a "Your squad" section naming the leader and
// each member (with its catalog description) plus concrete routing guidance.
func composeTailoredOrchestratorInstructions(squadName string, roster []squadRosterEntry) string {
	var b strings.Builder
	b.WriteString(defaultOrchestratorInstructions)

	b.WriteString("\n\n## Your squad")
	if strings.TrimSpace(squadName) != "" {
		b.WriteString(" — ")
		b.WriteString(strings.TrimSpace(squadName))
	}
	b.WriteString("\n")

	var leader *squadRosterEntry
	members := make([]squadRosterEntry, 0, len(roster))
	for i := range roster {
		if roster[i].isLeader {
			leader = &roster[i]
			continue
		}
		members = append(members, roster[i])
	}

	if leader != nil {
		b.WriteString(fmt.Sprintf("\nYou are the leader (%s).", strings.TrimSpace(leader.name)))
	}

	if len(members) == 0 {
		b.WriteString("\n\nThis squad has no members besides you yet. Decompose the issue anyway so the work is trackable and QA-separable, and create a subagent (agora agent create → agora squad member add) for any sub-task you should not do yourself.")
		return b.String()
	}

	b.WriteString("\n\nMembers you delegate to:\n")
	for _, m := range members {
		line := "- " + strings.TrimSpace(m.name)
		if r := strings.TrimSpace(m.role); r != "" && r != "member" {
			line += " (" + r + ")"
		}
		if d := strings.TrimSpace(m.description); d != "" {
			line += " — " + oneLine(d, 160)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\nRoute each sub-task to the member whose description best fits it. If a task fits no member, create a subagent for it, give it the right skills + a model that matches the task's difficulty, and archive it when done.")
	return b.String()
}

// oneLine collapses whitespace and truncates a description so a member line
// stays a single readable row.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = strings.TrimSpace(s[:max]) + "…"
	}
	return s
}

// buildSquadRoster loads the squad's members and resolves each agent's name +
// catalog description for the tailored brief. Best-effort per member.
func (h *Handler) buildSquadRoster(ctx context.Context, squad db.Squad) []squadRosterEntry {
	members, err := h.Queries.ListSquadMembers(ctx, squad.ID)
	if err != nil {
		return nil
	}
	leaderID := uuidToString(squad.LeaderID)
	roster := make([]squadRosterEntry, 0, len(members))
	for _, m := range members {
		entry := squadRosterEntry{role: m.Role, isLeader: uuidToString(m.MemberID) == leaderID}
		if m.MemberType == "agent" {
			if agent, aerr := h.Queries.GetAgent(ctx, m.MemberID); aerr == nil {
				entry.name = agent.Name
				entry.description = agent.Description
			}
		}
		if strings.TrimSpace(entry.name) == "" {
			entry.name = m.MemberType + " " + uuidToString(m.MemberID)
		}
		roster = append(roster, entry)
	}
	return roster
}

// SuggestSquadInstructions returns a tailored orchestrator brief for the squad
// (default policy + its real roster). It does NOT persist — the frontend puts
// the result in the editable Instructions field for the human to review + save.
func (h *Handler) SuggestSquadInstructions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireWorkspaceRole(w, r, workspaceIDFromURL(r, "workspaceId"), "workspace not found", "owner", "admin"); !ok {
		return
	}
	squad, _, ok := h.loadSquadInWorkspace(w, r)
	if !ok {
		return
	}
	roster := h.buildSquadRoster(r.Context(), squad)
	writeJSON(w, http.StatusOK, map[string]string{
		"instructions": composeTailoredOrchestratorInstructions(squad.Name, roster),
	})
}
