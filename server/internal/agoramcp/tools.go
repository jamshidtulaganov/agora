package agoramcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/util"
)

type field struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Minimum     *int     `json:"minimum,omitempty"`
	Maximum     *int     `json:"maximum,omitempty"`
	UUID        bool     `json:"-"`
}

type inputSchema struct {
	Type                 string           `json:"type"`
	Properties           map[string]field `json:"properties"`
	Required             []string         `json:"required,omitempty"`
	AdditionalProperties bool             `json:"additionalProperties"`
}

type annotations struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

type tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
	Annotations annotations `json:"annotations"`
}

// Agent configuration can contain third-party tokens. Only assignment metadata
// is exposed, even when the authenticated owner can read the full REST resource.
type agentSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	RuntimeID string `json:"runtime_id"`
}

func text(description string) field { return field{Type: "string", Description: description} }
func uuid(description string) field { f := text(description + " (UUID)"); f.UUID = true; return f }
func enum(values ...string) field   { return field{Type: "string", Enum: values} }
func integer(min, max int) field    { return field{Type: "integer", Minimum: &min, Maximum: &max} }

func tools(writes bool) []tool {
	status := enum("backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled")
	priority := enum("urgent", "high", "medium", "low", "none")
	var result []tool
	add := func(name, description string, props map[string]field, required []string, write bool) {
		if props == nil {
			props = map[string]field{}
		}
		result = append(result, tool{name, description, inputSchema{"object", props, required, false}, annotations{!write, write, !write, true}})
	}
	add("get_workspace", "Read the configured Agora workspace. This server cannot switch workspaces.", nil, nil, false)
	add("list_projects", "List projects in the configured workspace.", map[string]field{"include_archived": {Type: "boolean"}}, nil, false)
	add("get_project", "Read project details and summary counts.", map[string]field{"project_id": uuid("Project ID")}, []string{"project_id"}, false)
	add("list_issues", "List tasks/issues visible to the authenticated caller. Returns a paginated API response; use offset to continue.", map[string]field{
		"status": status, "priority": priority, "project_id": uuid("Filter by project"), "assignee_id": uuid("Filter by assignee"), "limit": integer(1, 100), "offset": integer(0, 1000000),
	}, nil, false)
	add("search_issues", "Search task titles, descriptions, or identifiers. Closed issues are excluded unless include_closed is true.", map[string]field{
		"query": text("Search text or issue identifier"), "limit": integer(1, 50), "offset": integer(0, 1000000), "include_closed": {Type: "boolean"},
	}, []string{"query"}, false)
	add("get_issue", "Read an issue, its description, attachments, and labels. Use search_issues to resolve a human-readable issue key to its UUID.", map[string]field{"issue_id": uuid("Issue ID")}, []string{"issue_id"}, false)
	add("list_comments", "Read the latest comments on an issue (default 50).", map[string]field{"issue_id": uuid("Issue ID"), "limit": integer(1, 100)}, []string{"issue_id"}, false)
	add("list_knowledge", "List workspace knowledge documents and processing status.", nil, nil, false)
	add("search_knowledge", "Search workspace knowledge for expected behavior and requirements. Returns snippets, source IDs, and citation references; use get_knowledge for full evidence.", map[string]field{"query": text("Knowledge search text"), "limit": integer(1, 20)}, []string{"query"}, false)
	add("get_knowledge", "Read a workspace knowledge document and its extracted sections.", map[string]field{"document_id": uuid("Document ID")}, []string{"document_id"}, false)
	add("list_project_knowledge", "Read reviewed project knowledge items. Only active items are returned; proposed or archived items are not established requirements.", map[string]field{"project_id": uuid("Project ID")}, []string{"project_id"}, false)
	add("list_labels", "List workspace labels.", nil, nil, false)
	add("list_members", "List workspace members to resolve user assignment IDs.", nil, nil, false)
	add("list_agents", "List workspace agents to resolve agent assignment IDs. Does not expose agent credentials.", nil, nil, false)
	if writes {
		issueFields := func() map[string]field {
			return map[string]field{
				"title": text("Issue title"), "description": text("Markdown description, evidence, and acceptance criteria"), "status": status, "priority": priority,
				"project_id": uuid("Project ID"), "parent_issue_id": uuid("Parent issue ID"), "assignee_type": enum("member", "agent"), "assignee_id": uuid("Assignee ID"),
			}
		}
		add("create_issue", "Create a task only when the user requests it. Defaults to backlog when status is omitted. Assigning or changing status can trigger automations and notifications. Never automatically retry a failed write.", issueFields(), []string{"title"}, true)
		update := issueFields()
		update["issue_id"] = uuid("Issue ID")
		add("update_issue", "Update only supplied fields of a task. Requires an explicit user request; status/assignment changes may trigger execution, automations, and notifications. Never automatically retry a failed write.", update, []string{"issue_id"}, true)
		add("comment_issue", "Post a comment only when requested. Comments can notify members or trigger workspace automation. Never automatically retry a failed write.", map[string]field{"issue_id": uuid("Issue ID"), "content": text("Markdown comment content")}, []string{"issue_id", "content"}, true)
	}
	return result
}

