package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// qaResultBlockRe extracts the ```qa-result``` fenced JSON the run_qa recipe
// appends to its verdict comment. Mirrors the frontend parseQAResultBlock so the
// server persists the SAME structured payload the editor used to parse on read.
var qaResultBlockRe = regexp.MustCompile("(?s)```qa-result\\s*\\n(.*?)```")

// qaResultPayload is the structured verdict the agent emits. baseline_ref /
// branch_sha are intentionally NOT part of the block in P1 — evidence is keyed
// (issue, "", "") so each verdict refreshes one latest-row per issue. P2 widens
// the block to carry the tested sha + baseline ref for per-commit history.
type qaResultPayload struct {
	Verdict     string            `json:"verdict"`
	Summary     string            `json:"summary"`
	Commands    []json.RawMessage `json:"commands"`
	Screenshots []json.RawMessage `json:"screenshots"`
}

// captureQAEvidence persists a run_qa verdict comment as a durable qa_evidence
// row so the issue's QA section + the QA cockpit read one indexed row instead of
// re-parsing the timeline. Best-effort + detached: a miss (no block, malformed
// JSON, DB error) silently no-ops — a verdict comment never fails because of it.
// Only structured ```qa-result``` verdicts are captured; free-form comments stay
// in the timeline and the QA section prompts a re-run.
// parseQAResultBlock extracts + validates the ```qa-result``` block from a
// comment. Mirrors the frontend parseQAResultBlock: returns the verbatim JSON +
// parsed payload, or ok=false on no block / malformed JSON / invalid verdict.
func parseQAResultBlock(content string) (raw string, p qaResultPayload, ok bool) {
	m := qaResultBlockRe.FindStringSubmatch(content)
	if m == nil {
		return "", qaResultPayload{}, false
	}
	raw = strings.TrimSpace(m[1])
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", qaResultPayload{}, false
	}
	if p.Verdict != "pass" && p.Verdict != "fail" {
		return "", qaResultPayload{}, false
	}
	return raw, p, true
}

// CaptureQAEvidence is exported so the HTTP comment handler can call it too:
// real agents (daemon/CLI) post their verdict via POST /comments, not the
// internal createAgentComment path.
// CaptureQAEvidence persists a qa-result block and, deterministically, attaches
// the qa:pass / qa:fail LABEL the whole gate machinery keys on. Returns the
// verdict ("pass"/"fail"/"") and whether it NEWLY attached the label — the
// handler caller fires the merge-gate / autoroute triggers only on a new attach
// (so an agent that ALSO set the label via CLI does not double-fire them).
//
// Why the server attaches the label: the run_qa agent is instructed to set
// qa:pass/qa:fail itself, but observed live (SD-588 stress test) writing a
// "QA Verdict: PASS" comment + a valid qa-result verdict WITHOUT running the
// label CLI — so the loop stalled (no label → no merge-gate → never done).
// Deriving the label from the captured verdict makes the gate reliable
// regardless of whether the agent remembered the label step.
//
// Label-first ordering (audit finding — "label-attach failure diverges from
// evidence"): the LABEL is attached BEFORE the qa_evidence row is persisted,
// and a label-attach failure aborts the whole capture (no evidence written).
// The old order wrote evidence first and merely warned on a label failure, so
// the qa_evidence row could carry a fresh "pass" while the qa:pass label never
// landed — the chip (reads evidence.verdict, qa-lens.tsx) would show green
// while the merge gate (reads the LABEL, slice_action.go
// enforceQAGateBeforeDone) stayed blocked. Attaching first means the two
// surfaces can never disagree: either both the label and the evidence are
// live, or NEITHER is — a re-run picks it back up (least invasive fix that
// still guarantees agreement, chosen over a cross-table transaction since
// UpsertQAEvidence and AttachLabelToIssue are separate tables with their own
// upsert/idempotency semantics).
func (s *TaskService) CaptureQAEvidence(ctx context.Context, issue db.Issue, content string) (verdict string, newlyLabeled bool) {
	raw, p, ok := parseQAResultBlock(content)
	if !ok {
		return "", false
	}

	v := strings.ToLower(strings.TrimSpace(p.Verdict))

	// Evidence floor (audit requirement): a "pass" that carries ZERO commands
	// asserted nothing was actually verified. downgradeQAVerdictToStale
	// attaches qa:stale + an explanatory comment INSTEAD of applying qa:pass —
	// the qa_evidence row is deliberately NOT written, so an under-evidenced
	// "pass" never sits in the evidence table reading as green.
	if v == "pass" {
		if reason := s.qaEvidenceFloorGap(ctx, issue, p); reason != "" {
			s.downgradeQAVerdictToStale(ctx, issue, reason)
			return "", false
		}
	}

	label, color := "", ""
	switch v {
	case "pass":
		label, color = "qa:pass", "#22c55e"
	case "fail":
		label, color = "qa:fail", "#ef4444"
	}

	// A verdict with no gate label (e.g. "maybe"/"blocked") has nothing for the
	// merge gate to disagree with — persist evidence unconditionally, same as
	// before.
	if label == "" {
		if err := s.upsertQAEvidenceRow(ctx, issue, raw, p); err != nil {
			return "", false
		}
		s.captureDesignVerdictLabel(ctx, issue, raw)
		return v, false
	}

	newlyLabeled = false
	if s.issueHasLabelName(ctx, issue, label) {
		// Agent already set it (e.g. via CLI) → the label handler already fired
		// triggers for it. The label is confirmed present, so evidence can be
		// persisted safely below; just don't report a NEW attach.
	} else {
		labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, label, color)
		if err != nil {
			slog.Warn("capture qa evidence: ensure label failed — evidence NOT recorded (would disagree with the missing label)",
				"error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
			return "", false
		}
		if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("capture qa evidence: attach label failed — evidence NOT recorded (would disagree with the missing label)",
				"error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
			return "", false
		}
		newlyLabeled = true
	}

	if err := s.upsertQAEvidenceRow(ctx, issue, raw, p); err != nil {
		// The label is already live at this point; leaving it there would swap
		// the divergence direction (label present, no evidence) instead of
		// closing it. If WE just attached it fresh this call, undo that attach
		// so the two surfaces still agree (both absent); if the label was
		// already present from an EARLIER successful capture, leave it — that
		// earlier capture's own evidence row still stands and is unaffected by
		// this call's failure.
		if newlyLabeled {
			s.DetachIssueLabelByName(ctx, issue, label)
		}
		return "", false
	}

	// The design-compare check (sliceActionDesignCompareContext, design_action.go)
	// nests its own advisory verdict at result_json.design.verdict inside this
	// SAME raw block. Mirror it into a design:pass/design:fail label the moment
	// it's captured — independent of the top-level qa verdict above.
	s.captureDesignVerdictLabel(ctx, issue, raw)

	// A verdict REPLACES the previous one — detach the opposite gate label.
	// Without this a fixed-and-re-passed issue carried BOTH labels forever,
	// and every fail-wins surface (cockpit lane, merge gate, sprint rollup)
	// kept reporting it as "need fix" (the audit's sticky-label defect).
	opposite := "qa:fail"
	if label == "qa:fail" {
		opposite = "qa:pass"
	}
	s.DetachIssueLabelByName(ctx, issue, opposite)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("qa evidence: auto-attached gate label from verdict", "issue_id", util.UUIDToString(issue.ID), "label", label)
	return v, newlyLabeled
}

