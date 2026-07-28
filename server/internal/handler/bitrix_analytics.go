package handler

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
)

// Bitrix task analytics — a portal-wide, time-windowed rollup of tasks.task.list.
//
// Deliberately NOT computed from the imported Agora issues: only a subset of
// Bitrix tasks is ever imported (the importer narrows by tag/group), and an
// issue's created_at is its IMPORT time, not the task's creation time. Reading
// the portal directly is the only way to answer "what happened this year".
//
// The webhook URL never leaves the server: the aggregation runs here and the
// response carries counts only, so a client (or an autopilot agent) can consume
// analytics without holding a credential that grants full REST access.

// bitrixStatus maps Bitrix's numeric STATUS to a coarse bucket. Bitrix statuses:
// 1 new, 2 pending, 3 in progress, 4 supposedly completed, 5 completed,
// 6 deferred, 7 declined. Anything unrecognised counts as open rather than
// silently vanishing from the totals.
func bitrixStatusBucket(status string) string {
	switch strings.TrimSpace(status) {
	case "5":
		return "completed"
	case "4":
		return "awaiting_control"
	case "6":
		return "deferred"
	case "7":
		return "declined"
	default:
		return "open"
	}
}

// maxStageLookupGroups bounds the per-group stage-name lookups a single
// analytics call may make. Names are a readability win, not a correctness one,
// so a wide window degrades to raw ids rather than to a slow request.
const maxStageLookupGroups = 25

// bitrixPriorityLabels names Bitrix's numeric priorities, so a report does not
// have to explain what "2" means to its reader.
var bitrixPriorityLabels = map[string]string{"0": "past", "1": "o'rta", "2": "yuqori"}

type bitrixAnalyticsBucket struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

