package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Scheduled refresh of a pinned report (docs/assistant-domain-plan.md
// §Phase 2b).
//
// Two questions run through every test here, because they are the two ways a
// cadence can go wrong in a way nobody notices: does it fire at the moment the
// owner actually asked for (in THEIR timezone, across a DST change), and can it
// ever fire more than once for one slot or for somebody who is not the owner.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func putScheduleRequest(t *testing.T, userID, artifactID, pinID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAssistantRequest(http.MethodPut,
		"/api/assistant/artifacts/"+artifactID+"/pins/"+pinID+"/schedule", userID, body)
	w := httptest.NewRecorder()
	testHandler.PutAssistantReportSchedule(w, withURLParams(req, "id", artifactID, "pinId", pinID))
	return w
}

func deleteScheduleRequest(t *testing.T, userID, artifactID, pinID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAssistantRequest(http.MethodDelete,
		"/api/assistant/artifacts/"+artifactID+"/pins/"+pinID+"/schedule", userID, "")
	w := httptest.NewRecorder()
	testHandler.DeleteAssistantReportSchedule(w, withURLParams(req, "id", artifactID, "pinId", pinID))
	return w
}

// putTestSchedule sets a cadence and fails the test if it did not take,
// returning the decoded schedule object.
func putTestSchedule(t *testing.T, userID, artifactID, pinID, body string) map[string]any {
	t.Helper()
	w := putScheduleRequest(t, userID, artifactID, pinID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("put schedule: status %d: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Schedule map[string]any `json:"schedule"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode schedule: %v (%s)", err, w.Body.String())
	}
	if envelope.Schedule == nil {
		t.Fatalf("response carries no schedule object: %s", w.Body.String())
	}
	return envelope.Schedule
}

