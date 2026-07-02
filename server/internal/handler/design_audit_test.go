package handler

import (
	"strings"
	"testing"
)

func TestBuildSliceInstructionDesignAudit(t *testing.T) {
	got := buildSliceInstruction(sliceActionDesignAudit, "")
	if got == "" {
		t.Fatal("design_audit must render")
	}
	for _, want := range []string{
		"AUDIT this project's design-system HEALTH",
		"READ-ONLY",
		"OFF-TOKEN VALUES",
		"Frequency-rank",
		"DUPLICATED MARKUP",
		"UNMANAGED COMPONENTS",
		"PROPOSED TOKENS",
		"```design-audit",
		"never invent counts or refs",
		"do NOT change code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("design_audit recipe missing %q", want)
		}
	}
	if !isKnownSliceActionKind(sliceActionDesignAudit) {
		t.Error("design_audit must be a known slice action")
	}
	if sliceActionOpensPR(sliceActionDesignAudit) {
		t.Error("design_audit must not open a PR")
	}
}
