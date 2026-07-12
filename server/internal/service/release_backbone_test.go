package service

import "testing"

func TestDeployEnvIsProductionTier(t *testing.T) {
	settings := []byte(`{"deploy_environments":[
		{"key":"staging","label":"Staging"},
		{"key":"production","label":"Production","requires_human":true},
		{"key":"prod-eu","label":"Prod EU","requires_human":true},
		{"key":"canary","label":"Canary"}
	]}`)

	tests := []struct {
		name string
		raw  []byte
		key  string
		want bool
	}{
		{"requires_human production", settings, "production", true},
		{"requires_human custom key", settings, "prod-eu", true},
		{"staging is not production", settings, "staging", false},
		{"canary flag-less is not production", settings, "canary", false},
		{"production-named without flag", []byte(`{"deploy_environments":[{"key":"production"}]}`), "production", true},
		{"prod-named without flag", []byte(`{"deploy_environments":[{"key":"prod"}]}`), "prod", true},
		{"unknown key", settings, "nope", false},
		{"empty key", settings, "", false},
		{"malformed settings", []byte(`{not json`), "production", false},
		{"no deploy_environments", []byte(`{}`), "production", false},
		{"case-insensitive key match", settings, "PRODUCTION", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deployEnvIsProductionTier(tt.raw, tt.key); got != tt.want {
				t.Errorf("deployEnvIsProductionTier(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestSprintRowVerdict(t *testing.T) {
	tests := []struct {
		name     string
		qaFail   bool
		qaPass   bool
		runsFail int64
		want     string
	}{
		{"qa fail label", true, false, 0, "fail"},
		{"failing run overrides pass", false, true, 2, "fail"},
		{"clean pass", false, true, 0, "pass"},
		{"no signal is pending", false, false, 0, "pending"},
		{"pass label with a failing run is fail", false, true, 1, "fail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sprintRowVerdict(tt.qaFail, tt.qaPass, tt.runsFail); got != tt.want {
				t.Errorf("sprintRowVerdict(%v,%v,%d) = %q, want %q", tt.qaFail, tt.qaPass, tt.runsFail, got, tt.want)
			}
		})
	}
}