func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
	var spec *tool
	for i := range s.tools {
		if s.tools[i].Name == name {
			spec = &s.tools[i]
			break
		}
	}
	if spec == nil {
		return nil, errors.New("unknown or disabled tool; task writes require starting Agora MCP with --allow-writes")
	}
	args, err := validateArguments(spec.InputSchema, raw)
	if err != nil {
		return nil, err
	}
	str := func(key string) string { value, _ := args[key].(string); return value }
	query := func() url.Values { return url.Values{"workspace_id": {s.client.WorkspaceID}} }
	page := func(q url.Values, key string, defaultValue int) {
		value := strconv.Itoa(defaultValue)
		if arg, ok := args[key].(json.Number); ok {
			value = arg.String()
		}
		q.Set(key, value)
	}
	path, method := "", "GET"
	var body map[string]any
	switch name {
	case "get_workspace":
		path = "/api/workspaces/" + s.client.WorkspaceID
	case "list_projects", "list_agents", "list_labels":
		q := query()
		if value, ok := args["include_archived"].(bool); ok {
			q.Set("include_archived", strconv.FormatBool(value))
		}
		path = "/api/" + strings.TrimPrefix(name, "list_") + "?" + q.Encode()
	case "get_project":
		path = "/api/projects/" + str("project_id")
	case "list_issues", "search_issues":
		q := query()
		page(q, "limit", 20)
		page(q, "offset", 0)
		for _, key := range []string{"status", "priority", "project_id", "assignee_id"} {
			if str(key) != "" {
				q.Set(key, str(key))
			}
		}
		path = "/api/issues"
		if name == "search_issues" {
			path += "/search"
			q.Set("q", str("query"))
			if value, ok := args["include_closed"].(bool); ok {
				q.Set("include_closed", strconv.FormatBool(value))
			}
		}
		path += "?" + q.Encode()
	case "get_issue":
		path = "/api/issues/" + str("issue_id")
	case "list_comments":
		q := url.Values{}
		page(q, "limit", 50)
		q.Set("tail", q.Get("limit"))
		q.Del("limit")
		path = "/api/issues/" + str("issue_id") + "/comments?" + q.Encode()
	case "list_knowledge":
		path = "/api/knowledge"
	case "search_knowledge":
		q := url.Values{"q": {str("query")}}
		page(q, "limit", 10)
		path = "/api/knowledge/search?" + q.Encode()
	case "get_knowledge":
		path = "/api/knowledge/" + str("document_id")
	case "list_project_knowledge":
		path = "/api/projects/" + str("project_id") + "/knowledge/items?status=active"
	case "list_members":
		path = "/api/workspaces/" + s.client.WorkspaceID + "/members"
	case "create_issue", "update_issue", "comment_issue":
		body = make(map[string]any)
		for key, value := range args {
			if key != "issue_id" {
				body[key] = value
			}
		}
		if name == "update_issue" && len(body) == 0 {
			return nil, errors.New("at least one update field is required")
		}
		if (str("assignee_id") == "") != (str("assignee_type") == "") {
			return nil, errors.New("assignee_id and assignee_type must be supplied together")
		}
		path, method = "/api/issues", "POST"
		if name == "create_issue" && str("status") == "" {
			// The REST API defaults to todo, which can start assigned agents.
			// MCP-created drafts stay parked unless the caller chooses a status.
			body["status"] = "backlog"
		}
		if name != "create_issue" {
			path += "/" + str("issue_id")
		}
		if name == "update_issue" {
			method = "PUT"
		}
		if name == "comment_issue" {
			path += "/comments"
		}
	default:
		return nil, errors.New("unsupported tool")
	}
	var result json.RawMessage
	switch method {
	case "POST":
		err = s.client.PostJSON(ctx, path, body, &result)
	case "PUT":
		err = s.client.PutJSON(ctx, path, body, &result)
	default:
		err = s.client.GetJSON(ctx, path, &result)
	}
	if err != nil {
		return nil, apiError(err)
	}
	if name == "list_agents" {
		var agents []agentSummary
		if err := json.Unmarshal(result, &agents); err != nil || agents == nil {
			return nil, errors.New("Agora API returned an invalid agent list")
		}
		for _, agent := range agents {
			if _, err := util.ParseUUID(agent.ID); err != nil || agent.Name == "" {
				return nil, errors.New("Agora API returned invalid agent assignment metadata")
			}
		}
		return json.Marshal(agents)
	}
	return result, nil
}

func validateArguments(schema inputSchema, raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil || args == nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	for _, key := range schema.Required {
		value, ok := args[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s is required", key)
		}
	}
	for key, value := range args {
		f, ok := schema.Properties[key]
		if !ok {
			return nil, fmt.Errorf("unknown argument: %s", key)
		}
		switch f.Type {
		case "string":
			v, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string", key)
			}
			if (key == "title" || f.UUID) && strings.TrimSpace(v) == "" {
				return nil, fmt.Errorf("%s must not be empty", key)
			}
			if f.UUID {
				if _, err := util.ParseUUID(v); err != nil {
					return nil, fmt.Errorf("%s must be a UUID", key)
				}
			}
			if len(f.Enum) > 0 {
				found := false
				for _, option := range f.Enum {
					if option == v {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("invalid %s; allowed values: %s", key, strings.Join(f.Enum, ", "))
				}
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", key)
			}
		case "integer":
			n, ok := value.(json.Number)
			if !ok {
				return nil, fmt.Errorf("%s must be an integer", key)
			}
			v, err := n.Int64()
			if err != nil || (f.Minimum != nil && v < int64(*f.Minimum)) || (f.Maximum != nil && v > int64(*f.Maximum)) {
				return nil, fmt.Errorf("%s is outside the allowed integer range", key)
			}
		}
	}
	return args, nil
}
