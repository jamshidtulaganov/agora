// Package sentry is a DB-free client for the Sentry Release connector
// (release-hub Thread B / Phase 4). On a shipped sprint it creates a Sentry
// release and an associated deploy, authenticating with a sealed API token
// (Bearer). The token + base URL travel in the sealed secret (base URL is
// overridable for self-hosted Sentry); the org + project are non-secret config.
//
// Like integrations/releasehook it depends on nothing from the handler/service
// layers so it can be unit-tested against httptest servers without a database.
package sentry

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
	// DefaultBaseURL is the SaaS Sentry base used when the sealed secret omits one.
	DefaultBaseURL = "https://sentry.io"
)

// Client creates Sentry releases + deploys with an API token.
type Client struct {
	http *http.Client
}

// NewClient builds a Client with a bounded HTTP timeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: defaultTimeout}}
}

func normalizeBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"))
	if baseURL == "" {
		return DefaultBaseURL
	}
	return baseURL
}

type releaseBody struct {
	Version  string   `json:"version"`
	Projects []string `json:"projects"`
}

type deployBody struct {
	Environment string `json:"environment"`
}

// CreateReleaseAndDeploy creates the release (POST /organizations/{org}/releases/)
// then registers a deploy for it (POST .../releases/{version}/deploys/) so the
// Sentry release timeline shows the environment it shipped to. A non-2xx on
// either call returns an error carrying the status; both bodies are drained up
// to maxResponseBytes. Never panics.
func (c *Client) CreateReleaseAndDeploy(ctx context.Context, baseURL, org, project, token, version, environment string) error {
	base := normalizeBaseURL(baseURL)
	if err := c.post(ctx, fmt.Sprintf("%s/api/0/organizations/%s/releases/", base, org), token, releaseBody{
		Version:  version,
		Projects: []string{project},
	}); err != nil {
		return err
	}
	// A deploy without an environment is meaningless; skip it but keep the
	// release (creating the release is the primary value).
	if strings.TrimSpace(environment) == "" {
		return nil
	}
	return c.post(ctx, fmt.Sprintf("%s/api/0/organizations/%s/releases/%s/deploys/", base, org, url.PathEscape(version)), token, deployBody{
		Environment: environment,
	})
}

// ValidateToken performs a lightweight authed GET on the org to check the token
// at save time WITHOUT creating anything. Returns the HTTP status (0 with
// reachable=false on a transport error/timeout).
func (c *Client) ValidateToken(ctx context.Context, baseURL, org, token string) (status int, reachable bool) {
	endpoint := fmt.Sprintf("%s/api/0/organizations/%s/", normalizeBaseURL(baseURL), org)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, true
}

func (c *Client) post(ctx context.Context, endpoint, token string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("sentry: marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("sentry: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sentry: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sentry: http %d", resp.StatusCode)
	}
	return nil
}
