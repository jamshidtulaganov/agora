package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// GitLab MCP auto-injection (MCP-P1, docs/deploy-mcp-integration.md §3).
//
// When a DEPLOY task is claimed, the workspace's sealed GitLab credential
// (git_credential, provider='gitlab' — migration 132, the same table the
// clone-auth path already reads via attachRepoAuth) is turned into a complete
// `gitlab` MCP server entry in the agent's per-task mcp_config, so the deploy
// agent can drive a real CI/CD pipeline with ZERO provider-specific Go client
// code. Mirrors injectFigmaMcpCreds: the full entry is built server-side —
// the human never hand-writes MCP JSON, and the PAT never sits in the
// unsealed workspace default_mcp_config column (§2.5 of the doc: why the
// Figma-style pattern and not the static-blob or hosted-proxy ones).
//
// Least privilege is enforced at the MCP-server layer, not just prompted:
// GITLAB_TOOLSETS=pipelines exposes only the pipeline/deployment/environment
// tools, and GITLAB_PERMISSION_MODE=modify blocks every delete tool while
// allowing create_pipeline (§4.3).

// gitlabMcpVersion pins the zereight/gitlab-mcp (npm @zereight/mcp-gitlab)
// release the backend auto-provisions, so a daemon resolves a reviewed
// version instead of whatever latest published overnight.
const gitlabMcpVersion = "2.1.30"

// gitlabMissingCredentialNote is appended to a deploy agent's instructions
// when its target is a GitLab pipeline but no workspace GitLab credential
// resolves — the agent reports the misconfiguration instead of improvising
// (deploy-mcp-integration.md §7, MCP-server-down row).
const gitlabMissingCredentialNote = "NOTE: this deploy targets a GitLab CI/CD pipeline, but no GitLab credential (provider='gitlab') is configured for this workspace — the `gitlab` MCP pipeline tools are NOT available this run. Tell the user to add a GitLab personal access token (api scope) under the workspace git credentials. If a FALLBACK command is configured in your deploy target, use it; otherwise post a deploy-result block with status=\"failed\" and a summary naming the missing credential — never improvise another deploy path."

// gitlabMcpResult reports what injectGitLabMcpCreds did, mirroring
// figmaMcpResult: the claim path gates the pipeline instruction on the tools
// ACTUALLY being available this run.
type gitlabMcpResult struct {
	Config json.RawMessage
	// Available means the agent will have working gitlab MCP tools this run:
	// either we provisioned the entry from the workspace credential, or the
	// operator declared their own "gitlab" server on the agent config.
	Available bool
	// Note is a claim-time instruction appended when the tools are NOT
	// available (no credential / decrypt failure), so the agent reports the
	// gap instead of failing silently.
	Note string
}

// injectGitLabMcpCreds auto-provisions the `gitlab` MCP server entry in the
// per-task mcp_config from the workspace's sealed GitLab git_credential.
// Mirrors injectFigmaMcpCreds' conservatism:
//
//   - An operator-declared "gitlab" entry always wins — config unchanged,
//     tools assumed available (a deliberately scoped per-agent server
//     overrides the workspace credential).
//   - Credential resolution prefers the credential matching the issue's
//     project GitLab repo (host+owner, the same match attachRepoAuth does),
//     then any credential on the same host, then any provider='gitlab'
//     credential in the workspace.
//   - No credential / sealed-box unavailable / decrypt failure → config
//     unchanged + a note so the agent reports the gap.
//   - Any malformed-JSON path returns the input unchanged — a claim never
//     fails because of GitLab wiring.
//
// Callers gate this on the task actually being a deploy slice-action
// (taskTriggerIsDeploy): pipeline tools are not blanket-attached to every
// task's MCP surface (deploy-mcp-integration.md §3, call-site note).
func (h *Handler) injectGitLabMcpCreds(ctx context.Context, agentID string, issue db.Issue, mcpConfig json.RawMessage) gitlabMcpResult {
	if mcpConfigHasServer(mcpConfig, "gitlab") {
		return gitlabMcpResult{Config: mcpConfig, Available: true}
	}

	host, owner := h.issueGitLabRepoHostOwner(ctx, issue)
	creds, err := h.Queries.GetGitCredentialsForWorkspace(ctx, issue.WorkspaceID)
	if err != nil || len(creds) == 0 {
		return gitlabMcpResult{Config: mcpConfig, Note: gitlabMissingCredentialNote}
	}
	cred, ok := matchGitLabCredential(creds, host, owner)
	if !ok {
		return gitlabMcpResult{Config: mcpConfig, Note: gitlabMissingCredentialNote}
	}
	box, err := gitCredentialBox()
	if err != nil {
		return gitlabMcpResult{Config: mcpConfig, Note: gitlabMissingCredentialNote}
	}
	plain, err := box.Open(cred.SecretEncrypted)
	if err != nil {
		// Same UX as attachRepoAuth's decrypt-failure no-op: surfaces as a
		// missing credential, never a failed claim.
		return gitlabMcpResult{Config: mcpConfig, Note: gitlabMissingCredentialNote}
	}

	out, provisioned := provisionMcpServer(mcpConfig, "gitlab", gitlabMcpServerEntry(string(plain), cred.Host))
	res := gitlabMcpResult{Config: out, Available: provisioned}
	slog.Info("gitlab mcp injection",
		"workspace_id", uuidToString(issue.WorkspaceID),
		"agent_id", agentID,
		"issue_id", uuidToString(issue.ID),
		"credential_host", cred.Host,
		"provisioned", provisioned,
	)
	return res
}

