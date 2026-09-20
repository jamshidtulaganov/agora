package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// THE RANKED DECISION QUEUE (docs/orchestration-upgrade-plan.md §A2).
//
// A measured Agora cycle is 9s of harness, 154s of agent work and 24.9 HOURS of
// human wait. There is exactly one scarce resource in the product — a human's
// attention — and until now it had no queue: escalations lived in one list,
// review requests in the inbox, QA fails on a label, approved-but-unmerged pull
// requests nowhere at all. Four inboxes is the failure the research names
// ("agents DDoSing our attention"), not the fix.
//
// This is the one list. Four item kinds, one row per issue, ranked by blast
// radius × kind × age, and computed on read.
//
// COMPUTED ON READ, NOTHING STORED. The score is derived at the moment of the
// call from facts the query returns (labels, PR states, escalation rows, the
// activity clock, the risk map). There is no rank column and no sweeper
// maintaining one — which is what stops the ranking from becoming the least
// fresh thing on the page, the same discipline docs/living-truth-plan.md
// applies to staleness.
//
// EXACT TOTALS, NO LIMIT. The query is unbounded and `total` is the real count.
// "You have 11 decisions waiting" is the sentence this endpoint exists to make
// sayable, and it is only sayable when the number is not secretly "the first
// 200".
//
// ONE ROW PER ISSUE. An issue with a parked agent AND a red QA verdict is one
// decision, not two; the highest-weight kind present wins and the rest is
// context. A queue that lists the same issue three times is a worse version of
// the inboxes it replaces.

// Item kinds. Fixed by the plan; a client MUST have a default branch (a fifth
// kind added later has to render generically, not vanish — CLAUDE.md enum
// drift).
const (
	decisionKindEscalation   = "escalation"
	decisionKindMergeReady   = "merge_ready"
	decisionKindQAFailed     = "qa_failed"
	decisionKindReviewFailed = "review_failed"
)

// What the human is being asked to do. A localizable code beside the English
// sentence, so the frontend does not have to parse prose.
const (
	decisionNeedAnswerEscalation = "answer_escalation"
	decisionNeedMergeApprovedPR  = "merge_approved_pr"
	decisionNeedReviewAndApprove = "review_and_approve"
	decisionNeedActOnQAFail      = "act_on_qa_fail"
	decisionNeedActOnQABlocked   = "act_on_qa_blocked"
	decisionNeedActOnReviewFail  = "act_on_review_fail"
)

// The ranking function, exactly as the plan specifies it (§A2):
//
//	score = risk_weight(tier)   # critical 100, guarded 40, safe 10, unclassified 25
//	      + kind_weight(item)   # escalation 60, merge_ready 30, qa_fail 25, review_fail 25
//	      + age_hours * 0.5     # capped at +40
//	      + stale_bonus         # +15 when living truth says review_done / idle
//
// Risk dominates deliberately: a critical-tier item outranks everything else in
// the queue on its tier alone, which is the whole point of ranking by blast
// radius rather than by arrival. Age is capped so a forgotten safe item can
// never climb past a fresh critical one — it surfaces, it does not take over.
const (
	decisionRiskWeightCritical     = 100.0
	decisionRiskWeightGuarded      = 40.0
	decisionRiskWeightUnclassified = 25.0
	decisionRiskWeightSafe         = 10.0

	decisionKindWeightEscalation   = 60.0
	decisionKindWeightMergeReady   = 30.0
	decisionKindWeightQAFailed     = 25.0
	decisionKindWeightReviewFailed = 25.0

	decisionAgeWeightPerHour = 0.5
	decisionAgeWeightMax     = 40.0
	decisionStaleBonus       = 15.0
)

// DecisionQueueEscalation is the escalation detail an escalation row carries, so
// the queue can offer the answer inline (and Telegram can render its options as
// buttons) without a second round trip per item.
type DecisionQueueEscalation struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Prompt  string   `json:"prompt"`
	Detail  string   `json:"detail"`
	Options []string `json:"options"`
}

