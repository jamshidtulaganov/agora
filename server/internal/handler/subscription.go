package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jamshidtulaganov/agora/server/internal/billing"
)

type subscriptionUsage struct {
	Seats           int `json:"seats"`
	Automations     int `json:"automations"`
	ReportSchedules int `json:"report_schedules"`
	Autopilots      int `json:"autopilots"`
}

type subscriptionResponse struct {
	billing.State
	Usage   subscriptionUsage  `json:"usage"`
	Plans   []billing.PlanInfo `json:"plans"`
	Contact string             `json:"contact"`
}

// GetWorkspaceSubscription returns the workspace's plan, trial and usage for
// the billing page. Read-only and member-visible: the plan is not a secret, and
// there is deliberately no tenant-facing write — plans change only through the
// operator's admintool (and later a payment provider webhook).
func (h *Handler) GetWorkspaceSubscription(w http.ResponseWriter, r *http.Request) {
	wsID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	resp := subscriptionResponse{
		State:   billing.Describe(r.Context(), h.Queries, wsID),
		Plans:   billing.Catalog,
		Contact: billing.Contact(),
	}
	counts, err := h.Queries.GetWorkspaceUsageCounts(r.Context(), wsID)
	if err != nil {
		// Usage is decoration on this page; the plan itself still renders.
		slog.Warn("billing: usage counts failed", "error", err, "workspace_id", uuidToString(wsID))
	} else {
		resp.Usage = subscriptionUsage{
			Seats:           int(counts.Seats),
			Automations:     int(counts.Automations),
			ReportSchedules: int(counts.ReportSchedules),
			Autopilots:      int(counts.Autopilots),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
