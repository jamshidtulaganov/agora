package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The assistant's INTEGRATION ROSTER — one read tool, list_integrations, that
// answers "what is this workspace connected to" from the same rows Settings →
// Integrations renders its badges from.
//
// Why this is a tool at all. Asked "is GitHub connected?", a model with no
// integration tool answers from the shape of the conversation — which is to
// say it guesses, and a wrong guess here is expensive in both directions: it
// either sends someone to reconnect something that already works, or it tells
// them a connector is live when nothing has ever been set up. The roster is
// small, cheap and entirely derived from rows the caller could already fetch
// themselves from the settings endpoints, so there is no reason for the model
// to be uncertain about it.
//
// Three rules shape everything below:
//
//  1. NO SECRET EVER LEAVES THIS FILE. Not a token, not a token's last four
//     digits, not an auth header name or value, not a webhook URL, not an MCP
//     server's `url` or `env`. The conversation is persisted (see
//     assistant.ExcludedCapabilities), so anything this tool returns outlives
//     the chat. `detail` carries counts and human labels only, and
//     TestAssistantListIntegrationsNeverLeaksSecretMaterial asserts that on the
//     marshaled JSON rather than on the struct, because the struct is not what
//     reaches the model.
//
//  2. THE STATUS PROBES ARE THE SETTINGS ENDPOINTS' OWN. Each connector below
//     reads exactly the query its member-visible status endpoint reads
//     (GetFigmaCredentialForWorkspace, ListReleaseIntegrationsByWorkspace,
//     GetZohoConnectionForWorkspace, ListTelegramInstallations,
//     LarkInstallations.ListByWorkspace, ListGitHubInstallationsByWorkspace,
//     ListGitCredentials, ListMcpCredentials + the agent list). Inventing a
//     second definition of "connected" is how the assistant and the settings
//     page start disagreeing in front of the user.
//
//  3. ENV GATES DOWNGRADE TO "unavailable", NOT TO "not_connected". Bitrix,
//     Zoho, Lark, Telegram bots, GitHub, Figma and Release each need an
//     instance-level env key before ANY connection can succeed. Reporting one
//     of those as merely "not connected" walks the user into a dead end: they
//     go to Settings, find no card (the tab hides env-gated connectors — see
//     integrations-tab.tsx), and come back. "unavailable" carries the real
//     answer, which is that an instance operator has to enable it first.
//
// Membership gates as everywhere else: the tool resolves the caller's
// membership through assistantWorkspaceScope before it reads a single row, so
// an outsider gets the standing refusal and never learns whether the workspace
// exists.

// Integration status values. Four, not three: the first three are the product
// states, and `unknown` exists because a probe can fail.
//
// A failed read is NOT "not connected" — that answer would send someone to
// reconnect a working integration — and it is not "unavailable" either, which
// means something specific (the operator has not enabled it). One connector
// whose query errored must not take the whole roster down with it, so it
// reports what is actually true: nobody knows right now.
const (
	assistantIntegrationConnected    = "connected"
	assistantIntegrationNotConnected = "not_connected"
	assistantIntegrationUnavailable  = "unavailable"
	assistantIntegrationUnknown      = "unknown"
)

// assistantIntegrationResult is one connector as the model sees it.
//
// Where is part of the contract, not decoration: the assistant's job on this
// topic is to walk somebody to the right screen, and a status without a
// destination produces "GitHub isn't connected" followed by nothing useful.
type assistantIntegrationResult struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// Detail is a SHORT human phrase — a count, an account login, a probe
	// verdict. Never a credential, never a URL.
	Detail string `json:"detail,omitempty"`
	// Where is the exact place in the app that owns this connector.
	Where string `json:"where"`
}

// assistantIntegrationSecretsNote rides with every answer. The model has the
// same rule in its system prompt (writeIntegrationGuidance), but the prompt is
// far away by the time a roster comes back mid-conversation, and this is the
// moment the model is most likely to offer to "just paste the token here".
const assistantIntegrationSecretsNote = "statuses only — this tool never returns tokens, auth headers or webhook URLs. " +
	"Credentials are pasted into the Agora UI at the place named in `where`, never into this conversation."

