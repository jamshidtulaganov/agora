package handler

import (
	"strings"
	"testing"
)

func TestTruncateForTelegram(t *testing.T) {
	t.Run("short text passes through untouched", func(t *testing.T) {
		in := "Weekly report\n\nBacklog grew by 12."
		if got := truncateForTelegram(in); got != in {
			t.Errorf("got %q, want the input unchanged", got)
		}
	})

	t.Run("exactly at the limit is not trimmed", func(t *testing.T) {
		in := strings.Repeat("x", telegramMessageLimit)
		if got := truncateForTelegram(in); got != in {
			t.Errorf("len %d was trimmed; the limit is inclusive", len(got))
		}
	})

	t.Run("over the limit fits within the Bot API cap", func(t *testing.T) {
		// A real report: many lines, so a clean break exists near the cut.
		in := strings.Repeat("a line of the weekly report\n", 400)
		got := truncateForTelegram(in)
		if len(got) > telegramMessageLimit {
			t.Fatalf("len %d exceeds the %d cap — Telegram would reject the whole message", len(got), telegramMessageLimit)
		}
		if !strings.Contains(got, "trimmed") {
			t.Error("a cut message must say so, or a partial report reads as complete")
		}
		if strings.HasSuffix(strings.TrimSpace(strings.Split(got, "…")[0]), "of the weekly rep") {
			t.Error("cut mid-word despite a nearby newline")
		}
	})

	t.Run("no newlines near the cut still fits", func(t *testing.T) {
		// Pathological: one enormous line. The newline-seek must not fire and
		// must not blow the budget either.
		in := strings.Repeat("x", telegramMessageLimit*2)
		got := truncateForTelegram(in)
		if len(got) > telegramMessageLimit {
			t.Fatalf("len %d exceeds cap on a newline-free report", len(got))
		}
	})

	t.Run("multibyte text stays within the byte cap", func(t *testing.T) {
		// Reports are written in the issue's language — Cyrillic and Uzbek text
		// is multi-byte, and the Bot API limit that matters here is on length.
		in := strings.Repeat("Отчёт по задачам за неделю\n", 400)
		got := truncateForTelegram(in)
		if len(got) > telegramMessageLimit {
			t.Fatalf("len %d exceeds cap for multibyte text", len(got))
		}
	})
}

// Stranded-output recovery: a completed run whose agent left no postable
// comment is a report that failed to reach anyone, not deliberate silence.
func TestAutopilotRunOutput(t *testing.T) {
	t.Run("recovers the agent's output", func(t *testing.T) {
		got := autopilotRunOutput([]byte(`{"output":"Haftalik hisobot\n\nBacklog +41."}`))
		if got != "Haftalik hisobot\n\nBacklog +41." {
			t.Errorf("got %q", got)
		}
	})

	t.Run("unescapes literal backslash-n the same way the comment path does", func(t *testing.T) {
		got := autopilotRunOutput([]byte(`{"output":"line one\\nline two"}`))
		if got != "line one\nline two" {
			t.Errorf("got %q, want a real newline", got)
		}
	})

	t.Run("nothing to recover yields empty, not garbage", func(t *testing.T) {
		for _, in := range [][]byte{nil, {}, []byte(`{}`), []byte(`not json`), []byte(`{"output":"   "}`)} {
			if got := autopilotRunOutput(in); got != "" {
				t.Errorf("input %q recovered %q, want empty", in, got)
			}
		}
	})
}
