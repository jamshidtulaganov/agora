package handler

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Box actions beyond git-sync: a cheap connection test and an
// operator-allowlisted seed command. Both run over the same control-plane SSH
// channel as sync — the caller (human OR agent via its task token) only ever
// triggers a predefined action; arbitrary remote commands never cross the API.

// TestConnectedBox verifies the control plane can actually reach the box over
// SSH — the "did my connection work?" button. Runs a constant probe command
// (no user input reaches the shell) and reports the round-trip.
// POST /api/remote-boxes/{id}/test.
func (h *Handler) TestConnectedBox(w http.ResponseWriter, r *http.Request) {
	box, keyPath, ok := h.boxActionPrelude(w, r)
	if !ok {
		return
	}
	started := time.Now()
	out, err := sshRunner{}.Run(r.Context(), box.SshHost, box.SshUser, int(box.SshPort), keyPath,
		"echo AGORA_CONNECTION_OK && uname -n && whoami")
	latency := time.Since(started).Milliseconds()
	if err != nil || !strings.Contains(out, "AGORA_CONNECTION_OK") {
		msg := strings.TrimSpace(out)
		if err != nil {
			msg = strings.TrimSpace(msg + "\n" + err.Error())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "output": redactGitToken(msg), "latency_ms": latency,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "output": strings.TrimSpace(out), "latency_ms": latency,
	})
}

// boxSeedCommand is the operator-defined seed template
// (AGORA_QA_BOX_SEED_COMMAND). The API never accepts a command from the
// caller — seeding is a single predefined action, parameterized only by the
// box it targets: {work_dir} and {subdomain} placeholders are expanded from
// the box row. Empty = the feature is off and /seed answers 503.
// Example: /usr/local/bin/agora-box-seed {subdomain}
// Example: cd {work_dir} && php protected/yiic.php seed
func boxSeedCommand() string {
	return strings.TrimSpace(os.Getenv("AGORA_QA_BOX_SEED_COMMAND"))
}

// expandBoxSeedCommand fills the operator template for one box. The expanded
// values are shell-quoted: a hostile work_dir/subdomain stored in the DB must
// not become shell syntax inside the operator's command.
func expandBoxSeedCommand(tmpl string, box db.ConnectedBox) string {
	wd := strings.TrimRight(strings.TrimSpace(box.WorkDir), "/")
	sub := wd[strings.LastIndex(wd, "/")+1:]
	out := strings.ReplaceAll(tmpl, "{work_dir}", shellQuote(wd))
	out = strings.ReplaceAll(out, "{subdomain}", shellQuote(sub))
	return out
}

// SeedConnectedBox re-seeds the box's data by running the operator-allowlisted
// seed command on it (e.g. re-cloning the demo database). Callable by humans
// and by agents (a QA agent may need fresh fixtures before a run) — the actor
// can only pull this one trigger, never choose what runs.
// POST /api/remote-boxes/{id}/seed.
func (h *Handler) SeedConnectedBox(w http.ResponseWriter, r *http.Request) {
	box, keyPath, ok := h.boxActionPrelude(w, r)
	if !ok {
		return
	}
	tmpl := boxSeedCommand()
	if tmpl == "" {
		writeError(w, http.StatusServiceUnavailable,
			"box seeding is not configured on this server — set AGORA_QA_BOX_SEED_COMMAND (an operator-defined command template; {work_dir}/{subdomain} placeholders)")
		return
	}
	if strings.TrimSpace(box.WorkDir) == "" {
		writeError(w, http.StatusBadRequest, "this box has no work_dir configured")
		return
	}

	// Serialize with the box's own sync lock: a seed racing a git-sync on the
	// same box would interleave on the served directory/database.
	ctx := r.Context()
	if lockTx, err := h.TxStarter.Begin(ctx); err == nil {
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, _ = lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "connected_box:"+uuidToString(box.ID))
	}

	script := expandBoxSeedCommand(tmpl, box)
	out, err := seedBoxRun(ctx, box, keyPath, script)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "output": redactGitToken(strings.TrimSpace(out + "\n" + err.Error())),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "output": redactGitToken(strings.TrimSpace(out)),
	})
}

// seedBoxRun executes the expanded seed script on the box. Split out so tests
// can cover expansion/quoting without a live SSH target.
func seedBoxRun(ctx context.Context, box db.ConnectedBox, keyPath, script string) (string, error) {
	return sshRunner{}.Run(ctx, box.SshHost, box.SshUser, int(box.SshPort), keyPath, script)
}

// boxActionPrelude is the shared gate for box actions: feature flag, session,
// workspace membership (via the box's own workspace), the box row, and the
// control-plane key. Mirrors SyncConnectedBox's checks.
func (h *Handler) boxActionPrelude(w http.ResponseWriter, r *http.Request) (db.ConnectedBox, string, bool) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return db.ConnectedBox{}, "", false
	}
	if _, ok := requireUserID(w, r); !ok {
		return db.ConnectedBox{}, "", false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.ConnectedBox{}, "", false
	}
	boxUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "box id")
	if !ok {
		return db.ConnectedBox{}, "", false
	}
	box, err := h.Queries.GetConnectedBox(r.Context(), db.GetConnectedBoxParams{ID: boxUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "remote box not found")
		return db.ConnectedBox{}, "", false
	}
	keyPath := remoteBoxesSSHKeyPath()
	if keyPath == "" {
		writeError(w, http.StatusServiceUnavailable, "remote box SSH key is not configured on the server")
		return db.ConnectedBox{}, "", false
	}
	return box, keyPath, true
}
