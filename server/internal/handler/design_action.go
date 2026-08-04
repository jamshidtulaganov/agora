package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Design as a build input (not an SDLC stepper stage — see
// packages/core/issues/stage.ts): an issue that references a Figma design
// gets its design context injected straight into the dev build (draft_code —
// see figmaDesignInputContext in figma_links.go), and optionally an explicit
// designer-analyst pass (the design_proposal slice action) that reads the
// design, maps it against the project's design system, and proposes an
// implementation decomposition for a human to approve — for teams that want
// that ceremony. This file holds the handler-side helpers — agent resolution
// and the project design-manifest context injected into the recipe. Capture +
// labels live in the service layer (service/design_proposal.go) because the
// agent-comment ingest points are there.

// resolveDesignerAgent resolves the agent a design_proposal targets when the
// caller did not pin one explicitly. Order (mirrors projectDocsAgentID /
// qaSquadLeader):
//  1. project.settings.design_agent — a dedicated designer-analyst agent.
//  2. the leader of a squad whose name contains "design".
//
// Returns ok=false when neither resolves (the slice-action resolver then falls
// through to the issue assignee / caller's own agent). Every candidate must be
// ready (has a runtime, not archived).
func (h *Handler) resolveDesignerAgent(ctx context.Context, issue db.Issue) (db.Agent, bool) {
	if id := h.projectDesignAgentID(ctx, issue); id != "" {
		if agentUUID, err := util.ParseUUID(id); err == nil {
			agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID:          agentUUID,
				WorkspaceID: issue.WorkspaceID,
			})
			if err == nil && sliceAgentReady(agent) {
				return agent, true
			}
		}
	}
	if leader, ok := h.designSquadLeader(ctx, issue.WorkspaceID); ok {
		return leader, true
	}
	return db.Agent{}, false
}

// projectDesignAgentID reads the project's configured design agent (an agent
// UUID in project.settings.design_agent). Empty when unset. Mirrors
// projectDocsAgentID.
func (h *Handler) projectDesignAgentID(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	var s struct {
		DesignAgent string `json:"design_agent"`
	}
	if json.Unmarshal(project.Settings, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s.DesignAgent)
}

// designSquadLeader resolves the leader of a squad whose name contains
// "design" (case-insensitive). Mirrors qaSquadLeader. ok=false when there is no
// design squad, it has no leader, or the leader is archived / not ready.
func (h *Handler) designSquadLeader(ctx context.Context, wsID pgtype.UUID) (db.Agent, bool) {
	squads, err := h.Queries.ListSquads(ctx, wsID)
	if err != nil {
		return db.Agent{}, false
	}
	for _, s := range squads {
		if !strings.Contains(strings.ToLower(s.Name), "design") || !s.LeaderID.Valid {
			continue
		}
		leader, err := h.Queries.GetAgent(ctx, s.LeaderID)
		if err == nil && !leader.ArchivedAt.Valid && sliceAgentReady(leader) {
			return leader, true
		}
	}
	return db.Agent{}, false
}

// designManifest is the project's KNOWN design system (stored in
// project.settings.design_manifest). Dual-kind: "tokens" for modern
// token-based repos, "inventory" for legacy monoliths (sd-main PHP/Yii+Vue)
// whose de-facto system is derived from existing markup. Injected into the
// designer + implementation prompts so agents build against the known system
// instead of re-discovering it each run. Authored ONCE per project (agent-
// generated + human-editable) and reused by every run — the design counterpart
// to qaManifest.
type designManifest struct {
	Kind      string `json:"kind"`   // tokens | inventory
	Source    string `json:"source"` // agent | manual | mixed
	Revision  int    `json:"revision"`
	UpdatedAt string `json:"updated_at"`
	Figma     struct {
		LibraryFileKey string `json:"library_file_key"`
		Notes          string `json:"notes"`
	} `json:"figma"`
	Tokens struct {
		Colors     map[string]string `json:"colors"`
		Typography map[string]string `json:"typography"`
		Spacing    map[string]string `json:"spacing"`
	} `json:"tokens"`
	Components []struct {
		Name        string `json:"name"`
		CodeRef     string `json:"code_ref"`
		FigmaNodeID string `json:"figma_node_id"`
		Usage       string `json:"usage"`
	} `json:"components"`
	Conventions      []string `json:"conventions"`
	AntiPatterns     []string `json:"anti_patterns"`
	LegacyNotes      string   `json:"legacy_notes"`
	ScreensReference string   `json:"screens_reference"`
}