func countSchedules(t *testing.T, pinID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM assistant_report_schedule WHERE pin_id = $1`, pinID).Scan(&n); err != nil {
		t.Fatalf("count schedules: %v", err)
	}
	return n
}

// scheduleRow is the stored cadence, read back raw so a test asserts what the
// scheduler will actually see rather than what the response said.
type scheduleRow struct {
	frequency  string
	atTime     string
	timezone   string
	lastStatus string
	lastError  string
	nextRunAt  time.Time
}

func readScheduleRow(t *testing.T, pinID string) scheduleRow {
	t.Helper()
	var row scheduleRow
	if err := testPool.QueryRow(context.Background(),
		`SELECT frequency, at_time, timezone, last_status, last_error, next_run_at
		 FROM assistant_report_schedule WHERE pin_id = $1`, pinID,
	).Scan(&row.frequency, &row.atTime, &row.timezone, &row.lastStatus, &row.lastError, &row.nextRunAt); err != nil {
		t.Fatalf("read schedule row: %v", err)
	}
	return row
}

// makeScheduleDue drags the stored slot into the past so one tick has work to
// do. The alternative — waiting for a real slot — is a test that takes a day.
func makeScheduleDue(t *testing.T, pinID string) time.Time {
	t.Helper()
	var due time.Time
	if err := testPool.QueryRow(context.Background(),
		`UPDATE assistant_report_schedule SET next_run_at = now() - interval '1 minute'
		 WHERE pin_id = $1 RETURNING next_run_at`, pinID).Scan(&due); err != nil {
		t.Fatalf("make schedule due: %v", err)
	}
	return due
}

// withBlockingAssistantSession swaps in an assistant whose model call blocks, so
// a started run stays ACTIVE for as long as the test needs it. Teardown releases
// the worker and waits for it to stop before the fixture's rows are deleted —
// register it after the fixture, never before.
func withBlockingAssistantSession(t *testing.T, sessionID string) *assistant.Service {
	t.Helper()
	t.Setenv("ZHIPU_API_KEY", "assistant-schedule-test-key")
	t.Setenv("AGORA_ASSISTANT_SCHEDULES_ENABLED", "true")

	release := make(chan struct{})
	client := &blockingToolChat{release: release}
	svc := assistant.NewService(testHandler.Queries, testHandler.Bus, func() (llm.ToolChat, string, error) {
		return client, "test-model", nil
	})
	svc.Store = testHandler.DB
	svc.TxStarter = testHandler.TxStarter
	svc.Exec = testHandler

	previous := testHandler.Assistant
	testHandler.Assistant = svc
	t.Cleanup(func() {
		close(release)
		svc.CancelSession(sessionID)
		deadline := time.Now().Add(5 * time.Second)
		for svc.HasActiveRun(sessionID) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		testHandler.Assistant = previous
	})
	return svc
}

func countSessionUserMessages(t *testing.T, sessionID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM assistant_message WHERE session_id = $1 AND role = 'user'`, sessionID).Scan(&n); err != nil {
		t.Fatalf("count user messages: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// Schedule math
// ---------------------------------------------------------------------------

// The arithmetic the whole feature rests on. Every case here is a moment where
// naive date math silently fires at the wrong hour — which, for an unattended
// run that rewrites what a workspace reads, is indistinguishable from a bug in
// the report itself.
func TestNextRunAtAcrossPresetsAndDST(t *testing.T) {
	at := func(tz string, year int, month time.Month, day, hour, minute int) time.Time {
		t.Helper()
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Fatalf("load %s: %v", tz, err)
		}
		return time.Date(year, month, day, hour, minute, 0, 0, loc)
	}

	cases := []struct {
		name      string
		frequency string
		atTime    string
		weekday   int
		timezone  string
		after     time.Time
		want      time.Time
	}{
		{
			name:      "same day, later time",
			frequency: "daily", atTime: "09:00", timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 21, 8, 0),
			want:  at("Asia/Tashkent", 2026, time.September, 21, 9, 0),
		},
		{
			// The slot that just fired must not be handed back: strictly after.
			name:      "exactly now rolls to tomorrow",
			frequency: "daily", atTime: "09:00", timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 21, 9, 0),
			want:  at("Asia/Tashkent", 2026, time.September, 22, 9, 0),
		},
		{
			name:      "crossing midnight",
			frequency: "daily", atTime: "00:30", timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 21, 23, 50),
			want:  at("Asia/Tashkent", 2026, time.September, 22, 0, 30),
		},
		{
			name:      "weekdays skip Friday to Monday",
			frequency: "weekdays", atTime: "09:00", timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 18, 10, 0), // Friday
			want:  at("Asia/Tashkent", 2026, time.September, 21, 9, 0),  // Monday
		},
		{
			name:      "weekdays still fire the same Friday when the time is ahead",
			frequency: "weekdays", atTime: "18:00", timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 18, 10, 0),
			want:  at("Asia/Tashkent", 2026, time.September, 18, 18, 0),
		},
		{
			name:      "weekly wraps a full week when today's slot has passed",
			frequency: "weekly", atTime: "09:00", weekday: 1, timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 21, 10, 0), // Monday
			want:  at("Asia/Tashkent", 2026, time.September, 28, 9, 0),  // next Monday
		},
		{
			name:      "weekly reaches forward to its day",
			frequency: "weekly", atTime: "09:00", weekday: 0, timezone: "Asia/Tashkent",
			after: at("Asia/Tashkent", 2026, time.September, 18, 10, 0), // Friday
			want:  at("Asia/Tashkent", 2026, time.September, 20, 9, 0),  // Sunday
		},
		{
			// Spring forward (2026-03-08, America/New_York). 09:00 EST -> 09:00
			// EDT is 23 hours of real time, not 24: the wall clock is what the
			// owner scheduled, so it is the wall clock that must not move.
			name:      "DST spring forward keeps the wall clock",
			frequency: "daily", atTime: "09:00", timezone: "America/New_York",
			after: at("America/New_York", 2026, time.March, 7, 9, 30),
			want:  at("America/New_York", 2026, time.March, 8, 9, 0),
		},
		{
			// Fall back (2026-11-01), the same assertion in the other direction.
			name:      "DST fall back keeps the wall clock",
			frequency: "daily", atTime: "09:00", timezone: "America/New_York",
			after: at("America/New_York", 2026, time.October, 31, 9, 30),
			want:  at("America/New_York", 2026, time.November, 1, 9, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nextRunAt(tc.frequency, tc.atTime, tc.weekday, tc.timezone, tc.after)
			if err != nil {
				t.Fatalf("nextRunAt: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("next = %s, want %s", got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
			// The instant is the contract, but the wall clock is what the owner
			// asked for — assert it separately so a DST regression cannot hide
			// behind an instant that happens to match a different local hour.
			loc, _ := time.LoadLocation(tc.timezone)
			if local := got.In(loc).Format("15:04"); local != normalizeTestTime(tc.atTime) {
				t.Fatalf("fires at %s local, want %s", local, tc.atTime)
			}
		})
	}
}

