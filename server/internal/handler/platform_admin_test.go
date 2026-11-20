package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/billing"
)

func TestBuildAdminSummary(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	in := func(d time.Duration) *time.Time { t := now.Add(d); return &t }

	users := []adminUser{
		{LastActiveAt: ago(time.Hour), CreatedAt: ago(2 * 24 * time.Hour)},
		{LastActiveAt: ago(10 * 24 * time.Hour), CreatedAt: ago(60 * 24 * time.Hour)},
		{CreatedAt: ago(90 * 24 * time.Hour)},
	}
	workspaces := []adminWorkspace{
		{Subscription: billing.State{Plan: "team", Status: billing.StatusTrialing, TrialEndsAt: in(3 * 24 * time.Hour)}},
		{Subscription: billing.State{Plan: "team", Status: billing.StatusTrialing, TrialEndsAt: in(12 * 24 * time.Hour)}},
		{Subscription: billing.State{Plan: "free", Status: billing.StatusTrialExpired}},
		{Provider: "manual", Members: 4, Subscription: billing.State{Plan: "team", Status: billing.StatusActive, BillingInterval: "year", SeatsPaid: 5}},
		{Provider: "manual", Members: 2, Subscription: billing.State{Plan: "team", Status: billing.StatusPastDue, BillingInterval: "month"}},
		{Provider: "grandfathered", Members: 30, Subscription: billing.State{Plan: "team", Status: billing.StatusActive}},
	}
	s := buildAdminSummary(users, workspaces, now)
	want := adminSummary{
		Users: 3, ActiveUsers7d: 1, ActiveUsers30d: 2, NewUsers7d: 1,
		Workspaces: 6, Trialing: 2, TrialsEnding7d: 1, Free: 1, Paying: 2, PastDue: 1,
		// 5 seats yearly at $12 + 2 members monthly at $15; grandfathered not counted.
		EstMonthlyRevenueCent: 5*1200 + 2*1500,
	}
	if s != want {
		t.Fatalf("summary\n got %+v\nwant %+v", s, want)
	}
}

func TestRequirePlatformAdminHidesFromEveryoneWhenUnset(t *testing.T) {
	t.Setenv("AGORA_PLATFORM_ADMIN_EMAILS", "")
	h := &Handler{}
	called := false
	srv := h.RequirePlatformAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	req.Header.Set("X-User-ID", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if called || rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 and no call, got %d called=%v", rec.Code, called)
	}
}

func TestRequirePlatformAdminRejectsMachineActors(t *testing.T) {
	t.Setenv("AGORA_PLATFORM_ADMIN_EMAILS", "boss@example.com")
	h := &Handler{}
	called := false
	srv := h.RequirePlatformAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	req.Header.Set("X-User-ID", "00000000-0000-0000-0000-000000000001")
	req.Header.Set("X-Actor-Source", "task_token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if called || rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 and no call, got %d called=%v", rec.Code, called)
	}
}
