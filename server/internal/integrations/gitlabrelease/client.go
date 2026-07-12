// Package gitlabrelease is a DB-free client for the GitLab Release connector
// (release-hub Thread B / Phase 4). On a shipped sprint it creates a GitLab
// Release via the REST API, authenticating with a sealed personal-access token
// (the PRIVATE-TOKEN header). The token + host travel in the sealed secret; the
// project path is non-secret config. A self-contained sealed token is used
// rather than reusing gitCredentialBox so a release integration is fully
// described by its own row.
//
// Like integrations/releasehook it depends on nothing from the handler/service
// layers so it can be unit-tested against httptest servers without a database.
package gitlabrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxResponseBytes = 1 << 20 // 1 MiB
	defaultTimeout   = 15 * time.Second
	// DefaultHost is the SaaS GitLab host used when the sealed secret omits one.
	DefaultHost = "gitlab.com"
)

// Client creates GitLab Releases with a PAT.
type Client struct {
	http *http.Client
}

// NewClient builds a Client with a bounded HTTP timeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: defaultTimeout}}
}

// Release is the payload for POST /projects/{id}/releases.
type Release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// baseURL turns a configured host into an API base URL. A bare host defaults to
// https ("gitlab.com" → "https://gitlab.com"); an explicit scheme is honored
// (so a self-hosted "https://gitlab.example.com" — or an http test server — is
// respected). An empty host falls back to the SaaS default.
func baseURL(host string) string {
	host = strings.TrimSuffix(strings.TrimSpace(host), "/")
	if host == "" {
		return "https://" + DefaultHost
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

// CreateRelease POSTs a new release for the url-encoded projectPath (group/repo)
// on host, authenticated by the PAT. A non-2xx response returns an error
// carrying the status; the body is drained up to maxResponseBytes. Never panics.
func (c *Client) CreateRelease(ctx context.Context, host, projectPath, token string, rel Release) error {
	buf, err := json.Marshal(rel)
	if err != nil {
		return fmt.Errorf("gitlabrelease: marshal release: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/releases", baseURL(host), url.PathEscape(strings.TrimSpace(projectPath)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("gitlabrelease: build request: %w", err)
	}
	c.authHeaders(req, token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitlabrelease: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlabrelease: http %d", resp.StatusCode)
	}
	return nil
}

// ValidateToken performs a lightweight authed GET on the project to check the
// PAT at save time WITHOUT creating anything. Returns the HTTP status (0 with
// reachable=false on a transport error/timeout).
func (c *Client) ValidateToken(ctx context.Context, host, projectPath, token string) (status int, reachable bool) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s", baseURL(host), url.PathEscape(strings.TrimSpace(projectPath)))
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

func (c *Client) authHeaders(req *http.Request, token string) {
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
}
