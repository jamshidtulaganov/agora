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
	"sort"
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
	// CreatedByID is the Bitrix user who OPENED the task. Distinct from
	// ResponsibleID: a breakdown by assignee shows where work landed, by
	// creator shows where it comes from, and only the second one tells you
	// whether a sprint plan matches reality.
	CreatedByID string
	// Priority is Bitrix's numeric urgency: 0 low, 1 normal, 2 high.
	Priority string
	GroupID  string
	// Deadline is what "overdue" is measured against. A portal that tracks
	// deadlines and a rollup that cannot see them disagree about the single
	// number a manager looks for first.
	Deadline string
	// SprintID / FlowID / ParentID place the task in the team's own structure:
	// which sprint, which intake flow, which epic. GROUP_ID answers none of
	// these — a project can run many sprints.
	SprintID string
	FlowID   string
	ParentID string
	// TimeEstimate and DurationFact are planned vs actual, in seconds. The gap
	// between them is the only measure of whether estimates mean anything.
	TimeEstimate string
	DurationFact string
	// Accomplices and Auditors are the other people attached to a task.
	// RESPONSIBLE_ID alone understates who is involved.
	Accomplices []string
	Auditors    []string
	// StageID is the scrum/kanban STAGE_ID (the live kanban column), resolved to
	// a human stage name via task.stages.get. Empty for tasks not on a kanban.
	StageID string
	// ChatID is the task's comment-chat id (newer tasks); the chat dialog is
	// "chat<ChatID>", read via im.dialog.messages.get.
	ChatID string
	// GroupName is the Bitrix workgroup name when the payload carried a nested
	// group object (some tasks.task.get responses include {group:{id,name}}).
	// Often empty — the handler resolves the name from GROUP_ID via a cached
	// ListGroups / GetGroup lookup when this is blank.
	GroupName string
	Tags      []string
	// CreatedAt / ClosedAt are the Bitrix task lifecycle timestamps, in the
	// portal's own format ("2026-01-14T09:12:03+05:00"). Only populated by
	// calls that SELECT them (see ListTasksSince) — the import paths do not,
	// so they are empty there. Kept as strings because Bitrix is inconsistent
	// about offsets across portals; callers parse with ParseTime.
	CreatedAt string
	ClosedAt  string
}

// ParseTime parses a Bitrix timestamp, returning ok=false for the empty string
// or an unrecognised shape. Bitrix returns RFC3339 on modern portals but has
// been observed emitting a space-separated form, so both are accepted.
func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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
	// CreatedBy answers "where is the work coming from" — the question a
	// per-assignee breakdown cannot, since it only shows where work landed.
	CreatedBy      jsonStr `json:"createdBy"`
	CreatedByUpper jsonStr `json:"CREATED_BY"`
	Priority       jsonStr `json:"priority"`
	PriorityUpper  jsonStr `json:"PRIORITY"`
	Deadline       jsonStr `json:"deadline"`
	DeadlineUpper  jsonStr `json:"DEADLINE"`
	SprintID       jsonStr `json:"sprintId"`
	SprintUpper    jsonStr `json:"SPRINT_ID"`
	FlowID         jsonStr `json:"flowId"`
	FlowUpper      jsonStr `json:"FLOW_ID"`
	ParentID       jsonStr `json:"parentId"`
	ParentUpper    jsonStr `json:"PARENT_ID"`
	TimeEstimate   jsonStr `json:"timeEstimate"`
	TimeEstUpper   jsonStr `json:"TIME_ESTIMATE"`
	DurationFact   jsonStr `json:"durationFact"`
	DurFactUpper   jsonStr `json:"DURATION_FACT"`
	Accomplices    rawIDs  `json:"accomplices"`
	AccompUpper    rawIDs  `json:"ACCOMPLICES"`
	Auditors       rawIDs  `json:"auditors"`
	AuditorsUpper  rawIDs  `json:"AUDITORS"`
	GroupID        jsonStr `json:"groupId"`
	GroupUpper     jsonStr `json:"GROUP_ID"`
	// Scrum/kanban STAGE_ID — the live kanban column the dev team moves the task
	// through (Новые / Code Review / Testing / Сделаны …), distinct from STATUS
	// (the coarse Bitrix task state). Resolved to a name via task.stages.get.
	StageID    jsonStr `json:"stageId"`
	StageUpper jsonStr `json:"STAGE_ID"`
	// Lifecycle timestamps — present only when the request SELECTs them.
	CreatedDate      jsonStr `json:"createdDate"`
	CreatedDateUpper jsonStr `json:"CREATED_DATE"`
	ClosedDate       jsonStr `json:"closedDate"`
	ClosedDateUpper  jsonStr `json:"CLOSED_DATE"`
	// Task comment CHAT id — newer tasks keep discussion in a chat dialog
	// (chat<ChatID>) rather than the legacy task.commentitem feed.
	ChatID    jsonStr `json:"chatId"`
	ChatUpper jsonStr `json:"CHAT_ID"`
	// Group is the optional nested workgroup object Bitrix includes on some
	// tasks.task.get responses ({"group":{"id":5,"name":"Sprint 12"}}). When
	// present it lets us pick up the group NAME without a second sonet_group.get.
	Group     rawGroupRef `json:"group"`
	GroupUp   rawGroupRef `json:"GROUP"`
	Tags      rawTags     `json:"tags"`
	TagsUpper rawTags     `json:"TAGS"`
}

// rawGroupRef is the nested {id,name} object Bitrix sometimes embeds in a task
// under "group". Both fields are flexibly typed.
type rawGroupRef struct {
	ID        jsonStr `json:"id"`
	IDUpper   jsonStr `json:"ID"`
	Name      jsonStr `json:"name"`
	NameUpper jsonStr `json:"NAME"`
}

