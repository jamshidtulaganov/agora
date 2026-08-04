package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
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

// figmaTestCredential seals a token with the process's figma credential box
// and inserts the workspace row directly (bypassing the PUT endpoint's live
// /v1/me probe). Mirrors gitlabTestCredential's pattern. Skips the test when
// the box was already initialized without a key earlier in the process
// (figmaBoxOnce is a sync.Once).
func figmaTestCredential(t *testing.T, ctx context.Context, token string) {
	t.Helper()
	t.Setenv("AGORA_FIGMA_SECRET_KEY", figmaTestKey(t))
	resetFigmaBox()
	t.Cleanup(resetFigmaBox)
	box, err := figmaCredentialBox()
	if err != nil {
		t.Skipf("figma credential box unavailable in this process: %v", err)
	}
	sealed, err := box.Seal([]byte(token))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	last4 := token
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	_, err = testHandler.Queries.UpsertFigmaCredential(ctx, db.UpsertFigmaCredentialParams{
		WorkspaceID:    testUUID(testWorkspaceID),
		Label:          "figma-mcp-test",
		TokenEncrypted: sealed,
		TokenLast4:     last4,
		TokenKind:      "pat",
		SeatProbe:      "unknown",
		ProbeStatus:    "ok",
	})
	if err != nil {
		t.Fatalf("insert figma credential: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM figma_credential WHERE workspace_id = $1`, testWorkspaceID)
	})
}

// figmaTestIssueWithDesignRef creates an issue whose description references a
// Figma design, returning the loaded row so injectFigmaMcpCreds sees a
// non-empty issueFigmaRefs.
func figmaTestIssueWithDesignRef(t *testing.T, ctx context.Context) db.Issue {
	t.Helper()
	issueID := createTestIssue(t, "figma mcp inject", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	if _, err := testPool.Exec(ctx, `UPDATE issue SET description = $1 WHERE id = $2`,
		"Design ref: https://www.figma.com/design/TestFileKey01/dashboard?node-id=1-10", issueID); err != nil {
		t.Fatalf("set description: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	return issue
}

// TestInjectFigmaMcpCreds_Presence covers the DB-touching wrapper
// injectFigmaMcpCreds end-to-end (not just the pure applyFigmaMcp core it
// delegates to): a sealed workspace credential + an issue whose description
// references a Figma design must result in the figma MCP server being
// auto-provisioned with the decrypted token — the shape a real claim
// (ClaimTaskByRuntime) hands the agent.
func TestInjectFigmaMcpCreds_Presence(t *testing.T) {
	ctx := context.Background()
	figmaTestCredential(t, ctx, "figd_real_test_token")
	issue := figmaTestIssueWithDesignRef(t, ctx)

	res := testHandler.injectFigmaMcpCreds(ctx, "agent-1", issue, nil)
	if !res.Available {
		t.Fatalf("expected Available=true, note=%q", res.Note)
	}
	if !res.Provisioned || !res.Synthesized {
		t.Errorf("expected the whole mcpServers document to be synthesized, got provisioned=%v synthesized=%v", res.Provisioned, res.Synthesized)
	}
	if !mcpConfigHasServer(res.Config, "figma") {
		t.Fatalf("config missing figma server: %s", res.Config)
	}
	env, args := figmaEnvOf(t, res.Config)
	if env["FIGMA_API_KEY"] != "figd_real_test_token" {
		t.Errorf("FIGMA_API_KEY = %q, want the decrypted workspace token", env["FIGMA_API_KEY"])
	}
	wantArg := "figma-developer-mcp@" + figmaMcpVersion
	found := false
	for _, a := range args {
		if a == wantArg {
			found = true
		}
	}
	if !found {
		t.Errorf("args %v missing pinned %q", args, wantArg)
	}
}
