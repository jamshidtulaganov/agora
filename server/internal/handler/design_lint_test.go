package handler

import (
	"strings"
	"testing"
)

func TestDesignLintHasBlock(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{"empty", "", false},
		{"no design", `{"verdict":"pass"}`, false},
		{"lint warn only", `{"design":{"lint":[{"severity":"warn"}]}}`, false},
		{"lint block", `{"design":{"lint":[{"severity":"warn"},{"severity":"block"}]}}`, true},
		{"no lint array", `{"design":{"verdict":"pass"}}`, false},
		{"malformed", `{bad`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := designLintHasBlock([]byte(tt.json)); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBaseRunQAHasNoLintByDefault(t *testing.T) {
	// The base run_qa recipe carries no design-lint appendix — it is injected
	// only by sliceActionDesignLintContext when the project has a manifest.
	got := buildSliceInstruction(sliceActionRunQA, "")
	if strings.Contains(got, "DESIGN-SYSTEM LINT") {
		t.Error("base run_qa must not contain the design-lint appendix")
	}
}
