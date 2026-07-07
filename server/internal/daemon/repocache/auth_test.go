package repocache

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestTokenAuthConfig(t *testing.T) {
	pair, ok := tokenAuthConfig("github.com", "x-access-token", "TKN")
	if !ok {
		t.Fatal("expected ok for a non-empty token")
	}
	if pair[0] != "http.https://github.com/.extraheader" {
		t.Errorf("config key = %q", pair[0])
	}
	want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:TKN"))
	if pair[1] != want {
		t.Errorf("config value = %q, want %q", pair[1], want)
	}

	// Defaults: empty host -> github.com, empty username -> x-access-token.
	p, _ := tokenAuthConfig("", "", "T")
	if !strings.Contains(p[0], "github.com") {
		t.Errorf("default host not applied: %q", p[0])
	}
	if !strings.Contains(p[1], base64.StdEncoding.EncodeToString([]byte("x-access-token:T"))) {
		t.Errorf("default username not applied: %q", p[1])
	}

	if _, ok := tokenAuthConfig("github.com", "u", ""); ok {
		t.Error("empty token must yield ok=false (no auth injected)")
	}
}

func TestRepoHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r":                      "github.com",
		"https://github.com/o/r.git":                  "github.com",
		"git@github.com:o/r.git":                      "github.com",
		"ssh://git@ssh-gitlab.sdteam.uz:2222/o/r.git": "ssh-gitlab.sdteam.uz",
		"": "",
	}
	for in, want := range cases {
		if got := repoHostFromURL(in); got != want {
			t.Errorf("repoHostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitEnvInjectsExtraConfig(t *testing.T) {
	env := gitEnv([2]string{"http.https://x/.extraheader", "Authorization: Basic abc"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_CONFIG_VALUE_") || !strings.Contains(joined, "Authorization: Basic abc") {
		t.Error("extra git config header not injected")
	}
	if !strings.Contains(joined, "safe.directory") {
		t.Error("base safe.directory config dropped")
	}
	// gitEnv() with no extras still yields a valid env.
	if len(gitEnv()) == 0 {
		t.Error("gitEnv() returned empty")
	}
}
