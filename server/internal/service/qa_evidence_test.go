package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestParseQAResultBlock(t *testing.T) {
	t.Run("valid block amid prose", func(t *testing.T) {
		content := "## QA verdict\n\nAll new tests pass; 1 pre-existing failure ignored.\n\n" +
			"```qa-result\n" +
			`{"verdict":"fail","summary":"1 new failure","commands":[{"cmd":"go test ./...","baseline_exit":0,"branch_exit":1,"kind":"new_failure"}],"screenshots":["/var/www/x.png"]}` +
			"\n```\n"
		raw, p, ok := parseQAResultBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if p.Verdict != "fail" {
			t.Errorf("verdict = %q, want fail", p.Verdict)
		}
		if p.Summary != "1 new failure" {
			t.Errorf("summary = %q", p.Summary)
		}
		if len(p.Commands) != 1 {
			t.Errorf("commands len = %d, want 1", len(p.Commands))
		}
		if raw == "" {
			t.Error("raw should be the verbatim JSON")
		}
	})

	t.Run("no block", func(t *testing.T) {
		if _, _, ok := parseQAResultBlock("just a normal comment, no fenced block"); ok {
			t.Error("expected ok=false when no qa-result block")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		content := "```qa-result\n{not valid json}\n```"
		if _, _, ok := parseQAResultBlock(content); ok {
			t.Error("expected ok=false on malformed JSON")
		}
	})

	t.Run("invalid verdict downgrades to skip", func(t *testing.T) {
		content := "```qa-result\n" + `{"verdict":"maybe","summary":"x"}` + "\n```"
		if _, _, ok := parseQAResultBlock(content); ok {
			t.Error("expected ok=false on a verdict that is neither pass nor fail")
		}
	})

	t.Run("pass verdict", func(t *testing.T) {
		content := "```qa-result\n" + `{"verdict":"pass","summary":"all green","commands":[],"screenshots":[]}` + "\n```"
		_, p, ok := parseQAResultBlock(content)
		if !ok || p.Verdict != "pass" {
			t.Errorf("ok=%v verdict=%q, want ok=true pass", ok, p.Verdict)
		}
	})
}

// Phase timings are TELEMETRY, never a gate. The parser must read them when
// present, tolerate them absent (older agents / an older pinned template), and
// never reject a verdict because of them.
func TestParseQAResultBlockPhases(t *testing.T) {
	t.Run("phases parse, including a skipped baseline", func(t *testing.T) {
		content := "```qa-result\n" + `{"verdict":"pass","summary":"green","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}],"screenshots":[],` +
			`"phases":[` +
			`{"phase":"checks","started_at":"2026-07-27T10:00:00Z","finished_at":"2026-07-27T10:04:00Z"},` +
			`{"phase":"baseline","skipped":true,"note":"every branch command passed"},` +
			`{"phase":"smoke","started_at":"2026-07-27T10:04:00Z","finished_at":"2026-07-27T10:05:30Z"}` +
			`]}` + "\n```"
		raw, p, ok := parseQAResultBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		phases := p.PhaseTimings()
		if len(phases) != 3 {
			t.Fatalf("phases len = %d, want 3", len(phases))
		}
		if phases[0].Phase != "checks" || phases[0].StartedAt == "" || phases[0].FinishedAt == "" {
			t.Errorf("checks phase malformed: %+v", phases[0])
		}
		// The datapoint the whole field exists for.
		if phases[1].Phase != "baseline" || !phases[1].Skipped || phases[1].Note == "" {
			t.Errorf("skipped baseline phase malformed: %+v", phases[1])
		}
		if phases[1].StartedAt != "" || phases[1].FinishedAt != "" {
			t.Errorf("a skipped phase must carry no timestamps: %+v", phases[1])
		}
		// result_json is stored verbatim, so the phases must survive into the row
		// without any schema change.
		if !strings.Contains(raw, `"phases"`) {
			t.Error("raw payload must carry phases through to qa_evidence.result_json")
		}
	})

	t.Run("absent phases is not an error", func(t *testing.T) {
		content := "```qa-result\n" + `{"verdict":"pass","summary":"green","commands":[],"screenshots":[]}` + "\n```"
		_, p, ok := parseQAResultBlock(content)
		if !ok {
			t.Fatal("a verdict without phases must still capture")
		}
		if len(p.PhaseTimings()) != 0 {
			t.Errorf("phases = %+v, want empty", p.PhaseTimings())
		}
	})

	t.Run("malformed phases must not cost the agent its verdict", func(t *testing.T) {
		// Telemetry is never a gate. A wrong-typed phases value must still
		// capture the verdict; only the timings are dropped.
		for _, bad := range []string{`"not-an-array"`, `42`, `{"phase":"checks"}`, `[1,2,3]`} {
			content := "```qa-result\n" + `{"verdict":"pass","summary":"x","commands":[],"phases":` + bad + `}` + "\n```"
			raw, p, ok := parseQAResultBlock(content)
			if !ok {
				t.Fatalf("phases=%s cost the agent its verdict", bad)
			}
			if p.Verdict != "pass" {
				t.Errorf("phases=%s: verdict = %q, want pass", bad, p.Verdict)
			}
			if got := p.PhaseTimings(); got != nil {
				t.Errorf("phases=%s: timings = %+v, want nil", bad, got)
			}
			// The raw payload still reaches result_json, so the bad value stays
			// inspectable in the row.
			if !strings.Contains(raw, "phases") {
				t.Errorf("phases=%s: raw lost the field", bad)
			}
		}
	})
}

func TestCheckPhaseTimings(t *testing.T) {
	dispatch := time.Date(2026, 7, 27, 9, 24, 38, 0, time.UTC)
	captured := time.Date(2026, 7, 27, 9, 31, 30, 0, time.UTC)
	at := func(h, m, s int) string {
		return time.Date(2026, 7, 27, h, m, s, 0, time.UTC).Format(time.RFC3339)
	}

	t.Run("real clock reads are trusted", func(t *testing.T) {
		got := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(9, 25, 11), FinishedAt: at(9, 27, 3)},
			{Phase: "baseline", Skipped: true, Note: "every branch command passed"},
			{Phase: "smoke", StartedAt: at(9, 27, 3), FinishedAt: at(9, 29, 41)},
		}, dispatch, true, captured)
		if got.Trust != "ok" {
			t.Errorf("trust = %q (%s), want ok", got.Trust, got.Reason)
		}
	})

	// The live EED-221 run: every boundary on an exact minute, every phase
	// exactly 120s. Plausible window, obviously reconstructed.
	t.Run("all-round boundaries read as estimated", func(t *testing.T) {
		got := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(9, 25, 0), FinishedAt: at(9, 27, 0)},
			{Phase: "smoke", StartedAt: at(9, 27, 0), FinishedAt: at(9, 29, 0)},
			{Phase: "cases", StartedAt: at(9, 29, 0), FinishedAt: at(9, 31, 0)},
		}, dispatch, true, captured)
		if got.Trust != "estimated" {
			t.Errorf("trust = %q (%s), want estimated", got.Trust, got.Reason)
		}
	})

	t.Run("a phase starting before dispatch is implausible", func(t *testing.T) {
		got := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(9, 10, 17), FinishedAt: at(9, 27, 3)},
		}, dispatch, true, captured)
		if got.Trust != "implausible" {
			t.Errorf("trust = %q, want implausible", got.Trust)
		}
	})

	t.Run("a phase ending after capture is implausible", func(t *testing.T) {
		got := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(9, 25, 11), FinishedAt: at(9, 59, 0)},
		}, dispatch, true, captured)
		if got.Trust != "implausible" {
			t.Errorf("trust = %q, want implausible", got.Trust)
		}
	})

	t.Run("a negative duration is implausible", func(t *testing.T) {
		got := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(9, 27, 0), FinishedAt: at(9, 25, 0)},
		}, dispatch, true, captured)
		if got.Trust != "implausible" {
			t.Errorf("trust = %q, want implausible", got.Trust)
		}
	})

	t.Run("only skipped phases is absent, not a failure", func(t *testing.T) {
		got := checkPhaseTimings([]qaResultPhase{
			{Phase: "baseline", Skipped: true, Note: "every branch command passed"},
		}, dispatch, true, captured)
		if got.Trust != "absent" {
			t.Errorf("trust = %q, want absent", got.Trust)
		}
	})

	// Without a dispatch comment there is no lower bound to check against; the
	// upper bound and internal consistency still apply.
	t.Run("no dispatch comment still checks the capture bound", func(t *testing.T) {
		ok := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(8, 0, 13), FinishedAt: at(9, 27, 3)},
		}, time.Time{}, false, captured)
		if ok.Trust != "ok" {
			t.Errorf("trust = %q, want ok (no lower bound to violate)", ok.Trust)
		}
		bad := checkPhaseTimings([]qaResultPhase{
			{Phase: "checks", StartedAt: at(9, 25, 11), FinishedAt: at(10, 30, 0)},
		}, time.Time{}, false, captured)
		if bad.Trust != "implausible" {
			t.Errorf("trust = %q, want implausible", bad.Trust)
		}
	})
}

