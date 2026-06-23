// Package zohoprojects is a DB-free client for the Zoho Projects REST API plus
// the pure mapping helpers that translate a Zoho Projects task into a Agora
// issue. Like the bitrix package it deliberately depends on nothing from the
// handler/service layers so it can be unit-tested against httptest mock servers
// without a database.
//
// Zoho Projects is treated as the "task master" for a one-way Phase-1 import:
// the client reads portals, projects, task lists (sprints), tasks and task
// comments and the handler reconciles each task into a Agora issue. Unlike
// Bitrix (whose inbound-webhook base URL embeds the credential), Zoho uses an
// OAuth2 refresh-token grant: a long-lived refresh token is exchanged for a
// short-lived access token against the accounts host, which is then sent as an
// "Authorization: Zoho-oauthtoken <token>" header on every API call.
//
// API host default: https://projectsapi.zoho.com/restapi
// Accounts host default: https://accounts.zoho.com
//
// Regional note: Zoho data centers differ by TLD (.com / .eu / .in / .com.au /
// .jp). Both hosts are env-overridable so a non-US portal points the client at
// its DC (e.g. ZOHO_PROJECTS_API_HOST=https://projectsapi.zoho.eu/restapi).
package zohoprojects

import (
	"bytes"
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

// defaultTimeout bounds a single Zoho REST round-trip. Callers that need a
// tighter deadline pass their own context; this is just the transport-level
// safety net so a hung API can't pin a goroutine forever.
const defaultTimeout = 20 * time.Second

// DefaultAccountsHost is the OAuth token endpoint host for the US (.com) data
// center. Override via ZOHO_PROJECTS_ACCOUNTS_HOST for other regions.
const DefaultAccountsHost = "https://accounts.zoho.com"

// DefaultAPIHost is the Zoho Projects REST base (US data center). Override via
// ZOHO_PROJECTS_API_HOST for other regions. No trailing slash — paths are
// joined with a leading "/".
const DefaultAPIHost = "https://projectsapi.zoho.com/restapi"

// accessTokenTTL is how long a freshly minted access token is trusted before a
// proactive refresh. Zoho access tokens live ~3600s; we refresh at ~55m so an
// in-flight request never races the hard expiry. A 401 mid-window also forces
// an immediate refresh (see do()).
const accessTokenTTL = 55 * time.Minute

// maxPageSize is the per-request page size for paginated list calls. Zoho caps
// the tasks/tasklists "range" param at 200; we request the max so a project is
// walked in as few round-trips as possible.
const maxPageSize = 200

// maxPages bounds the pagination loop so a pathological project (or an API that
// never signals "last page") can't spin forever. 200 pages * 200 rows = 40k
// tasks, far beyond any project we import in Phase 1.
const maxPages = 200

// Config carries the OAuth + host settings the client needs. PortalID is
// optional (ListPortals resolves it when blank); the handler snapshots it from
// ZOHO_PROJECTS_PORTAL.
type Config struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	// PortalID is the Zoho Projects portal (a.k.a. ZSOID). May be a numeric id
	// or the portal name; Zoho accepts either in the path. Optional — when blank
	// the caller resolves it via ListPortals.
	PortalID     string
	AccountsHost string
	APIHost      string
}

// Client talks to one Zoho Projects portal using an OAuth2 refresh-token grant.
// It caches the access token in-process and refreshes it lazily (on first use,
// near expiry, or after a 401). Safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExp    time.Time
}

// NewClient builds a Client from cfg, applying host defaults when unset. It does
// NOT perform any network call — the first API method triggers the initial
// token fetch.
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
	cfg.PortalID = strings.TrimSpace(cfg.PortalID)
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: defaultTimeout},
	}
}

// Portal returns the configured portal id ("" when unset). Exposed so the
// handler can fall back to ListPortals when the env var is blank.
func (c *Client) Portal() string { return c.cfg.PortalID }