// normalizeTestTime pads "9:00" to "09:00" so the wall-clock assertion above can
// compare against Go's zero-padded formatting.
func normalizeTestTime(atTime string) string {
	if len(atTime) == 4 {
		return "0" + atTime
	}
	return atTime
}

// DST is the case where "add 24 hours" and "the same time tomorrow" disagree, so
// the difference is asserted directly rather than trusted.
func TestNextRunAtDoesNotDriftAnHourAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	after := time.Date(2026, time.March, 7, 9, 30, 0, 0, loc) // Saturday, EST
	next, err := nextRunAt("daily", "09:00", 0, "America/New_York", after)
	if err != nil {
		t.Fatalf("nextRunAt: %v", err)
	}
	// 09:00 EDT on 2026-03-08 is 13:00 UTC. A naive +24h from 09:00 EST would
	// have produced 14:00 UTC — 10:00 local, an hour late, every day after.
	if utc := next.UTC(); utc != time.Date(2026, time.March, 8, 13, 0, 0, 0, time.UTC) {
		t.Fatalf("next = %s UTC, want 2026-03-08T13:00:00Z", utc.Format(time.RFC3339))
	}
}

func TestNextRunAtRefusesNonsense(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name      string
		frequency string
		atTime    string
		weekday   int
		timezone  string
	}{
		{"unknown frequency", "hourly", "09:00", 0, "UTC"},
		{"unknown timezone", "daily", "09:00", 0, "Mars/Olympus"},
		{"time is not a time", "daily", "9am", 0, "UTC"},
		{"hour out of range", "daily", "24:00", 0, "UTC"},
		{"minute out of range", "daily", "09:60", 0, "UTC"},
		{"weekday out of range", "weekly", "09:00", 7, "UTC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := nextRunAt(tc.frequency, tc.atTime, tc.weekday, tc.timezone, now); err == nil {
				t.Fatalf("accepted %+v", tc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PUT
// ---------------------------------------------------------------------------

func TestPutReportScheduleStoresTheCadenceAndItsNextRun(t *testing.T) {
	f := newReportFixture(t, "sched", "sched", "SC1")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	schedule := putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"America/New_York"}`)

	// The wire contract the frontend's zod schema pins.
	for _, key := range []string{"frequency", "time", "weekday", "timezone", "enabled", "last_run_at", "last_status", "next_run_at"} {
		if _, ok := schedule[key]; !ok {
			t.Fatalf("schedule is missing %q: %v", key, schedule)
		}
	}
	if schedule["frequency"] != "daily" || schedule["time"] != "09:00" || schedule["timezone"] != "America/New_York" {
		t.Fatalf("schedule = %v", schedule)
	}
	if enabled, _ := schedule["enabled"].(bool); !enabled {
		t.Fatalf("a new schedule is not enabled: %v", schedule)
	}
	if schedule["weekday"] != nil {
		t.Fatalf("a daily schedule carries a weekday: %v", schedule["weekday"])
	}
	if schedule["last_run_at"] != nil {
		t.Fatalf("a schedule that never ran carries last_run_at: %v", schedule["last_run_at"])
	}

	// next_run_at is the promise the whole feature keeps: RFC3339, in the
	// future, and at 09:00 in the cadence's OWN timezone — not the server's.
	nextRaw, _ := schedule["next_run_at"].(string)
	next, err := time.Parse(time.RFC3339, nextRaw)
	if err != nil {
		t.Fatalf("next_run_at %q is not RFC3339: %v", nextRaw, err)
	}
	if !next.After(time.Now()) {
		t.Fatalf("next_run_at %s is not in the future", nextRaw)
	}
	loc, _ := time.LoadLocation("America/New_York")
	if local := next.In(loc).Format("15:04"); local != "09:00" {
		t.Fatalf("next run is %s in New York, want 09:00", local)
	}
	if n := countSchedules(t, pinID); n != 1 {
		t.Fatalf("%d schedule rows, want 1", n)
	}
}

func TestPutReportScheduleValidatesTheCadence(t *testing.T) {
	f := newReportFixture(t, "schedvalid", "schedvalid", "SC2")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	for _, tc := range []struct {
		name, body string
	}{
		{"unknown frequency", `{"frequency":"hourly","time":"09:00","timezone":"UTC"}`},
		{"empty frequency", `{"time":"09:00","timezone":"UTC"}`},
		{"time is not HH:MM", `{"frequency":"daily","time":"9am","timezone":"UTC"}`},
		{"hour out of range", `{"frequency":"daily","time":"24:00","timezone":"UTC"}`},
		{"minute out of range", `{"frequency":"daily","time":"09:60","timezone":"UTC"}`},
		{"weekly without a weekday", `{"frequency":"weekly","time":"09:00","timezone":"UTC"}`},
		{"weekday out of range", `{"frequency":"weekly","time":"09:00","weekday":7,"timezone":"UTC"}`},
		{"unknown timezone", `{"frequency":"daily","time":"09:00","timezone":"Mars/Olympus"}`},
		{"empty timezone", `{"frequency":"daily","time":"09:00"}`},
		{"not json", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := putScheduleRequest(t, f.owner, f.artifactID, pinID, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
	if n := countSchedules(t, pinID); n != 0 {
		t.Fatalf("a rejected cadence wrote %d rows", n)
	}

	// The accepted weekly shape, for contrast: the weekday survives the round
	// trip as the number the browser sent (0=Sunday).
	schedule := putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"weekly","time":"08:30","weekday":1,"timezone":"Asia/Tashkent"}`)
	if weekday, _ := schedule["weekday"].(float64); weekday != 1 {
		t.Fatalf("weekday = %v, want 1", schedule["weekday"])
	}
	next, err := time.Parse(time.RFC3339, schedule["next_run_at"].(string))
	if err != nil {
		t.Fatalf("next_run_at: %v", err)
	}
	loc, _ := time.LoadLocation("Asia/Tashkent")
	if day := next.In(loc).Weekday(); day != time.Monday {
		t.Fatalf("a weekday=1 schedule fires on %s", day)
	}
}

// Only the owner schedules. A workspace admin may unpin (2a) but may not spend
// the owner's model budget on a standing run.
func TestPutReportScheduleIsOwnerOnly(t *testing.T) {
	f := newReportFixture(t, "schedowner", "schedowner", "SC3")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	admin := newAssistantTestUser(t, "assistant-schedule-admin@agora.dev")
	addAssistantTestMember(t, f.workspace, admin, "admin")
	outsider := newAssistantTestUser(t, "assistant-schedule-outsider@agora.dev")

	body := `{"frequency":"daily","time":"09:00","timezone":"UTC"}`
	for _, tc := range []struct {
		name, user string
	}{
		{"workspace admin", admin},
		{"outsider", outsider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := putScheduleRequest(t, tc.user, f.artifactID, pinID, body); w.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404: %s", w.Code, w.Body.String())
			}
			if w := deleteScheduleRequest(t, tc.user, f.artifactID, pinID); w.Code != http.StatusNotFound {
				t.Fatalf("delete status %d, want 404: %s", w.Code, w.Body.String())
			}
		})
	}

	// The artifact in the path is not decoration here either: a cadence is only
	// reachable through the artifact its pin belongs to.
	other := createTestArtifact(t, f.owner, f.session, "Another", assistant.ArtifactKindMarkdown, "# other")
	if w := putScheduleRequest(t, f.owner, other, pinID, body); w.Code != http.StatusNotFound {
		t.Fatalf("mismatched artifact: status %d, want 404: %s", w.Code, w.Body.String())
	}
	if w := putScheduleRequest(t, f.owner, "not-a-uuid", pinID, body); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed artifact id: status %d, want 400", w.Code)
	}
	if w := putScheduleRequest(t, f.owner, f.artifactID, "not-a-uuid", body); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed pin id: status %d, want 400", w.Code)
	}
	if n := countSchedules(t, pinID); n != 0 {
		t.Fatalf("a refused schedule wrote %d rows", n)
	}
}

