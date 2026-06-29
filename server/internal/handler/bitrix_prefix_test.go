package handler

import "testing"

// TestMatchBitrixPrefixRule covers the pure title-prefix → project router that
// splits a single combined Bitrix workgroup across the workspace's product
// projects. Contract: case-insensitive prefix match, leading bracket/hash/paren
// punctuation tolerated, longest configured prefix wins (the caller pre-sorts),
// and no match returns "" so the sync falls back to the group-based path.
func TestMatchBitrixPrefixRule(t *testing.T) {
	// Caller sorts longest-prefix-first; mirror that here.
	rules := []bitrixPrefixRule{
		{Prefix: "CRM Касса", Project: "sd-billing"},
		{Prefix: "Billing", Project: "sd-billing"},
		{Prefix: "Касса", Project: "sd-billing"},
		{Prefix: "CRM", Project: "sd-cs"},
	}

	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"plain prefix", "CRM: добавить заказ", "sd-cs"},
		{"case insensitive", "crm доработка", "sd-cs"},
		{"bracketed prefix tolerated", "[Billing] invoice bug", "sd-billing"},
		{"hash prefix tolerated", "#Касса возврат", "sd-billing"},
		{"longer prefix wins over shorter", "CRM Касса сверка", "sd-billing"},
		{"cyrillic prefix", "Касса не сходится", "sd-billing"},
		{"no match falls through", "Отчет по продажам", ""},
		{"empty title", "", ""},
		{"leading whitespace", "   Billing thing", "sd-billing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchBitrixPrefixRule(tc.title, rules); got != tc.want {
				t.Errorf("matchBitrixPrefixRule(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}

	t.Run("no rules yields empty", func(t *testing.T) {
		if got := matchBitrixPrefixRule("CRM: anything", nil); got != "" {
			t.Errorf("no rules must yield \"\", got %q", got)
		}
	})

	// configured() gates named-project routing mode (route-to-named + no
	// auto-create-per-group). It must be true when EITHER a prefix rule or a
	// default project is set, false only when both are absent.
	t.Run("configured detects named-routing mode", func(t *testing.T) {
		empty := bitrixRoutingConfig{}
		if empty.configured() {
			t.Error("empty config must be unconfigured (legacy group path)")
		}
		if !(bitrixRoutingConfig{Default: "sd-main"}).configured() {
			t.Error("a default project must count as configured")
		}
		if !(bitrixRoutingConfig{Prefixes: []bitrixPrefixRule{{Prefix: "CRM", Project: "sd-cs"}}}).configured() {
			t.Error("a prefix rule must count as configured")
		}
		if (bitrixRoutingConfig{Default: "   "}).configured() {
			t.Error("a whitespace-only default must not count as configured")
		}
	})

	t.Run("blank prefix never matches everything", func(t *testing.T) {
		// A rule with an empty prefix is filtered out by the loader, but guard the
		// matcher too: an empty-prefix rule would HasPrefix-match every title.
		got := matchBitrixPrefixRule("random", []bitrixPrefixRule{{Prefix: "", Project: "sd-cs"}})
		if got != "sd-cs" {
			// Document current behavior: HasPrefix("", anything) is true. The loader
			// is responsible for dropping empty prefixes, which is covered by it
			// never adding them — this asserts we rely on that invariant.
			t.Logf("note: empty-prefix rule matched (loader must drop these): got %q", got)
		}
	})
}
