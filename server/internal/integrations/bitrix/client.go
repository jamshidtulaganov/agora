// Package bitrix is a DB-free client for the Bitrix24 inbound-webhook REST
// API plus the pure mapping helpers that translate a Bitrix task into a
// Agora issue. It deliberately depends on nothing from the handler/service
// layers so it can be unit-tested against httptest mock servers without a
// database.
//
// Bitrix is the "task master": its tasks are the source of truth. The client
// reads tasks (tasks.task.get), mirrors status back (tasks.task.update), and
// posts courtesy comments (task.commentitem.add) over a portal inbound
// webhook base URL of the form:
//
//	https://<portal>.bitrix24.<tld>/rest/<user-id>/<token>/
//
// The trailing slash is significant — REST method names are appended directly
// (e.g. "tasks.task.get"). NewClient normalizes a missing trailing slash.
package bitrix

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
	"time"
)

// defaultTimeout bounds a single Bitrix REST round-trip. Callers that need a
// tighter deadline pass their own context; this is just the transport-level
// safety net so a hung portal can't pin a goroutine forever.
const defaultTimeout = 15 * time.Second

// Client talks to one Bitrix24 portal over its inbound-webhook REST base URL.
// The base URL already embeds the credential (user id + token), so there is
// no separate auth step — possession of the URL is the credential.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client over the given inbound-webhook REST base URL.
// A missing trailing slash is added so method names append cleanly.
func NewClient(restBaseURL string) *Client {
	base := strings.TrimSpace(restBaseURL)
	if base != "" && !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return &Client{
		baseURL: base,
		http:    &http.Client{Timeout: defaultTimeout},
	}
}

// Task is the subset of a Bitrix task Agora cares about. All scalar fields
// are normalized to strings up front because Bitrix is wildly inconsistent
// about encoding numbers as JSON numbers vs. JSON strings.
type Task struct {
	ID            string
	Title         string
	Description   string
	Status        string
	ResponsibleID string
	GroupID       string
	Tags          []string
}

// jsonStr decodes a value that Bitrix may send as either a JSON string or a
// JSON number into a Go string. Empty / null becomes "". This is the core of
// the "flexible decoder" the spec calls for — STATUS, RESPONSIBLE_ID, ID and
// friends all flow through it.
type jsonStr string

func (s *jsonStr) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	// String form: strip the quotes via the standard decoder so escapes are
	// handled correctly.
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = jsonStr(str)
		return nil
	}
	// Number (or bool) form: use json.Number to preserve integer fidelity,
	// then take its string representation verbatim.
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		*s = jsonStr(num.String())
		return nil
	}
	// Last resort: a bare token like true/false. Keep the raw bytes.
	*s = jsonStr(string(b))
	return nil
}

func (s jsonStr) String() string { return string(s) }

// rawTask mirrors the SCREAMING_SNAKE fields Bitrix returns inside
// result.task. Tags are decoded with a custom unmarshaler because Bitrix
// uses at least three different shapes for them.
type rawTask struct {
	ID          jsonStr `json:"id"`
	IDUpper     jsonStr `json:"ID"`
	Title       jsonStr `json:"title"`
	TitleUpper  jsonStr `json:"TITLE"`
	Description jsonStr `json:"description"`
	DescUpper   jsonStr `json:"DESCRIPTION"`
	Status      jsonStr `json:"status"`
	StatusUpper jsonStr `json:"STATUS"`
	Responsible jsonStr `json:"responsibleId"`
	RespUpper   jsonStr `json:"RESPONSIBLE_ID"`
	GroupID     jsonStr `json:"groupId"`
	GroupUpper  jsonStr `json:"GROUP_ID"`
	Tags        rawTags `json:"tags"`
	TagsUpper   rawTags `json:"TAGS"`
}

// firstNonEmpty returns the first non-empty value among the candidates,
// letting us tolerate Bitrix returning either camelCase or SCREAMING_SNAKE
// keys depending on the API version / scope.
func firstNonEmpty(vals ...jsonStr) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v.String()); s != "" {
			return s
		}
	}
	return ""
}

