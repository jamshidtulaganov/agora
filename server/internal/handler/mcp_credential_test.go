package handler

import (
	"encoding/json"
	"testing"
)

// remoteHeadersOf pulls mcpServers.<name>.headers out of a config for assertions.
func remoteHeadersOf(t *testing.T, cfg json.RawMessage, name string) (headers map[string]string, url string, typ string) {
	t.Helper()
	var parsed struct {
		McpServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Command string            `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	e := parsed.McpServers[name]
	return e.Headers, e.URL, e.Type
}

func TestMergeMcpCredentialHeaders_FillsBlankAuthHeader(t *testing.T) {
	// mcp_config stores the header key with a blank placeholder value; the
	// sealed credential supplies the real value at dispatch.
	in := json.RawMessage(`{"mcpServers":{"linear":{"type":"http","url":"https://mcp.linear.app/mcp","headers":{"Authorization":""}}}}`)
	byName := map[string]map[string]string{
		"linear": {"Authorization": "Bearer secret-token"},
	}
	out, merged := mergeMcpCredentialHeaders(in, byName)
	if !merged {
		t.Fatal("expected merge")
	}
	headers, url, typ := remoteHeadersOf(t, out, "linear")
	if headers["Authorization"] != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", headers["Authorization"])
	}
	if url != "https://mcp.linear.app/mcp" || typ != "http" {
		t.Errorf("url/type not preserved: url=%q type=%q", url, typ)
	}
}

func TestMergeMcpCredentialHeaders_ProvisionsHeadersWhenAbsent(t *testing.T) {
	// A remote entry with no headers object at all still gets the sealed header.
	in := json.RawMessage(`{"mcpServers":{"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}}`)
	byName := map[string]map[string]string{"linear": {"Authorization": "Bearer tok"}}
	out, merged := mergeMcpCredentialHeaders(in, byName)
	if !merged {
		t.Fatal("expected merge")
	}
	headers, _, _ := remoteHeadersOf(t, out, "linear")
	if headers["Authorization"] != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", headers["Authorization"])
	}
}

func TestMergeMcpCredentialHeaders_SealedValueWins(t *testing.T) {
	// A value already present in mcp_config is overwritten by the sealed one
	// (the sealed store is authoritative for its keys).
	in := json.RawMessage(`{"mcpServers":{"linear":{"type":"http","url":"https://x/mcp","headers":{"Authorization":"stale"}}}}`)
	byName := map[string]map[string]string{"linear": {"Authorization": "Bearer fresh"}}
	out, _ := mergeMcpCredentialHeaders(in, byName)
	headers, _, _ := remoteHeadersOf(t, out, "linear")
	if headers["Authorization"] != "Bearer fresh" {
		t.Errorf("Authorization = %q, want Bearer fresh", headers["Authorization"])
	}
}

func TestMergeMcpCredentialHeaders_PreservesOtherHeadersAndServers(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{` +
		`"linear":{"type":"http","url":"https://x/mcp","headers":{"Authorization":"","X-Env":"prod"}},` +
		`"local":{"command":"npx","args":["-y","srv"]}` +
		`}}`)
	byName := map[string]map[string]string{"linear": {"Authorization": "Bearer tok"}}
	out, merged := mergeMcpCredentialHeaders(in, byName)
	if !merged {
		t.Fatal("expected merge")
	}
	headers, _, _ := remoteHeadersOf(t, out, "linear")
	if headers["Authorization"] != "Bearer tok" || headers["X-Env"] != "prod" {
		t.Errorf("headers not preserved/merged: %+v", headers)
	}
	// The stdio server is untouched.
	var parsed struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	_ = json.Unmarshal(out, &parsed)
	if _, ok := parsed.McpServers["local"]; !ok {
		t.Error("stdio server 'local' was dropped")
	}
}

func TestMergeMcpCredentialHeaders_SkipsStdioEntryWithMatchingName(t *testing.T) {
	// A stdio entry (no url) that coincidentally matches a credential name must
	// NOT be given headers — auth headers belong only to remote entries.
	in := json.RawMessage(`{"mcpServers":{"linear":{"command":"npx","args":["-y","x"]}}}`)
	byName := map[string]map[string]string{"linear": {"Authorization": "Bearer tok"}}
	out, merged := mergeMcpCredentialHeaders(in, byName)
	if merged {
		t.Error("expected no merge for a stdio entry")
	}
	if string(out) != string(in) {
		t.Errorf("config mutated: %s", out)
	}
}

func TestMergeMcpCredentialHeaders_NoMatchingServer(t *testing.T) {
	in := json.RawMessage(`{"mcpServers":{"other":{"type":"http","url":"https://x/mcp"}}}`)
	byName := map[string]map[string]string{"linear": {"Authorization": "Bearer tok"}}
	out, merged := mergeMcpCredentialHeaders(in, byName)
	if merged {
		t.Error("expected no merge when no server name matches")
	}
	if string(out) != string(in) {
		t.Errorf("config mutated: %s", out)
	}
}

func TestMergeMcpCredentialHeaders_MalformedInputUnchanged(t *testing.T) {
	byName := map[string]map[string]string{"linear": {"Authorization": "Bearer tok"}}
	for _, in := range []json.RawMessage{
		json.RawMessage(`not json`),
		json.RawMessage(`null`),
		json.RawMessage(`{"mcpServers":null}`),
		json.RawMessage(`{}`),
	} {
		out, merged := mergeMcpCredentialHeaders(in, byName)
		if merged {
			t.Errorf("expected no merge for %s", in)
		}
		if string(out) != string(in) {
			t.Errorf("config mutated for %s -> %s", in, out)
		}
	}
}

func TestLast4(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"ab":                "ab",
		"abcd":              "abcd",
		"Bearer abcdef1234": "1234",
	}
	for in, want := range cases {
		if got := last4(in); got != want {
			t.Errorf("last4(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrimaryHeaderValue(t *testing.T) {
	// Deterministic: first key by sorted order.
	got := primaryHeaderValue(map[string]string{"X-Zed": "z", "Authorization": "a"})
	if got != "a" {
		t.Errorf("primaryHeaderValue = %q, want a", got)
	}
	if primaryHeaderValue(map[string]string{}) != "" {
		t.Error("empty map should yield empty string")
	}
}
