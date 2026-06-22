package handler

import "testing"

func TestApplyIssueCostTier(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		thinking     string
		labels       []string
		wantModel    string
		wantThinking string
	}{
		{"no labels: passthrough", "claude-opus-4-8", "medium", nil, "claude-opus-4-8", "medium"},
		{"1m stripped by default", "claude-opus-4-8[1m]", "medium", nil, "claude-opus-4-8", "medium"},
		{"1m kept when context:large", "claude-opus-4-8[1m]", "medium", []string{"context:large"}, "claude-opus-4-8[1m]", "medium"},
		{"tier:light -> sonnet, no thinking", "claude-opus-4-8[1m]", "high", []string{"tier:light"}, "claude-sonnet-4-6", ""},
		{"tier:trivial -> haiku, no thinking", "claude-opus-4-8", "high", []string{"tier:trivial"}, "claude-haiku-4-5-20251001", ""},
		{"trivial beats light", "claude-opus-4-8[1m]", "high", []string{"tier:light", "tier:trivial"}, "claude-haiku-4-5-20251001", ""},
		{"light + context:large keeps sonnet (no 1m to strip)", "claude-opus-4-8[1m]", "high", []string{"tier:light", "context:large"}, "claude-sonnet-4-6", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := make(map[string]bool, len(tt.labels))
			for _, l := range tt.labels {
				set[l] = true
			}
			m, th := applyIssueCostTier(tt.model, tt.thinking, set)
			if m != tt.wantModel || th != tt.wantThinking {
				t.Errorf("applyIssueCostTier(%q, %q, %v) = (%q, %q), want (%q, %q)",
					tt.model, tt.thinking, tt.labels, m, th, tt.wantModel, tt.wantThinking)
			}
		})
	}
}
