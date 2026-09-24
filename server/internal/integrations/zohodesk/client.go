// Package zohodesk is a small, read-only Zoho Desk API client. It carries no
// credentials of its own: it borrows access tokens from a TokenSource (the
// person's Zoho CRM client, which holds the same refresh token), so one grant
// covers both products and Desk applies that person's own department and
// ticket permissions on every call.
package zohodesk

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
	"time"
)

// Hosts maps a Zoho data center id to its Desk host. Like CRM, the host is
// load-bearing: a token minted in one DC is invalid in another.
var Hosts = map[string]string{
	"us": "https://desk.zoho.com",
	"eu": "https://desk.zoho.eu",
	"in": "https://desk.zoho.in",
	"au": "https://desk.zoho.com.au",
	"jp": "https://desk.zoho.jp",
	"sa": "https://desk.zoho.sa",
	"ca": "https://desk.zohocloud.ca",
}

// TokenSource hands out access tokens for the person's grant.
type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
	ForceRefresh(ctx context.Context, stale string) (string, error)
}

// Client talks to one person's Zoho Desk. orgID is required for every call
// except ListOrganizations.
type Client struct {
	tokens TokenSource
	base   string
	orgID  string
	httpc  *http.Client
}

// New builds a client for the given DC. base overrides the DC host when
// non-empty (tests and the local fake).
func New(tokens TokenSource, dc, base, orgID string) (*Client, error) {
	if base == "" {
		host, ok := Hosts[dc]
		if !ok {
			return nil, fmt.Errorf("zohodesk: unknown dc %q", dc)
		}
		base = host
	}
	return &Client{
		tokens: tokens,
		base:   strings.TrimRight(base, "/") + "/api/v1",
		orgID:  orgID,
		httpc:  &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// WithOrg returns a copy of the client bound to orgID.
func (c *Client) WithOrg(orgID string) *Client {
	cp := *c
	cp.orgID = orgID
	return &cp
}

// OrgID is the Desk organization this client reads.
func (c *Client) OrgID() string { return c.orgID }

// ErrNoAccess marks a Desk permission refusal (the person can't see this).
var ErrNoAccess = errors.New("zohodesk: no access")

func (c *Client) get(ctx context.Context, path string, q url.Values, withOrg bool, out any) error {
	if withOrg && c.orgID == "" {
		return fmt.Errorf("zohodesk: no Desk organization for this account")
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	token, err := c.tokens.AccessToken(ctx)
	if err != nil {
		return err
	}
	status, body, err := c.do(ctx, u, token, withOrg)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		if token, err = c.tokens.ForceRefresh(ctx, token); err != nil {
			return err
		}
		if status, body, err = c.do(ctx, u, token, withOrg); err != nil {
			return err
		}
	}
	switch {
	case status == http.StatusNoContent:
		// Desk answers an empty list with 204 and no body.
		return nil
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrNoAccess, truncate(body, 200))
	case status >= 300:
		return fmt.Errorf("zohodesk: GET %s: http %d: %s", path, status, truncate(body, 300))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("zohodesk: decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, u, token string, withOrg bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	if withOrg {
		req.Header.Set("orgId", c.orgID)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, err
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// ID is a Desk id. Desk sends ids as strings but some older endpoints send
// numbers; both decode.
type ID string

// UnmarshalJSON accepts a JSON string, a JSON number, or null.
func (id *ID) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*id = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*id = ID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*id = ID(n.String())
	return nil
}

// Organization is one Desk portal the person belongs to.
type Organization struct {
	ID          ID     `json:"id"`
	CompanyName string `json:"companyName"`
	PortalName  string `json:"portalName"`
}

// ListOrganizations lists the Desk organizations the person belongs to.
func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	var out struct {
		Data []Organization `json:"data"`
	}
	if err := c.get(ctx, "/organizations", nil, false, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Agent is the person as a Desk agent.
type Agent struct {
	ID                      ID     `json:"id"`
	FirstName               string `json:"firstName"`
	LastName                string `json:"lastName"`
	Name                    string `json:"name"`
	EmailID                 string `json:"emailId"`
	RoleID                  ID     `json:"roleId"`
	ProfileID               ID     `json:"profileId"`
	AssociatedDepartmentIDs []ID   `json:"associatedDepartmentIds"`
}

// DisplayName prefers the full name Desk reports.
func (a Agent) DisplayName() string {
	if a.Name != "" {
		return a.Name
	}
	return strings.TrimSpace(a.FirstName + " " + a.LastName)
}

// MyInfo returns the person's own Desk agent record.
func (c *Client) MyInfo(ctx context.Context) (Agent, error) {
	var out Agent
	err := c.get(ctx, "/myinfo", nil, true, &out)
	return out, err
}

// Department is one Desk department.
type Department struct {
	ID        ID     `json:"id"`
	Name      string `json:"name"`
	IsEnabled bool   `json:"isEnabled"`
}

// ListDepartments lists the departments the person can see.
func (c *Client) ListDepartments(ctx context.Context) ([]Department, error) {
	var out struct {
		Data []Department `json:"data"`
	}
	q := url.Values{"limit": {"100"}}
	if err := c.get(ctx, "/departments", q, true, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Person is the contact / assignee shape embedded in a ticket.
type Person struct {
	ID        ID     `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
}

// Name is the person's display name.
func (p *Person) Name() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.FirstName + " " + p.LastName)
}

// Ticket is the subset of a Desk ticket the tools return.
type Ticket struct {
	ID           ID      `json:"id"`
	TicketNumber ID      `json:"ticketNumber"`
	Subject      string  `json:"subject"`
	Status       string  `json:"status"`
	StatusType   string  `json:"statusType"`
	Priority     string  `json:"priority"`
	Channel      string  `json:"channel"`
	DepartmentID ID      `json:"departmentId"`
	AssigneeID   ID      `json:"assigneeId"`
	Email        string  `json:"email"`
	DueDate      string  `json:"dueDate"`
	CreatedTime  string  `json:"createdTime"`
	ModifiedTime string  `json:"modifiedTime"`
	WebURL       string  `json:"webUrl"`
	Description  string  `json:"description"`
	Assignee     *Person `json:"assignee"`
	Contact      *Person `json:"contact"`
}

// TicketQuery filters ListTickets. Empty fields don't filter.
type TicketQuery struct {
	DepartmentID string
	AssigneeID   string
	Status       string // Desk status names, comma-separated
	From         int
	Limit        int
}

// ListTickets lists tickets the person can see, newest activity first.
func (c *Client) ListTickets(ctx context.Context, tq TicketQuery) ([]Ticket, error) {
	q := url.Values{
		"include": {"contacts,assignee"},
		"sortBy":  {"-modifiedTime"},
	}
	limit := tq.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	q.Set("limit", strconv.Itoa(limit))
	if tq.From > 0 {
		q.Set("from", strconv.Itoa(tq.From))
	}
	if tq.DepartmentID != "" {
		q.Set("departmentId", tq.DepartmentID)
	}
	if tq.AssigneeID != "" {
		q.Set("assignee", tq.AssigneeID)
	}
	if tq.Status != "" {
		q.Set("status", tq.Status)
	}
	var out struct {
		Data []Ticket `json:"data"`
	}
	if err := c.get(ctx, "/tickets", q, true, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetTicket fetches one ticket by its id.
func (c *Client) GetTicket(ctx context.Context, id string) (Ticket, error) {
	var out Ticket
	q := url.Values{"include": {"contacts,assignee"}}
	err := c.get(ctx, "/tickets/"+url.PathEscape(id), q, true, &out)
	return out, err
}

// FindTicketsByNumber looks tickets up by their visible number (the "#4821"
// people quote).
func (c *Client) FindTicketsByNumber(ctx context.Context, number string) ([]Ticket, error) {
	var out struct {
		Data []Ticket `json:"data"`
	}
	q := url.Values{"ticketNumber": {number}, "limit": {"5"}}
	if err := c.get(ctx, "/tickets/search", q, true, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Thread is one message in a ticket's conversation (summary only).
type Thread struct {
	ID          ID     `json:"id"`
	Channel     string `json:"channel"`
	Direction   string `json:"direction"`
	Summary     string `json:"summary"`
	CreatedTime string `json:"createdTime"`
	Visibility  string `json:"visibility"`
	Author      struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Email string `json:"email"`
	} `json:"author"`
}

// ListThreads lists a ticket's conversation, newest first.
func (c *Client) ListThreads(ctx context.Context, ticketID string, limit int) ([]Thread, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var out struct {
		Data []Thread `json:"data"`
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "sortBy": {"-createdTime"}}
	if err := c.get(ctx, "/tickets/"+url.PathEscape(ticketID)+"/threads", q, true, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
