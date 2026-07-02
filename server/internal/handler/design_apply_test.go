package handler

import (
	"strings"
	"testing"
)

func sampleAudit() designAudit {
	var a designAudit
	a.ProposedTokens = []struct {
		Name     string   `json:"name"`
		Value    string   `json:"value"`
		Replaces []string `json:"replaces"`
	}{
		{Name: "primary", Value: "#2563EB", Replaces: []string{"#3b82f6", "#3B82F6"}},
	}
	a.Duplicates = []struct {
		Pattern            string   `json:"pattern"`
		Occurrences        int      `json:"occurrences"`
		SuggestedComponent string   `json:"suggested_component"`
		SampleRefs         []string `json:"sample_refs"`
	}{
		{Pattern: "data table", Occurrences: 9, SuggestedComponent: "SdGrid", SampleRefs: []string{"src/a.vue:12", "src/b.vue:30"}},
	}
	return a
}

func TestComposeCodemodIssue_Token(t *testing.T) {
	title, desc := composeCodemodIssue(sampleAudit(), applyDesignAuditRequest{Kind: "token", Index: 0})
	if title != "Adopt design token: primary" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"`primary` = `#2563EB`", "`#3b82f6`", "`#3B82F6`", "appearance must NOT change", "Open a pull request"} {
		if !strings.Contains(desc, want) {
			t.Errorf("token desc missing %q:\n%s", want, desc)
		}
	}
}

func TestComposeCodemodIssue_Component(t *testing.T) {
	title, desc := composeCodemodIssue(sampleAudit(), applyDesignAuditRequest{Kind: "component", Index: 0})
	if title != "Extract shared component: SdGrid" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"`SdGrid`", "data table", "9 occurrences", "src/a.vue:12", "appearance identical", "Open a pull request"} {
		if !strings.Contains(desc, want) {
			t.Errorf("component desc missing %q:\n%s", want, desc)
		}
	}
}

func TestComposeCodemodIssue_OutOfRange(t *testing.T) {
	audit := sampleAudit()
	if title, _ := composeCodemodIssue(audit, applyDesignAuditRequest{Kind: "token", Index: 5}); title != "" {
		t.Error("out-of-range token index must yield empty title")
	}
	if title, _ := composeCodemodIssue(audit, applyDesignAuditRequest{Kind: "component", Index: 5}); title != "" {
		t.Error("out-of-range component index must yield empty title")
	}
	if title, _ := composeCodemodIssue(audit, applyDesignAuditRequest{Kind: "bogus", Index: 0}); title != "" {
		t.Error("unknown kind must yield empty title")
	}
}

func TestDesignAuditBlockRe(t *testing.T) {
	content := "text\n```design-audit\n{\"proposed_tokens\":[{\"name\":\"primary\"}]}\n```\nmore"
	m := designAuditBlockRe.FindStringSubmatch(content)
	if m == nil || !strings.Contains(m[1], "proposed_tokens") {
		t.Fatalf("block not extracted: %v", m)
	}
}
