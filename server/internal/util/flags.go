package util

import (
	"os"
	"strings"
)

// SprintPRModeEnabled reports whether sprint PR mode is on
// (AGORA_SPRINT_PR_MODE=1|true). ONE reader shared by the handler and the
// daemon — the two used to carry identical private copies of this function,
// which was a silent split-brain risk the moment one edited its parsing
// (audit P2).
func SprintPRModeEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGORA_SPRINT_PR_MODE"))
	return v == "1" || strings.EqualFold(v, "true")
}
