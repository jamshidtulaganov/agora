package repoindex

import (
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// BM25 parameters. Standard Robertson/Sparck-Jones defaults: k1 controls
// term-frequency saturation, b controls length normalization. Code documents
// vary enormously in length (a 20-line config next to a 900-line service), so
// length normalization matters more here than it does on prose.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// pathBoost counts each path token this many times. A file named
	// `issue_status.go` is a strong signal for a query about issue status —
	// far stronger per-token than one passing mention in a body.
	pathBoost = 4
)

// ranker scores files against ONE query, streaming.
//
// Why streaming and not a persistent inverted index: the push pack serves
// exactly one query per task, and the query is known before the scan starts.
// That collapses the whole problem — there is no reason to build, store,
// invalidate, and garbage-collect an index that will answer a single question.
// Keeping only query-term frequencies bounds memory to O(matched files ×
// query terms) instead of O(repo), so a 20k-file monorepo costs kilobytes
// rather than gigabytes. A persistent store earns its keep only if agents
// query mid-loop, which measured tool adoption says they do not.
type ranker struct {
	qTerms []string
	qSet   map[string]bool

	cands    []candidate
	df       map[string]int // document frequency, query terms only
	totalLen int            // summed token length over ALL docs seen
	nDocs    int
}

// candidate is a file that matched at least one query term.
type candidate struct {
	path string
	lang string
	tf   map[string]int
	len  int
}

// Hit is one ranked file.
type Hit struct {
	Path  string
	Lang  string
	Score float64
	Terms []string // query terms this file matched, for snippet selection
}

// stopwords are dropped from the QUERY only (never from documents, where they
// still count toward length). An issue title is prose — "the board should show
// the issues" carries four words that match essentially every file in the
// repo, and BM25's IDF damps them but does not silence them. Dropping them
// keeps ranking on the terms that actually discriminate.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true, "not": true,
	"you": true, "all": true, "can": true, "her": true, "was": true, "one": true,
	"our": true, "out": true, "day": true, "get": true, "has": true, "him": true,
	"his": true, "how": true, "its": true, "new": true, "now": true, "old": true,
	"see": true, "two": true, "way": true, "who": true, "boy": true, "did": true,
	"use": true, "man": true, "too": true, "any": true, "say": true, "she": true,
	"may": true, "let": true, "put": true, "end": true, "why": true, "try": true,
	"this": true, "that": true, "with": true, "have": true, "from": true,
	"they": true, "been": true, "were": true, "when": true, "will": true,
	"would": true, "there": true, "their": true, "what": true, "about": true,
	"which": true, "them": true, "then": true, "some": true, "into": true,
	"only": true, "other": true, "than": true, "also": true, "just": true,
	"should": true, "could": true, "shows": true, "show": true, "make": true,
	"need": true, "want": true, "does": true, "doing": true, "using": true,
	"like": true, "such": true, "more": true, "most": true, "very": true,
	"here": true, "over": true, "after": true, "before": true, "each": true,
	"where": true, "these": true, "those": true, "being": true, "because": true,
}

func newRanker(query string) *ranker {
	var terms []string
	for _, t := range dedupe(tokenize(query)) {
		if stopwords[t] {
			continue
		}
		terms = append(terms, t)
	}
	set := make(map[string]bool, len(terms))
	for _, t := range terms {
		set[t] = true
	}
	return &ranker{qTerms: terms, qSet: set, df: make(map[string]int)}
}

// add feeds one file into the ranking. Only query-term frequencies are
// retained; the body is not held.
func (r *ranker) add(path, lang, body string) {
	tf := make(map[string]int)
	docLen := 0

	for _, tok := range tokenize(path) {
		docLen += pathBoost
		if r.qSet[tok] {
			tf[tok] += pathBoost
		}
	}
	for _, tok := range tokenize(body) {
		docLen++
		if r.qSet[tok] {
			tf[tok]++
		}
	}

	r.nDocs++
	r.totalLen += docLen
	if len(tf) == 0 {
		return // scored 0 by construction; nothing to keep
	}
	for term := range tf {
		r.df[term]++
	}
	r.cands = append(r.cands, candidate{path: path, lang: lang, tf: tf, len: docLen})
}

