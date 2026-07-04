package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Knowledge-item ingestion — the agent-facing half of the KB flywheel.
// A synthesizer (or any agent) posts a fenced ```knowledge-items``` JSON
// block in an issue comment; both agent-comment ingest paths (HTTP POST
// /comments and the internal createAgentComment) route it here. Items are
// sanitized, deduped (exact via the partial unique index, near via Jaccard),
// gated by proposer trust (only the workspace's persisted synthesizer UUID
// can auto-accept, and only low-risk kinds), and compiled into the KB skill
// via RecompileKB. Everything is best-effort: capture must never fail the
// comment that carried it.

const (
	kbCaptureMaxItems        = 10  // proposals processed per comment; extras dropped (spam guard)
	kbCaptureProposedCeiling = 100 // unreviewed agent proposals per KB before new proposals are refused
	kbTitleMaxRunes          = 160
	kbBodyMaxRunes           = 1200
	kbModuleMaxRunes         = 64
)

// Near-duplicate thresholds over norm_title token sets: a same-kind
// restatement merges earlier than a cross-kind one.
const (
	kbJaccardSameKind = 0.6
	kbJaccardAnyKind  = 0.8
)

// Kinds auto-accepted from the trusted synthesizer. Instruction-bearing kinds
// (convention, decision, architecture) always land proposed — the human
// review gate against prompt injection riding into future agents' context.
var kbAutoAcceptKinds = map[string]bool{"gotcha": true, "nav": true}

// knowledgeItemsBlockRe extracts the ```knowledge-items``` fenced JSON array.
// Leading whitespace is tolerated on the fence lines: an INDENTED fence is
// not protected by mention-expansion's line-anchored skip regions
// (mention/expand.go findSkipRegions), so it must still be findable here.
var knowledgeItemsBlockRe = regexp.MustCompile("(?ms)^[ \t]*```knowledge-items\\s*\\n(.*?)```")

// kbIssueKeyRe strips issue-key tokens (MUL-123) from titles BEFORE
// lowercasing. Uppercase-anchored on purpose: a post-lowercase [a-z]{2,10}-\d+
// would also delete lowercase tech tokens like react-18 / glm-4 / pg-17 and
// merge genuinely distinct learnings into silent hit-bumps.
var kbIssueKeyRe = regexp.MustCompile(`\b[A-Z]{2,10}-\d+\b`)

var knowledgeKinds = map[string]bool{
	"architecture": true,
	"gotcha":       true,
	"convention":   true,
	"nav":          true,
	"decision":     true,
}

// knowledgeItemProposal is one entry of the fenced JSON array (§ block schema).
type knowledgeItemProposal struct {
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// truncateKnowledgeRunes truncates at a rune boundary, marking the cut with an
// ellipsis so a truncated body reads as truncated, not corrupted.
func truncateKnowledgeRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// cleanKnowledgeString applies the field-agnostic hygiene: valid UTF-8, NUL
// strip (mirrors handler.sanitizeNullBytes), HTML-comment token strip (a body
// must not be able to spoof or terminate the managed-region markers) and 3+
// backtick-run collapse (must not be able to open/close a fence).
func cleanKnowledgeString(s string) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
	return stripKBRenderUnsafe(s)
}

// SanitizeKnowledgeTitle applies the ingest hygiene to a title: valid UTF-8 +
// NUL strip, marker/fence-token strip, ALL whitespace runs (incl. newlines)
// collapsed to single spaces (titles render inline as `- **<title>**`; an
// embedded newline would inject markdown structure into the compiled region),
// trimmed, capped at 160 runes. Returns "" when nothing survives.
// Exported for the review-API handler so human input passes the exact same
// hygiene as agent proposals.
func SanitizeKnowledgeTitle(s string) string {
	return truncateKnowledgeRunes(strings.TrimSpace(kbWhitespaceRunRe.ReplaceAllString(cleanKnowledgeString(s), " ")), kbTitleMaxRunes)
}

// SanitizeKnowledgeModule is SanitizeKnowledgeTitle's 64-rune module variant.
func SanitizeKnowledgeModule(s string) string {
	return truncateKnowledgeRunes(strings.TrimSpace(kbWhitespaceRunRe.ReplaceAllString(cleanKnowledgeString(s), " ")), kbModuleMaxRunes)
}

