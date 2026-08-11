package service

import "strings"

const (
	AgentRunModeAuto  = "auto"
	AgentRunModeDebug = "debug"
	AgentRunModePlan  = "plan"
	AgentRunModeBuild = "build"
)

// NormalizeAgentRunMode validates the per-task execution override accepted by
// human run controls. Empty is the backwards-compatible Auto default.
func NormalizeAgentRunMode(raw string) (string, bool) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return AgentRunModeAuto, true
	}
	switch mode {
	case AgentRunModeAuto, AgentRunModeDebug, AgentRunModePlan, AgentRunModeBuild:
		return mode, true
	default:
		return "", false
	}
}