// upsertQAEvidenceRow writes the qa_evidence row and publishes the ready
// event. Split out of CaptureQAEvidence so the label-first ordering above can
// call it from both branches (labeled and label-less verdicts) without
// duplicating the upsert + publish + log.
func (s *TaskService) upsertQAEvidenceRow(ctx context.Context, issue db.Issue, raw string, p qaResultPayload) error {
	if _, err := s.Queries.UpsertQAEvidence(ctx, db.UpsertQAEvidenceParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		BaselineRef: "",
		BranchSha:   "",
		Verdict:     p.Verdict,
		Summary:     p.Summary,
		ResultJson:  []byte(raw),
		Source:      "agent",
	}); err != nil {
		slog.Warn("capture qa evidence: upsert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return err
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventQAEvidenceReady,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload: map[string]any{
			"issue_id": util.UUIDToString(issue.ID),
			"verdict":  p.Verdict,
		},
	})
	slog.Info("qa evidence captured", "issue_id", util.UUIDToString(issue.ID), "verdict", p.Verdict)
	return nil
}

// qaEvidenceFloorGap returns a human-readable reason when a "pass" verdict's
// evidence does not clear the floor, or "" when it does. Two independent
// checks:
//  1. ZERO commands — the verdict asserted "pass" without a single command
//     result to back it. Always required, regardless of the issue's cases.
//  2. For an issue with at least one UI-modality test case: some VISUAL
//     evidence — a screenshot in the verdict, or a captured Playwright trace
//     on one of the issue's automated runs. Command exit codes alone don't
//     prove a UI case was actually driven and checked.
// Best-effort: a query error on the (2) check is treated as "no gap" (fail
// open on the SECOND, stricter check only — the first, unconditional zero-
// commands check never depends on a query and always applies).
func (s *TaskService) qaEvidenceFloorGap(ctx context.Context, issue db.Issue, p qaResultPayload) string {
	if len(p.Commands) == 0 {
		return "the verdict carried zero commands — nothing ran to prove it"
	}
	if len(p.Screenshots) > 0 {
		return ""
	}
	cases, err := s.Queries.ListTestCasesForIssue(ctx, db.ListTestCasesForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return ""
	}
	hasUICase := false
	for _, c := range cases {
		if c.Modality == "ui" {
			hasUICase = true
			break
		}
	}
	if !hasUICase {
		return ""
	}
	runs, err := s.Queries.ListLatestRunsForIssueCases(ctx, db.ListLatestRunsForIssueCasesParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return ""
	}
	for _, r := range runs {
		if strings.TrimSpace(r.TracePath) != "" {
			return ""
		}
	}
	return "this issue has a UI-modality test case but the verdict carried no screenshot and no run has a captured trace"
}

