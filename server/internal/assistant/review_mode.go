package assistant

import (
	"context"
	"errors"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

type RunMode string

const (
	RunModeAssist RunMode = "assist"
	RunModeReview RunMode = "review"
	// RunModeRoom is a turn whose answer is posted to a shared room (a
	// Telegram group). What the model reads there is written by other people
	// and what it says is read by all of them, so the turn is read-only, sees
	// one workspace, and carries no personal sources. See RoomToolAllowed.
	RunModeRoom RunMode = "room"
)

var ErrReviewReadOnly = errors.New("review mode is read-only; workspace changes and confirmation requests are not allowed")

// ResolveRunMode never lets a client downgrade an explicit /review request.
// Plain-language requests use the mode selected in the composer; natural
// language is not a reliable security boundary.
func ResolveRunMode(mode RunMode, content string) (RunMode, error) {
	if mode != "" && mode != RunModeAssist && mode != RunModeReview && mode != RunModeRoom {
		return "", errors.New("mode must be assist or review")
	}
	// A room turn is never widened by its own text.
	if mode == RunModeRoom {
		return RunModeRoom, nil
	}
	words := strings.Fields(content)
	if len(words) > 0 && strings.EqualFold(words[0], "/review") {
		return RunModeReview, nil
	}
	if mode == "" { return RunModeAssist, nil }
	return mode, nil
}

type runModeKey struct{}

func WithRunMode(ctx context.Context, mode RunMode) context.Context {
	return context.WithValue(ctx, runModeKey{}, mode)
}

func RunModeFrom(ctx context.Context) RunMode {
	mode, _ := ctx.Value(runModeKey{}).(RunMode)
	if mode == "" { return RunModeAssist }
	return mode
}

// ReviewToolAllowed is an explicit, fail-closed allowlist. Artifacts are
// private review outputs, not workspace edits. Import previews are excluded:
// despite their name, they persist jobs that can later be confirmed.
func ReviewToolAllowed(name string) bool {
	switch name {
	case ToolListWorkspaces, ToolListMyIssues, ToolListIssues, ToolSearchIssues,
		ToolGetIssue, ToolListComments, ToolListProjects, ToolGetProject,
		ToolListSprints, ToolListLabels, ToolListAgents, ToolListSquads,
		ToolListMembers, ToolListRuntimes, ToolListSkills, ToolListAutopilots,
		ToolListAutomations, ToolListIntegrations, ToolListImportConnections,
		ToolImportStatus, ToolGetMySettings, ToolUsageSummary, ToolActivityDigest,
		ToolInboxSummary, ToolQAStatus, ToolListStaleIssues,
		ToolSearchKnowledge, ToolReadKnowledge, ToolListKnowledge,
		ToolCreateArtifact, ToolUpdateArtifact:
		return true
	default:
		return IsZohoTool(name) || IsTelegramTool(name)
	}
}

// RoomToolAllowed is the explicit, fail-closed allowlist for a room turn:
// reads of ONE workspace's shared work, its knowledge base, and the room's own
// history. Nothing personal (the user's own task list, inbox, settings, Zoho),
// nothing that spans workspaces, and no artifacts. The handler layer pins
// every one of these to the run's workspace.
func RoomToolAllowed(name string) bool {
	switch name {
	case ToolListIssues, ToolSearchIssues, ToolGetIssue, ToolListComments,
		ToolListProjects, ToolGetProject, ToolListSprints, ToolListLabels,
		ToolListMembers, ToolListStaleIssues, ToolActivityDigest, ToolQAStatus,
		ToolSearchKnowledge, ToolReadKnowledge, ToolListKnowledge,
		ToolReadTelegramChat:
		return true
	}
	return false
}

func modeAllows(mode RunMode, name string) bool {
	switch mode {
	case RunModeAssist:
		return true
	case RunModeRoom:
		return RoomToolAllowed(name)
	default:
		return ReviewToolAllowed(name)
	}
}

func ToolsForRunMode(tools []llm.Tool, mode RunMode) []llm.Tool {
	if mode == RunModeAssist { return tools }
	out := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if modeAllows(mode, tool.Name) { out = append(out, tool) }
	}
	return out
}

func CheckRunTool(ctx context.Context, name string) error {
	if !modeAllows(RunModeFrom(ctx), name) {
		return ErrReviewReadOnly
	}
	return nil
}