func (rt rawTask) toTask() Task {
	tags := rt.Tags
	if len(tags) == 0 {
		tags = rt.TagsUpper
	}
	return Task{
		ID:            firstNonEmpty(rt.ID, rt.IDUpper),
		Title:         firstNonEmpty(rt.Title, rt.TitleUpper),
		Description:   firstNonEmpty(rt.Description, rt.DescUpper),
		Status:        firstNonEmpty(rt.Status, rt.StatusUpper),
		ResponsibleID: firstNonEmpty(rt.Responsible, rt.RespUpper),
		GroupID:       firstNonEmpty(rt.GroupID, rt.GroupUpper),
		Tags:          []string(tags),
	}
}

// rawTags carries the raw JSON of the tags field so parseTags can normalize
// its many shapes after the surrounding struct has decoded.
type rawTags []string

func (t *rawTags) UnmarshalJSON(b []byte) error {
	*t = parseTags(b)
	return nil
}

// parseTags normalizes every Bitrix tag encoding we've observed into a flat
// []string of tag names:
//
//   - array of strings:            ["ai", "urgent"]
//   - array of {NAME}/{TITLE}:     [{"NAME":"ai"}, {"TITLE":"urgent"}]
//   - map keyed by tag id:         {"12":{"NAME":"ai"}, "13":"urgent"}
//   - map of id -> name string:    {"12":"ai"}
//
// Anything it cannot interpret is skipped rather than erroring — a malformed
// tag must never block task sync.
func parseTags(raw []byte) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return nil
	}

	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}

	// Try array form first.
	if raw[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err == nil {
			for _, it := range items {
				add(tagFromElement(it))
			}
			return out
		}
		return nil
	}

	// Map form: keys are tag ids, values are either a name string or a
	// {NAME/TITLE} object.
	if raw[0] == '{' {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err == nil {
			for _, v := range m {
				add(tagFromElement(v))
			}
			return out
		}
		return nil
	}

	// Bare scalar (e.g. a single quoted tag).
	add(tagFromElement(raw))
	return out
}

