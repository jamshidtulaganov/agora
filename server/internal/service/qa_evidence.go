package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

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
func (s *TaskService) CaptureQAEvidence(ctx context.Context, issue db.Issue, content string) (verdict string, newlyLabeled bool) {
	raw, p, ok := parseQAResultBlock(content)
	if !ok {
		return "", false
	}

	if _, err := s.Queries.UpsertQAEvidence(ctx, db.UpsertQAEvidenceParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		BaselineRef: "",
		BranchSha:   "",
		Verdict:     p.Verdict,
		Summary:     p.Summary,
		ResultJson:  []byte(raw),
	}); err != nil {
		slog.Warn("capture qa evidence: upsert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return "", false
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

	v := strings.ToLower(strings.TrimSpace(p.Verdict))
	label, color := "", ""
	switch v {
	case "pass":
		label, color = "qa:pass", "#22c55e"
	case "fail":
		label, color = "qa:fail", "#ef4444"
	default:
		return v, false // no verdict-derived gate label (e.g. "maybe"/"blocked")
	}
	if s.issueHasLabelName(ctx, issue, label) {
		return v, false // agent already set it → the label handler already fired triggers
	}
	labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, label, color)
	if err != nil {
		slog.Warn("capture qa evidence: ensure label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
		return v, false
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("capture qa evidence: attach label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
		return v, false
	}
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
	return v, true
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
