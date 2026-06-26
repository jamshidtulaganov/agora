package handler

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
)

// daemonEditorBase is the local daemon health server the browser calls to launch
// code-server (browser VS Code) for a task's worktree. Self-host: the daemon,
// the browser and code-server all run on the same host, so the daemon's
// 127.0.0.1:<health-port> is reachable from the browser directly. Defaults to
// the daemon DefaultHealthPort (19514); override per install with
// AGORA_DAEMON_EDITOR_URL. (Cloud/multi-tenant will proxy this via the backend.)
func daemonEditorBase() string {
	if v := strings.TrimSpace(os.Getenv("AGORA_DAEMON_EDITOR_URL")); v != "" {
		return v
	}
	return "http://127.0.0.1:19514"
}

// GetIssueEditor returns the on-disk workdir of an issue's most recent task plus
// the daemon base URL, so the browser can ask the daemon to launch a code-server
// (browser VS Code) pointed at that worktree and iframe it in the issue view.
func (h *Handler) GetIssueEditor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	var workdir string
	err := h.DB.QueryRow(r.Context(),
		`SELECT work_dir FROM agent_task_queue
		 WHERE issue_id = $1 AND work_dir IS NOT NULL AND work_dir <> ''
		 ORDER BY COALESCE(completed_at, started_at, created_at) DESC
		 LIMIT 1`, issue.ID).Scan(&workdir)
	if err != nil || strings.TrimSpace(workdir) == "" {
		writeError(w, http.StatusNotFound, "no worktree yet — assign an agent to this issue first")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"workdir":    workdir,
		"daemon_url": daemonEditorBase(),
		// Per-(user, worktree) editor isolation: the daemon keys the
		// code-server instance by user_id + workdir so two humans on the same
		// worktree each get their own VS Code window (no single-window block).
		"user_id": requestUserID(r),
	})
}