// --- OAuth ------------------------------------------------------------------

// tokenResponse is the JSON body of the OAuth refresh-token grant. Zoho returns
// an error as a string in "error" on failure (HTTP 200 with an error field is
// possible), so both are decoded.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
}

// refreshAccessToken exchanges the refresh token for a fresh access token via
// POST {accounts}/oauth/v2/token?grant_type=refresh_token and caches it. The
// caller holds c.mu.
func (c *Client) refreshAccessToken(ctx context.Context) error {
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" || c.cfg.RefreshToken == "" {
		return errors.New("zohoprojects: missing OAuth credentials (client id/secret/refresh token)")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("refresh_token", c.cfg.RefreshToken)

	endpoint := c.cfg.AccountsHost + "/oauth/v2/token?" + form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("zohoprojects: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("zohoprojects: token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("zohoprojects: read token body: %w", err)
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("zohoprojects: decode token response (http %d): %w", resp.StatusCode, err)
	}
	if parsed.Error != "" {
		return fmt.Errorf("zohoprojects: token error: %s", parsed.Error)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return fmt.Errorf("zohoprojects: empty access token (http %d)", resp.StatusCode)
	}
	c.accessToken = parsed.AccessToken
	// Honor the server's expires_in when present, else fall back to our TTL,
	// always shaving the window so we refresh before the hard expiry.
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

// token returns a valid access token, refreshing it when missing or near
// expiry. force=true bypasses the cache (used after a 401).
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

// get issues an authenticated GET to {APIHost}{path} with the given query and
// returns the raw body. On a 401 it refreshes the token once and retries, so an
// access token that expired mid-window is transparently renewed.
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	return c.doWithRetry(ctx, http.MethodGet, path, query, false)
}

