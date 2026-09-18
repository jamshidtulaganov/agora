package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

// Tests for list_integrations — the roster tool.
//
// Three things are under test, and only the third is about integrations at all:
//
//   - The STATUS MAPPING. connected / not_connected / unavailable must each be
//     produced by the real condition the settings page uses, because the whole
//     point of the tool is that the model stops guessing. An env-gated
//     connector in particular must read "unavailable", never "not connected" —
//     the second answer sends the user to a Settings tab that does not render
//     that card at all.
//   - The MEMBERSHIP GATE, like every other workspace-scoped tool.
//   - The SECRET FLOOR. Asserted on the MARSHALED JSON rather than on the
//     struct, because the bytes are what reach the model and what gets written
//     into the persisted transcript. Seeded rows carry real sealed columns, a
//     token_last4, an MCP auth header and a remote URL, so a future field that
//     innocently forwards one of them fails here.

// assistantIntegrationRows decodes the roster into key → row.
func assistantIntegrationRows(t *testing.T, result map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := result["integrations"].([]any)
	if !ok {
		t.Fatalf("result has no integrations array: %v", result)
	}
	out := map[string]map[string]any{}
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("integration row is not an object: %v", item)
		}
		key, _ := row["key"].(string)
		out[key] = row
	}
	return out
}

func assistantIntegrationStatus(t *testing.T, result map[string]any, key string) string {
	t.Helper()
	row, ok := assistantIntegrationRows(t, result)[key]
	if !ok {
		t.Fatalf("roster has no %q connector: %v", key, result["integrations"])
	}
	status, _ := row["status"].(string)
	if where, _ := row["where"].(string); strings.TrimSpace(where) == "" {
		// A status with no destination is the failure this tool exists to
		// prevent: "Figma is not connected" and nothing the user can act on.
		t.Fatalf("%q reports no place in the app to go: %v", key, row)
	}
	return status
}

// ---------------------------------------------------------------------------
// Status mapping
// ---------------------------------------------------------------------------