// designManifestMaxComponents caps how many components are rendered into the
// prompt so a large inventory can't blow the context budget.
const designManifestMaxComponents = 40

// projectDesignManifest reads + unmarshals the project's design manifest.
// ok=false when the issue has no project, the project has no manifest, or it is
// unparseable.
func (h *Handler) projectDesignManifest(ctx context.Context, issue db.Issue) (designManifest, bool) {
	if !issue.ProjectID.Valid {
		return designManifest{}, false
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return designManifest{}, false
	}
	var settings struct {
		Manifest *designManifest `json:"design_manifest"`
	}
	if json.Unmarshal(project.Settings, &settings) != nil || settings.Manifest == nil {
		return designManifest{}, false
	}
	return *settings.Manifest, true
}

// workspaceDesignManifest reads the WORKSPACE-level shared design manifest
// (workspace.settings.design_manifest) — the base every project in the
// workspace inherits (e.g. one SalesDoctor design system across sd-cs / sd-main
// / sd-billing). ok=false when unset/unparseable.
func (h *Handler) workspaceDesignManifest(ctx context.Context, wsID pgtype.UUID) (designManifest, bool) {
	ws, err := h.Queries.GetWorkspace(ctx, wsID)
	if err != nil || len(ws.Settings) == 0 {
		return designManifest{}, false
	}
	var settings struct {
		Manifest *designManifest `json:"design_manifest"`
	}
	if json.Unmarshal(ws.Settings, &settings) != nil || settings.Manifest == nil {
		return designManifest{}, false
	}
	return *settings.Manifest, true
}

// sliceActionDesignManifestContext injects the design system into the
// design_proposal / implementation prompts so the agent maps against a KNOWN
// component inventory instead of re-discovering it. Renders the WORKSPACE base
// (shared across projects) first, then the PROJECT override — so the 3 SD apps
// converge on one system while each keeps its own specifics. Returns "" when
// neither is configured. Mirrors sliceActionQAManifestContext.
func (h *Handler) sliceActionDesignManifestContext(ctx context.Context, issue db.Issue) string {
	var b strings.Builder
	if wm, ok := h.workspaceDesignManifest(ctx, issue.WorkspaceID); ok {
		b.WriteString(renderDesignManifestContextLabeled(wm, "WORKSPACE DESIGN SYSTEM (shared across this workspace's projects — the base every project inherits)"))
	}
	if pm, ok := h.projectDesignManifest(ctx, issue); ok {
		b.WriteString(renderDesignManifestContextLabeled(pm, "PROJECT DESIGN SYSTEM (this project's specifics — take precedence over the workspace base)"))
	}
	return b.String()
}

// renderDesignManifestContext renders with the default PROJECT label — kept for
// the unit tests and any single-manifest caller.
func renderDesignManifestContext(m designManifest) string {
	return renderDesignManifestContextLabeled(m, "PROJECT DESIGN SYSTEM")
}

// renderDesignManifestContextLabeled is the pure renderer — separated so the
// prompt wording is unit-tested without a database.
func renderDesignManifestContextLabeled(m designManifest, label string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(" %s (rev %d, kind=%s) — build against THIS, do not re-invent it.", label, m.Revision, m.Kind))
	if len(m.Tokens.Colors) > 0 || len(m.Tokens.Typography) > 0 || len(m.Tokens.Spacing) > 0 {
		b.WriteString(" TOKENS:")
		for name, v := range m.Tokens.Colors {
			b.WriteString(" " + name + "=" + v + ";")
		}
		for name, v := range m.Tokens.Typography {
			b.WriteString(" " + name + "=" + v + ";")
		}
		for name, v := range m.Tokens.Spacing {
			b.WriteString(" " + name + "=" + v + ";")
		}
	}
	if len(m.Components) > 0 {
		b.WriteString(" COMPONENTS (reuse these):")
		for i, c := range m.Components {
			if i >= designManifestMaxComponents {
				b.WriteString(fmt.Sprintf(" …(+%d more)", len(m.Components)-designManifestMaxComponents))
				break
			}
			b.WriteString(" " + c.Name)
			if c.CodeRef != "" {
				b.WriteString(" (" + c.CodeRef + ")")
			}
			if c.Usage != "" {
				b.WriteString(" — " + c.Usage)
			}
			b.WriteString(";")
		}
	}
	if len(m.Conventions) > 0 {
		b.WriteString(" CONVENTIONS: " + strings.Join(m.Conventions, "; ") + ".")
	}
	if len(m.AntiPatterns) > 0 {
		b.WriteString(" ANTI-PATTERNS (never do): " + strings.Join(m.AntiPatterns, "; ") + ".")
	}
	if m.LegacyNotes != "" {
		b.WriteString(" LEGACY NOTES: " + m.LegacyNotes)
	}
	return b.String()
}

