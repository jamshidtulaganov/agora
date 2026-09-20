package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Project risk map — the machine-readable module/blast-radius tiering for a
// legacy codebase, stored as project.settings.risk_map. Every agent run gets it
// at claim time (the run_qa gate reads the same injected block as step 0):
// classify the diff against the path globs and take the HIGHEST matching tier.
//
// It is authored through PUT /api/projects/{id}/risk-map (project_risk_map_api.go),
// which writes the key with a key-scoped jsonb_set so sibling settings are never
// clobbered, and the SERVER now classifies the diff itself — see risk_tier.go
// for the derivation and its precedence over the agent's self-reported label.
//
// Tiers:
//   - critical — money/stock-integrity paths (billing, kassa, warehouse writes):
//     never auto-merge, human review mandatory, golden flows must pass.
//   - guarded  — shared/fragile surfaces (shared Vue components, god files):
//     extra care, regression run required.
//   - safe     — isolated, low-blast-radius areas: the normal flow applies.
//
// Unknown paths default to guarded, never safe.

// riskMapEntry is one module row. Paths are gitignore-style globs relative to
// the repo root; matching is textual (the agent classifies its own diff).
type riskMapEntry struct {
	Module string   `json:"module"`
	Tier   string   `json:"tier"` // critical | guarded | safe
	Paths  []string `json:"paths"`
	Owner  string   `json:"owner,omitempty"`
	Notes  string   `json:"notes,omitempty"`
}

// riskMapMaxEntries caps rendering so a sprawling map can't blow the context
// budget — a risk map should be a tiering of modules, not a file inventory.
const riskMapMaxEntries = 40

// projectRiskMap reads + parses the issue's project risk map. ok=false when the
// issue has no project, the key is unset, or the JSON is malformed. A malformed
// map is logged LOUDLY: the risk map is a safety control (critical → human
// review mandatory), and silently dropping it would strip that protection from
// every run while the admin still believes it is in force.
func (h *Handler) projectRiskMap(ctx context.Context, issue db.Issue) ([]riskMapEntry, bool) {
	if !issue.ProjectID.Valid {
		return nil, false
	}
	return h.projectRiskMapByID(ctx, issue.ProjectID, issue.WorkspaceID)
}

// projectRiskMapByID is the same read addressed by project rather than by
// issue — what the decision queue needs, since it classifies many issues across
// a handful of projects and must load each project's map once, not once per row.
func (h *Handler) projectRiskMapByID(ctx context.Context, projectID, workspaceID pgtype.UUID) ([]riskMapEntry, bool) {
	if !projectID.Valid {
		return nil, false
	}
	// Read fail-closed on workspace: the risk map is a safety control, so it must
	// come from the issue's OWN project — a workspace-unscoped GetProject would
	// source the tier policy from a foreign project on FK drift (issue.project_id
	// is a plain FK with no same-workspace DB constraint).
	project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil || len(project.Settings) == 0 {
		return nil, false
	}
	return parseProjectRiskMap(project.Settings, project.ID)
}

// parseProjectRiskMap pulls the risk_map key out of a project settings blob.
// Separated from the read so the write endpoint can validate exactly what the
// resolver will later parse, and so a malformed map is reported identically
// wherever it is found.
func parseProjectRiskMap(settings []byte, projectID pgtype.UUID) ([]riskMapEntry, bool) {
	if len(settings) == 0 {
		return nil, false
	}
	var s struct {
		RiskMap json.RawMessage `json:"risk_map"`
	}
	if json.Unmarshal(settings, &s) != nil || len(s.RiskMap) == 0 {
		return nil, false
	}
	var entries []riskMapEntry
	if err := json.Unmarshal(s.RiskMap, &entries); err != nil {
		slog.Warn("project risk_map is malformed — the risk tiering is NOT being injected; fix project.settings.risk_map",
			"project_id", uuidToString(projectID), "error", err)
		return nil, false
	}
	if len(entries) == 0 {
		return nil, false
	}
	return entries, true
}

