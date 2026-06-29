package handler

import (
	"encoding/json"
	"testing"
)

// larkEnvOf pulls mcpServers.lark.env out of a config payload for assertions.
func larkEnvOf(t *testing.T, cfg json.RawMessage) map[string]string {
	t.Helper()
	var parsed struct {
		McpServers struct {
			Lark struct {
				Env map[string]string `json:"env"`
			} `json:"lark"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	return parsed.McpServers.Lark.Env
}

func TestMergeLarkMcpEnv_FillsBlanks(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"lark":{"command":"npx","args":["-y","@larksuiteoapi/lark-mcp","mcp"]}}}`)
	out := mergeLarkMcpEnv(in, "cli_app123", "secret456", "https://open.feishu.cn")

	env := larkEnvOf(t, out)
	if env["APP_ID"] != "cli_app123" {
		t.Errorf("APP_ID = %q, want cli_app123", env["APP_ID"])
	}
	if env["APP_SECRET"] != "secret456" {
		t.Errorf("APP_SECRET = %q, want secret456", env["APP_SECRET"])
	}
	if env["LARK_DOMAIN"] != "https://open.feishu.cn" {
		t.Errorf("LARK_DOMAIN = %q, want https://open.feishu.cn", env["LARK_DOMAIN"])
	}
}

func TestMergeLarkMcpEnv_OperatorValuesWin(t *testing.T) {
	// A standalone Lark app: operator already set APP_ID/APP_SECRET. The bound
	// Bot creds must NOT clobber them; LARK_DOMAIN is still filled (was blank).
	in := json.RawMessage(`{"mcpServers":{"lark":{"command":"npx","env":{"APP_ID":"operator_app","APP_SECRET":"operator_secret"}}}}`)
	out := mergeLarkMcpEnv(in, "bound_app", "bound_secret", "https://open.larksuite.com")

	env := larkEnvOf(t, out)
	if env["APP_ID"] != "operator_app" {
		t.Errorf("APP_ID = %q, want operator_app (operator value must win)", env["APP_ID"])
	}
	if env["APP_SECRET"] != "operator_secret" {
		t.Errorf("APP_SECRET = %q, want operator_secret (operator value must win)", env["APP_SECRET"])
	}
	if env["LARK_DOMAIN"] != "https://open.larksuite.com" {
		t.Errorf("LARK_DOMAIN = %q, want it filled when blank", env["LARK_DOMAIN"])
	}
}

func TestMergeLarkMcpEnv_NoLarkServer_Unchanged(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"github":{"command":"npx"}}}`)
	out := mergeLarkMcpEnv(in, "cli_app123", "secret456", "https://open.feishu.cn")
	if string(out) != string(in) {
		t.Errorf("config without a lark server must be returned unchanged.\n got: %s\nwant: %s", out, in)
	}
}

func TestMergeLarkMcpEnv_PreservesOtherServers(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"ghp_x"}},"lark":{"command":"npx"}}}`)
	out := mergeLarkMcpEnv(in, "cli_app123", "secret456", "https://open.feishu.cn")

	var parsed struct {
		McpServers struct {
			GitHub struct {
				Env map[string]string `json:"env"`
			} `json:"github"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if parsed.McpServers.GitHub.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "ghp_x" {
		t.Errorf("github server env was not preserved: %s", out)
	}
	// And lark still got filled.
	if larkEnvOf(t, out)["APP_ID"] != "cli_app123" {
		t.Errorf("lark APP_ID not filled alongside preserved github server")
	}
}

func TestMergeLarkMcpEnv_MalformedJSON_Unchanged(t *testing.T) {
	in := json.RawMessage(`{"mcpServers": this is not json`)
	out := mergeLarkMcpEnv(in, "cli_app123", "secret456", "https://open.feishu.cn")
	if string(out) != string(in) {
		t.Errorf("malformed input must be returned unchanged, got %s", out)
	}
}

func TestMergeLarkMcpEnv_NoMcpServersKey_Unchanged(t *testing.T) {
	in := json.RawMessage(`{"somethingElse":true}`)
	out := mergeLarkMcpEnv(in, "cli_app123", "secret456", "https://open.feishu.cn")
	if string(out) != string(in) {
		t.Errorf("config without mcpServers must be returned unchanged, got %s", out)
	}
}
