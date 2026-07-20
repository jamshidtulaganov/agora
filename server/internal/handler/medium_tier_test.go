package handler

import "testing"

func TestMediumTierApplies(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"un-tiered, un-escalated: applies", nil, true},
		{"only unrelated labels: applies", []string{"type:feature", "priority:high"}, true},
		{"explicit tier:trivial opts out", []string{"tier:trivial"}, false},
		{"explicit tier:light opts out", []string{"tier:light"}, false},
		{"explicit tier:heavy opts out", []string{"tier:heavy"}, false},
		{"already tier:medium: idempotent opt-out", []string{"tier:medium"}, false},
		{"risk:guarded escalates out", []string{"risk:guarded"}, false},
		{"risk:critical escalates out", []string{"risk:critical"}, false},
		{"context:large escalates out", []string{"context:large"}, false},
		{"escalation wins over an unrelated label", []string{"type:feature", "risk:critical"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := make(map[string]bool, len(tt.labels))
			for _, l := range tt.labels {
				set[l] = true
			}
			if got := mediumTierApplies(set); got != tt.want {
				t.Errorf("mediumTierApplies(%v) = %v, want %v", tt.labels, got, tt.want)
			}
		})
	}
}