// top returns the best `limit` files, highest score first.
func (r *ranker) top(limit int) []Hit {
	if r.nDocs == 0 || len(r.cands) == 0 {
		return nil
	}
	avgLen := float64(r.totalLen) / float64(r.nDocs)
	if avgLen == 0 {
		return nil
	}
	n := float64(r.nDocs)

	hits := make([]Hit, 0, len(r.cands))
	for _, c := range r.cands {
		score := 0.0
		matched := make([]string, 0, len(c.tf))
		for term, freq := range c.tf {
			df := float64(r.df[term])
			// Robertson IDF with the +1 guard: a term present in every
			// document scores 0 rather than going negative.
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			tf := float64(freq)
			norm := tf * (bm25K1 + 1) /
				(tf + bm25K1*(1-bm25B+bm25B*float64(c.len)/avgLen))
			score += idf * norm
			matched = append(matched, term)
		}
		sort.Strings(matched)
		hits = append(hits, Hit{Path: c.path, Lang: c.lang, Score: score * langWeight(c.lang, c.path), Terms: matched})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path // deterministic across runs
	})
	hits = dropLocaleTwins(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// langWeight biases the ranking toward code. The consumer is a coding agent
// whose task is to change behavior, and behavior lives in source. Prose files
// are not excluded — a design doc or CLAUDE.md is often the best possible hit
// — but a long planning document mentions every concept in the product and so
// matches almost any query, which is exactly the failure BM25 alone does not
// prevent. The penalty only decides near-ties; a doc that genuinely dominates
// on term evidence still wins.
func langWeight(lang, path string) float64 {
	switch lang {
	case "markdown":
		return 0.6
	case "json", "yaml":
		return 0.8
	}
	// Test files describe behavior precisely and are useful, but the file a
	// task needs to CHANGE is usually the implementation next to them.
	base := strings.ToLower(filepath.Base(path))
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasSuffix(base, "_test.go") {
		return 0.85
	}
	return 1.0
}

// localeSuffixRe matches a locale segment in this repo's translated docs
// (`conventions.zh.mdx`, `mcp-injection.ru.mdx`, `page.zh-Hans.mdx`). The
// language subtag is matched against knownLocales rather than a bare
// [a-z]{2}, which would also swallow `schema.pb.go` -> `schema.go` and
// silently drop generated code whenever both files rank.
var localeSuffixRe = regexp.MustCompile(`\.([a-z]{2})(-[A-Za-z]+)?(\.[A-Za-z0-9]+)$`)

// knownLocales are the language subtags the docs site actually ships.
var knownLocales = map[string]bool{
	"en": true, "zh": true, "ru": true, "uz": true, "es": true, "fr": true,
	"de": true, "ja": true, "ko": true, "pt": true, "it": true, "tr": true,
	"ar": true, "hi": true, "vi": true, "th": true, "pl": true, "nl": true,
}

// localeBaseKey returns the path with its locale segment removed, or the path
// unchanged when it carries none.
func localeBaseKey(path string) string {
	m := localeSuffixRe.FindStringSubmatch(path)
	if m == nil || !knownLocales[m[1]] {
		return path
	}
	return strings.TrimSuffix(path, m[0]) + m[3]
}

// dropLocaleTwins keeps only the best-scoring translation of a page. The docs
// site stores each page as flat locale twins, so an unfiltered ranking spends
// three of twelve pack slots restating one document in Russian, Uzbek, and
// Chinese — pure waste in a budget this tight. Hits arrive sorted, so the
// first twin seen is the best-scoring one.
func dropLocaleTwins(hits []Hit) []Hit {
	seen := make(map[string]bool, len(hits))
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		key := localeBaseKey(h.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}

// tokenize splits text into lowercase search terms. Code identifiers are also
// emitted as their sub-words, so a query for "issue status" matches
// `IssueStatus`, `issue_status`, and `issue-status` alike — without that,
// BM25 over code misses almost everything an agent actually asks for.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		word := cur.String()
		cur.Reset()
		lower := strings.ToLower(word)
		if len(lower) > 1 && len(lower) < 64 {
			out = append(out, lower)
		}
		for _, part := range splitIdentifier(word) {
			if part != lower && len(part) > 1 {
				out = append(out, part)
			}
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// splitIdentifier breaks camelCase / PascalCase / snake_case / SCREAMING_CASE
// into lowercase parts. "parseHTTPResponse" -> [parse, http, response].
// Returns nil when no split happened — the caller already has the whole word.
func splitIdentifier(word string) []string {
	var parts []string
	var cur strings.Builder
	rs := []rune(word)
	for i, r := range rs {
		if r == '_' {
			if cur.Len() > 0 {
				parts = append(parts, strings.ToLower(cur.String()))
				cur.Reset()
			}
			continue
		}
		// Split before an uppercase run that starts a new word, and at the
		// tail of an acronym run (HTTPResponse -> http | response).
		if i > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				if cur.Len() > 0 {
					parts = append(parts, strings.ToLower(cur.String()))
					cur.Reset()
				}
			}
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		parts = append(parts, strings.ToLower(cur.String()))
	}
	if len(parts) < 2 {
		return nil
	}
	return parts
}

func dedupe(terms []string) []string {
	seen := make(map[string]bool, len(terms))
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
