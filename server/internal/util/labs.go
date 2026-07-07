package util

import (
	"encoding/json"
	"strings"
)

// WorkspaceLabs are the Settings → Labs experimental flags, stored under the
// `labs` key of workspace.settings. Parsed here (not in the handler package)
// because the task service reads them too: qa_dev_runtimes steers task
// runtime pinning at enqueue time.
type WorkspaceLabs struct {
	// QADevBoxes routes per-task QA to the assignee-developer's own connected
	// box (connected_box.owner_id match) before the project-bound box. Default
	// ON — matches the resolver's historical behavior.
	QADevBoxes bool `json:"qa_dev_boxes"`
	// QAFallbackBoxID names the shared box QA lands on when neither a per-dev
	// nor a project/repo match resolves. Empty = no fallback.
	QAFallbackBoxID string `json:"qa_fallback_box_id"`
	// QADevRuntimes routes QA tasks to the assignee-developer's own ONLINE
	// daemon when it declares a local app for the issue's project
	// (agent_runtime.metadata.dev_apps[project_id]) — "QA runs where the app
	// actually runs". Default OFF: strictly opt-in, never a cross-project or
	// cross-machine default.
	QADevRuntimes bool `json:"qa_dev_runtimes"`
	// QADevRuntimesStrict keeps a dev-pinned task waiting when the dev's
	// daemon goes offline instead of falling back to the shared runtime —
	// for teams where testing on the wrong environment is worse than waiting.
	QADevRuntimesStrict bool `json:"qa_dev_runtimes_strict"`
}

func DefaultWorkspaceLabs() WorkspaceLabs {
	return WorkspaceLabs{QADevBoxes: true}
}

// ParseWorkspaceLabs reads the labs block off a workspace settings blob,
// defaulting every absent field. Never errors — a malformed blob degrades to
// defaults (labs must never take task dispatch or QA resolution down).
func ParseWorkspaceLabs(settings []byte) WorkspaceLabs {
	labs := DefaultWorkspaceLabs()
	if len(settings) == 0 {
		return labs
	}
	var s struct {
		Labs *struct {
			QADevBoxes          *bool  `json:"qa_dev_boxes"`
			QAFallbackBoxID     string `json:"qa_fallback_box_id"`
			QADevRuntimes       *bool  `json:"qa_dev_runtimes"`
			QADevRuntimesStrict *bool  `json:"qa_dev_runtimes_strict"`
		} `json:"labs"`
	}
	if json.Unmarshal(settings, &s) != nil || s.Labs == nil {
		return labs
	}
	if s.Labs.QADevBoxes != nil {
		labs.QADevBoxes = *s.Labs.QADevBoxes
	}
	labs.QAFallbackBoxID = strings.TrimSpace(s.Labs.QAFallbackBoxID)
	if s.Labs.QADevRuntimes != nil {
		labs.QADevRuntimes = *s.Labs.QADevRuntimes
	}
	if s.Labs.QADevRuntimesStrict != nil {
		labs.QADevRuntimesStrict = *s.Labs.QADevRuntimesStrict
	}
	return labs
}

// DevAppURL extracts a runtime's declared local app URL for a project from
// its metadata `dev_apps` map (keyed by project UUID). "" when absent.
func DevAppURL(metadata []byte, projectID string) string {
	if len(metadata) == 0 || projectID == "" {
		return ""
	}
	var m struct {
		DevApps map[string]string `json:"dev_apps"`
	}
	if json.Unmarshal(metadata, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.DevApps[projectID])
}