// UNIQUE (pin_id) is the model: a report has ONE cadence, so a second PUT
// replaces the first in place rather than adding a competing unattended run.
func TestPutReportScheduleReplacesInPlace(t *testing.T) {
	f := newReportFixture(t, "schedreplace", "schedreplace", "SC4")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"Asia/Tashkent"}`)
	var firstID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM assistant_report_schedule WHERE pin_id = $1`, pinID).Scan(&firstID); err != nil {
		t.Fatalf("read schedule id: %v", err)
	}
	// A previous outcome describes a cadence that no longer exists.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE assistant_report_schedule SET last_status = 'failed', last_error = 'old failure' WHERE pin_id = $1`,
		pinID); err != nil {
		t.Fatalf("seed outcome: %v", err)
	}

	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"weekly","time":"18:45","weekday":5,"timezone":"America/New_York"}`)

	if n := countSchedules(t, pinID); n != 1 {
		t.Fatalf("%d schedules after replacing one, want 1", n)
	}
	row := readScheduleRow(t, pinID)
	if row.frequency != "weekly" || row.atTime != "18:45" || row.timezone != "America/New_York" {
		t.Fatalf("stored cadence = %+v", row)
	}
	if row.lastStatus != "" || row.lastError != "" {
		t.Fatalf("the replaced cadence inherited the old outcome: %+v", row)
	}
	var sameID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM assistant_report_schedule WHERE pin_id = $1`, pinID).Scan(&sameID); err != nil {
		t.Fatalf("read schedule id: %v", err)
	}
	if sameID != firstID {
		t.Fatalf("replace created a new row (%s -> %s)", firstID, sameID)
	}
}

