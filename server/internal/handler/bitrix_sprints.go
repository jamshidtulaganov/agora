package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

// Which Bitrix workgroup is the team's current sprint.
//
// The team runs two-week sprints as workgroups, and the names are what humans
// typed over a year: "Sprint 11", "Sprint(12)", "Спринт 12", "Iyun Sprint (8)",
// "10 спринт (Июль)". Matching a name pattern to pick "the current one" would
// silently choose the wrong sprint the first time someone names one
// differently — and a daily report against last month's sprint looks correct
// while being useless.
//
// So the name only decides whether a group is a SPRINT at all. Which one is
// CURRENT is decided by activity: the sprint being worked in is the one
// collecting tasks now. That also means a rollover needs no configuration —
// the day the team starts Sprint 13, the answer changes by itself.

// sprintNameHints are the words that mark a workgroup as a sprint, in the
// scripts this portal actually uses. Deliberately not a regex over numbers:
// "Sprint Top Tasks" is a sprint group too, and a numeric rule would drop it.
var sprintNameHints = []string{"sprint", "спринт"}

// sprintActivityWindow is how far back "being worked in now" looks. Two weeks
// is the sprint length, so a sprint that has just started still wins over the
// one that just ended.
const sprintActivityWindow = 14 * 24 * time.Hour

// looksLikeSprint reports whether a workgroup name marks it as a sprint.
func looksLikeSprint(name string) bool {
	lower := strings.ToLower(name)
	for _, hint := range sprintNameHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

type bitrixSprintResponse struct {
	GroupID string `json:"group_id"`
	Name    string `json:"name"`
	// RecentTasks is how many tasks were created in this group inside the
	// activity window. The ordering key, and the evidence for it — a caller
	// can see WHY one sprint was called current.
	RecentTasks int `json:"recent_tasks"`
	// Current marks the single most active sprint. Ties do not happen in
	// practice; when counts match, the higher group id wins as the newer one.
	Current bool `json:"current"`
}

// ListBitrixSprints handles GET /api/bitrix/sprints.
func (h *Handler) ListBitrixSprints(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	client := bitrix.NewClient(bitrixWebhookURL())

	groups, err := client.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list bitrix workgroups")
		return
	}
	sprintNames := map[string]string{}
	for _, g := range groups {
		if looksLikeSprint(g.Name) {
			sprintNames[g.ID] = g.Name
		}
	}
	if len(sprintNames) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"sprints": []bitrixSprintResponse{}, "count": 0})
		return
	}

	// One windowed read, counted per group — cheaper and more honest than
	// asking the portal once per sprint, which would also race a task moving
	// between them mid-scan.
	since := time.Now().Add(-sprintActivityWindow)
	tasks, err := client.ListTasksSince(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read recent bitrix tasks")
		return
	}
	counts := map[string]int{}
	for i := range tasks {
		if g := strings.TrimSpace(tasks[i].GroupID); sprintNames[g] != "" {
			counts[g]++
		}
	}

	out := make([]bitrixSprintResponse, 0, len(sprintNames))
	for id, name := range sprintNames {
		out = append(out, bitrixSprintResponse{GroupID: id, Name: name, RecentTasks: counts[id]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RecentTasks != out[j].RecentTasks {
			return out[i].RecentTasks > out[j].RecentTasks
		}
		// Newer group id breaks a tie: workgroup ids increase over time, so the
		// later-created sprint is the later one.
		a, _ := strconv.Atoi(out[i].GroupID)
		b, _ := strconv.Atoi(out[j].GroupID)
		return a > b
	})
	// Only mark a current sprint when something actually happened in it. With
	// no activity anywhere, saying "this is the current sprint" would be a
	// guess dressed as an answer.
	if len(out) > 0 && out[0].RecentTasks > 0 {
		out[0].Current = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprints": out, "count": len(out)})
}
