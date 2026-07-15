package handler

import (
	"os"
	"strings"
)

// daemonEditorBase is the browser-facing base of the local daemon runtime
// surface. The historical environment variable name remains supported for
// deployment compatibility; the endpoint now serves artifact preview, checks,
// QA browser, and trace capabilities rather than an embedded code editor.
func daemonEditorBase(port string) string {
	if v := strings.TrimSpace(os.Getenv("AGORA_DAEMON_EDITOR_URL")); v != "" {
		return v
	}
	if p := strings.TrimSpace(port); p != "" {
		return "http://127.0.0.1:" + p
	}
	return "http://127.0.0.1:19514"
}

// daemonInternalAddr is the daemon's private-network address. Empty means the
// browser and daemon share the self-hosted machine.
func daemonInternalAddr() string {
	return strings.TrimSpace(os.Getenv("AGORA_DAEMON_INTERNAL"))
}

// resolveDaemonInternalAddr prefers a runtime-specific Remote Box endpoint and
// otherwise preserves the process-wide private-daemon fallback.
func resolveDaemonInternalAddr(runtimeAddr string) string {
	if addr := strings.TrimSpace(runtimeAddr); addr != "" {
		return addr
	}
	return daemonInternalAddr()
}
