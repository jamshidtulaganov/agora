package handler

import (
	"reflect"
	"testing"
)

// The tier no longer gates anything — it only recommends a reviewer fleet.
// A `required` set used to live here and it is exactly what deadlocked the
// merge: the default tier required a `ci` gate no reporter in the product ever
// emits, so Ready was structurally false on every default-tier issue. See the
// package comment in merge_readiness.go.
func TestReviewTierForLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   []string
		wantName string
		wantRev  []string
	}{
		{"default is full fleet", nil, "full", []string{"qa", "security", "code-review"}},
		{"light recommends a spot-check", []string{"tier:light"}, "light", []string{"code-review"}},
		{"trivial recommends a spot-check", []string{"tier:trivial"}, "trivial", []string{"code-review"}},
		{"trivial beats light", []string{"tier:light", "tier:trivial"}, "trivial", []string{"code-review"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := make(map[string]bool, len(tt.labels))
			for _, l := range tt.labels {
				set[l] = true
			}
			got := reviewTierForLabels(set)
			if got.name != tt.wantName || !reflect.DeepEqual(got.reviews, tt.wantRev) {
				t.Errorf("reviewTierForLabels(%v) = {%q, %v}, want {%q, %v}",
					tt.labels, got.name, got.reviews, tt.wantName, tt.wantRev)
			}
		})
	}
}

// blockingGates must never include a gate nothing emits. `ci:pass` is written
// only by a manually-fired run_ci slice action and is auto-dispatched by
// nothing, so requiring it is an unsatisfiable gate — the exact bug this set
// replaced.
func TestBlockingGatesExcludeCI(t *testing.T) {
	for _, g := range blockingGates {
		if g == "ci" {
			t.Fatalf("ci must not be a blocking gate: nothing in the product emits ci:pass automatically")
		}
	}
	if !reflect.DeepEqual(blockingGates, []string{"qa", "review"}) {
		t.Errorf("blockingGates = %v, want [qa review]", blockingGates)
	}
}
