package handler

import "testing"

// TestBitrixGroupIsSprint covers the case-insensitive, multi-script sprint
// detector that decides whether a Bitrix workgroup should map to an Agora
// sprint under sd-main (rather than its own project). The match looks for the
// substrings "sprint" (Latin) or "спринт" (Cyrillic) anywhere in the name.
func TestBitrixGroupIsSprint(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Sprint 7", true},
		{"Iyun Sprint", true},
		{"Спринт 12", true},
		{"CRM API", false},
		{"", false},
	}
	for _, c := range cases {
		if got := bitrixGroupIsSprint(c.name); got != c.want {
			t.Errorf("bitrixGroupIsSprint(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
