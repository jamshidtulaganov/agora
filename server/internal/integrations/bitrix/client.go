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
	GroupID       string
	// StageID is the scrum/kanban STAGE_ID (the live kanban column), resolved to
	// a human stage name via task.stages.get. Empty for tasks not on a kanban.
	StageID string
	// GroupName is the Bitrix workgroup name when the payload carried a nested
	// group object (some tasks.task.get responses include {group:{id,name}}).
	// Often empty — the handler resolves the name from GROUP_ID via a cached
	// ListGroups / GetGroup lookup when this is blank.
	GroupName string
	Tags      []string
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
	// Scrum/kanban STAGE_ID — the live kanban column the dev team moves the task
	// through (Новые / Code Review / Testing / Сделаны …), distinct from STATUS
	// (the coarse Bitrix task state). Resolved to a name via task.stages.get.
	StageID    jsonStr `json:"stageId"`
	StageUpper jsonStr `json:"STAGE_ID"`
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
		ResponsibleID: firstNonEmpty(rt.Responsible, rt.RespUpper),
		GroupID:       groupID,
		GroupName:     firstNonEmpty(rt.Group.Name, rt.Group.NameUpper, rt.GroupUp.Name, rt.GroupUp.NameUpper),
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
	for _, f := range []string{"ID", "TITLE", "DESCRIPTION", "STATUS", "STAGE_ID", "RESPONSIBLE_ID", "GROUP_ID", "TAGS"} {
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

// ListUsers returns the portal's active users so the importer can offer "import
// by responsible". Bitrix user.get pages at 50; this fetches the first page
// (ordered by surname), which covers a typical dev team — extend with `start`
// paging if a portal needs more.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	endpoint := c.baseURL + "user.get"
	form := url.Values{}
	form.Set("FILTER[ACTIVE]", "true")
	form.Set("sort", "LAST_NAME")
	form.Set("order", "asc")

	body, err := c.post(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result []struct {
			ID           jsonStr `json:"ID"`
			Name         jsonStr `json:"NAME"`
			LastName     jsonStr `json:"LAST_NAME"`
			Email        jsonStr `json:"EMAIL"`
			WorkPosition jsonStr `json:"WORK_POSITION"`
		} `json:"result"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bitrix: decode user.get (list): %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: user.get (list) error %s: %s", parsed.Error, parsed.ErrorDesc)
	}
	users := make([]User, 0, len(parsed.Result))
	for _, r := range parsed.Result {
		id := firstNonEmpty(r.ID)
		if id == "" {
			continue
		}
		users = append(users, User{
			ID:       id,
			Name:     firstNonEmpty(r.Name),
			LastName: firstNonEmpty(r.LastName),
			Email:    firstNonEmpty(r.Email),
			Position: firstNonEmpty(r.WorkPosition),
		})
	}
	return users, nil
}

// ListTasksByUser returns the tasks a given user is RESPONSIBLE for, for the
// "import by responsible" flow. Mirrors ListTasks but filters on RESPONSIBLE_ID
// instead of GROUP_ID. Capped at maxTasksPerGroup.
func (c *Client) ListTasksByUser(ctx context.Context, userID, tag string) ([]Task, error) {
	if c.baseURL == "" {
		return nil, errors.New("bitrix: empty base URL")
	}
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("bitrix: empty user id")
	}
	endpoint := c.baseURL + "tasks.task.list"
	form := url.Values{}
	form.Set("filter[RESPONSIBLE_ID]", strings.TrimSpace(userID))
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
		return nil, fmt.Errorf("bitrix: decode tasks.task.list (by user): %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("bitrix: tasks.task.list (by user) error %s: %s", parsed.Error, parsed.ErrorDesc)
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

// Comment is the subset of a Bitrix task comment Agora mirrors into an issue
// comment. Author/Date are normalized strings; Text is the raw POST_MESSAGE
// (BB-code, left untouched — the handler renders it verbatim).
type Comment struct {
	ID     string
	Author string
	Date   string
	Text   string
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
// sonet_group.get the result is a bare array, not wrapped in an object.
type listCommentsResponse struct {
	Result    []rawComment `json:"result"`
	Error     string       `json:"error"`
	ErrorDesc string       `json:"error_description"`
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

	comments := make([]Comment, 0, len(parsed.Result))
	for _, rc := range parsed.Result {
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
	for _, rg := range parsed.Result {
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
		ID:       firstNonEmpty(r.ID, jsonStr(id)),
		Name:     firstNonEmpty(r.Name),
		LastName: firstNonEmpty(r.LastName),
		Email:    firstNonEmpty(r.Email),
		Position: firstNonEmpty(r.WorkPosition),
	}, nil
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
