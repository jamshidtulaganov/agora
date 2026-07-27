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

// Every gate is WATCHED for a red verdict; none is a precondition. The bug that
// deadlocked every merge was requiring `ci` to PASS when nothing auto-emits
// ci:pass — watching it for FAILURE is free and still stops a merge over a
// manually-run CI that came back red.
func TestBlockingGatesWatchAllThree(t *testing.T) {
	if !reflect.DeepEqual(blockingGates, []string{"ci", "qa", "review"}) {
		t.Errorf("blockingGates = %v, want [ci qa review]", blockingGates)
	}
}

// A tier must never be able to name a gate that must PASS — that is the shape
// of the original defect. Tiers carry advisory reviewer fleets only.
func TestReviewTierCarriesOnlyAdvisoryReviews(t *testing.T) {
	for _, labels := range []map[string]bool{{}, {"tier:light": true}, {"tier:trivial": true}} {
		tier := reviewTierForLabels(labels)
		if tier.name == "" {
			t.Fatalf("tier for %v has no name", labels)
		}
		if len(tier.reviews) == 0 {
			t.Errorf("tier %q should still recommend a reviewer fleet", tier.name)
		}
	}
}
