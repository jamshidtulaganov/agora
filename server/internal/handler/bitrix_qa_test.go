package handler

import "testing"

// TestQAVerdictFromLabels locks the QA verdict reduction: case-insensitive,
// whitespace-tolerant, fail beats pass, and a non-QA label set yields no verdict.
func TestQAVerdictFromLabels(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		want  string
	}{
		{"none", nil, ""},
		{"unrelated", []string{"bug", "p1"}, ""},
		{"pass", []string{"qa:pass"}, "pass"},
		{"pass_uppercase", []string{"QA:PASS"}, "pass"},
		{"pass_padded", []string{"  qa:pass "}, "pass"},
		{"fail", []string{"qa:fail"}, "fail"},
		{"fail_wins", []string{"qa:pass", "qa:fail"}, "fail"},
		{"mixed_with_others", []string{"bug", "qa:pass", "p2"}, "pass"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := qaVerdictFromLabels(c.names); got != c.want {
				t.Errorf("qaVerdictFromLabels(%v) = %q, want %q", c.names, got, c.want)
			}
		})
	}
}
