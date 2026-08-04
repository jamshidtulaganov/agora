package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
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
// durable, append-only deploy_event row — the single writer of that table — so
// the stepper's Deploy stage and the Deploy lens read pipeline deploys from ONE
// place. Exported for the same reason CaptureQAEvidence is: real agents
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

	s.publishDeployEvents(ctx, issue, ref, target, p.Status)
}

// publishDeployEvents fans the just-persisted deploy_event onto the bus for the
// release-integrations dispatcher. Always publishes deploy:recorded; ADDS
// release:shipped when the deploy SUCCEEDED to a production-tier environment.
//
// "Shipped" heuristic (documented per release-hub-and-redesign.md B1, since the
// merge is a human action with no single unambiguous seam): status=="success"
// AND the deployed environment key resolves to a requires_human / production-
// named entry in the issue's project's deploy_environments. A deploy to a
// non-production environment (e.g. staging) never matches — so it only ever
// emits deploy:recorded.
func (s *TaskService) publishDeployEvents(ctx context.Context, issue db.Issue, ref, target, status string) {
	if s.Bus == nil {
		return
	}
	// Resolve the issue's sprint once (optional) so connectors can group by it.
	sprintID, branch, hasSprint := "", "", false
	if sprint, err := s.Queries.GetSprintForIssue(ctx, issue.ID); err == nil {
		hasSprint = true
		sprintID = util.UUIDToString(sprint.ID)
		branch = strings.TrimSpace(sprint.Branch)
	}

	s.Bus.Publish(events.Event{
		Type:        protocol.EventDeployRecorded,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		Payload: map[string]any{
			"issue_id":  util.UUIDToString(issue.ID),
			"ref":       ref,
			"target":    target,
			"status":    status,
			"sprint_id": sprintID,
		},
	})

	if status != "success" || !s.deployTargetIsProductionTier(ctx, issue, target) {
		return
	}
	if branch == "" {
		branch = ref
	}
	// issue_ids: the sprint's shipped issues when the deploy belongs to a
	// sprint, else just the triggering issue. The dispatcher rebuilds the full
	// changelog from sprint_id — this is the routing/grouping list.
	issueIDs := []string{util.UUIDToString(issue.ID)}
	if hasSprint {
		if changelog, err := s.BuildSprintChangelog(ctx, mustSprintUUID(sprintID), issue.WorkspaceID); err == nil && len(changelog) > 0 {
			issueIDs = issueIDs[:0]
			for _, e := range changelog {
				issueIDs = append(issueIDs, e.ID)
			}
		}
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventReleaseShipped,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		Payload: map[string]any{
			"sprint_id":   sprintID,
			"project_id":  util.UUIDToString(issue.ProjectID),
			"branch":      branch,
			"environment": target,
			"issue_ids":   issueIDs,
		},
	})
}

// mustSprintUUID re-parses a sprint id string we just serialized from a valid
// pgtype.UUID — a trusted round-trip, so a parse failure is a programmer error
// (returns the zero UUID, which BuildSprintChangelog treats as an empty sprint).
func mustSprintUUID(s string) pgtype.UUID {
	id, _ := util.ParseUUID(s)
	return id
}

// deployTargetIsProductionTier reports whether the deployed environment key is
// a production-tier target of the issue's project. Best-effort: no project, no
// settings, or a malformed blob returns false (no release:shipped fired).
func (s *TaskService) deployTargetIsProductionTier(ctx context.Context, issue db.Issue, envKey string) bool {
	if !issue.ProjectID.Valid {
		return false
	}
	project, err := s.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return false
	}
	return deployEnvIsProductionTier(project.Settings, envKey)
}

// deployEnvIsProductionTier parses project.settings.deploy_environments and
// reports whether the entry keyed envKey is human-gated / production. Mirrors
// the rule handler.deployEnvironmentRequiresHuman enforces (the requires_human
// flag OR a key literally named production/prod). It is duplicated rather than
// shared because the service package sits BELOW handler in the import graph and
// cannot import it; the rule is tiny and stable. Defensive: a non-array or
// malformed settings blob, or a missing entry, returns false.
func deployEnvIsProductionTier(settingsRaw []byte, envKey string) bool {
	key := strings.ToLower(strings.TrimSpace(envKey))
	if key == "" {
		return false
	}
	var settings struct {
		DeployEnvironments []struct {
			Key           string `json:"key"`
			RequiresHuman bool   `json:"requires_human"`
		} `json:"deploy_environments"`
	}
	if json.Unmarshal(settingsRaw, &settings) != nil {
		return false
	}
	for _, env := range settings.DeployEnvironments {
		if strings.ToLower(strings.TrimSpace(env.Key)) != key {
			continue
		}
		if env.RequiresHuman {
			return true
		}
		switch key {
		case "production", "prod":
			return true
		}
		return false
	}
	return false
}