func (c *Client) doWithRetry(ctx context.Context, method, path string, query url.Values, retried bool) ([]byte, error) {
	tok, err := c.token(ctx, retried)
	if err != nil {
		return nil, err
	}
	endpoint := c.cfg.APIHost + path
	if enc := query.Encode(); enc != "" {
		endpoint += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("zohoprojects: build request: %w", err)
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+tok)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zohoprojects: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("zohoprojects: read body: %w", err)
	}
	// Token expired mid-window — refresh once and retry. Zoho returns 401 with
	// an INVALID_OAUTHTOKEN/EXPIRED error code in that case.
	if resp.StatusCode == http.StatusUnauthorized && !retried {
		return c.doWithRetry(ctx, method, path, query, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg := zohoErrorMessage(body); msg != "" {
			return nil, fmt.Errorf("zohoprojects: http %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("zohoprojects: http %d", resp.StatusCode)
	}
	return body, nil
}

// postForm issues an authenticated form-encoded POST to {APIHost}{path} and
// returns the raw body. Used by the write paths (UpdateTaskStatus). Like get it
// refreshes the token once and retries on a 401, so a token that expired
// mid-window is transparently renewed.
func (c *Client) postForm(ctx context.Context, path string, form url.Values) ([]byte, error) {
	return c.doFormWithRetry(ctx, path, form, false)
}

func (c *Client) doFormWithRetry(ctx context.Context, path string, form url.Values, retried bool) ([]byte, error) {
	tok, err := c.token(ctx, retried)
	if err != nil {
		return nil, err
	}
	endpoint := c.cfg.APIHost + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("zohoprojects: build request: %w", err)
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zohoprojects: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("zohoprojects: read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized && !retried {
		return c.doFormWithRetry(ctx, path, form, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg := zohoErrorMessage(body); msg != "" {
			return nil, fmt.Errorf("zohoprojects: http %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("zohoprojects: http %d", resp.StatusCode)
	}
	return body, nil
}

// zohoErrorEnvelope is the shape Zoho uses for API errors: {"error":{"code":...,
// "message":"..."}}. Some endpoints nest it differently, so message extraction
// is best-effort.
type zohoErrorEnvelope struct {
	Error struct {
		Code    json.Number `json:"code"`
		Message string      `json:"message"`
	} `json:"error"`
}

// zohoErrorMessage extracts a human-readable error from a Zoho error body, or "".
func zohoErrorMessage(body []byte) string {
	var env zohoErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error.Message != "" {
			if env.Error.Code != "" {
				return env.Error.Code.String() + ": " + env.Error.Message
			}
			return env.Error.Message
		}
	}
	// Fall back to the raw (capped) body so a non-standard error still surfaces.
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// --- types ------------------------------------------------------------------

// flexInt decodes a value Zoho may send as a JSON number or a JSON string into
// a Go string, mirroring bitrix.jsonStr. Zoho is inconsistent about encoding
// ids (project/task ids overflow int32 and are often sent as numbers but
// sometimes as strings).
type flexInt string

func (s *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = flexInt(str)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		*s = flexInt(num.String())
		return nil
	}
	*s = flexInt(string(b))
	return nil
}

func (s flexInt) String() string { return string(s) }

// Portal is the subset of a Zoho Projects portal Agora cares about. The numeric
// ZSOID id is what the rest of the API paths key on.
type Portal struct {
	ID   string
	Name string
}

// Project is the subset of a Zoho Projects project Agora imports. ID is the
// numeric project id used in subsequent task/tasklist/comment calls.
type Project struct {
	ID          string
	Name        string
	Description string
	Status      string
}

// User is a minimal Zoho Projects user — a task's owner/creator. Only the
// fields Agora needs to display or map an assignee (by email).
type User struct {
	ID    string
	Name  string
	Email string
}

// Task is the subset of a Zoho Projects task Agora maps onto an issue. Scalar
// ids are normalized to strings up front (Zoho number-vs-string inconsistency).
// Created/LastUpdated are the raw Zoho display strings (kept verbatim for the
// Phase-2 modified-since cursor and for provenance display).
type Task struct {
	ID              string
	Name            string
	Status          string // human status name (e.g. "Open", "In Progress", "Closed")
	StatusType      string // status "type" bucket when present ("open"/"closed")
	Owner           User
	Created         string
	LastUpdated     string
	LastUpdatedUnix int64 // ms epoch when Zoho supplied last_updated_time_long; 0 otherwise
	Description     string
	TasklistID      string
	TasklistName    string
}

// TaskList is a Zoho Projects task list — the closest native analog to a
// Agora sprint (an SD portal commonly names task lists "Sprint N").
type TaskList struct {
	ID   string
	Name string
}

// Comment is a task comment Agora mirrors into an issue comment. Author/Date
// are normalized display strings; Content is the raw comment body.
type Comment struct {
	ID      string
	Author  string
	Date    string
	Content string
}

// --- ListPortals ------------------------------------------------------------

type listPortalsResponse struct {
	Portals []struct {
		ID      flexInt `json:"id"`
		IDLong  flexInt `json:"id_string"`
		Name    flexInt `json:"name"`
		Company struct {
			Name flexInt `json:"name"`
		} `json:"company"`
	} `json:"portals"`
}

// ListPortals returns the portals the authenticated user can access. Used to
// resolve a portal id when ZOHO_PROJECTS_PORTAL is unset (and for an
// integration-status probe).
func (c *Client) ListPortals(ctx context.Context) ([]Portal, error) {
	body, err := c.get(ctx, "/portals/", nil)
	if err != nil {
		return nil, err
	}
	var parsed listPortalsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("zohoprojects: decode portals: %w", err)
	}
	portals := make([]Portal, 0, len(parsed.Portals))
	for _, p := range parsed.Portals {
		id := firstNonEmpty(p.IDLong, p.ID)
		if id == "" {
			continue
		}
		name := firstNonEmpty(p.Name, p.Company.Name)
		portals = append(portals, Portal{ID: id, Name: name})
	}
	return portals, nil
}

// --- ListProjects -----------------------------------------------------------

type listProjectsResponse struct {
	Projects []rawProject `json:"projects"`
}

type rawProject struct {
	ID          flexInt `json:"id"`
	IDString    flexInt `json:"id_string"`
	Name        flexInt `json:"name"`
	Description flexInt `json:"description"`
	Status      flexInt `json:"status"`
}

// ListProjects returns the projects in a portal. Paginates via index/range.
func (c *Client) ListProjects(ctx context.Context, portalID string) ([]Project, error) {
	portalID = strings.TrimSpace(portalID)
	if portalID == "" {
		return nil, errors.New("zohoprojects: empty portal id")
	}
	path := "/portal/" + url.PathEscape(portalID) + "/projects/"

	var out []Project
	index := 1
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("index", strconv.Itoa(index))
		q.Set("range", strconv.Itoa(maxPageSize))
		body, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		if emptyJSONBody(body) {
			break // 204 / empty body = portal with zero projects (or no more pages)
		}
		var parsed listProjectsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("zohoprojects: decode projects: %w", err)
		}
		if len(parsed.Projects) == 0 {
			break
		}
		for _, rp := range parsed.Projects {
			id := firstNonEmpty(rp.IDString, rp.ID)
			if id == "" {
				continue
			}
			out = append(out, Project{
				ID:          id,
				Name:        rp.Name.String(),
				Description: rp.Description.String(),
				Status:      rp.Status.String(),
			})
		}
		if len(parsed.Projects) < maxPageSize {
			break
		}
		index += maxPageSize
	}
	return out, nil
}

// --- ListTaskLists ----------------------------------------------------------

type listTaskListsResponse struct {
	TaskLists []struct {
		ID       flexInt `json:"id"`
		IDString flexInt `json:"id_string"`
		Name     flexInt `json:"name"`
	} `json:"tasklists"`
}

// ListTaskLists returns a project's task lists (the sprint analog). Paginates
// via index/range. The "flag=allflag" query asks Zoho for every task list
// (internal + external) rather than only the caller's.
func (c *Client) ListTaskLists(ctx context.Context, portalID, projectID string) ([]TaskList, error) {
	portalID = strings.TrimSpace(portalID)
	projectID = strings.TrimSpace(projectID)
	if portalID == "" || projectID == "" {
		return nil, errors.New("zohoprojects: empty portal or project id")
	}
	path := "/portal/" + url.PathEscape(portalID) + "/projects/" + url.PathEscape(projectID) + "/tasklists/"

	var out []TaskList
	index := 1
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("index", strconv.Itoa(index))
		q.Set("range", strconv.Itoa(maxPageSize))
		q.Set("flag", "allflag")
		body, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		if emptyJSONBody(body) {
			break // 204 / empty body = project with zero task lists (or no more pages)
		}
		var parsed listTaskListsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("zohoprojects: decode tasklists: %w", err)
		}
		if len(parsed.TaskLists) == 0 {
			break
		}
		for _, tl := range parsed.TaskLists {
			id := firstNonEmpty(tl.IDString, tl.ID)
			if id == "" {
				continue
			}
			out = append(out, TaskList{ID: id, Name: tl.Name.String()})
		}
		if len(parsed.TaskLists) < maxPageSize {
			break
		}
		index += maxPageSize
	}
	return out, nil
}

