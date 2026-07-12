package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestGitlabMcpServerEntry asserts the exact provisioned shape: pinned npm
// package, self-hosted API URL from the credential's host, and the
// least-privilege knobs (pipelines toolset, modify mode) enforced at the
// MCP-server layer — not just prompted (deploy-mcp-integration.md §4.3).
func TestGitlabMcpServerEntry(t *testing.T) {
	entry := gitlabMcpServerEntry("glpat-secret", "gitlab.sdteam.uz")
	if entry["command"] != "npx" {
		t.Errorf("command = %v, want npx", entry["command"])
	}
	args, ok := entry["args"].([]string)
	if !ok || len(args) != 2 || args[1] != "@zereight/mcp-gitlab@"+gitlabMcpVersion {
		t.Errorf("args = %v, want pinned @zereight/mcp-gitlab", entry["args"])
	}
	env, ok := entry["env"].(map[string]string)
	if !ok {
		t.Fatalf("env has unexpected type: %T", entry["env"])
	}
	want := map[string]string{
		"GITLAB_PERSONAL_ACCESS_TOKEN": "glpat-secret",
		"GITLAB_API_URL":               "https://gitlab.sdteam.uz/api/v4",
		"GITLAB_PERMISSION_MODE":       "modify",
		"GITLAB_TOOLSETS":              "pipelines",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
}

// TestProvisionMcpServer covers the pure provisioning core: synthesizing the
// whole document from an empty config, preserving unrelated servers, never
// clobbering an existing entry, and returning malformed input unchanged.
func TestProvisionMcpServer(t *testing.T) {
	entry := map[string]any{"command": "npx"}

	t.Run("synthesizes from an empty config", func(t *testing.T) {
		out, provisioned := provisionMcpServer(nil, "gitlab", entry)
		if !provisioned {
			t.Fatal("expected provisioned=true")
		}
		if !mcpConfigHasServer(out, "gitlab") {
			t.Errorf("provisioned config missing gitlab entry: %s", out)
		}
	})

	t.Run("preserves unrelated servers and top-level fields", func(t *testing.T) {
		in := json.RawMessage(`{"mcpServers":{"zoho":{"type":"http","url":"https://x/mcp"}},"other":1}`)
		out, provisioned := provisionMcpServer(in, "gitlab", entry)
		if !provisioned {
			t.Fatal("expected provisioned=true")
		}
		if !mcpConfigHasServer(out, "zoho") || !mcpConfigHasServer(out, "gitlab") {
			t.Errorf("expected both servers present: %s", out)
		}
		if !strings.Contains(string(out), `"other":1`) {
			t.Errorf("top-level field dropped: %s", out)
		}
	})

	t.Run("never overwrites an existing entry", func(t *testing.T) {
		in := json.RawMessage(`{"mcpServers":{"gitlab":{"command":"custom"}}}`)
		out, provisioned := provisionMcpServer(in, "gitlab", entry)
		if provisioned {
			t.Error("expected provisioned=false for an existing entry")
		}
		if string(out) != string(in) {
			t.Errorf("config changed: %s", out)
		}
	})

	t.Run("malformed input returns unchanged", func(t *testing.T) {
		in := json.RawMessage(`{not json`)
		out, provisioned := provisionMcpServer(in, "gitlab", entry)
		if provisioned || string(out) != string(in) {
			t.Errorf("expected untouched malformed input, got provisioned=%v out=%s", provisioned, out)
		}
	})

	t.Run("JSON-null maps are reinitialized", func(t *testing.T) {
		out, provisioned := provisionMcpServer(json.RawMessage(`{"mcpServers":null}`), "gitlab", entry)
		if !provisioned || !mcpConfigHasServer(out, "gitlab") {
			t.Errorf("expected provisioning over a null mcpServers, got provisioned=%v out=%s", provisioned, out)
		}
	})
}

// TestMatchGitLabCredential: exact host+owner beats host-only beats any
// provider='gitlab' row; non-gitlab and non-token rows never match.
func TestMatchGitLabCredential(t *testing.T) {
	creds := []db.GitCredential{
		{Provider: "github", Host: "github.com", Owner: "acme", AuthKind: "token"},
		{Provider: "gitlab", Host: "gitlab.other.io", Owner: "misc", AuthKind: "token"},
		{Provider: "gitlab", Host: "gitlab.sdteam.uz", Owner: "other-group", AuthKind: "token"},
		{Provider: "gitlab", Host: "gitlab.sdteam.uz", Owner: "salesdoctor", AuthKind: "token"},
		{Provider: "gitlab", Host: "gitlab.ssh.io", Owner: "x", AuthKind: "ssh"},
	}

	t.Run("host+owner exact match wins", func(t *testing.T) {
		c, ok := matchGitLabCredential(creds, "gitlab.sdteam.uz", "salesdoctor")
		if !ok || c.Owner != "salesdoctor" {
			t.Errorf("expected the salesdoctor credential, got ok=%v %+v", ok, c)
		}
	})

	t.Run("host-only match when the owner differs", func(t *testing.T) {
		c, ok := matchGitLabCredential(creds, "gitlab.sdteam.uz", "unknown-group")
		if !ok || c.Host != "gitlab.sdteam.uz" {
			t.Errorf("expected a same-host credential, got ok=%v %+v", ok, c)
		}
	})

	t.Run("workspace-wide fallback when no repo host resolves", func(t *testing.T) {
		c, ok := matchGitLabCredential(creds, "", "")
		if !ok || c.Provider != "gitlab" || c.AuthKind != "token" {
			t.Errorf("expected any gitlab token credential, got ok=%v %+v", ok, c)
		}
	})

	t.Run("github and ssh rows never match", func(t *testing.T) {
		only := []db.GitCredential{
			{Provider: "github", Host: "github.com", Owner: "acme", AuthKind: "token"},
			{Provider: "gitlab", Host: "gitlab.ssh.io", Owner: "x", AuthKind: "ssh"},
		}
		if _, ok := matchGitLabCredential(only, "gitlab.ssh.io", "x"); ok {
			t.Error("expected no match from github/ssh rows")
		}
	})
}

// gitlabTestCredential seals a PAT with the process's git credential box and
// inserts the row. Skips the test when the box was already initialized
// without a key earlier in the process (gitCredentialBox is a sync.Once).
func gitlabTestCredential(t *testing.T, ctx context.Context, host, owner, token string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("AGORA_GIT_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	box, err := gitCredentialBox()
	if err != nil {
		t.Skipf("git credential box unavailable in this process: %v", err)
	}
	sealed, err := box.Seal([]byte(token))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	var credID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO git_credential (workspace_id, label, provider, host, owner, username, auth_kind, secret_encrypted, created_by)
		VALUES ($1, $2, 'gitlab', $3, $4, '', 'token', $5, $6)
		RETURNING id
	`, testWorkspaceID, owner, host, owner, sealed, testUserID).Scan(&credID); err != nil {
		t.Fatalf("insert git credential: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM git_credential WHERE id = $1`, credID)
	})
}