// ---------------------------------------------------------------------------
// DELETE
// ---------------------------------------------------------------------------

func TestDeleteReportScheduleIsIdempotent(t *testing.T) {
	f := newReportFixture(t, "scheddelete", "scheddelete", "SC5")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"UTC"}`)

	if w := deleteScheduleRequest(t, f.owner, f.artifactID, pinID); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d: %s", w.Code, w.Body.String())
	}
	if n := countSchedules(t, pinID); n != 0 {
		t.Fatalf("%d schedules survive the delete", n)
	}
	// Deleting what is already gone is the same answer: the caller's intent is
	// satisfied, and a retried request must not look like a failure.
	if w := deleteScheduleRequest(t, f.owner, f.artifactID, pinID); w.Code != http.StatusNoContent {
		t.Fatalf("second delete: status %d, want 204: %s", w.Code, w.Body.String())
	}
	// The report itself is untouched — stopping the robot is not unpublishing.
	if w := getReportRequest(t, f.owner, pinID); w.Code != http.StatusOK {
		t.Fatalf("the report died with its schedule: %d", w.Code)
	}
}

// Unpinning withdraws the disclosure, so the standing spend must go with it.
func TestUnpinCascadesTheSchedule(t *testing.T) {
	f := newReportFixture(t, "schedcascade", "schedcascade", "SC6")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"UTC"}`)

	if w := unpinReportRequest(t, f.owner, f.artifactID, pinID); w.Code != http.StatusNoContent {
		t.Fatalf("unpin: %d %s", w.Code, w.Body.String())
	}
	if n := countSchedules(t, pinID); n != 0 {
		t.Fatalf("%d schedules survive the pin they belonged to", n)
	}
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// Members see the cadence — that is how "how fresh is this report" is answered
// without opening it — even though only the owner can change it.
func TestReportsCarryTheirSchedule(t *testing.T) {
	f := newReportFixture(t, "schedread", "schedread", "SC7")
	teammate := newAssistantTestUser(t, "assistant-schedule-mate@agora.dev")
	addAssistantTestMember(t, f.workspace, teammate, "member")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	// Before any cadence: the key is absent, not null — a report that does not
	// refresh itself must be indistinguishable from the 2a shape.
	w := listProjectReportsRequest(t, teammate, f.workspace, f.project)
	var list struct {
		Reports []map[string]any `json:"reports"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, w.Body.String())
	}
	if len(list.Reports) != 1 {
		t.Fatalf("%d reports, want 1", len(list.Reports))
	}
	if _, present := list.Reports[0]["schedule"]; present {
		t.Fatalf("an unscheduled report carries a schedule key: %v", list.Reports[0])
	}

	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"weekdays","time":"07:15","timezone":"Asia/Tashkent"}`)

	w = listProjectReportsRequest(t, teammate, f.workspace, f.project)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, w.Body.String())
	}
	schedule, ok := list.Reports[0]["schedule"].(map[string]any)
	if !ok {
		t.Fatalf("the list row carries no schedule: %v", list.Reports[0])
	}
	if schedule["frequency"] != "weekdays" || schedule["time"] != "07:15" || schedule["timezone"] != "Asia/Tashkent" {
		t.Fatalf("list schedule = %v", schedule)
	}
	if schedule["next_run_at"] == "" || schedule["next_run_at"] == nil {
		t.Fatalf("list schedule has no next run: %v", schedule)
	}

	// The single read answers the same question beside the body.
	w = getReportRequest(t, teammate, pinID)
	if w.Code != http.StatusOK {
		t.Fatalf("read: status %d: %s", w.Code, w.Body.String())
	}
	var single map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &single); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	readSchedule, ok := single["schedule"].(map[string]any)
	if !ok {
		t.Fatalf("the report read carries no schedule: %v", single)
	}
	if readSchedule["frequency"] != "weekdays" {
		t.Fatalf("read schedule = %v", readSchedule)
	}
}