// designManifestSource returns the manifest's source ("agent"/"manual"/"mixed")
// or "" when there is no manifest — the guard that stops an agent capture from
// overwriting a human-curated ("manual") manifest.
func (h *Handler) designManifestSource(ctx context.Context, issue db.Issue) string {
	m, ok := h.projectDesignManifest(ctx, issue)
	if !ok {
		return ""
	}
	return m.Source
}

// sliceActionDesignCompareContext appends an ADVISORY design-verification
// section to a run_qa instruction when the issue implements a Figma design and
// the workspace Figma credential is usable. It teaches DETERMINISTIC
// DOM/getComputedStyle comparison against the manifest tokens + the referenced
// Figma node values — never pixel-diffing. Returns "" otherwise. Consistent
// with the repo's anti-vision QA doctrine.
func (h *Handler) sliceActionDesignCompareContext(ctx context.Context, issue db.Issue) string {
	if len(issueFigmaRefs(issue)) == 0 {
		return ""
	}
	if _, _, ok := h.decryptWorkspaceFigmaToken(ctx, issue.WorkspaceID); !ok {
		return "" // no readable design to compare against
	}
	return " DESIGN VERIFICATION (this issue implements a Figma design — ADVISORY): after the functional checks, " +
		"(1) download the reference render(s) for the design node(s) referenced by this issue " +
		"(download_figma_images, pngScale=2). (2) Open the implemented screen in the embedded Chromium over CDP (or a " +
		"headless Chromium) at the smoke URL. (3) Compare DETERMINISTICALLY, NOT by pixels: from the Figma node tree " +
		"and the PROJECT DESIGN SYSTEM (above), assert in the LIVE DOM — text content present, element inventory/order, " +
		"and key colors / font-sizes / spacing via getComputedStyle. (4) Screenshot both sides and attach them as " +
		"evidence. (5) Extend your qa-result JSON with a `design` object: " +
		"`\"design\":{\"verdict\":\"pass\"|\"fail\"|\"skipped\",\"reference_node\":\"208:5147\",\"mismatches\":" +
		"[{\"kind\":\"color\"|\"typography\"|\"spacing\"|\"layout\"|\"missing_element\"|\"other\",\"selector\":\"…\"," +
		"\"expected\":\"…\",\"actual\":\"…\"}]}`. Sub-pixel and font-rendering differences are NOT mismatches; a " +
		"deviation the design proposal explicitly approved is NOT a mismatch; design debt predating this task's diff is " +
		"OUT of scope — note it, don't fail on it. The design verdict is ADVISORY: apply qa:fail ONLY when functional " +
		"checks fail OR the mismatches are severe (missing elements, wrong colors on primary surfaces). If Figma is " +
		"unreachable (429 after one Retry-After, 403, expired credential), set verdict:\"skipped\" with the reason — " +
		"NEVER fail the issue for an infra reason."
}

// designGateEnforced reports whether an issue carrying design context must have
// a passing/skipped design verdict before it can move to done. Opt-in, default
// off — ships dark until the false-fail rate is observed.
func designGateEnforced() bool {
	return strings.TrimSpace(os.Getenv("AGORA_DESIGN_GATE_ENFORCED")) == "true"
}

// issueHasDesignManifest reports whether the issue's project OR workspace
// configures a design manifest — the condition for design-lint to run.
func (h *Handler) issueHasDesignManifest(ctx context.Context, issue db.Issue) bool {
	if _, ok := h.projectDesignManifest(ctx, issue); ok {
		return true
	}
	_, ok := h.workspaceDesignManifest(ctx, issue.WorkspaceID)
	return ok
}

