// Package zohosprints is a DB-free client for the Zoho Sprints REST API plus the
// pure mapping helpers that translate a Zoho Sprints work item into a Agora
// issue. It is the sibling of the zohoprojects package: Zoho Sprints is a
// SEPARATE product from Zoho Projects (different host, different data model —
// Team → Project → Sprint → Item — and different OAuth scopes, ZohoSprints.*),
// so it gets its own client even though the OAuth refresh-token transport is
// identical.
//
// The Zoho Sprints API returns entities in a column-oriented shape rather than
// plain objects: each list response carries
//
//	"<x>_prop": {"fieldName": columnIndex, ...}   // the column layout
//	"<x>Ids":   ["id1","id2", ...]                 // ordered ids
//	"<x>JObj":  {"id1": [v0, v1, ...], ...}        // each row is an array
//
// so a field is read as JObj[id][prop[fieldName]]. The decode helpers below hide
// that indirection.
//
// API host default: https://sprintsapi.zoho.com/zsapi
// Accounts host default: https://accounts.zoho.com
package zohosprints

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 30 * time.Second

// DefaultAccountsHost is the OAuth token endpoint host (US data center).
const DefaultAccountsHost = "https://accounts.zoho.com"

// DefaultAPIHost is the Zoho Sprints REST base (US data center). No trailing
// slash — paths are joined with a leading "/".
const DefaultAPIHost = "https://sprintsapi.zoho.com/zsapi"

const accessTokenTTL = 55 * time.Minute

// maxPageSize is the per-request page size for paginated item calls. The backlog
// commonly holds a few hundred items; 300 keeps a project to a couple of pages.
const maxPageSize = 300

// maxPages bounds the pagination loop.
const maxPages = 200

// Config carries the OAuth + host settings. TeamID (a.k.a. ZSOID) is optional —
// ResolveTeamID looks it up via /teams/ when blank.
type Config struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	TeamID       string
	AccountsHost string
	APIHost      string
}

// Client talks to one Zoho Sprints portal using an OAuth2 refresh-token grant.
// Safe for concurrent use; the access token is cached in-process.
type Client struct {
	cfg  Config
	http *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExp    time.Time
}

func NewClient(cfg Config) *Client {
	cfg.AccountsHost = strings.TrimRight(strings.TrimSpace(cfg.AccountsHost), "/")
	if cfg.AccountsHost == "" {
		cfg.AccountsHost = DefaultAccountsHost
	}
	cfg.APIHost = strings.TrimRight(strings.TrimSpace(cfg.APIHost), "/")
	if cfg.APIHost == "" {
		cfg.APIHost = DefaultAPIHost
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.RefreshToken = strings.TrimSpace(cfg.RefreshToken)
	cfg.TeamID = strings.TrimSpace(cfg.TeamID)
	return &Client{cfg: cfg, http: &http.Client{Timeout: defaultTimeout}}
}

// Team returns the configured team id ("" when unset).
func (c *Client) Team() string { return c.cfg.TeamID }

// --- OAuth (identical grant to zohoprojects) --------------------------------

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
}

func (c *Client) refreshAccessToken(ctx context.Context) error {
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" || c.cfg.RefreshToken == "" {
		return errors.New("zohosprints: missing OAuth credentials")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("refresh_token", c.cfg.RefreshToken)

	endpoint := c.cfg.AccountsHost + "/oauth/v2/token?" + form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("zohosprints: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("zohosprints: token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("zohosprints: read token body: %w", err)
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("zohosprints: decode token response (http %d): %w", resp.StatusCode, err)
	}
	if parsed.Error != "" {
		return fmt.Errorf("zohosprints: token error: %s", parsed.Error)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return fmt.Errorf("zohosprints: empty access token (http %d)", resp.StatusCode)
	}
	c.accessToken = parsed.AccessToken
	ttl := accessTokenTTL
	if parsed.ExpiresIn > 0 {
		ttl = time.Duration(parsed.ExpiresIn)*time.Second - 5*time.Minute
		if ttl <= 0 {
			ttl = time.Duration(parsed.ExpiresIn) * time.Second / 2
		}
	}
	c.tokenExp = time.Now().Add(ttl)
	return nil
}

func (c *Client) token(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.accessToken != "" && time.Now().Before(c.tokenExp) {
		return c.accessToken, nil
	}
	if err := c.refreshAccessToken(ctx); err != nil {
		return "", err
	}
	return c.accessToken, nil
}

// --- transport --------------------------------------------------------------

func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	return c.doWithRetry(ctx, path, query, false)
}

