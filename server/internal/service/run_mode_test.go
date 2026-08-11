package service

import "testing"

func TestNormalizeAgentRunMode(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", AgentRunModeAuto, true},
		{" AUTO ", AgentRunModeAuto, true},
		{"Debug", AgentRunModeDebug, true},
		{"plan", AgentRunModePlan, true},
		{"build", AgentRunModeBuild, true},
		{"turbo", "", false},
	}
	for _, test := range tests {
		got, ok := NormalizeAgentRunMode(test.raw)
		if got != test.want || ok != test.ok {
			t.Errorf("NormalizeAgentRunMode(%q) = (%q, %v), want (%q, %v)", test.raw, got, ok, test.want, test.ok)
		}
	}
}
