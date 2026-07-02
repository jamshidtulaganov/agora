package handler

import (
	"net/http"
	"time"
)

// QA speed / regression metrics — the QA Metrics page's aggregate payload.
// Everything is derived from test_run / test_case / agent_task_queue, scoped to
// the workspace; empty slices (never null) so the frontend can map without
// guards.

type qaMetricsTotals struct {
	Total   int64 `json:"total"`
	Passed  int64 `json:"passed"`
	Failed  int64 `json:"failed"`
	Skipped int64 `json:"skipped"`
}

type qaMetricsDay struct {
	Day    string `json:"day"`
	Total  int64  `json:"total"`
	Failed int64  `json:"failed"`
}

type qaMetricsAgent struct {
	Agent  string `json:"agent"`
	Runs   int64  `json:"runs"`
	AvgSec int32  `json:"avg_sec"`
	MinSec int32  `json:"min_sec"`
	MaxSec int32  `json:"max_sec"`
}

type qaMetricsCoverage struct {
	Automated int64 `json:"automated"`
	Scripted  int64 `json:"scripted"`
}

type qaMetricsRun struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	RunSource   string `json:"run_source"`
	CaseTitle   string `json:"case_title"`
	IssueNumber *int32 `json:"issue_number"`
}

type qaMetricsResponse struct {
	Totals     qaMetricsTotals   `json:"totals"`
	ByDay      []qaMetricsDay    `json:"by_day"`
	Agents     []qaMetricsAgent  `json:"agents"`
	Coverage   qaMetricsCoverage `json:"coverage"`
	RecentRuns []qaMetricsRun    `json:"recent_runs"`
}

// GetQAMetrics returns the workspace's QA speed / regression aggregates.
// GET /api/qa/metrics. Each aggregate is best-effort: a failing query leaves
// its section empty rather than failing the page.
func (h *Handler) GetQAMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	ctx := r.Context()

	resp := qaMetricsResponse{
		ByDay:      []qaMetricsDay{},
		Agents:     []qaMetricsAgent{},
		RecentRuns: []qaMetricsRun{},
	}

	if t, err := h.Queries.QAMetricsRunTotals(ctx, wsUUID); err == nil {
		resp.Totals = qaMetricsTotals{Total: t.Total, Passed: t.Passed, Failed: t.Failed, Skipped: t.Skipped}
	}
	if rows, err := h.Queries.QAMetricsRunsByDay(ctx, wsUUID); err == nil {
		for _, d := range rows {
			day := ""
			if d.Day.Valid {
				day = d.Day.Time.Format("2006-01-02")
			}
			resp.ByDay = append(resp.ByDay, qaMetricsDay{Day: day, Total: d.Total, Failed: d.Failed})
		}
	}
	if rows, err := h.Queries.QAMetricsAgentDurations(ctx, wsUUID); err == nil {
		for _, a := range rows {
			resp.Agents = append(resp.Agents, qaMetricsAgent{
				Agent: a.Agent, Runs: a.Runs, AvgSec: a.AvgSec, MinSec: a.MinSec, MaxSec: a.MaxSec,
			})
		}
	}
	if c, err := h.Queries.QAMetricsScriptCoverage(ctx, wsUUID); err == nil {
		resp.Coverage = qaMetricsCoverage{Automated: c.Automated, Scripted: c.Scripted}
	}
	if rows, err := h.Queries.QAMetricsRecentRuns(ctx, wsUUID); err == nil {
		for _, rr := range rows {
			run := qaMetricsRun{
				ID:        uuidToString(rr.ID),
				Status:    rr.Status,
				RunSource: rr.RunSource,
				CaseTitle: rr.CaseTitle,
			}
			if rr.CreatedAt.Valid {
				run.CreatedAt = rr.CreatedAt.Time.Format(time.RFC3339)
			}
			if rr.IssueNumber.Valid {
				n := rr.IssueNumber.Int32
				run.IssueNumber = &n
			}
			resp.RecentRuns = append(resp.RecentRuns, run)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
