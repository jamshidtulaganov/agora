package handler

import (
	"context"
	"path"
	"sort"
	"strings"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// SERVER-DERIVED RISK TIER (docs/orchestration-upgrade-plan.md §A1).
//
// The risk tier drives five decisions — dev landing mode (slice_action.go:987),
// auto-merge refusal (:1966), the done gate (:1294), QA gate depth (:2152) and
// the visual-evidence bar (service/qa_evidence.go:739). Until this file it was
// SELF-REPORTED: the agent read the project risk map out of its own brief,
// classified its own diff, and wrote a risk:* label the server then trusted.
//
// A safety control must not be self-reported. This file does the same
// classification the agent was asked to do, from the same risk map, against the
// file list the PULL REQUEST actually carries (github_pull_request.changed_paths,
// migration 210, filled from GitHub's Files API — never from the agent).
//
// Precedence, and every branch of it is deliberate:
//
//  1. DERIVED WINS when there is evidence. A known changed-path list plus a
//     project risk map produces the tier, and the agent's label does not
//     override it DOWNWARD.
//  2. A STRICTER LABEL STILL ESCALATES. risk:critical on a change the globs
//     call guarded is honoured: raising the bar is always allowed, from anyone.
//     Only the downgrade is refused, because only the downgrade is dangerous.
//  3. NO EVIDENCE ⇒ TODAY'S BEHAVIOUR, UNCHANGED. With no changed-path list
//     (no PR yet, sprint mode's shared branch, GitHub App not configured, a
//     GitLab-provider row) the label wins exactly as it did before, and a
//     risk-mapped project with no label still fails closed at `guarded`. This
//     is what keeps the change from silently re-tiering every existing issue
//     the moment it ships.
//  4. NO RISK MAP AND NO LABEL ⇒ "" internally, `unclassified` at the API
//     boundary (§A1.3). "" is correct as "no opinion" but is one careless
//     switch away from being read as "safe", so the wire word is explicit.

// Risk tiers, in ascending blast radius. `unclassified` is an API-boundary word
// only — it is never stored and never returned by issueRiskTier.
const (
	riskTierSafe         = "safe"
	riskTierGuarded      = "guarded"
	riskTierCritical     = "critical"
	riskTierUnclassified = "unclassified"
)

// Where a resolved tier came from. Shipped on every read surface that carries a
// tier, because "the agent says this is safe" and "the server matched the diff
// against the risk map" are not the same claim and must not render the same.
const (
	riskSourceDerived    = "derived"     // globs matched the PR's real file list
	riskSourceLabel      = "label"       // an explicit risk:* label (self-reported unless a human set it)
	riskSourceMapDefault = "map_default" // risk-mapped project, no diff to classify — fail closed
	riskSourceNone       = "unclassified"
)

// riskDerivedMaxPaths caps how many changed paths one classification considers.
// A PR touching more than this is guarded-or-worse by any sane risk map, and an
// unbounded loop over a 5000-file vendor bump has no business running inside a
// queue read.
const riskDerivedMaxPaths = 500

// riskTierResolution is the full answer: the tier, where it came from, and how
// much evidence stood behind it.
type riskTierResolution struct {
	Tier   string // "" | safe | guarded | critical  ("" = unclassified)
	Source string
	// KnownPaths is the number of changed paths the classification saw. 0 means
	// there was no diff evidence at all — the reason a consumer should not read
	// `safe` as "the server checked".
	KnownPaths int
}

// APITier renders the tier for the wire, making the absence explicit (§A1.3).
func (r riskTierResolution) APITier() string { return apiRiskTier(r.Tier) }

// apiRiskTier maps the internal "" (no opinion) to the explicit wire word. Every
// consumer of the wire value must have a default: branch — a future sixth tier
// has to degrade to a generic rendering, not disappear (CLAUDE.md enum drift).
func apiRiskTier(tier string) string {
	switch tier {
	case riskTierSafe, riskTierGuarded, riskTierCritical:
		return tier
	default:
		return riskTierUnclassified
	}
}

// riskTierRank orders the tiers so "the strictest wins" is one comparison.
// Anything unknown ranks 0 so it can never beat a real tier.
func riskTierRank(tier string) int {
	switch tier {
	case riskTierCritical:
		return 3
	case riskTierGuarded:
		return 2
	case riskTierSafe:
		return 1
	default:
		return 0
	}
}

// normalizeRiskMapTier reads an entry's declared tier. An entry with a missing
// or unrecognised tier is GUARDED, matching renderRiskMapContext — a typo in a
// safety control must never silently mean "safe".
func normalizeRiskMapTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case riskTierCritical:
		return riskTierCritical
	case riskTierSafe:
		return riskTierSafe
	default:
		return riskTierGuarded
	}
}

// normalizeRiskPath puts a glob or a repo path in one comparable shape:
// forward slashes, no leading "./" or "/", no trailing "/".
func normalizeRiskPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	p = strings.TrimPrefix(p, "/")
	return strings.TrimSuffix(p, "/")
}

// riskGlobMatch reports whether a risk-map glob covers a changed file.
//
// Semantics, stated because a safety control's matcher must be unambiguous:
//   - segments are matched with path.Match, so `*` and `?` stop at a "/";
//   - `**` matches zero or more whole segments;
//   - a pattern that matches any DIRECTORY above the file matches the file, so
//     "billing" covers "billing/pay/Kassa.php" and "protected/modules/pay"
//     covers everything under it — the gitignore-ish reading the risk map's own
//     documentation promises ("path globs relative to the repo root").
func riskGlobMatch(glob, filePath string) bool {
	g := normalizeRiskPath(glob)
	p := normalizeRiskPath(filePath)
	if g == "" || p == "" {
		return false
	}
	pattern := strings.Split(g, "/")
	segments := strings.Split(p, "/")
	// Try the whole path first, then each ancestor directory. Matching an
	// ancestor is what makes a bare module name cover its subtree.
	for i := len(segments); i > 0; i-- {
		if matchRiskGlobSegments(pattern, segments[:i]) {
			return true
		}
	}
	return false
}

