package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// KB synthesizer — default-on provisioning for the knowledge flywheel.
// resolveKBSynthesizer finds (or creates) the workspace's "KB Synthesizer"
// agent and persists its UUID into workspace.settings.kb_synthesizer_agent_id.
// That stamped UUID is the ingest-side TRUST ANCHOR (service.findKBSynthesizer):
// only comment blocks authored by it may auto-accept low-risk knowledge items.
// Names are never a trust decision — agent create/update routes are not
// human-gated, so any running agent can mint a "KB Synthesizer" name; the
// name lookup below exists only to adopt a pre-existing agent at
// provisioning time, and trust always flows from the stamp.

const kbSynthesizerAgentName = "KB Synthesizer"

const kbSynthesizerDescription = "Distills durable learnings from completed issues into the project knowledge base. Auto-provisioned by Agora."

const kbSynthesizerInstructions = "You are the workspace's knowledge synthesizer. When triggered on a completed issue, " +
	"read what actually happened (description, comment thread, QA verdicts, linked diff/PR) and post ONE comment " +
	"containing a fenced ```knowledge-items``` JSON block that distills the durable learnings, following the " +
	"automated directive comment on the issue. Never run the agora skill CLI and never create or edit skills — " +
	"the server compiles your items into the knowledge base. Never delegate, never assign issues, never start " +
	"work beyond the capture itself."

// Per-task model escalation (§13.3): the synthesizer's stored model stays
// haiku; a single capture over a large issue thread rides sonnet via
// agent_task_queue.model_override (wins over the agent model at claim,
// loses to issue cost-tier labels).
const (
	kbLargeContextRunes    = 25000
	kbSynthEscalationModel = "claude-sonnet-4-6" // same sonnet id applyIssueCostTier uses for tier:light
)

// kbSynthModelForProvider picks the cheap default model for an
// auto-provisioned synthesizer. The model string is opaque to the server and
// forwarded verbatim to the CLI at claim.
func kbSynthModelForProvider(provider string) string {
	switch provider {
	case "opencode":
		return "zhipuai/glm-4.5-flash" // the branded free "Agora" model
	case "claude":
		return "claude-haiku-4-5-20251001" // cheapest tier; same id applyIssueCostTier uses for tier:trivial
	default:
		return "" // unknown provider: let the runtime default decide
	}
}

// resolveKBSynthesizer returns the workspace's KB synthesizer agent id,
// provisioning one when none exists. Resolution order:
//
//  1. Persisted settings.kb_synthesizer_agent_id (trust anchor + back-compat
//     with workspaces that opted in by hand). ARCHIVED stamped agent →
//     ok=false: archiving the synthesizer IS the per-workspace opt-out
//     (restore the agent to re-enable). Never fall through to re-provision
//     here — CreateAgent would 409 forever on the case-sensitive unique name
//     (ArchiveAgent keeps the row and name) while giving the user no way to
//     turn the feature off.
//  2. Find by name, INCLUDING archived rows (archived → same opt-out). A live
//     match is adopted: its UUID is stamped so step 1 short-circuits — and so
//     ingest trusts it — from then on.
//  3. Auto-provision on a live runtime (prefer the completing agent's), cheap
//     model per provider, then stamp. Skipped entirely when
//     AGORA_KB_AUTOPROVISION_DISABLED=1.
func (h *Handler) resolveKBSynthesizer(ctx context.Context, ws db.Workspace, issue db.Issue) (pgtype.UUID, bool) {
	// 1. Persisted UUID.
	if len(ws.Settings) > 0 {
		var settings struct {
			KBAgent string `json:"kb_synthesizer_agent_id"`
		}
		_ = json.Unmarshal(ws.Settings, &settings)
		if raw := strings.TrimSpace(settings.KBAgent); raw != "" {
			if id, err := util.ParseUUID(raw); err == nil {
				agent, aerr := h.Queries.GetAgent(ctx, id)
				if aerr == nil && agent.WorkspaceID == ws.ID {
					if agent.ArchivedAt.Valid {
						return pgtype.UUID{}, false // opt-out
					}
					if agent.RuntimeID.Valid {
						return agent.ID, true
					}
				}
			}
			// Row deleted, cross-workspace, unparsable, or runtime-less:
			// fall through and re-resolve.
			slog.Warn("kb synthesizer: stamped agent unusable, re-resolving",
				"workspace_id", uuidToString(ws.ID), "agent_id", raw)
		}
	}

	// 2. Find by name (provisioning-time convenience only, never trust).
	if agent, found := h.findKBSynthesizerByName(ctx, ws.ID); found {
		if agent.ArchivedAt.Valid {
			return pgtype.UUID{}, false // opt-out, same rule as the stamped path
		}
		if !agent.RuntimeID.Valid {
			// Name occupied by an unusable row: provisioning would 409 on the
			// unique name, so capture is skipped rather than looped.
			slog.Warn("kb synthesizer: named agent has no runtime, capture skipped",
				"workspace_id", uuidToString(ws.ID), "agent_id", uuidToString(agent.ID))
			return pgtype.UUID{}, false
		}
		h.stampKBSynthesizer(ctx, ws.ID, agent.ID)
		return agent.ID, true
	}

	// 3. Auto-provision.
	if os.Getenv("AGORA_KB_AUTOPROVISION_DISABLED") == "1" {
		return pgtype.UUID{}, false
	}
	runtime, ok := h.pickKBSynthRuntime(ctx, ws.ID, issue)
	if !ok {
		// No online runtime: capture silently skipped this time, retried on
		// the next →done — matches the degrade-to-nothing contract.
		return pgtype.UUID{}, false
	}
	model := kbSynthModelForProvider(runtime.Provider)
	created, err := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        ws.ID,
		Name:               kbSynthesizerAgentName,
		Description:        kbSynthesizerDescription,
		Instructions:       kbSynthesizerInstructions,
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          runtime.ID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 3,
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		Model:              pgtype.Text{String: model, Valid: model != ""},
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "agent_workspace_name_unique" {
			// Concurrent →done race: another request provisioned first.
			// Converge on the winner via the name lookup.
			if agent, found := h.findKBSynthesizerByName(ctx, ws.ID); found && !agent.ArchivedAt.Valid && agent.RuntimeID.Valid {
				h.stampKBSynthesizer(ctx, ws.ID, agent.ID)
				return agent.ID, true
			}
			return pgtype.UUID{}, false
		}
		slog.Warn("kb synthesizer auto-provision failed",
			"workspace_id", uuidToString(ws.ID), "error", err)
		return pgtype.UUID{}, false
	}
	// Same post-create reconcile the CreateAgent handler does, so the agent
	// flips READY (the picked runtime is online by construction).
	h.TaskService.ReconcileAgentStatus(ctx, created.ID)
	// The stamp is what CaptureKnowledgeItems trusts for auto-accept —
	// without it the trust anchor degrades to a spoofable name match.
	h.stampKBSynthesizer(ctx, ws.ID, created.ID)
	slog.Info("kb synthesizer auto-provisioned",
		"workspace_id", uuidToString(ws.ID), "agent_id", uuidToString(created.ID),
		"runtime_id", uuidToString(runtime.ID), "provider", runtime.Provider, "model", model)
	return created.ID, true
}

