package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Project risk map — the machine-readable module/blast-radius tiering for a
// legacy codebase, stored as project.settings.risk_map. Every agent run gets it
// at claim time (the run_qa gate reads the same injected block as step 0):
// classify the diff against the path globs and take the HIGHEST matching tier.
// There is no dedicated write endpoint yet — the key is authored via project
// settings (jsonb) directly; writers should prefer a key-scoped jsonb_set so
// sibling keys are never clobbered.
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
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return nil, false
	}
	var s struct {
		RiskMap json.RawMessage `json:"risk_map"`
	}
	if json.Unmarshal(project.Settings, &s) != nil || len(s.RiskMap) == 0 {
		return nil, false
	}
	var entries []riskMapEntry
	if err := json.Unmarshal(s.RiskMap, &entries); err != nil {
		slog.Warn("project risk_map is malformed — the risk tiering is NOT being injected; fix project.settings.risk_map",
			"project_id", uuidToString(project.ID), "error", err)
		return nil, false
	}
	if len(entries) == 0 {
		return nil, false
	}
	return entries, true
}

// issueRiskTier resolves the autonomy tier the merge gate enforces for an
// issue. Order: an explicit risk:<tier> label wins (set by triage or a human —
// editing the label IS the override mechanism); otherwise, in a risk-mapped
// project, the tier is GUARDED — fail closed: the server cannot see the diff,
// and unknown must never mean safe. Projects with no risk map return "" (no
// tiering; pre-risk-map behavior stands).
func (h *Handler) issueRiskTier(ctx context.Context, issue db.Issue) string {
	labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err == nil {
		for _, l := range labels {
			switch strings.ToLower(strings.TrimSpace(l.Name)) {
			case "risk:critical":
				return "critical"
			case "risk:guarded":
				return "guarded"
			case "risk:safe":
				return "safe"
			}
		}
	}
	if _, ok := h.projectRiskMap(ctx, issue); ok {
		return "guarded"
	}
	return ""
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
// project ride-alongs (qa manifest, conventions, design manifest).
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
