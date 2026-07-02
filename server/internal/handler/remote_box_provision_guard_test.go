package handler

import "testing"

// TestQAHostCheckTarget_EmptyEnvAllowsEverything proves the target-suffix guard
// is a pure no-op when AGORA_QA_HOST_ALLOWED_TARGET_SUFFIX is unset — this is
// the default state and MUST match current (unguarded) behavior exactly, so the
// real sd-main flow targeting agora.sdteam.uz is unaffected.
func TestQAHostCheckTarget_EmptyEnvAllowsEverything(t *testing.T) {
	p := provisionParams{Handle: "shakhzod", BaseDomain: "agora.sdteam.uz"}
	if err := qaHostCheckTarget("", p); err != nil {
		t.Errorf("empty allowed-suffix env must never refuse, got: %v", err)
	}
	if err := qaHostCheckTarget("agora.sdteam.uz", p); err != nil {
		t.Errorf("empty allowed-suffix env must never refuse, got: %v", err)
	}
}

// TestQAHostCheckTarget_MatchingSuffixAllowed proves a demo deployment whose
// SSH host + base domain + derived box subdomain all end with the configured
// suffix is allowed through.
func TestQAHostCheckTarget_MatchingSuffixAllowed(t *testing.T) {
	suffix := "demo.sdteam.uz"
	sshHost := "demo.sdteam.uz"
	p := provisionParams{Handle: "shakhzod", BaseDomain: "demo.sdteam.uz"}

	if err := qaHostCheckTargetWithSuffix(suffix, sshHost, p); err != nil {
		t.Errorf("matching suffix must be allowed, got: %v", err)
	}
}

// TestQAHostCheckTarget_MismatchRefused proves the guard refuses a resolved
// target that does not end with the configured suffix — this is the exact
// scenario the guard exists for: a demo backend accidentally pointing at the
// real prod QA host.
func TestQAHostCheckTarget_MismatchRefused(t *testing.T) {
	suffix := "demo.sdteam.uz"
	cases := []struct {
		name    string
		sshHost string
		p       provisionParams
	}{
		{
			name:    "ssh host points at prod",
			sshHost: "agora.sdteam.uz",
			p:       provisionParams{Handle: "shakhzod", BaseDomain: "demo.sdteam.uz"},
		},
		{
			name:    "base domain points at prod",
			sshHost: "demo.sdteam.uz",
			p:       provisionParams{Handle: "shakhzod", BaseDomain: "agora.sdteam.uz"},
		},
		{
			name:    "both correct but totally unrelated host",
			sshHost: "evil.example.com",
			p:       provisionParams{Handle: "shakhzod", BaseDomain: "evil.example.com"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := qaHostCheckTargetWithSuffix(suffix, c.sshHost, c.p); err == nil {
				t.Errorf("mismatched target must be refused, got nil error")
			}
		})
	}
}

// TestQAHostCheckTarget_CaseInsensitive proves the host/domain compare ignores
// case (DNS names are not case sensitive; a demo backend or operator might set
// AGORA_QA_HOST_SSH_HOST with different casing than the suffix env var).
func TestQAHostCheckTarget_CaseInsensitive(t *testing.T) {
	suffix := "Demo.SDTeam.uz"
	sshHost := "DEMO.sdteam.UZ"
	p := provisionParams{Handle: "shakhzod", BaseDomain: "demo.sdteam.uz"}

	if err := qaHostCheckTargetWithSuffix(suffix, sshHost, p); err != nil {
		t.Errorf("case-insensitive suffix match must be allowed, got: %v", err)
	}
}

// TestQAHostCheckDBPrefix_EmptyEnvAllowsEverything proves the DB-prefix guard
// is a pure no-op when AGORA_QA_HOST_REQUIRE_DB_PREFIX is unset — the default
// state, matching the real sd-main flow's seed DB dbt_agora unaffected.
func TestQAHostCheckDBPrefix_EmptyEnvAllowsEverything(t *testing.T) {
	if err := qaHostCheckDBPrefixWithPrefix("", "dbt_agora"); err != nil {
		t.Errorf("empty required-prefix env must never refuse, got: %v", err)
	}
	if err := qaHostCheckDBPrefixWithPrefix("", ""); err != nil {
		t.Errorf("empty required-prefix env must never refuse, got: %v", err)
	}
}

// TestQAHostCheckDBPrefix_MatchingPrefixAllowed proves a demo seed DB name
// carrying the configured prefix is allowed through.
func TestQAHostCheckDBPrefix_MatchingPrefixAllowed(t *testing.T) {
	if err := qaHostCheckDBPrefixWithPrefix("demo_", "demo_agora"); err != nil {
		t.Errorf("matching prefix must be allowed, got: %v", err)
	}
}

// TestQAHostCheckDBPrefix_MismatchRefused proves the guard refuses a seed DB
// name that doesn't carry the required prefix — the exact scenario this guard
// exists for: a demo backend accidentally configured with the real prod seed
// DB name (dbt_agora).
func TestQAHostCheckDBPrefix_MismatchRefused(t *testing.T) {
	cases := []struct{ prefix, seedDB string }{
		{"demo_", "dbt_agora"},
		{"demo_", ""},
		{"demo_", "Demo_agora"}, // prefix match IS case sensitive (DB names are)
	}
	for _, c := range cases {
		if err := qaHostCheckDBPrefixWithPrefix(c.prefix, c.seedDB); err == nil {
			t.Errorf("qaHostCheckDBPrefixWithPrefix(%q, %q) must be refused, got nil error", c.prefix, c.seedDB)
		}
	}
}