// A workspace with nothing set up, on an instance with nothing enabled: every
// connector is either "available but empty" or "the operator has not turned it
// on", and the two must not be confused.
func TestAssistantListIntegrationsSeparatesEmptyFromDisabled(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-integrations-empty@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-integrations-empty-ws", "AIE")
	addAssistantTestMember(t, ws, user, "owner")

	// Every instance-level gate OFF. These read the environment on each call
	// (the two that cache behind a sync.Once are reset below).
	t.Setenv("GITHUB_APP_SLUG", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	t.Setenv("BITRIX_WEBHOOK_URL", "")
	t.Setenv("ZOHO_PROJECTS_CLIENT_ID", "")
	t.Setenv("ZOHO_PROJECTS_CLIENT_SECRET", "")
	t.Setenv("ZOHO_PROJECTS_REFRESH_TOKEN", "")
	t.Setenv("AGORA_LARK_SECRET_KEY", "")
	t.Setenv("AGORA_TELEGRAM_SECRET_KEY", "")
	t.Setenv("AGORA_FIGMA_SECRET_KEY", "")
	t.Setenv("AGORA_RELEASE_SECRET_KEY", "")
	resetFigmaBox()
	resetReleaseBox()
	t.Cleanup(func() {
		resetFigmaBox()
		resetReleaseBox()
	})

	result, err := executeAssistantTool(t, user, assistant.ToolListIntegrations, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_integrations: %v", err)
	}

	// Env-gated: the operator has to act, not the user.
	for _, key := range []string{"github", "bitrix", "zoho", "lark", "telegram", "figma", "release"} {
		if got := assistantIntegrationStatus(t, result, key); got != "unavailable" {
			t.Fatalf("%s = %q on an instance with the gate unset, want unavailable", key, got)
		}
	}
	// Not env-gated: nothing is set up, and that is something the user CAN fix.
	for _, key := range []string{"git_accounts", "mcp"} {
		if got := assistantIntegrationStatus(t, result, key); got != "not_connected" {
			t.Fatalf("%s = %q with no rows, want not_connected", key, got)
		}
	}
	if result["your_role"] != "owner" || result["can_manage"] != true {
		t.Fatalf("role reporting = %v / %v", result["your_role"], result["can_manage"])
	}
}

// The same roster with rows in place. Each connector below flips for a
// DIFFERENT reason, which is why they are asserted together rather than one
// per test: a single shared "is there a row" shortcut would pass three of
// these and be wrong about Telegram, whose card deliberately keys on an ACTIVE
// installation rather than on the instance's seal key.
func TestAssistantListIntegrationsReportsWhatIsConnected(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-integrations-full@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-integrations-full-ws", "AIF")
	addAssistantTestMember(t, ws, user, "owner")
	ctx := context.Background()

	t.Setenv("GITHUB_APP_SLUG", "agora-test")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("BITRIX_WEBHOOK_URL", "https://example.invalid/rest/1/hook/")
	t.Setenv("AGORA_TELEGRAM_SECRET_KEY", figmaTestKey(t))
	t.Setenv("AGORA_FIGMA_SECRET_KEY", figmaTestKey(t))
	resetFigmaBox()
	t.Cleanup(resetFigmaBox)

	if _, err := testPool.Exec(ctx, `
		INSERT INTO github_installation (workspace_id, installation_id, account_login, account_type)
		VALUES ($1, 987654321, 'acme-eng', 'Organization')`, ws); err != nil {
		t.Fatalf("seed github installation: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO git_credential (workspace_id, label, owner, host, secret_encrypted)
		VALUES ($1, 'Acme bot', 'acme', 'github.com', '\x6769747365637265743131'::bytea)`, ws); err != nil {
		t.Fatalf("seed git credential: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO figma_credential (workspace_id, label, token_encrypted, token_last4, probe_status)
		VALUES ($1, 'Design team PAT', '\x6669676d61746f6b656e'::bytea, 'Z9Q7', 'ok')`, ws); err != nil {
		t.Fatalf("seed figma credential: %v", err)
	}

	runtime := newAssistantTestRuntime(t, ws)
	agent := newAssistantTestAgent(t, ws, runtime, "Roster Bot", "workspace", user)
	// A realistic remote MCP entry: an auth header and a URL, both of which the
	// roster must count without ever repeating.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET mcp_config = $2::jsonb WHERE id = $1`, agent,
		`{"mcpServers":{"linear":{"type":"http","url":"https://mcp.example.invalid/sse?key=urlsecret1","headers":{"Authorization":"Bearer headersecret1"}}}}`,
	); err != nil {
		t.Fatalf("seed agent mcp_config: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO telegram_installation (workspace_id, agent_id, bot_token_encrypted, bot_username, bot_user_id, status)
		VALUES ($1, $2, '\x7467746f6b656e'::bytea, 'roster_bot', 918273645, 'active')`, ws, agent); err != nil {
		t.Fatalf("seed telegram installation: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolListIntegrations, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_integrations: %v", err)
	}
	for _, key := range []string{"github", "git_accounts", "figma", "mcp", "telegram", "bitrix"} {
		if got := assistantIntegrationStatus(t, result, key); got != "connected" {
			t.Fatalf("%s = %q after seeding, want connected", key, got)
		}
	}

	rows := assistantIntegrationRows(t, result)
	// The one identifying label that ships is the public GitHub account handle,
	// exactly as the settings tab prints it.
	if detail, _ := rows["github"]["detail"].(string); !strings.Contains(detail, "acme-eng") {
		t.Fatalf("github detail = %q, want the installed account named", detail)
	}
	// MCP reports a count, never a server name — see the file comment.
	if detail, _ := rows["mcp"]["detail"].(string); !strings.Contains(detail, "1 MCP server") {
		t.Fatalf("mcp detail = %q, want the server count", detail)
	}
	if detail, _ := rows["mcp"]["detail"].(string); strings.Contains(detail, "linear") {
		t.Fatalf("mcp detail names the server: %q", detail)
	}
}

// A revoked Telegram bot is not a connection. The Integrations card learned
// this the hard way (a green badge over an empty panel), so the tool mirrors
// the corrected rule rather than the `configured` flag.
func TestAssistantListIntegrationsIgnoresRevokedTelegramBots(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-integrations-revoked@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-integrations-revoked-ws", "AIR")
	addAssistantTestMember(t, ws, user, "owner")

	t.Setenv("AGORA_TELEGRAM_SECRET_KEY", figmaTestKey(t))
	runtime := newAssistantTestRuntime(t, ws)
	agent := newAssistantTestAgent(t, ws, runtime, "Revoked Bot", "workspace", user)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO telegram_installation (workspace_id, agent_id, bot_token_encrypted, bot_username, bot_user_id, status)
		VALUES ($1, $2, '\x7467746f6b656e32'::bytea, 'revoked_bot', 918273646, 'revoked')`, ws, agent); err != nil {
		t.Fatalf("seed telegram installation: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolListIntegrations, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_integrations: %v", err)
	}
	if got := assistantIntegrationStatus(t, result, "telegram"); got != "not_connected" {
		t.Fatalf("telegram = %q with only a revoked bot, want not_connected", got)
	}
}

// ---------------------------------------------------------------------------
// The membership gate
// ---------------------------------------------------------------------------

// The roster says what a workspace is wired to, which is exactly the kind of
// reconnaissance an outsider should not get. (The catalog-wide sweep in
// assistant_tools_test.go covers this too; this asserts the wording the model
// relays.)
func TestAssistantListIntegrationsRefusesNonMembers(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-integrations-outsider@agora.dev")
	insider := newAssistantTestUser(t, "assistant-integrations-insider@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-integrations-gate-ws", "AIG")
	addAssistantTestMember(t, ws, insider, "owner")

	_, err := executeAssistantTool(t, outsider, assistant.ToolListIntegrations, `{"workspace_id":"`+ws+`"}`)
	if err == nil {
		t.Fatal("list_integrations returned a roster to a non-member")
	}
	if err.Error() != errAssistantNoAccess.Error() {
		t.Fatalf("refusal = %q, want the standing one", err.Error())
	}
}

// ---------------------------------------------------------------------------
// The secret floor
// ---------------------------------------------------------------------------

// Every connector seeded with real secret material, and the assertion runs on
// the BYTES the model receives.
//
// The sentinels are chosen to be impossible to produce by accident: if any of
// them appears, some field is forwarding a sealed column, a token hint, an
// auth header or a capability URL into a transcript that is saved forever.
func TestAssistantListIntegrationsNeverLeaksSecretMaterial(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-integrations-secrets@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-integrations-secrets-ws", "AIS")
	addAssistantTestMember(t, ws, user, "owner")
	ctx := context.Background()

	t.Setenv("GITHUB_APP_SLUG", "agora-test")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("AGORA_FIGMA_SECRET_KEY", figmaTestKey(t))
	t.Setenv("AGORA_TELEGRAM_SECRET_KEY", figmaTestKey(t))
	t.Setenv("AGORA_RELEASE_SECRET_KEY", figmaTestKey(t))
	t.Setenv("ZOHO_PROJECTS_CLIENT_ID", "zoho-client")
	t.Setenv("ZOHO_PROJECTS_CLIENT_SECRET", "zoho-client-secret")
	t.Setenv("ZOHO_PROJECTS_REFRESH_TOKEN", "zoho-refresh")
	resetFigmaBox()
	resetReleaseBox()
	t.Cleanup(func() {
		resetFigmaBox()
		resetReleaseBox()
	})

	// token_last4 is a real column the settings endpoint returns; this tool
	// deliberately does not.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO figma_credential (workspace_id, label, token_encrypted, token_last4, probe_status)
		VALUES ($1, 'Design PAT', '\x6669676d61'::bytea, 'LAST4SENTINEL', 'ok')`, ws); err != nil {
		t.Fatalf("seed figma credential: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO git_credential (workspace_id, label, owner, host, secret_encrypted)
		VALUES ($1, 'PATSENTINEL', 'acme', 'github.com', '\x676974'::bytea)`, ws); err != nil {
		t.Fatalf("seed git credential: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO mcp_credential (workspace_id, server_name, secret_encrypted, secret_last4)
		VALUES ($1, 'linear', '\x6d6370'::bytea, 'MCPLAST4SENTINEL')`, ws); err != nil {
		t.Fatalf("seed mcp credential: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO release_integration (workspace_id, kind, config, secret_encrypted, events, enabled)
		VALUES ($1, 'webhook', '{"name":"Ship channel"}'::jsonb, '\x72656c65617365'::bytea,
		        ARRAY['release_shipped'], true)`, ws); err != nil {
		t.Fatalf("seed release integration: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO zoho_connection (workspace_id, dc, client_id, client_secret_encrypted, refresh_token_encrypted, probe_status)
		VALUES ($1, 'eu', 'ZOHOCLIENTIDSENTINEL', '\x7a63'::bytea, '\x7a72'::bytea, 'ok')`, ws); err != nil {
		t.Fatalf("seed zoho connection: %v", err)
	}

	runtime := newAssistantTestRuntime(t, ws)
	agent := newAssistantTestAgent(t, ws, runtime, "Secret Bot", "workspace", user)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET mcp_config = $2::jsonb WHERE id = $1`, agent,
		`{"mcpServers":{"linear":{"type":"http","url":"https://mcp.example.invalid/x?key=URLSECRETSENTINEL","headers":{"Authorization":"Bearer HEADERSECRETSENTINEL"}},`+
			`"local":{"command":"node","env":{"API_KEY":"ENVSECRETSENTINEL"}}}}`,
	); err != nil {
		t.Fatalf("seed agent mcp_config: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO telegram_installation (workspace_id, agent_id, bot_token_encrypted, bot_username, bot_user_id, status)
		VALUES ($1, $2, '\x7467'::bytea, 'secret_bot', 918273647, 'active')`, ws, agent); err != nil {
		t.Fatalf("seed telegram installation: %v", err)
	}

	raw, err := testHandler.Execute(ctx, user, "", assistant.ToolListIntegrations,
		json.RawMessage(`{"workspace_id":"`+ws+`"}`))
	if err != nil {
		t.Fatalf("list_integrations: %v", err)
	}
	body := string(raw)

	for _, sentinel := range []string{
		"LAST4SENTINEL",
		"MCPLAST4SENTINEL",
		"URLSECRETSENTINEL",
		"HEADERSECRETSENTINEL",
		"ENVSECRETSENTINEL",
		"ZOHOCLIENTIDSENTINEL",
		// Header NAMES are as unwelcome as their values: they are the shape of
		// the credential and have no business in a roster.
		"Authorization",
		"Bearer",
		// No URL of any kind — a sealed webhook URL is itself a capability.
		"https://",
	} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("list_integrations leaked %q into the transcript:\n%s", sentinel, body)
		}
	}

	// And it really did have something to leak: the roster is populated, so
	// this is a floor the seeded rows actually exercised.
	var result map[string]any
	if uerr := json.Unmarshal(raw, &result); uerr != nil {
		t.Fatalf("result is not a JSON object: %v", uerr)
	}
	for _, key := range []string{"figma", "git_accounts", "mcp", "release", "zoho", "telegram"} {
		if got := assistantIntegrationStatus(t, result, key); got != "connected" {
			t.Fatalf("%s = %q — the leak test must run against populated rows", key, got)
		}
	}
}