// --- ListTasks --------------------------------------------------------------

type listTasksResponse struct {
	Tasks []rawTask `json:"tasks"`
}

type rawTask struct {
	ID           flexInt `json:"id"`
	IDString     flexInt `json:"id_string"`
	Name         flexInt `json:"name"`
	Description  flexInt `json:"description"`
	Created      flexInt `json:"created_time"`
	CreatedLong  flexInt `json:"created_time_long"`
	Updated      flexInt `json:"last_updated_time"`
	UpdatedLong  flexInt `json:"last_updated_time_long"`
	Status       rawTaskStatus `json:"status"`
	Owner        rawTaskOwner  `json:"details"`
	TasklistRef  rawTasklistRef `json:"tasklist"`
}

// rawTaskStatus is the nested status object Zoho returns per task:
// {"name":"In Progress","type":"open","color_code":"..."}.
type rawTaskStatus struct {
	Name flexInt `json:"name"`
	Type flexInt `json:"type"`
}

// rawTaskOwner mirrors the "details.owners" array Zoho nests in a task. Only
// the first owner is taken as the assignee candidate.
type rawTaskOwner struct {
	Owners []struct {
		ID    flexInt `json:"zpuid"`
		IDAlt flexInt `json:"id"`
		Name  flexInt `json:"name"`
		Email flexInt `json:"email"`
	} `json:"owners"`
}

