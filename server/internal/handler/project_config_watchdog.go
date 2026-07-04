package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Project config watchdog — the "did the knowledge actually land?" guard for
// risk-mapped (legacy) projects. Every build in the legacy pipeline is
// best-effort and async (KB study, manifest build, base-suite authoring), so a
// silent failure leaves a project that LOOKS covered but injects nothing.
// The watchdog sweeps risk-mapped projects and verifies the three artifacts the
// safety spine depends on:
//
//	(1) the KB skill (settings.kb_skill / "<slug>-kb") exists in the workspace,
//	(2) settings.qa_manifest is present,
//	(3) the standing base suite has at least one automated case.
//
// Gaps escalate as a task for the project's lead agent, telling it exactly
// which build endpoint to run. Escalation is throttled per project via the
// settings.config_watchdog_at timestamp (key-scoped write), so a persistent
// gap nags once per interval instead of every sweep. Mirrors the QA silent-gate
// watchdog (qa_watchdog.go) in spirit: missing must be LOUD, never green.

// configWatchdogMinInterval is the minimum time between escalations for the
// same project. 72h: each escalation is a fresh quick-create task for the lead
// — a daily cadence for a gap the agent may not be able to fix alone (e.g.
// missing credentials) is nag-spam, not signal.
const configWatchdogMinInterval = 72 * time.Hour

// SweepProjectConfigWatchdog checks every risk-mapped project once. Called by
// the cmd/server scheduler; each project is independent and best-effort.
func (h *Handler) SweepProjectConfigWatchdog(ctx context.Context) {
	projects, err := h.Queries.ListRiskMappedProjects(ctx)
	if err != nil {
		slog.Warn("config watchdog: list projects failed", "error", err)
		return
	}
	for _, p := range projects {
		h.checkProjectConfig(ctx, p)
	}
}

func (h *Handler) checkProjectConfig(ctx context.Context, project db.Project) {
	var gaps []string

	// (1) KB skill exists? Only a definitive no-rows counts as a gap — a
	// transient DB error must not escalate a false "skill missing".
	if name := service.ProjectKBSkillName(project); name == "" {
		gaps = append(gaps, "no resolvable KB skill name — set project.settings.kb_skill (the project title does not slugify)")
	} else if _, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: project.WorkspaceID,
		Name:        name,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			gaps = append(gaps, "knowledge-base skill \""+name+"\" does not exist — run POST /api/projects/{id}/knowledge/build (or create the skill)")
		} else {
			slog.Warn("config watchdog: kb skill lookup failed (not treated as a gap)",
				"project_id", util.UUIDToString(project.ID), "error", err)
		}
	}

	// (2) qa_manifest present?
	if !projectHasQAManifest(project.Settings) {
		gaps = append(gaps, "no qa_manifest — run POST /api/projects/{id}/qa-manifest/build or author one (PUT /api/projects/{id}/qa-manifest)")
	}

	// (3) base suite non-empty?
	if cases, err := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
	}); err == nil && len(cases) == 0 {
		gaps = append(gaps, "standing base suite is EMPTY — run POST /api/projects/{id}/base-suite/build so run_qa has golden-path regressions to execute")
	}

	if len(gaps) == 0 {
		return
	}
	slog.Warn("config watchdog: risk-mapped project has missing artifacts",
		"project_id", util.UUIDToString(project.ID), "title", project.Title, "gaps", strings.Join(gaps, " | "))

	// Throttle the escalation.
	var s struct {
		LastAt string `json:"config_watchdog_at"`
	}
	if len(project.Settings) > 0 {
		_ = json.Unmarshal(project.Settings, &s)
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s.LastAt)); err == nil && time.Since(t) < configWatchdogMinInterval {
		return
	}
	// The escalation itself: a quick-create task for the project's agent lead
	// with the exact fix list. No agent lead → the slog above is the signal.
	if !project.LeadType.Valid || project.LeadType.String != "agent" || !project.LeadID.Valid {
		return
	}
	prompt := "CONFIG WATCHDOG: the project \"" + project.Title + "\" is risk-mapped (legacy safety spine) but is missing " +
		"artifacts the QA pipeline depends on. Fix each gap below — use the listed endpoint/CLI, then verify the artifact " +
		"actually exists (the previous build may have silently failed):\n- " + strings.Join(gaps, "\n- ") +
		"\nDo not guess content: build from the connected repo and the QA box. Report what you fixed."
	// Requester = the workspace owner, so the quick-create completion signal
	// (which routes to the requesting user) is not silently dropped on a zero
	// requester id.
	requester, _ := h.bitrixWorkspaceOwner(ctx, project.WorkspaceID)
	if _, err := h.TaskService.EnqueueQuickCreateTask(
		ctx, project.WorkspaceID, requester, project.LeadID, pgtype.UUID{},
		prompt, project.ID, pgtype.UUID{}, nil,
	); err != nil {
		slog.Warn("config watchdog: escalation enqueue failed", "project_id", util.UUIDToString(project.ID), "error", err)
		return
	}
	stampJSON, _ := json.Marshal(time.Now().UTC().Format(time.RFC3339))
	if _, err := h.Queries.SetProjectSettingKey(ctx, db.SetProjectSettingKeyParams{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		Key:         "config_watchdog_at",
		Value:       stampJSON,
	}); err != nil {
		slog.Warn("config watchdog: stamp write failed", "project_id", util.UUIDToString(project.ID), "error", err)
	}
	slog.Info("config watchdog: escalated missing artifacts to project lead",
		"project_id", util.UUIDToString(project.ID), "gaps", len(gaps))
}
