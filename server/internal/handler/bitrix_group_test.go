package handler

import "testing"

// TestBitrixGroupIDFromDescription covers the inverse of the durable
// "bitrix_group:<id>" project marker used to resolve a project's Bitrix
// workgroup for on-demand sync.
func TestBitrixGroupIDFromDescription(t *testing.T) {
	cases := map[string]string{
		"Imported from Bitrix bitrix_group:42 — do not remove": "42",
		"bitrix_group:7":                 "7",
		"prefix\nbitrix_group:123\nmore": "123",
		"no marker here":                 "",
		"":                               "",
		"bitrix_group:":                  "",   // marker present but no numeric id
		"bitrix_group:99abc":             "99", // id stops at the first non-digit
	}
	for desc, want := range cases {
		if got := bitrixGroupIDFromDescription(desc); got != want {
			t.Errorf("bitrixGroupIDFromDescription(%q) = %q, want %q", desc, got, want)
		}
	}
}