// DecisionQueueItem is one thing waiting on a human.
type DecisionQueueItem struct {
	Kind       string `json:"kind"`
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	ProjectID  string `json:"project_id,omitempty"`

	// RiskTier is the SERVER-DERIVED tier wherever the pull request's real
	// changed-file list could be matched against the project risk map;
	// RiskTierSource says which it was, because "the agent says this is safe"
	// and "the globs matched the diff" must never render as the same claim.
	// Never the empty string — an unclassified project reads `unclassified`.
	RiskTier       string `json:"risk_tier"`
	RiskTierSource string `json:"risk_tier_source"`

	// Since is the instant this started waiting: raised_at for an escalation,
	// the issue's freshest activity clock otherwise (issue_to_label carries no
	// timestamp, so that is the honest measured-from). AgeHours is derived from
	// it at the moment of the call.
	Since    string  `json:"since"`
	AgeHours float64 `json:"age_hours"`

	// Needed / NeededCode: what the human has to do. The code is the stable,
	// localizable form; the sentence is the safe default rendering.
	Needed     string `json:"needed"`
	NeededCode string `json:"needed_code"`

	// Score is the computed rank. Shipped so the UI can show WHY an item is at
	// the top, and so a sort done client-side can never disagree with the
	// server's ordering.
	Score float64 `json:"score"`
	// StaleReason is the living-truth signal for this issue when it has one
	// ("idle" / "review_done" earn the stale bonus), otherwise "".
	StaleReason string `json:"stale_reason,omitempty"`

	OpenPRCount int64    `json:"open_pr_count"`
	Labels      []string `json:"labels"`

	Escalation *DecisionQueueEscalation `json:"escalation,omitempty"`
}

// DecisionQueueResponse is the wire shape. `total` is exact and equals
// len(items); `counts` is the per-kind breakdown for the tab badges.
type DecisionQueueResponse struct {
	Items  []DecisionQueueItem `json:"items"`
	Total  int                 `json:"total"`
	Counts map[string]int      `json:"counts"`
}

// decisionRiskWeight scores the blast radius. The unclassified weight sits
// BETWEEN safe and guarded on purpose: "nobody has tiered this project" is more
// urgent than a known-safe change and less urgent than a known-fragile one, and
// collapsing it into either direction is the mistake §A1.3 exists to prevent.
func decisionRiskWeight(tier string) float64 {
	switch tier {
	case riskTierCritical:
		return decisionRiskWeightCritical
	case riskTierGuarded:
		return decisionRiskWeightGuarded
	case riskTierSafe:
		return decisionRiskWeightSafe
	default: // unclassified, and any tier a future server adds
		return decisionRiskWeightUnclassified
	}
}

func decisionKindWeight(kind string) float64 {
	switch kind {
	case decisionKindEscalation:
		return decisionKindWeightEscalation
	case decisionKindMergeReady:
		return decisionKindWeightMergeReady
	case decisionKindQAFailed:
		return decisionKindWeightQAFailed
	case decisionKindReviewFailed:
		return decisionKindWeightReviewFailed
	default:
		return 0
	}
}

// decisionAgeWeight is age in hours at half a point an hour, capped. A negative
// age (a clock skew, a `since` in the future) scores zero rather than pushing an
// item below everything else.
func decisionAgeWeight(ageHours float64) float64 {
	if ageHours <= 0 {
		return 0
	}
	w := ageHours * decisionAgeWeightPerHour
	if w > decisionAgeWeightMax {
		return decisionAgeWeightMax
	}
	return w
}

// decisionStaleBonusFor is +15 for the two living-truth reasons that mean
// "this has been waiting on a person": in_review with every PR long resolved,
// and in_progress with nothing happening. reopened_work / blocked_quiet are
// real signals but they are not a decision sitting in someone's lap.
func decisionStaleBonusFor(reason string) float64 {
	switch reason {
	case staleReasonReviewDone, staleReasonIdle:
		return decisionStaleBonus
	default:
		return 0
	}
}

// decisionScore is the whole ranking function in one place, pure and testable.
func decisionScore(tier, kind, staleReason string, ageHours float64) float64 {
	return decisionRiskWeight(tier) +
		decisionKindWeight(kind) +
		decisionAgeWeight(ageHours) +
		decisionStaleBonusFor(staleReason)
}

