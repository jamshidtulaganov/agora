// Package zohoread is the one read-only Zoho tool surface shared by agents
// (the /mcp/zoho proxy) and the Assistant. Every call runs with one person's
// own Zoho clients, so Zoho decides what comes back; nothing here writes to
// Zoho, and nothing here holds credentials.
package zohoread

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohocrm"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohodesk"
)

// Identity is who the calls act as, as shown to the model and the person.
type Identity struct {
	Email           string   `json:"email"`
	Name            string   `json:"name"`
	CRMRole         string   `json:"crm_role,omitempty"`
	CRMProfile      string   `json:"crm_profile,omitempty"`
	DeskAgentID     string   `json:"-"`
	DeskDepartments []string `json:"desk_departments,omitempty"`
	// ActingFor explains why this person ("the person who assigned the
	// issue"); empty when the person is asking for themselves.
	ActingFor string `json:"acting_for,omitempty"`
}

// Clients are one person's Zoho clients. CRM or Desk is nil when that
// product isn't available on their Zoho account.
type Clients struct {
	CRM  *zohocrm.Client
	Desk *zohodesk.Client
	Me   Identity
}

// Tool is one tool definition (name, description, JSON schema).
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Result is a tool's output plus what the audit log records about it.
type Result struct {
	Value  any
	Object string // module or "tickets" — what was read
	Count  int    // how many records came back
}

// ErrUnknownTool is returned for a name outside Tools().
var ErrUnknownTool = errors.New("unknown Zoho tool")

var moduleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,100}$`)

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// Tools is the read-only tool surface, CRM then Desk.
func Tools() []Tool {
	return []Tool{
		{Name: "zoho_whoami", Description: "Which Zoho person these reads act as, with their CRM role and profile and Desk departments. Zoho shows only what this person may see.", InputSchema: obj(map[string]any{})},
		{Name: "zoho_crm_modules", Description: "List the Zoho CRM modules this person can use (standard and custom). Call before searching to learn what exists.", InputSchema: obj(map[string]any{})},
		{Name: "zoho_crm_fields", Description: "List one CRM module's fields with types and picklist values, to learn the api names before searching.", InputSchema: obj(map[string]any{
			"module": str("Module api_name, e.g. Deals or Collection_Cases"),
		}, "module")},
		{Name: "zoho_crm_search", Description: "Read CRM records with one COQL SELECT, e.g. SELECT Deal_Name, Stage, Amount FROM Deals WHERE Stage = 'Negotiation' LIMIT 50. Returns only records this person can see.", InputSchema: obj(map[string]any{
			"coql": str("A single COQL SELECT statement"),
		}, "coql")},
		{Name: "zoho_crm_get_record", Description: "Read one CRM record by module and id.", InputSchema: obj(map[string]any{
			"module": str("Module api_name"),
			"id":     str("Record id"),
		}, "module", "id")},
		{Name: "zoho_desk_departments", Description: "List the Zoho Desk departments this person can see.", InputSchema: obj(map[string]any{})},
		{Name: "zoho_desk_list_tickets", Description: "List Zoho Desk tickets this person can see, most recently changed first.", InputSchema: obj(map[string]any{
			"mine":          map[string]any{"type": "boolean", "description": "Only tickets assigned to this person"},
			"department_id": str("Only this department (id from zoho_desk_departments)"),
			"status":        str("Only these statuses, comma-separated, e.g. Open,On Hold"),
			"limit":         map[string]any{"type": "integer", "description": "1-50, default 25"},
		})},
		{Name: "zoho_desk_get_ticket", Description: "Read one Zoho Desk ticket by its number (as people quote it, e.g. 4821) or its id.", InputSchema: obj(map[string]any{
			"ticket_number": str("The ticket number people see, e.g. 4821"),
			"ticket_id":     str("The ticket id"),
		})},
		{Name: "zoho_desk_ticket_conversation", Description: "Read a ticket's conversation (message summaries, newest first).", InputSchema: obj(map[string]any{
			"ticket_id": str("The ticket id"),
			"limit":     map[string]any{"type": "integer", "description": "1-20, default 10"},
		}, "ticket_id")},
	}
}

// ValidateCOQL accepts exactly one COQL SELECT. COQL is read-only by design;
// this makes the rule explicit and fails fast on anything else.
func ValidateCOQL(q string) error {
	q = strings.TrimSpace(q)
	switch {
	case q == "":
		return errors.New("coql is required")
	case utf8.RuneCountInString(q) > 2000:
		return errors.New("coql is too long (max 2000 characters)")
	case !strings.HasPrefix(strings.ToLower(q), "select "):
		return errors.New("coql must be a single SELECT statement")
	case strings.Contains(q, ";"):
		return errors.New("coql must be a single statement (no ';')")
	}
	return nil
}

var coqlFrom = regexp.MustCompile(`(?i)\bfrom\s+([A-Za-z0-9_]+)`)

func argString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

func argInt(args map[string]any, key string, def, max int) int {
	n := def
	switch v := args[key].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	}
	if n < 1 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

var errNoCRM = errors.New("Zoho CRM isn't available on this person's Zoho account")
var errNoDesk = errors.New("Zoho Desk isn't available on this person's Zoho account")

// friendly turns a permission refusal into words the model can relay.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, zohodesk.ErrNoAccess) {
		return errors.New("Zoho says this person doesn't have access to that")
	}
	if strings.Contains(err.Error(), "NO_PERMISSION") {
		return errors.New("Zoho says this person doesn't have access to that")
	}
	return err
}

// Call runs one tool as the person behind c.
func Call(ctx context.Context, c Clients, name string, args map[string]any) (Result, error) {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "zoho_whoami":
		return Result{Value: c.Me}, nil

	case "zoho_crm_modules", "zoho_crm_fields", "zoho_crm_search", "zoho_crm_get_record":
		if c.CRM == nil {
			return Result{}, errNoCRM
		}
		return callCRM(ctx, c.CRM, name, args)

	case "zoho_desk_departments", "zoho_desk_list_tickets", "zoho_desk_get_ticket", "zoho_desk_ticket_conversation":
		if c.Desk == nil {
			return Result{}, errNoDesk
		}
		return callDesk(ctx, c, name, args)
	}
	return Result{}, fmt.Errorf("%w: %s", ErrUnknownTool, name)
}

func callCRM(ctx context.Context, crm *zohocrm.Client, name string, args map[string]any) (Result, error) {
	module := argString(args, "module")
	needModule := func() error {
		if !moduleRe.MatchString(module) {
			return fmt.Errorf("module must be a CRM api name like Deals (letters, digits, underscore)")
		}
		return nil
	}
	switch name {
	case "zoho_crm_modules":
		mods, err := crm.ListModules(ctx)
		return Result{Value: map[string]any{"modules": mods}, Object: "modules", Count: len(mods)}, friendly(err)
	case "zoho_crm_fields":
		if err := needModule(); err != nil {
			return Result{}, err
		}
		fields, err := crm.ListFields(ctx, module)
		return Result{Value: map[string]any{"module": module, "fields": fields}, Object: module, Count: len(fields)}, friendly(err)
	case "zoho_crm_search":
		q := argString(args, "coql")
		if err := ValidateCOQL(q); err != nil {
			return Result{}, err
		}
		object := ""
		if m := coqlFrom.FindStringSubmatch(q); m != nil {
			object = m[1]
		}
		rows, more, err := crm.Query(ctx, q)
		if rows == nil {
			rows = []map[string]any{}
		}
		return Result{Value: map[string]any{"rows": rows, "more_records": more}, Object: object, Count: len(rows)}, friendly(err)
	default: // zoho_crm_get_record
		if err := needModule(); err != nil {
			return Result{}, err
		}
		id := argString(args, "id")
		if id == "" {
			return Result{}, errors.New("id is required")
		}
		rec, err := crm.GetRecord(ctx, module, id)
		count := 0
		if rec != nil {
			count = 1
		}
		return Result{Value: rec, Object: module, Count: count}, friendly(err)
	}
}

// ticketView is the compact ticket shape tools return.
type ticketView struct {
	ID           string `json:"id"`
	Number       string `json:"number"`
	Subject      string `json:"subject"`
	Status       string `json:"status"`
	Priority     string `json:"priority,omitempty"`
	DepartmentID string `json:"department_id,omitempty"`
	Assignee     string `json:"assignee,omitempty"`
	Contact      string `json:"contact,omitempty"`
	Due          string `json:"due,omitempty"`
	Created      string `json:"created,omitempty"`
	Modified     string `json:"modified,omitempty"`
	URL          string `json:"url,omitempty"`
	Description  string `json:"description,omitempty"`
}

func viewTicket(t zohodesk.Ticket, withDescription bool) ticketView {
	v := ticketView{
		ID: string(t.ID), Number: string(t.TicketNumber), Subject: t.Subject, Status: t.Status,
		Priority: t.Priority, DepartmentID: string(t.DepartmentID), Assignee: t.Assignee.Name(),
		Contact: t.Contact.Name(), Due: t.DueDate, Created: t.CreatedTime, Modified: t.ModifiedTime, URL: t.WebURL,
	}
	if v.Contact == "" {
		v.Contact = t.Email
	}
	if withDescription {
		v.Description = clip(t.Description, 2000)
	}
	return v
}

func callDesk(ctx context.Context, c Clients, name string, args map[string]any) (Result, error) {
	desk := c.Desk
	switch name {
	case "zoho_desk_departments":
		deps, err := desk.ListDepartments(ctx)
		out := make([]map[string]string, 0, len(deps))
		for _, d := range deps {
			out = append(out, map[string]string{"id": string(d.ID), "name": d.Name})
		}
		return Result{Value: map[string]any{"departments": out}, Object: "departments", Count: len(out)}, friendly(err)

	case "zoho_desk_list_tickets":
		q := zohodesk.TicketQuery{
			DepartmentID: argString(args, "department_id"),
			Status:       argString(args, "status"),
			Limit:        argInt(args, "limit", 25, 50),
		}
		if mine, _ := args["mine"].(bool); mine {
			if c.Me.DeskAgentID == "" {
				return Result{}, errors.New("this person isn't a Zoho Desk agent, so there are no tickets assigned to them")
			}
			q.AssigneeID = c.Me.DeskAgentID
		}
		list, err := desk.ListTickets(ctx, q)
		out := make([]ticketView, 0, len(list))
		for _, t := range list {
			out = append(out, viewTicket(t, false))
		}
		return Result{Value: map[string]any{"tickets": out}, Object: "tickets", Count: len(out)}, friendly(err)

	case "zoho_desk_get_ticket":
		if number := strings.TrimPrefix(argString(args, "ticket_number"), "#"); number != "" {
			found, err := desk.FindTicketsByNumber(ctx, number)
			if err != nil {
				return Result{}, friendly(err)
			}
			if len(found) == 0 {
				return Result{Value: map[string]any{"ticket": nil, "note": "No ticket with that number that this person can see."}, Object: "tickets"}, nil
			}
			t, err := desk.GetTicket(ctx, string(found[0].ID))
			if err != nil {
				return Result{}, friendly(err)
			}
			return Result{Value: map[string]any{"ticket": viewTicket(t, true)}, Object: "tickets", Count: 1}, nil
		}
		id := argString(args, "ticket_id")
		if id == "" {
			return Result{}, errors.New("give ticket_number or ticket_id")
		}
		t, err := desk.GetTicket(ctx, id)
		if err != nil {
			return Result{}, friendly(err)
		}
		return Result{Value: map[string]any{"ticket": viewTicket(t, true)}, Object: "tickets", Count: 1}, nil

	default: // zoho_desk_ticket_conversation
		id := argString(args, "ticket_id")
		if id == "" {
			return Result{}, errors.New("ticket_id is required")
		}
		threads, err := desk.ListThreads(ctx, id, argInt(args, "limit", 10, 20))
		out := make([]map[string]string, 0, len(threads))
		for _, th := range threads {
			out = append(out, map[string]string{
				"when":      th.CreatedTime,
				"from":      th.Author.Name,
				"direction": th.Direction,
				"summary":   clip(th.Summary, 500),
			})
		}
		return Result{Value: map[string]any{"messages": out}, Object: "tickets", Count: len(out)}, friendly(err)
	}
}
