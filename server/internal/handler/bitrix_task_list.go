package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

// A filtered list of Bitrix tasks, with links.
//
// The analytics endpoint answers "how many". A message that says "6 overdue"
// and names three people still leaves the reader opening Bitrix and searching
// — so the tasks themselves, addressable, are what turns a status line into
// something someone can act on in one tap.
//
// The link is built HERE, not by the agent. A portal URL is easy to get subtly
// wrong (personal vs group path, trailing slash, wrong id), and a report full
// of links that 404 is worse than one with none. The server knows the portal
// host from the webhook it already holds, so it is the only place that can
// build them correctly.

// bitrixTaskListLimit bounds the response. A daily message quotes a handful;
// returning a whole sprint would push the useful ones out of the model's
// attention and out of the reader's.
const bitrixTaskListLimit = 50

type bitrixTaskListItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// URL opens the task in Bitrix. Empty when the portal host cannot be
	// derived — a missing link is honest, a broken one is not.
	URL             string `json:"url,omitempty"`
	ResponsibleID   string `json:"responsible_id,omitempty"`
	ResponsibleName string `json:"responsible_name,omitempty"`
	Deadline        string `json:"deadline,omitempty"`
	StageID         string `json:"stage_id,omitempty"`
	Overdue         bool   `json:"overdue"`
}

// ListBitrixTaskDetails handles GET /api/bitrix/task-list.
//
// Takes the same filters as the analytics rollup, so "the tasks behind that
// number" is the same query with a different verb — a caller cannot accidentally
// list a different set than it counted.
func (h *Handler) ListBitrixTaskDetails(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	query := r.URL.Query()
	for key := range query {
		if !bitrixFilterParams[key] && key != "limit" {
			writeError(w, http.StatusBadRequest, "unknown filter: "+key)
			return
		}
	}

	since, until, ok := parseBitrixWindow(w, query)
	if !ok {
		return
	}
	filters, ok := parseBitrixFilters(w, query)
	if !ok {
		return
	}

	client := bitrix.NewClient(bitrixWebhookURL())
	tasks, err := client.ListTasksBetween(r.Context(), since, until)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list bitrix tasks")
		return
	}
	if filters.Tag != "" {
		filters.tagSpellings = expandTagQuery(filters.Tag)
	}
	if filters.Stage != "" {
		// Stage names are per group; resolving them needs the group's stage
		// list, which the rollup already walks. Here an id is expected — the
		// caller has one from by_stage.
		filters.stageIDs = map[string]bool{}
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
	out := make([]bitrixTaskListItem, 0, bitrixTaskListLimit)
	for i := range tasks {
		t := &tasks[i]
		if !filters.matches(t) {
			continue
		}
		item := bitrixTaskListItem{
			ID:    t.ID,
			Title: t.Title,
			// Built server-side: a portal URL is easy to get subtly wrong,
			// and a report full of links that 404 is worse than one with none.
			URL:             bitrixTaskURL(t.ID),
			ResponsibleID:   strings.TrimSpace(t.ResponsibleID),
			ResponsibleName: userNames[strings.TrimSpace(t.ResponsibleID)],
			Deadline:        t.Deadline,
			StageID:         t.StageID,
			Overdue:         taskIsOverdue(t),
		}
		out = append(out, item)
	}
	// Oldest deadline first: the task that has been late longest is the one to
	// name, and a caller taking the first few should get those.
	sort.SliceStable(out, func(i, j int) bool {
		a, aOK := bitrix.ParseTime(out[i].Deadline)
		b, bOK := bitrix.ParseTime(out[j].Deadline)
		switch {
		case aOK && bOK:
			return a.Before(b)
		case aOK:
			return true
		default:
			return false
		}
	})

	limit := bitrixTaskListLimit
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < limit {
			limit = n
		}
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":     out,
		"count":     len(out),
		"truncated": truncated,
		"filters":   filters,
	})
}
