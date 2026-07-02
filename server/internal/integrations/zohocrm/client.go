// Package zohocrm is a DB-free Zoho CRM v8 client for the dynamic
// integration engine (docs/zoho-dynamic-integration.md). D1 scope:
// OAuth token minting + the metadata (discovery) surface — org, modules,
// fields. The COQL/record surface lands with the sync engine (D2).
//
// Mirrors the zohoprojects client discipline: refresh-token grant, one
// in-process cached access token (~55m TTL, Zoho tokens live 60m),
// single forced re-mint on a 401. Never refresh per request — Zoho caps
// token minting at 10/10min per client.
package zohocrm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DCHosts maps a Zoho data center id to its accounts + CRM API hosts.
// dc is load-bearing (the #1 integration footgun): a token minted on one
// DC is invalid on another, and every host differs. Never hardcode .com.
var DCHosts = map[string]struct{ Accounts, API string }{
	"us": {"https://accounts.zoho.com", "https://www.zohoapis.com"},
	"eu": {"https://accounts.zoho.eu", "https://www.zohoapis.eu"},
	"in": {"https://accounts.zoho.in", "https://www.zohoapis.in"},
	"au": {"https://accounts.zoho.com.au", "https://www.zohoapis.com.au"},
	"jp": {"https://accounts.zoho.jp", "https://www.zohoapis.jp"},
	"sa": {"https://accounts.zoho.sa", "https://www.zohoapis.sa"},
	"ca": {"https://accounts.zohocloud.ca", "https://www.zohoapis.ca"},
}

// KnownDC reports whether dc has a host mapping.
func KnownDC(dc string) bool {
	_, ok := DCHosts[dc]
	return ok
}

const accessTokenTTL = 55 * time.Minute

// Client talks to one Zoho CRM org with one OAuth grant.
type Client struct {
	clientID     string
	clientSecret string
	refreshToken string
	accountsBase string
	apiBase      string

	httpc *http.Client

	mu          sync.Mutex
	accessToken string
	fetchedAt   time.Time
}

// New builds a client for the given DC. accountsBase/apiBase override the
// DC-derived hosts when non-empty (tests point them at an httptest server).
func New(clientID, clientSecret, refreshToken, dc, accountsBase, apiBase string) (*Client, error) {
	hosts, ok := DCHosts[dc]
	if !ok {
		return nil, fmt.Errorf("zohocrm: unknown dc %q", dc)
	}
	if accountsBase == "" {
		accountsBase = hosts.Accounts
	}
	if apiBase == "" {
		apiBase = hosts.API
	}
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
		accountsBase: strings.TrimRight(accountsBase, "/"),
		apiBase:      strings.TrimRight(apiBase, "/"),
		httpc:        &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// AccessToken returns the cached access token, minting a fresh one when the
// cache is empty or past TTL.
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken != "" && time.Since(c.fetchedAt) < accessTokenTTL {
		return c.accessToken, nil
	}
	return c.mintLocked(ctx)
}

// forceRefresh discards the cached token and mints a new one — the 401-retry
// path. stale is the token that just failed; the mint is skipped when another
// goroutine already replaced it.
func (c *Client) forceRefresh(ctx context.Context, stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken != "" && c.accessToken != stale {
		return c.accessToken, nil
	}
	c.accessToken = ""
	return c.mintLocked(ctx)
}

func (c *Client) mintLocked(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"refresh_token": {c.refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.accountsBase+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("zohocrm: token response: %w", err)
	}
	// Zoho reports grant errors as 200 + {"error": "..."} — treat any empty
	// access_token as a definite auth failure so callers can classify it.
	if tok.AccessToken == "" {
		msg := tok.Error
		if msg == "" {
			msg = fmt.Sprintf("http %d", resp.StatusCode)
		}
		return "", &AuthError{Msg: msg}
	}
	c.accessToken = tok.AccessToken
	c.fetchedAt = time.Now()
	return c.accessToken, nil
}

// AuthError marks a definite credential rejection (bad refresh token /
// client pair), as opposed to transport or Zoho-side outages.
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return "zohocrm: auth: " + e.Msg }

// IsAuthError reports whether err is a definite credential rejection.
func IsAuthError(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}

// getJSON performs an authenticated GET with one forced token refresh on 401.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	token, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	status, body, err := c.doGet(ctx, path, token)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		if token, err = c.forceRefresh(ctx, token); err != nil {
			return err
		}
		if status, body, err = c.doGet(ctx, path, token); err != nil {
			return err
		}
	}
	// 204: Zoho returns No Content for empty collections (e.g. a module
	// with no fields visible to the token). Leave out untouched.
	if status == http.StatusNoContent {
		return nil
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("zohocrm: GET %s: http %d: %s", path, status, truncate(body, 300))
	}
	return json.Unmarshal(body, out)
}

func (c *Client) doGet(ctx context.Context, path, token string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Org is the subset of /crm/v8/org used for connection probing.
type Org struct {
	ID          string `json:"id"`
	CompanyName string `json:"company_name"`
	DomainName  string `json:"domain_name"`
}

// GetOrganization fetches the org record — the cheapest authenticated call,
// used as the save-time and nightly probe.
func (c *Client) GetOrganization(ctx context.Context) (Org, error) {
	var out struct {
		Org []Org `json:"org"`
	}
	if err := c.getJSON(ctx, "/crm/v8/org", &out); err != nil {
		return Org{}, err
	}
	if len(out.Org) == 0 {
		return Org{}, fmt.Errorf("zohocrm: org response empty")
	}
	return out.Org[0], nil
}

// Module is the discovery projection of a CRM module. generated_type
// distinguishes stock ("default") from operator-created ("custom") modules —
// both are sync candidates; subforms/linking/field-tracker modules are not.
type Module struct {
	APIName       string `json:"api_name"`
	Module        string `json:"module_name"`
	SingularLabel string `json:"singular_label"`
	PluralLabel   string `json:"plural_label"`
	GeneratedType string `json:"generated_type"`
	APISupported  bool   `json:"api_supported"`
	Creatable     bool   `json:"creatable"`
}

// ListModules returns the org's modules, filtered to API-supported
// default/custom modules (the only viable sync targets).
func (c *Client) ListModules(ctx context.Context) ([]Module, error) {
	var out struct {
		Modules []Module `json:"modules"`
	}
	if err := c.getJSON(ctx, "/crm/v8/settings/modules", &out); err != nil {
		return nil, err
	}
	filtered := make([]Module, 0, len(out.Modules))
	for _, m := range out.Modules {
		if !m.APISupported {
			continue
		}
		if m.GeneratedType != "default" && m.GeneratedType != "custom" {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered, nil
}

// PicklistValue is one selectable value of a picklist field.
type PicklistValue struct {
	DisplayValue string `json:"display_value"`
	ActualValue  string `json:"actual_value"`
}

// Field is the discovery projection of a module field — enough for the UI to
// render a field-map editor and for suggested status/field defaults.
type Field struct {
	APIName         string          `json:"api_name"`
	DisplayLabel    string          `json:"field_label"`
	DataType        string          `json:"data_type"`
	ReadOnly        bool            `json:"read_only"`
	SystemMandatory bool            `json:"system_mandatory"`
	PickListValues  []PicklistValue `json:"pick_list_values"`
}

// ListFields returns the fields of one module.
func (c *Client) ListFields(ctx context.Context, module string) ([]Field, error) {
	var out struct {
		Fields []Field `json:"fields"`
	}
	path := "/crm/v8/settings/fields?module=" + url.QueryEscape(module)
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Fields, nil
}
