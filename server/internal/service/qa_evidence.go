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
func (s *TaskService) CaptureQAEvidence(ctx context.Context, issue db.Issue, content string) {
	raw, p, ok := parseQAResultBlock(content)
	if !ok {
		return
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
		return
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
}
