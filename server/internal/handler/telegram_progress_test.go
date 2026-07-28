package handler

import (
	"testing"
	"time"
)

func TestProgressHeadlineIsNotLineAnchored(t *testing.T) {
	// Agents write the marker mid-sentence often enough that requiring it at
	// the start of a line drops exactly the updates worth relaying — the same
	// mistake already made once with the QA phase markers.
	cases := map[string]string{
		"PROGRESS: importing 412 tasks":                    "importing 412 tasks",
		"Fetched the window. PROGRESS: aggregating by tag": "aggregating by tag",
		"a\nb\nPROGRESS: still running\nmore text":         "still running",
		"  PROGRESS:   padded  ":                           "padded",
	}
	for in, want := range cases {
		if got := progressHeadline(in); got != want {
			t.Errorf("progressHeadline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProgressHeadlineIgnoresUnmarkedText(t *testing.T) {
	for _, in := range []string{"", "just some output", "progress: lowercase is not the marker"} {
		if got := progressHeadline(in); got != "" {
			t.Errorf("progressHeadline(%q) = %q, want empty", in, got)
		}
	}
}

func TestProgressStaysSilentOnAShortRun(t *testing.T) {
	// A run that finishes quickly should produce exactly one message: its
	// report. Narrating a four-minute run is how a group learns to ignore the
	// channel.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	if s.shouldPost("run-1", "working", started, started.Add(30*time.Second)) {
		t.Fatal("posted before the start delay")
	}
	if !s.shouldPost("run-1", "working", started, started.Add(telegramProgressStartDelay+time.Second)) {
		t.Fatal("did not post once the run was long enough")
	}
}

func TestProgressSuppressesRepeatsAndFloods(t *testing.T) {
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	base := started.Add(telegramProgressStartDelay + time.Second)
	if !s.shouldPost("run-1", "step one", started, base) {
		t.Fatal("first post was suppressed")
	}
	// Same headline again is not news.
	if s.shouldPost("run-1", "step one", started, base.Add(telegramProgressInterval+time.Minute)) {
		t.Error("an unchanged headline was posted again")
	}
	// A new headline still waits for the interval.
	if s.shouldPost("run-1", "step two", started, base.Add(time.Minute)) {
		t.Error("posted inside the throttle interval")
	}
	if !s.shouldPost("run-1", "step two", started, base.Add(telegramProgressInterval+time.Second)) {
		t.Error("a changed headline was suppressed past the interval")
	}
}

func TestProgressStateIsPerRun(t *testing.T) {
	// Two autopilots running at once must not throttle each other.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)
	if !s.shouldPost("run-1", "x", started, at) || !s.shouldPost("run-2", "x", started, at) {
		t.Fatal("one run's post suppressed another's")
	}
}

func TestForgetDropsRunState(t *testing.T) {
	// Without this the map grows for the life of the process.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)
	s.shouldPost("run-1", "x", started, at)
	s.forget("run-1")
	if _, ok := s.last["run-1"]; ok {
		t.Fatal("run state survived forget")
	}
}