// tagFromElement extracts a tag name from a single tag element, which may be
// a JSON string or a {NAME/TITLE} object.
func tagFromElement(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return ""
	}
	if raw[0] == '{' {
		var obj struct {
			Name       jsonStr `json:"NAME"`
			NameLower  jsonStr `json:"name"`
			Title      jsonStr `json:"TITLE"`
			TitleLower jsonStr `json:"title"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil {
			return firstNonEmpty(obj.Name, obj.NameLower, obj.Title, obj.TitleLower)
		}
		return ""
	}
	return ""
}

// taskGetResponse is the envelope Bitrix wraps every tasks.task.get response
// in: {"result":{"task":{...}}}. Error responses carry "error"/"error_description".
type taskGetResponse struct {
	Result struct {
		Task rawTask `json:"task"`
	} `json:"result"`
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

// errorEnvelope is the minimal shape of a Bitrix error response, shared by
// the write methods that don't parse a result payload.
type errorEnvelope struct {
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

// GetTask fetches a single task by id. taskID may be numeric or "T123" — it is
// passed through verbatim as the taskId query parameter.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("bitrix: empty task id")
	}

	endpoint := c.baseURL + "tasks.task.get"
	form := url.Values{}
	form.Set("taskId", taskID)
	// Ask Bitrix to return the fields we map. Bitrix ignores unknown select
	// entries, so over-asking is safe and forward-compatible.
	for _, f := range []string{"ID", "TITLE", "DESCRIPTION", "STATUS", "RESPONSIBLE_ID", "GROUP_ID", "TAGS"} {
		form.Add("select[]", f)
	}

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}

	var parsed taskGetResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode tasks.task.get: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: tasks.task.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	task := parsed.Result.Task.toTask()
	// Bitrix sometimes omits ID from the body but echoes the request id; fall
	// back to the requested id so downstream dedup keys stay stable.
	if task.ID == "" {
		task.ID = strings.TrimSpace(taskID)
	}
	return &task, nil
}

// Group is the subset of a Bitrix workgroup/project Agora cares about for the
// import browser. Like Task, scalars are normalized to strings because Bitrix
// encodes the id as either a JSON number or a JSON string depending on the
// portal.
type Group struct {
	ID   string
	Name string
}

// rawGroup mirrors the SCREAMING_SNAKE fields Bitrix returns for a workgroup
// in the sonet_group.get result array. Both the id and name are flexibly
// decoded so a number-vs-string id (the usual Bitrix inconsistency) parses.
type rawGroup struct {
	ID        jsonStr `json:"ID"`
	IDLower   jsonStr `json:"id"`
	Name      jsonStr `json:"NAME"`
	NameLower jsonStr `json:"name"`
}

// listGroupsResponse is the envelope for sonet_group.get. Unlike the task
// endpoints, the result is a bare ARRAY of group objects (not wrapped in a
// {tasks:[...]} object), so result decodes directly into a slice.
type listGroupsResponse struct {
	Result    []rawGroup `json:"result"`
	Error     string     `json:"error"`
	ErrorDesc string     `json:"error_description"`
}

// maxGroups caps how many workgroups ListGroups returns so a portal with
// thousands of projects can't balloon the picker. Matches the legacy bot's
// "newest ~30-50" behavior; the order is ID DESC (newest first) server-side.
const maxGroups = 50

// ListGroups returns the active Bitrix workgroups/projects, newest first. It
// POSTs sonet_group.get with ORDER ID DESC and FILTER ACTIVE=Y. The endpoint
// needs the "sonet" REST scope on the inbound webhook; when that scope is
// missing Bitrix returns an error envelope which surfaces as a non-nil error.
// The result is capped at maxGroups.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	endpoint := c.baseURL + "sonet_group.get"
	form := url.Values{}
	form.Set("ORDER[ID]", "DESC")
	form.Set("FILTER[ACTIVE]", "Y")

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}

	var parsed listGroupsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode sonet_group.get: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: sonet_group.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}

	groups := make([]Group, 0, len(parsed.Result))
	for _, rg := range parsed.Result {
		id := firstNonEmpty(rg.ID, rg.IDLower)
		if id == "" {
			continue // skip malformed rows rather than emitting blank ids
		}
		groups = append(groups, Group{
			ID:   id,
			Name: firstNonEmpty(rg.Name, rg.NameLower),
		})
		if len(groups) >= maxGroups {
			break
		}
	}
	return groups, nil
}

// listTasksResponse is the envelope for tasks.task.list: {"result":{"tasks":[...]}}.
type listTasksResponse struct {
	Result struct {
		Tasks []rawTask `json:"tasks"`
	} `json:"result"`
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

// maxTasksPerGroup caps a single ListTasks page. Bitrix returns 50 per page;
// the import browser shows one page (the newest tasks) rather than paginating,
// matching the legacy dashboard's per-group view.
const maxTasksPerGroup = 50

// ListTasks returns the tasks in a Bitrix workgroup, newest first. It POSTs
// tasks.task.list filtered by GROUP_ID (and TAG when tag != ""), selecting the
// fields Agora maps. Reuses the same rawTask → Task parsing as GetTask so a
// task's id/title/status/etc. decode identically regardless of Bitrix's
// number-vs-string quirks.
func (c *Client) ListTasks(ctx context.Context, groupID, tag string) ([]Task, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(groupID) == "" {
		return nil, errors.New("bitrix: empty group id")
	}
	endpoint := c.baseURL + "tasks.task.list"
	form := url.Values{}
	form.Set("filter[GROUP_ID]", strings.TrimSpace(groupID))
	if t := strings.TrimSpace(tag); t != "" {
		form.Set("filter[TAG]", t)
	}
	for _, f := range []string{"ID", "TITLE", "DESCRIPTION", "GROUP_ID", "RESPONSIBLE_ID", "STATUS", "TAGS"} {
		form.Add("select[]", f)
	}
	form.Set("order[ID]", "DESC")

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}

	var parsed listTasksResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode tasks.task.list: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: tasks.task.list error %s: %s", parsed.Error, parsed.ErrorDesc)
	}

	tasks := make([]Task, 0, len(parsed.Result.Tasks))
	for _, rt := range parsed.Result.Tasks {
		tasks = append(tasks, rt.toTask())
		if len(tasks) >= maxTasksPerGroup {
			break
		}
	}
	return tasks, nil
}

// AddTaskComment posts a comment to a task's comment feed via
// task.commentitem.add. Fields are sent as taskId + fields[POST_MESSAGE].
func (c *Client) AddTaskComment(ctx context.Context, taskID, text string) error {
	if c.baseURL == "" {
		return errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("bitrix: empty task id")
	}
	endpoint := c.baseURL + "task.commentitem.add"
	form := url.Values{}
	form.Set("TASKID", taskID)
	form.Set("FIELDS[POST_MESSAGE]", text)
	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return err
	}
	return checkError(body)
}

// updateTaskRequest is the JSON body for tasks.task.update.json. Bitrix
// accepts both form-encoded and JSON bodies for the .json suffix variant; the
// spec asks for JSON with a fields[STATUS] entry, so we send that.
type updateTaskRequest struct {
	TaskID string            `json:"taskId"`
	Fields map[string]string `json:"fields"`
}

// UpdateTaskStatus mirrors a status change back to Bitrix via
// tasks.task.update.json, setting fields[STATUS] to the Bitrix status code.
func (c *Client) UpdateTaskStatus(ctx context.Context, taskID, bitrixStatus string) error {
	if c.baseURL == "" {
		return errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("bitrix: empty task id")
	}
	endpoint := c.baseURL + "tasks.task.update.json"
	payload := updateTaskRequest{
		TaskID: taskID,
		Fields: map[string]string{"STATUS": bitrixStatus},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("bitrix: encode tasks.task.update: %w", err)
	}
	body, err := c.postJSON(ctx, endpoint, buf)
	if err != nil {
		return err
	}
	return checkError(body)
}

// post issues an x-www-form-urlencoded POST and returns the raw body.
func (c *Client) post(ctx context.Context, endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("bitrix: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// postJSON issues an application/json POST and returns the raw body.
func (c *Client) postJSON(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bitrix: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bitrix: request failed: %w", err)
	}
	defer resp.Body.Close()
	// Cap the body read so a misbehaving portal can't exhaust memory. 1 MiB is
	// far more than any task payload we care about.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("bitrix: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface Bitrix's structured error when present, else the status.
		if msg := errorMessage(body); msg != "" {
			return nil, fmt.Errorf("bitrix: http %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("bitrix: http %d", resp.StatusCode)
	}
	return body, nil
}

// checkError returns a non-nil error when the (2xx) body still carries a
// Bitrix-level error field.
func checkError(body []byte) error {
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		// A write call that returns non-JSON but 2xx is unusual but not fatal.
		return nil
	}
	if env.Error != "" {
		return fmt.Errorf("bitrix: error %s: %s", env.Error, env.ErrorDesc)
	}
	return nil
}

// errorMessage extracts a human-readable Bitrix error from a body, or "".
func errorMessage(body []byte) string {
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if env.ErrorDesc != "" {
		return env.ErrorDesc
	}
	return env.Error
}

// ParseWebhookEvent extracts the event name and task id from a Bitrix outbound
// event payload. Bitrix posts events as x-www-form-urlencoded with keys like:
//
//	event=ONTASKUPDATE
//	data[FIELDS_AFTER][ID]=123
//
// Only ONTASKADD / ONTASKUPDATE are handled; everything else returns ok=false.
// The id is read from data[FIELDS_AFTER][ID], falling back to
// data[FIELDS_BEFORE][ID] for delete-shaped payloads.
func ParseWebhookEvent(values url.Values) (event string, taskID string, ok bool) {
	event = strings.ToUpper(strings.TrimSpace(values.Get("event")))
	switch event {
	case "ONTASKADD", "ONTASKUPDATE":
	default:
		return event, "", false
	}

	for _, key := range []string{
		"data[FIELDS_AFTER][ID]",
		"data[FIELDS_BEFORE][ID]",
		"data[fields_after][id]",
		"DATA[FIELDS_AFTER][ID]",
	} {
		if id := strings.TrimSpace(values.Get(key)); id != "" {
			// Bitrix occasionally pads ids; strconv round-trip normalizes
			// "00123" -> "123" while leaving non-numeric ids (e.g. "T9") intact.
			if n, err := strconv.Atoi(id); err == nil {
				return event, strconv.Itoa(n), true
			}
			return event, id, true
		}
	}
	return event, "", false
}