// decisionKindForRow picks the ONE kind that describes this issue's pending
// decision. Order is by what blocks what, not by weight: a parked agent
// outranks everything (the run has ended and cannot restart without a person),
// then a red gate, then the approval. Red gates and merge-ready are mutually
// exclusive by construction — a change with a failing verdict is not ready to
// merge — so the ordering only ever arbitrates genuine overlaps.
//
// Returns "" when the row carries no pending decision, which the caller drops.
func decisionKindForRow(row db.ListDecisionQueueIssuesRow, labels map[string]bool) (kind, needCode string) {
	if row.EscalationID.Valid {
		return decisionKindEscalation, decisionNeedAnswerEscalation
	}
	switch {
	case labels["qa:fail"]:
		return decisionKindQAFailed, decisionNeedActOnQAFail
	case labels["qa:blocked"]:
		return decisionKindQAFailed, decisionNeedActOnQABlocked
	case labels["review:fail"]:
		return decisionKindReviewFailed, decisionNeedActOnReviewFail
	case labels["merge:approved"] && row.OpenPrCount > 0:
		return decisionKindMergeReady, decisionNeedMergeApprovedPR
	case row.Status == "in_review":
		return decisionKindMergeReady, decisionNeedReviewAndApprove
	}
	return "", ""
}

// decisionNeededSentence is the default English rendering of a needed code.
// The frontend localizes off the code; this exists so a client that does not
// know a code still shows a sentence instead of a blank cell.
func decisionNeededSentence(code, escalationPrompt string) string {
	switch code {
	case decisionNeedAnswerEscalation:
		if p := strings.TrimSpace(escalationPrompt); p != "" {
			return "Answer the agent: " + p
		}
		return "An agent is stopped and waiting on your answer"
	case decisionNeedMergeApprovedPR:
		return "Approved — the pull request is still unmerged"
	case decisionNeedReviewAndApprove:
		return "Review the change: approve, or request changes"
	case decisionNeedActOnQAFail:
		return "QA failed — decide: fix, waive, or re-run"
	case decisionNeedActOnQABlocked:
		return "The QA gate is blocked — it cannot produce a verdict without you"
	case decisionNeedActOnReviewFail:
		return "Review found blockers — decide the correction"
	default:
		return "This is waiting on a decision"
	}
}

