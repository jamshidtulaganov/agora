package repoindex

import (
	"reflect"
	"testing"
)

func TestSplitIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"IssueStatus", []string{"issue", "status"}},
		{"issue_status", []string{"issue", "status"}},
		{"parseHTTPResponse", []string{"parse", "http", "response"}},
		{"MAX_RETRIES", []string{"max", "retries"}},
		{"injectQAMcpConfig", []string{"inject", "qa", "mcp", "config"}},
		{"simple", nil}, // no split — caller already holds the whole word
	}
	for _, tc := range cases {
		got := splitIdentifier(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitIdentifier(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestTokenizeSplitsIdentifiers is the property the whole ranking rests on: a
// natural-language query must match code identifiers. Without sub-word
// emission, "issue status" scores zero against `IssueStatus` and BM25 over
// code retrieves nothing useful.
func TestTokenizeSplitsIdentifiers(t *testing.T) {
	got := tokenize("func UpdateIssueStatus(issue_id string)")
	want := map[string]bool{"updateissuestatus": true, "update": true, "issue": true, "status": true, "issue_id": true, "id": true}
	have := make(map[string]bool)
	for _, tok := range got {
		have[tok] = true
	}
	for w := range want {
		if !have[w] {
			t.Errorf("tokenize missing %q; got %v", w, got)
		}
	}
}

func TestNewRankerDropsStopwords(t *testing.T) {
	r := newRanker("the board should show the archived issues")
	for _, term := range r.qTerms {
		if stopwords[term] {
			t.Errorf("query kept stopword %q", term)
		}
	}
	has := func(s string) bool {
		for _, t := range r.qTerms {
			if t == s {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"board", "archived", "issues"} {
		if !has(want) {
			t.Errorf("query dropped discriminating term %q; got %v", want, r.qTerms)
		}
	}
}

// TestRankerPrefersTermDenseFile is the core ranking contract: the file that
// is actually about the query wins over one that merely mentions it.
func TestRankerPrefersTermDenseFile(t *testing.T) {
	r := newRanker("archived issue board")
	r.add("board/archived_issue.go", "go", "func filterArchivedIssues() { // archived issue board logic }")
	r.add("unrelated/billing.go", "go", "func chargeCard() { /* nothing to do with an issue here */ }")
	r.add("docs/everything.md", "markdown", longProse())

	hits := r.top(3)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Path != "board/archived_issue.go" {
		t.Errorf("top hit = %q, want board/archived_issue.go (scores: %v)", hits[0].Path, hits)
	}
}

func TestRankerZeroMatchesReturnsNothing(t *testing.T) {
	r := newRanker("zebra quantum harpsichord")
	r.add("a.go", "go", "package main")
	if hits := r.top(5); len(hits) != 0 {
		t.Errorf("top() = %v, want none", hits)
	}
}

// TestDropLocaleTwins pins the docs-site twin collapse: three translations of
// one page must not consume three of twelve pack slots.
func TestDropLocaleTwins(t *testing.T) {
	in := []Hit{
		{Path: "content/docs/conventions.mdx", Score: 9},
		{Path: "content/docs/conventions.zh.mdx", Score: 8},
		{Path: "content/docs/conventions.ru.mdx", Score: 7},
		{Path: "content/docs/other.zh-Hans.mdx", Score: 6},
		{Path: "server/handler.go", Score: 5},
	}
	got := dropLocaleTwins(in)
	var paths []string
	for _, h := range got {
		paths = append(paths, h.Path)
	}
	want := []string{"content/docs/conventions.mdx", "content/docs/other.zh-Hans.mdx", "server/handler.go"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("dropLocaleTwins = %v, want %v", paths, want)
	}
}

func TestLangWeightPrefersCodeOverProse(t *testing.T) {
	if langWeight("go", "a/b.go") <= langWeight("markdown", "a/b.md") {
		t.Error("code must outweigh prose at equal term evidence")
	}
	if langWeight("go", "a/b_test.go") >= langWeight("go", "a/b.go") {
		t.Error("implementation must outweigh its test at equal term evidence")
	}
}

func longProse() string {
	s := ""
	for i := 0; i < 200; i++ {
		s += "this document mentions the archived issue board and every other concept in the product. "
	}
	return s
}

// TestLocaleBaseKeyLeavesNonLocalesAlone guards the twin collapse against
// eating real files whose second-to-last segment merely looks like a locale.
func TestLocaleBaseKeyLeavesNonLocalesAlone(t *testing.T) {
	for _, path := range []string{"api/schema.pb.go", "types/index.d.ts", "server/main.go"} {
		if got := localeBaseKey(path); got != path {
			t.Errorf("localeBaseKey(%q) = %q, want unchanged", path, got)
		}
	}
	if got := localeBaseKey("docs/conventions.zh.mdx"); got != "docs/conventions.mdx" {
		t.Errorf("localeBaseKey collapse = %q, want docs/conventions.mdx", got)
	}
}
