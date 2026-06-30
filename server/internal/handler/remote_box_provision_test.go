package handler

import (
	"strings"
	"testing"
)

// TestSanitizeHandle is the security boundary test: a handle becomes a DNS
// subdomain, a filesystem path segment, and a DB name, so a hostile member name
// must never escape into any of those. Path traversal, shell metacharacters, and
// SQL fragments must all reduce to a safe `[a-z0-9-]` slug or be rejected.
func TestSanitizeHandle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"shakhzod", "shakhzod"},
		{"Shakhzod", "shakhzod"},
		{"j.tulaganov@salesdoc.io", "j-tulaganov"}, // email → local part, slugged
		{"  Aziz K  ", "aziz-k"},
		{"a__b--c", "a-b-c"},
		{"-lead-", "lead"},
		{"../etc/passwd", "etc-passwd"}, // path traversal neutralized
		{"a;rm -rf /", "a-rm-rf"},       // shell metacharacters neutralized
		{"'; DROP", "drop"},             // SQL fragment neutralized
		{"", ""},
		{"!@#$", ""}, // nothing valid remains
		{"-", ""},
		{strings.Repeat("a", 60), strings.Repeat("a", 40)}, // length-capped
	}
	for _, c := range cases {
		if got := sanitizeHandle(c.in); got != c.want {
			t.Errorf("sanitizeHandle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHandleDBName(t *testing.T) {
	// Hyphens (valid in DNS/paths) must become underscores for an unquoted MySQL
	// identifier.
	for in, want := range map[string]string{"shakhzod": "dbt_shakhzod", "aziz-k": "dbt_aziz_k"} {
		if got := handleDBName(in); got != want {
			t.Errorf("handleDBName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildProvisionScript(t *testing.T) {
	p := provisionParams{
		Handle:     "shakhzod",
		BaseDomain: "sdteam.uz",
		WebRoot:    "/var/www",
		RepoURL:    "https://github.com/x/sd.git",
		SeedDir:    "/var/www/agora.sdteam.uz",
		SeedDB:     "dbt_agora",
	}
	s := buildProvisionScript(p, "secrettoken")

	// Placement is derived from handle + base domain + web root.
	for _, want := range []string{"set -e", "/var/www/shakhzod.sdteam.uz", "dbt_shakhzod"} {
		if !strings.Contains(s, want) {
			t.Errorf("script must contain %q, got: %s", want, s)
		}
	}

	// Idempotent + NON-DESTRUCTIVE guards: every mutating step is gated on absence.
	for _, want := range []string{"[ ! -d", "[ -f", "[ -e framework", "CREATE DATABASE IF NOT EXISTS", "SHOW TABLES FROM"} {
		if !strings.Contains(s, want) {
			t.Errorf("script must guard step with %q (idempotency), got: %s", want, s)
		}
	}

	// Word-boundary sed so renaming the seed DB never partial-matches a sibling
	// (dbt_agora must NOT also rewrite dbt_agora_cs — the #2192-class bug).
	if !strings.Contains(s, `\bdbt_agora\b`) {
		t.Errorf("DB-name rewrite must be word-boundary-anchored, got: %s", s)
	}

	// A provision is fill-what's-missing — never a wipe.
	for _, banned := range []string{"rm -rf", "DROP DATABASE", "git clean", "checkout -f", "rm -f"} {
		if strings.Contains(s, banned) {
			t.Errorf("provision must be non-destructive; found %q", banned)
		}
	}

	// Token is injected into the clone URL (live script) but must NOT survive
	// redaction (what gets logged/returned).
	if !strings.Contains(s, "secrettoken") {
		t.Error("token must be present in the live clone URL")
	}
	if red := redactGitToken(s); strings.Contains(red, "secrettoken") {
		t.Errorf("redacted script must not leak the token, got: %s", red)
	}
}

// TestBuildProvisionScriptInjectionSafe proves a hostile config value (a single
// quote that could close the shell string) is neutralized by shellQuote rather
// than breaking out into a new command.
func TestBuildProvisionScriptInjectionSafe(t *testing.T) {
	p := provisionParams{
		Handle:     "x",
		BaseDomain: "d",
		WebRoot:    "/w",
		RepoURL:    "https://h/r.git",
		SeedDir:    `/seed'; touch /tmp/pwned; echo '`,
		SeedDB:     "dbt_s",
	}
	s := buildProvisionScript(p, "")
	// shellQuote escapes an embedded single quote as '\'' — its presence proves
	// the value stayed inside its quoted argument.
	if !strings.Contains(s, `'\''`) {
		t.Errorf("a single quote in a config value must be shell-escaped, got: %s", s)
	}
}