// rawTasklistRef is the nested {id,name} task-list object Zoho includes on a
// task ("tasklist":{"id":...,"name":"Sprint 5"}).
type rawTasklistRef struct {
	ID       flexInt `json:"id"`
	IDString flexInt `json:"id_string"`
	Name     flexInt `json:"name"`
}

func (rt rawTask) toTask() Task {
	id := firstNonEmpty(rt.IDString, rt.ID)
	var owner User
	if len(rt.Owner.Owners) > 0 {
		o := rt.Owner.Owners[0]
		owner = User{
			ID:    firstNonEmpty(o.ID, o.IDAlt),
			Name:  o.Name.String(),
			Email: o.Email.String(),
		}
	}
	var updatedUnix int64
	if v := strings.TrimSpace(rt.UpdatedLong.String()); v != "" {
		updatedUnix, _ = strconv.ParseInt(v, 10, 64)
	}
	return Task{
		ID:              id,
		Name:            rt.Name.String(),
		Status:          rt.Status.Name.String(),
		StatusType:      rt.Status.Type.String(),
		Owner:           owner,
		Created:         firstNonEmpty(rt.Created),
		LastUpdated:     firstNonEmpty(rt.Updated),
		LastUpdatedUnix: updatedUnix,
		Description:     rt.Description.String(),
		TasklistID:      firstNonEmpty(rt.TasklistRef.IDString, rt.TasklistRef.ID),
		TasklistName:    rt.TasklistRef.Name.String(),
	}
}

// ListTasks returns the tasks in a project, paginating via index/range. When
// modifiedSince is non-nil it is passed as the Zoho last_modified_time filter so
// a periodic sync pulls only changed tasks. When owner is non-empty it is passed
// as the Zoho "owner" filter (a zpuid / accounts user id) so only that user's
// tasks are returned — the server-side "only my tasks" scope.
func (c *Client) ListTasks(ctx context.Context, portalID, projectID string, modifiedSince *time.Time, owner string) ([]Task, error) {
	portalID = strings.TrimSpace(portalID)
	projectID = strings.TrimSpace(projectID)
	owner = strings.TrimSpace(owner)
	if portalID == "" || projectID == "" {
		return nil, errors.New("zohoprojects: empty portal or project id")
	}
	path := "/portal/" + url.PathEscape(portalID) + "/projects/" + url.PathEscape(projectID) + "/tasks/"

	var out []Task
	index := 1
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("index", strconv.Itoa(index))
		q.Set("range", strconv.Itoa(maxPageSize))
		// Ask for all statuses (Zoho otherwise hides completed tasks behind a
		// status filter on some endpoints).
		q.Set("status", "all")
		if owner != "" {
			// Server-side "only this owner's tasks" filter (zpuid / accounts id).
			q.Set("owner", owner)
		}
		if modifiedSince != nil {
			// Zoho expects the modified-time filter in MM-DD-YYYY form.
			q.Set("last_modified_time", modifiedSince.UTC().Format("01-02-2006"))
		}
		body, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		if emptyJSONBody(body) {
			break // 204 / empty body = zero-task project (or no more pages)
		}
		var parsed listTasksResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("zohoprojects: decode tasks: %w", err)
		}
		if len(parsed.Tasks) == 0 {
			break
		}
		for _, rt := range parsed.Tasks {
			t := rt.toTask()
			if t.ID == "" {
				continue
			}
			out = append(out, t)
		}
		if len(parsed.Tasks) < maxPageSize {
			break
		}
		index += maxPageSize
	}
	return out, nil
}