func (h *Handler) assistantListIntegrations(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	workspaceID := uuidToString(ws.ID)

	// Order mirrors the product: the source-control pair first (GitHub has its
	// own settings tab), then the Integrations gallery top to bottom.
	rows := []assistantIntegrationResult{
		h.assistantGitHubIntegration(ctx, ws.ID),
		h.assistantGitAccountsIntegration(ctx, ws.ID),
		h.assistantMcpIntegration(ctx, caller, ws.ID, workspaceID, role),
		h.assistantReleaseIntegration(ctx, ws.ID),
		h.assistantFigmaIntegration(ctx, ws.ID),
		h.assistantBitrixIntegration(),
		h.assistantZohoIntegration(ctx, ws.ID),
		h.assistantTelegramIntegration(ctx, ws.ID),
		h.assistantLarkIntegration(ctx, ws.ID),
	}

	return json.Marshal(map[string]any{
		"workspace_id":   workspaceID,
		"workspace_slug": ws.Slug,
		"your_role":      role,
		"integrations":   rows,
		// Connecting anything is owner/admin work in the product. A plain
		// member asking "connect Figma" should be told who can, rather than
		// walked through a form the server will refuse.
		"can_manage":    roleAllowed(role, "owner", "admin"),
		"status_values": []string{assistantIntegrationConnected, assistantIntegrationNotConnected, assistantIntegrationUnavailable, assistantIntegrationUnknown},
		"status_note": "connected = set up in this workspace; not_connected = available but nothing set up yet; " +
			"unavailable = the instance operator has not enabled it (nothing the user can fix from Settings → Integrations); " +
			"unknown = the status could not be read on this call, so do not claim either way.",
		"secrets_note": assistantIntegrationSecretsNote,
	})
}

// assistantIntegrationProbeFailed logs and downgrades one connector, leaving
// the rest of the roster intact.
func assistantIntegrationProbeFailed(key, name, where string, err error) assistantIntegrationResult {
	slog.Warn("assistant: integration probe failed", "integration", key, "error", err)
	return assistantIntegrationResult{
		Key:    key,
		Name:   name,
		Status: assistantIntegrationUnknown,
		Detail: "status could not be read just now",
		Where:  where,
	}
}

// ---------------------------------------------------------------------------
// Source control
// ---------------------------------------------------------------------------

const assistantWhereGitHub = "Settings → GitHub"

// assistantGitHubIntegration mirrors ListGitHubInstallations: `configured` is
// the instance gate (the GitHub App slug + webhook secret), and connection is
// the presence of at least one installation row. The account LOGIN is a public
// GitHub handle and the same string the settings tab prints, so it is the one
// identifying detail that ships.
func (h *Handler) assistantGitHubIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "github", Name: "GitHub", Where: assistantWhereGitHub}
	if !isGitHubConfigured() {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "the GitHub App is not configured on this Agora instance"
		return row
	}
	installs, err := h.Queries.ListGitHubInstallationsByWorkspace(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	if len(installs) == 0 {
		row.Status = assistantIntegrationNotConnected
		row.Detail = "no GitHub account or organisation installed"
		return row
	}
	row.Status = assistantIntegrationConnected
	logins := make([]string, 0, len(installs))
	for _, install := range installs {
		logins = append(logins, install.AccountLogin)
	}
	row.Detail = fmt.Sprintf("%s installed: %s", assistantCountLabel(len(installs), "account"), strings.Join(logins, ", "))
	return row
}

// assistantGitAccountsIntegration mirrors ListGitCredentials — the per-owner
// PATs the daemon matches a repo against. The tokens are write-only in the
// product and the list query never selects them; host/owner pairs are public
// repository coordinates, but they are reported as a COUNT plus the distinct
// hosts rather than as a list, because an owner list is org structure the
// model has no use for here.
func (h *Handler) assistantGitAccountsIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "git_accounts", Name: "Git accounts", Where: "Settings → Repositories → Git accounts"}
	creds, err := h.Queries.ListGitCredentials(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	if len(creds) == 0 {
		row.Status = assistantIntegrationNotConnected
		row.Detail = "no git account tokens saved — private repos on other accounts cannot be cloned"
		return row
	}
	hosts := map[string]bool{}
	ordered := []string{}
	for _, cred := range creds {
		if cred.Host == "" || hosts[cred.Host] {
			continue
		}
		hosts[cred.Host] = true
		ordered = append(ordered, cred.Host)
	}
	row.Status = assistantIntegrationConnected
	row.Detail = assistantCountLabel(len(creds), "git account")
	if len(ordered) > 0 {
		row.Detail += " on " + strings.Join(ordered, ", ")
	}
	return row
}

// ---------------------------------------------------------------------------
// MCP
// ---------------------------------------------------------------------------