func (c *Client) doWithRetry(ctx context.Context, path string, query url.Values, retried bool) ([]byte, error) {
	tok, err := c.token(ctx, retried)
	if err != nil {
		return nil, err
	}
	endpoint := c.cfg.APIHost + path
	if enc := query.Encode(); enc != "" {
		endpoint += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("zohosprints: build request: %w", err)
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+tok)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zohosprints: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("zohosprints: read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized && !retried {
		return c.doWithRetry(ctx, path, query, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg := zohoErrorMessage(body); msg != "" {
			return nil, fmt.Errorf("zohosprints: http %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("zohosprints: http %d", resp.StatusCode)
	}
	return body, nil
}

func zohoErrorMessage(body []byte) string {
	var env struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Message != "" {
		return env.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// --- column-oriented decode helpers -----------------------------------------

// jobjResponse is the generic shape of a Zoho Sprints list response for a single
// entity kind. The caller passes the entity's key prefix (e.g. "project",
// "sprint", "item", "status") and decode() pulls the matching _prop / Ids / JObj.
type jobjResponse map[string]json.RawMessage

func decodeJObj(body []byte) (jobjResponse, error) {
	var r jobjResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return r, nil
}

// section returns the (prop, ids, rows) for an entity prefix. rows maps id -> the
// raw column array. Missing sections decode to empty (not an error) so a project
// with zero sprints/items is handled gracefully.
func (r jobjResponse) section(prefix string) (prop map[string]int, ids []string, rows map[string][]any) {
	prop = map[string]int{}
	rows = map[string][]any{}
	if raw, ok := r[prefix+"_prop"]; ok {
		_ = json.Unmarshal(raw, &prop)
	}
	if raw, ok := r[prefix+"Ids"]; ok {
		_ = json.Unmarshal(raw, &ids)
	}
	if raw, ok := r[prefix+"JObj"]; ok {
		_ = json.Unmarshal(raw, &rows)
	}
	return prop, ids, rows
}

func colString(prop map[string]int, row []any, name string) string {
	idx, ok := prop[name]
	if !ok || idx < 0 || idx >= len(row) {
		return ""
	}
	switch v := row[idx].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

// colStringList reads a column that is a JSON array of ids (the owner field).
func colStringList(prop map[string]int, row []any, name string) []string {
	idx, ok := prop[name]
	if !ok || idx < 0 || idx >= len(row) {
		return nil
	}
	arr, ok := row[idx].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// --- types ------------------------------------------------------------------

// Project is a Zoho Sprints project.
type Project struct {
	ID        string
	Name      string
	StartDate string // ISO-8601, or "-1" when unset
	EndDate   string
}

// Sprint is a Zoho Sprints sprint with its real date range.
type Sprint struct {
	ID        string
	Name      string
	No        string
	StartDate string
	EndDate   string
	Duration  string
}

// ItemStatus is one workflow status (id -> name + bucket). Bucket is Zoho's
// "To do" / "Doing" / "Done" grouping (statusDescription).
type ItemStatus struct {
	ID     string
	Name   string
	Bucket string
}

// Item is a Zoho Sprints work item (backlog or sprint).
type Item struct {
	ID        string
	Name      string
	Desc      string
	No        string
	OwnerIDs  []string // Zoho Sprints user ids (no public email lookup)
	StatusID  string
	ParentID  string // parent item id ("" / "-1" for a top-level item)
	SprintID  string // the sprint or backlog id this item sits in
	Points    string
}

// --- ResolveTeamID ----------------------------------------------------------

type teamsResponse struct {
	Portals []struct {
		Zsoid    string `json:"zsoid"`
		TeamName string `json:"teamName"`
	} `json:"portals"`
}

// ResolveTeamID returns the configured team id, or the first portal's ZSOID from
// /teams/ when unset.
func (c *Client) ResolveTeamID(ctx context.Context) (string, error) {
	if c.cfg.TeamID != "" {
		return c.cfg.TeamID, nil
	}
	body, err := c.get(ctx, "/teams/", nil)
	if err != nil {
		return "", err
	}
	var parsed teamsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("zohosprints: decode teams: %w", err)
	}
	for _, p := range parsed.Portals {
		if s := strings.TrimSpace(p.Zsoid); s != "" {
			return s, nil
		}
	}
	return "", errors.New("zohosprints: no team/portal accessible for this token")
}

// --- ListProjects -----------------------------------------------------------

func (c *Client) ListProjects(ctx context.Context, teamID string) ([]Project, error) {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return nil, errors.New("zohosprints: empty team id")
	}
	q := url.Values{}
	q.Set("action", "allprojects")
	body, err := c.get(ctx, "/team/"+url.PathEscape(teamID)+"/projects/", q)
	if err != nil {
		return nil, err
	}
	r, err := decodeJObj(body)
	if err != nil {
		return nil, fmt.Errorf("zohosprints: decode projects: %w", err)
	}
	prop, ids, rows := r.section("project")
	out := make([]Project, 0, len(ids))
	for _, id := range ids {
		row := rows[id]
		out = append(out, Project{
			ID:        id,
			Name:      colString(prop, row, "projName"),
			StartDate: colString(prop, row, "startDate"),
			EndDate:   colString(prop, row, "endDate"),
		})
	}
	return out, nil
}

// --- ListSprints ------------------------------------------------------------

func (c *Client) ListSprints(ctx context.Context, teamID, projectID string) ([]Sprint, error) {
	teamID = strings.TrimSpace(teamID)
	projectID = strings.TrimSpace(projectID)
	if teamID == "" || projectID == "" {
		return nil, errors.New("zohosprints: empty team or project id")
	}
	q := url.Values{}
	q.Set("action", "data")
	q.Set("type", "[1,2,3,4]") // all sprint statuses (active/upcoming/completed/draft)
	body, err := c.get(ctx, "/team/"+url.PathEscape(teamID)+"/projects/"+url.PathEscape(projectID)+"/sprints/", q)
	if err != nil {
		return nil, err
	}
	r, err := decodeJObj(body)
	if err != nil {
		return nil, fmt.Errorf("zohosprints: decode sprints: %w", err)
	}
	prop, ids, rows := r.section("sprint")
	out := make([]Sprint, 0, len(ids))
	for _, id := range ids {
		row := rows[id]
		out = append(out, Sprint{
			ID:        id,
			Name:      colString(prop, row, "sprintName"),
			No:        colString(prop, row, "sprintNo"),
			StartDate: colString(prop, row, "startDate"),
			EndDate:   colString(prop, row, "endDate"),
			Duration:  colString(prop, row, "duration"),
		})
	}
	return out, nil
}

// --- BacklogID --------------------------------------------------------------

// BacklogID returns the project's backlog "sprint" id, the container the bulk of
// unscheduled items sit in. Empty string when none.
func (c *Client) BacklogID(ctx context.Context, teamID, projectID string) (string, error) {
	teamID = strings.TrimSpace(teamID)
	projectID = strings.TrimSpace(projectID)
	if teamID == "" || projectID == "" {
		return "", errors.New("zohosprints: empty team or project id")
	}
	q := url.Values{}
	q.Set("action", "getbacklog")
	body, err := c.get(ctx, "/team/"+url.PathEscape(teamID)+"/projects/"+url.PathEscape(projectID)+"/", q)
	if err != nil {
		return "", err
	}
	var parsed struct {
		BacklogID string `json:"backlogId"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("zohosprints: decode backlog: %w", err)
	}
	return strings.TrimSpace(parsed.BacklogID), nil
}

// --- ListItemStatuses -------------------------------------------------------

// ListItemStatuses returns the project's workflow statuses keyed by status id.
func (c *Client) ListItemStatuses(ctx context.Context, teamID, projectID string) (map[string]ItemStatus, error) {
	teamID = strings.TrimSpace(teamID)
	projectID = strings.TrimSpace(projectID)
	if teamID == "" || projectID == "" {
		return nil, errors.New("zohosprints: empty team or project id")
	}
	q := url.Values{}
	q.Set("action", "data")
	body, err := c.get(ctx, "/team/"+url.PathEscape(teamID)+"/projects/"+url.PathEscape(projectID)+"/itemstatus/", q)
	if err != nil {
		return nil, err
	}
	r, err := decodeJObj(body)
	if err != nil {
		return nil, fmt.Errorf("zohosprints: decode item statuses: %w", err)
	}
	prop, ids, rows := r.section("status")
	out := make(map[string]ItemStatus, len(ids))
	for _, id := range ids {
		row := rows[id]
		out[id] = ItemStatus{
			ID:     id,
			Name:   colString(prop, row, "statusName"),
			Bucket: colString(prop, row, "statusDescription"),
		}
	}
	return out, nil
}

// --- ListItems --------------------------------------------------------------

// ListItems returns the items in a sprint (or the backlog), including subitems.
// Paginates via index/range.
func (c *Client) ListItems(ctx context.Context, teamID, projectID, sprintID string) ([]Item, error) {
	teamID = strings.TrimSpace(teamID)
	projectID = strings.TrimSpace(projectID)
	sprintID = strings.TrimSpace(sprintID)
	if teamID == "" || projectID == "" || sprintID == "" {
		return nil, errors.New("zohosprints: empty team, project, or sprint id")
	}
	path := "/team/" + url.PathEscape(teamID) + "/projects/" + url.PathEscape(projectID) +
		"/sprints/" + url.PathEscape(sprintID) + "/item/"

	var out []Item
	index := 1
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("action", "sprintitems")
		q.Set("subitem", "true")
		q.Set("index", strconv.Itoa(index))
		q.Set("range", strconv.Itoa(maxPageSize))
		body, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		r, err := decodeJObj(body)
		if err != nil {
			return nil, fmt.Errorf("zohosprints: decode items: %w", err)
		}
		prop, ids, rows := r.section("item")
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			row := rows[id]
			out = append(out, Item{
				ID:       id,
				Name:     colString(prop, row, "itemName"),
				Desc:     colString(prop, row, "description"),
				No:       colString(prop, row, "itemNo"),
				OwnerIDs: colStringList(prop, row, "ownerId"),
				StatusID: colString(prop, row, "statusId"),
				ParentID: colString(prop, row, "parentItem"),
				SprintID: colString(prop, row, "sprintId"),
				Points:   colString(prop, row, "points"),
			})
		}
		if len(ids) < maxPageSize {
			break
		}
		index += maxPageSize
	}
	return out, nil
}