// ---------------------------------------------------------------------------
// WS events
// ---------------------------------------------------------------------------

// The cadence badge lives on a page the owner is not looking at, so setting and
// clearing a schedule has to announce itself — workspace-scoped, ids only, like
// every other report event.
func TestReportScheduleChangeEmitsAWorkspaceEvent(t *testing.T) {
	f := newReportFixture(t, "schedevents", "schedevents", "SC8")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	changed := recordBusEvents(t, protocol.EventReportScheduleChanged)

	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"UTC"}`)

	assertScheduleEvent := func(want int) {
		t.Helper()
		seen := changed()
		if len(seen) != want {
			t.Fatalf("report:schedule_changed fired %d times, want %d", len(seen), want)
		}
		last := seen[len(seen)-1]
		if last.WorkspaceID != f.workspace {
			t.Fatalf("event went to workspace %s, want %s", last.WorkspaceID, f.workspace)
		}
		payload, ok := last.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload is not an object: %v", last.Payload)
		}
		if payload["pin_id"] != pinID || payload["project_id"] != f.project || payload["artifact_id"] != f.artifactID {
			t.Fatalf("payload = %v", payload)
		}
		for _, leaked := range []string{"content", "title", "session_id", "frequency"} {
			if _, bad := payload[leaked]; bad {
				t.Fatalf("payload carries %q: %v", leaked, payload)
			}
		}
	}
	assertScheduleEvent(1)

	if w := deleteScheduleRequest(t, f.owner, f.artifactID, pinID); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	assertScheduleEvent(2)

	// A delete that removed nothing changed nothing, so it announces nothing —
	// the same rule the idempotent pin create follows.
	if w := deleteScheduleRequest(t, f.owner, f.artifactID, pinID); w.Code != http.StatusNoContent {
		t.Fatalf("second delete: %d", w.Code)
	}
	if seen := changed(); len(seen) != 2 {
		t.Fatalf("a no-op delete announced itself: %d events", len(seen))
	}
}

// ---------------------------------------------------------------------------
// The ticker
// ---------------------------------------------------------------------------

// The happy path, end to end through the real claim: a due slot starts an
// ordinary assistant run in the owner's own session, and the slot moves forward
// before the run starts so nothing can serve it twice.
func TestRunDueReportSchedulesStartsARefresh(t *testing.T) {
	f := newReportFixture(t, "schedrun", "schedrun", "SC9")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"Asia/Tashkent"}`)
	withBlockingAssistantSession(t, f.session)

	due := makeScheduleDue(t, pinID)
	testHandler.RunDueReportSchedules(context.Background())

	row := readScheduleRow(t, pinID)
	if row.lastStatus != reportScheduleStatusOK {
		t.Fatalf("last_status = %q (%s), want ok", row.lastStatus, row.lastError)
	}
	// Claimed BEFORE the run: the slot has already moved on.
	if !row.nextRunAt.After(due) || !row.nextRunAt.After(time.Now()) {
		t.Fatalf("next_run_at %s was not advanced past the claimed slot %s",
			row.nextRunAt.Format(time.RFC3339), due.Format(time.RFC3339))
	}

	// The refresh is an ORDINARY turn in the owner's own conversation.
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM assistant_message WHERE session_id = $1 AND role = 'user'
		 ORDER BY created_at DESC LIMIT 1`, f.session).Scan(&content); err != nil {
		t.Fatalf("read the synthetic message: %v", err)
	}
	for _, want := range []string{"Scheduled refresh", f.artifactID, "Sprint report", "update_artifact", "never create_artifact"} {
		if !strings.Contains(content, want) {
			t.Fatalf("the synthetic message does not mention %q: %s", want, content)
		}
	}
	// ...and it is a real run, owned by the report's owner.
	var runs int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM assistant_run WHERE session_id = $1 AND user_id = $2`,
		f.session, f.owner).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 1 {
		t.Fatalf("%d runs started, want 1", runs)
	}

	// A second tick finds nothing due — the claim is what makes a slot
	// at-most-once, not the run's own success.
	testHandler.RunDueReportSchedules(context.Background())
	if n := countSessionUserMessages(t, f.session); n != 1 {
		t.Fatalf("%d user messages after two ticks, want 1", n)
	}
}