// issueRiskTier resolves the autonomy tier the merge gate enforces for an
// issue, for the five pipeline consumers that have always read it as a bare
// string (dev landing mode, auto-merge refusal, the done gate, QA gate depth,
// the evidence floor). It is now a thin projection of resolveIssueRiskTier
// (risk_tier.go), which owns the precedence:
//
//   - the SERVER-DERIVED tier when the linked pull request's real changed-file
//     list can be glob-matched against the project risk map;
//   - an explicit risk:<tier> label when there is no such evidence (unchanged
//     behaviour) or when the label is STRICTER than what the globs derived;
//   - GUARDED in a risk-mapped project with neither — fail closed, unknown is
//     never safe;
//   - "" for a project with no risk map (pre-risk-map behaviour stands).
//
// "" stays the internal no-opinion value on purpose: every existing consumer
// branches on it. The API boundary renders it as `unclassified` instead
// (apiRiskTier), so no client can read the empty string as "safe".
func (h *Handler) issueRiskTier(ctx context.Context, issue db.Issue) string {
	return h.resolveIssueRiskTier(ctx, issue).Tier
}

// issueRiskOwners returns the human owner names of the issue's module:<name>
// labels from the risk map (deduped, in map order) — the people a critical
// qa:pass should be surfaced to. Empty when nothing matches.
func (h *Handler) issueRiskOwners(ctx context.Context, issue db.Issue) []string {
	entries, ok := h.projectRiskMap(ctx, issue)
	if !ok {
		return nil
	}
	labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return nil
	}
	modules := map[string]bool{}
	for _, l := range labels {
		name := strings.ToLower(strings.TrimSpace(l.Name))
		if m, found := strings.CutPrefix(name, "module:"); found {
			modules[strings.TrimSpace(m)] = true
		}
	}
	if len(modules) == 0 {
		return nil
	}
	var owners []string
	seen := map[string]bool{}
	for _, e := range entries {
		owner := strings.TrimSpace(e.Owner)
		if owner == "" || seen[owner] || !modules[strings.ToLower(strings.TrimSpace(e.Module))] {
			continue
		}
		seen[owner] = true
		owners = append(owners, owner)
	}
	return owners
}

// sliceActionRiskMapContext injects the project risk map into an agent's
// instructions. Returns "" when the project has none. Mirrors the other
// project ride-alongs (QA manifest and approved Design context).
func (h *Handler) sliceActionRiskMapContext(ctx context.Context, issue db.Issue) string {
	entries, ok := h.projectRiskMap(ctx, issue)
	if !ok {
		return ""
	}
	return renderRiskMapContext(entries)
}

// renderRiskMapContext is the pure renderer — separated so the prompt wording is
// unit-testable without a database.
func renderRiskMapContext(entries []riskMapEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nPROJECT RISK MAP — modules tiered by blast radius. BEFORE changing or judging code, classify the diff against these path globs and take the HIGHEST matching tier. Any path not listed here is GUARDED, never safe.")
	for i, e := range entries {
		if i >= riskMapMaxEntries {
			b.WriteString("\n…(more entries truncated)")
			break
		}
		tier := strings.ToLower(strings.TrimSpace(e.Tier))
		if tier == "" {
			tier = "guarded"
		}
		b.WriteString("\n- [" + tier + "] " + strings.TrimSpace(e.Module))
		if len(e.Paths) > 0 {
			b.WriteString(": " + strings.Join(e.Paths, ", "))
		}
		if strings.TrimSpace(e.Owner) != "" {
			b.WriteString(" (owner: " + strings.TrimSpace(e.Owner) + ")")
		}
		if strings.TrimSpace(e.Notes) != "" {
			b.WriteString(" — " + strings.TrimSpace(e.Notes))
		}
	}
	b.WriteString("\nTIER RULES: critical → do NOT merge or self-approve; a human reviews and merges, and the golden flows for that module MUST pass first. " +
		"guarded → proceed with extra care; run the regression/base suite before calling it done; prefer the smallest possible diff; " +
		"auto-merge is withheld — a human reviews and merges here too. " +
		"safe → the normal flow applies (auto-merge allowed where enabled). When several modules match, the strictest tier wins.")
	return b.String()
}
