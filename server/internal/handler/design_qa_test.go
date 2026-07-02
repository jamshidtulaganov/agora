package handler

import "testing"

func TestDesignVerdictOf(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"empty", "", ""},
		{"no design key", `{"verdict":"pass","commands":[]}`, ""},
		{"design pass", `{"verdict":"pass","design":{"verdict":"pass"}}`, "pass"},
		{"design skipped", `{"design":{"verdict":"skipped","reference_node":"1:2"}}`, "skipped"},
		{"design fail", `{"design":{"verdict":"fail","mismatches":[]}}`, "fail"},
		{"malformed", `{bad`, ""},
		{"design null", `{"design":null}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := designVerdictOf([]byte(tt.json)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSliceInstructionRunQAHasNoDesignSectionByDefault(t *testing.T) {
	// The base run_qa recipe (no per-issue design context) must NOT carry the
	// design-verification appendix — that is injected only by
	// sliceActionDesignCompareContext when the issue implements a design.
	got := buildSliceInstruction(sliceActionRunQA, "")
	if got == "" {
		t.Fatal("run_qa must render")
	}
	// The base recipe talks about deterministic checks generally, but the
	// design-specific "reference_node" JSON field only comes from the appendix.
	if containsSub(got, "reference_node") {
		t.Error("base run_qa recipe must not contain the design appendix")
	}
}

func containsSub(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
