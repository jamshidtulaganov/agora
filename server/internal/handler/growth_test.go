package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestAttributionCleaning(t *testing.T) {
	if got := referrerHost("https://www.Google.com/search?q=my+email@x.com"); got != "google.com" {
		t.Fatalf("referrer host: %q", got)
	}
	if got := referrerHost("not a url"); got != "" {
		t.Fatalf("bad referrer must be empty, got %q", got)
	}
	if got := landingPath("/pricing?utm_source=x&email=a@b.c#top"); got != "/pricing" {
		t.Fatalf("landing path: %q", got)
	}
	if got := landingPath("https://evil.test/x"); got != "" {
		t.Fatalf("absolute url must be dropped, got %q", got)
	}
	if got := cleanAttr("  Telegram\n\x00Ads  ", 100, true); got != "telegramads" {
		t.Fatalf("clean: %q", got)
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	if got := cleanAttr(string(long), 100, false); len(got) != 100 {
		t.Fatalf("must cap length, got %d", len(got))
	}
}

func TestBuildGrowth(t *testing.T) {
	day := func(s string) pgtype.Date {
		d, _ := time.Parse("2006-01-02", s)
		return pgtype.Date{Time: d, Valid: true}
	}
	daily := []db.AdminGrowthDailyRow{
		{Day: day("2026-11-19"), Signups: 2, ActiveUsers: 5, NewWorkspaces: 1},
		{Day: day("2026-11-20"), Signups: 3, ActiveUsers: 6},
	}
	signups := []db.AdminSignupAttributionRow{
		{Source: "telegram", Medium: "cpc", Campaign: "launch", OwnsWorkspace: true, Paying: true},
		{Source: "telegram", Medium: "cpc", Campaign: "launch", OwnsWorkspace: true},
		{ReferrerHost: "google.com", LandingPath: "/pricing"},
		{},
		{},
	}
	g := buildGrowth(daily, signups)
	if len(g.Days) != 2 || g.Days[0].Day != "2026-11-19" || g.Days[1].ActiveUsers != 6 {
		t.Fatalf("days: %+v", g.Days)
	}
	if g.Signups != 5 || g.Attributed != 3 {
		t.Fatalf("totals: signups=%d attributed=%d", g.Signups, g.Attributed)
	}
	// telegram and (direct) tie at 2 signups; ties sort by key, so (direct)
	// comes first. Look rows up by key instead of relying on that order.
	if g.Sources[0].Key != directKey || g.Sources[1].Key != "telegram / cpc" {
		t.Fatalf("source order: %+v", g.Sources)
	}
	keys := map[string]int{}
	for _, s := range g.Sources {
		keys[s.Key] = s.Signups
		if s.Key == "telegram / cpc" && (s.Signups != 2 || s.OwnsWorkspace != 2 || s.Paying != 1) {
			t.Fatalf("telegram source: %+v", s)
		}
	}
	if keys["google.com / referral"] != 1 || keys[directKey] != 2 {
		t.Fatalf("sources: %+v", g.Sources)
	}
	if g.LandingPages[0].Key != directKey || g.LandingPages[0].Signups != 4 {
		t.Fatalf("landing pages: %+v", g.LandingPages)
	}
}
