package handler

import (
	"reflect"
	"testing"
)

func TestReviewTierForLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   []string
		wantName string
		wantReq  []string
		wantRev  []string
	}{
		{"default is full fleet", nil, "full", []string{"ci", "qa"}, []string{"ci", "qa", "security", "code-review"}},
		{"light gates on ci only", []string{"tier:light"}, "light", []string{"ci"}, []string{"ci"}},
		{"trivial gates on ci only", []string{"tier:trivial"}, "trivial", []string{"ci"}, []string{"ci"}},
		{"trivial beats light", []string{"tier:light", "tier:trivial"}, "trivial", []string{"ci"}, []string{"ci"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := make(map[string]bool, len(tt.labels))
			for _, l := range tt.labels {
				set[l] = true
			}
			got := reviewTierForLabels(set)
			if got.name != tt.wantName ||
				!reflect.DeepEqual(got.required, tt.wantReq) ||
				!reflect.DeepEqual(got.reviews, tt.wantRev) {
				t.Errorf("reviewTierForLabels(%v) = {%q, %v, %v}, want {%q, %v, %v}",
					tt.labels, got.name, got.required, got.reviews,
					tt.wantName, tt.wantReq, tt.wantRev)
			}
		})
	}
}
