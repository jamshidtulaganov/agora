package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// deployResultBlockRe extracts the ```deploy-result``` fenced JSON a deploy
// slice-action's terminal comment carries (slice_action_templates/deploy.md).
// Same shape as qaResultBlockRe — the deploy write-back deliberately reuses
// the qa-result capture mechanism verbatim (deploy-mcp-integration.md §5):
// the agent posts a comment, the server parses the block, no new
// authenticated endpoint for the agent to call.
var deployResultBlockRe = regexp.MustCompile("(?s)```deploy-result\\s*\\n(.*?)```")

// deployResultPayload is the structured outcome the deploy agent emits.
// environment names the project's deploy_environments key it deployed
// (target is a tolerated alias); ref is the branch/SHA the server told it to
// deploy; status is the deterministic terminal outcome. pipeline_url /
// duration / failed_jobs stay in the raw block for humans — only the columns
// deploy_event persists are lifted here.
type deployResultPayload struct {
	Environment string `json:"environment"`
	Target      string `json:"target"`
	Ref         string `json:"ref"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	PipelineURL string `json:"pipeline_url"`
}

// parseDeployResultBlock extracts + validates the ```deploy-result``` block
// from a comment. ok=false on no block, malformed JSON, or an unknown status
// — the caller silently no-ops, so a chatty comment never breaks capture and
// a malformed block never crashes anything (it is logged and skipped).
func parseDeployResultBlock(content string) (p deployResultPayload, ok bool) {
	m := deployResultBlockRe.FindStringSubmatch(content)
	if m == nil {
		return deployResultPayload{}, false
	}
	raw := strings.TrimSpace(m[1])
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		slog.Warn("deploy-result block: malformed JSON, skipping", "error", err)
		return deployResultPayload{}, false
	}
	switch p.Status {
	case "success", "failed", "timeout":
	default:
		slog.Warn("deploy-result block: unknown status, skipping", "status", p.Status)
		return deployResultPayload{}, false
	}
	return p, true
}

// CaptureDeployEvent persists a deploy agent's ```deploy-result``` block as a
// durable deploy_event row — the same append-only signal the Tier-1 QA-box
// git-sync writes (recordDeployEvent, connected_box.go), so the stepper's
// Deploy stage and the Deploy lens read pipeline deploys and box syncs from
// ONE table. Exported for the same reason CaptureQAEvidence is: real agents
// (daemon/CLI) post their terminal comment via POST /comments, so the HTTP
// comment handler must be able to call it too.
//
// Best-effort + idempotent: a miss (no block, malformed JSON, unknown status,
// DB error) silently no-ops — a deploy comment never fails because of it. The
// same terminal content can legitimately flow through both the comment path
// and the final-result capture path (captureStructuredResult), so an insert
// identical to the issue's freshest row is skipped rather than duplicated.
func (s *TaskService) CaptureDeployEvent(ctx context.Context, issue db.Issue, content string) {
	p, ok := parseDeployResultBlock(content)
	if !ok {
		return
	}
	target := strings.TrimSpace(p.Environment)
	if target == "" {
		target = strings.TrimSpace(p.Target)
	}
	summary := strings.TrimSpace(p.Summary)
	if summary == "" {
		summary = strings.TrimSpace(p.PipelineURL)
	}
	if len(summary) > 500 {
		summary = summary[:500]
	}
	ref := strings.TrimSpace(p.Ref)

	if latest, err := s.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err == nil &&
		latest.Ref == ref && latest.Target == target &&
		latest.Status == p.Status && latest.Summary == summary {
		return // the same terminal block was already captured on another path
	}

	if _, err := s.Queries.InsertDeployEvent(ctx, db.InsertDeployEventParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		Ref:         ref,
		Target:      target,
		Status:      p.Status,
		Summary:     summary,
	}); err != nil {
		slog.Warn("capture deploy event: insert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	slog.Info("deploy event captured from deploy-result",
		"issue_id", util.UUIDToString(issue.ID), "target", target, "status", p.Status)
}