// assistantMcpIntegration counts the MCP servers the workspace's agents are
// actually configured with, plus the workspace-level sealed credentials.
//
// The gallery card for MCP is a launcher with no badge, so there is no status
// to mirror — but "are my agents wired to any MCP servers" is a real question
// with a real answer, and it is the same pair of reads the MCP admin panel
// makes (agentListOptions + mcpCredentialsOptions).
//
// Only the COUNT ships. An agent's mcp_config holds `env` values, auth headers
// and remote URLs; even the server NAMES are withheld here, because nothing in
// the guidance needs them and a name list is one refactor away from someone
// deciding the URL beside it would be helpful too.
//
// accessibleAgentIDs applies the private-agent gate, so the count is what THIS
// caller may see rather than what exists.
func (h *Handler) assistantMcpIntegration(ctx context.Context, caller assistantCaller, wsUUID pgtype.UUID, workspaceID, role string) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "mcp", Name: "MCP servers", Where: "Settings → Integrations → MCP servers"}

	creds, err := h.Queries.ListMcpCredentials(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	allowed, ok := h.accessibleAgentIDs(ctx, workspaceID, "member", caller.ID, role)
	if !ok {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, errors.New("could not resolve agent access"))
	}
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}

	servers := map[string]bool{}
	agentsWithServers := 0
	for _, agent := range agents {
		if agent.ArchivedAt.Valid {
			continue
		}
		if _, permitted := allowed[uuidToString(agent.ID)]; !permitted {
			continue
		}
		names := assistantMcpServerNames(agent.McpConfig)
		if len(names) == 0 {
			continue
		}
		agentsWithServers++
		for _, name := range names {
			servers[name] = true
		}
	}

	if len(servers) == 0 && len(creds) == 0 {
		row.Status = assistantIntegrationNotConnected
		row.Detail = "no MCP servers configured on any agent you can see"
		return row
	}
	row.Status = assistantIntegrationConnected
	parts := []string{fmt.Sprintf("%s across %s",
		assistantCountLabel(len(servers), "MCP server"),
		assistantCountLabel(agentsWithServers, "agent"))}
	if len(creds) > 0 {
		parts = append(parts, assistantCountLabel(len(creds), "server")+" with sealed auth")
	}
	row.Detail = strings.Join(parts, "; ")
	return row
}

// assistantMcpServerNames pulls the keys of the `mcpServers` object — the same
// shape validateMcpConfigShape enforces. Malformed JSON counts as no servers
// rather than failing the roster: this is a status line, not a validator.
//
// The names are used to DE-DUPLICATE a count and never returned.
func assistantMcpServerNames(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var doc struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	names := make([]string, 0, len(doc.McpServers))
	for name := range doc.McpServers {
		names = append(names, name)
	}
	return names
}

// ---------------------------------------------------------------------------
// The Integrations gallery
// ---------------------------------------------------------------------------

// assistantReleaseIntegration mirrors ListReleaseIntegrations. The sealed
// webhook URL and signing secret are never selected by that query, so only the
// kind and the enabled flag are available here — which is all the roster needs.
//
// The env gate is the AT-REST key: without AGORA_RELEASE_SECRET_KEY no
// integration can be created (CreateReleaseIntegration answers 503), so an
// instance without it reports unavailable rather than inviting a doomed setup.
func (h *Handler) assistantReleaseIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "release", Name: "Release", Where: "Settings → Integrations → Release"}
	rows, err := h.Queries.ListReleaseIntegrationsByWorkspace(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	if len(rows) > 0 {
		enabled := 0
		for _, integration := range rows {
			if integration.Enabled {
				enabled++
			}
		}
		row.Status = assistantIntegrationConnected
		row.Detail = fmt.Sprintf("%s, %d enabled", assistantCountLabel(len(rows), "release integration"), enabled)
		return row
	}
	if _, boxErr := releaseIntegrationBox(); boxErr != nil {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "release integrations are not enabled on this Agora instance"
		return row
	}
	row.Status = assistantIntegrationNotConnected
	row.Detail = "no release integration configured"
	return row
}

// assistantFigmaIntegration mirrors GetFigmaCredentialStatus. The label is the
// user's own name for the credential and the probe verdict is a health word —
// token_last4 is deliberately NOT carried, even though the settings endpoint
// returns it: four digits of a token is still token material, and a transcript
// is the wrong place for it.
func (h *Handler) assistantFigmaIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "figma", Name: "Figma", Where: "Settings → Integrations → Figma"}
	if _, boxErr := figmaCredentialBox(); boxErr != nil {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "Figma credentials cannot be stored on this Agora instance"
		return row
	}
	cred, err := h.Queries.GetFigmaCredentialForWorkspace(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			row.Status = assistantIntegrationNotConnected
			row.Detail = "no Figma access token saved for this workspace"
			return row
		}
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	row.Status = assistantIntegrationConnected
	details := []string{}
	if label := strings.TrimSpace(cred.Label); label != "" {
		details = append(details, label)
	}
	if probe := strings.TrimSpace(cred.ProbeStatus); probe != "" {
		details = append(details, "probe: "+probe)
	}
	if cred.ExpiresAt.Valid {
		details = append(details, "expires "+cred.ExpiresAt.Time.UTC().Format("2006-01-02"))
	}
	row.Detail = strings.Join(details, ", ")
	if row.Detail == "" {
		row.Detail = "access token saved"
	}
	return row
}

