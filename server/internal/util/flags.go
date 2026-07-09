package util

import (
	"github.com/multica-ai/multica/server/internal/config"
)

// SprintPRModeEnabled reports whether sprint PR mode is on
// (AGORA_SPRINT_PR_MODE=1|true). ONE reader shared by the handler and the
// daemon — the two used to carry identical private copies of this function,
// which was a silent split-brain risk the moment one edited its parsing
// (audit P2). Resolves through the config store so a Settings → Configs
// override applies without a redeploy.
func SprintPRModeEnabled() bool {
	return config.Bool("AGORA_SPRINT_PR_MODE")
}
