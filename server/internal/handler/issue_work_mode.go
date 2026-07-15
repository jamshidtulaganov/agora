package handler

import "strings"

// agentContextNote formats human-authored, per-issue guidance for injection
// into every agent run. Historical issues may already carry this metadata even
// though the retired embedded-editor control no longer edits it.
func agentContextNote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "\n\n## Context from the human (applies to this issue)\n" +
		"Treat the following as authoritative guidance for this task — rules, files to focus on, links, and constraints the human set:\n\n" +
		raw
}
