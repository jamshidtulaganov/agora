package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Bitrix intake triage — the front-door pass over every NEWLY-imported Bitrix
// issue. A dedicated triage agent classifies the raw RU/UZ ticket (type /
// module / risk labels from the project risk map), enriches it with codebase
// pointers, and asks the reporter back when the ticket is under-specified.
//
// SUGGEST-ONLY by design: the triage agent labels and comments — it never
// changes status, assignee, or project. Routing decisions stay with humans and
// the squad leads until the rollout ladder promotes them.
//
// Opt-in via workspace.settings.triage_agent_id (mirrors the KB synthesizer's
// kb_synthesizer_agent_id key). Absent key = no triage, zero behavior change.

// bitrixTriagePrompt is the server-authored triage contract. It rides as the
// task's trigger comment (mention tasks carry no instruction field), so what
// triage means is pinned by the server, not by the agent's own persona.
const bitrixTriagePrompt = "[AUTOMATED DIRECTIVE — intake triage] " +
	"TRIAGE this newly-imported Bitrix ticket (intake pass, SUGGEST-ONLY). " +
	"(1) CLASSIFY — decide the work type yourself from the title, description, comments, and attachments. " +
	"Bitrix tags are noisy (mixed RU/UZ/EN, typos, #prefixes) — do NOT trust them for type. " +
	"Attach exactly one of: `type:bug` (defect / wrong behavior / regression — implementers debug), " +
	"`type:feature` (new or changed capability — implementers plan then build), " +
	"or `type:question` (clarification / investigation with no clear deliverable yet). " +
	"Do NOT invent separate mode:* labels — type:* is the fundamental classifier. " +
	"Also attach `module:<name>` using the module names from the PROJECT RISK MAP in your context (pick every module the " +
	"ticket plausibly touches); `risk:<tier>` = the HIGHEST tier among those modules (critical|guarded|safe; " +
	"no match → risk:guarded, never safe). " +
	"(2) ENRICH — post ONE comment IN THE ISSUE'S LANGUAGE (Russian/Uzbek) that gives the future implementer a head start: " +
	"the likely files/modules (use the knowledge-base skill in your context; flag any 10k+ LOC god file), related known " +
	"gotchas (tenancy, deadlock paths, frozen APIs), and how to verify on the QA box. Be concrete or be silent — no filler. " +
	"(3) ASK BACK — if the ticket lacks what a developer needs (reproduction steps, expected vs actual, affected " +
	"tenant/filial, screenshots), attach the `needs:spec` label and add to your comment a short numbered list of the " +
	"missing details. NOTE: the reporter lives in Bitrix and may never see this comment — write the list so a teammate " +
	"can forward it verbatim, and keep `needs:spec` as the signal for humans to relay it. " +
	"HARD LIMITS: do NOT change status, assignee, project, or sprint; do NOT start implementing; do NOT create sub-issues. " +
	"Labels + one comment — that is the whole job."

// bitrixTriageMaxPerSync caps triage fan-out per sync run. The create path is
// shared by the single-task webhook AND the bulk import/poll loops — without a
// cap, one historical import of hundreds of tasks would queue hundreds of
// serialized agent runs on the one triage agent. The webhook path creates one
// issue per call, so the cap never bites it; a bulk import triages the first N
// and skips the rest (logged), which is the right trade for stale backfill.
const bitrixTriageMaxPerSync = 10

// maybeEnqueueBitrixTriage fires the triage pass for a just-created Bitrix
// issue. Best-effort: any failure logs and returns — the import itself must
// never fail because triage couldn't be enqueued. issueStatus is the mapped
// Agora status of the incoming task: already-closed tickets (done/cancelled
// historical backfill) are never triaged.
func (h *Handler) maybeEnqueueBitrixTriage(ctx context.Context, ws db.Workspace, issue db.Issue, issueStatus string, st *bitrixSyncState) {
	var settings struct {
		TriageAgent string `json:"triage_agent_id"`
	}
	if len(ws.Settings) > 0 {
		_ = json.Unmarshal(ws.Settings, &settings)
	}
	if strings.TrimSpace(settings.TriageAgent) == "" {
		return // workspace has not opted in
	}
	switch issueStatus {
	case "done", "cancelled":
		return // historical/closed backfill — nothing to triage
	}
	if st != nil {
		if st.triaged >= bitrixTriageMaxPerSync {
			slog.Info("bitrix triage: per-sync cap reached, skipping", "issue_id", util.UUIDToString(issue.ID), "cap", bitrixTriageMaxPerSync)
			return
		}
	}
	agentID, err := parseUUIDLoose(settings.TriageAgent)
	if err != nil || !agentID.Valid {
		slog.Warn("bitrix triage: invalid triage_agent_id", "workspace_id", util.UUIDToString(ws.ID), "value", settings.TriageAgent)
		return
	}
	// Validate the agent BEFORE writing the prompt comment — an archived or
	// runtime-less agent would otherwise leave an orphaned instruction comment
	// on every imported issue (and poison later agent runs reading the thread).
	agent, aerr := h.Queries.GetAgent(ctx, agentID)
	if aerr != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		slog.Warn("bitrix triage: triage agent unavailable", "workspace_id", util.UUIDToString(ws.ID), "agent_id", settings.TriageAgent)
		return
	}
	// The prompt rides as a trigger comment authored by the triage agent itself
	// (direct Queries.CreateComment — bypasses the agent-comment ingest, so no
	// capture hooks or self-trigger loops fire on it).
	comment, cerr := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		AuthorType: "agent", AuthorID: agentID,
		Content: bitrixTriagePrompt + soloAutomationDirective, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if cerr != nil {
		slog.Warn("bitrix triage: prompt comment failed", "issue_id", util.UUIDToString(issue.ID), "error", cerr)
		return
	}
	if _, err := h.TaskService.EnqueueTaskForMention(ctx, issue, agentID, comment.ID); err != nil {
		slog.Warn("bitrix triage: enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	if st != nil {
		st.triaged++
	}
	slog.Info("bitrix triage enqueued", "issue_id", util.UUIDToString(issue.ID), "agent_id", settings.TriageAgent)
}