// UnmarshalJSON tolerates Bitrix's two shapes for the nested group: an object
// ({"id":5,"name":"Sprint 9"}) on a grouped task, OR an array ([] when the task
// has no workgroup — seen in tasks.task.list filtered by RESPONSIBLE_ID). The
// array form decodes to the zero value rather than erroring the whole response.
func (g *rawGroupRef) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] == '[' || string(trimmed) == "null" {
		return nil // array / empty / null → no embedded group
	}
	type alias rawGroupRef
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*g = rawGroupRef(a)
	return nil
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
	groupID := firstNonEmpty(rt.GroupID, rt.GroupUpper)
	if groupID == "" {
		// Fall back to the nested group object's id when the flat field is absent.
		groupID = firstNonEmpty(rt.Group.ID, rt.Group.IDUpper, rt.GroupUp.ID, rt.GroupUp.IDUpper)
	}
	return Task{
		ID:            firstNonEmpty(rt.ID, rt.IDUpper),
		Title:         firstNonEmpty(rt.Title, rt.TitleUpper),
		Description:   firstNonEmpty(rt.Description, rt.DescUpper),
		Status:        firstNonEmpty(rt.Status, rt.StatusUpper),
		StageID:       firstNonEmpty(rt.StageID, rt.StageUpper),
		ChatID:        firstNonEmpty(rt.ChatID, rt.ChatUpper),
		ResponsibleID: firstNonEmpty(rt.Responsible, rt.RespUpper),
		CreatedByID:   firstNonEmpty(rt.CreatedBy, rt.CreatedByUpper),
		Priority:      firstNonEmpty(rt.Priority, rt.PriorityUpper),
		Deadline:      firstNonEmpty(rt.Deadline, rt.DeadlineUpper),
		SprintID:      firstNonEmpty(rt.SprintID, rt.SprintUpper),
		FlowID:        firstNonEmpty(rt.FlowID, rt.FlowUpper),
		ParentID:      firstNonEmpty(rt.ParentID, rt.ParentUpper),
		TimeEstimate:  firstNonEmpty(rt.TimeEstimate, rt.TimeEstUpper),
		DurationFact:  firstNonEmpty(rt.DurationFact, rt.DurFactUpper),
		Accomplices:   firstNonEmptyIDs(rt.Accomplices, rt.AccompUpper),
		Auditors:      firstNonEmptyIDs(rt.Auditors, rt.AuditorsUpper),
		GroupID:       groupID,
		GroupName:     firstNonEmpty(rt.Group.Name, rt.Group.NameUpper, rt.GroupUp.Name, rt.GroupUp.NameUpper),
		Tags:          []string(tags),
		CreatedAt:     firstNonEmpty(rt.CreatedDate, rt.CreatedDateUpper),
		ClosedAt:      firstNonEmpty(rt.ClosedDate, rt.ClosedDateUpper),
	}
}

// rawIDs decodes a Bitrix id list, which arrives as an array of numbers, an
// array of strings, or an empty object depending on the field and the task.
// Anything unrecognised decodes to empty rather than failing the response — a
// missing accomplice list must not cost the whole rollup.
type rawIDs []string

func (r *rawIDs) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}
	var raw []jsonStr
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s := strings.TrimSpace(v.String()); s != "" {
			out = append(out, s)
		}
	}
	*r = out
	return nil
}

