package handler

import (
	"encoding/json"
	"testing"
)

func TestDeployedRefFromEnvelope(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantBranch string
		wantSHA    string
	}{
		{
			name:       "gitlab push hook",
			payload:    `{"object_kind":"push","ref":"refs/heads/dev","after":"abc123def456","checkout_sha":"abc123def456"}`,
			wantBranch: "dev",
			wantSHA:    "abc123def456",
		},
		{
			name:       "gitlab pipeline hook uses checkout_sha",
			payload:    `{"object_kind":"pipeline","ref":"refs/heads/main","checkout_sha":"ffee0011"}`,
			wantBranch: "main",
			wantSHA:    "ffee0011",
		},
		{
			name:       "gitlab deploy hook uses sha + environment",
			payload:    `{"object_kind":"deployment","environment":"dev","sha":"9911aabb","ref":"dev"}`,
			wantBranch: "dev",
			wantSHA:    "9911aabb",
		},
		{
			name:       "bare branch ref (no refs/heads prefix)",
			payload:    `{"ref":"feature/x","after":"77aa"}`,
			wantBranch: "feature/x",
			wantSHA:    "77aa",
		},
		// Boundary: malformed / unexpected shapes must not panic — the regression
		// still runs, just unlabelled (empty branch/sha).
		{name: "empty object", payload: `{}`, wantBranch: "", wantSHA: ""},
		{name: "missing fields", payload: `{"object_kind":"note"}`, wantBranch: "", wantSHA: ""},
		{name: "wrong types are ignored", payload: `{"ref":123,"after":true}`, wantBranch: "", wantSHA: ""},
		{name: "non-object json", payload: `[1,2,3]`, wantBranch: "", wantSHA: ""},
		{name: "not json at all", payload: `not json`, wantBranch: "", wantSHA: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := WebhookEnvelope{EventPayload: json.RawMessage(tc.payload)}
			branch, sha := deployedRefFromEnvelope(env)
			if branch != tc.wantBranch {
				t.Errorf("branch = %q, want %q", branch, tc.wantBranch)
			}
			if sha != tc.wantSHA {
				t.Errorf("sha = %q, want %q", sha, tc.wantSHA)
			}
		})
	}
}
