package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

func TestChooseAutopilotDestinationKeepsBotAndChatPaired(t *testing.T) {
	agentBot := telegram.NewBotClient("agent-token")
	platformBot := telegram.NewBotClient("platform-token")

	bot, chat := chooseAutopilotDestination(agentBot, "-100-agent", false, platformBot, "")
	if bot != agentBot || chat != "-100-agent" {
		t.Fatalf("complete agent destination = (%p, %q), want agent pair", bot, chat)
	}

	bot, chat = chooseAutopilotDestination(agentBot, "-100-agent", true, platformBot, "-100-project")
	if bot != agentBot || chat != "-100-project" {
		t.Fatalf("reachable project override = (%p, %q), want agent/project pair", bot, chat)
	}

	bot, chat = chooseAutopilotDestination(agentBot, "-100-agent", false, platformBot, "-100-project")
	if bot != platformBot || chat != "-100-project" {
		t.Fatalf("agent without chat = (%p, %q), want platform pair", bot, chat)
	}

	bot, chat = chooseAutopilotDestination(agentBot, "-100-agent", false, nil, "-100-project")
	if bot != nil || chat != "" {
		t.Fatalf("chat without its bot = (%p, %q), want no destination", bot, chat)
	}
}

func TestRunOnlyAutopilotReportUsesRunResult(t *testing.T) {
	h := &Handler{}
	run := db.AutopilotRun{Result: []byte(`{"output":"run-only weekly report"}`)}
	got := h.autopilotRunReportBody(context.Background(), run, db.Autopilot{})
	if got != "run-only weekly report" {
		t.Fatalf("body = %q, want the run result", got)
	}
}

func TestAutopilotSpeakerAgentResolvesSquadLeader(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := t.Context()
	var leaderID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1::uuid LIMIT 1`,
		testWorkspaceID,
	).Scan(&leaderID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid)
		RETURNING id::text`,
		testWorkspaceID, "telegram destination fixture", leaderID, testUserID,
	).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1::uuid`, squadID)
	})

	got, ok := testHandler.autopilotSpeakerAgent(ctx, db.Autopilot{
		AssigneeType: "squad",
		AssigneeID:   parseUUID(squadID),
	})
	if !ok || uuidToString(got) != leaderID {
		t.Fatalf("speaker = %s, ok=%v; want leader %s", uuidToString(got), ok, leaderID)
	}
}
