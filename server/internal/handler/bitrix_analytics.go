package handler

import (
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
	Open        int                     `json:"open"`
	ByStatus    []bitrixAnalyticsBucket `json:"by_status"`
	ByMonth     []bitrixAnalyticsBucket `json:"by_month"`
	ByGroup     []bitrixAnalyticsBucket `json:"by_group"`
	ByAssignee  []bitrixAnalyticsBucket `json:"by_assignee"`
	ClosedByMon []bitrixAnalyticsBucket `json:"closed_by_month"`
	// MedianDaysToClose is over tasks that carry BOTH timestamps. Reported as
	// -1 when no task in the window closed, so "no data" is distinguishable
	// from "closed the same day".
	MedianDaysToClose float64 `json:"median_days_to_close"`
	// Truncated warns that the portal returned as many tasks as the client's
	// safety cap allows, so older tasks in the window are missing.
	Truncated bool `json:"truncated"`
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

// GetBitrixAnalytics handles
// GET /api/bitrix/analytics?since=YYYY-MM-DD&until=YYYY-MM-DD.
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

	resp := bitrixAnalyticsResponse{
		Since:             since.Format("2006-01-02"),
		Until:             untilLabel(until, now),
		Total:             len(tasks),
		MedianDaysToClose: -1,
		Truncated:         len(tasks) >= bitrix.MaxTasksPerRequest,
	}

	byStatus := map[string]int{}
	byMonth := map[string]int{}
	byGroup := map[string]int{}
	byAssignee := map[string]int{}
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