func matchRiskGlobSegments(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(segments); i++ {
			if matchRiskGlobSegments(pattern[1:], segments[i:]) {
				return true
			}
		}
		return false
	}
	if len(segments) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], segments[0])
	if err != nil || !ok {
		return false
	}
	return matchRiskGlobSegments(pattern[1:], segments[1:])
}

// riskTierForPath resolves one changed file against the map: the strictest
// matching entry wins, and a path no entry claims is GUARDED, never safe (the
// rule the risk map's own header states and the brief tells agents to apply).
func riskTierForPath(entries []riskMapEntry, filePath string) string {
	best := ""
	for _, e := range entries {
		matched := false
		for _, g := range e.Paths {
			if riskGlobMatch(g, filePath) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if tier := normalizeRiskMapTier(e.Tier); riskTierRank(tier) > riskTierRank(best) {
			best = tier
		}
	}
	if best == "" {
		return riskTierGuarded
	}
	return best
}

// classifyRiskTierForPaths is the pure classifier — no database, no request, so
// the safety rule it encodes is unit-testable on its own. Returns "" when there
// is nothing to classify (no map, or no evidence), which the caller must read as
// UNKNOWN and never as safe.
func classifyRiskTierForPaths(entries []riskMapEntry, paths []string) string {
	if len(entries) == 0 || len(paths) == 0 {
		return ""
	}
	best := ""
	for i, p := range paths {
		if i >= riskDerivedMaxPaths {
			break
		}
		if strings.TrimSpace(p) == "" {
			continue
		}
		if tier := riskTierForPath(entries, p); riskTierRank(tier) > riskTierRank(best) {
			best = tier
			if best == riskTierCritical {
				break // nothing outranks critical; stop early
			}
		}
	}
	return best
}

// riskTierFromLabelNames reads an explicit risk:<tier> label. The strictest one
// wins when several are attached (a sticky pair from an earlier re-tier must not
// let the weaker of the two decide).
func riskTierFromLabelNames(names []string) string {
	best := ""
	for _, n := range names {
		var tier string
		switch strings.ToLower(strings.TrimSpace(n)) {
		case "risk:critical":
			tier = riskTierCritical
		case "risk:guarded":
			tier = riskTierGuarded
		case "risk:safe":
			tier = riskTierSafe
		default:
			continue
		}
		if riskTierRank(tier) > riskTierRank(best) {
			best = tier
		}
	}
	return best
}

// resolveRiskTier is the precedence rule as a pure function (see the file
// header). Split out from the DB reads so the four branches are testable
// without a database and so the decision queue, which has already loaded the
// labels and paths in bulk, can reuse the exact same rule.
func resolveRiskTier(entries []riskMapEntry, mapped bool, paths, labelNames []string) riskTierResolution {
	labelTier := riskTierFromLabelNames(labelNames)
	if !mapped {
		if labelTier != "" {
			return riskTierResolution{Tier: labelTier, Source: riskSourceLabel}
		}
		return riskTierResolution{Tier: "", Source: riskSourceNone}
	}
	derived := classifyRiskTierForPaths(entries, paths)
	if derived == "" {
		// NO EVIDENCE. Today's behaviour exactly: an explicit label decides,
		// otherwise the risk-mapped project fails closed at guarded.
		if labelTier != "" {
			return riskTierResolution{Tier: labelTier, Source: riskSourceLabel}
		}
		return riskTierResolution{Tier: riskTierGuarded, Source: riskSourceMapDefault}
	}
	res := riskTierResolution{Tier: derived, Source: riskSourceDerived, KnownPaths: len(paths)}
	// A stricter label escalates; a weaker one is ignored. Raising the bar is
	// always allowed; lowering it is the self-report this whole file removes.
	if riskTierRank(labelTier) > riskTierRank(derived) {
		res.Tier = labelTier
		res.Source = riskSourceLabel
	}
	return res
}

// ---------------------------------------------------------------------------
// The database-backed resolver
// ---------------------------------------------------------------------------

// resolveIssueRiskTier is the one read every surface goes through. Computed on
// read from (project risk map × PR changed paths × labels) — nothing about the
// derivation is stored, so it cannot itself go stale (the discipline
// docs/living-truth-plan.md applies to staleness).
func (h *Handler) resolveIssueRiskTier(ctx context.Context, issue db.Issue) riskTierResolution {
	entries, mapped := h.projectRiskMap(ctx, issue)
	var labelNames []string
	if labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		labelNames = make([]string, 0, len(labels))
		for _, l := range labels {
			labelNames = append(labelNames, l.Name)
		}
	}
	var paths []string
	if mapped {
		paths = h.issueChangedPaths(ctx, issue)
	}
	return resolveRiskTier(entries, mapped, paths, labelNames)
}

// issueChangedPaths returns the deduplicated repo-relative paths every pull
// request linked to this issue touches, as GitHub reported them. Empty means
// UNKNOWN — no PR, no App credentials to fetch with, a provider that does not
// report files — and callers must treat it as unknown, never as "no changes".
func (h *Handler) issueChangedPaths(ctx context.Context, issue db.Issue) []string {
	prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID)
	if err != nil || len(prs) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, 16)
	for _, pr := range prs {
		for _, p := range pr.ChangedPaths {
			n := normalizeRiskPath(p)
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
			if len(out) >= riskDerivedMaxPaths {
				sort.Strings(out)
				return out
			}
		}
	}
	sort.Strings(out)
	return out
}
