package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Sign-up attribution (first touch), written by the person's own browser.
// ---------------------------------------------------------------------------

// attributionWindow: only a fresh account may record where it came from. An
// old account posting a first touch now would be describing a later visit.
const attributionWindow = 7 * 24 * time.Hour

type attributionRequest struct {
	Source      string `json:"source"`
	Medium      string `json:"medium"`
	Campaign    string `json:"campaign"`
	Referrer    string `json:"referrer"`
	LandingPath string `json:"landing_path"`
	FirstSeenAt string `json:"first_seen_at"`
}

// cleanAttr keeps a short, printable, single-line value.
func cleanAttr(s string, max int, lower bool) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if lower {
		s = strings.ToLower(s)
	}
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// referrerHost keeps only the host of a referring URL (never its path or
// query, which can carry personal data).
func referrerHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return cleanAttr(strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."), 100, true)
}

// landingPath keeps only the path of the first page (no query, no fragment).
func landingPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	return cleanAttr(raw, 200, false)
}

// PostMyAttribution records where the signed-in person came from before they
// signed up. Write-once and only for accounts created in the last 7 days; any
// other call is a quiet 204 so the browser can fire and forget.
func (h *Handler) PostMyAttribution(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req attributionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, err := parseUUIDStrict(userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user")
		return
	}
	user, err := h.Queries.GetUser(r.Context(), id)
	if err != nil || !user.CreatedAt.Valid || time.Since(user.CreatedAt.Time) > attributionWindow {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	params := db.CreateUserAttributionParams{
		UserID:       id,
		Source:       cleanAttr(req.Source, 100, true),
		Medium:       cleanAttr(req.Medium, 100, true),
		Campaign:     cleanAttr(req.Campaign, 100, false),
		ReferrerHost: referrerHost(req.Referrer),
		LandingPath:  landingPath(req.LandingPath),
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.FirstSeenAt)); err == nil && t.Before(time.Now().Add(time.Minute)) {
		params.FirstSeenAt = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if params.Source == "" && params.Medium == "" && params.Campaign == "" && params.ReferrerHost == "" && params.LandingPath == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.Queries.CreateUserAttribution(r.Context(), params); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Growth + marketing view for /admin.
// ---------------------------------------------------------------------------

type growthDay struct {
	Day           string `json:"day"` // YYYY-MM-DD (UTC)
	Signups       int    `json:"signups"`
	ActiveUsers   int    `json:"active_users"`
	NewWorkspaces int    `json:"new_workspaces"`
}

type funnelRow struct {
	Key           string `json:"key"`
	Signups       int    `json:"signups"`
	OwnsWorkspace int    `json:"owns_workspace"`
	Paying        int    `json:"paying"`
}

type growthResponse struct {
	Days         []growthDay `json:"days"`
	Signups      int         `json:"signups"`
	Attributed   int         `json:"attributed"`
	Sources      []funnelRow `json:"sources"`
	Campaigns    []funnelRow `json:"campaigns"`
	Referrers    []funnelRow `json:"referrers"`
	LandingPages []funnelRow `json:"landing_pages"`
}

// directKey is the bucket for sign-ups with no source, campaign or referrer.
const directKey = "(direct)"

func groupFunnel(rows []db.AdminSignupAttributionRow, key func(db.AdminSignupAttributionRow) string, limit int) []funnelRow {
	byKey := map[string]*funnelRow{}
	for _, row := range rows {
		k := key(row)
		if k == "" {
			k = directKey
		}
		f := byKey[k]
		if f == nil {
			f = &funnelRow{Key: k}
			byKey[k] = f
		}
		f.Signups++
		if row.OwnsWorkspace {
			f.OwnsWorkspace++
		}
		if row.Paying {
			f.Paying++
		}
	}
	out := make([]funnelRow, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Signups != out[j].Signups {
			return out[i].Signups > out[j].Signups
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// sourceKey is "source / medium" (medium only when present).
func sourceKey(r db.AdminSignupAttributionRow) string {
	switch {
	case r.Source != "" && r.Medium != "":
		return r.Source + " / " + r.Medium
	case r.Source != "":
		return r.Source
	case r.ReferrerHost != "":
		// No UTM tags: the referring site is the best source we have.
		return r.ReferrerHost + " / referral"
	default:
		return ""
	}
}

func buildGrowth(days []db.AdminGrowthDailyRow, signups []db.AdminSignupAttributionRow) growthResponse {
	resp := growthResponse{Days: make([]growthDay, 0, len(days)), Signups: len(signups)}
	for _, d := range days {
		resp.Days = append(resp.Days, growthDay{
			Day:           d.Day.Time.Format("2006-01-02"),
			Signups:       int(d.Signups),
			ActiveUsers:   int(d.ActiveUsers),
			NewWorkspaces: int(d.NewWorkspaces),
		})
	}
	for _, s := range signups {
		if s.Source != "" || s.Medium != "" || s.Campaign != "" || s.ReferrerHost != "" || s.LandingPath != "" {
			resp.Attributed++
		}
	}
	resp.Sources = groupFunnel(signups, sourceKey, 20)
	resp.Campaigns = groupFunnel(signups, func(r db.AdminSignupAttributionRow) string { return r.Campaign }, 20)
	resp.Referrers = groupFunnel(signups, func(r db.AdminSignupAttributionRow) string { return r.ReferrerHost }, 20)
	resp.LandingPages = groupFunnel(signups, func(r db.AdminSignupAttributionRow) string { return r.LandingPath }, 20)
	return resp
}

// GetAdminGrowth returns daily growth series and the sign-up funnel by
// marketing source for the last N days (?days=7|30|90, default 30).
func (h *Handler) GetAdminGrowth(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v >= 7 && v <= 365 {
		days = v
	}
	daily, err := h.Queries.AdminGrowthDaily(r.Context(), int32(days))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load growth")
		return
	}
	since := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	signups, err := h.Queries.AdminSignupAttribution(r.Context(), pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sign-ups")
		return
	}
	writeJSON(w, http.StatusOK, buildGrowth(daily, signups))
}