// assistantBitrixIntegration is the one connector with NO per-workspace
// connection row anywhere — Bitrix is wired at the instance level
// (BITRIX_WEBHOOK_URL) and the workspace surface is an import browser, which is
// exactly why the gallery card carries no status badge.
//
// So the two honest answers are "the operator has not enabled it" and "it is
// enabled here, and the import browser is where you use it". The detail says
// which of those it is in words, so the model never reports a workgroup as
// mirrored on the strength of a green status.
func (h *Handler) assistantBitrixIntegration() assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "bitrix", Name: "Bitrix24", Where: "Settings → Integrations → Bitrix24"}
	if !bitrixEndpointsEnabled() {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "Bitrix24 is not enabled on this Agora instance"
		return row
	}
	row.Status = assistantIntegrationConnected
	row.Detail = "enabled on this instance — Bitrix has no per-workspace connection; open the import browser to mirror workgroups"
	return row
}

// assistantZohoIntegration mirrors GetZohoConnectionStatus. The data centre and
// the probe verdict travel; the client id, the scopes and the org ids do not —
// none of them help the user connect anything, and client_id in a saved
// transcript is half of an OAuth pair.
func (h *Handler) assistantZohoIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "zoho", Name: "Zoho", Where: "Settings → Integrations → Zoho"}
	if !zohoConfigured() {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "Zoho is not enabled on this Agora instance"
		return row
	}
	conn, err := h.Queries.GetZohoConnectionForWorkspace(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			row.Status = assistantIntegrationNotConnected
			row.Detail = "no Zoho connection for this workspace"
			return row
		}
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	row.Status = assistantIntegrationConnected
	details := []string{}
	if dc := strings.TrimSpace(conn.Dc); dc != "" {
		details = append(details, "data centre "+dc)
	}
	if probe := strings.TrimSpace(conn.ProbeStatus); probe != "" {
		details = append(details, "probe: "+probe)
	}
	row.Detail = strings.Join(details, ", ")
	if row.Detail == "" {
		row.Detail = "connected"
	}
	return row
}

// assistantTelegramIntegration mirrors the Integrations card exactly, including
// the correction that card already carries: `configured` only means the server
// holds a seal key, so connection is the presence of an ACTIVE per-agent
// installation. Bot usernames are public handles but are still withheld — the
// count is the answer to "is Telegram set up".
func (h *Handler) assistantTelegramIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "telegram", Name: "Telegram", Where: "Settings → Integrations → Telegram"}
	if _, sealErr := telegramSealBox(); sealErr != nil {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "agent Telegram bots are not enabled on this Agora instance"
		return row
	}
	installs, err := h.Queries.ListTelegramInstallations(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	active := 0
	for _, install := range installs {
		if install.Status == "active" {
			active++
		}
	}
	if active == 0 {
		row.Status = assistantIntegrationNotConnected
		row.Detail = "no active agent bots"
		return row
	}
	row.Status = assistantIntegrationConnected
	row.Detail = assistantCountLabel(active, "active agent bot")
	return row
}

// assistantLarkIntegration mirrors ListLarkInstallations, whose `configured`
// flag is the wiring of the installation service itself (the deployment's
// at-rest key). A nil service is the same answer the HTTP endpoint gives.
func (h *Handler) assistantLarkIntegration(ctx context.Context, wsUUID pgtype.UUID) assistantIntegrationResult {
	row := assistantIntegrationResult{Key: "lark", Name: "Lark", Where: "Settings → Integrations → Lark"}
	if h.LarkInstallations == nil || strings.TrimSpace(os.Getenv("AGORA_LARK_SECRET_KEY")) == "" {
		row.Status = assistantIntegrationUnavailable
		row.Detail = "Lark is not enabled on this Agora instance"
		return row
	}
	installs, err := h.LarkInstallations.ListByWorkspace(ctx, wsUUID)
	if err != nil {
		return assistantIntegrationProbeFailed(row.Key, row.Name, row.Where, err)
	}
	active := 0
	for _, install := range installs {
		if install.Status == "active" {
			active++
		}
	}
	if active == 0 {
		row.Status = assistantIntegrationNotConnected
		row.Detail = "no active Lark bots"
		return row
	}
	row.Status = assistantIntegrationConnected
	row.Detail = assistantCountLabel(active, "active Lark bot")
	return row
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// assistantCountLabel renders "1 agent" / "3 agents". The model relays these
// phrases verbatim, so the plural is worth getting right here rather than
// leaving it to be reworded downstream.
func assistantCountLabel(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