// SanitizeKnowledgeBody applies the ingest hygiene to a body: newlines are
// KEPT (bodies are line-indented at render), capped at 1200 runes.
func SanitizeKnowledgeBody(s string) string {
	return truncateKnowledgeRunes(strings.TrimSpace(cleanKnowledgeString(s)), kbBodyMaxRunes)
}

// NormalizeKnowledgeTitle is the exported norm_title derivation for the
// review-API handler (title edits must recompute the dedupe key).
func NormalizeKnowledgeTitle(title string) string {
	return normalizeKnowledgeTitle(title)
}

// IsKnowledgeKind reports whether kind is one of the five item kinds.
func IsKnowledgeKind(kind string) bool {
	return knowledgeKinds[kind]
}

// sanitizeKnowledgeText scrubs one proposal: hygiene on every field, rune caps
// 160/1200/64, unknown kind → "gotcha". ok=false when the title is empty
// after cleaning (item dropped).
func sanitizeKnowledgeText(p knowledgeItemProposal) (knowledgeItemProposal, bool) {
	title := SanitizeKnowledgeTitle(p.Title)
	if title == "" {
		return p, false
	}
	p.Title = title
	p.Module = SanitizeKnowledgeModule(p.Module)
	p.Body = SanitizeKnowledgeBody(p.Body)
	if kind := strings.ToLower(strings.TrimSpace(p.Kind)); knowledgeKinds[kind] {
		p.Kind = kind
	} else {
		p.Kind = "gotcha"
	}
	return p, true
}

