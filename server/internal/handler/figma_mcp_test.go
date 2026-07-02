package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

// figmaEnvOf pulls mcpServers.figma out of a config payload for assertions.
func figmaEnvOf(t *testing.T, cfg json.RawMessage) (env map[string]string, args []string) {
	t.Helper()
	var parsed struct {
		McpServers struct {
			Figma struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"figma"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	return parsed.McpServers.Figma.Env, parsed.McpServers.Figma.Args
}

func TestMergeFigmaMcpEnv_FillsAbsentKey(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"figma":{"command":"npx","args":["-y","figma-developer-mcp","--stdio"]}}}`)
	out, merged := mergeFigmaMcpEnv(in, "figd_token123")
	if !merged {
		t.Fatal("expected merge")
	}
	env, _ := figmaEnvOf(t, out)
	if env["FIGMA_API_KEY"] != "figd_token123" {
		t.Errorf("FIGMA_API_KEY = %q, want figd_token123", env["FIGMA_API_KEY"])
	}
}

func TestMergeFigmaMcpEnv_FillsEmptyStringKey(t *testing.T) {
	// The UI template stores {"FIGMA_API_KEY": ""} when the operator follows
	// the documented "leave the key blank" flow (buildServerEntry keeps empty
	// VALUES) — an empty string is blank and MUST be filled.
	in := json.RawMessage(`{"mcpServers":{"figma":{"command":"npx","env":{"FIGMA_API_KEY":""}}}}`)
	out, merged := mergeFigmaMcpEnv(in, "workspace_token")
	if !merged {
		t.Fatal("expected merge for empty-string key")
	}
	env, _ := figmaEnvOf(t, out)
	if env["FIGMA_API_KEY"] != "workspace_token" {
		t.Errorf("FIGMA_API_KEY = %q, want workspace_token (empty string is blank)", env["FIGMA_API_KEY"])
	}
}

func TestMergeFigmaMcpEnv_OperatorValueWins(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"figma":{"command":"npx","env":{"FIGMA_API_KEY":"operator_token"}}}}`)
	out, merged := mergeFigmaMcpEnv(in, "workspace_token")
	if merged {
		t.Fatal("must not merge over an operator-set token")
	}
	env, _ := figmaEnvOf(t, out)
	if env["FIGMA_API_KEY"] != "operator_token" {
		t.Errorf("FIGMA_API_KEY = %q, want operator_token (operator value must win)", env["FIGMA_API_KEY"])
	}
}

func TestMergeFigmaMcpEnv_MalformedOrNullUnchanged(t *testing.T) {
	// JSON nulls are valid JSON: json.Unmarshal nils the target map without
	// an error, and a later assignment would panic the claim endpoint. Every
	// null shape must pass through unchanged instead.
	for _, in := range []string{
		`{not json`,
		`null`,
		`{"mcpServers":null}`,
		`{"mcpServers":{"figma":null}}`,
		`{"mcpServers":{"github":{"command":"npx"}}}`, // no figma server
	} {
		out, merged := mergeFigmaMcpEnv(json.RawMessage(in), "tok")
		if merged || string(out) != in {
			t.Errorf("input %q must pass through unchanged (merged=%v)", in, merged)
		}
	}
}

