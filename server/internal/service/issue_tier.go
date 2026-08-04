package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Per-task cost tiering classifies an issue by size on its first agent run and
// attaches a tier label, which the daemon claim handler (applyIssueCostTier)
// turns into a cheaper model. The audit found opus[1m] was ~74% of spend and a
// trivial CSS fix cost $2.82 vs ~$0.20 on haiku; this removes the need for a
// human to hand-tag every small issue.
//
// The keyword sets are deliberately TIGHT and UNAMBIGUOUS. The failure mode we
// must avoid is a wrong DOWNGRADE: running a genuinely large task on a small
// model produces bad output and a costly re-run, which is worse than just
// paying for opus. So we only downgrade on a reliably-small keyword AND a short
// issue body; anything else stays full.
//
// Validated against the real sd-main backlog before shipping: broad terms cause
// false positives (e.g. "rename" matched *atomic RENAME* in a backend P&L
// task), so they are intentionally excluded — better to under-tag and let a
// human/LLM catch the rest than to mis-route a real task onto haiku. Likewise
// "translation"/"language"/"локализация" are absent: the Persian localization
// read "small" but was the single most expensive issue in the audit.
var trivialTierKeywords = []string{
	"typo", "опечат", "placeholder", "tooltip", "подсказк", "wording", "reword",
}

var lightTierKeywords = []string{
	"css", "стил", "style", "выравнив", "align", "margin", "padding", "отступ",
	"spacing", "indent", "цвет", "color", "шрифт", "font", "иконк", "icon",
	"hover", "верстк",
}

// docsTierKeywords tag a documentation-only change as trivial. Docs are
// inherently low-blast-radius (no runtime code path), so they downgrade even
// though their bodies run longer than a typo — gated on tierDocsMaxLen, not the
// tight trivial/light caps. Excludes bare "doc" (matches "document"/"docker").
var docsTierKeywords = []string{
	"docs", "documentation", "readme", "changelog", ".md", ".mdx",
	"markdown", "doc-only", "документац",
}

const (
	tierTrivialMaxLen = 200  // a typo/rename issue is short
	tierLightMaxLen   = 700  // a CSS/spacing tweak is short-ish; longer ⇒ treat as full
	tierDocsMaxLen    = 4000 // docs bodies run long; still low blast radius
)

var autoTierLabelColors = map[string]string{
	"tier:trivial": "#22C55E",
	"tier:light":   "#3B82F6",
}

// classifyIssueTier returns "tier:trivial", "tier:light", or "" (leave full)
// from the issue text. Conservative by construction: a small-work keyword is
// necessary but not sufficient — a long/detailed body resolves to "" even on a
// keyword hit, because detail implies scope a small model will fumble.
func classifyIssueTier(title, description string) string {
	body := strings.ToLower(strings.TrimSpace(title + "\n" + description))
	if body == "" {
		return ""
	}
	n := len(body)
	hasAny := func(kws []string) bool {
		for _, k := range kws {
			if strings.Contains(body, k) {
				return true
			}
		}
		return false
	}
	trivial := hasAny(trivialTierKeywords)
	light := hasAny(lightTierKeywords)
	docs := hasAny(docsTierKeywords)
	switch {
	case trivial && n <= tierTrivialMaxLen:
		return "tier:trivial"
	// Docs-only work is low blast radius even when the body is longer than a
	// typo — a doc keyword within tierDocsMaxLen tags trivial so QA scopes down.
	case docs && n <= tierDocsMaxLen:
		return "tier:trivial"
	case (trivial || light) && n <= tierLightMaxLen:
		return "tier:light"
	default:
		return ""
	}
}

// maybeAutoTierIssue classifies an issue by size and attaches a tier label the
// first time it is worked. Best-effort: every failure is logged and swallowed
// because cost tiering must never block the actual enqueue. No-op when the
// issue already carries a tier: or context: label so it never overrides a human
// (or a prior auto run).
func (s *TaskService) maybeAutoTierIssue(ctx context.Context, issue db.Issue) {
	existing, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		slog.Warn("auto-tier: list issue labels failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	for _, l := range existing {
		name := strings.ToLower(strings.TrimSpace(l.Name))
		if strings.HasPrefix(name, "tier:") || strings.HasPrefix(name, "context:") {
			return // already decided by a human or an earlier run
		}
	}

	desc := ""
	if issue.Description.Valid {
		desc = issue.Description.String
	}
	tier := classifyIssueTier(issue.Title, desc)
	if tier == "" {
		return
	}

	labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, tier, autoTierLabelColors[tier])
	if err != nil {
		slog.Warn("auto-tier: ensure label failed", "issue_id", util.UUIDToString(issue.ID), "tier", tier, "error", err)
		return
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID:     issue.ID,
		LabelID:     labelID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("auto-tier: attach label failed", "issue_id", util.UUIDToString(issue.ID), "tier", tier, "error", err)
		return
	}
	slog.Info("auto-tier: applied", "issue_id", util.UUIDToString(issue.ID), "tier", tier)
}

// ensureLabel returns the id of the workspace label with this name, creating it
// when absent so auto-tiering self-seeds the tier vocabulary in any workspace
// (no separate seed step needed).
func (s *TaskService) ensureLabel(ctx context.Context, workspaceID pgtype.UUID, name, color string) (pgtype.UUID, error) {
	labels, err := s.Queries.ListLabels(ctx, workspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l.Name), name) {
			return l.ID, nil
		}
	}
	if color == "" {
		color = "#6B7280"
	}
	created, err := s.Queries.CreateLabel(ctx, db.CreateLabelParams{
		WorkspaceID: workspaceID,
		Name:        name,
		Color:       color,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}