// normalizeKnowledgeTitle derives the norm_title dedupe key: issue-key tokens
// stripped (before lowercasing — see kbIssueKeyRe), lowercased, every
// non-letter/digit rune folded to a space, whitespace collapsed, trimmed.
func normalizeKnowledgeTitle(title string) string {
	s := strings.ToLower(kbIssueKeyRe.ReplaceAllString(title, " "))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func knowledgeTitleTokens(normTitle string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, t := range strings.Fields(normTitle) {
		set[t] = struct{}{}
	}
	return set
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if _, ok := b[t]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// knowledgeDedupeKey is one non-archived item's dedupe signature. Freshly
// inserted proposals are appended to the in-memory list during a capture so
// same-batch near-duplicates (a synthesizer paraphrasing itself across items)
// are caught too.
type knowledgeDedupeKey struct {
	id     pgtype.UUID
	tokens map[string]struct{}
	kind   string
	status string
}

// findKnowledgeNearDuplicate returns the best-scoring existing item the
// proposal restates: jaccard >= 0.6 for the same kind, >= 0.8 regardless of
// kind. O(proposals × items); items per KB are hundreds at most.
func findKnowledgeNearDuplicate(keys []knowledgeDedupeKey, tokens map[string]struct{}, kind string) (knowledgeDedupeKey, bool) {
	best := knowledgeDedupeKey{}
	bestScore := 0.0
	found := false
	for _, k := range keys {
		score := jaccardSimilarity(tokens, k.tokens)
		threshold := kbJaccardAnyKind
		if k.kind == kind {
			threshold = kbJaccardSameKind
		}
		if score >= threshold && (!found || score > bestScore) {
			best, bestScore, found = k, score, true
		}
	}
	return best, found
}

// findKBSynthesizer is the ingest-side trust read: the agent UUID persisted in
// workspace.settings.kb_synthesizer_agent_id, and nothing else — no name
// matching (agent names are mintable by any running agent, so a name match is
// spoofable) and no provisioning (that is the capture-enqueue resolver's job).
// ok=false when unset, unparsable, cross-workspace, missing, or archived
// (archiving the synthesizer is the per-workspace opt-out).
func (s *TaskService) findKBSynthesizer(ctx context.Context, workspaceID pgtype.UUID) (pgtype.UUID, bool) {
	ws, err := s.Queries.GetWorkspace(ctx, workspaceID)
	if err != nil || len(ws.Settings) == 0 {
		return pgtype.UUID{}, false
	}
	var settings struct {
		KBSynthesizerAgentID string `json:"kb_synthesizer_agent_id"`
	}
	if json.Unmarshal(ws.Settings, &settings) != nil {
		return pgtype.UUID{}, false
	}
	raw := strings.TrimSpace(settings.KBSynthesizerAgentID)
	if raw == "" {
		return pgtype.UUID{}, false
	}
	id, err := util.ParseUUID(raw)
	if err != nil {
		return pgtype.UUID{}, false
	}
	agent, err := s.Queries.GetAgent(ctx, id)
	if err != nil || agent.WorkspaceID != workspaceID || agent.ArchivedAt.Valid {
		return pgtype.UUID{}, false
	}
	return agent.ID, true
}

// CaptureKnowledgeItems persists a ```knowledge-items``` fenced JSON block
// from an agent comment as knowledge_item rows, then recompiles the KB.
// Best-effort + detached (no block / malformed JSON / no project → no-op,
// but malformed JSON logs a warning — a completed LLM run's output must not
// be silently indistinguishable from "no learnings").
// Exported so the HTTP comment handler calls it too. The internal
// createAgentComment path MUST pass the PRE-expansion comment content:
// ExpandIssueIdentifiers rewrites MUL-123 tokens into markdown links even
// inside indented fences, which breaks the JSON parse.
func (s *TaskService) CaptureKnowledgeItems(ctx context.Context, issue db.Issue, content string, agentID pgtype.UUID) {
	m := knowledgeItemsBlockRe.FindStringSubmatch(content)
	if m == nil {
		return
	}
	raw := strings.TrimSpace(m[1])
	var proposals []knowledgeItemProposal
	if err := json.Unmarshal([]byte(raw), &proposals); err != nil {
		slog.Warn("knowledge capture: malformed knowledge-items block",
			"issue_id", util.UUIDToString(issue.ID), "error", err,
			"snippet", truncateKnowledgeRunes(raw, 200))
		return
	}
	if len(proposals) == 0 || !issue.ProjectID.Valid {
		return
	}
	project, err := s.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return
	}
	// Defensive: a capture must never write rows (or compile a skill) across
	// workspaces, whatever path handed us the issue.
	if project.WorkspaceID != issue.WorkspaceID {
		slog.Warn("knowledge capture: project/workspace mismatch",
			"issue_id", util.UUIDToString(issue.ID), "project_id", util.UUIDToString(project.ID))
		return
	}
	kbName := ProjectKBSkillName(project)
	if kbName == "" {
		return
	}

	skipped := 0
	if len(proposals) > kbCaptureMaxItems {
		skipped += len(proposals) - kbCaptureMaxItems
		proposals = proposals[:kbCaptureMaxItems]
	}

	// Spam guard: a saturated review queue means more unreviewed rows help no
	// one — would-be-proposed items are refused until humans catch up.
	// Auto-accepted items (trusted synthesizer, low-risk kinds) still land.
	reviewSaturated := false
	if backlog, err := s.Queries.CountProposedAgentKnowledgeItems(ctx, db.CountProposedAgentKnowledgeItemsParams{
		WorkspaceID: issue.WorkspaceID, KbName: kbName,
	}); err != nil {
		slog.Warn("knowledge capture: proposed backlog count failed", "error", err, "kb_name", kbName)
	} else if backlog >= kbCaptureProposedCeiling {
		reviewSaturated = true
		slog.Warn("knowledge capture: review queue saturated, refusing new proposals",
			"kb_name", kbName, "backlog", backlog, "issue_id", util.UUIDToString(issue.ID))
	}

	synthID, synthOK := s.findKBSynthesizer(ctx, issue.WorkspaceID)
	trusted := synthOK && agentID.Valid && agentID == synthID

	keys, err := s.Queries.ListKnowledgeItemKeysForDedupe(ctx, db.ListKnowledgeItemKeysForDedupeParams{
		WorkspaceID: issue.WorkspaceID, KbName: kbName,
	})
	if err != nil {
		slog.Warn("knowledge capture: dedupe key load failed", "error", err, "kb_name", kbName)
		return
	}
	dedupe := make([]knowledgeDedupeKey, 0, len(keys)+len(proposals))
	for _, k := range keys {
		dedupe = append(dedupe, knowledgeDedupeKey{
			id: k.ID, tokens: knowledgeTitleTokens(k.NormTitle), kind: k.Kind, status: k.Status,
		})
	}

	var (
		inserted, confirmed, proposed int
		newProposed                   int
		acceptedTitles                []string
		activeChanged                 bool
		wrote                         bool
	)

	for _, rawProposal := range proposals {
		p, ok := sanitizeKnowledgeText(rawProposal)
		if !ok {
			skipped++
			continue
		}
		normTitle := normalizeKnowledgeTitle(p.Title)
		if normTitle == "" {
			skipped++
			continue
		}
		tokens := knowledgeTitleTokens(normTitle)

		// Near-duplicate → confirm instead of insert. The hit bump is
		// proposer-gated: an untrusted agent restating existing items must not
		// pin them to the top of every future compile (rank pumping).
		if match, found := findKnowledgeNearDuplicate(dedupe, tokens, p.Kind); found {
			if !trusted {
				skipped++
				continue
			}
			if err := s.Queries.BumpKnowledgeItemHits(ctx, db.BumpKnowledgeItemHitsParams{
				ID: match.id, WorkspaceID: issue.WorkspaceID,
			}); err != nil {
				slog.Warn("knowledge capture: hit bump failed", "error", err, "item_id", util.UUIDToString(match.id))
				skipped++
				continue
			}
			confirmed++
			wrote = true
			if match.status == "active" {
				activeChanged = true // rank / ×N rendering changed
			}
			continue
		}

		status := "proposed"
		if trusted && kbAutoAcceptKinds[p.Kind] {
			status = "active"
		}
		if status == "proposed" && reviewSaturated {
			skipped++
			continue
		}

		if trusted {
			row, err := s.Queries.UpsertKnowledgeItem(ctx, db.UpsertKnowledgeItemParams{
				WorkspaceID:   issue.WorkspaceID,
				ProjectID:     issue.ProjectID,
				KbName:        kbName,
				Module:        p.Module,
				Kind:          p.Kind,
				Title:         p.Title,
				Body:          p.Body,
				NormTitle:     normTitle,
				SourceIssueID: issue.ID,
				CreatedByType: "agent",
				CreatedByID:   agentID,
				Status:        status,
			})
			if err != nil {
				slog.Warn("knowledge capture: upsert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
				skipped++
				continue
			}
			wrote = true
			if !row.Inserted {
				// Exact normalized-title collision with a live row: the upsert
				// confirmed it (hits+1) and left title/body/status untouched.
				confirmed++
				if row.Status == "active" {
					activeChanged = true
				}
				continue
			}
			inserted++
			if row.Status == "active" {
				activeChanged = true
				acceptedTitles = append(acceptedTitles, row.Title)
			} else {
				proposed++
				newProposed++
			}
			dedupe = append(dedupe, knowledgeDedupeKey{id: row.ID, tokens: tokens, kind: row.Kind, status: row.Status})
			continue
		}

		id, err := s.Queries.InsertKnowledgeItemIgnoreDup(ctx, db.InsertKnowledgeItemIgnoreDupParams{
			WorkspaceID:   issue.WorkspaceID,
			ProjectID:     issue.ProjectID,
			KbName:        kbName,
			Module:        p.Module,
			Kind:          p.Kind,
			Title:         p.Title,
			Body:          p.Body,
			NormTitle:     normTitle,
			SourceIssueID: issue.ID,
			CreatedByType: "agent",
			CreatedByID:   agentID,
			Status:        "proposed",
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Exact collision: silent no-op for untrusted proposers.
				skipped++
				continue
			}
			slog.Warn("knowledge capture: insert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
			skipped++
			continue
		}
		wrote = true
		inserted++
		proposed++
		newProposed++
		dedupe = append(dedupe, knowledgeDedupeKey{id: id, tokens: tokens, kind: p.Kind, status: "proposed"})
	}

	if !wrote {
		if skipped > 0 {
			slog.Info("knowledge items captured",
				"issue_id", util.UUIDToString(issue.ID), "kb_name", kbName,
				"inserted", 0, "confirmed", 0, "proposed", 0, "skipped", skipped)
		}
		return
	}

	if activeChanged {
		// RecompileKB publishes EventKnowledgeChanged itself on success.
		s.RecompileKB(ctx, issue.WorkspaceID, kbName)
	} else if s.Bus != nil {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventKnowledgeChanged,
			WorkspaceID: util.UUIDToString(issue.WorkspaceID),
			ActorType:   "agent",
			ActorID:     util.UUIDToString(agentID),
			Payload:     map[string]any{"kb_name": kbName},
		})
	}

	// The review queue must not be invisible — Phase 3 UI doesn't exist yet.
	if newProposed > 0 {
		s.notifyKnowledgeItems(ctx, project, issue, agentID, "knowledge_items_proposed", "action_required",
			fmt.Sprintf("%d knowledge items await review on %s", newProposed, project.Title), nil)
	}
	// Auto-accepts are announced so silent KB poisoning is observable in
	// minutes, not never.
	if len(acceptedTitles) > 0 {
		s.notifyKnowledgeItems(ctx, project, issue, agentID, "knowledge_items_accepted", "info",
			fmt.Sprintf("%d knowledge items auto-accepted on %s", len(acceptedTitles), project.Title), acceptedTitles)
	}

	slog.Info("knowledge items captured",
		"issue_id", util.UUIDToString(issue.ID), "kb_name", kbName,
		"inserted", inserted, "confirmed", confirmed, "proposed", proposed, "skipped", skipped)
}

// knowledgeReviewRecipients returns the user ids to notify about knowledge
// items on a project: the project lead when it is a human, else every
// workspace owner/admin.
func (s *TaskService) knowledgeReviewRecipients(ctx context.Context, project db.Project) []pgtype.UUID {
	if project.LeadType.Valid && project.LeadType.String == "member" && project.LeadID.Valid {
		return []pgtype.UUID{project.LeadID}
	}
	members, err := s.Queries.ListMembers(ctx, project.WorkspaceID)
	if err != nil {
		slog.Warn("knowledge capture: list members failed", "error", err,
			"workspace_id", util.UUIDToString(project.WorkspaceID))
		return nil
	}
	var out []pgtype.UUID
	for _, m := range members {
		if m.Role == "owner" || m.Role == "admin" {
			out = append(out, m.UserID)
		}
	}
	return out
}

// notifyKnowledgeItems writes one inbox item per recipient and publishes
// inbox:new for each — the same CreateInboxItem + publish flow the
// quick-create completion and design-proposal notifications use.
func (s *TaskService) notifyKnowledgeItems(ctx context.Context, project db.Project, issue db.Issue, agentID pgtype.UUID, itemType, severity, message string, titles []string) {
	recipients := s.knowledgeReviewRecipients(ctx, project)
	if len(recipients) == 0 {
		return
	}
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	detailsMap := map[string]any{
		"issue_id":   util.UUIDToString(issue.ID),
		"identifier": fmt.Sprintf("%s-%d", prefix, issue.Number),
		"project_id": util.UUIDToString(project.ID),
		"kb_name":    ProjectKBSkillName(project),
	}
	if len(titles) > 0 {
		detailsMap["titles"] = titles
	}
	details, _ := json.Marshal(detailsMap)
	for _, uid := range recipients {
		item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   issue.WorkspaceID,
			RecipientType: "member",
			RecipientID:   uid,
			Type:          itemType,
			Severity:      severity,
			IssueID:       issue.ID,
			Title:         issue.Title,
			Body:          pgtype.Text{String: message, Valid: true},
			ActorType:     pgtype.Text{String: "agent", Valid: true},
			ActorID:       agentID,
			Details:       details,
		})
		if err != nil {
			slog.Warn("knowledge capture: inbox write failed", "error", err,
				"issue_id", util.UUIDToString(issue.ID), "type", itemType)
			continue
		}
		s.publishQuickCreateInbox(item, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(agentID), issue.Status)
	}
}