func TestAnnotatePhaseCheck(t *testing.T) {
	raw := `{"verdict":"pass","summary":"x","phases":[{"phase":"checks"}]}`
	out := annotatePhaseCheck(raw, phaseTimingCheck{Trust: "estimated", Reason: "rounded"})

	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("annotated payload is not valid JSON: %v", err)
	}
	if obj["verdict"] != "pass" || obj["phases"] == nil {
		t.Error("annotation must preserve the agent's own fields")
	}
	ann, ok := obj["_phase_timing"].(map[string]any)
	if !ok || ann["trust"] != "estimated" {
		t.Errorf("_phase_timing = %v, want trust=estimated", obj["_phase_timing"])
	}

	// Fails open: unparseable input is stored as-is rather than lost.
	if got := annotatePhaseCheck("{not json", phaseTimingCheck{Trust: "ok"}); got != "{not json" {
		t.Errorf("bad raw must pass through unchanged, got %q", got)
	}
}

func TestDerivePhasesFromStream(t *testing.T) {
	base := time.Date(2026, 7, 27, 9, 25, 11, 0, time.UTC)
	msg := func(offsetSec int, content string) db.TaskMessage {
		return db.TaskMessage{
			Content:   pgtype.Text{String: content, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: base.Add(time.Duration(offsetSec) * time.Second), Valid: true},
		}
	}
	ended := base.Add(370 * time.Second)

	t.Run("rebuilds windows from message timestamps", func(t *testing.T) {
		got := derivePhasesFromStream([]db.TaskMessage{
			msg(0, "PHASE: checks"),
			msg(30, "some ordinary output, no marker"),
			msg(112, "PHASE: baseline skipped — every branch command passed"),
			msg(113, "PHASE: smoke"),
			msg(298, "PHASE: cases"),
		}, ended)

		if len(got) != 4 {
			t.Fatalf("phases = %d, want 4: %+v", len(got), got)
		}
		// checks runs until the next marker, not until the end of the task.
		if got[0].Phase != "checks" || got[0].StartedAt != base.Format(time.RFC3339) ||
			got[0].FinishedAt != base.Add(112*time.Second).Format(time.RFC3339) {
			t.Errorf("checks window wrong: %+v", got[0])
		}
		// A skipped phase carries a reason and NO window.
		if !got[1].Skipped || got[1].StartedAt != "" || got[1].FinishedAt != "" {
			t.Errorf("skipped baseline must carry no window: %+v", got[1])
		}
		if !strings.Contains(got[1].Note, "every branch command passed") {
			t.Errorf("skipped note lost: %q", got[1].Note)
		}
		// The last phase closes at the supplied end.
		if got[3].Phase != "cases" || got[3].FinishedAt != ended.Format(time.RFC3339) {
			t.Errorf("last phase must close at endedAt: %+v", got[3])
		}
	})

	t.Run("the timestamps come from the stream, not from anything the agent wrote", func(t *testing.T) {
		// The agent claims a wildly wrong time INSIDE the marker line; the
		// derivation must ignore the text and use the message's own clock.
		got := derivePhasesFromStream([]db.TaskMessage{
			msg(0, "PHASE: checks started_at 1999-01-01T00:00:00Z"),
			msg(60, "PHASE: smoke"),
		}, ended)
		if got[0].StartedAt != base.Format(time.RFC3339) {
			t.Errorf("started_at = %q, want the message timestamp", got[0].StartedAt)
		}
	})

	t.Run("no markers yields nothing so the caller can fall back", func(t *testing.T) {
		if got := derivePhasesFromStream([]db.TaskMessage{msg(0, "just working")}, ended); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("unknown phase names are ignored", func(t *testing.T) {
		if got := derivePhasesFromStream([]db.TaskMessage{msg(0, "PHASE: deploying")}, ended); got != nil {
			t.Errorf("got %+v, want nil for an off-contract name", got)
		}
	})

	t.Run("markers are matched case-insensitively and mid-message", func(t *testing.T) {
		got := derivePhasesFromStream([]db.TaskMessage{
			msg(0, "Running the suite now.\nphase: Checks\nmore text"),
		}, ended)
		if len(got) != 1 || got[0].Phase != "checks" {
			t.Fatalf("got %+v, want one checks phase", got)
		}
	})
}

// Regression: the first live run wrote two of its four markers MID-LINE, after
// a sentence. A line-anchored pattern dropped both — including the skipped
// baseline, which is the most valuable datapoint the gate produces. Message
// bodies below are verbatim from that run (task a5c36474).
func TestDerivePhasesFromStreamMidLineMarkers(t *testing.T) {
	base := time.Date(2026, 7, 27, 10, 35, 27, 0, time.UTC)
	msg := func(sec int, content string) db.TaskMessage {
		return db.TaskMessage{
			Content:   pgtype.Text{String: content, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: base.Add(time.Duration(sec) * time.Second), Valid: true},
		}
	}
	got := derivePhasesFromStream([]db.TaskMessage{
		msg(0, "New QA run requested.\n\n```todo\n- [~] QA gate\n```\n\nPHASE: checks\n\nPROGRESS: Checking out PR branch"),
		msg(21, "15/15 tests pass. PHASE: baseline skipped — every branch command passed\n\nPHASE: smoke\n\nPROGRESS: Starting server"),
		msg(27, "PHASE: cases\n\nPROGRESS: Running 3 defined case scripts"),
		msg(42, "All 3 case scripts pass. PHASE: materialize skipped — developer committed equivalent tests in server.test.js"),
	}, base.Add(72*time.Second))

	byPhase := map[string]qaResultPhase{}
	for _, ph := range got {
		byPhase[ph.Phase] = ph
	}
	for _, want := range []string{"checks", "baseline", "smoke", "cases", "materialize"} {
		if _, ok := byPhase[want]; !ok {
			t.Fatalf("phase %q missing — mid-line markers dropped again: %+v", want, got)
		}
	}
	// The two the old pattern lost, both mid-line, both skipped.
	if !byPhase["baseline"].Skipped || !strings.Contains(byPhase["baseline"].Note, "every branch command passed") {
		t.Errorf("baseline skip signal lost: %+v", byPhase["baseline"])
	}
	if !byPhase["materialize"].Skipped {
		t.Errorf("materialize skip lost: %+v", byPhase["materialize"])
	}
	// checks still closes at the NEXT marker (baseline, mid-line at +21s).
	if byPhase["checks"].FinishedAt != base.Add(21*time.Second).Format(time.RFC3339) {
		t.Errorf("checks window wrong: %+v", byPhase["checks"])
	}
	// smoke shares its message with the skipped baseline, so it opens at the
	// same instant and closes at the next marker.
	if byPhase["smoke"].StartedAt != base.Add(21*time.Second).Format(time.RFC3339) ||
		byPhase["smoke"].FinishedAt != base.Add(27*time.Second).Format(time.RFC3339) {
		t.Errorf("smoke window wrong: %+v", byPhase["smoke"])
	}
}

func TestDerivePhasesFromStreamFirstAnnouncementWins(t *testing.T) {
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	msg := func(sec int, content string) db.TaskMessage {
		return db.TaskMessage{
			Content:   pgtype.Text{String: content, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: base.Add(time.Duration(sec) * time.Second), Valid: true},
		}
	}
	got := derivePhasesFromStream([]db.TaskMessage{
		msg(0, "PHASE: checks"),
		msg(60, "PHASE: cases"),
		msg(90, "Recap: PHASE: checks was clean, PHASE: cases all green"),
	}, base.Add(120*time.Second))

	if len(got) != 2 {
		t.Fatalf("a recap must not reopen closed phases: %+v", got)
	}
	if got[0].StartedAt != base.Format(time.RFC3339) {
		t.Errorf("first announcement must win: %+v", got[0])
	}
}
