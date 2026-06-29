package handler

import "testing"

// TestParseRepoHostOwner covers the repo-URL → (host, owner) parsing that maps a
// repo to a workspace git credential. Host + owner are lowercased so the match
// is case-insensitive. Pure (no DB).
func TestParseRepoHostOwner(t *testing.T) {
	cases := []struct {
		in    string
		host  string
		owner string
	}{
		{"https://github.com/jamshid-tulaganov/zoho-octane", "github.com", "jamshid-tulaganov"},
		{"https://github.com/Owner/Repo.git", "github.com", "owner"},
		{"git@github.com:Owner/repo.git", "github.com", "owner"},
		{"ssh://git@ssh-gitlab.sdteam.uz:2222/grp/repo.git", "ssh-gitlab.sdteam.uz", "grp"},
		{"", "", ""},
	}
	for _, c := range cases {
		h, o := parseRepoHostOwner(c.in)
		if h != c.host || o != c.owner {
			t.Errorf("parseRepoHostOwner(%q) = (%q, %q), want (%q, %q)", c.in, h, o, c.host, c.owner)
		}
	}
}
