package handler

import "testing"

// TestCoCodeBranchName covers the human-readable co-code branch derivation from
// an issue key + title (replacing the old opaque "cocode/issue-<n>-<uuid8>").
// Pure (no DB): exercises slugging, title truncation on a word boundary, and the
// empty-key / empty-title fallbacks.
func TestCoCodeBranchName(t *testing.T) {
	cases := []struct {
		name       string
		issueKey   string
		issueTitle string
		want       string
	}{
		{"key_and_title", "OCT-967", "Add WEX application endpoint", "cocode/oct-967-add-wex-application-endpoint"},
		{"messy_title_chars", "MUL-12", "Fix: token/refresh (race!)", "cocode/mul-12-fix-token-refresh-race"},
		{"empty_title", "OCT-5", "", "cocode/oct-5"},
		{"empty_key", "", "Some task", "cocode/issue-some-task"},
		{"long_title_cut_on_word", "OCT-1", "this is a very long issue title that should be truncated somewhere", "cocode/oct-1-this-is-a-very-long-issue-title-that"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := coCodeBranchName(c.issueKey, c.issueTitle); got != c.want {
				t.Errorf("coCodeBranchName(%q, %q) = %q, want %q", c.issueKey, c.issueTitle, got, c.want)
			}
		})
	}
}
