package handler

import (
	"strings"
	"testing"
)

// TestBuildSliceInstructionDesignProposal asserts the design_proposal recipe
// tells the agent to analyze-not-implement: node-scoped reads, no code, no issue
// creation, reuse-vs-new classification, deviations as questions, the language
// rule, the blocked protocol, the filename contract, and the exact block tag.
func TestBuildSliceInstructionDesignProposal(t *testing.T) {
	got := buildSliceInstruction(sliceActionDesignProposal, "")
	if got == "" {
		t.Fatal("design_proposal must render a non-empty instruction")
	}
	musts := []struct{ label, substr string }{
		{"designer-analyst role", "DESIGNER-ANALYST"},
		{"no implementation code", "Do NOT write implementation code"},
		{"no issue creation", "do NOT create issues"},
		{"node-scoped reads", "get_figma_data(fileKey, nodeId) NODE-SCOPED"},
		{"never whole file", "never fetch a whole file"},
		{"render download", "download_figma_images"},
		{"filename contract", "figma-<node-id-with-dashes>.png"},
		{"upload attachments", "UPLOAD them as attachments"},
		{"reuse verdict", "REUSE"},
		{"extend verdict", "EXTEND"},
		{"new verdict", "NEW"},
		{"deviations as questions", "QUESTION for the human"},
		{"empty/error states", "empty / loading / error states"},
		{"language rule", "SAME LANGUAGE AS THE ISSUE DESCRIPTION"},
		{"block tag", "```design-proposal"},
		{"blocked protocol", "status:\"blocked\""},
		{"no fabrication", "NEVER fabricate design content"},
		{"depends_on ordering", "depends_on"},
	}
	for _, m := range musts {
		if !strings.Contains(got, m.substr) {
			t.Errorf("design_proposal recipe missing %s (%q)", m.label, m.substr)
		}
	}
	// It must NOT tell the agent to open a PR / merge (it's analysis only).
	if strings.Contains(got, "open a pull request") || strings.Contains(got, "Open a pull request") {
		t.Error("design_proposal must not instruct the agent to open a PR")
	}
}

func TestDesignProposalNotPRProducing(t *testing.T) {
	if sliceActionOpensPR(sliceActionDesignProposal) {
		t.Error("design_proposal must not be a PR-producing action")
	}
	if isQASliceAction(sliceActionDesignProposal) {
		t.Error("design_proposal is not a QA-family action")
	}
	if !isKnownSliceActionKind(sliceActionDesignProposal) {
		t.Error("design_proposal must be a known slice action kind")
	}
}