// downgradeQAVerdictToStale attaches qa:stale (replacing qa:pass/qa:fail, if
// either is present) and posts a loud system comment explaining why the
// verdict was not applied, instead of accepting an under-evidenced "pass".
// Mirrors EscalateStaleQAGate's framing (qa:stale = "the gate didn't produce
// a trustworthy result", not a test failure) so this reads consistently with
// the watchdog's own stale escalation. The qa_evidence row is intentionally
// NEVER written here — an insufficiently-evidenced "pass" must not sit in the
// evidence table at all, or the chip would still show a stale-but-green
// summary.
func (s *TaskService) downgradeQAVerdictToStale(ctx context.Context, issue db.Issue, reason string) {
	labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, "qa:stale", "#f59e0b")
	if err != nil {
		slog.Warn("qa evidence floor: ensure qa:stale label failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("qa evidence floor: attach qa:stale failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	s.DetachIssueLabelByName(ctx, issue, "qa:pass")
	s.DetachIssueLabelByName(ctx, issue, "qa:fail")
	note := "⚠️ QA reported a \"pass\" verdict, but " + reason + " — insufficient evidence — verdict not applied. " +
		"Marking qa:stale (not qa:fail — this is a missing-evidence problem, not a proven test failure); re-run QA with real evidence."
	if _, cerr := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     note,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	}); cerr != nil {
		slog.Warn("qa evidence floor: system comment failed", "error", cerr, "issue_id", util.UUIDToString(issue.ID))
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Warn("qa evidence floor: pass verdict downgraded to qa:stale — insufficient evidence",
		"issue_id", util.UUIDToString(issue.ID), "reason", reason)
}

// designVerdictFromResult extracts result_json.design.verdict from a raw
// qa-result JSON block. Mirrors designVerdictOf (design_action.go) — a small,
// intentional duplicate rather than a shared package: handler already imports
// service (Handler.TaskService), so service importing handler back would be a
// cycle. The design-compare appendix embeds its verdict in the SAME qa-result
// JSON blob CaptureQAEvidence already parses above, so this reads that raw
// text directly instead of adding a new capture path.
func designVerdictFromResult(raw string) string {
	if raw == "" {
		return ""
	}
	var r struct {
		Design *struct {
			Verdict string `json:"verdict"`
		} `json:"design"`
	}
	if json.Unmarshal([]byte(raw), &r) != nil || r.Design == nil {
		return ""
	}
	return r.Design.Verdict
}

// captureDesignVerdictLabel mirrors the qa:pass/qa:fail attach above for the
// ADVISORY design-compare verdict nested at result_json.design.verdict (see
// sliceActionDesignCompareContext, design_action.go). Same replace-on-write
// semantics: attaching one detaches the other, the label is auto-created per
// workspace if missing. "skipped" (Figma unreachable) and "" (no
// design-compare ran, e.g. the issue has no Figma refs) touch nothing — never
// fail an issue for an infra reason, per the recipe's own doctrine.
func (s *TaskService) captureDesignVerdictLabel(ctx context.Context, issue db.Issue, raw string) {
	v := strings.ToLower(strings.TrimSpace(designVerdictFromResult(raw)))
	label, color := "", ""
	switch v {
	case "pass":
		label, color = "design:pass", "#22c55e"
	case "fail":
		label, color = "design:fail", "#ef4444"
	default:
		return
	}
	if s.issueHasLabelName(ctx, issue, label) {
		return // already set → nothing new to do
	}
	labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, label, color)
	if err != nil {
		slog.Warn("capture design verdict: ensure label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("capture design verdict: attach label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	// A verdict REPLACES the previous one — same "opposite gate label" rule
	// CaptureQAEvidence enforces for qa:pass/qa:fail above.
	opposite := "design:fail"
	if label == "design:fail" {
		opposite = "design:pass"
	}
	s.DetachIssueLabelByName(ctx, issue, opposite)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("qa evidence: auto-attached design gate label from verdict", "issue_id", util.UUIDToString(issue.ID), "label", label)
}

// DetachIssueLabelByName removes a label (matched case-insensitively by name)
// from an issue. Best-effort — a miss or error is a no-op. Exported so the
// label handler can reuse it for the qa:pass/qa:fail replace semantics.
func (s *TaskService) DetachIssueLabelByName(ctx context.Context, issue db.Issue, name string) {
	labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return
	}
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l.Name), name) {
			if err := s.Queries.DetachLabelFromIssue(ctx, db.DetachLabelFromIssueParams{
				IssueID: issue.ID, LabelID: l.ID, WorkspaceID: issue.WorkspaceID,
			}); err != nil {
				slog.Warn("detach label by name failed", "error", err, "label", name, "issue_id", util.UUIDToString(issue.ID))
			}
			return
		}
	}
}

// issueHasLabelName reports whether the issue already carries a label by name.
func (s *TaskService) issueHasLabelName(ctx context.Context, issue db.Issue, name string) bool {
	labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return false
	}
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l.Name), name) {
			return true
		}
	}
	return false
}
