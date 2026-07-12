// Package githubrelease is a DB-free client for the GitHub Release connector
// (release-hub Thread B / Phase 4). On a shipped sprint it creates a GitHub
// Release via the REST API, authenticating with a sealed personal-access token
// (PAT). The PAT path is chosen over the App-installation-token exchange
// (handler.signGitHubAppJWT) for simplicity and generality: a PAT works for any
// repo the token can see without wiring an App installation per workspace.
//
// Like integrations/releasehook it depends on nothing from the handler/service
// layers so it can be unit-tested against httptest servers without a database.
package githubrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIBase is the base URL for GitHub's REST API. Mutable so tests can point the
// client at an httptest server without touching real GitHub.
var APIBase = "https://api.github.com"

const (
	maxResponseBytes = 1 << 20 // 1 MiB — cap a hostile/huge response body
	defaultTimeout   = 15 * time.Second
)

// Client creates GitHub Releases with a PAT.
type Client struct {
	http *http.Client
}

// NewClient builds a Client with a bounded HTTP timeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: defaultTimeout}}
}

// Release is the payload for POST /repos/{owner}/{repo}/releases. TargetCommitish
// is optional (GitHub defaults it to the repo's default branch) and omitted when
// empty.
type Release struct {
	TagName         string `json:"tag_name"`
	Name            string `json:"name"`
	Body            string `json:"body"`
	TargetCommitish string `json:"target_commitish,omitempty"`
}

// CreateRelease POSTs a new release to owner/repo authenticated by the PAT.
// A non-2xx response returns an error carrying the status (the caller logs it);
// the body is drained up to maxResponseBytes. Never panics.
func (c *Client) CreateRelease(ctx context.Context, owner, repo, token string, rel Release) error {
	buf, err := json.Marshal(rel)
	if err != nil {
		return fmt.Errorf("githubrelease: marshal release: %w", err)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases", APIBase, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("githubrelease: build request: %w", err)
	}
	c.authHeaders(req, token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("githubrelease: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("githubrelease: http %d", resp.StatusCode)
	}
	return nil
}

// ValidateToken performs a lightweight authed GET on the repo to check the PAT
// at save time WITHOUT creating anything. Returns the HTTP status (0 with
// reachable=false on a transport error/timeout). Classification into
// ok/invalid/unreachable lives in the handler so it stays unit-testable.
func (c *Client) ValidateToken(ctx context.Context, owner, repo, token string) (status int, reachable bool) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s", APIBase, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false
	}
	c.authHeaders(req, token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, true
}

// authHeaders sets the GitHub REST auth + versioning headers shared by every
// call.
func (c *Client) authHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
}