type bitrixAnalyticsResponse struct {
	Since     string `json:"since"`
	Until     string `json:"until"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	// Open is everything NOT completed — it therefore INCLUDES awaiting_control
	// and deferred. Use by_status for the finer split; `open` + `completed`
	// always equals `total`, which by_status alone does not guarantee.
	Open       int                     `json:"open"`
	ByStatus   []bitrixAnalyticsBucket `json:"by_status"`
	ByMonth    []bitrixAnalyticsBucket `json:"by_month"`
	ByGroup    []bitrixAnalyticsBucket `json:"by_group"`
	ByAssignee []bitrixAnalyticsBucket `json:"by_assignee"`
	// ByTag counts each tag SEPARATELY, so a task carrying two tags is counted
	// under both. The sum therefore exceeds `total` and is not a partition —
	// reporting it as one would double-count the work.
	ByTag []bitrixAnalyticsBucket `json:"by_tag"`
	// ByCreator answers "where is the work coming from" — the question
	// by_assignee cannot, since it only shows where work landed. A sprint plan
	// that does not match this column is not the plan being followed.
	ByCreator []bitrixAnalyticsBucket `json:"by_creator"`
	// ByPriority uses Bitrix's numeric urgency: 0 low, 1 normal, 2 high.
	ByPriority []bitrixAnalyticsBucket `json:"by_priority"`
	// ByStage is the live kanban column, distinct from by_status: a task can be
	// "open" for weeks while sitting in Code Review, and only this shows where.
	ByStage []bitrixAnalyticsBucket `json:"by_stage"`
	// Overdue counts OPEN tasks past their deadline. A closed task that missed
	// its deadline is history, not something to act on — counting it would
	// make the number grow forever and stop meaning anything.
	Overdue int `json:"overdue"`
	// NoDeadline is the count carrying no deadline at all. Without it a low
	// overdue number reads as health when it may just mean nothing is dated.
	NoDeadline int `json:"no_deadline"`
	// BySprint groups by the team's own sprint, which GROUP_ID cannot express:
	// one project runs many sprints.
	BySprint []bitrixAnalyticsBucket `json:"by_sprint"`
	// Untagged is the count with no tag at all, which by_tag cannot express and
	// is usually the largest single group in a portal that tags loosely.
	Untagged    int                     `json:"untagged"`
	ClosedByMon []bitrixAnalyticsBucket `json:"closed_by_month"`
	// MedianDaysToClose is over tasks that carry BOTH timestamps. Reported as
	// -1 when no task in the window closed, so "no data" is distinguishable
	// from "closed the same day".
	MedianDaysToClose float64 `json:"median_days_to_close"`
	// Truncated warns that the portal returned as many tasks as the client's
	// safety cap allows, so older tasks in the window are missing.
	Truncated bool `json:"truncated"`
	// Filters echoes what was actually measured. Without it a filtered rollup
	// is indistinguishable from a portal-wide one, and a report that says
	// "2417 tasks" when it measured only the BUG-tagged ones is worse than no
	// report at all.
	Filters bitrixAnalyticsFilters `json:"filters"`
}

// bitrixAnalyticsFilters narrows the whole rollup, not just the totals: every
// series below (by_month, by_assignee, median, …) is computed over the filtered
// set. That is what makes "BUG tasks per month, per person" answerable in one
// request instead of forcing the caller to bucket by hand.
type bitrixAnalyticsFilters struct {
	Tag      string `json:"tag,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Creator  string `json:"creator,omitempty"`
	Group    string `json:"group,omitempty"`
	Status   string `json:"status,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Priority string `json:"priority,omitempty"`
	Title    string `json:"title,omitempty"`
	Closed   string `json:"closed,omitempty"`
	Sprint   string `json:"sprint,omitempty"`
	Flow     string `json:"flow,omitempty"`
	Parent   string `json:"parent,omitempty"`
	// Involves matches a user in ANY role on the task — responsible,
	// accomplice or auditor. "How loaded is this person" is a different
	// question from "what were they assigned", and only this answers it.
	Involves string `json:"involves,omitempty"`
	// Overdue is true/false against DEADLINE. An overdue count is the first
	// number a manager looks for and the portal's own saved filters lead with
	// it ("Просрочены").
	Overdue      string `json:"overdue,omitempty"`
	DeadlineFrom string `json:"deadline_from,omitempty"`
	DeadlineTo   string `json:"deadline_to,omitempty"`
	ClosedFrom   string `json:"closed_from,omitempty"`
	ClosedTo     string `json:"closed_to,omitempty"`
	// deadlineFrom/To and closedFrom/To parsed once, so the per-task loop does
	// not re-parse a date for every row in the window.
	deadlineFrom, deadlineTo time.Time
	closedFrom, closedTo     time.Time
	// TagMatched echoes the spellings the tag query expanded to, so a report
	// can state what it actually counted rather than implying it matched one
	// literal label.
	TagMatched []string `json:"tag_matched,omitempty"`
	// tagSpellings is TagMatched as a set, for the per-task check.
	tagSpellings map[string]bool
	// stageIDs holds the ids a named stage resolved to. Empty means Stage was
	// given as an id, or matched no stage at all.
	stageIDs map[string]bool
}

// sortedTagSet renders the expanded tag set for the response, in stable order
// so two identical requests do not produce two different-looking echoes.
func sortedTagSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stageIDsNamed returns every stage id whose title matches, case-insensitively.
// A name can map to several ids because stages are defined per group — "Code
// Review" exists separately in each project that uses it.
func stageIDsNamed(names map[string]string, want string) map[string]bool {
	out := map[string]bool{}
	for id, title := range names {
		if strings.EqualFold(strings.TrimSpace(title), want) {
			out[id] = true
		}
	}
	return out
}

// bitrixFilterParams is the closed set of accepted filter keys, alongside the
// date bounds. Anything outside it is a 400.
//
// Rejecting unknown keys is the important half. A silently ignored `?priorty=2`
// returns the portal-wide rollup, which the caller then reports as "high
// priority tasks" — a wrong number stated confidently, and the response's own
// `filters` echo would agree with it. Failing the request is the only outcome
// that cannot be misread.
var bitrixFilterParams = map[string]bool{
	"since": true, "until": true,
	"tag": true, "assignee": true, "creator": true, "group": true,
	"status": true, "stage": true, "priority": true, "title": true, "closed": true,
	"sprint": true, "flow": true, "parent": true, "involves": true,
	"overdue": true, "deadline_from": true, "deadline_to": true,
	"closed_from": true, "closed_to": true,
}

// matches reports whether a task survives every active filter. Empty fields do
// not constrain, so filters compose: tag=BUG&creator=525&closed=false is one
// question, not three requests.
func (f bitrixAnalyticsFilters) matches(t *bitrix.Task) bool {
	if f.Tag != "" && !matchesTag(t, f.tagSpellings) {
		return false
	}
	if f.Assignee != "" && strings.TrimSpace(t.ResponsibleID) != f.Assignee {
		return false
	}
	if f.Creator != "" && strings.TrimSpace(t.CreatedByID) != f.Creator {
		return false
	}
	if f.Group != "" && strings.TrimSpace(t.GroupID) != f.Group {
		return false
	}
	if f.Stage != "" {
		stage := strings.TrimSpace(t.StageID)
		// Named stages resolve to ids; an unresolvable name falls back to a
		// direct comparison so an id still works either way.
		if len(f.stageIDs) > 0 {
			if !f.stageIDs[stage] {
				return false
			}
		} else if !strings.EqualFold(stage, f.Stage) {
			return false
		}
	}
	if f.Priority != "" && strings.TrimSpace(t.Priority) != f.Priority {
		return false
	}
	if f.Status != "" {
		// Accepts either the coarse bucket ("open", "completed") or Bitrix's
		// raw numeric STATUS. A caller reading by_status sees bucket names, so
		// requiring the number there would mean translating by hand.
		if !strings.EqualFold(bitrixStatusBucket(t.Status), f.Status) &&
			strings.TrimSpace(t.Status) != f.Status {
			return false
		}
	}
	if f.Closed != "" {
		_, hasClosed := bitrix.ParseTime(t.ClosedAt)
		if (f.Closed == "true") != hasClosed {
			return false
		}
	}
	if f.Title != "" && !strings.Contains(
		strings.ToLower(t.Title), strings.ToLower(f.Title)) {
		return false
	}
	if f.Sprint != "" && strings.TrimSpace(t.SprintID) != f.Sprint {
		return false
	}
	if f.Flow != "" && strings.TrimSpace(t.FlowID) != f.Flow {
		return false
	}
	if f.Parent != "" && strings.TrimSpace(t.ParentID) != f.Parent {
		return false
	}
	if f.Involves != "" && !taskInvolves(t, f.Involves) {
		return false
	}
	if f.Overdue != "" && (f.Overdue == "true") != taskIsOverdue(t) {
		return false
	}
	if !f.deadlineFrom.IsZero() || !f.deadlineTo.IsZero() {
		due, ok := bitrix.ParseTime(t.Deadline)
		// A task with no deadline cannot satisfy a deadline window. Treating
		// it as a match would put every undated task into a "due this week"
		// answer.
		if !ok {
			return false
		}
		if !f.deadlineFrom.IsZero() && due.Before(f.deadlineFrom) {
			return false
		}
		if !f.deadlineTo.IsZero() && due.After(f.deadlineTo) {
			return false
		}
	}
	if !f.closedFrom.IsZero() || !f.closedTo.IsZero() {
		closed, ok := bitrix.ParseTime(t.ClosedAt)
		if !ok {
			return false
		}
		if !f.closedFrom.IsZero() && closed.Before(f.closedFrom) {
			return false
		}
		if !f.closedTo.IsZero() && closed.After(f.closedTo) {
			return false
		}
	}
	return true
}

// taskInvolves reports whether a user appears on the task in any role.
func taskInvolves(t *bitrix.Task, userID string) bool {
	if strings.TrimSpace(t.ResponsibleID) == userID {
		return true
	}
	for _, list := range [][]string{t.Accomplices, t.Auditors} {
		for _, id := range list {
			if strings.TrimSpace(id) == userID {
				return true
			}
		}
	}
	return false
}

// taskIsOverdue reports whether the deadline has passed and the task is still
// open. A closed task that missed its deadline is history, not a problem to
// act on — counting it would make the overdue number grow forever.
func taskIsOverdue(t *bitrix.Task) bool {
	due, ok := bitrix.ParseTime(t.Deadline)
	if !ok {
		return false
	}
	if _, closed := bitrix.ParseTime(t.ClosedAt); closed {
		return false
	}
	return due.Before(time.Now())
}

// active reports whether any filter constrains the set.
func (f bitrixAnalyticsFilters) active() bool {
	return f.Tag != "" || f.Assignee != "" || f.Creator != "" || f.Group != "" ||
		f.Status != "" || f.Stage != "" || f.Priority != "" || f.Title != "" ||
		f.Closed != "" || f.Sprint != "" || f.Flow != "" || f.Parent != "" ||
		f.Involves != "" || f.Overdue != "" ||
		!f.deadlineFrom.IsZero() || !f.deadlineTo.IsZero() ||
		!f.closedFrom.IsZero() || !f.closedTo.IsZero()
}

// matchesTag reports whether a task carries any spelling of the wanted tag.
// Bitrix tags are free text typed by humans in more than one language, so
// "BUG", "bug", "баг" and "#BugReport" are one tag in everything except
// storage — see bitrix_tag_alias.go.
func matchesTag(task *bitrix.Task, wanted map[string]bool) bool {
	for _, t := range task.Tags {
		if wanted[normalizeTag(t)] {
			return true
		}
	}
	return false
}

func sortedBuckets(counts map[string]int, labels map[string]string, byKey bool) []bitrixAnalyticsBucket {
	out := make([]bitrixAnalyticsBucket, 0, len(counts))
	for k, n := range counts {
		out = append(out, bitrixAnalyticsBucket{Key: k, Label: labels[k], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if byKey {
			return out[i].Key < out[j].Key // chronological for month series
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// GetBitrixAnalytics handles GET /api/bitrix/analytics.
//
// Windows: since, until (inclusive, YYYY-MM-DD) over CREATED_DATE.
// Filters: tag, assignee, creator, involves, group, sprint, flow, parent,
// status, stage, priority, title, closed, overdue, deadline_from/to,
// closed_from/to. They compose, and each narrows the ENTIRE rollup rather than
// just the total — so "BUG-tagged tasks opened by 525, per month" is one
// request.
//
// stage takes a NAME as well as an id ("Code Review"), because the stage is
// the field this team actually works in and nobody knows its numeric id.
//
// The response echoes the filters back, and an unrecognised parameter is a 400
// rather than a no-op: a filtered rollup that reads as portal-wide is worse
// than no rollup at all.
//
// `since` defaults to the start of the current year in the server's local zone,
// which is what "this year so far" means to the humans reading it. `until` is
// optional and inclusive; omitting it means "up to now".
func (h *Handler) GetBitrixAnalytics(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}

	now := time.Now()
	since := time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, now.Location())
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, now.Location())
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be YYYY-MM-DD")
			return
		}
		since = parsed
	}

	// `until` makes a PAST window reconstructible, which is what any
	// week-over-week comparison needs: without it a weekly report can state
	// the current level but never the change. Bounded to end-of-day so
	// `until=2026-07-20` includes the 20th.
	var until time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("until")); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, now.Location())
		if err != nil {
			writeError(w, http.StatusBadRequest, "until must be YYYY-MM-DD")
			return
		}
		until = parsed.Add(24*time.Hour - time.Second)
		if until.Before(since) {
			writeError(w, http.StatusBadRequest, "until must not be before since")
			return
		}
	}

	query := r.URL.Query()
	for key := range query {
		if !bitrixFilterParams[key] {
			writeError(w, http.StatusBadRequest, "unknown filter: "+key)
			return
		}
	}
	filters := bitrixAnalyticsFilters{
		Tag:      strings.TrimSpace(query.Get("tag")),
		Assignee: strings.TrimSpace(query.Get("assignee")),
		Creator:  strings.TrimSpace(query.Get("creator")),
		Group:    strings.TrimSpace(query.Get("group")),
		Status:   strings.TrimSpace(query.Get("status")),
		Stage:    strings.TrimSpace(query.Get("stage")),
		Priority: strings.TrimSpace(query.Get("priority")),
		Title:    strings.TrimSpace(query.Get("title")),
		Closed:   strings.ToLower(strings.TrimSpace(query.Get("closed"))),
	}
	filters.Sprint = strings.TrimSpace(query.Get("sprint"))
	filters.Flow = strings.TrimSpace(query.Get("flow"))
	filters.Parent = strings.TrimSpace(query.Get("parent"))
	filters.Involves = strings.TrimSpace(query.Get("involves"))
	filters.Overdue = strings.ToLower(strings.TrimSpace(query.Get("overdue")))
	filters.DeadlineFrom = strings.TrimSpace(query.Get("deadline_from"))
	filters.DeadlineTo = strings.TrimSpace(query.Get("deadline_to"))
	filters.ClosedFrom = strings.TrimSpace(query.Get("closed_from"))
	filters.ClosedTo = strings.TrimSpace(query.Get("closed_to"))

	for _, b := range []struct {
		name  string
		value string
	}{{"closed", filters.Closed}, {"overdue", filters.Overdue}} {
		if b.value != "" && b.value != "true" && b.value != "false" {
			writeError(w, http.StatusBadRequest, b.name+" must be true or false")
			return
		}
	}
	// Date bounds are parsed once, here, rather than per task: the window can
	// hold thousands of rows and re-parsing a constant for each is pure waste.
	// An upper bound covers the whole day, so deadline_to=2026-07-31 includes
	// the 31st — a reader means the date, not midnight on it.
	for _, b := range []struct {
		raw   string
		name  string
		dst   *time.Time
		endOf bool
	}{
		{filters.DeadlineFrom, "deadline_from", &filters.deadlineFrom, false},
		{filters.DeadlineTo, "deadline_to", &filters.deadlineTo, true},
		{filters.ClosedFrom, "closed_from", &filters.closedFrom, false},
		{filters.ClosedTo, "closed_to", &filters.closedTo, true},
	} {
		if b.raw == "" {
			continue
		}
		parsed, err := time.ParseInLocation("2006-01-02", b.raw, now.Location())
		if err != nil {
			writeError(w, http.StatusBadRequest, b.name+" must be YYYY-MM-DD")
			return
		}
		if b.endOf {
			parsed = parsed.Add(24*time.Hour - time.Second)
		}
		*b.dst = parsed
	}

	client := bitrix.NewClient(bitrixWebhookURL())
	tasks, err := client.ListTasksBetween(r.Context(), since, until)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list bitrix tasks: "+err.Error())
		return
	}

	// Resolve ids to names once. Both lookups are best-effort: analytics must
	// still return counts when a portal denies the directory scopes, so a
	// failure leaves labels empty rather than failing the request.
	groupNames := map[string]string{}
	if groups, gErr := client.ListGroups(r.Context()); gErr == nil {
		for _, g := range groups {
			groupNames[g.ID] = g.Name
		}
	}
	userNames := map[string]string{}
	if users, uErr := client.ListUsers(r.Context()); uErr == nil {
		for _, u := range users {
			name := strings.TrimSpace(u.Name + " " + u.LastName)
			if name == "" {
				name = u.Email
			}
			userNames[u.ID] = name
		}
	}

	// Stage names, per group. STAGE_ID is the field this team actually works
	// in — To Do / Code Review / Testing / Need merge / Done — while STATUS is
	// the coarse Bitrix state that reports a task sitting in Code Review as
	// "in progress". Without the names, by_stage is a column of opaque ids.
	//
	// Stages are defined per group, so this walks the groups present in the
	// window. Capped: a portal with many groups would otherwise turn one
	// analytics call into hundreds of REST round-trips.
	stageNames := map[string]string{}
	seenGroups := map[string]bool{}
	for i := range tasks {
		g := strings.TrimSpace(tasks[i].GroupID)
		if g == "" || g == "0" || seenGroups[g] {
			continue
		}
		seenGroups[g] = true
		if len(seenGroups) > maxStageLookupGroups {
			slog.Info("bitrix analytics: stage name lookup capped",
				"groups_in_window", len(seenGroups), "cap", maxStageLookupGroups)
			break
		}
		if stages, sErr := client.GetTaskStages(r.Context(), g); sErr == nil {
			for id, title := range stages {
				stageNames[id] = title
			}
		}
	}

	// Filtering happens here rather than in the Bitrix query: the window is
	// fetched whole anyway for the date bounds, and tag matching in particular
	// cannot be expressed reliably in a portal filter (tags are free text and
	// the REST filter is exact-match, case-sensitive).
	//
	// The truncation warning below therefore still refers to the WINDOW, not
	// the filtered subset — a truncated window makes a filtered count a floor,
	// which the caller must know before quoting it.
	// A stage given by name is translated to ids here, so the per-task check
	// stays a plain id comparison. Names are what a human types — nobody knows
	// that Code Review is stage 1247 — but they are per-group, so the same
	// name can map to several ids.
	if filters.Stage != "" {
		filters.stageIDs = stageIDsNamed(stageNames, filters.Stage)
	}
	if filters.Tag != "" {
		filters.tagSpellings = expandTagQuery(filters.Tag)
		filters.TagMatched = sortedTagSet(filters.tagSpellings)
	}

	truncated := len(tasks) >= bitrix.MaxTasksPerRequest
	if filters.active() {
		kept := tasks[:0]
		for i := range tasks {
			if filters.matches(&tasks[i]) {
				kept = append(kept, tasks[i])
			}
		}
		tasks = kept
	}

	resp := bitrixAnalyticsResponse{
		Since:             since.Format("2006-01-02"),
		Until:             untilLabel(until, now),
		Total:             len(tasks),
		MedianDaysToClose: -1,
		Truncated:         truncated,
		Filters:           filters,
	}

	byStatus := map[string]int{}
	byMonth := map[string]int{}
	byGroup := map[string]int{}
	byAssignee := map[string]int{}
	byTag := map[string]int{}
	byCreator := map[string]int{}
	byPriority := map[string]int{}
	byStage := map[string]int{}
	bySprint := map[string]int{}
	closedByMonth := map[string]int{}
	var closeDurations []float64

	for i := range tasks {
		t := &tasks[i]
		bucket := bitrixStatusBucket(t.Status)
		byStatus[bucket]++
		if bucket == "completed" {
			resp.Completed++
		} else {
			resp.Open++
		}

		created, hasCreated := bitrix.ParseTime(t.CreatedAt)
		if hasCreated {
			byMonth[created.Format("2006-01")]++
		}
		if closed, ok := bitrix.ParseTime(t.ClosedAt); ok {
			closedByMonth[closed.Format("2006-01")]++
			if hasCreated && !closed.Before(created) {
				closeDurations = append(closeDurations, closed.Sub(created).Hours()/24)
			}
		}
		if g := strings.TrimSpace(t.GroupID); g != "" && g != "0" {
			byGroup[g]++
		}
		if a := strings.TrimSpace(t.ResponsibleID); a != "" {
			byAssignee[a]++
		}
		if c := strings.TrimSpace(t.CreatedByID); c != "" {
			byCreator[c]++
		}
		if pr := strings.TrimSpace(t.Priority); pr != "" {
			byPriority[pr]++
		}
		if st := strings.TrimSpace(t.StageID); st != "" {
			byStage[st]++
		}
		if sp := strings.TrimSpace(t.SprintID); sp != "" && sp != "0" {
			bySprint[sp]++
		}
		if _, hasDeadline := bitrix.ParseTime(t.Deadline); !hasDeadline {
			resp.NoDeadline++
		} else if taskIsOverdue(t) {
			resp.Overdue++
		}
		tagged := false
		for _, raw := range t.Tags {
			tag := strings.TrimSpace(raw)
			if tag == "" {
				continue
			}
			// Fold case so "BUG" and "bug" are one bucket. The first spelling
			// seen wins as the display key, which keeps the portal's own
			// casing rather than shouting or flattening it.
			key := tag
			for existing := range byTag {
				if strings.EqualFold(existing, tag) {
					key = existing
					break
				}
			}
			byTag[key]++
			tagged = true
		}
		if !tagged {
			resp.Untagged++
		}
	}

	if len(closeDurations) > 0 {
		sort.Float64s(closeDurations)
		mid := len(closeDurations) / 2
		if len(closeDurations)%2 == 1 {
			resp.MedianDaysToClose = closeDurations[mid]
		} else {
			resp.MedianDaysToClose = (closeDurations[mid-1] + closeDurations[mid]) / 2
		}
	}

	resp.ByStatus = sortedBuckets(byStatus, nil, false)
	resp.ByMonth = sortedBuckets(byMonth, nil, true)
	resp.ClosedByMon = sortedBuckets(closedByMonth, nil, true)
	resp.ByGroup = sortedBuckets(byGroup, groupNames, false)
	resp.ByAssignee = sortedBuckets(byAssignee, userNames, false)
	resp.ByTag = sortedBuckets(byTag, nil, false)
	resp.ByCreator = sortedBuckets(byCreator, userNames, false)
	resp.ByPriority = sortedBuckets(byPriority, bitrixPriorityLabels, false)
	resp.ByStage = sortedBuckets(byStage, stageNames, false)
	resp.BySprint = sortedBuckets(bySprint, nil, false)

	writeJSON(w, http.StatusOK, resp)
}

// untilLabel reports the window's real upper bound: the caller's `until` when
// one was given, otherwise today. Echoing "today" for a bounded request would
// mislabel a historical window as current.
func untilLabel(until, now time.Time) string {
	if until.IsZero() {
		return now.Format("2006-01-02")
	}
	return until.Format("2006-01-02")
}