// gitlabTestIssueWithRepo creates an issue in a project bound to a GitLab
// repo resource, returning the loaded issue row.
func gitlabTestIssueWithRepo(t *testing.T, ctx context.Context, repoURL string) db.Issue {
	t.Helper()
	issueID := createTestIssue(t, "gitlab mcp inject", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status) VALUES ($1, 'gitlab-mcp-test', 'in_progress') RETURNING id`,
		testWorkspaceID,
	).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
	if repoURL != "" {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, created_by)
			VALUES ($1, $2, 'github_repo', $3::jsonb, $4)
		`, projectID, testWorkspaceID, `{"url":"`+repoURL+`"}`, testUserID); err != nil {
			t.Fatalf("insert project resource: %v", err)
		}
	}
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $1 WHERE id = $2`, projectID, issueID); err != nil {
		t.Fatalf("link issue to project: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	return issue
}

// TestInjectGitLabMcpCreds_Presence: with a sealed provider='gitlab'
// credential matching the project repo's host, the claim path provisions the
// full gitlab entry — decrypted PAT in env, host-derived API URL.
func TestInjectGitLabMcpCreds_Presence(t *testing.T) {
	ctx := context.Background()
	issue := gitlabTestIssueWithRepo(t, ctx, "https://gitlab.sdteam.uz/salesdoctor/sd-main.git")
	gitlabTestCredential(t, ctx, "gitlab.sdteam.uz", "salesdoctor", "glpat-test-token")

	res := testHandler.injectGitLabMcpCreds(ctx, "agent-1", issue, nil)
	if !res.Available {
		t.Fatalf("expected Available=true, note=%q", res.Note)
	}
	if !mcpConfigHasServer(res.Config, "gitlab") {
		t.Fatalf("config missing gitlab server: %s", res.Config)
	}
	cfg := string(res.Config)
	for _, want := range []string{
		"glpat-test-token",
		"https://gitlab.sdteam.uz/api/v4",
		`"GITLAB_TOOLSETS":"pipelines"`,
		`"GITLAB_PERMISSION_MODE":"modify"`,
		"@zereight/mcp-gitlab@" + gitlabMcpVersion,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("provisioned config missing %q: %s", want, cfg)
		}
	}
}

// TestInjectGitLabMcpCreds_Absence: no provider='gitlab' credential → config
// unchanged, tools unavailable, and the claim-time note tells the agent to
// report the gap instead of improvising.
func TestInjectGitLabMcpCreds_Absence(t *testing.T) {
	ctx := context.Background()
	issue := gitlabTestIssueWithRepo(t, ctx, "https://gitlab.sdteam.uz/salesdoctor/sd-main.git")

	in := json.RawMessage(`{"mcpServers":{"zoho":{"type":"http","url":"https://x/mcp"}}}`)
	res := testHandler.injectGitLabMcpCreds(ctx, "agent-1", issue, in)
	if res.Available {
		t.Error("expected Available=false without a gitlab credential")
	}
	if string(res.Config) != string(in) {
		t.Errorf("config must be unchanged, got %s", res.Config)
	}
	if res.Note != gitlabMissingCredentialNote {
		t.Errorf("expected the missing-credential note, got %q", res.Note)
	}
}

// TestInjectGitLabMcpCreds_OperatorEntryWins: an agent whose config already
// declares a "gitlab" server is left untouched — a deliberately scoped
// per-agent server overrides the workspace credential.
func TestInjectGitLabMcpCreds_OperatorEntryWins(t *testing.T) {
	ctx := context.Background()
	issue := gitlabTestIssueWithRepo(t, ctx, "https://gitlab.sdteam.uz/salesdoctor/sd-main.git")

	in := json.RawMessage(`{"mcpServers":{"gitlab":{"command":"custom-gitlab-mcp"}}}`)
	res := testHandler.injectGitLabMcpCreds(ctx, "agent-1", issue, in)
	if !res.Available {
		t.Error("an operator-declared gitlab entry counts as available")
	}
	if string(res.Config) != string(in) {
		t.Errorf("operator config must be untouched, got %s", res.Config)
	}
}
