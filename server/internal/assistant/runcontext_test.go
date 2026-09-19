package assistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The window arithmetic, on a fixed clock — the only way to state the claim
// ("an issue filed at 23:50 in Asia/Dubai is part of that caller's today")
// without the answer depending on when the suite happens to run.
func TestDayWindowIsTheCallersCalendar(t *testing.T) {
	dubai, err := time.LoadLocation("Asia/Dubai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// An issue filed at 23:50 local is part of that caller's today.
	lateEvening := time.Date(2026, 9, 17, 23, 50, 0, 0, dubai)
	justBeforeMidnight := time.Date(2026, 9, 17, 23, 55, 0, 0, dubai)
	from, to := DayWindow(justBeforeMidnight, dubai, 1)
	if !from.Equal(time.Date(2026, 9, 17, 0, 0, 0, 0, dubai)) {
		t.Fatalf("today started at %s, want local midnight", from)
	}
	if lateEvening.Before(from) || lateEvening.After(to) {
		t.Fatalf("23:50 local is outside the caller's own today [%s, %s]", from, to)
	}

	// And the hours a UTC window loses. At 10:00 local on the 17th it is still
	// 06:00 UTC on the 17th, so both calendars agree on the DATE — but this
	// caller's day began four hours before UTC's did, and everything they did
	// in those four hours (01:00 local = 21:00 UTC on the 16th) falls outside a
	// server-side "today" while being squarely inside their own.
	morning := time.Date(2026, 9, 17, 10, 0, 0, 0, dubai)
	earlyHours := time.Date(2026, 9, 17, 1, 0, 0, 0, dubai)
	localFrom, _ := DayWindow(morning, dubai, 1)
	if earlyHours.Before(localFrom) {
		t.Fatalf("01:00 local is outside the caller's today (window from %s)", localFrom)
	}
	utcFrom, _ := DayWindow(morning, time.UTC, 1)
	if !earlyHours.Before(utcFrom) {
		t.Fatalf("the fixture does not discriminate: UTC today starts at %s", utcFrom)
	}

	// n whole local days, ending with the one in progress.
	weekFrom, _ := DayWindow(lateEvening, dubai, 7)
	if !weekFrom.Equal(time.Date(2026, 9, 11, 0, 0, 0, 0, dubai)) {
		t.Fatalf("7-day window started at %s, want 11 Sep local midnight", weekFrom)
	}

	// A day count below one is still a window, not an empty one.
	zeroFrom, _ := DayWindow(lateEvening, dubai, 0)
	if !zeroFrom.Equal(time.Date(2026, 9, 17, 0, 0, 0, 0, dubai)) {
		t.Fatalf("days=0 window started at %s, want today", zeroFrom)
	}
}

// An unknown or absent zone degrades to UTC rather than to the server's local
// zone, which is an accident of where the container happens to run.
func TestLoadLocationFallsBackToUTC(t *testing.T) {
	for _, tz := range []string{"", "   ", "Mars/Olympus_Mons"} {
		if got := LoadLocation(tz); got != time.UTC {
			t.Fatalf("LoadLocation(%q) = %v, want UTC", tz, got)
		}
	}
	if got := LoadLocation("UTC"); got != time.UTC {
		t.Fatalf(`LoadLocation("UTC") = %v`, got)
	}
}

func TestTimezoneRidesOnTheContext(t *testing.T) {
	ctx := context.Background()
	if got := TimezoneFrom(ctx); got != "" {
		t.Fatalf("bare context carries %q", got)
	}
	if got := TimezoneFrom(WithTimezone(ctx, "Asia/Dubai")); got != "Asia/Dubai" {
		t.Fatalf("TimezoneFrom = %q", got)
	}
	// An empty capture must not overwrite an inherited one with nothing.
	if got := TimezoneFrom(WithTimezone(WithTimezone(ctx, "Asia/Dubai"), "  ")); got != "Asia/Dubai" {
		t.Fatalf("empty timezone clobbered the captured one: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Tool-outcome classification
// ---------------------------------------------------------------------------

// The distinction that the "a write may have succeeded" bug collapsed: a tool
// that REFUSED is not a tool whose outcome is unknown.
func TestClassifyToolOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result string
		err    error
		want   string
	}{
		{"plain success", `{"created":true,"identifier":"MUL-1"}`, nil, operationSucceeded},
		{"parked for confirmation", `{"status":"needs_confirmation","operation":{}}`, nil, operationSucceeded},
		{"validation refusal", `{"error":"title is required"}`, context.Canceled, operationFailed},
		{"refusal with no go error", `{"error":"you are not a member of that workspace"}`, nil, operationFailed},
		{"executor error, empty body", `{}`, context.DeadlineExceeded, operationFailed},
		{"dispatched, outcome unknown", `{"status":"uncertain","inspect":"check the issue"}`, nil, operationUncertain},
		{"unparseable body, no error", `not json`, nil, operationSucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyToolOutcome([]byte(tc.result), tc.err); got != tc.want {
				t.Fatalf("classifyToolOutcome(%s) = %q, want %q", tc.result, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Prompt: coverage and self-knowledge
// ---------------------------------------------------------------------------

// The model has to be TOLD that a page length is not a total, or it will keep
// counting the rows in front of it and calling that the answer.
func TestPromptRequiresCoverageToBeStated(t *testing.T) {
	prompt := buildSystemPrompt(UserContext{Name: "Ann"}, "")
	for _, want := range []string{
		"scope",
		"workspaces_checked",
		"is NEVER the total",
		"showing 20 of 143",
		"scope.failed",
		"scope.window",
	} {
		if !containsFold(prompt, want) {
			t.Fatalf("the prompt never mentions %q — the model has no rule to follow", want)
		}
	}
}

// "Which model are you" is answered from configuration, not from the model's
// training data, so the reply matches the label in the UI footer.
func TestPromptCarriesTheConfiguredModelLabel(t *testing.T) {
	prompt := buildSystemPrompt(UserContext{Name: "Ann", ModelLabel: "GPT (gpt-5.6-luna)"}, "")
	if !containsFold(prompt, "GPT (gpt-5.6-luna)") {
		t.Fatal("the prompt does not name the configured model")
	}
	if !containsFold(prompt, "do not guess from your training data") {
		t.Fatal("the prompt does not tell the model to trust the configured label")
	}
	// No label configured: say nothing rather than something made up.
	if containsFold(buildSystemPrompt(UserContext{Name: "Ann"}, ""), "You are running on") {
		t.Fatal("the prompt asserts a model identity it was never given")
	}
}

// The standing-report recipes are the domain layer: without them every
// "sprint report" is improvised from scratch with a different shape and a
// different (often wrong) tool choreography. This pins the five recipe names
// and the two rules that keep recipes safe — reuse the artifact instead of
// minting duplicates, and never write.
func TestPromptCarriesTheReportRecipes(t *testing.T) {
	prompt := buildSystemPrompt(UserContext{Name: "Ann"}, "")
	for _, want := range []string{
		"SPRINT REPORT",
		"STANDUP",
		"QA HEALTH",
		"RELEASE NOTES",
		"MY DAY",
		"never a second create_artifact of the same report",
		"Recipes READ.",
		// Living truth: the sprint report's risks section and the my-day
		// closer both start from the tracker's own staleness signal rather
		// than from the model's read of a status list.
		"list_stale_issues",
		"LEAD THE RISKS WITH WHAT THE TRACKER ITSELF SAYS IS STALE",
	} {
		if !containsFold(prompt, want) {
			t.Fatalf("the prompt never mentions %q — the recipe layer is missing", want)
		}
	}
}

// The management layer is the WRITE half of the domain layer, and it only works
// if the model reaches for propose_plan instead of a chain of single writes.
// This pins the primitive, the four recipes that end in a plan, the rule that
// stops a bulk change being applied without the card, and the cap — the four
// things whose absence silently reverts the assistant to one-write-at-a-time.
func TestPromptCarriesThePlanGuidanceAndManagementRecipes(t *testing.T) {
	prompt := buildSystemPrompt(UserContext{Name: "Ann"}, "")
	for _, want := range []string{
		"propose_plan",
		"SPRINT PLANNING",
		"BULK CHANGE",
		"INBOX TRIAGE",
		"PROJECT BOOTSTRAP",
		"AGENT INTERVIEW",
		"NEVER apply a bulk change without the plan card",
		"A plan holds at most 25 items",
		// The allowlist is rendered from the map, so a tool added to or removed
		// from PlanAllowedTools cannot go unmentioned in the instructions.
		"Plans carry ONLY these tools: " + strings.Join(PlanAllowedToolNames(), ", "),
	} {
		if !containsFold(prompt, want) {
			t.Fatalf("the prompt never mentions %q — the management layer is missing", want)
		}
	}
}

// The timezone line has to say what it is FOR, or the model converts the
// windows the tools already computed.
func TestPromptExplainsTheTimezone(t *testing.T) {
	prompt := buildSystemPrompt(UserContext{Name: "Ann", Timezone: "Asia/Dubai"}, "")
	if !containsFold(prompt, "Asia/Dubai") || !containsFold(prompt, "local calendar") {
		t.Fatalf("timezone guidance missing from the prompt")
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