// --- GetTaskComments --------------------------------------------------------

type listCommentsResponse struct {
	Comments []struct {
		ID        flexInt `json:"id"`
		IDString  flexInt `json:"id_string"`
		Content   flexInt `json:"content"`
		AddedBy   flexInt `json:"added_by"`
		AddedName flexInt `json:"added_person"`
		AddedTime flexInt `json:"created_time"`
		AddedTimL flexInt `json:"added_time"`
	} `json:"comments"`
}

// maxCommentsPerTask caps how many comments GetTaskComments returns so a task
// with a long thread can't balloon the import. Mirrors the bitrix cap.
const maxCommentsPerTask = 50

// GetTaskComments returns a task's comment feed (author, date, content). It
// reads one page (the API returns newest-first); the result is capped at
// maxCommentsPerTask. Comments with empty content are skipped.
func (c *Client) GetTaskComments(ctx context.Context, portalID, projectID, taskID string) ([]Comment, error) {
	portalID = strings.TrimSpace(portalID)
	projectID = strings.TrimSpace(projectID)
	taskID = strings.TrimSpace(taskID)
	if portalID == "" || projectID == "" || taskID == "" {
		return nil, errors.New("zohoprojects: empty portal, project, or task id")
	}
	path := "/portal/" + url.PathEscape(portalID) + "/projects/" + url.PathEscape(projectID) +
		"/tasks/" + url.PathEscape(taskID) + "/comments/"
	q := url.Values{}
	q.Set("index", "1")
	q.Set("range", strconv.Itoa(maxCommentsPerTask))

	body, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	// Zoho returns 204 No Content (empty body) for a task with no comments;
	// treat that as zero comments rather than a JSON decode error.
	if emptyJSONBody(body) {
		return nil, nil
	}
	var parsed listCommentsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("zohoprojects: decode comments: %w", err)
	}
	comments := make([]Comment, 0, len(parsed.Comments))
	for _, rc := range parsed.Comments {
		content := strings.TrimSpace(rc.Content.String())
		if content == "" {
			continue
		}
		comments = append(comments, Comment{
			ID:      firstNonEmpty(rc.IDString, rc.ID),
			Author:  rc.AddedName.String(),
			Date:    firstNonEmpty(rc.AddedTime, rc.AddedTimL),
			Content: content,
		})
		if len(comments) >= maxCommentsPerTask {
			break
		}
	}
	return comments, nil
}

// --- custom statuses + status push (Phase 2 outbound) -----------------------

// CustomStatus is one of a project's task statuses. A Zoho portal defines its
// own status names per project, each bucketed by Zoho into a "type" of "open"
// or "closed" and addressed by a numeric id. Updating a task's status requires
// this id (not the name), so the outbound mirror lists these and resolves the
// id whose name/type best matches the Agora issue status.
type CustomStatus struct {
	ID   string
	Name string
	Type string // "open" | "closed"
}

type rawCustomStatus struct {
	ID       flexInt `json:"id"`
	IDString flexInt `json:"id_string"`
	Name     flexInt `json:"name"`
	Type     flexInt `json:"type"`
}

// listCustomStatusResponse tolerates the two keys Zoho has shipped this list
// under across API revisions ("customstatus" and "status"), so a key rename
// upstream doesn't silently empty the list. Mirrors the parse-don't-cast ethos.
type listCustomStatusResponse struct {
	CustomStatus []rawCustomStatus `json:"customstatus"`
	Status       []rawCustomStatus `json:"status"`
}