// firstNonEmptyIDs picks whichever casing the portal used.
func firstNonEmptyIDs(vals ...rawIDs) []string {
	for _, v := range vals {
		if len(v) > 0 {
			return []string(v)
		}
	}
	return nil
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
	for _, f := range []string{"ID", "TITLE", "DESCRIPTION", "STATUS", "STAGE_ID", "CHAT_ID", "RESPONSIBLE_ID", "GROUP_ID", "TAGS"} {
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

// bitrixEmptyResult reports whether a Bitrix `result` payload is one of the
// "empty set" sentinels the REST API returns instead of an empty array. Several
// list endpoints (task.commentitem.getlist, sonet_group.get, user.get,
// department.get, event.get) return the JSON literal `false` — and occasionally
// `null` — for a zero-row result set rather than `[]`. Decoding that straight
// into a []T target fails with "cannot unmarshal bool into ... of type []T", so
// every bare-array list decoder captures `result` as a RawMessage and skips the
// array decode when this reports true (leaving the caller with an empty slice).
func bitrixEmptyResult(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("false")) || bytes.Equal(t, []byte("null"))
}

// listGroupsResponse is the envelope for sonet_group.get. Unlike the task
// endpoints, the result is a bare ARRAY of group objects (not wrapped in a
// {tasks:[...]} object). It is captured raw so the false/null empty-set
// sentinel (see bitrixEmptyResult) decodes to an empty list, not an error.
type listGroupsResponse struct {
	Result    json.RawMessage `json:"result"`
	Error     string          `json:"error"`
	ErrorDesc string          `json:"error_description"`
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

	var rows []rawGroup
	if !bitrixEmptyResult(parsed.Result) {
		if err := json.Unmarshal(parsed.Result, &rows); err != nil {
			return nil, fmt.Errorf("bitrix: decode sonet_group.get result: %w", err)
		}
	}

	groups := make([]Group, 0, len(rows))
	for _, rg := range rows {
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

// listTasksResponse is the envelope for tasks.task.list:
// {"result":{"tasks":[...]}, "total":N, "next":M}. Total is the group's full
// task count; Next is the offset to pass as `start` for the following page.
// Bitrix omits Next on the final page, which is how pagination terminates.
type listTasksResponse struct {
	Result struct {
		Tasks []rawTask `json:"tasks"`
	} `json:"result"`
	Total     *int   `json:"total"`
	Next      *int   `json:"next"`
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

// Bitrix tasks.task.list returns up to 50 tasks per page. ListTasks paginates
// through every page (following the response's `next` offset) so a full import
// captures the entire group, not just the newest 50. bitrixTaskPageSize is the
// fixed Bitrix page size; bitrixMaxTasksPerImport is a runaway safety cap.
const (
	bitrixTaskPageSize      = 50
	bitrixMaxTasksPerImport = 5000
)

// MaxTasksPerRequest exposes the safety cap so callers can tell a COMPLETE
// result from a truncated one. Hitting it means the window held more tasks
// than one call returns, and the caller should narrow the range rather than
// report the totals as portal-wide truth.
const MaxTasksPerRequest = bitrixMaxTasksPerImport

// ListTasks returns ALL tasks in a Bitrix workgroup, newest first. It POSTs
// tasks.task.list filtered by GROUP_ID (and TAG when tag != ""), selecting the
// fields Agora maps, and follows the response's `next` offset across every page
// (Bitrix returns 50 per page). Reuses the same rawTask → Task parsing as
// GetTask so a task's id/title/status/etc. decode identically regardless of
// Bitrix's number-vs-string quirks. A safety cap (bitrixMaxTasksPerImport)
// bounds pathological groups; the page loop also breaks on an empty page or a
// non-advancing `next` so a misbehaving portal can't spin forever.
func (c *Client) ListTasks(ctx context.Context, groupID, tag string) ([]Task, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(groupID) == "" {
		return nil, errors.New("bitrix: empty group id")
	}
	endpoint := c.baseURL + "tasks.task.list"

	tasks := make([]Task, 0, bitrixTaskPageSize)
	start := 0
	for {
		form := url.Values{}
		form.Set("filter[GROUP_ID]", strings.TrimSpace(groupID))
		if t := strings.TrimSpace(tag); t != "" {
			form.Set("filter[TAG]", t)
		}
		for _, f := range []string{"ID", "TITLE", "DESCRIPTION", "GROUP_ID", "RESPONSIBLE_ID", "STATUS", "TAGS"} {
			form.Add("select[]", f)
		}
		form.Set("order[ID]", "DESC")
		if start > 0 {
			form.Set("start", strconv.Itoa(start))
		}

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

		for _, rt := range parsed.Result.Tasks {
			tasks = append(tasks, rt.toTask())
		}

		// Terminate: no further page (Bitrix omits `next` on the last page),
		// an empty page, the safety cap, or a `next` that doesn't advance the
		// offset (defensive against a portal echoing the same start).
		if parsed.Next == nil || len(parsed.Result.Tasks) == 0 || len(tasks) >= bitrixMaxTasksPerImport || *parsed.Next <= start {
			break
		}
		start = *parsed.Next
	}
	return tasks, nil
}

// ListTasksSince returns every task CREATED at or after `since`, across all
// workgroups, with the lifecycle fields analytics needs.
//
// Distinct from ListTasks in three ways that matter:
//   - filters by date, not by group, so a portal-wide year-to-date view is one
//     call chain instead of one per workgroup;
//   - SELECTs CREATED_DATE / CLOSED_DATE, which the import path deliberately
//     omits (it only needs identity + routing), so without this the tasks carry
//     no timestamps and no time-series is possible;
//   - applies no TAG filter — analytics must count the whole portal, whereas
//     the importer intentionally narrows to its own tag.
//
// Paging follows Bitrix's `next` offset and stops on the same guards as
// ListTasks, including the bitrixMaxTasksPerImport safety cap; a portal with
// more matching tasks than that cap returns the newest ones (order[ID] DESC)
// and the caller should narrow the window rather than assume completeness.
func (c *Client) ListTasksSince(ctx context.Context, since time.Time) ([]Task, error) {
	return c.ListTasksBetween(ctx, since, time.Time{})
}

// ListTasksBetween is ListTasksSince with a closed upper bound. A zero `until`
// means "up to now" and adds no upper filter.
//
// The bound exists so a caller can reconstruct a PAST window — "the same period
// a week ago" — which is what any week-over-week comparison needs. Without it a
// weekly report can only ever state the current level, never the change, and
// the change is the whole point of running it weekly.
func (c *Client) ListTasksBetween(ctx context.Context, since, until time.Time) ([]Task, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	endpoint := c.baseURL + "tasks.task.list"

	tasks := make([]Task, 0, bitrixTaskPageSize)
	start := 0
	for {
		form := url.Values{}
		form.Set("filter[>=CREATED_DATE]", since.Format("2006-01-02 15:04:05"))
		if !until.IsZero() {
			form.Set("filter[<=CREATED_DATE]", until.Format("2006-01-02 15:04:05"))
		}
		for _, f := range []string{
			"ID", "TITLE", "GROUP_ID", "RESPONSIBLE_ID", "CREATED_BY", "STATUS",
			"PRIORITY", "STAGE_ID", "CREATED_DATE", "CLOSED_DATE", "TAGS",
			"DEADLINE", "SPRINT_ID", "FLOW_ID", "PARENT_ID",
			"TIME_ESTIMATE", "DURATION_FACT", "ACCOMPLICES", "AUDITORS",
		} {
			form.Add("select[]", f)
		}
		form.Set("order[ID]", "DESC")
		if start > 0 {
			form.Set("start", strconv.Itoa(start))
		}

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
		for _, rt := range parsed.Result.Tasks {
			tasks = append(tasks, rt.toTask())
		}
		if parsed.Next == nil || len(parsed.Result.Tasks) == 0 ||
			len(tasks) >= bitrixMaxTasksPerImport || *parsed.Next <= start {
			break
		}
		start = *parsed.Next
	}
	return tasks, nil
}

// ListUsers returns ALL of the portal's active users so the importer's "import
// by responsible" picker lists everyone, not just the first page. Bitrix
// user.get pages at 50; this follows the `next` offset across every page —
// without it a portal with hundreds of users hid anyone past user #50 from the
// picker, so their tasks couldn't be imported by responsible.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	endpoint := c.baseURL + "user.get"

	users := make([]User, 0, bitrixTaskPageSize)
	start := 0
	for {
		form := url.Values{}
		form.Set("FILTER[ACTIVE]", "true")
		form.Set("sort", "LAST_NAME")
		form.Set("order", "asc")
		if start > 0 {
			form.Set("start", strconv.Itoa(start))
		}

		body, err := c.post(ctx, endpoint, form)
		if err != nil {
			return nil, err
		}
		type userRow struct {
			ID           jsonStr `json:"ID"`
			Name         jsonStr `json:"NAME"`
			LastName     jsonStr `json:"LAST_NAME"`
			Email        jsonStr `json:"EMAIL"`
			WorkPosition jsonStr `json:"WORK_POSITION"`
			Department   deptIDs `json:"UF_DEPARTMENT"`
		}
		var parsed struct {
			Result    json.RawMessage `json:"result"`
			Next      *int            `json:"next"`
			Error     string          `json:"error"`
			ErrorDesc string          `json:"error_description"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("bitrix: decode user.get (list): %w", err)
		}
		if parsed.Error != "" {
			return nil, fmt.Errorf("bitrix: user.get (list) error %s: %s", parsed.Error, parsed.ErrorDesc)
		}
		var rows []userRow
		if !bitrixEmptyResult(parsed.Result) {
			if err := json.Unmarshal(parsed.Result, &rows); err != nil {
				return nil, fmt.Errorf("bitrix: decode user.get (list) result: %w", err)
			}
		}
		for _, r := range rows {
			id := firstNonEmpty(r.ID)
			if id == "" {
				continue
			}
			users = append(users, User{
				ID:         id,
				Name:       firstNonEmpty(r.Name),
				LastName:   firstNonEmpty(r.LastName),
				Email:      firstNonEmpty(r.Email),
				Position:   firstNonEmpty(r.WorkPosition),
				Department: r.Department,
			})
		}
		if parsed.Next == nil || len(rows) == 0 || len(users) >= bitrixMaxTasksPerImport || *parsed.Next <= start {
			break
		}
		start = *parsed.Next
	}
	return users, nil
}

// ListTasksByUser returns ALL tasks a given user is RESPONSIBLE for, for the
// "import by responsible" flow. Mirrors ListTasks but filters on RESPONSIBLE_ID
// instead of GROUP_ID, paginating through every page the same way.
func (c *Client) ListTasksByUser(ctx context.Context, userID, tag string) ([]Task, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("bitrix: empty user id")
	}
	endpoint := c.baseURL + "tasks.task.list"

	tasks := make([]Task, 0, bitrixTaskPageSize)
	start := 0
	for {
		form := url.Values{}
		form.Set("filter[RESPONSIBLE_ID]", strings.TrimSpace(userID))
		if t := strings.TrimSpace(tag); t != "" {
			form.Set("filter[TAG]", t)
		}
		for _, f := range []string{"ID", "TITLE", "DESCRIPTION", "GROUP_ID", "RESPONSIBLE_ID", "STATUS", "TAGS"} {
			form.Add("select[]", f)
		}
		form.Set("order[ID]", "DESC")
		if start > 0 {
			form.Set("start", strconv.Itoa(start))
		}

		body, err := c.post(ctx, endpoint, form)
		if err != nil {
			return nil, err
		}
		var parsed listTasksResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("bitrix: decode tasks.task.list (by user): %w", err)
		}
		if parsed.Error != "" {
			return nil, fmt.Errorf("bitrix: tasks.task.list (by user) error %s: %s", parsed.Error, parsed.ErrorDesc)
		}
		for _, rt := range parsed.Result.Tasks {
			tasks = append(tasks, rt.toTask())
		}
		if parsed.Next == nil || len(parsed.Result.Tasks) == 0 || len(tasks) >= bitrixMaxTasksPerImport || *parsed.Next <= start {
			break
		}
		start = *parsed.Next
	}
	return tasks, nil
}

// GetTaskChatMessages fetches a task's comment-CHAT messages (im.dialog.messages
// .get on dialog "chat<chatID>") — how NEWER Bitrix tasks store their discussion
// (the legacy task.commentitem feed is empty for them). Returns each message as
// a Comment (id/author/date/text). Empty + nil error when chatID is blank; an
// error (often a permission/scope error if the webhook lacks `im`) when the call
// fails, which the caller logs and treats as "no chat comments".
func (c *Client) GetTaskChatMessages(ctx context.Context, chatID string) ([]Comment, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, nil
	}
	endpoint := c.baseURL + "im.dialog.messages.get"
	form := url.Values{}
	form.Set("DIALOG_ID", "chat"+chatID)
	form.Set("LIMIT", "50")

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result struct {
			Messages []struct {
				ID       jsonStr `json:"id"`
				AuthorID jsonStr `json:"author_id"`
				Date     jsonStr `json:"date"`
				Text     jsonStr `json:"text"`
			} `json:"messages"`
			Users []struct {
				ID   jsonStr `json:"id"`
				Name jsonStr `json:"name"`
			} `json:"users"`
		} `json:"result"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode im.dialog.messages.get: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: im.dialog.messages.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	names := make(map[string]string, len(parsed.Result.Users))
	for _, u := range parsed.Result.Users {
		if id := firstNonEmpty(u.ID); id != "" {
			names[id] = firstNonEmpty(u.Name)
		}
	}
	out := make([]Comment, 0, len(parsed.Result.Messages))
	for _, m := range parsed.Result.Messages {
		text := strings.TrimSpace(string(m.Text))
		if text == "" {
			continue
		}
		authorID := strings.TrimSpace(firstNonEmpty(m.AuthorID))
		// Skip portal SYSTEM messages (author id 0) — these are the kanban
		// stage-change / status notifications ("X изменил стадию на Y"), which
		// are noise in Agora (the stage already drives the issue status) and
		// would otherwise import attributed to no real person.
		if authorID == "" || authorID == "0" {
			continue
		}
		author := names[authorID]
		if author == "" {
			author = "user " + authorID
		}
		out = append(out, Comment{
			// Namespace the id so a chat message can't collide with a
			// commentitem id in the issue's synced-id dedup set.
			ID:       "chat-" + firstNonEmpty(m.ID),
			Author:   author,
			Date:     firstNonEmpty(m.Date),
			Text:     text,
			AuthorID: authorID,
		})
	}
	return out, nil
}

// Comment is the subset of a Bitrix task comment Agora mirrors into an issue
// comment. Author/Date are normalized strings; Text is the raw POST_MESSAGE
// (BB-code, left untouched — the handler renders it verbatim).
type Comment struct {
	ID     string
	Author string
	Date   string
	Text   string
	// AuthorID is the Bitrix user id of the comment author, when known (chat
	// messages carry it). Empty for the legacy commentitem feed. Lets the
	// importer attribute the Agora comment to the real person instead of the
	// workspace owner.
	AuthorID string
}

// rawComment mirrors the SCREAMING_SNAKE fields task.commentitem.getlist
// returns per comment. Bitrix returns the list as a bare array (like
// sonet_group.get), so the envelope decodes result directly into a slice.
type rawComment struct {
	ID         jsonStr `json:"ID"`
	IDLower    jsonStr `json:"id"`
	PostMsg    jsonStr `json:"POST_MESSAGE"`
	PostMsgL   jsonStr `json:"postMessage"`
	AuthorName jsonStr `json:"AUTHOR_NAME"`
	AuthorL    jsonStr `json:"authorName"`
	PostDate   jsonStr `json:"POST_DATE"`
	PostDateL  jsonStr `json:"postDate"`
}

// listCommentsResponse is the envelope for task.commentitem.getlist. Like
// sonet_group.get the result is a bare array, not wrapped in an object, and is
// captured raw so the false/null empty-set sentinel (a task with no comments —
// the common case) decodes to an empty list instead of erroring. See
// bitrixEmptyResult.
type listCommentsResponse struct {
	Result    json.RawMessage `json:"result"`
	Error     string          `json:"error"`
	ErrorDesc string          `json:"error_description"`
}

// maxCommentsPerTask caps how many comments GetTaskComments returns so a task
// with a long discussion thread can't balloon the import. Mirrors the legacy
// bot's bounded comment mirror.
const maxCommentsPerTask = 50

// GetTaskComments returns a task's comment feed (author, date, text), oldest
// first, via task.commentitem.getlist with ORDER[ID]=asc. Comments with an
// empty POST_MESSAGE (file-only system rows) are skipped. The result is capped
// at maxCommentsPerTask. Reuses the same flexible jsonStr decoding as the rest
// of the client so a number-vs-string id / author parses identically.
func (c *Client) GetTaskComments(ctx context.Context, taskID string) ([]Comment, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("bitrix: empty task id")
	}
	endpoint := c.baseURL + "task.commentitem.getlist"
	form := url.Values{}
	// NOTE: do NOT pass an ORDER[...] param. The legacy task.commentitem.getlist
	// binds arguments POSITIONALLY, and url.Values.Encode sorts "ORDER[ID]" ahead
	// of "taskId" — so Bitrix maps the ORDER array into the first positional arg
	// ($taskId) and rejects it ("Param #0 (taskId) expected to be of type
	// integer"). We request unordered and sort client-side below instead.
	form.Set("taskId", strings.TrimSpace(taskID))

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}

	var parsed listCommentsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode task.commentitem.getlist: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: task.commentitem.getlist error %s: %s", parsed.Error, parsed.ErrorDesc)
	}

	var rows []rawComment
	if !bitrixEmptyResult(parsed.Result) {
		if err := json.Unmarshal(parsed.Result, &rows); err != nil {
			return nil, fmt.Errorf("bitrix: decode task.commentitem.getlist result: %w", err)
		}
	}

	comments := make([]Comment, 0, len(rows))
	for _, rc := range rows {
		text := strings.TrimSpace(firstNonEmpty(rc.PostMsg, rc.PostMsgL))
		if text == "" {
			continue // file-only / system row — nothing to mirror as a comment
		}
		comments = append(comments, Comment{
			ID:     firstNonEmpty(rc.ID, rc.IDLower),
			Author: firstNonEmpty(rc.AuthorName, rc.AuthorL),
			Date:   firstNonEmpty(rc.PostDate, rc.PostDateL),
			Text:   text,
		})
	}
	// Oldest-first by numeric comment id (we can't ask Bitrix to ORDER; see the
	// note above), then cap. Falls back to a string compare for non-numeric ids.
	sort.Slice(comments, func(i, j int) bool {
		a, errA := strconv.Atoi(comments[i].ID)
		b, errB := strconv.Atoi(comments[j].ID)
		if errA == nil && errB == nil {
			return a < b
		}
		return comments[i].ID < comments[j].ID
	})
	if len(comments) > maxCommentsPerTask {
		comments = comments[:maxCommentsPerTask]
	}
	return comments, nil
}

// File is a task attachment Agora mirrors into an issue attachment. URL is the
// (possibly host-relative) Bitrix DOWNLOAD_URL; the credential is in the REST
// base URL so it can be fetched directly via DownloadFile. ContentType is left
// empty by the listing call (Bitrix doesn't return it on disk.attachedObject.get)
// and inferred by the handler from the filename extension.
type File struct {
	ID          string
	Name        string
	URL         string
	Size        int64
	ContentType string
}

// taskFilesResponse decodes tasks.task.get when selecting UF_TASK_WEBDAV_FILES.
// The file id list is flexibly typed (string or number ids), so it is captured
// raw and normalized via parseFileIDs.
type taskFilesResponse struct {
	Result struct {
		Task struct {
			Files  json.RawMessage `json:"ufTaskWebdavFiles"`
			FilesU json.RawMessage `json:"UF_TASK_WEBDAV_FILES"`
		} `json:"task"`
	} `json:"result"`
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

// attachedObjectResponse decodes disk.attachedObject.get: {"result":{NAME,DOWNLOAD_URL,SIZE}}.
type attachedObjectResponse struct {
	Result struct {
		ID          jsonStr `json:"ID"`
		IDLower     jsonStr `json:"id"`
		Name        jsonStr `json:"NAME"`
		NameLower   jsonStr `json:"name"`
		DownloadURL jsonStr `json:"DOWNLOAD_URL"`
		DownloadL   jsonStr `json:"downloadUrl"`
		Size        jsonStr `json:"SIZE"`
		SizeLower   jsonStr `json:"size"`
	} `json:"result"`
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

// maxFilesPerTask caps how many attachments GetTaskFiles resolves so a task
// with dozens of files can't make the sync goroutine fan out unbounded
// disk.attachedObject.get calls. Matches the legacy bot's per-task cap.
const maxFilesPerTask = 8

// GetTaskFiles resolves a task's attachments. It first reads the task's
// UF_TASK_WEBDAV_FILES (a list of disk file ids) via tasks.task.get, then
// resolves each id to NAME/DOWNLOAD_URL/SIZE via disk.attachedObject.get. The
// returned URL is made absolute against the portal host derived from the REST
// base URL when Bitrix hands back a host-relative path. The result is capped at
// maxFilesPerTask; an individual file that fails to resolve is skipped rather
// than failing the whole call.
func (c *Client) GetTaskFiles(ctx context.Context, taskID string) ([]File, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("bitrix: empty task id")
	}

	endpoint := c.baseURL + "tasks.task.get"
	form := url.Values{}
	form.Set("taskId", strings.TrimSpace(taskID))
	form.Add("select[]", "ID")
	form.Add("select[]", "UF_TASK_WEBDAV_FILES")

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	var parsed taskFilesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode tasks.task.get (files): %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: tasks.task.get (files) error %s: %s", parsed.Error, parsed.ErrorDesc)
	}

	ids := parseFileIDs(parsed.Result.Task.Files)
	if len(ids) == 0 {
		ids = parseFileIDs(parsed.Result.Task.FilesU)
	}

	files := make([]File, 0, len(ids))
	for _, fid := range ids {
		if len(files) >= maxFilesPerTask {
			break
		}
		f, err := c.getAttachedObject(ctx, fid)
		if err != nil {
			// A single unresolved file must not abort the whole attachment
			// sync — skip it and keep going.
			continue
		}
		if f.URL == "" {
			continue
		}
		files = append(files, f)
	}
	return files, nil
}

// getAttachedObject resolves one disk file id to a File via disk.attachedObject.get.
func (c *Client) getAttachedObject(ctx context.Context, fileID string) (File, error) {
	endpoint := c.baseURL + "disk.attachedObject.get"
	form := url.Values{}
	form.Set("id", fileID)
	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return File{}, err
	}
	var parsed attachedObjectResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return File{}, fmt.Errorf("bitrix: decode disk.attachedObject.get: %w", err)
	}
	if parsed.Error != "" {
		return File{}, fmt.Errorf("bitrix: disk.attachedObject.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	r := parsed.Result
	size, _ := strconv.ParseInt(firstNonEmpty(r.Size, r.SizeLower), 10, 64)
	return File{
		ID:   firstNonEmpty(r.ID, r.IDLower, jsonStr(fileID)),
		Name: firstNonEmpty(r.Name, r.NameLower),
		URL:  c.absoluteURL(firstNonEmpty(r.DownloadURL, r.DownloadL)),
		Size: size,
	}, nil
}

// ResolveAttachedObject resolves one Bitrix disk attached-object id — the N in a
// task description's "[DISK FILE ID=N]" — to a File (name + absolute download
// URL). Public wrapper around getAttachedObject, used to embed inline images.
func (c *Client) ResolveAttachedObject(ctx context.Context, id string) (File, error) {
	return c.getAttachedObject(ctx, id)
}

// parseFileIDs normalizes the UF_TASK_WEBDAV_FILES value, which Bitrix encodes
// as an array of ids that may be strings or numbers (or, on some portals, a
// map keyed by index). Anything it can't interpret yields no ids.
func parseFileIDs(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return nil
	}
	var out []string
	add := func(s jsonStr) {
		if v := strings.TrimSpace(s.String()); v != "" && v != "0" {
			out = append(out, v)
		}
	}
	if raw[0] == '[' {
		var items []jsonStr
		if err := json.Unmarshal(raw, &items); err == nil {
			for _, it := range items {
				add(it)
			}
		}
		return out
	}
	if raw[0] == '{' {
		var m map[string]jsonStr
		if err := json.Unmarshal(raw, &m); err == nil {
			for _, v := range m {
				add(v)
			}
		}
		return out
	}
	return out
}

// absoluteURL makes a Bitrix DOWNLOAD_URL absolute. Bitrix often returns a
// host-relative path (e.g. "/rest/...&token=..."); we resolve it against the
// scheme+host of the REST base URL so DownloadFile (and any HTTP client) can
// fetch it. An already-absolute http(s) URL is returned unchanged.
func (c *Client) absoluteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Host == "" {
		return raw
	}
	rel, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(rel).String()
}