// ListDecisionQueue handles GET /api/issues/decision-queue.
//
// Workspace-scoped, read-only, and a SIBLING of GET /api/issues/staleness — a
// separate query the frontend fetches beside the list, never a join into the
// board's hot path. Optional ?project_id= narrows to one project. The non-owner
// visibility gate is the same one the issue list applies, so a restricted
// member can never see a decision on an issue the list would hide.
func (h *Handler) ListDecisionQueue(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	var projectUUID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("project_id")); raw != "" {
		parsed, pok := parseUUIDOrBadRequest(w, raw, "project_id")
		if !pok {
			return
		}
		projectUUID = parsed
	}

	items, err := h.buildDecisionQueue(r.Context(), wsUUID, projectUUID, h.issueVisibilityRestriction(r))
	if err != nil {
		slog.Warn("decision queue: build failed", "workspace_id", uuidToString(wsUUID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute the decision queue")
		return
	}

	counts := map[string]int{
		decisionKindEscalation:   0,
		decisionKindMergeReady:   0,
		decisionKindQAFailed:     0,
		decisionKindReviewFailed: 0,
	}
	for _, it := range items {
		counts[it.Kind]++
	}
	writeJSON(w, http.StatusOK, DecisionQueueResponse{Items: items, Total: len(items), Counts: counts})
}

// buildDecisionQueue is the read both the endpoint and any future surface go
// through, so two callers can never drift into different definitions of "waiting
// on a human".
func (h *Handler) buildDecisionQueue(ctx context.Context, workspaceID, projectID, restrictToUser pgtype.UUID) ([]DecisionQueueItem, error) {
	rows, err := h.Queries.ListDecisionQueueIssues(ctx, db.ListDecisionQueueIssuesParams{
		WorkspaceID:    workspaceID,
		ProjectID:      projectID,
		RestrictToUser: restrictToUser,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []DecisionQueueItem{}, nil
	}

	// The living-truth signal, read once for the whole queue rather than per
	// row. A failure here costs the stale bonus and nothing else — the queue is
	// still correct without it, so it must never fail the request.
	staleByIssue := map[string]string{}
	if stale, serr := h.listStaleIssues(ctx, workspaceID, projectID, restrictToUser); serr == nil {
		for _, s := range stale {
			staleByIssue[s.IssueID] = s.Reason
		}
	} else {
		slog.Warn("decision queue: staleness read failed (ranking loses the stale bonus)",
			"workspace_id", uuidToString(workspaceID), "error", serr)
	}

	// One risk-map read per PROJECT, not per issue: a 40-item queue across three
	// projects is three project reads.
	type riskMapCacheEntry struct {
		entries []riskMapEntry
		mapped  bool
	}
	riskMaps := map[string]riskMapCacheEntry{}
	riskMapFor := func(projectID pgtype.UUID) ([]riskMapEntry, bool) {
		if !projectID.Valid {
			return nil, false
		}
		key := uuidToString(projectID)
		if cached, ok := riskMaps[key]; ok {
			return cached.entries, cached.mapped
		}
		entries, mapped := h.projectRiskMapByID(ctx, projectID, workspaceID)
		riskMaps[key] = riskMapCacheEntry{entries: entries, mapped: mapped}
		return entries, mapped
	}

	prefix := h.getIssuePrefix(ctx, workspaceID)
	now := time.Now()
	items := make([]DecisionQueueItem, 0, len(rows))
	for _, row := range rows {
		labels := make(map[string]bool, len(row.LabelNames))
		for _, l := range row.LabelNames {
			labels[strings.ToLower(strings.TrimSpace(l))] = true
		}
		kind, needCode := decisionKindForRow(row, labels)
		if kind == "" {
			// The SQL predicate and this switch agree today; if they ever drift,
			// dropping the row is the safe half of the disagreement (a phantom
			// item with no action is worse than a missing one the next refresh
			// brings back).
			continue
		}

		entries, mapped := riskMapFor(row.ProjectID)
		risk := resolveRiskTier(entries, mapped, row.ChangedPaths, row.LabelNames)

		since := row.LastActivityAt
		if kind == decisionKindEscalation && row.EscalationRaisedAt.Valid {
			since = row.EscalationRaisedAt
		}
		ageHours := 0.0
		if since.Valid {
			ageHours = now.Sub(since.Time).Hours()
		}

		issueID := uuidToString(row.ID)
		staleReason := staleByIssue[issueID]

		item := DecisionQueueItem{
			Kind:           kind,
			IssueID:        issueID,
			Identifier:     prefix + "-" + strconv.Itoa(int(row.Number)),
			Title:          row.Title,
			Status:         row.Status,
			ProjectID:      uuidToString(row.ProjectID),
			RiskTier:       risk.APITier(),
			RiskTierSource: risk.Source,
			Since:          staleTimestamp(since),
			AgeHours:       roundHours(ageHours),
			Needed:         decisionNeededSentence(needCode, row.EscalationPrompt),
			NeededCode:     needCode,
			StaleReason:    staleReason,
			OpenPRCount:    row.OpenPrCount,
			Labels:         row.LabelNames,
			Score:          decisionScore(risk.APITier(), kind, staleReason, ageHours),
		}
		if item.Labels == nil {
			item.Labels = []string{}
		}
		if row.EscalationID.Valid {
			options := row.EscalationOptions
			if options == nil {
				options = []string{}
			}
			item.Escalation = &DecisionQueueEscalation{
				ID:      uuidToString(row.EscalationID),
				Kind:    row.EscalationKind,
				Prompt:  row.EscalationPrompt,
				Detail:  row.EscalationDetail,
				Options: options,
			}
		}
		items = append(items, item)
	}

	// Highest score first. Ties break OLDEST first — between two identical
	// items the one that has been waiting longer is the one costing more — and
	// then by identifier, so the order is stable across refreshes instead of
	// shuffling on every poll.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Since != items[j].Since {
			return items[i].Since < items[j].Since
		}
		return items[i].Identifier < items[j].Identifier
	})
	return items, nil
}

// roundHours trims the age to one decimal. A queue that renders "12.4h" does
// not need fifteen significant digits, and a stable number keeps the JSON from
// churning on every poll.
func roundHours(h float64) float64 {
	return float64(int64(h*10+0.5)) / 10
}