// ListTaskCustomStatuses returns the task custom statuses defined for a project.
// Used by the outbound status mirror to resolve a Agora issue status to the
// project's own status id before pushing a task update.
func (c *Client) ListTaskCustomStatuses(ctx context.Context, portalID, projectID string) ([]CustomStatus, error) {
	portalID = strings.TrimSpace(portalID)
	projectID = strings.TrimSpace(projectID)
	if portalID == "" || projectID == "" {
		return nil, errors.New("zohoprojects: empty portal or project id")
	}
	path := "/portal/" + url.PathEscape(portalID) + "/projects/" + url.PathEscape(projectID) + "/tasks/customstatus/"
	body, err := c.get(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	if emptyJSONBody(body) {
		return nil, nil // 204 / empty body = no custom statuses
	}
	var parsed listCustomStatusResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("zohoprojects: decode custom statuses: %w", err)
	}
	raws := parsed.CustomStatus
	if len(raws) == 0 {
		raws = parsed.Status
	}
	out := make([]CustomStatus, 0, len(raws))
	for _, rs := range raws {
		id := firstNonEmpty(rs.IDString, rs.ID)
		if id == "" {
			continue
		}
		out = append(out, CustomStatus{ID: id, Name: rs.Name.String(), Type: rs.Type.String()})
	}
	return out, nil
}

// UpdateTaskStatus pushes a status change back to a Zoho task by setting its
// custom_status to the given status id (resolve the id with
// ListTaskCustomStatuses + ResolveCustomStatusID). Mirrors
// bitrix.Client.UpdateTaskStatus; the Zoho endpoint is a form-encoded POST to
// the task resource.
func (c *Client) UpdateTaskStatus(ctx context.Context, portalID, projectID, taskID, customStatusID string) error {
	portalID = strings.TrimSpace(portalID)
	projectID = strings.TrimSpace(projectID)
	taskID = strings.TrimSpace(taskID)
	customStatusID = strings.TrimSpace(customStatusID)
	if portalID == "" || projectID == "" || taskID == "" {
		return errors.New("zohoprojects: empty portal, project, or task id")
	}
	if customStatusID == "" {
		return errors.New("zohoprojects: empty custom status id")
	}
	path := "/portal/" + url.PathEscape(portalID) + "/projects/" + url.PathEscape(projectID) +
		"/tasks/" + url.PathEscape(taskID) + "/"
	form := url.Values{}
	form.Set("custom_status", customStatusID)
	_, err := c.postForm(ctx, path, form)
	return err
}

// --- helpers ----------------------------------------------------------------

// emptyJSONBody reports whether a Zoho response carried no content (an HTTP 204
// or whitespace-only body), which Zoho returns for "no results" on several list
// endpoints (a project with zero tasks, a task with zero comments). Such a body
// must be treated as an empty list, not a JSON decode error.
func emptyJSONBody(body []byte) bool { return len(bytes.TrimSpace(body)) == 0 }

// IsThrottle reports whether err is a Zoho rolling-throttle / rate-limit
// rejection (URL_ROLLING_THROTTLES_LIMIT_EXCEEDED, or an HTTP 429). The bulk
// importer uses this to stop hammering a throttled endpoint for the rest of a run
// instead of retrying into the same wall on every remaining item.
func IsThrottle(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToUpper(err.Error())
	return strings.Contains(s, "THROTTLE") || strings.Contains(s, "RATE_LIMIT") ||
		strings.Contains(s, "HTTP 429") || strings.Contains(s, "TOO MANY REQUEST")
}

// firstNonEmpty returns the first non-empty (trimmed) value among the
// candidates, tolerating Zoho returning either id or id_string / camelCase keys.
func firstNonEmpty(vals ...flexInt) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v.String()); s != "" {
			return s
		}
	}
	return ""
}