// maxDownloadBytes caps a single attachment download so a hostile/huge file
// can't exhaust memory in the sync goroutine. 80 MiB matches the legacy bot.
const maxDownloadBytes = 80 << 20

// DownloadFile fetches the bytes at a Bitrix file URL. The portal credential is
// already embedded in the REST base URL (and therefore in DOWNLOAD_URLs derived
// from it), so no separate auth is attached. Redirects are followed. The body
// is capped at maxDownloadBytes; a larger file returns an error rather than a
// truncated download. The returned content type is the server's reported one
// (may be empty).
func (c *Client) DownloadFile(ctx context.Context, fileURL string) (data []byte, contentType string, err error) {
	if strings.TrimSpace(fileURL) == "" {
		return nil, "", errors.New("bitrix: empty file url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("bitrix: build download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("bitrix: download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("bitrix: download http %d", resp.StatusCode)
	}
	// Read one extra byte so we can distinguish "exactly at the cap" from
	// "exceeds the cap" and reject the latter.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("bitrix: read download body: %w", err)
	}
	if int64(len(body)) > maxDownloadBytes {
		return nil, "", fmt.Errorf("bitrix: file exceeds %d byte cap", maxDownloadBytes)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// GetGroup resolves a single workgroup by id to its name via sonet_group.get
// with FILTER[ID]. Returns ("", nil) when the group is not found (so the caller
// can fall back to a placeholder name) and a non-nil error only on transport /
// Bitrix-error failures. Used to label a Bitrix GROUP_ID when ListGroups hasn't
// GetTaskStages resolves a scrum/kanban entity's stages (its columns) to an
// id→title map via task.stages.get. entityID is the Bitrix workgroup id (the
// kanban lives on the group). Bitrix returns result as an object keyed by stage
// id: {"<id>":{"ID":..,"TITLE":"Code Review",..}}. Returns an empty map (no
// error) when the entity has no kanban, so the caller degrades to STATUS-only.
func (c *Client) GetTaskStages(ctx context.Context, entityID string) (map[string]string, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(entityID) == "" {
		return map[string]string{}, nil
	}
	endpoint := c.baseURL + "task.stages.get"
	form := url.Values{}
	form.Set("entityId", strings.TrimSpace(entityID))

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result    map[string]rawStage `json:"result"`
		Error     string              `json:"error"`
		ErrorDesc string              `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode task.stages.get: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: task.stages.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	out := make(map[string]string, len(parsed.Result))
	for key, st := range parsed.Result {
		id := firstNonEmpty(st.ID)
		if id == "" {
			id = key // the map key is the stage id when the body omits it
		}
		if title := firstNonEmpty(st.Title); id != "" && title != "" {
			out[id] = title
		}
	}
	return out, nil
}

// rawStage is one task.stages.get entry.
type rawStage struct {
	ID    jsonStr `json:"ID"`
	Title jsonStr `json:"TITLE"`
}

// been cached.
func (c *Client) GetGroup(ctx context.Context, groupID string) (Group, error) {
	if c.baseURL == "" {
		return Group{}, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(groupID) == "" {
		return Group{}, errors.New("bitrix: empty group id")
	}
	endpoint := c.baseURL + "sonet_group.get"
	form := url.Values{}
	form.Set("FILTER[ID]", strings.TrimSpace(groupID))

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return Group{}, err
	}
	var parsed listGroupsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Group{}, fmt.Errorf("bitrix: decode sonet_group.get (by id): %w", err)
	}
	if parsed.Error != "" {
		return Group{}, fmt.Errorf("bitrix: sonet_group.get (by id) error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	var rows []rawGroup
	if !bitrixEmptyResult(parsed.Result) {
		if err := json.Unmarshal(parsed.Result, &rows); err != nil {
			return Group{}, fmt.Errorf("bitrix: decode sonet_group.get (by id) result: %w", err)
		}
	}
	for _, rg := range rows {
		id := firstNonEmpty(rg.ID, rg.IDLower)
		if id == "" {
			continue
		}
		return Group{ID: id, Name: firstNonEmpty(rg.Name, rg.NameLower)}, nil
	}
	return Group{}, nil // not found — caller falls back to a placeholder
}

// User is a minimal Bitrix portal user — the person behind a task's
// RESPONSIBLE_ID. Only the fields Agora needs to display or map an assignee.
type User struct {
	ID       string
	Name     string // given name
	LastName string
	Email    string
	Position string
	// Department is the user's UF_DEPARTMENT — the numeric ids (as strings) of
	// the Bitrix departments (Отдел) the user belongs to. Used to gate which
	// responsibles get provisioned as workspace members (team = a specific
	// department only). Empty when the portal doesn't expose it.
	Department []string
}

// deptIDs decodes Bitrix's UF_DEPARTMENT. It is normally an array of numeric
// department ids (e.g. [152]) but portals have been seen to return "", null, or
// a bare scalar when a user has no/one department — all of which decode to a
// clean []string of non-zero ids.
type deptIDs []string

func (d *deptIDs) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		*d = nil
		return nil
	}
	switch string(b) {
	case "null", `""`, "false", "0":
		*d = nil
		return nil
	}
	appendID := func(out []string, s string) []string {
		s = strings.TrimSpace(s)
		if s != "" && s != "0" {
			out = append(out, s)
		}
		return out
	}
	if b[0] == '[' {
		var raw []json.Number
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		out := make([]string, 0, len(raw))
		for _, n := range raw {
			out = appendID(out, n.String())
		}
		*d = out
		return nil
	}
	var one jsonStr
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*d = appendID(nil, string(one))
	return nil
}

// FullName joins the given and family name, trimmed. Falls back to the email,
// then the id, so the result is never empty when the user exists.
func (u User) FullName() string {
	if n := strings.TrimSpace(u.Name + " " + u.LastName); n != "" {
		return n
	}
	if e := strings.TrimSpace(u.Email); e != "" {
		return e
	}
	return strings.TrimSpace(u.ID)
}

// GetUser fetches a portal user by id via user.get. Returns a zero User (no
// error) when the id is unknown so a missing/renamed responsible never fails a
// sync. Requires the token's "user" scope (present on the SD webhook).
func (c *Client) GetUser(ctx context.Context, userID string) (User, error) {
	if c.baseURL == "" {
		return User{}, errors.New("bitrix: empty base URL")
	}
	id := strings.TrimSpace(userID)
	if id == "" {
		return User{}, errors.New("bitrix: empty user id")
	}
	endpoint := c.baseURL + "user.get"
	form := url.Values{}
	form.Set("ID", id)

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return User{}, err
	}
	var parsed struct {
		Result []struct {
			ID           jsonStr `json:"ID"`
			Name         jsonStr `json:"NAME"`
			LastName     jsonStr `json:"LAST_NAME"`
			Email        jsonStr `json:"EMAIL"`
			WorkPosition jsonStr `json:"WORK_POSITION"`
			Department   deptIDs `json:"UF_DEPARTMENT"`
		} `json:"result"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return User{}, fmt.Errorf("bitrix: decode user.get: %w", err)
	}
	if parsed.Error != "" {
		return User{}, fmt.Errorf("bitrix: user.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	if len(parsed.Result) == 0 {
		return User{}, nil // unknown id — caller treats as "no info"
	}
	r := parsed.Result[0]
	return User{
		ID:         firstNonEmpty(r.ID, jsonStr(id)),
		Name:       firstNonEmpty(r.Name),
		LastName:   firstNonEmpty(r.LastName),
		Email:      firstNonEmpty(r.Email),
		Position:   firstNonEmpty(r.WorkPosition),
		Department: r.Department,
	}, nil
}

// Department is one Bitrix department (Отдел) from department.get: its numeric
// id, display name, and parent id ("0"/"" for the company root). Parent lets a
// caller expand a named department to its whole subtree.
type Department struct {
	ID     string
	Name   string
	Parent string
}

// ListDepartments returns ALL departments in the portal via department.get,
// following the response's `next` offset across every page. Used to resolve a
// team department by name (e.g. "SD Разработка") to its id — and, with Parent,
// to that department's descendants — so member provisioning can be gated to a
// single org unit.
func (c *Client) ListDepartments(ctx context.Context) ([]Department, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	endpoint := c.baseURL + "department.get"

	depts := make([]Department, 0, bitrixTaskPageSize)
	start := 0
	for {
		form := url.Values{}
		if start > 0 {
			form.Set("start", strconv.Itoa(start))
		}
		body, err := c.post(ctx, endpoint, form)
		if err != nil {
			return nil, err
		}
		type deptRow struct {
			ID     jsonStr `json:"ID"`
			Name   jsonStr `json:"NAME"`
			Parent jsonStr `json:"PARENT"`
		}
		var parsed struct {
			Result    json.RawMessage `json:"result"`
			Next      *int            `json:"next"`
			Error     string          `json:"error"`
			ErrorDesc string          `json:"error_description"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("bitrix: decode department.get: %w", err)
		}
		if parsed.Error != "" {
			return nil, fmt.Errorf("bitrix: department.get error %s: %s", parsed.Error, parsed.ErrorDesc)
		}
		var rows []deptRow
		if !bitrixEmptyResult(parsed.Result) {
			if err := json.Unmarshal(parsed.Result, &rows); err != nil {
				return nil, fmt.Errorf("bitrix: decode department.get result: %w", err)
			}
		}
		for _, r := range rows {
			depts = append(depts, Department{
				ID:     strings.TrimSpace(string(r.ID)),
				Name:   strings.TrimSpace(string(r.Name)),
				Parent: strings.TrimSpace(string(r.Parent)),
			})
		}
		if parsed.Next == nil || len(rows) == 0 || len(depts) >= bitrixMaxTasksPerImport || *parsed.Next <= start {
			break
		}
		start = *parsed.Next
	}
	return depts, nil
}

// ResolveDepartmentSubtree returns the set of department ids whose name matches
// any of `names` (case-insensitive, whitespace-trimmed) PLUS every descendant
// department, so a team named "SD Разработка" also captures its sub-teams. The
// returned set is empty when no name matches (caller decides whether that means
// "no filter" or "empty team").
func ResolveDepartmentSubtree(depts []Department, names []string) map[string]bool {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		if t := strings.ToLower(strings.TrimSpace(n)); t != "" {
			want[t] = true
		}
	}
	if len(want) == 0 {
		return map[string]bool{}
	}
	// Seed with the departments whose name matches.
	matched := map[string]bool{}
	for _, d := range depts {
		if d.ID != "" && want[strings.ToLower(d.Name)] {
			matched[d.ID] = true
		}
	}
	// Expand to descendants: repeatedly add any dept whose parent is already
	// matched, until the set stops growing (org trees are shallow, so a bounded
	// pass over the slice per iteration is fine).
	for {
		grew := false
		for _, d := range depts {
			if d.ID == "" || matched[d.ID] {
				continue
			}
			if d.Parent != "" && matched[d.Parent] {
				matched[d.ID] = true
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	return matched
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

// BindEvent registers an outbound event handler on the portal via event.bind,
// so Bitrix calls handlerURL whenever `event` fires (e.g. ONTASKUPDATE). Bitrix
// dedups by (event, handler) so re-binding the same pair is a harmless no-op.
func (c *Client) BindEvent(ctx context.Context, event, handlerURL string) error {
	if c.baseURL == "" {
		return errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(event) == "" || strings.TrimSpace(handlerURL) == "" {
		return errors.New("bitrix: empty event or handler url")
	}
	endpoint := c.baseURL + "event.bind"
	form := url.Values{}
	form.Set("event", strings.TrimSpace(event))
	form.Set("handler", strings.TrimSpace(handlerURL))
	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return err
	}
	return checkError(body)
}

// ListBoundEvents returns the (event → handler URL) bindings currently
// registered on the portal via event.get, so the registration endpoint can
// report what is already wired.
func (c *Client) ListBoundEvents(ctx context.Context) ([]EventBinding, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	endpoint := c.baseURL + "event.get"
	body, err := c.post(ctx, endpoint, url.Values{})
	if err != nil {
		return nil, err
	}
	type eventRow struct {
		Event   jsonStr `json:"event"`
		Handler jsonStr `json:"handler"`
	}
	var parsed struct {
		Result    json.RawMessage `json:"result"`
		Error     string          `json:"error"`
		ErrorDesc string          `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode event.get: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: event.get error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	var rows []eventRow
	if !bitrixEmptyResult(parsed.Result) {
		if err := json.Unmarshal(parsed.Result, &rows); err != nil {
			return nil, fmt.Errorf("bitrix: decode event.get result: %w", err)
		}
	}
	out := make([]EventBinding, 0, len(rows))
	for _, b := range rows {
		out = append(out, EventBinding{Event: string(b.Event), Handler: string(b.Handler)})
	}
	return out, nil
}

// EventBinding is one portal event→handler registration.
type EventBinding struct {
	Event   string `json:"event"`
	Handler string `json:"handler"`
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
	case "ONTASKADD", "ONTASKUPDATE",
		// Comment events keep the imported issue's discussion LIVE: a comment
		// added/edited in Bitrix re-syncs the task, mirroring the new comment.
		"ONTASKCOMMENTADD", "ONTASKCOMMENTUPDATE":
	default:
		return event, "", false
	}

	for _, key := range []string{
		"data[FIELDS_AFTER][ID]",
		"data[FIELDS_BEFORE][ID]",
		"data[fields_after][id]",
		"DATA[FIELDS_AFTER][ID]",
		// Comment events carry the parent task id under TASK_ID, not ID.
		"data[FIELDS_AFTER][TASK_ID]",
		"data[fields_after][task_id]",
		"DATA[FIELDS_AFTER][TASK_ID]",
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

// FieldDef is one entry from tasks.task.getFields.
type FieldDef struct {
	Type  string
	Title string
}

// GetTaskFields returns the portal's task-field catalogue, standard and custom
// alike. Custom field names are opaque (UF_AUTO_809721135658) and their meaning
// lives only in the title, so a caller has no way to use them without this.
func (c *Client) GetTaskFields(ctx context.Context) (map[string]FieldDef, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	body, err := c.post(ctx, c.baseURL+"tasks.task.getFields", url.Values{})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result struct {
			Fields map[string]struct {
				// Bitrix sends null for a field with no label, and the two
				// casings appear on different portals.
				Type       *string `json:"type"`
				Title      *string `json:"title"`
				TitleUpper *string `json:"TITLE"`
			} `json:"fields"`
		} `json:"result"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode tasks.task.getFields: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: tasks.task.getFields error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	out := make(map[string]FieldDef, len(parsed.Result.Fields))
	deref := func(vals ...*string) string {
		for _, v := range vals {
			if v != nil && strings.TrimSpace(*v) != "" {
				return strings.TrimSpace(*v)
			}
		}
		return ""
	}
	for name, def := range parsed.Result.Fields {
		out[name] = FieldDef{Type: deref(def.Type), Title: deref(def.Title, def.TitleUpper)}
	}
	return out, nil
}
