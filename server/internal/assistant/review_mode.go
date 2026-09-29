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
)

var ErrReviewReadOnly = errors.New("review mode is read-only; workspace changes and confirmation requests are not allowed")

// ResolveRunMode never lets a client downgrade an explicit /review request.
// Plain-language requests use the mode selected in the composer; natural
// language is not a reliable security boundary.
func ResolveRunMode(mode RunMode, content string) (RunMode, error) {
	if mode != "" && mode != RunModeAssist && mode != RunModeReview {
		return "", errors.New("mode must be assist or review")
	}
	words := strings.Fields(content)
	if len(words) > 0 && strings.EqualFold(words[0], "/review") {
		return RunModeReview, nil
	}
	if mode == "" {
		return RunModeAssist, nil
	}
	return mode, nil
}

type runModeKey struct{}

func WithRunMode(ctx context.Context, mode RunMode) context.Context {
	return context.WithValue(ctx, runModeKey{}, mode)
}

func RunModeFrom(ctx context.Context) RunMode {
	mode, _ := ctx.Value(runModeKey{}).(RunMode)
	if mode == "" {
		return RunModeAssist
	}
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
		return IsZohoTool(name)
	}
}

func ToolsForRunMode(tools []llm.Tool, mode RunMode) []llm.Tool {
	if mode == RunModeAssist {
		return tools
	}
	out := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if ReviewToolAllowed(tool.Name) {
			out = append(out, tool)
		}
	}
	return out
}

func CheckRunTool(ctx context.Context, name string) error {
	if RunModeFrom(ctx) != RunModeAssist && !ReviewToolAllowed(name) {
		return ErrReviewReadOnly
	}
	return nil
}
