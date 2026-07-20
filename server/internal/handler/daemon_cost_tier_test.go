package handler

import "testing"

func TestApplyIssueCostTier(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		thinking     string
		provider     string
		labels       []string
		wantModel    string
		wantThinking string
	}{
		{"no labels: passthrough", "claude-opus-4-8", "medium", "claude", nil, "claude-opus-4-8", "medium"},
		{"1m stripped by default", "claude-opus-4-8[1m]", "medium", "claude", nil, "claude-opus-4-8", "medium"},
		{"1m kept when context:large", "claude-opus-4-8[1m]", "medium", "claude", []string{"context:large"}, "claude-opus-4-8[1m]", "medium"},
		{"tier:light -> sonnet, no thinking", "claude-opus-4-8[1m]", "high", "claude", []string{"tier:light"}, "claude-sonnet-4-6", ""},
		{"tier:medium -> sonnet, no thinking (leaner default)", "claude-opus-4-8[1m]", "high", "claude", []string{"tier:medium"}, "claude-sonnet-4-6", ""},
		{"codex + tier:medium: model untouched", "gpt-5-codex", "high", "codex", []string{"tier:medium"}, "gpt-5-codex", "high"},
		{"tier:trivial -> haiku, no thinking", "claude-opus-4-8", "high", "claude", []string{"tier:trivial"}, "claude-haiku-4-5-20251001", ""},
		{"trivial beats light", "claude-opus-4-8[1m]", "high", "claude", []string{"tier:light", "tier:trivial"}, "claude-haiku-4-5-20251001", ""},
		{"light + context:large keeps sonnet (no 1m to strip)", "claude-opus-4-8[1m]", "high", "claude", []string{"tier:light", "context:large"}, "claude-sonnet-4-6", ""},
		{"tier:heavy -> opus, high thinking (raises from sonnet)", "claude-sonnet-4-6", "", "claude", []string{"tier:heavy"}, "claude-opus-4-8", "high"},
		{"trivial beats heavy (most specific first)", "claude-sonnet-4-6", "", "claude", []string{"tier:heavy", "tier:trivial"}, "claude-haiku-4-5-20251001", ""},
		// Provider gate: a non-claude runtime must keep its own model even when
		// a tier label is present — the claude ids would break the codex CLI.
		{"codex + tier:light: model untouched", "gpt-5-codex", "high", "codex", []string{"tier:light"}, "gpt-5-codex", "high"},
		{"codex + tier:trivial: model untouched", "gpt-5-codex", "medium", "codex", []string{"tier:trivial"}, "gpt-5-codex", "medium"},
		{"codex + tier:heavy: no capability raise onto codex", "gpt-5-codex", "", "codex", []string{"tier:heavy"}, "gpt-5-codex", ""},
		{"empty provider: treated as non-claude, untouched", "gpt-5-codex", "low", "", []string{"tier:light"}, "gpt-5-codex", "low"},
		{"claude case-insensitive (Claude)", "claude-opus-4-8", "high", "Claude", []string{"tier:light"}, "claude-sonnet-4-6", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := make(map[string]bool, len(tt.labels))
			for _, l := range tt.labels {
				set[l] = true
			}
			m, th := applyIssueCostTier(tt.model, tt.thinking, tt.provider, set)
			if m != tt.wantModel || th != tt.wantThinking {
				t.Errorf("applyIssueCostTier(%q, %q, %q, %v) = (%q, %q), want (%q, %q)",
					tt.model, tt.thinking, tt.provider, tt.labels, m, th, tt.wantModel, tt.wantThinking)
			}
		})
	}
}

func TestIsSmallConfirmedDiff(t *testing.T) {
	tests := []struct {
		name                          string
		additions, deletions, changed int32
		want                          bool
	}{
		{"one-line bugfix (EED-1 shape)", 1, 1, 1, true},
		{"at the threshold: 3 files, 15 lines", 10, 5, 3, true},
		{"one line over the line threshold", 8, 8, 3, false},
		{"one file over the file threshold", 1, 1, 4, false},
		{"zero-diff PR (no files changed)", 0, 0, 0, false},
		{"large diff", 200, 50, 12, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSmallConfirmedDiff(tt.additions, tt.deletions, tt.changed)
			if got != tt.want {
				t.Errorf("isSmallConfirmedDiff(%d, %d, %d) = %v, want %v",
					tt.additions, tt.deletions, tt.changed, got, tt.want)
			}
		})
	}
}

func TestApplyDiffSizeCostDowngrade(t *testing.T) {
	m, th := applyDiffSizeCostDowngrade("claude-opus-4-8", "high", "claude")
	if m != "claude-sonnet-4-6" || th != "" {
		t.Errorf("applyDiffSizeCostDowngrade(opus, high, claude) = (%q, %q), want (claude-sonnet-4-6, \"\")", m, th)
	}
	// Non-claude runtime keeps its own model — no claude id forced onto codex.
	m2, th2 := applyDiffSizeCostDowngrade("gpt-5-codex", "medium", "codex")
	if m2 != "gpt-5-codex" || th2 != "medium" {
		t.Errorf("applyDiffSizeCostDowngrade(codex) = (%q, %q), want (gpt-5-codex, medium)", m2, th2)
	}
}