func TestMergeFigmaMcpEnv_NullEnvTreatedAsEmpty(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"figma":{"command":"npx","env":null}}}`)
	out, merged := mergeFigmaMcpEnv(in, "tok")
	if !merged {
		t.Fatal("env:null should read as empty env and be filled")
	}
	env, _ := figmaEnvOf(t, out)
	if env["FIGMA_API_KEY"] != "tok" {
		t.Errorf("FIGMA_API_KEY = %q, want tok", env["FIGMA_API_KEY"])
	}
}

func TestProvisionFigmaMcpServer_SynthesizesFromEmpty(t *testing.T) {
	// The MUL-348 case: the agent has NO mcp_config at all. Provisioning must
	// synthesize the entire document, not bail like the Lark injector does.
	out, provisioned := provisionFigmaMcpServer(nil, "figd_tok")
	if !provisioned {
		t.Fatal("expected provisioning from an empty config")
	}
	env, args := figmaEnvOf(t, out)
	if env["FIGMA_API_KEY"] != "figd_tok" {
		t.Errorf("FIGMA_API_KEY = %q, want figd_tok", env["FIGMA_API_KEY"])
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "figma-developer-mcp@"+figmaMcpVersion) {
		t.Errorf("args = %q, want pinned figma-developer-mcp@%s", joined, figmaMcpVersion)
	}
	if !strings.Contains(joined, "--stdio") || !strings.Contains(joined, "--no-telemetry") {
		t.Errorf("args = %q, want --stdio --no-telemetry", joined)
	}
}

func TestProvisionFigmaMcpServer_NullShapesRecovered(t *testing.T) {
	// `null` / {"mcpServers":null} unmarshal to nil maps without an error —
	// the pre-fix code panicked with "assignment to entry in nil map" INSIDE
	// the claim endpoint, after the task was already claimed in the DB.
	for _, in := range []string{`null`, `{"mcpServers":null}`} {
		out, provisioned := provisionFigmaMcpServer(json.RawMessage(in), "tok")
		if !provisioned {
			t.Fatalf("input %q: expected provisioning into re-initialized maps", in)
		}
		env, _ := figmaEnvOf(t, out)
		if env["FIGMA_API_KEY"] != "tok" {
			t.Errorf("input %q: FIGMA_API_KEY = %q, want tok", in, env["FIGMA_API_KEY"])
		}
	}
}

func TestProvisionFigmaMcpServer_PreservesOtherServers(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"gh_tok"}}},"other":"field"}`)
	out, provisioned := provisionFigmaMcpServer(in, "figd_tok")
	if !provisioned {
		t.Fatal("expected provisioning")
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(parsed["other"]) != `"field"` {
		t.Errorf("top-level field lost: %s", parsed["other"])
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(parsed["mcpServers"], &servers); err != nil {
		t.Fatalf("invalid servers: %v", err)
	}
	if _, ok := servers["github"]; !ok {
		t.Error("github server lost during provisioning")
	}
	if _, ok := servers["figma"]; !ok {
		t.Error("figma server not added")
	}
}

func TestProvisionFigmaMcpServer_ExistingFigmaUntouched(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"figma":{"command":"custom"}}}`)
	out, provisioned := provisionFigmaMcpServer(in, "figd_tok")
	if provisioned {
		t.Fatal("must not overwrite an existing figma entry")
	}
	if string(out) != string(in) {
		t.Errorf("existing figma entry must pass through unchanged")
	}
}

func TestProvisionFigmaMcpServer_MalformedUnchanged(t *testing.T) {
	in := json.RawMessage(`[1,2,3]`)
	out, provisioned := provisionFigmaMcpServer(in, "tok")
	if provisioned || string(out) != string(in) {
		t.Errorf("malformed config must pass through unchanged")
	}
}

func TestMcpConfigHasServer(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want bool
	}{
		{"empty", "", false},
		{"no servers key", `{}`, false},
		{"present", `{"mcpServers":{"figma":{}}}`, true},
		{"absent", `{"mcpServers":{"github":{}}}`, false},
		{"malformed", `{oops`, false},
		{"null servers", `{"mcpServers":null}`, false},
		{"null root", `null`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpConfigHasServer(json.RawMessage(tt.cfg), "figma"); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestApplyFigmaMcp covers the full injection decision matrix — the pure core
// injectFigmaMcpCreds delegates to after resolving the credential.
func TestApplyFigmaMcp(t *testing.T) {
	operatorEntry := `{"mcpServers":{"figma":{"command":"npx","env":{"FIGMA_API_KEY":"op_tok"}}}}`
	blankEntry := `{"mcpServers":{"figma":{"command":"npx","env":{"FIGMA_API_KEY":""}}}}`

	tests := []struct {
		name          string
		cfg           string
		hasRefs       bool
		cred          figmaCredState
		wantAvailable bool
		wantNote      string
		wantProvision bool
		wantSynth     bool
		wantMerged    bool
	}{
		{"no refs, no entry, ok cred → untouched", `{}`, false, figmaCredOK, false, "", false, false, false},
		{"refs, empty config, ok cred → synthesized", ``, true, figmaCredOK, true, "", true, true, false},
		{"refs, plain config, ok cred → provisioned", `{}`, true, figmaCredOK, true, "", true, false, false},
		{"refs, blank-key entry, ok cred → merged", blankEntry, true, figmaCredOK, true, "", false, false, true},
		{"refs, operator entry, ok cred → available, not merged", operatorEntry, true, figmaCredOK, true, "", false, false, false},
		{"refs, no entry, missing cred → missing note", `{}`, true, figmaCredMissing, false, figmaMissingCredentialNote, false, false, false},
		{"refs, no entry, expired cred → expired note", `{}`, true, figmaCredExpired, false, figmaExpiredCredentialNote, false, false, false},
		{"refs, operator entry, missing cred → available via operator key, no note", operatorEntry, true, figmaCredMissing, true, "", false, false, false},
		{"refs, blank-key entry, missing cred → dark + missing note", blankEntry, true, figmaCredMissing, false, figmaMissingCredentialNote, false, false, false},
		{"no refs, operator entry, missing cred → available, no note", operatorEntry, false, figmaCredMissing, true, "", false, false, false},
		{"no refs, no entry, missing cred → silent", `{}`, false, figmaCredMissing, false, "", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := applyFigmaMcp(json.RawMessage(tt.cfg), tt.hasRefs, "ws_tok", tt.cred)
			if res.Available != tt.wantAvailable {
				t.Errorf("Available = %v, want %v", res.Available, tt.wantAvailable)
			}
			if res.Note != tt.wantNote {
				t.Errorf("Note = %q, want %q", res.Note, tt.wantNote)
			}
			if res.Provisioned != tt.wantProvision {
				t.Errorf("Provisioned = %v, want %v", res.Provisioned, tt.wantProvision)
			}
			if res.Synthesized != tt.wantSynth {
				t.Errorf("Synthesized = %v, want %v", res.Synthesized, tt.wantSynth)
			}
			if res.Merged != tt.wantMerged {
				t.Errorf("Merged = %v, want %v", res.Merged, tt.wantMerged)
			}
		})
	}
}

func TestApplyFigmaMcp_ExpiredNeverInjectsToken(t *testing.T) {
	res := applyFigmaMcp(json.RawMessage(`{}`), true, "ws_tok", figmaCredExpired)
	if strings.Contains(string(res.Config), "ws_tok") {
		t.Fatal("expired credential must never reach the config")
	}
	res = applyFigmaMcp(json.RawMessage(`{"mcpServers":{"figma":{"command":"npx"}}}`), true, "ws_tok", figmaCredExpired)
	if strings.Contains(string(res.Config), "ws_tok") {
		t.Fatal("expired credential must never be merged into an entry")
	}
}