// sliceActionDesignLintContext appends a DIFF-SCOPED design-system lint to a
// run_qa instruction when the project (or workspace) has a design manifest. It
// checks whether the CHANGE erodes the design system — introduces off-token
// values or a new component that duplicates one the system already has — the
// governance counterpart to the whole-repo design_audit. Returns "" when there
// is no manifest to lint against.
func (h *Handler) sliceActionDesignLintContext(ctx context.Context, issue db.Issue) string {
	if !h.issueHasDesignManifest(ctx, issue) {
		return ""
	}
	return " DESIGN-SYSTEM LINT (this project has a design system — lint the CHANGE, not the whole repo): if your diff touches UI, check whether it ERODES the design system relative to the PROJECT/WORKSPACE DESIGN SYSTEM above. Flag ONLY things this change INTRODUCES: a raw hardcoded value where a token exists (a hex color / off-scale spacing / one-off font that the manifest already has a token for), or a NEW component that duplicates one the system already provides (it should reuse the existing component). Pre-existing debt the diff did not touch is OUT of scope. Record findings in the qa-result `design` object under a `lint` array: `\"lint\":[{\"kind\":\"off_token\"|\"duplicate_component\"|\"other\",\"where\":\"path:line or selector\",\"issue\":\"…\",\"severity\":\"warn\"|\"block\"}]`. Use `block` ONLY for a clear regression (a token exists and the change hardcoded its value anyway; a shared component exists and the change re-implemented it). A lint finding does NOT by itself set the qa verdict — report it; the platform decides whether to gate."
}

// designLintEnforced gates the design-lint blocking behavior. Opt-in, dark.
func designLintEnforced() bool {
	return strings.TrimSpace(os.Getenv("AGORA_DESIGN_LINT_ENFORCED")) == "true"
}

// enforceDesignLintGateBeforeDone redirects an issue's direct →done write to
// →in_review when its project has a design manifest and the latest qa-result
// carries a `block`-severity design.lint finding — the change eroded the design
// system. Opt-in (AGORA_DESIGN_LINT_ENFORCED, default off); a qa:pass label is
// always an override. Returns (statusToWrite, redirected).
func (h *Handler) enforceDesignLintGateBeforeDone(ctx context.Context, issue db.Issue, prevStatus, targetStatus string) (string, bool) {
	if !designLintEnforced() {
		return targetStatus, false
	}
	if targetStatus != "done" || prevStatus == "done" || prevStatus == "in_review" {
		return targetStatus, false
	}
	if !h.issueHasDesignManifest(ctx, issue) {
		return targetStatus, false
	}
	if h.issueHasLabel(ctx, issue, "qa:pass") {
		return targetStatus, false // human override
	}
	ev, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return targetStatus, false // no verdict yet — lint can't block what didn't run
	}
	if designLintHasBlock(ev.ResultJson) {
		return "in_review", true
	}
	return targetStatus, false
}

// designLintHasBlock reports whether result_json.design.lint has a
// severity=="block" finding.
func designLintHasBlock(resultJSON []byte) bool {
	if len(resultJSON) == 0 {
		return false
	}
	var r struct {
		Design *struct {
			Lint []struct {
				Severity string `json:"severity"`
			} `json:"lint"`
		} `json:"design"`
	}
	if json.Unmarshal(resultJSON, &r) != nil || r.Design == nil {
		return false
	}
	for _, l := range r.Design.Lint {
		if l.Severity == "block" {
			return true
		}
	}
	return false
}

// enforceDesignGateBeforeDone redirects a design-decomposed issue's direct
// →done write to →in_review when its latest QA verdict has no passing/skipped
// design result. Advisory + opt-in (AGORA_DESIGN_GATE_ENFORCED, default off);
// a `skipped` design verdict (Figma unreachable) NEVER blocks, and a human
// qa:pass label is always an override. Returns (statusToWrite, redirected).
func (h *Handler) enforceDesignGateBeforeDone(ctx context.Context, issue db.Issue, prevStatus, targetStatus string) (string, bool) {
	if !designGateEnforced() {
		return targetStatus, false
	}
	if targetStatus != "done" || prevStatus == "done" || prevStatus == "in_review" {
		return targetStatus, false
	}
	// Only issues that implement an approved design proposal are gated.
	if metaString(issue.Metadata, designMetaKeyCommentID) == "" {
		return targetStatus, false
	}
	if h.issueHasLabel(ctx, issue, "qa:pass") {
		return targetStatus, false // human override
	}
	ev, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return "in_review", true // no verdict at all → gate
	}
	switch designVerdictOf(ev.ResultJson) {
	case "pass", "skipped":
		return targetStatus, false
	default:
		return "in_review", true
	}
}

// designVerdictOf extracts result_json.design.verdict; "" when absent/malformed.
func designVerdictOf(resultJSON []byte) string {
	if len(resultJSON) == 0 {
		return ""
	}
	var r struct {
		Design *struct {
			Verdict string `json:"verdict"`
		} `json:"design"`
	}
	if json.Unmarshal(resultJSON, &r) != nil || r.Design == nil {
		return ""
	}
	return r.Design.Verdict
}
