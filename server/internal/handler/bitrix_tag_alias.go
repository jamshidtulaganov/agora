package handler

import (
	"encoding/json"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/config"
)

// Tag synonyms.
//
// A portal's tags are typed by hand, in more than one language, over years. The
// SalesDoctor portal carries `баг`, `BugReport`, `bug` and `#BugReport` for the
// same idea, so "how many bugs" answered by exact match returns whichever
// spelling the asker happened to guess — a wrong number that looks right.
//
// Transliteration does not solve this: баг transliterates to "bag", not "bug".
// The pairing is a fact about the team's vocabulary, not about the alphabets,
// so it has to be stated.
//
// The map is operator-editable (AGORA_BITRIX_TAG_ALIASES) because vocabulary is
// portal-specific and changes as people invent new labels. The default reflects
// what the SalesDoctor portal actually uses today.

// defaultBitrixTagAliases groups the spellings observed in the portal.
// Canonical name → every spelling that means it, including itself.
var defaultBitrixTagAliases = map[string][]string{
	"bug":     {"bug", "баг", "bugreport", "#bugreport", "баг репорт", "ошибка"},
	"feature": {"feature", "новый функционал", "новый функцонал", "фича"},
	"server":  {"server", "настройка сервера", "сервер", "devops"},
	"task":    {"task", "задача"},
}

// normalizeTag folds the noise humans add to a tag: case, surrounding space,
// and the leading '#' people type out of habit. Kept deliberately small — an
// aggressive normaliser would merge tags that are genuinely different.
func normalizeTag(tag string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(tag), "#")))
}

// bitrixTagAliases returns the alias table, honouring an operator override.
// A malformed override falls back to the default rather than to an empty table:
// silently disabling synonyms would return a plausible, wrong count.
func bitrixTagAliases() map[string][]string {
	raw := strings.TrimSpace(config.String("AGORA_BITRIX_TAG_ALIASES"))
	if raw == "" {
		return defaultBitrixTagAliases
	}
	var parsed map[string][]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil || len(parsed) == 0 {
		return defaultBitrixTagAliases
	}
	return parsed
}

// expandTagQuery returns every spelling a tag query should match, normalized.
//
// Membership is checked in both directions: asking for "bug" finds the group
// whose canonical name is bug, and asking for "баг" — the spelling actually in
// the portal — finds the same group. A tag in no group matches only itself, so
// an unknown label still works and never silently widens.
func expandTagQuery(query string) map[string]bool {
	want := normalizeTag(query)
	out := map[string]bool{want: true}
	if want == "" {
		return out
	}
	for canonical, spellings := range bitrixTagAliases() {
		inGroup := normalizeTag(canonical) == want
		if !inGroup {
			for _, s := range spellings {
				if normalizeTag(s) == want {
					inGroup = true
					break
				}
			}
		}
		if !inGroup {
			continue
		}
		out[normalizeTag(canonical)] = true
		for _, s := range spellings {
			if n := normalizeTag(s); n != "" {
				out[n] = true
			}
		}
	}
	return out
}
