package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/billing"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Platform admin: the operator of this Agora instance (not a workspace role).
// Who that is comes from AGORA_PLATFORM_ADMIN_EMAILS (comma-separated), read
// from the process environment ONLY — instance_config is writable by workspace
// owners, so a registry key would let any owner make themselves admin.

func platformAdminEmails() map[string]bool {
	out := map[string]bool{}
	for _, e := range strings.Split(os.Getenv("AGORA_PLATFORM_ADMIN_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out[e] = true
		}
	}
	return out
}

// RequirePlatformAdmin lets through only a human signed in as one of the
// configured admin emails. Everyone else gets 404, so the page's existence is
// not advertised.
func (h *Handler) RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admins := platformAdminEmails()
		userID := requestUserID(r)
		if len(admins) == 0 || userID == "" || IsMachineActor(r) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		id, err := parseUUIDStrict(userID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		user, err := h.Queries.GetUser(r.Context(), id)
		if err != nil || !admins[strings.ToLower(strings.TrimSpace(user.Email))] {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseUUIDStrict(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := id.Scan(s)
	return id, err
}

func tsOrNil(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

type adminUserWorkspace struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type adminUser struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Email        string               `json:"email"`
	CreatedAt    *time.Time           `json:"created_at"`
	LastLoginAt  *time.Time           `json:"last_login_at"`
	LastActiveAt *time.Time           `json:"last_active_at"`
	Workspaces   []adminUserWorkspace `json:"workspaces"`
}

type adminWorkspace struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Slug         string        `json:"slug"`
	CreatedAt    *time.Time    `json:"created_at"`
	Members      int           `json:"members"`
	OwnerEmail   string        `json:"owner_email"`
	LastActiveAt *time.Time    `json:"last_active_at"`
	Provider     string        `json:"provider"`
	Note         string        `json:"note"`
	Subscription billing.State `json:"subscription"`
}

type adminSummary struct {
	Users                 int `json:"users"`
	ActiveUsers7d         int `json:"active_users_7d"`
	ActiveUsers30d        int `json:"active_users_30d"`
	NewUsers7d            int `json:"new_users_7d"`
	Workspaces            int `json:"workspaces"`
	Trialing              int `json:"trialing"`
	TrialsEnding7d        int `json:"trials_ending_7d"`
	Paying                int `json:"paying"`
	PastDue               int `json:"past_due"`
	Free                  int `json:"free"`
	EstMonthlyRevenueCent int `json:"est_monthly_revenue_cents"`
}

type adminOverview struct {
	Summary    adminSummary     `json:"summary"`
	Users      []adminUser      `json:"users"`
	Workspaces []adminWorkspace `json:"workspaces"`
}

func teamPriceCents(interval string) int {
	for _, p := range billing.Catalog {
		if p.ID == billing.PlanTeam {
			if interval == "year" {
				return p.PriceYearlyCents
			}
			return p.PriceMonthlyCents
		}
	}
	return 0
}

func adminWorkspaceFromRow(row db.AdminListWorkspacesRow, now time.Time) adminWorkspace {
	var sub *db.WorkspaceSubscription
	if row.SubWorkspaceID.Valid {
		sub = &db.WorkspaceSubscription{
			WorkspaceID:      row.SubWorkspaceID,
			Plan:             row.SubPlan,
			Status:           row.SubStatus,
			BillingInterval:  row.SubInterval,
			SeatsPaid:        row.SubSeatsPaid,
			TrialEndsAt:      row.SubTrialEndsAt,
			CurrentPeriodEnd: row.SubCurrentPeriodEnd,
		}
	}
	state := billing.Effective(sub, now)
	state.Enforced = billing.Enabled()
	return adminWorkspace{
		ID:           uuidToString(row.ID),
		Name:         row.Name,
		Slug:         row.Slug,
		CreatedAt:    tsOrNil(row.CreatedAt),
		Members:      int(row.Members),
		OwnerEmail:   row.OwnerEmail,
		LastActiveAt: tsOrNil(row.LastActiveAt),
		Provider:     row.SubProvider,
		Note:         row.SubNote,
		Subscription: state,
	}
}

// buildAdminSummary counts the dashboard numbers. "Paying" means a paid plan
// bought by hand or by a provider; grandfathered workspaces are on Team but
// have never paid, so they are not counted as paying or as revenue.
func buildAdminSummary(users []adminUser, workspaces []adminWorkspace, now time.Time) adminSummary {
	s := adminSummary{Users: len(users), Workspaces: len(workspaces)}
	for _, u := range users {
		if u.LastActiveAt != nil {
			if now.Sub(*u.LastActiveAt) <= 7*24*time.Hour {
				s.ActiveUsers7d++
			}
			if now.Sub(*u.LastActiveAt) <= 30*24*time.Hour {
				s.ActiveUsers30d++
			}
		}
		if u.CreatedAt != nil && now.Sub(*u.CreatedAt) <= 7*24*time.Hour {
			s.NewUsers7d++
		}
	}
	for _, w := range workspaces {
		st := w.Subscription
		switch {
		case st.Status == billing.StatusTrialing:
			s.Trialing++
			if st.TrialEndsAt != nil && st.TrialEndsAt.Sub(now) <= 7*24*time.Hour {
				s.TrialsEnding7d++
			}
		case st.Plan == billing.PlanFree:
			s.Free++
		case w.Provider != "grandfathered" && (st.Status == billing.StatusActive || st.Status == billing.StatusPastDue):
			s.Paying++
			if st.Status == billing.StatusPastDue {
				s.PastDue++
			}
			if st.Plan == billing.PlanTeam {
				seats := st.SeatsPaid
				if seats <= 0 {
					seats = w.Members
				}
				s.EstMonthlyRevenueCent += seats * teamPriceCents(st.BillingInterval)
			}
		}
	}
	return s
}

// GetAdminOverview is the platform admin dashboard: every user and workspace
// with plan state, and the summary numbers on top.
func (h *Handler) GetAdminOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()

	userRows, err := h.Queries.AdminListUsers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	users := make([]adminUser, 0, len(userRows))
	for _, row := range userRows {
		u := adminUser{
			ID:           uuidToString(row.ID),
			Name:         row.Name,
			Email:        row.Email,
			CreatedAt:    tsOrNil(row.CreatedAt),
			LastLoginAt:  tsOrNil(row.LastLoginAt),
			LastActiveAt: tsOrNil(row.LastActiveAt),
			Workspaces:   []adminUserWorkspace{},
		}
		if err := json.Unmarshal([]byte(row.Workspaces), &u.Workspaces); err != nil || u.Workspaces == nil {
			u.Workspaces = []adminUserWorkspace{}
		}
		users = append(users, u)
	}

	wsRows, err := h.Queries.AdminListWorkspaces(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}
	workspaces := make([]adminWorkspace, 0, len(wsRows))
	for _, row := range wsRows {
		workspaces = append(workspaces, adminWorkspaceFromRow(row, now))
	}

	writeJSON(w, http.StatusOK, adminOverview{
		Summary:    buildAdminSummary(users, workspaces, now),
		Users:      users,
		Workspaces: workspaces,
	})
}

// SetAdminWorkspaceSubscription changes a workspace's plan by hand — the same
// operation as `admintool set-plan`, for the admin page.
func (h *Handler) SetAdminWorkspaceSubscription(w http.ResponseWriter, r *http.Request) {
	wsID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var change billing.Change
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&change); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params, err := change.Params()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.Queries.GetWorkspace(r.Context(), wsID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	params.WorkspaceID = wsID
	row, err := h.Queries.SetWorkspaceSubscription(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to change plan")
		return
	}
	slog.Info("platform admin: plan changed", append(logger.RequestAttrs(r),
		"workspace_id", uuidToString(wsID), "admin_user_id", requestUserID(r),
		"mode", change.Mode, "plan", row.Plan, "status", row.Status)...)

	state := billing.Effective(&row, time.Now())
	state.Enforced = billing.Enabled()
	writeJSON(w, http.StatusOK, state)
}