// The owner's own conversation always wins: a busy session is skipped, recorded
// as such, and never gets a second concurrent run.
func TestRunDueReportSchedulesSkipsABusySession(t *testing.T) {
	f := newReportFixture(t, "schedbusy", "schedbusy", "SD1")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"Asia/Tashkent"}`)
	svc := withBlockingAssistantSession(t, f.session)

	// The owner is mid-conversation when the slot comes due.
	w := httptest.NewRecorder()
	testHandler.SendAssistantMessage(w, withURLParam(
		newAssistantRequest(http.MethodPost, "/x", f.owner, `{"content":"what is on my plate?"}`), "id", f.session))
	if w.Code != http.StatusAccepted {
		t.Fatalf("seed message: %d %s", w.Code, w.Body.String())
	}
	if !svc.HasActiveRun(f.session) {
		t.Fatalf("the seeded run is not active, so the test proves nothing")
	}

	due := makeScheduleDue(t, pinID)
	testHandler.RunDueReportSchedules(context.Background())

	row := readScheduleRow(t, pinID)
	if row.lastStatus != reportScheduleStatusSkipped {
		t.Fatalf("last_status = %q (%s), want skipped", row.lastStatus, row.lastError)
	}
	// A skipped slot is still a CLAIMED slot: it waits for the next one rather
	// than retrying in a minute.
	if !row.nextRunAt.After(due) {
		t.Fatalf("a skipped slot was not advanced: %s", row.nextRunAt.Format(time.RFC3339))
	}
	if n := countSessionUserMessages(t, f.session); n != 1 {
		t.Fatalf("%d user messages, want only the owner's own", n)
	}
}

// The kill switch is read every tick and does its work before any query, so an
// operator can stop all unattended spend without a redeploy.
func TestRunDueReportSchedulesRespectsTheKillSwitch(t *testing.T) {
	f := newReportFixture(t, "schedoff", "schedoff", "SD2")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"Asia/Tashkent"}`)
	withBlockingAssistantSession(t, f.session)
	t.Setenv("AGORA_ASSISTANT_SCHEDULES_ENABLED", "false")

	due := makeScheduleDue(t, pinID)
	testHandler.RunDueReportSchedules(context.Background())

	row := readScheduleRow(t, pinID)
	if row.lastStatus != "" {
		t.Fatalf("a disabled scheduler ran a slot: %q", row.lastStatus)
	}
	if !row.nextRunAt.Equal(due) {
		t.Fatalf("a disabled scheduler claimed a slot: %s", row.nextRunAt.Format(time.RFC3339))
	}
	if n := countSessionUserMessages(t, f.session); n != 0 {
		t.Fatalf("%d messages sent with the scheduler off", n)
	}
}

// A disabled cadence is invisible to the ticker: pausing must not mean
// "runs every minute the moment it is switched back on" either.
func TestRunDueReportSchedulesIgnoresDisabledRows(t *testing.T) {
	f := newReportFixture(t, "scheddisabled", "scheddisabled", "SD3")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	putTestSchedule(t, f.owner, f.artifactID, pinID,
		`{"frequency":"daily","time":"09:00","timezone":"Asia/Tashkent"}`)
	withBlockingAssistantSession(t, f.session)

	due := makeScheduleDue(t, pinID)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE assistant_report_schedule SET enabled = false WHERE pin_id = $1`, pinID); err != nil {
		t.Fatalf("disable schedule: %v", err)
	}

	testHandler.RunDueReportSchedules(context.Background())

	row := readScheduleRow(t, pinID)
	if row.lastStatus != "" || !row.nextRunAt.Equal(due) {
		t.Fatalf("a disabled cadence ran: %+v", row)
	}
	if n := countSessionUserMessages(t, f.session); n != 0 {
		t.Fatalf("%d messages sent for a disabled cadence", n)
	}
}
