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
// glue from a known-good seed site, isolates the box onto its own database
// (`dbt_<handle>` cloned from the seed DB), and makes the runtime dirs writable.
// Re-running only fills what is MISSING — it never drops or overwrites an
// existing dev's checkout or DB. The script is PURE-built + unit-tested here; the
// SSH transport reuses remote_box_sync.go's runner, token injection, and
// redaction. The runbook is product-specific (Yii) for v1; generalizing is later.

// handleRe is the allowed shape for a developer handle once sanitized: it becomes
// a DNS subdomain label, a filesystem path segment, AND (with `-`→`_`) a MySQL
// database name, so it must be the safe intersection of all three — lowercase
// alphanumerics + internal hyphens, 1..40 chars, no leading/trailing hyphen.
var handleRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// sanitizeHandle lowercases + slugs a raw handle (or an email/local-part used as
// the default) into a valid box handle, or "" if nothing valid remains. Every
// run of non-[a-z0-9] collapses to a single hyphen; leading/trailing hyphens are
// trimmed; the result is length-capped and re-validated against handleRe. This is
// the security boundary for the subdomain/path/DB name — a hostile member name
// can never escape it (no `.`, `/`, `;`, quote, or space survives).
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

// handleDBName maps a handle to its MySQL database identifier. Handles allow `-`
// (valid in DNS + paths) but an unquoted `-` is a MySQL syntax error, so it is
// mapped to `_`. Result is always `dbt_[a-z0-9_]+`.
func handleDBName(handle string) string {
	return "dbt_" + strings.ReplaceAll(handle, "-", "_")
}

// provisionParams is the resolved, non-secret input to buildProvisionScript.
type provisionParams struct {
	Handle     string
	BaseDomain string
	WebRoot    string
	RepoURL    string
	SeedDir    string
	SeedDB     string
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
// on absence (`[ ! -d .git ]`, `[ -e ]`, `CREATE DATABASE IF NOT EXISTS`, an
// empty-schema check before seeding) so a re-run on an existing box is a safe
// no-op rather than a wipe.
func buildProvisionScript(p provisionParams, token string) string {
	work := boxWorkDir(p)
	db := handleDBName(p.Handle)

	dirQ := shellQuote(work)
	authed := shellQuote(authedRepoURL(p.RepoURL, token))
	seedQ := shellQuote(p.SeedDir)
	seedDBQ := shellQuote(p.SeedDB)
	dbBareQ := shellQuote(db)
	// Word-boundary (\b) sed so renaming the seed DB never partial-matches a
	// sibling (the seedDB "dbt_agora" must NOT also rewrite "dbt_agora_cs" — the
	// `_` after the name is a word char, so \b after the name does not match it).
	sedProgQ := shellQuote(fmt.Sprintf(`s/\b%s\b/%s/g`, p.SeedDB, db))
	createQ := shellQuote(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", db))
	showQ := shellQuote(fmt.Sprintf("SHOW TABLES FROM `%s`", db))

	return strings.Join([]string{
		"set -e",
		// 1. checkout the repo into the served dir — first run only; a re-run never
		//    re-clones over a dev's working copy.
		fmt.Sprintf("if [ ! -d %s/.git ]; then mkdir -p %s && git clone --depth 1 %s %s; fi", dirQ, dirQ, authed, dirQ),
		fmt.Sprintf("cd %s", dirQ),
		// 2. gitignored deploy glue + the (shared) Yii framework: copy from the seed
		//    site only when absent here AND the seed actually has it (some sites keep
		//    the DB config inside main.php and have no db.php — a missing seed file is
		//    skipped, never an abort).
		fmt.Sprintf("for f in index.php protected/config/main.php protected/config/db.php; do "+
			"if [ ! -f \"$f\" ] && [ -f %s/\"$f\" ]; then mkdir -p \"$(dirname \"$f\")\" && cp %s/\"$f\" \"$f\"; fi; done", seedQ, seedQ),
		fmt.Sprintf("[ -e framework ] || cp -a %s/framework framework 2>/dev/null || true", seedQ),
		// 3. isolate this box onto its OWN database name inside the copied config
		//    (seedDB -> dbt_<handle>), scoped to protected/config only.
		fmt.Sprintf("grep -rl %s protected/config 2>/dev/null | xargs -r sed -i %s || true", seedDBQ, sedProgQ),
		// 4. writable runtime dirs (relative to the work dir we cd'd into).
		"mkdir -p protected/runtime assets && chmod -R 0777 protected/runtime assets",
		// 5. database: create if missing, then seed ONLY when empty. Uses the box's
		//    own local mysql auth (~/.my.cnf) — Agora transmits no DB password.
		fmt.Sprintf("mysql -e %s", createQ),
		fmt.Sprintf("if [ -z \"$(mysql -N -e %s 2>/dev/null)\" ]; then mysqldump %s | mysql %s; fi", showQ, seedDBQ, dbBareQ),
	}, " && ")
}
