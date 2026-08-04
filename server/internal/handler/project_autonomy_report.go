package handler

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Project autonomy report — the rollout-ladder instrument. Phase 2 made only
// risk:safe issues auto-merge; this read-only report tells a human WHICH modules
// have earned that promotion, by aggregating each module's QA outcomes (and per
// agent). A module with a long clean pass streak is a candidate to re-tier to
// safe in the risk map; a module with fails stays guarded. No writes, no
// automation — the human decides and edits the risk map.
//
// GET /api/projects/{id}/autonomy-report

type autonomyAgentStat struct {
	Agent string `json:"agent"`
	Total int    `json:"total"`
	Pass  int    `json:"pass"`
	Fail  int    `json:"fail"`
}

type autonomyModuleStat struct {
	Module   string              `json:"module"`
	Total    int                 `json:"total"` // issues with a QA verdict
	Pass     int                 `json:"pass"`
	Fail     int                 `json:"fail"`
	Untested int                 `json:"untested"`  // module issues with no QA verdict yet
	PassRate float64             `json:"pass_rate"` // pass / (pass+fail), 0 when none tested
	ByAgent  []autonomyAgentStat `json:"by_agent"`
}

type autonomyReport struct {
	ProjectID string               `json:"project_id"`
	Modules   []autonomyModuleStat `json:"modules"`
}

// ProjectAutonomyReport aggregates the per-issue QA outcomes into per-module
// (and per-agent) pass/fail stats.
func (h *Handler) ProjectAutonomyReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: project.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	rows, err := h.Queries.ProjectAutonomyRows(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to load autonomy rows: "+err.Error())
		return
	}

	// module -> stat, and module -> agent -> {pass,fail,total}, aggregated in Go
	// (per-project volume is small). Agent display name resolved lazily + cached.
	type agg struct {
		total, pass, fail, untested int
		byAgent                     map[string]*autonomyAgentStat
	}
	mods := map[string]*agg{}
	nameCache := map[string]string{}

	agentName := func(row db.ProjectAutonomyRowsRow) string {
		if !row.AssigneeID.Valid || !row.AssigneeType.Valid || row.AssigneeType.String != "agent" {
			return "" // human / unassigned — not an autonomy signal
		}
		id := uuidToString(row.AssigneeID)
		if n, ok := nameCache[id]; ok {
			return n
		}
		name := id
		if a, err := h.Queries.GetAgent(r.Context(), row.AssigneeID); err == nil {
			name = a.Name
		}
		nameCache[id] = name
		return name
	}

	for _, row := range rows {
		verdict := strings.ToLower(strings.TrimSpace(row.QaVerdict))
		an := agentName(row)
		for _, label := range row.Modules {
			mod := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(label)), "module:"))
			if mod == "" {
				continue
			}
			m := mods[mod]
			if m == nil {
				m = &agg{byAgent: map[string]*autonomyAgentStat{}}
				mods[mod] = m
			}
			switch verdict {
			case "pass":
				m.total++
				m.pass++
			case "fail":
				m.total++
				m.fail++
			default:
				m.untested++
			}
			if an != "" && (verdict == "pass" || verdict == "fail") {
				as := m.byAgent[an]
				if as == nil {
					as = &autonomyAgentStat{Agent: an}
					m.byAgent[an] = as
				}
				as.Total++
				if verdict == "pass" {
					as.Pass++
				} else {
					as.Fail++
				}
			}
		}
	}

	report := autonomyReport{ProjectID: uuidToString(project.ID)}
	for mod, m := range mods {
		stat := autonomyModuleStat{
			Module: mod, Total: m.total, Pass: m.pass, Fail: m.fail, Untested: m.untested,
		}
		if m.pass+m.fail > 0 {
			stat.PassRate = float64(m.pass) / float64(m.pass+m.fail)
		}
		for _, as := range m.byAgent {
			stat.ByAgent = append(stat.ByAgent, *as)
		}
		sort.Slice(stat.ByAgent, func(i, j int) bool { return stat.ByAgent[i].Agent < stat.ByAgent[j].Agent })
		report.Modules = append(report.Modules, stat)
	}
	// Most-tested modules first — the ones with enough signal to act on.
	sort.Slice(report.Modules, func(i, j int) bool {
		if report.Modules[i].Total != report.Modules[j].Total {
			return report.Modules[i].Total > report.Modules[j].Total
		}
		return report.Modules[i].Module < report.Modules[j].Module
	})

	writeJSON(w, http.StatusOK, report)
}