// issueGitLabRepoHostOwner resolves the lowercased host + owner of the
// issue's project GitLab repo. GitLab repos are bound as github_repo
// resources carrying a gitlab URL (that resource type is just the daemon's
// checkout trigger — see issueRepoIsGitLab). Empty strings when the project
// binds no GitLab repo; credential matching then falls back workspace-wide.
func (h *Handler) issueGitLabRepoHostOwner(ctx context.Context, issue db.Issue) (host, owner string) {
	if !issue.ProjectID.Valid {
		return "", ""
	}
	for _, row := range h.listProjectResourcesForProject(ctx, issue.ProjectID) {
		if row.ResourceType != "github_repo" {
			continue
		}
		var ref struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(row.ResourceRef, &ref) != nil {
			continue
		}
		if !strings.Contains(strings.ToLower(ref.URL), "gitlab") {
			continue
		}
		if rh, ro := parseRepoHostOwner(ref.URL); rh != "" {
			return rh, ro
		}
	}
	return "", ""
}

// matchGitLabCredential picks the workspace GitLab credential for a repo:
// exact host+owner match first (one workspace can hold PATs for several
// GitLab accounts — the same precedence attachRepoAuth's index encodes),
// then any credential on the same host, then any provider='gitlab' token
// credential at all. ok=false when the workspace holds none.
func matchGitLabCredential(creds []db.GitCredential, host, owner string) (db.GitCredential, bool) {
	var hostMatch, anyMatch *db.GitCredential
	for i := range creds {
		c := &creds[i]
		if c.Provider != "gitlab" || c.AuthKind != "token" {
			continue
		}
		if host != "" && c.Host == host {
			if owner != "" && c.Owner == owner {
				return *c, true
			}
			if hostMatch == nil {
				hostMatch = c
			}
		}
		if anyMatch == nil {
			anyMatch = c
		}
	}
	if hostMatch != nil {
		return *hostMatch, true
	}
	if anyMatch != nil {
		return *anyMatch, true
	}
	return db.GitCredential{}, false
}

// gitlabMcpServerEntry builds the pinned zereight/gitlab-mcp stdio entry
// (deploy-mcp-integration.md §3): self-hosted GitLab via GITLAB_API_URL,
// pipeline toolset only, modify mode (create/update allowed, deletes blocked
// at the MCP-server layer). Pure so tests assert the exact provisioned shape.
func gitlabMcpServerEntry(token, host string) map[string]any {
	return map[string]any{
		"command": "npx",
		"args":    []string{"-y", "@zereight/mcp-gitlab@" + gitlabMcpVersion},
		"env": map[string]string{
			"GITLAB_PERSONAL_ACCESS_TOKEN": token,
			"GITLAB_API_URL":               "https://" + host + "/api/v4",
			"GITLAB_PERMISSION_MODE":       "modify",
			"GITLAB_TOOLSETS":              "pipelines",
		},
	}
}

// provisionMcpServer adds a named server entry to an mcp_config, synthesizing
// the whole {"mcpServers":{…}} document when the config is empty and
// re-initializing JSON-null maps (json.Unmarshal nils a map on a literal null
// without an error — assigning into it would panic the claim endpoint). Pure;
// malformed input returns the original bytes with provisioned=false; an
// existing entry under the same name is never overwritten. The generic
// sibling of provisionFigmaMcpServer, for injectors whose entry is built
// entirely server-side.
func provisionMcpServer(mcpConfig json.RawMessage, name string, entry map[string]any) (out json.RawMessage, provisioned bool) {
	root := map[string]json.RawMessage{}
	if len(mcpConfig) > 0 {
		if err := json.Unmarshal(mcpConfig, &root); err != nil {
			return mcpConfig, false
		}
		if root == nil { // literal `null` config
			root = map[string]json.RawMessage{}
		}
	}
	servers := map[string]json.RawMessage{}
	if serversRaw, ok := root["mcpServers"]; ok {
		if err := json.Unmarshal(serversRaw, &servers); err != nil {
			return mcpConfig, false
		}
		if servers == nil { // "mcpServers": null
			servers = map[string]json.RawMessage{}
		}
	}
	if _, ok := servers[name]; ok {
		return mcpConfig, false
	}
	entryBytes, err := json.Marshal(entry)
	if err != nil {
		return mcpConfig, false
	}
	servers[name] = entryBytes
	serversBytes, err := json.Marshal(servers)
	if err != nil {
		return mcpConfig, false
	}
	root["mcpServers"] = serversBytes
	doc, err := json.Marshal(root)
	if err != nil {
		return mcpConfig, false
	}
	return doc, true
}
