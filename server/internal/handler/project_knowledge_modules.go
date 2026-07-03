package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Per-module knowledge base — the depth track for a large legacy monolith. One
// "<slug>-kb" skill cannot hold a 37-module ERP; each risk-map module gets a
// focused "<slug>-kb-<module>" skill studied from ONLY that module's paths, and
// the claim path injects a module KB only when the issue carries the matching
// "module:<name>" label (from triage). The base "<slug>-kb" stays the thin
// always-injected index; module KBs add depth without inflating every run.

// projectModuleKBMax caps how many module KBs ride on a single claim, so an
// over-labelled issue can't blow the context budget.
const projectModuleKBMax = 3

// projectModuleKBMaxChars bounds each injected module KB's content (rune-safe,
// Cyrillic-aware). A module KB is meant to be a focused supplement to the base
// KB; without a cap, three large module skills plus the base KB could dominate
// the run's context. Over-cap content is truncated with a marker.
const projectModuleKBMaxChars = 8000

// capSkillContent trims an injected skill's content to a rune budget so a large
// authored skill can't blow the claim context. Returns the skill unchanged when
// within budget.
func capSkillContent(sk service.AgentSkillData, maxRunes int) service.AgentSkillData {
	if r := []rune(sk.Content); len(r) > maxRunes {
		sk.Content = strings.TrimSpace(string(r[:maxRunes])) + "\n\n…(module KB truncated for context budget — open the skill for the full text)"
	}
	return sk
}

// projectModuleKBName derives the module KB skill name: "<base-kb>-<module-slug>".
// Empty when the base name is unresolvable or the module slugifies to nothing.
func projectModuleKBName(project db.Project, module string) string {
	base := projectKBSkillName(project)
	if base == "" {
		return ""
	}
	slug := slugifyProjectName(module)
	if slug == "" {
		return ""
	}
	return base + "-" + slug
}

// buildModuleStudyPrompt composes the module-scoped study prompt + the target
// skill name. reason!="" carries a user-facing 400 message (unknown module).
func (h *Handler) buildModuleStudyPrompt(ctx context.Context, project db.Project, module string) (prompt, kbName, reason string) {
	entries, ok := h.projectRiskMapForProject(ctx, project)
	if !ok {
		return "", "", "this project has no risk map — module builds resolve a module's paths from it"
	}
	var paths []string
	var matched string
	for _, e := range entries {
		if strings.EqualFold(strings.TrimSpace(e.Module), strings.TrimSpace(module)) {
			paths = append(paths, e.Paths...)
			matched = e.Module
		}
	}
	if matched == "" || len(paths) == 0 {
		return "", "", "module \"" + module + "\" is not in the project risk map (or has no paths)"
	}
	kbName = projectModuleKBName(project, matched)
	if kbName == "" {
		return "", "", "could not derive a KB skill name for this project — set project.settings.kb_skill"
	}
	prompt = fmt.Sprintf(
		"Build the MODULE knowledge base for the \"%s\" module of the project \"%s\". Work AUTONOMOUSLY — do NOT ask "+
			"questions; finish end to end.\n\nStudy ONLY this module's code (the connected repositories are attached — "+
			"check them out). Its paths in this codebase: %s. Cover: what this module does, its key entry points / "+
			"controllers / models / services, the data it owns, its invariants and gotchas (transactions, locks, tenancy, "+
			"external contracts), how it is verified on the QA box, and the traps an engineer must avoid here. Ground every "+
			"claim in real files — cite paths.\n\nThen persist it as the workspace SKILL named \"%s\" via the agora skill "+
			"CLI (create or update). A worktree file does NOT count — ONLY the saved \"%s\" skill is read by other agents. "+
			"Keep it tight and specific to THIS module; do not restate the whole-project base KB.",
		matched, project.Title, strings.Join(paths, ", "), kbName, kbName) + soloAutomationDirective
	return prompt, kbName, ""
}

// projectRiskMapForProject is projectRiskMap keyed by a loaded project (the
// build path already has the project; avoids a re-fetch via the issue).
func (h *Handler) projectRiskMapForProject(_ context.Context, project db.Project) ([]riskMapEntry, bool) {
	if len(project.Settings) == 0 {
		return nil, false
	}
	var s struct {
		RiskMap json.RawMessage `json:"risk_map"`
	}
	if json.Unmarshal(project.Settings, &s) != nil || len(s.RiskMap) == 0 {
		return nil, false
	}
	var entries []riskMapEntry
	if json.Unmarshal(s.RiskMap, &entries) != nil || len(entries) == 0 {
		return nil, false
	}
	return entries, true
}

// recordModuleKBCoverage stamps settings.kb_coverage[<module>] = <RFC3339> so a
// human can see which modules have had a KB build requested. The single-pair
// merge happens in SQL (MergeProjectCoverageEntry, jsonb ||) so concurrent
// per-module builds never clobber each other's stamp — a Go read-modify-write
// of the whole object would last-write-wins and drop a sibling's timestamp.
func (h *Handler) recordModuleKBCoverage(ctx context.Context, project db.Project, module string) {
	entry, err := json.Marshal(map[string]string{
		strings.TrimSpace(module): time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	if _, err := h.Queries.MergeProjectCoverageEntry(ctx, db.MergeProjectCoverageEntryParams{
		ID: project.ID, WorkspaceID: project.WorkspaceID, Entry: entry,
	}); err != nil {
		slog.Warn("record module kb coverage failed", "project_id", util.UUIDToString(project.ID), "error", err)
	}
}

// projectKBSkills resolves ALL KB skills to auto-inject for an issue: the base
// "<slug>-kb" plus each "<slug>-kb-<module>" whose module matches one of the
// issue's "module:<name>" labels (capped, deduped). Replaces the single-skill
// projectKBSkill at the claim path.
func (h *Handler) projectKBSkills(ctx context.Context, issue db.Issue) []service.AgentSkillData {
	var out []service.AgentSkillData
	seen := map[string]bool{}
	add := func(sk service.AgentSkillData, ok bool) {
		if ok && !seen[sk.Name] {
			seen[sk.Name] = true
			out = append(out, sk)
		}
	}
	// Base KB (existing single-skill resolver).
	add(h.projectKBSkill(ctx, issue))

	if !issue.ProjectID.Valid {
		return out
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return out
	}
	labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return out
	}
	count := 0
	for _, l := range labels {
		if count >= projectModuleKBMax {
			break
		}
		mod, found := strings.CutPrefix(strings.ToLower(strings.TrimSpace(l.Name)), "module:")
		if !found {
			continue
		}
		name := projectModuleKBName(project, strings.TrimSpace(mod))
		if name == "" || seen[name] {
			continue
		}
		skill, serr := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: issue.WorkspaceID,
			Name:        name,
		})
		if serr != nil {
			continue // module KB not built yet — silently skip
		}
		data := service.AgentSkillData{
			ID: uuidToString(skill.ID), Name: skill.Name, Description: skill.Description, Content: skill.Content,
		}
		files, _ := h.Queries.ListSkillFiles(ctx, skill.ID)
		for _, f := range files {
			data.Files = append(data.Files, service.AgentSkillFileData{Path: f.Path, Content: f.Content})
		}
		add(capSkillContent(data, projectModuleKBMaxChars), true)
		count++
	}
	return out
}