// findKBSynthesizerByName returns the workspace agent named exactly
// "KB Synthesizer", INCLUDING archived rows (an archived row means the
// workspace opted out, and its kept unique name means provisioning must not
// be retried). Exact match on purpose: it is what CreateAgent's
// case-sensitive unique constraint would collide with.
func (h *Handler) findKBSynthesizerByName(ctx context.Context, wsID pgtype.UUID) (db.Agent, bool) {
	agents, err := h.Queries.ListAllAgents(ctx, wsID)
	if err != nil {
		return db.Agent{}, false
	}
	for _, a := range agents {
		if a.Name == kbSynthesizerAgentName {
			return a, true
		}
	}
	return db.Agent{}, false
}

// stampKBSynthesizer persists the synthesizer UUID via an atomic jsonb merge —
// safe against concurrent human settings saves, unlike UpdateWorkspace's
// whole-blob settings replace. Best-effort: a failed stamp only means the
// next capture re-resolves (and re-stamps) by name.
func (h *Handler) stampKBSynthesizer(ctx context.Context, wsID, agentID pgtype.UUID) {
	entry, err := json.Marshal(map[string]string{"kb_synthesizer_agent_id": uuidToString(agentID)})
	if err != nil {
		return
	}
	if err := h.Queries.MergeWorkspaceSettingsEntry(ctx, db.MergeWorkspaceSettingsEntryParams{
		Entry: entry, ID: wsID,
	}); err != nil {
		slog.Warn("kb synthesizer: settings stamp failed",
			"workspace_id", uuidToString(wsID), "agent_id", uuidToString(agentID), "error", err)
	}
}

// pickKBSynthRuntime picks the runtime an auto-provisioned synthesizer lives
// on: the completing agent's runtime when it is online (it just finished a
// task there — alive and provider-compatible), else the online workspace
// runtime seen most recently. ok=false when nothing is online.
func (h *Handler) pickKBSynthRuntime(ctx context.Context, wsID pgtype.UUID, issue db.Issue) (db.AgentRuntime, bool) {
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		if a, err := h.Queries.GetAgent(ctx, issue.AssigneeID); err == nil && a.WorkspaceID == wsID && a.RuntimeID.Valid {
			if rt, rerr := h.Queries.GetAgentRuntime(ctx, a.RuntimeID); rerr == nil && rt.WorkspaceID == wsID && rt.Status == "online" {
				return rt, true
			}
		}
	}
	runtimes, err := h.Queries.ListAgentRuntimes(ctx, wsID)
	if err != nil {
		return db.AgentRuntime{}, false
	}
	var best db.AgentRuntime
	found := false
	for _, rt := range runtimes {
		if rt.Status != "online" {
			continue
		}
		if !found || rt.LastSeenAt.Time.After(best.LastSeenAt.Time) {
			best, found = rt, true
		}
	}
	return best, found
}
