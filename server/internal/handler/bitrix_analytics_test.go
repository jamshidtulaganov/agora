package handler

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
)

// Bitrix STATUS is numeric and a portal can introduce codes we have never seen.
// An unknown code must count as OPEN, not silently drop out of the totals —
// created/completed/open have to reconcile or the backlog number is a lie.
func TestBitrixStatusBucket(t *testing.T) {
	cases := map[string]string{
		"5":   "completed",
		"4":   "awaiting_control",
		"6":   "deferred",
		"7":   "declined",
		"1":   "open",
		"2":   "open",
		"3":   "open",
		"":    "open",
		"99":  "open",      // unknown future code
		" 5 ": "completed", // whitespace from the portal
	}
	for status, want := range cases {
		if got := bitrixStatusBucket(status); got != want {
			t.Errorf("bitrixStatusBucket(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestSortedBuckets(t *testing.T) {
	counts := map[string]int{"a": 3, "b": 9, "c": 3}
	labels := map[string]string{"b": "Bee"}

	t.Run("by count, ties broken by key", func(t *testing.T) {
		got := sortedBuckets(counts, labels, false)
		if got[0].Key != "b" || got[0].Count != 9 || got[0].Label != "Bee" {
			t.Fatalf("highest count should lead with its label: %+v", got[0])
		}
		if got[1].Key != "a" || got[2].Key != "c" {
			t.Errorf("equal counts must fall back to key order: %+v", got)
		}
	})

	t.Run("by key for chronological series", func(t *testing.T) {
		months := map[string]int{"2026-03": 1, "2026-01": 50, "2026-02": 7}
		got := sortedBuckets(months, nil, true)
		if got[0].Key != "2026-01" || got[1].Key != "2026-02" || got[2].Key != "2026-03" {
			t.Errorf("month series must stay chronological regardless of count: %+v", got)
		}
	})

	t.Run("empty map yields an empty slice, not nil", func(t *testing.T) {
		if got := sortedBuckets(map[string]int{}, nil, false); got == nil || len(got) != 0 {
			t.Errorf("got %#v, want empty non-nil slice (JSON [] not null)", got)
		}
	})
}

// The portal's timestamp format varies; a parse failure must not be mistaken
// for a real date, or a task would land in the wrong month.
func TestBitrixParseTime(t *testing.T) {
	for _, in := range []string{
		"2026-01-14T09:12:03+05:00",
		"2026-01-14 09:12:03",
		"2026-01-14T09:12:03",
	} {
		if _, ok := bitrix.ParseTime(in); !ok {
			t.Errorf("ParseTime(%q) failed, want parsed", in)
		}
	}
	for _, in := range []string{"", "   ", "not a date", "14.01.2026"} {
		if ts, ok := bitrix.ParseTime(in); ok {
			t.Errorf("ParseTime(%q) = %v, want not-ok", in, ts)
		}
	}
	got, ok := bitrix.ParseTime("2026-04-02T00:00:00Z")
	if !ok || got.Month() != time.April || got.Day() != 2 {
		t.Errorf("parsed to %v, want 2 April", got)
	}
}

// The first live weekly report could not state a week-over-week change: the
// endpoint only accepted `since`, so a PAST window was unreachable and the
// agent correctly reported the delta as unavailable. untilLabel is the visible
// half of that fix — a bounded request must not be labelled as ending today.
func TestUntilLabel(t *testing.T) {
	now := time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)

	if got := untilLabel(time.Time{}, now); got != "2026-07-27" {
		t.Errorf("unbounded window = %q, want today", got)
	}
	bounded := time.Date(2026, 7, 20, 23, 59, 59, 0, time.UTC)
	if got := untilLabel(bounded, now); got != "2026-07-20" {
		t.Errorf("bounded window = %q, want its own end date — labelling it today would pass a historical window off as current", got)
	}
}
