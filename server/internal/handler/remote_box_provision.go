package handler

import (
	"fmt"
	"regexp"
	"strings"
)

// Per-developer QA box provisioning (v1, Yii/PHP stack). A developer's box is a
// wildcard subdomain `<handle>.<baseDomain>` carved out of the SHARED QA host:
// Agora SSHes the host and runs an IDEMPOTENT, NON-DESTRUCTIVE runbook that
// checks the project repo out into the served dir, copies the gitignored deploy
// glue (incl. the DB config) from a known-good seed site, links the shared Yii
// framework, and makes the runtime dirs writable. Re-running only fills what is
// MISSING — it never drops or overwrites an existing dev's checkout.
//
// It does NOT touch databases: the box INHERITS the seed's DB config verbatim
// ("keep each box's existing DB"), because this team points/shares DBs by hand
// rather than isolating one per dev. So the runbook runs no mysql at all — no
// password is needed on the box, and nothing destructive can happen to data.
// The script is PURE-built + unit-tested here; the SSH transport reuses
// remote_box_sync.go's runner, token injection, and redaction.

// handleRe is the allowed shape for a developer handle once sanitized: it becomes
// a DNS subdomain label AND a filesystem path segment, so it must be the safe
// intersection — lowercase alphanumerics + internal hyphens, 1..40 chars, no
// leading/trailing hyphen.
var handleRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// sanitizeHandle lowercases + slugs a raw handle (or an email/local-part used as
// the default) into a valid box handle, or "" if nothing valid remains. Every
// run of non-[a-z0-9] collapses to a single hyphen; leading/trailing hyphens are
// trimmed; the result is length-capped and re-validated against handleRe. This is
// the security boundary for the subdomain/path — a hostile member name can never
// escape it (no `.`, `/`, `;`, quote, or space survives).
func sanitizeHandle(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if at := strings.IndexByte(s, '@'); at >= 0 { // tolerate a full email — take the local part
		s = s[:at]
	}
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s = strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if !handleRe.MatchString(s) {
		return ""
	}
	return s
}

// provisionParams is the resolved, non-secret input to buildProvisionScript.
type provisionParams struct {
	Handle     string
	BaseDomain string
	WebRoot    string
	RepoURL    string
	SeedDir    string
}

// boxSubdomain / boxWorkDir derive a handle's placement on the QA host.
func boxSubdomain(p provisionParams) string { return p.Handle + "." + p.BaseDomain }
func boxWorkDir(p provisionParams) string {
	return strings.TrimRight(p.WebRoot, "/") + "/" + boxSubdomain(p)
}

// buildProvisionScript renders the box-side /bin/sh runbook. The git token is
// injected into the clone URL ONLY (ephemeral; redacted before the output is
// logged/stored), exactly like buildGitSyncScript. Every interpolated value is
// shellQuote'd, so a handle/seed carrying a shell metacharacter cannot break out
// of its argument. IDEMPOTENT + NON-DESTRUCTIVE by construction: each step guards
// on absence (`[ ! -d .git ]`, `[ ! -f ]`, `[ -e framework ]`) so a re-run on an
// existing box is a safe no-op. It runs NO database commands — the box inherits
// the seed's DB config as copied.
func buildProvisionScript(p provisionParams, token string) string {
	dirQ := shellQuote(boxWorkDir(p))
	authed := shellQuote(authedRepoURL(p.RepoURL, token))
	seedQ := shellQuote(p.SeedDir)

	return strings.Join([]string{
		"set -e",
		// 1. checkout the repo into the served dir — first run only; a re-run never
		//    re-clones over a dev's working copy.
		fmt.Sprintf("if [ ! -d %s/.git ]; then mkdir -p %s && git clone --depth 1 %s %s; fi", dirQ, dirQ, authed, dirQ),
		fmt.Sprintf("cd %s", dirQ),
		// 2. gitignored deploy glue (incl. the DB config) + the shared Yii framework,
		//    copied from the seed site ONLY when absent here AND present there. The box
		//    inherits the seed's DB config verbatim — no DB is created, cloned, or
		//    renamed ("keep each box's existing DB"). A seed that lacks an optional file
		//    (e.g. no db.php) is skipped, never an abort.
		fmt.Sprintf("for f in index.php protected/config/main.php protected/config/db.php; do "+
			"if [ ! -f \"$f\" ] && [ -f %s/\"$f\" ]; then mkdir -p \"$(dirname \"$f\")\" && cp %s/\"$f\" \"$f\"; fi; done", seedQ, seedQ),
		fmt.Sprintf("[ -e framework ] || cp -a %s/framework framework 2>/dev/null || true", seedQ),
		// 3. writable runtime dirs (relative to the work dir we cd'd into).
		"mkdir -p protected/runtime assets && chmod -R 0777 protected/runtime assets",
	}, " && ")
}
